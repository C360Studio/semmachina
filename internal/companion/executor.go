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
	if _, err := e.authority.ValidateBond(ctx, identity.BondID, identity.PlayerID, identity.CompanionID); err != nil {
		if errors.Is(err, ErrBondIntegrity) {
			return permanent(call, "validate companion bond", err)
		}
		return transient(call, "read companion bond", err)
	}

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
	triple := message.Triple{
		Subject: identity.TurnEntityID, Predicate: vocabulary.TurnCompanionDecisionRef.String(),
		Object: ref.String(), Source: "persona-companion", Timestamp: e.now().UTC(),
	}
	if _, err := e.graph.MergeTriples(ctx, identity.TurnEntityID, []message.Triple{triple}); err != nil {
		return transient(call, "record companion decision reference", err)
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
