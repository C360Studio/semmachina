package caseflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ProgressGraph is the authoritative state surface used by case progress.
type ProgressGraph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	MergeTriples(context.Context, string, []message.Triple, ...graphio.MergeOption) (*graph.EntityState, error)
}

// ProgressArtifacts is the private structured-artifact surface.
type ProgressArtifacts interface {
	InstanceName() string
	GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error)
	GetKnowledgeReceipt(context.Context, content.Ref) (*content.KnowledgeReceipt, error)
	PutCaseProgressRecord(context.Context, string, *content.CaseProgressRecord) (content.Ref, error)
	GetCaseProgressRecord(context.Context, content.Ref) (*content.CaseProgressRecord, error)
}

// ProgressLifecycleRecorder is the existing structural receipt seam. It owns no phase.
type ProgressLifecycleRecorder interface {
	Record(context.Context, TransitionRequest) (ReceiptOutcome, error)
}

// PermanentProgressError marks committed malformed, foreign, colliding, or
// semantically impossible progress input. Retrying the same delivery cannot
// repair it; the consumer closes the owning turn with a non-disclosing code.
type PermanentProgressError struct{ Err error }

func (e *PermanentProgressError) Error() string { return e.Err.Error() }
func (e *PermanentProgressError) Unwrap() error { return e.Err }

// MissingTriggeredTurnError marks a delivery whose named turn does not exist.
// There is no entity on which to record a failure, so the consumer terminates it.
type MissingTriggeredTurnError struct {
	TurnEntityID string
	Err          error
}

func (e *MissingTriggeredTurnError) Error() string {
	return fmt.Sprintf("case progress trigger turn %s is missing: %v", e.TurnEntityID, e.Err)
}
func (e *MissingTriggeredTurnError) Unwrap() error { return e.Err }

func permanentProgress(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentProgressError{Err: err}
}

func permanentProgressf(format string, args ...any) error {
	return permanentProgress(fmt.Errorf(format, args...))
}

func permanentArtifact(err error) bool {
	return errors.Is(err, content.ErrArtifactNotFound) || errors.Is(err, content.ErrArtifactReference) ||
		errors.Is(err, content.ErrArtifactCorrupt) || errors.Is(err, content.ErrArtifactConflict)
}

// Progressor executes one workflow-agnostic deterministic progress operation.
type Progressor struct {
	graph     ProgressGraph
	artifacts ProgressArtifacts
	recorder  ProgressLifecycleRecorder
	now       func() time.Time
}

// NewProgressor composes graph reads, structured artifacts, and receipt recording.
func NewProgressor(
	graphStore ProgressGraph, artifacts ProgressArtifacts, recorder ProgressLifecycleRecorder,
) (*Progressor, error) {
	if graphStore == nil || artifacts == nil || recorder == nil {
		return nil, errors.New("case progress requires graph, artifacts, and lifecycle recorder")
	}
	return &Progressor{graph: graphStore, artifacts: artifacts, recorder: recorder, now: time.Now}, nil
}

// Process records an applicable event, persists one result, and lands its turn
// reference last. Sequential redelivery observes that reference and does no work.
func (p *Progressor) Process(ctx context.Context, turnID, turnEntityID string) (content.Ref, error) {
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return content.Ref{}, permanentProgress(err)
	}
	state, err := p.graph.GetEntity(ctx, turnEntityID)
	if err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return content.Ref{}, &MissingTriggeredTurnError{TurnEntityID: turnEntityID, Err: err}
		}
		return content.Ref{}, fmt.Errorf("read progress turn %s: %w", turnEntityID, err)
	}
	if state == nil || state.ID != turnEntityID || state.IsStub() {
		return content.Ref{}, &MissingTriggeredTurnError{TurnEntityID: turnEntityID,
			Err: graphio.ErrEntityNotFound}
	}
	resident, err := objects(state, vocabulary.TurnCaseProgressRef)
	if err != nil {
		return content.Ref{}, permanentProgress(err)
	}

	decisionRef, err := p.requiredRef(state, vocabulary.TurnCaseDecisionRef, turnID)
	if err != nil {
		return content.Ref{}, permanentProgress(err)
	}
	knowledgeRef, err := p.requiredRef(state, vocabulary.TurnKnowledgeRef, turnID)
	if err != nil {
		return content.Ref{}, permanentProgress(err)
	}
	decisionRecord, err := p.artifacts.GetCaseDecisionRecord(ctx, decisionRef)
	if err != nil {
		if permanentArtifact(err) {
			return content.Ref{}, permanentProgress(fmt.Errorf("read case decision for progress: %w", err))
		}
		return content.Ref{}, fmt.Errorf("read case decision for progress: %w", err)
	}
	knowledge, err := p.artifacts.GetKnowledgeReceipt(ctx, knowledgeRef)
	if err != nil {
		if permanentArtifact(err) {
			return content.Ref{}, permanentProgress(fmt.Errorf("read knowledge receipt for progress: %w", err))
		}
		return content.Ref{}, fmt.Errorf("read knowledge receipt for progress: %w", err)
	}
	if decisionRecord == nil || knowledge == nil {
		return content.Ref{}, permanentProgressf("case progress artifact store returned nil")
	}
	if err := decisionRecord.Validate(); err != nil {
		return content.Ref{}, permanentProgressf("case decision for progress: %w", err)
	}
	if err := knowledge.Validate(); err != nil {
		return content.Ref{}, permanentProgressf("knowledge receipt for progress: %w", err)
	}
	if decisionRecord.TurnID != turnID || knowledge.TurnID != turnID {
		return content.Ref{}, permanentProgressf("progress artifact identity does not match turn %q", turnID)
	}

	expected, event, err := p.derive(ctx, decisionRecord, knowledge)
	if err != nil {
		return content.Ref{}, err
	}
	if len(resident) > 0 {
		if len(resident) != 1 {
			return content.Ref{}, permanentProgressf("turn %s carries %d case progress references", turnEntityID, len(resident))
		}
		ref, err := p.exactRef(resident[0], vocabulary.TurnCaseProgressRef, turnID)
		if err != nil {
			return content.Ref{}, permanentProgress(err)
		}
		record, err := p.artifacts.GetCaseProgressRecord(ctx, ref)
		if err != nil {
			if permanentArtifact(err) {
				return content.Ref{}, permanentProgress(fmt.Errorf("read resident case progress: %w", err))
			}
			return content.Ref{}, fmt.Errorf("read resident case progress: %w", err)
		}
		if record == nil {
			return content.Ref{}, permanentProgressf("resident case progress store returned nil")
		}
		if err := record.Validate(); err != nil {
			return content.Ref{}, permanentProgressf("resident case progress: %w", err)
		}
		if *record != *expected {
			return content.Ref{}, permanentProgressf(
				"resident case progress does not match the canonical decision and knowledge: got %+v, want %+v",
				record, expected)
		}
		return ref, nil
	}
	if event != nil {
		if _, err := p.recorder.Record(ctx, *event); err != nil {
			var illegal *IllegalTransitionError
			if errors.As(err, &illegal) || errors.Is(err, graphio.ErrEntityNotFound) {
				return content.Ref{}, permanentProgress(fmt.Errorf("record deterministic case progress: %w", err))
			}
			return content.Ref{}, fmt.Errorf("record deterministic case progress: %w", err)
		}
	}
	ref, err := p.artifacts.PutCaseProgressRecord(ctx, turnEntityID, expected)
	if err != nil {
		if permanentArtifact(err) {
			return content.Ref{}, permanentProgress(fmt.Errorf("store case progress: %w", err))
		}
		return content.Ref{}, fmt.Errorf("store case progress: %w", err)
	}
	triple := message.Triple{
		Subject: turnEntityID, Predicate: vocabulary.TurnCaseProgressRef.String(), Object: ref.String(),
		Source: ReceiptSource, Timestamp: p.now().UTC(), Confidence: 1,
	}
	if _, err := p.graph.MergeTriples(ctx, turnEntityID, []message.Triple{triple}); err != nil {
		return content.Ref{}, fmt.Errorf("land case progress reference last: %w", err)
	}
	return ref, nil
}

func (p *Progressor) derive(
	ctx context.Context, record *payload.CaseDecisionRecord, knowledge *content.KnowledgeReceipt,
) (*content.CaseProgressRecord, *TransitionRequest, error) {
	if record.Status == payload.CaseDecisionStatusNotApplicable {
		if knowledge.Status != content.KnowledgeNotApplicable {
			return nil, nil, permanentProgressf("non-case decision has a case-scoped knowledge receipt")
		}
		return &content.CaseProgressRecord{TurnID: record.TurnID, Status: content.CaseProgressNotApplicable}, nil, nil
	}
	decision := record.Decision
	if decision == nil || knowledge.Status != content.KnowledgeCommitted || knowledge.DecisionID != decision.DecisionID {
		return nil, nil, permanentProgressf("case progress decision and knowledge identities disagree")
	}
	kind := vocabulary.CaseLifecycleEventKind("")
	switch decision.Kind {
	case payload.CaseDecisionObserve:
		caseState, err := p.graph.GetEntity(ctx, decision.CaseID)
		if err != nil {
			if errors.Is(err, graphio.ErrEntityNotFound) {
				return nil, nil, permanentProgress(fmt.Errorf("read case victim for progress: %w", err))
			}
			return nil, nil, fmt.Errorf("read case victim for progress: %w", err)
		}
		if caseState == nil || caseState.ID != decision.CaseID || caseState.IsStub() {
			return nil, nil, permanentProgressf("case %s is missing, foreign, or a stub", decision.CaseID)
		}
		victims, err := objects(caseState, vocabulary.CaseMemberVictim)
		if err != nil {
			return nil, nil, permanentProgress(err)
		}
		if len(victims) != 1 {
			return nil, nil, permanentProgressf("case %s carries %d victim references", decision.CaseID, len(victims))
		}
		if slices.Contains(decision.TargetRefs, victims[0]) {
			kind = vocabulary.CaseEventBodyObserved
		}
	case payload.CaseDecisionInvestigate:
		kind = vocabulary.CaseEventInvestigationStarted
	}
	progress := &content.CaseProgressRecord{
		TurnID: record.TurnID, DecisionID: decision.DecisionID, CaseID: decision.CaseID,
		Status: content.CaseProgressNotApplicable,
	}
	if kind == "" {
		return progress, nil, nil
	}
	progress.Status = content.CaseProgressEventRecorded
	progress.EventKind = kind
	progress.EventID = content.CaseProgressEventID(record.TurnID, decision.DecisionID, kind)
	request := &TransitionRequest{CaseEntityID: decision.CaseID, EventID: progress.EventID, Kind: kind}
	return progress, request, nil
}

func (p *Progressor) requiredRef(
	state *graph.EntityState, predicate vocabulary.Predicate, turnID string,
) (content.Ref, error) {
	values, err := objects(state, predicate)
	if err != nil {
		return content.Ref{}, err
	}
	if len(values) != 1 {
		return content.Ref{}, fmt.Errorf("turn %s carries %d %s values", state.ID, len(values), predicate)
	}
	return p.exactRef(values[0], predicate, turnID)
}

func (p *Progressor) exactRef(text string, predicate vocabulary.Predicate, turnID string) (content.Ref, error) {
	ref, err := content.ParseRef(text)
	if err != nil {
		return content.Ref{}, fmt.Errorf("parse %s: %w", predicate, err)
	}
	want, err := content.KeyFor(predicate, content.SubjectTurn, turnID)
	if err != nil {
		return content.Ref{}, err
	}
	if ref.Instance != p.artifacts.InstanceName() || ref.Key != want {
		return content.Ref{}, fmt.Errorf("%s reference %s does not address %s/%s", predicate, ref, p.artifacts.InstanceName(), want)
	}
	return ref, nil
}

func objects(state *graph.EntityState, predicate vocabulary.Predicate) ([]string, error) {
	if state == nil {
		return nil, nil
	}
	out := make([]string, 0, 1)
	for _, triple := range state.Triples {
		if triple.Subject != state.ID || triple.Predicate != predicate.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("entity %s carries malformed %s object %T(%v)",
				state.ID, predicate, triple.Object, triple.Object)
		}
		out = append(out, value)
	}
	return out, nil
}
