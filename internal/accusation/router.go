package accusation

import (
	"context"
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ProjectionService is the stage spawner's existing projection seam.
type ProjectionService interface {
	Project(context.Context, epistemic.AuthenticatedAudience) (*epistemic.Projection, error)
}

// NarrationRouter is a boot-only adapter. It upgrades only a narrator request
// carrying a valid, identity-bound correct result; Projector independently
// rechecks denouement phase and the real DenouementAuthorizer. Companion-cited
// evidence stays inside Projector's separately injected companion resolver;
// this accusation adapter never reads or augments that evidence.
type NarrationRouter struct {
	graph  ReadGraph
	store  RecordStore
	next   ProjectionService
	caseID string
}

// NewNarrationRouter builds the narrow narrator-purpose adapter.
func NewNarrationRouter(graphReader ReadGraph, artifacts RecordStore, next ProjectionService,
	caseID string) (*NarrationRouter, error) {
	if graphReader == nil || artifacts == nil || next == nil || caseID == "" {
		return nil, errors.New("narration router requires graph, result store, projector, and scoped case")
	}
	return &NarrationRouter{graph: graphReader, store: artifacts, next: next, caseID: caseID}, nil
}

// Project delegates every non-narrator purpose unchanged. Missing accusation
// state and a valid incorrect result remain ordinary narration; ambiguous,
// malformed, unreadable, or foreign state fails closed.
func (r *NarrationRouter) Project(ctx context.Context, audience epistemic.AuthenticatedAudience) (*epistemic.Projection, error) {
	if audience.Purpose() != epistemic.PurposeNarrator {
		return r.next.Project(ctx, audience)
	}
	turnID, turnEntityID := audience.TurnIdentity()
	state, err := r.graph.GetEntity(ctx, turnEntityID)
	if err != nil {
		return nil, fmt.Errorf("route narration accusation state: %w", err)
	}
	refs := stringObjects(state, vocabulary.TurnAccusationRef)
	if len(refs) == 0 {
		return r.next.Project(ctx, audience)
	}
	if len(refs) != 1 {
		return nil, errors.New("narration accusation state is partial or ambiguous")
	}
	ref, err := content.ParseRef(refs[0])
	if err != nil || ref.IsZero() {
		return nil, errors.New("narration accusation reference is malformed")
	}
	expectedKey, err := content.KeyFor(vocabulary.TurnAccusationRef, content.SubjectTurn, turnID)
	if err != nil || ref.Key != expectedKey {
		return nil, errors.New("narration accusation reference belongs to a foreign turn")
	}
	record, err := r.store.GetAccusationRecord(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("read narration accusation record: %w", err)
	}
	if record == nil {
		return nil, errors.New("accusation store returned nil narration record")
	}
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("narration accusation record: %w", err)
	}
	if record.TurnID != turnID {
		return nil, errors.New("narration accusation result has foreign or mismatched identity")
	}
	if record.Status == content.AccusationNotApplicable {
		return r.next.Project(ctx, audience)
	}
	result := record.Result
	if result == nil || result.CaseID != r.caseID {
		return nil, errors.New("narration accusation result has foreign or mismatched identity")
	}
	switch result.Outcome {
	case payload.AccusationIncorrect:
		return r.next.Project(ctx, audience)
	case payload.AccusationCorrect:
		return r.next.Project(ctx, epistemic.DenouementAudience(turnID, turnEntityID, r.caseID, ref.String()))
	default:
		return nil, errors.New("narration accusation result has an unknown outcome")
	}
}
