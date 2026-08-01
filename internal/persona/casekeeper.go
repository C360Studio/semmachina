package persona

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/c360studio/semstreams/agentic"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
)

// CaseDecisionStore is the durable private interpretation store.
type CaseDecisionStore interface {
	PutCaseDecisionRecord(
		ctx context.Context, turnEntityID string, record *payload.CaseDecisionRecord,
	) (content.Ref, error)
}

var _ CaseDecisionStore = (*content.Store)(nil)

// CaseDecisionExecutor commits the casekeeper's terminal structured exit.
type CaseDecisionExecutor struct {
	store CaseDecisionStore
	graph TurnWriter
	now   func() time.Time
}

// NewCaseDecisionExecutor builds the casekeeper's terminal tool.
func NewCaseDecisionExecutor(
	store CaseDecisionStore, writer TurnWriter, opts ...ExecutorOption,
) (*CaseDecisionExecutor, error) {
	if store == nil {
		return nil, errors.New("the case decision tool requires a content store")
	}
	if writer == nil {
		return nil, errors.New("the case decision tool requires a graph write surface")
	}
	deps := resolveDeps(opts)
	return &CaseDecisionExecutor{store: store, graph: writer, now: deps.now}, nil
}

// ListTools implements the framework tool-executor contract.
func (e *CaseDecisionExecutor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{caseDecisionToolDefinition()}
}

type caseDecisionArgs struct {
	Kind       payload.CaseDecisionKind `json:"kind"`
	TargetRefs []string                 `json:"target_refs"`
	RevealRefs []string                 `json:"reveal_refs"`
	CulpritRef string                   `json:"culprit_ref"`
	MethodRef  string                   `json:"method_ref"`
	MotiveRef  string                   `json:"motive_ref"`
}

// Execute injects engine identity, validates, stores, and projects one decision.
func (e *CaseDecisionExecutor) Execute(
	ctx context.Context, call agentic.ToolCall,
) (agentic.ToolResult, error) {
	if call.Name != CaseDecisionToolName {
		return notFound(call, CaseDecisionToolName)
	}
	identity, err := CaseIdentityFrom(call.Metadata)
	if err != nil {
		return internalFailure(call, "resolve the case and turn this decision belongs to", err)
	}
	var args caseDecisionArgs
	if err := decodeStrict(call.Arguments, &args); err != nil {
		return correctable(call, err), nil
	}
	decision := &payload.CaseDecision{
		TurnID: identity.TurnID, ActionID: identity.ActionID,
		CaseID: identity.CaseID, ActorID: identity.ActorID,
		Kind: args.Kind, TargetRefs: args.TargetRefs, RevealRefs: args.RevealRefs,
		CulpritRef: args.CulpritRef, MethodRef: args.MethodRef, MotiveRef: args.MotiveRef,
	}
	decision.DecisionID = payload.CaseDecisionID(
		decision.TurnID, decision.ActionID, decision.CaseID, decision.ActorID,
	)
	record := &payload.CaseDecisionRecord{
		TurnID: identity.TurnID, ActionID: identity.ActionID,
		Status: payload.CaseDecisionStatusDecision, Decision: decision,
	}
	if err := record.Validate(); err != nil {
		return correctable(call, err), nil
	}
	ref, err := e.store.PutCaseDecisionRecord(ctx, identity.TurnEntityID, record)
	if err != nil {
		return transientFailure(call, "store the case decision", err)
	}
	triples, err := record.Triples(identity.TurnEntityID, ref.String(), sourceFor(RoleCasekeeper), e.now().UTC())
	if err != nil {
		return internalFailure(call, "project the case decision onto the turn", err)
	}
	if _, err := e.graph.MergeTriples(ctx, identity.TurnEntityID, triples); err != nil {
		return transientFailure(call, "record the case decision reference on the turn", err)
	}
	body, err := json.Marshal(record)
	if err != nil {
		return internalFailure(call, "encode the case decision result", err)
	}
	return agentic.ToolResult{
		CallID: call.ID, Name: call.Name, Content: string(body), StopLoop: true,
		Metadata: map[string]any{
			"turn_id": identity.TurnID, "case_decision_ref": ref.String(), "kind": string(decision.Kind),
		},
	}, nil
}
