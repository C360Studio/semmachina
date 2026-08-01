package epistemic

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func (p *Projector) addKnowledge(ctx context.Context, projection *Projection, actorID string) error {
	recordIDs, err := p.queryIDs(ctx, vocabulary.KnowledgeActorHolder, actorID)
	if err != nil {
		return err
	}
	records, err := p.hydrate(ctx, recordIDs)
	if err != nil {
		return fmt.Errorf("hydrate knowledge grants: %w", err)
	}
	evidenceIDs := make([]string, 0, len(records))
	for _, record := range records {
		if err := requireEntityKind(record, vocabulary.EntityKindKnowledge); err != nil {
			return fmt.Errorf("knowledge authorization record: %w", err)
		}
		holder, err := exactString(record, vocabulary.KnowledgeActorHolder)
		if err != nil {
			return fmt.Errorf("knowledge authorization record: %w", err)
		}
		if holder != actorID {
			return fmt.Errorf("knowledge record %s holder %s does not match queried actor %s",
				record.ID, holder, actorID)
		}
		evidenceID, err := exactString(record, vocabulary.KnowledgeEvidenceRef)
		if err != nil {
			return fmt.Errorf("knowledge authorization record: %w", err)
		}
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	return p.addEvidence(ctx, projection, evidenceIDs, evidencePredicates)
}

func (p *Projector) addRevelations(
	ctx context.Context, projection *Projection, actorID, turnID string,
) error {
	recordIDs, err := p.queryIDs(ctx, vocabulary.RevelationTurnID, turnID)
	if err != nil {
		return err
	}
	records, err := p.hydrate(ctx, recordIDs)
	if err != nil {
		return fmt.Errorf("hydrate revelation receipts: %w", err)
	}
	evidenceIDs := make([]string, 0, len(records))
	for _, record := range records {
		if err := requireEntityKind(record, vocabulary.EntityKindRevelation); err != nil {
			return fmt.Errorf("revelation authorization record: %w", err)
		}
		recordTurnID, err := exactString(record, vocabulary.RevelationTurnID)
		if err != nil {
			return fmt.Errorf("revelation authorization record: %w", err)
		}
		if recordTurnID != turnID {
			return fmt.Errorf("revelation record %s turn %s does not match queried turn %s",
				record.ID, recordTurnID, turnID)
		}
		holder, err := exactString(record, vocabulary.RevelationActorHolder)
		if err != nil {
			return fmt.Errorf("revelation authorization record: %w", err)
		}
		evidenceID, err := exactString(record, vocabulary.RevelationEvidenceRef)
		if err != nil {
			return fmt.Errorf("revelation authorization record: %w", err)
		}
		if holder == actorID {
			evidenceIDs = append(evidenceIDs, evidenceID)
		}
	}
	return p.addEvidence(ctx, projection, evidenceIDs, evidencePredicates)
}

func (p *Projector) verifyCompanionBond(
	ctx context.Context, bondID, characterID, playerID string,
) error {
	bonds, err := p.hydrate(ctx, []string{bondID})
	if err != nil {
		return fmt.Errorf("hydrate companion bonds: %w", err)
	}
	if len(bonds) != 1 {
		return fmt.Errorf("companion bond %s is missing or a referential stub", bondID)
	}
	for _, bond := range bonds {
		if err := requireEntityKind(bond, vocabulary.EntityKindCompanionBond); err != nil {
			return fmt.Errorf("companion bond authorization record: %w", err)
		}
		bondCharacter, err := exactString(bond, vocabulary.CompanionBondCharacter)
		if err != nil {
			return fmt.Errorf("companion bond authorization record: %w", err)
		}
		if bondCharacter != characterID {
			return fmt.Errorf("companion bond %s character %s does not match queried character %s",
				bond.ID, bondCharacter, characterID)
		}
		bondPlayer, err := exactString(bond, vocabulary.CompanionBondPlayer)
		if err != nil {
			return fmt.Errorf("companion bond authorization record: %w", err)
		}
		if bondPlayer == playerID {
			return nil
		}
	}
	return fmt.Errorf("companion %s has no verified bond to player %s", characterID, playerID)
}

func (p *Projector) addCasekeeperState(
	ctx context.Context, projection *Projection, targetActorIDs []string,
) error {
	caseState, err := p.caseState(ctx)
	if err != nil {
		return err
	}
	projection.Neighbours = append(projection.Neighbours, projectState(caseState, casekeeperPredicates))
	references := make([]string, 0)
	for _, triple := range caseState.Triples {
		predicate := vocabulary.Predicate(triple.Predicate)
		if !casekeeperPredicates[predicate] || !vocabulary.IsEntityReference(predicate) {
			continue
		}
		if id, ok := triple.Object.(string); ok {
			references = append(references, id)
		}
	}
	slices.Sort(references)
	references = slices.Compact(references)
	states, err := p.hydrate(ctx, references)
	if err != nil {
		return err
	}
	for _, state := range states {
		projection.Neighbours = append(projection.Neighbours, projectState(state, casekeeperPredicates))
	}
	eligibleTargets := make(map[string]bool)
	for _, triple := range caseState.Triples {
		if triple.Predicate == vocabulary.CaseMemberSuspect.String() {
			if actorID, ok := triple.Object.(string); ok {
				eligibleTargets[actorID] = true
			}
		}
	}
	for _, member := range projection.Members {
		kinds := member.Objects(vocabulary.WorldEntityKind)
		if len(kinds) == 1 && kinds[0] == string(vocabulary.EntityKindCharacter) {
			eligibleTargets[member.ID] = true
		}
	}
	for _, targetActorID := range targetActorIDs {
		if !eligibleTargets[targetActorID] {
			return fmt.Errorf("casekeeper target actor %s is neither a scoped case suspect nor visible actor",
				targetActorID)
		}
	}
	beliefRecords, err := p.scope.beliefRecords(targetActorIDs, p.maxSupplemental)
	if err != nil {
		return err
	}
	beliefIDs := sortedRecordIDs(beliefRecords)
	beliefs, err := p.hydrate(ctx, beliefIDs)
	if err != nil {
		return err
	}
	if len(beliefs) != len(beliefIDs) {
		return fmt.Errorf("scoped case %s is missing one or more authored belief records", caseState.ID)
	}
	caseEvidence := make(map[string]bool)
	for _, triple := range caseState.Triples {
		if triple.Predicate == vocabulary.CaseMemberEvidence.String() {
			if evidenceID, ok := triple.Object.(string); ok {
				caseEvidence[evidenceID] = true
			}
		}
	}
	for _, belief := range beliefs {
		if err := requireEntityKind(belief, vocabulary.EntityKindBelief); err != nil {
			return fmt.Errorf("scoped belief authorization record: %w", err)
		}
		holder, err := exactString(belief, vocabulary.BeliefActorHolder)
		if err != nil {
			return fmt.Errorf("scoped belief authorization record: %w", err)
		}
		if holder != beliefRecords[belief.ID] {
			return fmt.Errorf("scoped belief %s holder %s does not match plan-scoped actor %s",
				belief.ID, holder, beliefRecords[belief.ID])
		}
		evidenceID, err := exactString(belief, vocabulary.BeliefEvidenceRef)
		if err != nil {
			return fmt.Errorf("scoped belief authorization record: %w", err)
		}
		if !caseEvidence[evidenceID] {
			return fmt.Errorf("scoped belief %s evidence %s is not a member of case %s",
				belief.ID, evidenceID, caseState.ID)
		}
		stance, err := exactString(belief, vocabulary.BeliefStanceCurrent)
		if err != nil {
			return fmt.Errorf("scoped belief authorization record: %w", err)
		}
		if _, err := vocabulary.ParseBeliefStance(stance); err != nil {
			return fmt.Errorf("scoped belief %s stance: %w", belief.ID, err)
		}
		projection.Neighbours = append(projection.Neighbours, projectState(belief, casekeeperPredicates))
	}
	return nil
}

func (p *Projector) addEvidence(
	ctx context.Context,
	projection *Projection,
	ids []string,
	allowed map[vocabulary.Predicate]bool,
) error {
	slices.Sort(ids)
	ids = slices.Compact(ids)
	states, err := p.hydrate(ctx, ids)
	if err != nil {
		return err
	}
	for _, state := range states {
		if err := requireEntityKind(state, vocabulary.EntityKindEvidence); err != nil {
			return fmt.Errorf("authorized evidence target: %w", err)
		}
		projection.Neighbours = append(projection.Neighbours, projectState(state, allowed))
	}
	return nil
}

func (p *Projector) solutionOnly(
	ctx context.Context, purpose Purpose, _, _ string,
) (*Projection, error) {
	caseState, err := p.caseState(ctx)
	if err != nil {
		return nil, err
	}
	solution, err := solutionOf(caseState)
	if err != nil {
		return nil, err
	}
	return &Projection{Purpose: purpose, HasSolution: true, Solution: solution}, nil
}

func (p *Projector) authorizeDenouement(
	ctx context.Context, projection *Projection, authorizerRef string,
) error {
	caseState, err := p.caseState(ctx)
	if err != nil {
		return err
	}
	caseEntity := projectState(caseState, casekeeperPredicates)
	phases := caseEntity.Objects(vocabulary.CaseLifecyclePhase)
	if len(phases) != 1 || phases[0] != string(vocabulary.CasePhaseDenouement) {
		return errors.New("denouement projection requires the case lifecycle denouement phase")
	}
	if p.denouement == nil {
		return errors.New("denouement projection requires an accusation authorizer")
	}
	authorized, err := p.denouement.Authorized(ctx, projection.TurnID, caseState.ID, authorizerRef)
	if err != nil {
		return fmt.Errorf("authorize denouement: %w", err)
	}
	if !authorized {
		return errors.New("denouement projection is not authorized by a correct accusation result")
	}
	solution, err := solutionOf(caseState)
	if err != nil {
		return err
	}
	projection.HasSolution = true
	projection.Solution = solution
	return nil
}

func (p *Projector) caseState(ctx context.Context) (graph.EntityState, error) {
	if p.scope.caseID == "" {
		return graph.EntityState{}, errors.New("this world instance has no scoped mystery case")
	}
	states, err := p.hydrate(ctx, []string{p.scope.caseID})
	if err != nil {
		return graph.EntityState{}, err
	}
	if len(states) != 1 {
		return graph.EntityState{}, fmt.Errorf("scoped case %s is missing or a referential stub", p.scope.caseID)
	}
	if err := requireEntityKind(states[0], vocabulary.EntityKindCase); err != nil {
		return graph.EntityState{}, fmt.Errorf("scoped case: %w", err)
	}
	return states[0], nil
}

func solutionOf(state graph.EntityState) (Solution, error) {
	read := func(predicate vocabulary.Predicate) (string, error) {
		var values []string
		for _, triple := range state.Triples {
			if triple.Predicate == predicate.String() {
				if value, ok := triple.Object.(string); ok {
					values = append(values, value)
				}
			}
		}
		if len(values) != 1 {
			return "", fmt.Errorf("case %s has %d values for %s", state.ID, len(values), predicate)
		}
		return values[0], nil
	}
	culprit, err := read(vocabulary.CaseSolutionCulprit)
	if err != nil {
		return Solution{}, err
	}
	method, err := read(vocabulary.CaseSolutionMethod)
	if err != nil {
		return Solution{}, err
	}
	motive, err := read(vocabulary.CaseSolutionMotive)
	if err != nil {
		return Solution{}, err
	}
	return Solution{Culprit: culprit, Method: method, Motive: motive}, nil
}

func exactString(state graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var values []string
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok {
			return "", fmt.Errorf("entity %s records a %T for %s, want string",
				state.ID, triple.Object, predicate)
		}
		values = append(values, value)
	}
	if len(values) != 1 {
		return "", fmt.Errorf("entity %s holds %d values for exact-one %s", state.ID, len(values), predicate)
	}
	return values[0], nil
}

func requireEntityKind(state graph.EntityState, want vocabulary.EntityKind) error {
	kind, err := exactString(state, vocabulary.WorldEntityKind)
	if err != nil {
		return err
	}
	if vocabulary.EntityKind(kind) != want {
		return fmt.Errorf("entity %s has kind %s, want %s", state.ID, kind, want)
	}
	return nil
}

// compile-time assertion that the existing assembler remains the bounded base.
var _ SceneAssembler = (*scene.Assembler)(nil)
