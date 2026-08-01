package companion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// DecisionStore is the exact-resident durable home for structural decisions.
type DecisionStore interface {
	PutCompanionDecision(context.Context, string, *payload.CompanionDecision) (content.Ref, error)
	PutCompanionStageRecord(context.Context, string, *payload.CompanionStageRecord) (content.Ref, error)
}

// ProjectionAuthorizer builds the companion-only evidence snapshot.
type ProjectionAuthorizer interface {
	Project(context.Context, epistemic.AuthenticatedAudience) (*epistemic.Projection, error)
}

// DecisionWriter is kept identical to persona.TurnWriter without importing graphio options here.
type DecisionWriter = persona.TurnWriter

// Executor commits the companion persona's terminal structural exit.
type Executor struct {
	store     DecisionStore
	graph     DecisionWriter
	authority *Authority
	projector ProjectionAuthorizer
	now       func() time.Time
}

// NewExecutor builds the terminal companion decision boundary.
func NewExecutor(
	store DecisionStore, writer DecisionWriter, authority *Authority, projector ProjectionAuthorizer,
) (*Executor, error) {
	if store == nil || writer == nil || authority == nil || projector == nil {
		return nil, errors.New("companion executor requires artifact, graph, bond-authority, and projection surfaces")
	}
	return &Executor{store: store, graph: writer, authority: authority, projector: projector, now: time.Now}, nil
}

// ListTools implements the agentic executor contract.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{persona.Companion().Tool}
}

type decisionArgs struct {
	Kind         payload.CompanionDecisionKind `json:"kind"`
	HintLevel    vocabulary.HintLevel          `json:"hint_level"`
	EvidenceRefs []string                      `json:"evidence_refs"`
	TargetRef    string                        `json:"target_ref"`
}

// Execute revalidates identity and bond, authorizes evidence, then stores first
// and lands the turn reference last.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if call.Name != persona.CompanionDecisionToolName {
		cause := fmt.Errorf("tool %q routed to %q", call.Name, persona.CompanionDecisionToolName)
		return failed(call, agentic.ToolErrorNotFound, cause), errs.WrapInvalid(cause, "companion", "Execute", "route tool")
	}
	identity, err := persona.CompanionIdentityFrom(call.Metadata)
	if err != nil {
		return permanent(call, "resolve injected companion identity", err)
	}
	var result agentic.ToolResult
	var executeErr error
	err = e.authority.WithBondTransaction(ctx, identity.BondID, identity.PlayerID, identity.CompanionID,
		func(transaction *LadderTransaction) error {
			result, executeErr = e.executeTransaction(ctx, call, identity, transaction)
			return nil
		})
	if err != nil {
		if errors.Is(err, ErrBondIntegrity) {
			return permanent(call, "validate companion bond", err)
		}
		return transient(call, "read companion bond", err)
	}
	return result, executeErr
}

func (e *Executor) executeTransaction(
	ctx context.Context, call agentic.ToolCall, identity persona.Identity,
	transaction *LadderTransaction,
) (agentic.ToolResult, error) {
	bondValue := transaction.Bond()
	bond := &bondValue

	var args decisionArgs
	data, err := json.Marshal(call.Arguments)
	if err != nil {
		return correctable(call, err), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return correctable(call, err), nil
	}
	decision := &payload.CompanionDecision{
		TurnID: identity.TurnID, ContextRef: identity.ContextRef,
		PlayerID: identity.PlayerID, CompanionID: identity.CompanionID,
		Kind: args.Kind, HintLevel: args.HintLevel,
		EvidenceRefs: args.EvidenceRefs, TargetRef: args.TargetRef,
	}
	decision.DecisionID = payload.CompanionDecisionID(
		decision.TurnID, decision.ContextRef, decision.PlayerID, decision.CompanionID)
	if err := decision.Validate(); err != nil {
		return correctable(call, err), nil
	}
	if decision.Kind == payload.CompanionDecisionHint {
		return correctable(call, errors.New("hint decisions are fixed by the deterministic companion component")), nil
	}
	turnState, err := e.authority.graph.GetEntity(ctx, identity.TurnEntityID)
	if err != nil {
		return transient(call, "read companion trigger", err)
	}
	selected, err := SelectTrigger(turnState, bond)
	if err != nil {
		return permanent(call, "validate companion trigger", err)
	}
	if selected.Kind != vocabulary.CompanionTriggerWarning || selected.Source != vocabulary.CompanionTriggerSourceResolvedRisk {
		return permanent(call, "validate companion trigger",
			fmt.Errorf("agentic companion exit requires the persisted automatic warning trigger, got %s/%s",
				selected.Kind, selected.Source))
	}
	persisted, err := PersistedTrigger(turnState)
	if err != nil {
		return permanent(call, "validate persisted companion trigger", err)
	}
	if persisted != selected {
		return permanent(call, "validate persisted companion trigger",
			fmt.Errorf("persisted trigger %+v does not match authorized trigger %+v", persisted, selected))
	}
	projection, err := e.projector.Project(ctx, epistemic.CompanionAudience(
		identity.TurnID, identity.TurnEntityID, identity.ContextRef, identity.CompanionID, identity.BondID))
	if err != nil {
		if errors.Is(err, ErrBondIntegrity) || !errs.IsTransient(err) {
			return permanent(call, "validate companion projection", err)
		}
		return transient(call, "read companion projection", err)
	}
	knownEvidence := make(map[string]bool)
	for _, entity := range projection.Entities() {
		kinds := entity.Objects(vocabulary.WorldEntityKind)
		if len(kinds) == 1 && kinds[0] == string(vocabulary.EntityKindEvidence) {
			knownEvidence[entity.ID] = true
		}
	}
	for _, evidenceID := range decision.EvidenceRefs {
		if !knownEvidence[evidenceID] {
			return correctable(call, fmt.Errorf(
				"evidence_refs cites %s, which is outside this companion's authorized knowledge projection",
				evidenceID)), nil
		}
	}

	ref, err := e.store.PutCompanionDecision(ctx, identity.TurnEntityID, decision)
	if err != nil {
		if errors.Is(err, content.ErrArtifactConflict) || errors.Is(err, content.ErrArtifactCorrupt) {
			return permanent(call, "verify resident companion decision", err)
		}
		return transient(call, "store companion decision", err)
	}
	record := &payload.CompanionStageRecord{
		TurnID: identity.TurnID, PlayerID: identity.PlayerID, CompanionID: identity.CompanionID,
		BondID: identity.BondID, Status: payload.CompanionStageDecision,
		TriggerKind: selected.Kind, TriggerSource: selected.Source, DecisionRef: ref.String(),
	}
	stageRef, err := e.store.PutCompanionStageRecord(ctx, identity.TurnEntityID, record)
	if err != nil {
		if errors.Is(err, content.ErrArtifactConflict) || errors.Is(err, content.ErrArtifactCorrupt) {
			return permanent(call, "verify resident companion stage record", err)
		}
		return transient(call, "store companion stage record", err)
	}
	triples, err := record.Triples(identity.TurnEntityID, stageRef.String(), "persona-companion", e.now())
	if err != nil {
		return permanent(call, "project companion stage record", err)
	}
	triples = append(triples, message.Triple{
		Subject: identity.TurnEntityID, Predicate: vocabulary.TurnCompanionDecisionRef.String(),
		Object: ref.String(), Source: "persona-companion", Timestamp: e.now().UTC(), Confidence: 1,
		Context: identity.TurnEntityID,
	})
	if _, err := e.graph.MergeTriples(ctx, identity.TurnEntityID, triples); err != nil {
		return transient(call, "record companion decision and stage references", err)
	}
	body, err := json.Marshal(decision)
	if err != nil {
		return permanent(call, "encode companion decision result", err)
	}
	return agentic.ToolResult{
		CallID: call.ID, Name: call.Name, Content: string(body), StopLoop: true,
		Metadata: map[string]any{"turn_id": identity.TurnID, "companion_decision_ref": ref.String(),
			"companion_id": identity.CompanionID, "kind": string(decision.Kind)},
	}, nil
}

// Exhaust commits the special companion cap outcome and deliberately does not fail the turn.
func (e *Executor) Exhaust(ctx context.Context, identity persona.Identity) error {
	return e.authority.WithBondTransaction(ctx, identity.BondID, identity.PlayerID, identity.CompanionID,
		func(transaction *LadderTransaction) error {
			bond := transaction.Bond()
			return e.exhaustTransaction(ctx, identity, &bond)
		})
}

func (e *Executor) exhaustTransaction(ctx context.Context, identity persona.Identity, bond *Bond) error {
	state, err := e.authority.graph.GetEntity(ctx, identity.TurnEntityID)
	if err != nil {
		return err
	}
	selected, err := SelectTrigger(state, bond)
	if err != nil {
		return err
	}
	if selected.Kind != vocabulary.CompanionTriggerWarning {
		return fmt.Errorf("companion exhaustion has trigger %s, want warning", selected.Kind)
	}
	persisted, err := PersistedTrigger(state)
	if err != nil {
		return err
	}
	if persisted != selected {
		return fmt.Errorf("persisted trigger %+v does not match authorized trigger %+v", persisted, selected)
	}
	decision := &payload.CompanionDecision{TurnID: identity.TurnID, ContextRef: identity.ContextRef,
		PlayerID: identity.PlayerID, CompanionID: identity.CompanionID,
		Kind: payload.CompanionDecisionSilent, EvidenceRefs: []string{}}
	decision.DecisionID = payload.CompanionDecisionID(decision.TurnID, decision.ContextRef,
		decision.PlayerID, decision.CompanionID)
	decisionRef, err := e.store.PutCompanionDecision(ctx, identity.TurnEntityID, decision)
	if err != nil {
		return err
	}
	record := &payload.CompanionStageRecord{TurnID: identity.TurnID, PlayerID: identity.PlayerID,
		CompanionID: identity.CompanionID, BondID: identity.BondID,
		Status: payload.CompanionStageExhausted, TriggerKind: selected.Kind,
		TriggerSource: selected.Source, DecisionRef: decisionRef.String()}
	stageRef, err := e.store.PutCompanionStageRecord(ctx, identity.TurnEntityID, record)
	if err != nil {
		return err
	}
	triples, err := record.Triples(identity.TurnEntityID, stageRef.String(), "persona-companion-exhausted", e.now())
	if err != nil {
		return err
	}
	triples = append(triples, message.Triple{Subject: identity.TurnEntityID,
		Predicate: vocabulary.TurnCompanionDecisionRef.String(), Object: decisionRef.String(),
		Source: "persona-companion-exhausted", Timestamp: e.now().UTC(), Confidence: 1, Context: identity.TurnEntityID})
	_, err = e.graph.MergeTriples(ctx, identity.TurnEntityID, triples)
	return err
}

func failed(call agentic.ToolCall, kind agentic.ToolErrorKind, cause error) agentic.ToolResult {
	return agentic.ToolResult{CallID: call.ID, Name: call.Name, Error: cause.Error(), ErrorKind: kind}
}

func correctable(call agentic.ToolCall, cause error) agentic.ToolResult {
	return failed(call, agentic.ToolErrorInvalidArgs, fmt.Errorf(
		"%v. Correct the closed structural call and emit it again", cause))
}

func transient(call agentic.ToolCall, what string, cause error) (agentic.ToolResult, error) {
	wrapped := fmt.Errorf("%s: %w", what, cause)
	return failed(call, agentic.ToolErrorNetwork, wrapped), errs.WrapTransient(wrapped, "companion", "Execute", what)
}

func permanent(call agentic.ToolCall, what string, cause error) (agentic.ToolResult, error) {
	wrapped := fmt.Errorf("%s: %w", what, cause)
	return failed(call, agentic.ToolErrorInternal, wrapped), errs.WrapInvalid(wrapped, "companion", "Execute", what)
}
