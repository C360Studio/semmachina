package accusation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// AuthorizationGraph binds a reference back to exactly one turn entity.
type AuthorizationGraph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	EntitiesByPredicateValue(context.Context, string, string, int) ([]string, error)
}

// DenouementAuthorizer validates the exact committed correct result used by
// epistemic.Projector's separate lifecycle-phase gate.
type DenouementAuthorizer struct {
	graph AuthorizationGraph
	store RecordStore
}

// NewDenouementAuthorizer builds the production accusation-result gate.
func NewDenouementAuthorizer(graphReader AuthorizationGraph, artifacts RecordStore) (*DenouementAuthorizer, error) {
	if graphReader == nil || artifacts == nil {
		return nil, errors.New("denouement authorizer requires graph and accusation result store")
	}
	return &DenouementAuthorizer{graph: graphReader, store: artifacts}, nil
}

// Authorized proves reference ownership plus turn, case, result, and correct-outcome identity.
func (a *DenouementAuthorizer) Authorized(ctx context.Context, turnID, caseID, refText string) (bool, error) {
	ref, err := content.ParseRef(refText)
	if err != nil {
		return false, fmt.Errorf("invalid denouement accusation reference: %w", err)
	}
	if ref.IsZero() {
		return false, errors.New("invalid denouement accusation reference: empty reference")
	}
	expectedKey, err := content.KeyFor(vocabulary.TurnAccusationRef, content.SubjectTurn, turnID)
	if err != nil || ref.Key != expectedKey {
		return false, errors.New("denouement accusation reference is not the turn's deterministic result slot")
	}
	ids, err := a.graph.EntitiesByPredicateValue(ctx, vocabulary.TurnAccusationRef.String(), refText, 2)
	if err != nil {
		return false, fmt.Errorf("find turn holding accusation reference: %w", err)
	}
	if len(ids) != 1 {
		return false, fmt.Errorf("accusation reference is held by %d turns; want exactly one", len(ids))
	}
	parts := strings.Split(ids[0], ".")
	if len(parts) != 6 || parts[4] != "turn" || parts[5] != turnID {
		return false, errors.New("accusation reference is committed on a foreign turn")
	}
	state, err := a.graph.GetEntity(ctx, ids[0])
	if err != nil {
		return false, fmt.Errorf("read accusation turn: %w", err)
	}
	residentRef, err := soleObject(state, vocabulary.TurnAccusationRef)
	if err != nil || residentRef != refText {
		return false, errors.New("turn does not hold the supplied accusation reference exactly")
	}
	record, err := a.store.GetAccusationRecord(ctx, ref)
	if err != nil {
		return false, fmt.Errorf("read denouement accusation record: %w", err)
	}
	if record == nil {
		return false, errors.New("accusation store returned nil record")
	}
	if err := record.Validate(); err != nil {
		return false, fmt.Errorf("stored accusation record: %w", err)
	}
	if record.Status != content.AccusationResultRecorded || record.Result == nil {
		return false, errors.New("stored accusation record carries no result")
	}
	result := record.Result
	if result.TurnID != turnID || result.CaseID != caseID || result.Outcome != payload.AccusationCorrect {
		return false, errors.New("stored accusation result does not authorize this turn and case")
	}
	return true, nil
}
