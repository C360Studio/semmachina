package world

import (
	"fmt"
	"slices"
	"sort"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MysterySolution is the immutable authored answer, expressed in local entity
// references until the package is resolved into an instance.
type MysterySolution struct {
	Culprit string
	Method  string
	Motive  string
}

// MysteryTimelineEvent is an ordered event in the canonical case history.
type MysteryTimelineEvent struct {
	Event string
	Order int
}

// MysteryBelief is one named actor's authored stance toward evidence.
type MysteryBelief struct {
	Record   string
	Actor    string
	Evidence string
	Stance   vocabulary.BeliefStance
}

// MysteryKnowledgeSeed grants a character evidence at package import.
type MysteryKnowledgeSeed struct {
	Record   string
	Actor    string
	Evidence string
}

// MysteryCase is the typed projection of one fully validated authored case.
type MysteryCase struct {
	ID                  string
	Victim              string
	Solution            MysterySolution
	Suspects            []string
	Evidence            []string
	Timeline            []MysteryTimelineEvent
	Beliefs             []MysteryBelief
	KnowledgeSeeds      []MysteryKnowledgeSeed
	CompanionCandidates []string
}

func validateMysteryEntities(entities []TemplateEntity) (*MysteryCase, error) {
	byID := make(map[string]TemplateEntity, len(entities))
	var cases []TemplateEntity
	hasMysteryRecords := false
	for _, entity := range entities {
		byID[entity.LocalID] = entity
		switch entity.Kind {
		case vocabulary.EntityKindCase:
			cases = append(cases, entity)
			hasMysteryRecords = true
		case vocabulary.EntityKindEvidence, vocabulary.EntityKindEvent,
			vocabulary.EntityKindBelief, vocabulary.EntityKindKnowledge:
			hasMysteryRecords = true
		}
		for _, fact := range entity.Facts {
			if vocabulary.IsProtectedPredicate(fact.Predicate) {
				hasMysteryRecords = true
			}
		}
	}
	if !hasMysteryRecords {
		return nil, nil
	}
	if len(cases) != 1 {
		return nil, fmt.Errorf("mystery package declares %d case entities; exactly one is required", len(cases))
	}
	return validateMysteryCase(cases[0], entities, byID)
}

func validateMysteryCase(
	caseEntity TemplateEntity, entities []TemplateEntity, byID map[string]TemplateEntity,
) (*MysteryCase, error) {
	culprit, err := exactlyOneRef(caseEntity, vocabulary.CaseSolutionCulprit, "solution culprit")
	if err != nil {
		return nil, err
	}
	method, err := exactlyOneRef(caseEntity, vocabulary.CaseSolutionMethod, "solution method")
	if err != nil {
		return nil, err
	}
	motive, err := exactlyOneRef(caseEntity, vocabulary.CaseSolutionMotive, "solution motive")
	if err != nil {
		return nil, err
	}
	victim, err := exactlyOneRef(caseEntity, vocabulary.CaseMemberVictim, "victim")
	if err != nil {
		return nil, err
	}
	suspectCount, err := exactlyOneInt(caseEntity, vocabulary.CaseRequirementSuspects, "required suspects")
	if err != nil {
		return nil, err
	}
	evidenceCount, err := exactlyOneInt(caseEntity, vocabulary.CaseRequirementEvidence, "required evidence")
	if err != nil {
		return nil, err
	}
	suspects, err := uniqueRefs(caseEntity, vocabulary.CaseMemberSuspect, "suspects")
	if err != nil {
		return nil, err
	}
	evidence, err := uniqueRefs(caseEntity, vocabulary.CaseMemberEvidence, "evidence")
	if err != nil {
		return nil, err
	}
	timelineRefs, err := uniqueRefs(caseEntity, vocabulary.CaseMemberTimeline, "timeline")
	if err != nil {
		return nil, err
	}
	if len(suspects) != suspectCount {
		return nil, fmt.Errorf("case %q suspects: declares %d members, requires %d",
			caseEntity.LocalID, len(suspects), suspectCount)
	}
	if len(evidence) != evidenceCount {
		return nil, fmt.Errorf("case %q evidence: declares %d members, requires %d",
			caseEntity.LocalID, len(evidence), evidenceCount)
	}
	if len(timelineRefs) == 0 {
		return nil, fmt.Errorf("case %q timeline: at least one event is required", caseEntity.LocalID)
	}
	if err := requireRefs(byID, suspects, vocabulary.EntityKindCharacter, "suspects"); err != nil {
		return nil, err
	}
	if err := requireRefs(byID, evidence, vocabulary.EntityKindEvidence, "evidence"); err != nil {
		return nil, err
	}
	if err := requireRefs(byID, timelineRefs, vocabulary.EntityKindEvent, "timeline"); err != nil {
		return nil, err
	}
	if err := requireRef(byID, culprit, vocabulary.EntityKindCharacter, "culprit"); err != nil {
		return nil, err
	}
	if err := requireRef(byID, victim, vocabulary.EntityKindCharacter, "victim"); err != nil {
		return nil, err
	}
	if !slices.Contains(suspects, culprit) {
		return nil, fmt.Errorf("solution culprit %q is not a declared suspect", culprit)
	}
	if err := requireRef(byID, method, vocabulary.EntityKindItem, "method"); err != nil {
		return nil, err
	}
	if err := requireRef(byID, motive, vocabulary.EntityKindEvidence, "motive"); err != nil {
		return nil, err
	}
	if !slices.Contains(evidence, motive) {
		return nil, fmt.Errorf("solution motive %q is not declared case evidence", motive)
	}

	timeline, err := validateMysteryTimeline(caseEntity.LocalID, timelineRefs, byID)
	if err != nil {
		return nil, err
	}
	if err := validateMysteryEvidence(
		caseEntity.LocalID, evidence, timelineRefs, entities, byID,
	); err != nil {
		return nil, err
	}

	beliefs, knowledge, companions, err := validateMysteryRecords(entities, byID, evidence)
	if err != nil {
		return nil, err
	}
	if len(beliefs) == 0 {
		return nil, fmt.Errorf("case %q requires at least one authored belief", caseEntity.LocalID)
	}
	if len(knowledge) == 0 {
		return nil, fmt.Errorf("case %q requires at least one authored knowledge seed", caseEntity.LocalID)
	}
	if len(companions) == 0 {
		return nil, fmt.Errorf("case %q requires at least one valid companion candidate", caseEntity.LocalID)
	}

	return &MysteryCase{
		ID:                  caseEntity.LocalID,
		Victim:              victim,
		Solution:            MysterySolution{Culprit: culprit, Method: method, Motive: motive},
		Suspects:            suspects,
		Evidence:            evidence,
		Timeline:            timeline,
		Beliefs:             beliefs,
		KnowledgeSeeds:      knowledge,
		CompanionCandidates: companions,
	}, nil
}

func validateMysteryTimeline(
	caseID string, timelineRefs []string, byID map[string]TemplateEntity,
) ([]MysteryTimelineEvent, error) {
	timeline := make([]MysteryTimelineEvent, 0, len(timelineRefs))
	for _, eventID := range timelineRefs {
		order, err := exactlyOneInt(byID[eventID], vocabulary.CaseTimelineOrder, "timeline order")
		if err != nil {
			return nil, err
		}
		timeline = append(timeline, MysteryTimelineEvent{Event: eventID, Order: order})
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Order < timeline[j].Order })
	for index, event := range timeline {
		if event.Order != index+1 {
			return nil, fmt.Errorf("case %q timeline order: got %d at sorted position %d; want contiguous 1..%d",
				caseID, event.Order, index+1, len(timeline))
		}
	}
	return timeline, nil
}

func validateMysteryEvidence(
	caseID string,
	evidence []string,
	timelineRefs []string,
	entities []TemplateEntity,
	byID map[string]TemplateEntity,
) error {
	redHerrings := 0
	for _, evidenceID := range evidence {
		statusText, err := exactlyOneLiteral(byID[evidenceID], vocabulary.EvidenceTruthStatusCurrent,
			"evidence truth status")
		if err != nil {
			return err
		}
		status, err := vocabulary.ParseEvidenceTruthStatus(statusText)
		if err != nil {
			return fmt.Errorf("evidence %q truth status: %w", evidenceID, err)
		}
		if status == vocabulary.EvidenceTruthRedHerring {
			redHerrings++
		}
	}
	if redHerrings == 0 {
		return fmt.Errorf("case %q evidence includes no red herrings", caseID)
	}
	for _, entity := range entities {
		if entity.Kind == vocabulary.EntityKindEvidence && !slices.Contains(evidence, entity.LocalID) {
			return fmt.Errorf("evidence entity %q is outside case %q evidence membership", entity.LocalID, caseID)
		}
		if entity.Kind == vocabulary.EntityKindEvent && !slices.Contains(timelineRefs, entity.LocalID) {
			return fmt.Errorf("timeline event %q is outside case %q timeline membership", entity.LocalID, caseID)
		}
	}
	return nil
}

func validateMysteryRecords(
	entities []TemplateEntity,
	byID map[string]TemplateEntity,
	evidence []string,
) ([]MysteryBelief, []MysteryKnowledgeSeed, []string, error) {
	beliefs := make([]MysteryBelief, 0)
	knowledge := make([]MysteryKnowledgeSeed, 0)
	companions := make([]string, 0)
	for _, entity := range entities {
		switch entity.Kind {
		case vocabulary.EntityKindBelief:
			actor, err := exactlyOneRef(entity, vocabulary.BeliefActorHolder, "belief actor")
			if err != nil {
				return nil, nil, nil, err
			}
			evidenceID, err := exactlyOneRef(entity, vocabulary.BeliefEvidenceRef, "belief evidence")
			if err != nil {
				return nil, nil, nil, err
			}
			stanceText, err := exactlyOneLiteral(entity, vocabulary.BeliefStanceCurrent, "belief stance")
			if err != nil {
				return nil, nil, nil, err
			}
			stance, err := vocabulary.ParseBeliefStance(stanceText)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("belief %q stance: %w", entity.LocalID, err)
			}
			if err := requireRef(byID, actor, vocabulary.EntityKindCharacter, "belief actor"); err != nil {
				return nil, nil, nil, err
			}
			if !slices.Contains(evidence, evidenceID) {
				return nil, nil, nil, fmt.Errorf(
					"belief %q references evidence %q outside the case", entity.LocalID, evidenceID)
			}
			beliefs = append(beliefs, MysteryBelief{
				Record: entity.LocalID, Actor: actor, Evidence: evidenceID, Stance: stance,
			})
		case vocabulary.EntityKindKnowledge:
			actor, err := exactlyOneRef(entity, vocabulary.KnowledgeActorHolder, "knowledge actor")
			if err != nil {
				return nil, nil, nil, err
			}
			evidenceID, err := exactlyOneRef(entity, vocabulary.KnowledgeEvidenceRef, "knowledge evidence")
			if err != nil {
				return nil, nil, nil, err
			}
			if err := requireRef(byID, actor, vocabulary.EntityKindCharacter, "knowledge actor"); err != nil {
				return nil, nil, nil, err
			}
			if !slices.Contains(evidence, evidenceID) {
				return nil, nil, nil, fmt.Errorf(
					"knowledge %q references evidence %q outside the case", entity.LocalID, evidenceID)
			}
			knowledge = append(knowledge, MysteryKnowledgeSeed{
				Record: entity.LocalID, Actor: actor, Evidence: evidenceID,
			})
		case vocabulary.EntityKindCharacter:
			candidate, err := companionCandidate(entity)
			if err != nil {
				return nil, nil, nil, err
			}
			if candidate {
				companions = append(companions, entity.LocalID)
			}
		}
	}
	return beliefs, knowledge, companions, nil
}

func factsFor(entity TemplateEntity, predicate vocabulary.Predicate) []TemplateFact {
	var facts []TemplateFact
	for _, fact := range entity.Facts {
		if fact.Predicate == predicate {
			facts = append(facts, fact)
		}
	}
	return facts
}

func exactlyOneRef(entity TemplateEntity, predicate vocabulary.Predicate, field string) (string, error) {
	facts := factsFor(entity, predicate)
	if len(facts) != 1 || !facts[0].IsReference() {
		return "", fmt.Errorf("entity %q %s: requires exactly one reference, got %d",
			entity.LocalID, field, len(facts))
	}
	return facts[0].LocalRef, nil
}

func exactlyOneInt(entity TemplateEntity, predicate vocabulary.Predicate, field string) (int, error) {
	facts := factsFor(entity, predicate)
	if len(facts) != 1 {
		return 0, fmt.Errorf("entity %q %s: requires exactly one value, got %d",
			entity.LocalID, field, len(facts))
	}
	value, ok := facts[0].Literal.(int)
	if !ok {
		return 0, fmt.Errorf("entity %q %s: expected an integer, got %T", entity.LocalID, field, facts[0].Literal)
	}
	return value, nil
}

func exactlyOneLiteral(entity TemplateEntity, predicate vocabulary.Predicate, field string) (string, error) {
	facts := factsFor(entity, predicate)
	if len(facts) != 1 {
		return "", fmt.Errorf("entity %q %s: requires exactly one value, got %d",
			entity.LocalID, field, len(facts))
	}
	value, ok := facts[0].Literal.(string)
	if !ok {
		return "", fmt.Errorf("entity %q %s: expected text, got %T", entity.LocalID, field, facts[0].Literal)
	}
	return value, nil
}

func uniqueRefs(entity TemplateEntity, predicate vocabulary.Predicate, field string) ([]string, error) {
	facts := factsFor(entity, predicate)
	if len(facts) == 0 {
		return nil, fmt.Errorf("entity %q %s: requires at least one reference", entity.LocalID, field)
	}
	refs := make([]string, 0, len(facts))
	seen := make(map[string]bool, len(facts))
	for _, fact := range facts {
		if !fact.IsReference() {
			return nil, fmt.Errorf("entity %q %s contains a non-reference", entity.LocalID, field)
		}
		if seen[fact.LocalRef] {
			return nil, fmt.Errorf("entity %q %s names %q more than once", entity.LocalID, field, fact.LocalRef)
		}
		seen[fact.LocalRef] = true
		refs = append(refs, fact.LocalRef)
	}
	return refs, nil
}

func requireRefs(byID map[string]TemplateEntity, refs []string, kind vocabulary.EntityKind, field string) error {
	for _, ref := range refs {
		if err := requireRef(byID, ref, kind, field); err != nil {
			return err
		}
	}
	return nil
}

func requireRef(byID map[string]TemplateEntity, ref string, kind vocabulary.EntityKind, field string) error {
	entity, ok := byID[ref]
	if !ok {
		return fmt.Errorf("%s references missing entity %q", field, ref)
	}
	if entity.Kind != kind {
		return fmt.Errorf("%s references %q, which is %q rather than %q", field, ref, entity.Kind, kind)
	}
	return nil
}

func companionCandidate(entity TemplateEntity) (bool, error) {
	facts := factsFor(entity, vocabulary.CompanionCandidatePolicy)
	if len(facts) == 0 {
		return false, nil
	}
	if len(facts) != 1 {
		return false, fmt.Errorf(
			"character %q companion candidate policy requires exactly one value, got %d",
			entity.LocalID, len(facts))
	}
	policy, ok := facts[0].Literal.(string)
	if !ok {
		return false, fmt.Errorf("character %q companion candidate policy is a %T, want a closed string value",
			entity.LocalID, facts[0].Literal)
	}
	if _, err := vocabulary.ParseCompanionPolicy(policy); err != nil {
		return false, fmt.Errorf("character %q companion candidate policy: %w", entity.LocalID, err)
	}
	return true, nil
}
