package companion

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const maxNarrationKnowledgeRecords = 256

// NarrationArtifacts is the exact-resident companion artifact read surface.
type NarrationArtifacts interface {
	InstanceName() string
	GetCompanionDecision(context.Context, content.Ref) (*payload.CompanionDecision, error)
	GetCompanionStageRecord(context.Context, content.Ref) (*payload.CompanionStageRecord, error)
}

// NarrationResolver reauthorizes only evidence explicitly cited by the
// committed structural companion decision for this exact turn.
type NarrationResolver struct {
	graph     Graph
	artifacts NarrationArtifacts
	authority *Authority
}

// NewNarrationResolver builds the companion-owned narration authorization gate.
func NewNarrationResolver(graphReader Graph, artifacts NarrationArtifacts, authority *Authority) (*NarrationResolver, error) {
	if graphReader == nil || artifacts == nil || authority == nil {
		return nil, errors.New("companion narration resolver requires graph, artifact, and bond authority surfaces")
	}
	return &NarrationResolver{graph: graphReader, artifacts: artifacts, authority: authority}, nil
}

// AuthorizedNarrationEvidence implements epistemic.NarrationEvidenceResolver.
func (r *NarrationResolver) AuthorizedNarrationEvidence(
	ctx context.Context, request epistemic.NarrationEvidenceRequest,
) ([]string, error) {
	if request.Purpose != epistemic.PurposeNarrator && request.Purpose != epistemic.PurposeDenouement {
		return nil, fmt.Errorf("companion narration resolver rejects purpose %q", request.Purpose)
	}
	if err := payload.RequireTurnEntityID(request.TurnID, request.TurnEntityID); err != nil {
		return nil, fmt.Errorf("companion narration turn identity: %w", err)
	}
	turnState, err := r.graph.GetEntity(ctx, request.TurnEntityID)
	if err != nil {
		return nil, fmt.Errorf("read companion narration turn: %w", err)
	}
	if turnState == nil || turnState.ID != request.TurnEntityID || turnState.IsStub() {
		return nil, errors.New("companion narration turn is missing, foreign, or a stub")
	}
	playerID, err := exactString(turnState, vocabulary.TurnActionPlayer)
	if err != nil {
		return nil, fmt.Errorf("read companion narration player identity: %w", err)
	}
	if playerID != request.PlayerID {
		return nil, errors.New("companion narration player identity does not match authenticated turn")
	}
	contextRef, err := exactString(turnState, vocabulary.TurnActionScene)
	if err != nil {
		return nil, fmt.Errorf("read companion narration context: %w", err)
	}
	if contextRef != request.ContextRef {
		return nil, errors.New("companion narration context does not match authenticated turn")
	}
	stageRef, err := r.exactTurnRef(turnState, vocabulary.TurnCompanionStageRef, request.TurnID)
	if err != nil {
		return nil, err
	}
	stage, err := r.artifacts.GetCompanionStageRecord(ctx, stageRef)
	if err != nil {
		return nil, fmt.Errorf("read companion narration stage: %w", err)
	}
	if stage == nil {
		return nil, errors.New("companion narration stage store returned nil")
	}
	if err := stage.Validate(); err != nil {
		return nil, fmt.Errorf("invalid companion narration stage: %w", err)
	}
	if stage.TurnID != request.TurnID || stage.PlayerID != request.PlayerID {
		return nil, errors.New("companion narration stage has foreign turn or player identity")
	}

	switch stage.Status {
	case payload.CompanionStageNoActiveBond:
		if err := forbidDecisionRef(turnState); err != nil {
			return nil, err
		}
		bond, err := r.authority.ActiveBondForPlayer(ctx, request.PlayerID)
		if err != nil {
			return nil, err
		}
		if bond != nil {
			return nil, errors.New("no-active-bond narration stage disagrees with authoritative active bond")
		}
		return []string{}, nil
	case payload.CompanionStageNoTrigger:
		if err := forbidDecisionRef(turnState); err != nil {
			return nil, err
		}
		if _, err := r.authority.ValidateBond(ctx, stage.BondID, request.PlayerID, stage.CompanionID); err != nil {
			return nil, err
		}
		return []string{}, nil
	case payload.CompanionStageDecision, payload.CompanionStageExhausted:
	default:
		return nil, fmt.Errorf("unsupported companion narration stage status %q", stage.Status)
	}

	if _, err := r.authority.ValidateBond(ctx, stage.BondID, request.PlayerID, stage.CompanionID); err != nil {
		return nil, err
	}
	decisionRef, err := r.exactTurnRef(turnState, vocabulary.TurnCompanionDecisionRef, request.TurnID)
	if err != nil {
		return nil, err
	}
	if decisionRef.String() != stage.DecisionRef {
		return nil, errors.New("companion narration decision reference disagrees with stage record")
	}
	decision, err := r.artifacts.GetCompanionDecision(ctx, decisionRef)
	if err != nil {
		return nil, fmt.Errorf("read companion narration decision: %w", err)
	}
	if decision == nil {
		return nil, errors.New("companion narration decision store returned nil")
	}
	if err := decision.Validate(); err != nil {
		return nil, fmt.Errorf("invalid companion narration decision: %w", err)
	}
	if decision.TurnID != request.TurnID || decision.PlayerID != request.PlayerID ||
		decision.CompanionID != stage.CompanionID || decision.ContextRef != request.ContextRef {
		return nil, errors.New("companion narration decision has foreign turn, player, companion, or context identity")
	}

	return r.authorizedKnowledge(ctx, stage.CompanionID, decision.EvidenceRefs)
}

func (r *NarrationResolver) authorizedKnowledge(
	ctx context.Context, companionID string, cited []string,
) ([]string, error) {
	recordIDs, err := r.graph.EntitiesByPredicateValue(ctx, vocabulary.KnowledgeActorHolder.String(),
		companionID, maxNarrationKnowledgeRecords+1)
	if err != nil {
		return nil, fmt.Errorf("query companion narration knowledge: %w", err)
	}
	if len(recordIDs) > maxNarrationKnowledgeRecords {
		return nil, fmt.Errorf("companion narration knowledge exceeds %d records", maxNarrationKnowledgeRecords)
	}
	records := make([]graph.EntityState, 0, len(recordIDs))
	for _, recordID := range recordIDs {
		record, err := r.graph.GetEntity(ctx, recordID)
		if err != nil {
			return nil, fmt.Errorf("read companion narration knowledge %s: %w", recordID, err)
		}
		if record == nil || record.ID != recordID || record.IsStub() {
			return nil, fmt.Errorf("companion narration knowledge %s is missing, foreign, or a stub", recordID)
		}
		records = append(records, *record)
	}
	known, err := knowledgeEvidenceIDs(records, companionID)
	if err != nil {
		return nil, err
	}
	for _, evidenceID := range cited {
		if !slices.Contains(known, evidenceID) {
			return nil, fmt.Errorf("companion decision cites evidence %s outside authoritative companion knowledge", evidenceID)
		}
	}
	ids := slices.Clone(cited)
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (r *NarrationResolver) exactTurnRef(
	state *graph.EntityState, predicate vocabulary.Predicate, turnID string,
) (content.Ref, error) {
	value, err := exactString(state, predicate)
	if err != nil {
		return content.Ref{}, fmt.Errorf("companion narration %s: %w", predicate, err)
	}
	ref, err := content.ParseRef(value)
	if err != nil {
		return content.Ref{}, fmt.Errorf("parse companion narration %s: %w", predicate, err)
	}
	expectedKey, err := content.KeyFor(predicate, content.SubjectTurn, turnID)
	if err != nil || ref.Instance != r.artifacts.InstanceName() || ref.Key != expectedKey {
		return content.Ref{}, fmt.Errorf("companion narration %s is not the canonical turn artifact reference", predicate)
	}
	return ref, nil
}

func forbidDecisionRef(state *graph.EntityState) error {
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnCompanionDecisionRef.String() {
			return errors.New("no-decision companion narration stage carries a decision reference")
		}
	}
	return nil
}

var _ epistemic.NarrationEvidenceResolver = (*NarrationResolver)(nil)
