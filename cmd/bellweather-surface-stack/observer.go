package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const defaultObservationTimeout = 2 * time.Second

var (
	errTurnNotMaterialized   = errors.New("accepted turn has not materialized")
	errAcceptedTurnInvariant = errors.New("accepted turn state is malformed")
	errFailureStateInvariant = errors.New("turn failure state is malformed")
	errCasePhaseInvariant    = errors.New("case phase state is malformed")
)

type turnObservation struct {
	Phase           vocabulary.TurnPhase
	PhaseRecordedAt time.Time
	HintProof       hintProof
	Failure         *diagnosticFailure
}

// diagnosticFailure is deliberately only the two closed values safe to expose.
// The referenced FailureDetail.Message is never copied onto this surface.
type diagnosticFailure struct {
	Reason              vocabulary.FailureReason        `json:"reason"`
	Class               content.FailureClass            `json:"class"`
	AuthorizationReason *vocabulary.AuthorizationReason `json:"authorization_reason"`
}

type hintProof struct {
	Proved           bool   `json:"proved"`
	CaseDecisionKind string `json:"case_decision_kind,omitempty"`
	TriggerKind      string `json:"trigger_kind,omitempty"`
	TriggerSource    string `json:"trigger_source,omitempty"`
}

type observer interface {
	observeTurn(context.Context, string) (turnObservation, error)
	casePhase(context.Context, string) (vocabulary.CasePhase, error)
}

type entityReader interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
}

type failureDetailReader interface {
	GetFailureDetail(context.Context, content.Ref) (*content.FailureDetail, error)
}

type productionObserver struct {
	graph   entityReader
	details failureDetailReader
}

func newProductionObserver(
	ctx context.Context,
	client *natsclient.Client,
	contentBucket string,
) (*productionObserver, error) {
	store, err := graphio.NewStore(client)
	if err != nil {
		return nil, err
	}
	backend, err := content.NewObjectStore(ctx, client, content.WithBucket(contentBucket))
	if err != nil {
		return nil, err
	}
	details, err := content.NewStore(backend)
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	return &productionObserver{graph: store, details: details}, nil
}

// Close releases the content reader owned by the production observer.
func (o *productionObserver) Close() error {
	if details, ok := o.details.(*content.Store); ok {
		return details.Close()
	}
	return nil
}

func (o *productionObserver) observeTurn(ctx context.Context, id string) (turnObservation, error) {
	state, err := o.graph.GetEntity(ctx, id)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return turnObservation{}, fmt.Errorf("%w: turn entity is missing", errTurnNotMaterialized)
	case err != nil:
		return turnObservation{}, err
	}
	phase, recordedAt, err := exactTurnPhase(state)
	if err != nil {
		return turnObservation{}, fmt.Errorf("%w: turn phase is malformed", errAcceptedTurnInvariant)
	}
	failure, err := o.failureFrom(ctx, id, state, phase)
	if err != nil {
		return turnObservation{}, err
	}
	return turnObservation{
		Phase:           phase,
		PhaseRecordedAt: recordedAt,
		HintProof:       hintProofFrom(state),
		Failure:         failure,
	}, nil
}

func (o *productionObserver) failureFrom(
	ctx context.Context,
	turnEntityID string,
	state *graph.EntityState,
	phase vocabulary.TurnPhase,
) (*diagnosticFailure, error) {
	if phase != vocabulary.PhaseFailed {
		if hasFailureEvidence(state) {
			return nil, fmt.Errorf("%w: nonfailed turn carries failure evidence", errFailureStateInvariant)
		}
		return nil, nil
	}

	reasonText, err := exactFailureString(state, vocabulary.TurnFailureReason, true)
	if err != nil {
		return nil, fmt.Errorf("%w: reason cardinality", errFailureStateInvariant)
	}
	reason, err := vocabulary.ParseFailureReason(reasonText)
	if err != nil {
		return nil, fmt.Errorf("%w: reason is outside the closed set", errFailureStateInvariant)
	}
	refText, err := exactFailureString(state, vocabulary.TurnFailureRef, false)
	if err != nil {
		return nil, fmt.Errorf("%w: reference cardinality", errFailureStateInvariant)
	}
	if refText == "" {
		return &diagnosticFailure{Reason: reason, Class: reflessFailureClass(reason)}, nil
	}
	ref, err := content.ParseRef(refText)
	if err != nil || ref.IsZero() || o.details == nil {
		return nil, fmt.Errorf("%w: detail reference is malformed", errFailureStateInvariant)
	}
	turnID := turnEntityID[strings.LastIndex(turnEntityID, ".")+1:]
	expectedKey, err := content.KeyFor(vocabulary.TurnFailureRef, content.SubjectTurn, turnID)
	if err != nil || ref.Key != expectedKey {
		return nil, fmt.Errorf("%w: detail reference names another turn", errFailureStateInvariant)
	}
	detail, err := o.details.GetFailureDetail(ctx, ref)
	if err != nil {
		if errors.Is(err, content.ErrArtifactNotFound) || errors.Is(err, content.ErrArtifactReference) ||
			errors.Is(err, content.ErrArtifactCorrupt) {
			return nil, fmt.Errorf("%w: referenced detail is unavailable", errFailureStateInvariant)
		}
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("%w: referenced detail is missing", errFailureStateInvariant)
	}
	if err := detail.Validate(); err != nil {
		return nil, fmt.Errorf("%w: referenced detail is invalid", errFailureStateInvariant)
	}
	if detail.TurnID != turnID || detail.Reason != reason {
		return nil, fmt.Errorf("%w: referenced detail does not match the turn", errFailureStateInvariant)
	}
	class := detail.Class
	if class == "" {
		class = content.FailureClassUnknown
	} else if !failureClassMatchesReason(reason, class) {
		return nil, fmt.Errorf("%w: failure reason and class disagree", errFailureStateInvariant)
	}
	var authorizationReason *vocabulary.AuthorizationReason
	if detail.AuthorizationReason != "" {
		reason := detail.AuthorizationReason
		authorizationReason = &reason
	}
	return &diagnosticFailure{
		Reason: reason, Class: class, AuthorizationReason: authorizationReason,
	}, nil
}

func hasFailureEvidence(state *graph.EntityState) bool {
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnFailureReason.String() ||
			triple.Predicate == vocabulary.TurnFailureRef.String() {
			return true
		}
	}
	return false
}

func exactFailureString(state *graph.EntityState, predicate vocabulary.Predicate, required bool) (string, error) {
	var value string
	count := 0
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		count++
		text, ok := triple.Object.(string)
		if !ok || text == "" {
			return "", errors.New("failure evidence is not a non-empty string")
		}
		value = text
	}
	if count > 1 || (required && count != 1) {
		return "", errors.New("failure evidence has invalid cardinality")
	}
	return value, nil
}

func reflessFailureClass(reason vocabulary.FailureReason) content.FailureClass {
	switch reason {
	case vocabulary.FailurePersonaCapExhausted:
		return content.FailureClassAgentLimit
	case vocabulary.FailurePersonaLoopFailed:
		return content.FailureClassUnknown
	default:
		return content.FailureClassDeterministic
	}
}

func failureClassMatchesReason(reason vocabulary.FailureReason, class content.FailureClass) bool {
	switch reason {
	case vocabulary.FailurePersonaCapExhausted:
		return class == content.FailureClassAgentLimit
	case vocabulary.FailurePersonaLoopFailed:
		return class == content.FailureClassProviderModel ||
			class == content.FailureClassModelOutputLimit ||
			class == content.FailureClassAgentRuntime ||
			class == content.FailureClassUnknown
	default:
		return class == content.FailureClassDeterministic
	}
}

func exactTurnPhase(state *graph.EntityState) (vocabulary.TurnPhase, time.Time, error) {
	if state == nil {
		return "", time.Time{}, errors.New("turn read back as nil")
	}
	if state.IsStub() {
		return "", time.Time{}, errors.New("turn is a referential stub")
	}
	var value any
	var recordedAt time.Time
	count := 0
	for _, triple := range state.Triples {
		if triple.Predicate != vocabulary.TurnPhaseCurrent.String() {
			continue
		}
		count++
		value = triple.Object
		recordedAt = triple.Timestamp
	}
	if count != 1 {
		return "", time.Time{}, fmt.Errorf("turn holds %d phase values; want exactly one", count)
	}
	phaseText, ok := value.(string)
	if !ok || phaseText == "" {
		return "", time.Time{}, errors.New("turn phase is not a non-empty string")
	}
	phase, err := vocabulary.ParseTurnPhase(phaseText)
	if err != nil {
		return "", time.Time{}, err
	}
	if recordedAt.IsZero() {
		return "", time.Time{}, errors.New("turn phase has no recorded timestamp")
	}
	return phase, recordedAt.UTC(), nil
}

func (o *productionObserver) casePhase(ctx context.Context, id string) (vocabulary.CasePhase, error) {
	state, err := o.graph.GetEntity(ctx, id)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return "", fmt.Errorf("%w: case entity is missing", errCasePhaseInvariant)
	case err != nil:
		return "", err
	}
	phase, err := semanticCasePhase(state)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errCasePhaseInvariant, err)
	}
	return phase, nil
}

func semanticCasePhase(state *graph.EntityState) (vocabulary.CasePhase, error) {
	if state == nil {
		return "", errors.New("case read back as nil")
	}
	if state.IsStub() {
		return "", errors.New("case is a referential stub")
	}
	values := make(map[string]struct{})
	for _, triple := range state.Triples {
		if triple.Predicate != vocabulary.CaseLifecyclePhase.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok || value == "" {
			return "", errors.New("case phase is not a non-empty string")
		}
		values[value] = struct{}{}
	}
	if len(values) != 1 {
		return "", fmt.Errorf("case holds %d distinct phase values; want exactly one", len(values))
	}
	var value string
	for value = range values {
		break
	}
	return vocabulary.ParseCasePhase(value)
}

func hintProofFrom(state *graph.EntityState) hintProof {
	caseKind, err := exactString(state, vocabulary.TurnCaseDecisionKind)
	if err != nil {
		return hintProof{}
	}
	triggerKind, err := exactString(state, vocabulary.TurnCompanionTriggerKind)
	if err != nil {
		return hintProof{}
	}
	triggerSource, err := exactString(state, vocabulary.TurnCompanionTriggerSource)
	if err != nil {
		return hintProof{}
	}
	if caseKind != string(payload.CaseDecisionRequestHint) ||
		triggerKind != string(vocabulary.CompanionTriggerPlayerHint) ||
		triggerSource != string(vocabulary.CompanionTriggerSourceCaseDecision) {
		return hintProof{}
	}
	return hintProof{
		Proved: true, CaseDecisionKind: caseKind, TriggerKind: triggerKind, TriggerSource: triggerSource,
	}
}

func exactString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var values []string
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok || value == "" {
			return "", errors.New("invalid value")
		}
		values = append(values, value)
	}
	if len(values) != 1 {
		return "", errors.New("not exactly one")
	}
	return values[0], nil
}

func diagnosticHandler(obs observer, worldPrefix, caseID string) http.Handler {
	return diagnosticHandlerWithTimeout(obs, worldPrefix, caseID, defaultObservationTimeout)
}

func diagnosticHandlerWithTimeout(obs observer, worldPrefix, caseID string, timeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if _, err := obs.casePhase(ctx, caseID); err != nil {
			writeObservationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ready": true})
	})
	mux.HandleFunc("GET /turns/{turnID}", func(w http.ResponseWriter, r *http.Request) {
		turnID := r.PathValue("turnID")
		if err := vocabulary.ValidateIDSegment(turnID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_turn_id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		entityID := strings.TrimSuffix(worldPrefix, ".") + ".turn." + turnID
		observation, err := obs.observeTurn(ctx, entityID)
		if err != nil {
			writeObservationError(w, err)
			return
		}
		casePhase, err := obs.casePhase(ctx, caseID)
		if err != nil {
			writeObservationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"turn_id": turnID, "phase": observation.Phase,
			"phase_recorded_at": observation.PhaseRecordedAt.Format(time.RFC3339Nano),
			"case_phase":        casePhase, "kit_hint_proof": observation.HintProof,
			"failure": observation.Failure,
		})
	})
	return withObservationTiming(mux)
}

type observationTimingWriter struct {
	http.ResponseWriter
	started time.Time
	wrote   bool
}

func (w *observationTimingWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.Header().Set("Server-Timing", fmt.Sprintf("observation;dur=%.3f", float64(time.Since(w.started).Microseconds())/1000))
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *observationTimingWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func withObservationTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&observationTimingWriter{ResponseWriter: w, started: time.Now()}, r)
	})
}

func writeObservationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTurnNotMaterialized):
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "turn_not_materialized"})
	case errors.Is(err, errAcceptedTurnInvariant):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "accepted_turn_invariant"})
	case errors.Is(err, errFailureStateInvariant):
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failure_state_invariant"})
	case errors.Is(err, errCasePhaseInvariant):
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "case_phase_invariant"})
	case errors.Is(err, context.DeadlineExceeded):
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "observation_timeout"})
	default:
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "observer_unavailable"})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
