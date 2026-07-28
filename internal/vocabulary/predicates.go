package vocabulary

import (
	"slices"

	ssvocab "github.com/c360studio/semstreams/vocabulary"
)

// Predicate is a canonical semstreams triple predicate: exactly three
// lower-kebab ASCII segments, domain.category.property, each segment matching
// [a-z][a-z0-9]*(-[a-z0-9]+)* and at most 64 bytes.
//
// This is not a style preference. semstreams validates every predicate
// fail-closed at the ENTITY_STATES write gate, so a two-segment or
// underscore-bearing predicate is rejected at write time and the triple never
// lands. Underscores are therefore illegal here even though they are perfectly
// legal in JSON payload fields: `requires_roll` is a payload field, and
// TurnVerdictRequiresRoll ("turn.verdict.requires-roll") is its triple form.
type Predicate string

// String returns the predicate identity.
func (p Predicate) String() string { return string(p) }

// Turn-entity predicates. The turn entity is the idempotency guard, the
// crash-diagnosis record, and the explicit failure state for one turn.
const (
	// TurnPhaseCurrent is the single-valued durable phase. Phase writes
	// replace; they never append.
	TurnPhaseCurrent Predicate = "turn.phase.current"
	// TurnActionRef references the stored player action payload.
	TurnActionRef Predicate = "turn.action.ref"
	// TurnActionPlayer references the player entity that submitted the turn.
	TurnActionPlayer Predicate = "turn.action.player"
	// TurnActionScene references the scene the turn occurred in.
	TurnActionScene Predicate = "turn.action.scene"

	// TurnVerdictPlausibility is the rule-matched plausibility scalar.
	TurnVerdictPlausibility Predicate = "turn.verdict.plausibility"
	// TurnVerdictRisk is the rule-matched risk scalar.
	TurnVerdictRisk Predicate = "turn.verdict.risk"
	// TurnVerdictConsequence is the rule-matched consequence-class scalar.
	TurnVerdictConsequence Predicate = "turn.verdict.consequence"
	// TurnVerdictRequiresRoll is the rule-matched roll gate. This is the
	// triple form of the `requires_roll` payload field; the rule pack
	// branches on this predicate to route a verdict to the dice.
	TurnVerdictRequiresRoll Predicate = "turn.verdict.requires-roll"
	// TurnVerdictRef references the full verdict payload, whose banded
	// effect-intent set is bulky and never rule-matched.
	TurnVerdictRef Predicate = "turn.verdict.ref"

	// TurnRollBand is the rule-matched selected band.
	TurnRollBand Predicate = "turn.roll.band"
	// TurnRollTotal is the rule-matched roll total.
	TurnRollTotal Predicate = "turn.roll.total"
	// TurnRollRef references the full roll-result payload.
	TurnRollRef Predicate = "turn.roll.ref"

	// TurnEffectsBatch is the applied batch identity, derived from turn_id.
	// Its presence is the applier's idempotency guard.
	TurnEffectsBatch Predicate = "turn.effects.batch"
	// TurnEffectsRef references the applied effect-batch payload.
	TurnEffectsRef Predicate = "turn.effects.ref"

	// TurnNarrationRef references narration prose in ObjectStore. Written
	// only after the prose write succeeds — a ref to missing prose is a
	// correctness bug, an unreferenced orphan object is only garbage.
	TurnNarrationRef Predicate = "turn.narration.ref"

	// TurnFailureReason records why a turn entered the failed phase.
	TurnFailureReason Predicate = "turn.failure.reason"
)

// Campaign-entity predicates.
const (
	// CampaignSeedValue is the campaign seed minted at world instantiation.
	// Every per-roll seed derives from it and the turn_id; it is stored
	// exactly once, here.
	CampaignSeedValue Predicate = "campaign.seed.value"
)

// World predicates written by effect intents.
const (
	// CharacterStatusCurrent is the single-valued target of a set_status
	// effect.
	CharacterStatusCurrent Predicate = "character.status.current"
	// WorldLocationCurrent is the single-valued target of a move_entity
	// effect; its object is a location entity ID.
	WorldLocationCurrent Predicate = "world.location.current"
)

// Attribute predicates: the single-valued targets of set_attribute effects.
// Each is paired with registered bounds in attributeSpecs.
const (
	// CharacterAttributeHealth is bodily integrity.
	CharacterAttributeHealth Predicate = "character.attribute.health"
	// CharacterAttributeStamina is capacity for exertion.
	CharacterAttributeStamina Predicate = "character.attribute.stamina"
	// CharacterAttributeResolve is capacity to withstand pressure.
	CharacterAttributeResolve Predicate = "character.attribute.resolve"
	// ItemAttributeQuantity is a countable item stack.
	ItemAttributeQuantity Predicate = "item.attribute.quantity"
	// SceneAttributeTension is how close the scene is to breaking.
	SceneAttributeTension Predicate = "scene.attribute.tension"
)

// Relationship predicates: the registered list add_relationship and
// remove_relationship may write. Objects are entity IDs.
const (
	// WorldRelationAlliedWith asserts alliance.
	WorldRelationAlliedWith Predicate = "world.relation.allied-with"
	// WorldRelationHostileTo asserts hostility.
	WorldRelationHostileTo Predicate = "world.relation.hostile-to"
	// WorldRelationKnows asserts acquaintance.
	WorldRelationKnows Predicate = "world.relation.knows"
	// WorldRelationCarries asserts possession.
	WorldRelationCarries Predicate = "world.relation.carries"
	// WorldRelationOwesDebt asserts obligation.
	WorldRelationOwesDebt Predicate = "world.relation.owes-debt"
)

// allPredicates is the authoritative list. A predicate constant missing from
// it is caught by TestClosedSets_EveryDeclaredConstantIsEnumerated.
var allPredicates = []Predicate{
	TurnPhaseCurrent,
	TurnActionRef,
	TurnActionPlayer,
	TurnActionScene,
	TurnVerdictPlausibility,
	TurnVerdictRisk,
	TurnVerdictConsequence,
	TurnVerdictRequiresRoll,
	TurnVerdictRef,
	TurnRollBand,
	TurnRollTotal,
	TurnRollRef,
	TurnEffectsBatch,
	TurnEffectsRef,
	TurnNarrationRef,
	TurnFailureReason,
	CampaignSeedValue,
	CharacterStatusCurrent,
	WorldLocationCurrent,
	CharacterAttributeHealth,
	CharacterAttributeStamina,
	CharacterAttributeResolve,
	ItemAttributeQuantity,
	SceneAttributeTension,
	WorldRelationAlliedWith,
	WorldRelationHostileTo,
	WorldRelationKnows,
	WorldRelationCarries,
	WorldRelationOwesDebt,
}

// AllPredicates returns every predicate identity the engine writes.
func AllPredicates() []Predicate { return slices.Clone(allPredicates) }

// verdictScalarPredicates is the rule-matched projection of a verdict — the
// only verdict fields small enough and structural enough to become triples.
// The banded effect-intent set rides as a reference (TurnVerdictRef),
// following the upstream decide-executor precedent.
var verdictScalarPredicates = []Predicate{
	TurnVerdictPlausibility,
	TurnVerdictRisk,
	TurnVerdictConsequence,
	TurnVerdictRequiresRoll,
}

// VerdictScalarPredicates returns the predicates a verdict is allowed to
// write as triples. Anything not in this list must travel by reference.
func VerdictScalarPredicates() []Predicate { return slices.Clone(verdictScalarPredicates) }

// ValidatePredicate delegates to the semstreams predicate contract — the same
// parser the ENTITY_STATES write gate runs. Callers get the identical
// accept/reject decision the graph will make, before issuing a write.
func ValidatePredicate(p Predicate) error {
	_, err := ssvocab.ParsePredicate(string(p))
	return err
}
