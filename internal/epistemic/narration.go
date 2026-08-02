package epistemic

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// NarrationEvidenceRequest binds companion-cited narration evidence to the
// already authenticated turn, player, and structural context.
type NarrationEvidenceRequest struct {
	TurnID, TurnEntityID, PlayerID, ContextRef string
	Purpose                                    Purpose
}

// NarrationEvidenceResolver is the companion-owned authorization boundary for
// evidence cited by a committed companion decision. It returns identities only;
// Projector remains the sole entity hydration and predicate-filtering boundary.
type NarrationEvidenceResolver interface {
	AuthorizedNarrationEvidence(context.Context, NarrationEvidenceRequest) ([]string, error)
}

// WithNarrationEvidenceResolver installs the committed companion-evidence gate.
func WithNarrationEvidenceResolver(resolver NarrationEvidenceResolver) Option {
	return func(p *Projector) { p.narrationEvidence = resolver }
}

func (p *Projector) addNarrationEvidence(
	ctx context.Context, projection *Projection, audience AuthenticatedAudience,
) error {
	if p.narrationEvidence == nil {
		if len(projection.Turn.Objects(vocabulary.TurnCompanionStageRef)) == 0 {
			return nil
		}
		return errors.New("narrator projection requires the companion narration evidence resolver")
	}
	ids, err := p.narrationEvidence.AuthorizedNarrationEvidence(ctx, NarrationEvidenceRequest{
		TurnID: audience.turnID, TurnEntityID: audience.turnEntityID,
		PlayerID: projection.Actor.PlayerID, ContextRef: projection.SceneID, Purpose: audience.purpose,
	})
	if err != nil {
		return fmt.Errorf("authorize companion narration evidence: %w", err)
	}
	if !slices.IsSorted(ids) {
		return errors.New("companion narration evidence resolver returned unsorted identities")
	}
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return errors.New("companion narration evidence resolver returned duplicate identities")
	}
	if audience.purpose == PurposeNarrator && len(ids) != 0 && p.scope.caseID != "" {
		caseState, err := p.caseState(ctx)
		if err != nil {
			return err
		}
		solution, err := solutionOf(caseState)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == solution.Culprit || id == solution.Method || id == solution.Motive {
				return fmt.Errorf("ordinary narrator companion evidence cites locked solution identity %s", id)
			}
		}
	}
	states, err := p.hydrate(ctx, slices.Clone(ids))
	if err != nil {
		return err
	}
	if len(states) != len(ids) {
		return errors.New("one or more authorized companion narration evidence entities are missing")
	}
	allowed := map[vocabulary.Predicate]bool{
		vocabulary.WorldEntityName: true, vocabulary.WorldEntityKind: true,
		vocabulary.WorldEntityDescription: true,
	}
	for _, state := range states {
		if err := requireEntityKind(state, vocabulary.EntityKindEvidence); err != nil {
			return fmt.Errorf("authorized companion narration evidence: %w", err)
		}
		projection.Neighbours = append(projection.Neighbours, projectState(state, allowed))
	}
	return nil
}
