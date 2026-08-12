// Package projectioncontract declares every graph projection semmachina writes.
package projectioncontract

import (
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Target names one complete predicate group reconciled by a writer.
type Target struct {
	Contract string
	Group    string
}

// Projection contract names identify complete predicate groups reconciled by writers.
const (
	CampaignBirthContract        = "campaign-birth"
	CampaignStateContract        = "campaign-state"
	TurnBirthContract            = "turn-birth"
	TurnStateContract            = "turn-state"
	TurnCompanionTriggerContract = "turn-companion-trigger"
	TurnCompanionResultContract  = "turn-companion-result"
	PlayerStateContract          = "player-state"
	CompanionBondContract        = "companion-bond"
	CaseLifecycleContract        = "case-lifecycle"
	EffectTargetContract         = "effect-target-state"
	KnowledgeBirthContract       = "knowledge-birth"
	RevelationBirthContract      = "revelation-birth"
)

// Projection targets identify a contract and one of its predicate groups.
var (
	CampaignImport            = Target{CampaignStateContract, "import"}
	TurnPhaseState            = Target{TurnStateContract, "phase-state"}
	TurnAccusation            = Target{TurnStateContract, "accusation"}
	TurnCaseProgress          = Target{TurnStateContract, "case-progress"}
	TurnKnowledge             = Target{TurnStateContract, "knowledge"}
	TurnNarration             = Target{TurnStateContract, "narration"}
	TurnResume                = Target{TurnStateContract, "resume"}
	TurnCaseDecision          = Target{TurnStateContract, "case-decision"}
	TurnVerdict               = Target{TurnStateContract, "verdict"}
	TurnRoll                  = Target{TurnStateContract, "roll"}
	TurnEffectMarker          = Target{TurnStateContract, "effect-marker"}
	TurnCompanionTrigger      = Target{TurnCompanionTriggerContract, "trigger"}
	TurnCompanionResult       = Target{TurnCompanionResultContract, "result"}
	PlayerCurrentTurn         = Target{PlayerStateContract, "current-turn"}
	PlayerResolvedTurn        = Target{PlayerStateContract, "resolved-turn"}
	CompanionBondHint         = Target{CompanionBondContract, "hint"}
	CaseLifecycleReceipt      = Target{CaseLifecycleContract, "lifecycle-receipt"}
	EffectTargetAttributes    = Target{EffectTargetContract, "attributes"}
	EffectTargetStatus        = Target{EffectTargetContract, "status"}
	EffectTargetLocation      = Target{EffectTargetContract, "location"}
	EffectTargetRelationships = Target{EffectTargetContract, "relationships"}
)

func strings(predicates ...vocabulary.Predicate) []string {
	out := make([]string, len(predicates))
	for i, predicate := range predicates {
		out[i] = predicate.String()
	}
	return out
}

func group(name string, predicates ...vocabulary.Predicate) projection.PredicateGroup {
	return projection.PredicateGroup{Name: name, Mode: projection.ModeReconcile, Predicates: strings(predicates...)}
}

// Contracts returns a fresh complete contract set for one mutation client.
func Contracts() []projection.Contract {
	turnPattern := "*.semmachina.*.*.turn.*"
	worldType := payload.Domain + "." + payload.CategoryWorldEntity + "." + payload.SchemaVersion
	turnType := payload.Domain + "." + payload.CategoryTurnState + "." + payload.SchemaVersion
	campaignType := payload.Domain + "." + payload.CategoryCampaignEntity + "." + payload.SchemaVersion
	return []projection.Contract{
		{Name: CampaignBirthContract, MessageType: campaignType, EntityPattern: "*.semmachina.*.*.campaign.*",
			BirthPredicates: strings(vocabulary.CampaignSeedValue, vocabulary.CampaignExperiencePersonaPack, vocabulary.CampaignExperienceMechanicsPack)},
		{Name: CampaignStateContract, MessageType: campaignType, EntityPattern: "*.semmachina.*.*.campaign.*",
			Groups: []projection.PredicateGroup{group("import", vocabulary.CampaignImportCompleted)}},
		{Name: TurnBirthContract, MessageType: turnType, EntityPattern: turnPattern,
			BirthPredicates: strings(vocabulary.TurnPhaseCurrent, vocabulary.TurnActionPlayer, vocabulary.TurnActionScene, vocabulary.TurnActionRef)},
		{Name: TurnStateContract, MessageType: turnType, EntityPattern: turnPattern, Groups: []projection.PredicateGroup{
			group("phase-state", vocabulary.TurnPhaseCurrent, vocabulary.TurnFailureReason, vocabulary.TurnFailureRef),
			group("accusation", vocabulary.TurnAccusationRef), group("case-progress", vocabulary.TurnCaseProgressRef),
			group("knowledge", vocabulary.TurnKnowledgeRef), group("narration", vocabulary.TurnNarrationRef),
			group("resume", vocabulary.TurnResumeAttempts),
			group("case-decision", vocabulary.TurnCaseDecisionRef, vocabulary.TurnCaseDecisionKind),
			group("verdict", vocabulary.TurnVerdictPlausibility, vocabulary.TurnVerdictRisk, vocabulary.TurnVerdictConsequence, vocabulary.TurnVerdictRequiresRoll, vocabulary.TurnVerdictRef),
			group("roll", vocabulary.TurnRollBand, vocabulary.TurnRollTotal, vocabulary.TurnRollRef),
			group("effect-marker", vocabulary.TurnEffectsBatch, vocabulary.TurnEffectsRef),
		}},
		{Name: TurnCompanionTriggerContract, MessageType: turnType, EntityPattern: turnPattern,
			Groups: []projection.PredicateGroup{group("trigger", vocabulary.TurnCompanionTriggerKind, vocabulary.TurnCompanionTriggerSource)}},
		{Name: TurnCompanionResultContract, MessageType: turnType, EntityPattern: turnPattern,
			Groups: []projection.PredicateGroup{group("result", vocabulary.TurnCompanionStageRef, vocabulary.TurnCompanionTriggerKind, vocabulary.TurnCompanionTriggerSource, vocabulary.TurnCompanionDecisionRef)}},
		{Name: PlayerStateContract, MessageType: worldType, EntityPattern: "*.semmachina.*.*.player.*", Groups: []projection.PredicateGroup{
			group("current-turn", vocabulary.PlayerTurnCurrent), group("resolved-turn", vocabulary.PlayerTurnResolved),
		}},
		{Name: CompanionBondContract, MessageType: worldType, EntityPattern: "*.semmachina.*.*.companion-bond.*",
			Groups: []projection.PredicateGroup{group("hint", vocabulary.CompanionBondHintLevel)}},
		{Name: CaseLifecycleContract, MessageType: worldType, EntityPattern: "*.semmachina.*.*.case.*",
			Groups: []projection.PredicateGroup{group("lifecycle-receipt", vocabulary.CaseLifecycleEventID, vocabulary.CaseLifecycleEventKindPredicate, vocabulary.CaseLifecycleFromPhase, vocabulary.CaseLifecycleToPhase)}},
		{Name: EffectTargetContract, MessageType: worldType, EntityPattern: "*.semmachina.*.*.*.*",
			Groups: effectTargetGroups()},
		{Name: KnowledgeBirthContract, MessageType: payload.Domain + ".knowledge_grant_entity." + payload.SchemaVersion,
			EntityPattern: "*.semmachina.*.*.knowledge.*", BirthPredicates: strings(vocabulary.WorldEntityKind, vocabulary.KnowledgeActorHolder, vocabulary.KnowledgeEvidenceRef)},
		{Name: RevelationBirthContract, MessageType: payload.Domain + ".revelation_receipt_entity." + payload.SchemaVersion,
			EntityPattern: "*.semmachina.*.*.revelation.*", BirthPredicates: strings(vocabulary.WorldEntityKind, vocabulary.RevelationEvidenceRef, vocabulary.RevelationActorHolder, vocabulary.RevelationTurnID, vocabulary.RevelationSourceActor, vocabulary.RevelationTestimonyRef)},
	}
}

// EffectTargetForPredicate returns the complete mutable family containing one
// effect-owned predicate. Families are separate projection groups so a writer
// changing health, for example, has no authority over location or relations.
func EffectTargetForPredicate(predicate vocabulary.Predicate) (Target, bool) {
	if _, ok := vocabulary.AttributeForPredicate(predicate); ok {
		return EffectTargetAttributes, true
	}
	if _, ok := vocabulary.RelationForPredicate(predicate); ok {
		return EffectTargetRelationships, true
	}
	switch predicate {
	case vocabulary.CharacterStatusCurrent:
		return EffectTargetStatus, true
	case vocabulary.WorldLocationCurrent:
		return EffectTargetLocation, true
	default:
		return Target{}, false
	}
}

func effectTargetGroups() []projection.PredicateGroup {
	targets := []Target{
		EffectTargetAttributes,
		EffectTargetStatus,
		EffectTargetLocation,
		EffectTargetRelationships,
	}
	predicates := make(map[Target][]string, len(targets))
	for _, predicate := range vocabulary.WorldFactPredicates() {
		if target, ok := EffectTargetForPredicate(predicate); ok {
			predicates[target] = append(predicates[target], predicate.String())
		}
	}
	groups := make([]projection.PredicateGroup, 0, len(targets))
	for _, target := range targets {
		groups = append(groups, projection.PredicateGroup{
			Name: target.Group, Mode: projection.ModeReconcile, Predicates: predicates[target],
		})
	}
	return groups
}

// Predicates returns the complete declared predicate set for a target.
func Predicates(target Target) []string {
	for _, contract := range Contracts() {
		if contract.Name != target.Contract {
			continue
		}
		for _, group := range contract.Groups {
			if group.Name == target.Group {
				return append([]string(nil), group.Predicates...)
			}
		}
	}
	return nil
}

// BirthPredicates returns the complete birth set for a create contract.
func BirthPredicates(name string) []string {
	for _, contract := range Contracts() {
		if contract.Name == name {
			return append([]string(nil), contract.BirthPredicates...)
		}
	}
	return nil
}
