package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ReadGraph is the authoritative preflight read surface.
type ReadGraph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	EntitiesByPredicateValue(context.Context, string, string, int) ([]string, error)
}

// DecisionStore resolves the private case-decision artifact.
type DecisionStore interface {
	GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error)
}

// LoadResult is either a complete mystery preflight or a deterministic no-op.
type LoadResult struct {
	TurnID     string
	Applicable bool
	Preflight  Preflight
}

// Loader reads every authorization input before Granter performs its first write.
type Loader struct {
	graph  ReadGraph
	store  DecisionStore
	caseID string
}

// NewLoader builds the complete read-only preflight resolver for one case.
func NewLoader(graphReader ReadGraph, decisions DecisionStore, caseID string) (*Loader, error) {
	if graphReader == nil || decisions == nil {
		return nil, errors.New("knowledge loader requires graph and decision stores")
	}
	return &Loader{graph: graphReader, store: decisions, caseID: caseID}, nil
}

// Load resolves one turn into a complete authorization snapshot or a
// deterministic non-mystery no-op before the granter may write anything.
func (l *Loader) Load(ctx context.Context, turnEntityID string) (LoadResult, error) {
	turnState, err := l.graph.GetEntity(ctx, turnEntityID)
	if err != nil {
		return LoadResult{}, err
	}
	turnID := entityInstance(turnEntityID)
	refText, err := soleString(turnState, vocabulary.TurnCaseDecisionRef)
	if err != nil {
		return LoadResult{}, err
	}
	ref, err := content.ParseRef(refText)
	if err != nil {
		return LoadResult{}, err
	}
	record, err := l.store.GetCaseDecisionRecord(ctx, ref)
	if err != nil {
		return LoadResult{}, err
	}
	// The object-store reference is carried by this turn, but possession of the
	// reference is not identity proof. A valid record for another turn can pass
	// its own Validate and still authorize writes under this trigger unless both
	// envelope and decision identities are rebound here, before any additional
	// reads and before the granter is reachable.
	if record == nil {
		return LoadResult{}, errors.New("case decision store returned a nil record")
	}
	if record.TurnID != turnID {
		return LoadResult{}, reject(ReasonWrongTurn,
			"decision record turn %s does not match triggered turn %s", record.TurnID, turnID)
	}
	if record.Decision != nil && record.Decision.TurnID != turnID {
		return LoadResult{}, reject(ReasonWrongTurn,
			"decision turn %s does not match triggered turn %s", record.Decision.TurnID, turnID)
	}
	if record.Status == payload.CaseDecisionStatusNotApplicable {
		return LoadResult{TurnID: turnID}, nil
	}
	decision := record.Decision
	if decision == nil {
		return LoadResult{}, errors.New("decision record is applicable but has no decision")
	}
	playerID, err := soleString(turnState, vocabulary.TurnActionPlayer)
	if err != nil {
		return LoadResult{}, err
	}
	player, err := l.graph.GetEntity(ctx, playerID)
	if err != nil {
		return LoadResult{}, err
	}
	actorID, err := soleString(player, vocabulary.PlayerCharacterCurrent)
	if err != nil {
		return LoadResult{}, err
	}
	caseState, err := l.graph.GetEntity(ctx, l.caseID)
	if err != nil {
		return LoadResult{}, err
	}
	phase, err := currentCasePhase(caseState)
	if err != nil {
		return LoadResult{}, err
	}
	input := Preflight{
		Decision: decision, ActingActorID: actorID, CaseID: l.caseID, CasePhase: phase,
		CaseEvidence: map[string]bool{}, CaseCharacters: map[string]bool{},
		Eligibility: map[string]eligibility{}, Beliefs: map[beliefKey]AuthoredBelief{},
		Known: map[knowledgeKey]bool{}, SolutionLocked: map[string]bool{},
	}
	for _, value := range objects(caseState, vocabulary.CaseMemberEvidence) {
		input.CaseEvidence[value] = true
	}
	for _, predicate := range []vocabulary.Predicate{vocabulary.CaseMemberSuspect, vocabulary.CaseMemberVictim} {
		for _, value := range objects(caseState, predicate) {
			input.CaseCharacters[value] = true
		}
	}
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.CaseSolutionCulprit, vocabulary.CaseSolutionMethod, vocabulary.CaseSolutionMotive,
	} {
		for _, value := range objects(caseState, predicate) {
			input.SolutionLocked[value] = true
		}
	}
	if err := l.loadEligibility(ctx, decision.RevealRefs, &input); err != nil {
		return LoadResult{}, err
	}
	if decision.Kind == payload.CaseDecisionQuestion && len(decision.TargetRefs) == 1 {
		if err := l.loadBeliefs(ctx, decision.TargetRefs[0], &input); err != nil {
			return LoadResult{}, err
		}
	}
	if decision.Kind == payload.CaseDecisionShare {
		if err := l.loadKnown(ctx, actorID, &input); err != nil {
			return LoadResult{}, err
		}
	}
	return LoadResult{TurnID: turnID, Applicable: true, Preflight: input}, nil
}

func currentCasePhase(state *graph.EntityState) (vocabulary.CasePhase, error) {
	values := objects(state, vocabulary.CaseLifecyclePhase)
	if len(values) == 0 {
		return "", fmt.Errorf("entity %s holds no values for %s", state.ID, vocabulary.CaseLifecyclePhase)
	}
	phases := vocabulary.CasePhases()
	currentRank := -1
	var current vocabulary.CasePhase
	for _, value := range values {
		phase, err := vocabulary.ParseCasePhase(value)
		if err != nil {
			return "", err
		}
		for rank, candidate := range phases {
			if candidate == phase && rank > currentRank {
				current, currentRank = phase, rank
			}
		}
	}
	return current, nil
}

func (l *Loader) loadEligibility(ctx context.Context, evidenceIDs []string, input *Preflight) error {
	for _, evidenceID := range evidenceIDs {
		evidence, err := l.graph.GetEntity(ctx, evidenceID)
		if err != nil {
			if errors.Is(err, graphio.ErrEntityNotFound) {
				continue
			}
			return err
		}
		phaseValue, err := soleString(evidence, vocabulary.EvidenceRevealPhase)
		if err != nil {
			return err
		}
		minimum, err := vocabulary.ParseCasePhase(phaseValue)
		if err != nil {
			return err
		}
		resolved := eligibility{MinimumPhase: minimum, Targets: objects(evidence, vocabulary.EvidenceRevealTarget)}
		for _, value := range objects(evidence, vocabulary.EvidenceRevealKindPredicate) {
			kind, err := vocabulary.ParseEvidenceRevealKind(value)
			if err != nil {
				return err
			}
			resolved.Kinds = append(resolved.Kinds, kind)
		}
		input.Eligibility[evidenceID] = resolved
	}
	return nil
}

func (l *Loader) loadBeliefs(ctx context.Context, actorID string, input *Preflight) error {
	beliefIDs, err := l.graph.EntitiesByPredicateValue(ctx, vocabulary.BeliefActorHolder.String(), actorID, 64)
	if err != nil {
		return err
	}
	for _, beliefID := range beliefIDs {
		belief, err := l.graph.GetEntity(ctx, beliefID)
		if err != nil {
			return err
		}
		evidenceID, err := soleString(belief, vocabulary.BeliefEvidenceRef)
		if err != nil {
			return err
		}
		stanceText, err := soleString(belief, vocabulary.BeliefStanceCurrent)
		if err != nil {
			return err
		}
		stance, err := vocabulary.ParseBeliefStance(stanceText)
		if err != nil {
			return err
		}
		prose, err := soleString(belief, vocabulary.WorldEntityDescription)
		if err != nil {
			return err
		}
		holder, err := soleString(belief, vocabulary.BeliefActorHolder)
		if err != nil {
			return err
		}
		input.Beliefs[beliefKey{ActorID: holder, EvidenceID: evidenceID}] = AuthoredBelief{
			ID: beliefID, HolderID: holder, EvidenceID: evidenceID, Stance: stance, Prose: prose,
		}
	}
	return nil
}

func (l *Loader) loadKnown(ctx context.Context, actorID string, input *Preflight) error {
	knowledgeIDs, err := l.graph.EntitiesByPredicateValue(ctx,
		vocabulary.KnowledgeActorHolder.String(), actorID, content.MaxKnowledgeReceiptEntries)
	if err != nil {
		return err
	}
	for _, knowledgeID := range knowledgeIDs {
		state, err := l.graph.GetEntity(ctx, knowledgeID)
		if err != nil {
			return err
		}
		evidenceID, err := soleString(state, vocabulary.KnowledgeEvidenceRef)
		if err != nil {
			return err
		}
		input.Known[knowledgeKey{ActorID: actorID, EvidenceID: evidenceID}] = true
	}
	return nil
}

func soleString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	values := objects(state, predicate)
	if len(values) != 1 {
		return "", fmt.Errorf("entity %s holds %d values for %s", state.ID, len(values), predicate)
	}
	return values[0], nil
}

func objects(state *graph.EntityState, predicate vocabulary.Predicate) []string {
	var out []string
	if state == nil {
		return out
	}
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			if value, ok := triple.Object.(string); ok {
				out = append(out, value)
			}
		}
	}
	return out
}

func entityInstance(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
