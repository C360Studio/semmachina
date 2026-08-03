package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type recordingFailureDetailReader struct {
	details map[string]*content.FailureDetail
	errs    map[string]error
	reads   []string
}

func (r *recordingFailureDetailReader) GetFailureDetail(
	ctx context.Context, ref content.Ref,
) (*content.FailureDetail, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.reads = append(r.reads, ref.String())
	if err := r.errs[ref.String()]; err != nil {
		return nil, err
	}
	return r.details[ref.String()], nil
}

func failedTurnState(reason any, refs ...any) *graph.EntityState {
	triples := []message.Triple{
		{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseFailed), Timestamp: time.Now()},
		{Predicate: vocabulary.TurnFailureReason.String(), Object: reason},
	}
	for _, ref := range refs {
		triples = append(triples, message.Triple{Predicate: vocabulary.TurnFailureRef.String(), Object: ref})
	}
	return &graph.EntityState{ID: "prefix.turn.turn-42", Version: 1, Triples: triples}
}

func failureCaseState() *graph.EntityState {
	return &graph.EntityState{ID: "prefix.case.main", Version: 1, Triples: []message.Triple{{
		Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery),
	}}}
}

func TestHintProofFromExposesFieldsOnlyForExactProofTuple(t *testing.T) {
	state := &graph.EntityState{Triples: []message.Triple{
		{Predicate: vocabulary.TurnCaseDecisionKind.String(), Object: string(payload.CaseDecisionObserve)},
		{Predicate: vocabulary.TurnCompanionTriggerKind.String(), Object: string(vocabulary.CompanionTriggerPlayerHint)},
		{Predicate: vocabulary.TurnCompanionTriggerSource.String(), Object: string(vocabulary.CompanionTriggerSourceCaseDecision)},
	}}
	if got := hintProofFrom(state); got != (hintProof{}) {
		t.Fatalf("nonmatching tuple exposed partial proof: %+v", got)
	}
	state.Triples[0].Object = string(payload.CaseDecisionRequestHint)
	want := hintProof{
		Proved: true, CaseDecisionKind: string(payload.CaseDecisionRequestHint),
		TriggerKind:   string(vocabulary.CompanionTriggerPlayerHint),
		TriggerSource: string(vocabulary.CompanionTriggerSourceCaseDecision),
	}
	if got := hintProofFrom(state); got != want {
		t.Fatalf("exact tuple proof = %+v, want %+v", got, want)
	}
}

func TestDiagnosticHandlerOmitsFieldsFromUnprovedHintTuple(t *testing.T) {
	turnID, caseID := "prefix.turn.turn-42", "prefix.case.main"
	turnState := &graph.EntityState{ID: turnID, Version: 1, Triples: []message.Triple{
		{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseComplete), Timestamp: time.Now()},
		{Predicate: vocabulary.TurnCaseDecisionKind.String(), Object: string(payload.CaseDecisionObserve)},
		{Predicate: vocabulary.TurnCompanionTriggerKind.String(), Object: string(vocabulary.CompanionTriggerPlayerHint)},
		{Predicate: vocabulary.TurnCompanionTriggerSource.String(), Object: string(vocabulary.CompanionTriggerSourceCaseDecision)},
	}}
	obs := &productionObserver{graph: &recordingEntityReader{states: map[string]*graph.EntityState{
		turnID: turnState, caseID: failureCaseState(),
	}, errs: map[string]error{}, reads: map[string]int{}}}
	rec := httptest.NewRecorder()
	diagnosticHandler(obs, "prefix", caseID).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	proof, ok := body["kit_hint_proof"].(map[string]any)
	if !ok || len(proof) != 1 || proof["proved"] != false {
		t.Fatalf("kit_hint_proof = %#v, want exact {proved:false}", body["kit_hint_proof"])
	}
}

func TestProductionObserverProjectsOnlyClosedFailureReasonAndClass(t *testing.T) {
	turnEntityID, caseID := "prefix.turn.turn-42", "prefix.case.main"
	ref := content.Ref{Instance: "CONTENT", Key: "turn/turn-42/failure"}
	reader := &recordingEntityReader{states: map[string]*graph.EntityState{
		turnEntityID: failedTurnState(string(vocabulary.FailurePersonaLoopFailed), ref.String()),
		caseID:       failureCaseState(),
	}, errs: map[string]error{}, reads: map[string]int{}}
	details := &recordingFailureDetailReader{details: map[string]*content.FailureDetail{
		ref.String(): {
			TurnID: "turn-42", Reason: vocabulary.FailurePersonaLoopFailed,
			Class:   content.FailureClassProviderModel,
			Message: "API key super-secret, provider body and model name must stay stored",
		},
	}, errs: map[string]error{}}

	rec := httptest.NewRecorder()
	diagnosticHandler(&productionObserver{graph: reader, details: details}, "prefix", caseID).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	want := `"failure":{"reason":"persona-loop-failed","class":"provider-model","authorization_reason":null}`
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("response does not carry exact closed failure object: %s", rec.Body.String())
	}
	for _, forbidden := range []string{"super-secret", "provider body", "model name", ref.String(), "message"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestProductionObserverProjectsClosedKnowledgeAuthorizationReason(t *testing.T) {
	ref := content.Ref{Instance: "CONTENT", Key: "turn/turn-42/failure"}
	details := &recordingFailureDetailReader{details: map[string]*content.FailureDetail{
		ref.String(): {
			TurnID: "turn-42", Reason: vocabulary.FailureKnowledgeUnauthorized,
			Class: content.FailureClassDeterministic, AuthorizationReason: vocabulary.AuthorizationWrongActor,
			Message: "knowledge authorization was refused",
		},
	}, errs: map[string]error{}}
	obs := &productionObserver{graph: &recordingEntityReader{
		states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": failedTurnState(string(vocabulary.FailureKnowledgeUnauthorized), ref.String()),
			"prefix.case.main":    failureCaseState(),
		}, errs: map[string]error{}, reads: map[string]int{},
	}, details: details}
	rec := httptest.NewRecorder()
	diagnosticHandler(obs, "prefix", "prefix.case.main").ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(),
		`"failure":{"reason":"knowledge-unauthorized","class":"deterministic","authorization_reason":"wrong-actor"}`) {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestProductionObserverMapsLegacyAndReflessFailuresWithoutOpenText(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason vocabulary.FailureReason
		ref    *content.Ref
		detail *content.FailureDetail
		want   content.FailureClass
	}{
		{name: "legacy referenced", reason: vocabulary.FailureEffectInvalid,
			ref:    &content.Ref{Instance: "CONTENT", Key: "turn/turn-42/failure"},
			detail: &content.FailureDetail{TurnID: "turn-42", Reason: vocabulary.FailureEffectInvalid, Message: "legacy"},
			want:   content.FailureClassUnknown},
		{name: "legacy referenced knowledge", reason: vocabulary.FailureKnowledgeUnauthorized,
			ref: &content.Ref{Instance: "CONTENT", Key: "turn/turn-42/failure"},
			detail: &content.FailureDetail{
				TurnID: "turn-42", Reason: vocabulary.FailureKnowledgeUnauthorized, Message: "legacy",
			},
			want: content.FailureClassUnknown},
		{name: "cap without detail", reason: vocabulary.FailurePersonaCapExhausted, want: content.FailureClassAgentLimit},
		{name: "loop without detail", reason: vocabulary.FailurePersonaLoopFailed, want: content.FailureClassUnknown},
		{name: "deterministic without detail", reason: vocabulary.FailureEffectInvalid, want: content.FailureClassDeterministic},
		{name: "knowledge without detail", reason: vocabulary.FailureKnowledgeUnauthorized, want: content.FailureClassDeterministic},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := failedTurnState(string(test.reason))
			details := &recordingFailureDetailReader{details: map[string]*content.FailureDetail{}, errs: map[string]error{}}
			if test.ref != nil {
				state.Triples = append(state.Triples, message.Triple{
					Predicate: vocabulary.TurnFailureRef.String(), Object: test.ref.String(),
				})
				details.details[test.ref.String()] = test.detail
			}
			obs := &productionObserver{graph: &recordingEntityReader{
				states: map[string]*graph.EntityState{"prefix.turn.turn-42": state},
				errs:   map[string]error{}, reads: map[string]int{},
			}, details: details}
			got, err := obs.observeTurn(t.Context(), "prefix.turn.turn-42")
			if err != nil {
				t.Fatalf("observeTurn: %v", err)
			}
			if got.Failure == nil || got.Failure.Reason != test.reason || got.Failure.Class != test.want {
				t.Fatalf("failure = %+v, want %q/%q", got.Failure, test.reason, test.want)
			}
			if got.Failure.AuthorizationReason != nil {
				t.Fatalf("authorization reason = %q, want null", *got.Failure.AuthorizationReason)
			}
		})
	}
}

func TestProductionObserverRejectsFailureEvidenceOnNonfailedTurns(t *testing.T) {
	for _, evidence := range []message.Triple{
		{Predicate: vocabulary.TurnFailureReason.String(), Object: string(vocabulary.FailureEffectInvalid)},
		{Predicate: vocabulary.TurnFailureRef.String(), Object: "not-even-a-reference"},
	} {
		state := &graph.EntityState{ID: "prefix.turn.turn-42", Version: 1, Triples: []message.Triple{
			{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseComplete), Timestamp: time.Now()},
			evidence,
		}}
		obs := &productionObserver{graph: &recordingEntityReader{
			states: map[string]*graph.EntityState{"prefix.turn.turn-42": state}, errs: map[string]error{}, reads: map[string]int{},
		}}
		_, err := obs.observeTurn(t.Context(), "prefix.turn.turn-42")
		if !errors.Is(err, errFailureStateInvariant) {
			t.Fatalf("predicate %q: error = %v, want failure state invariant", evidence.Predicate, err)
		}
	}
}

func TestDiagnosticFailureDetailReadErrorsHaveStableHTTPClasses(t *testing.T) {
	ref := content.Ref{Instance: "CONTENT", Key: "turn/turn-42/failure"}
	for _, test := range []struct {
		name string
		err  error
		code int
		body string
	}{
		{name: "dangling object", err: content.ErrArtifactNotFound, code: http.StatusInternalServerError,
			body: `{"error":"failure_state_invariant"}`},
		{name: "transport", err: errors.New("content unavailable"), code: http.StatusServiceUnavailable,
			body: `{"error":"observer_unavailable"}`},
		{name: "deadline", err: context.DeadlineExceeded, code: http.StatusGatewayTimeout,
			body: `{"error":"observation_timeout"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			obs := &productionObserver{
				graph: &recordingEntityReader{states: map[string]*graph.EntityState{
					"prefix.turn.turn-42": failedTurnState(string(vocabulary.FailureEffectInvalid), ref.String()),
				}, errs: map[string]error{}, reads: map[string]int{}},
				details: &recordingFailureDetailReader{errs: map[string]error{ref.String(): test.err}},
			}
			rec := httptest.NewRecorder()
			diagnosticHandler(obs, "prefix", "prefix.case.main").ServeHTTP(rec,
				httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
			if rec.Code != test.code || strings.TrimSpace(rec.Body.String()) != test.body {
				t.Fatalf("response = %d %q, want %d %s", rec.Code, rec.Body.String(), test.code, test.body)
			}
		})
	}
}

func TestProductionObserverRejectsForeignFailureKeyBeforeObjectRead(t *testing.T) {
	foreignRef := content.Ref{Instance: "CONTENT", Key: "turn/turn-foreign/failure"}
	details := &recordingFailureDetailReader{
		errs: map[string]error{foreignRef.String(): errors.New("transport-secret-that-must-not-be-reached")},
	}
	obs := &productionObserver{
		graph: &recordingEntityReader{states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": failedTurnState(string(vocabulary.FailureEffectInvalid), foreignRef.String()),
		}, errs: map[string]error{}, reads: map[string]int{}},
		details: details,
	}
	rec := httptest.NewRecorder()
	diagnosticHandler(obs, "prefix", "prefix.case.main").ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusInternalServerError ||
		strings.TrimSpace(rec.Body.String()) != `{"error":"failure_state_invariant"}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if len(details.reads) != 0 {
		t.Fatalf("foreign reference triggered %d detail read(s): %v", len(details.reads), details.reads)
	}
}

func TestProductionObserverFailsClosedOnMalformedFailureEvidence(t *testing.T) {
	validRef := content.Ref{Instance: "CONTENT", Key: "turn/turn-42/failure"}
	transportErr := errors.New("content transport unavailable")
	withoutReason := failedTurnState(string(vocabulary.FailureEffectInvalid))
	withoutReason.Triples = withoutReason.Triples[:1]
	duplicateReason := failedTurnState(string(vocabulary.FailureEffectInvalid))
	duplicateReason.Triples = append(duplicateReason.Triples, message.Triple{
		Predicate: vocabulary.TurnFailureReason.String(), Object: string(vocabulary.FailureEffectInvalid),
	})
	for _, test := range []struct {
		name    string
		state   *graph.EntityState
		details map[string]*content.FailureDetail
		errs    map[string]error
		wantErr error
	}{
		{name: "reason missing", state: withoutReason},
		{name: "reason duplicated", state: duplicateReason},
		{name: "reason open", state: failedTurnState("API key invalid")},
		{name: "reason wrong type", state: failedTurnState(42)},
		{name: "ref duplicated", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String(), validRef.String())},
		{name: "ref malformed", state: failedTurnState(string(vocabulary.FailureEffectInvalid), "not-a-reference")},
		{name: "ref wrong type", state: failedTurnState(string(vocabulary.FailureEffectInvalid), 42)},
		{name: "detail dangling", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			errs: map[string]error{validRef.String(): content.ErrArtifactNotFound}},
		{name: "detail transport unavailable", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			errs: map[string]error{validRef.String(): transportErr}, wantErr: transportErr},
		{name: "detail deadline", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			errs: map[string]error{validRef.String(): context.DeadlineExceeded}, wantErr: context.DeadlineExceeded},
		{name: "detail missing", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String())},
		{name: "detail reason mismatch", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailureTurnStranded, Class: content.FailureClassDeterministic, Message: "x",
			}}},
		{name: "detail turn mismatch", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-other", Reason: vocabulary.FailureEffectInvalid, Class: content.FailureClassDeterministic, Message: "x",
			}}},
		{name: "deterministic reason with agent class", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailureEffectInvalid, Class: content.FailureClassAgentRuntime, Message: "x",
			}}},
		{name: "cap reason with provider class", state: failedTurnState(string(vocabulary.FailurePersonaCapExhausted), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailurePersonaCapExhausted, Class: content.FailureClassProviderModel, Message: "x",
			}}},
		{name: "loop reason with deterministic class", state: failedTurnState(string(vocabulary.FailurePersonaLoopFailed), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailurePersonaLoopFailed, Class: content.FailureClassDeterministic, Message: "x",
			}}},
		{name: "unknown authorization reason", state: failedTurnState(string(vocabulary.FailureKnowledgeUnauthorized), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailureKnowledgeUnauthorized,
				Class: content.FailureClassDeterministic, AuthorizationReason: "credential-invalid", Message: "fixed",
			}}},
		{name: "classed knowledge detail missing authorization reason", state: failedTurnState(string(vocabulary.FailureKnowledgeUnauthorized), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailureKnowledgeUnauthorized,
				Class: content.FailureClassDeterministic, Message: "fixed",
			}}},
		{name: "authorization reason on other failure", state: failedTurnState(string(vocabulary.FailureEffectInvalid), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailureEffectInvalid,
				Class: content.FailureClassDeterministic, AuthorizationReason: vocabulary.AuthorizationWrongActor, Message: "fixed",
			}}},
		{name: "authorization reason with legacy class", state: failedTurnState(string(vocabulary.FailureKnowledgeUnauthorized), validRef.String()),
			details: map[string]*content.FailureDetail{validRef.String(): {
				TurnID: "turn-42", Reason: vocabulary.FailureKnowledgeUnauthorized,
				AuthorizationReason: vocabulary.AuthorizationWrongActor, Message: "fixed",
			}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			obs := &productionObserver{
				graph: &recordingEntityReader{states: map[string]*graph.EntityState{
					"prefix.turn.turn-42": test.state,
				}, errs: map[string]error{}, reads: map[string]int{}},
				details: &recordingFailureDetailReader{details: test.details, errs: test.errs},
			}
			_, err := obs.observeTurn(t.Context(), "prefix.turn.turn-42")
			wantErr := test.wantErr
			if wantErr == nil {
				wantErr = errFailureStateInvariant
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
		})
	}
}
