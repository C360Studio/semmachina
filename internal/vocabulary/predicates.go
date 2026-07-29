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

	// TurnResumeAttempts counts how many times the boot-time stranded-turn pass
	// has re-triggered this turn.
	//
	// SINGLE-VALUED, and that is the whole reason it is declared here rather than
	// tracked wherever the pass happens to run. A counter is the one predicate
	// shape where the append/merge lane distinction is not a subtlety but the
	// entire semantics: written through the entity MERGE lane it replaces its own
	// prior value and the turn holds one count, and written through a triple-add
	// lane it appends and the turn holds N counters — with a success response, no
	// error, and a bound that silently stops bounding anything.
	//
	// It is rule-matchable on purpose. The object is a number, not fiction, and a
	// future rule pack thresholding on "this turn has been rescued twice" is
	// exactly the kind of branch the rule layer is for. Nothing matches it today.
	TurnResumeAttempts Predicate = "turn.resume.attempts"

	// TurnFailureReason records why a turn entered the failed phase. Its object
	// is a closed FailureReason code, never a sentence: the turn entity is
	// rule-matching surface, and the shared triple projection gates an object's
	// SHAPE but not its CLOSURE, so an applier- or persona-authored explanation
	// would pass every gate and land free text where rules match.
	TurnFailureReason Predicate = "turn.failure.reason"
	// TurnFailureRef references stored detail about a failure — the effect
	// batch that was refused, the loop that exhausted its cap.
	//
	// It exists so the closed reason code stays survivable. A code alone
	// sometimes cannot answer "which intent?", and the only place that answer
	// could otherwise go is the reason itself; a predicate whose object is a
	// reference gives the detail somewhere to live that is not the rule-matching
	// surface. Written only alongside a failed phase, and only when the caller
	// actually holds a durable reference.
	//
	// The `.ref` suffix is load-bearing, not cosmetic. Predicate registration
	// sorts this engine's surface into two branches — `*.ref` predicates stay
	// rule-matchable because a reference is a structural pointer, and predicates
	// whose object is fiction are marked rule-opaque — and a name matching
	// neither branch has no home in that rule. It is also the predicate most
	// likely to be handed an explanation instead of a pointer: a sentence passes
	// the reason parse it never has to face, passes the projection's scalar size
	// gate, and lands free text on rule-matching surface — the exact hole the
	// closed reason code exists to close, relocated one predicate over. The name
	// says what the object must be.
	TurnFailureRef Predicate = "turn.failure.ref"
)

// Campaign-entity predicates.
const (
	// CampaignSeedValue is the campaign seed minted at world instantiation.
	// Every per-roll seed derives from it and the turn_id; it is stored
	// exactly once, here.
	CampaignSeedValue Predicate = "campaign.seed.value"
)

// Descriptive world predicates. These are what a WORLD PACKAGE authors: they
// carry starting state, not turn state, and the engine never writes them at
// runtime.
//
// They exist because a world with no names is not a world. Authoring the
// starter fixture is what surfaced them — the closed effect/turn vocabulary
// could describe everything that HAPPENS to a character and nothing about who
// the character IS, so the narrator had nothing to call anyone.
const (
	// WorldEntityName is the display name a persona uses in prose. Rule-opaque
	// by intent: nothing may branch on it.
	WorldEntityName Predicate = "world.entity.name"
	// WorldEntityKind is the declared EntityKind. Rule-matchable, and equal by
	// construction to the type segment of the entity's six-part ID.
	WorldEntityKind Predicate = "world.entity.kind"
	// WorldEntityDescription is bounded author-written flavor text the context
	// assembler feeds personas. Rule-opaque, and bounded at import — bulky
	// prose belongs in ObjectStore behind a ref, not in the graph's
	// rule-matching surface.
	WorldEntityDescription Predicate = "world.entity.description"
)

// Player-entity predicates. A player is a durable graph entity, never a
// connection, so the binding between the human and the character they play is
// a fact in the graph.
const (
	// PlayerCharacterCurrent is the single-valued played-character binding.
	// Its object is a character entity ID.
	//
	// It is written from INSTANCE configuration, never from a template: a
	// template that named its own player could be instantiated exactly once,
	// which would make "worlds are data" false at the first campaign.
	PlayerCharacterCurrent Predicate = "player.character.current"
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
	TurnResumeAttempts,
	TurnFailureReason,
	TurnFailureRef,
	CampaignSeedValue,
	WorldEntityName,
	WorldEntityKind,
	WorldEntityDescription,
	PlayerCharacterCurrent,
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

// rollScalarPredicates is the rule-matched projection of a roll result: the
// band the rule pack routes on and the total a threshold rule can compare.
//
// The mechanic version, the RNG version, the seed derivation inputs, the raw
// dice, and the modifier list are all RECORDED — on the RollResult payload,
// which is what makes a roll byte-identically replayable — and none of them are
// rule-matching surface. They reach the graph as TurnRollRef, following the
// same reference discipline the verdict's banded intents follow. A rule that
// needed the dice values would be a rule re-deriving the band, which the
// component already decided.
var rollScalarPredicates = []Predicate{
	TurnRollBand,
	TurnRollTotal,
}

// RollScalarPredicates returns the predicates a roll result is allowed to
// write as triples.
func RollScalarPredicates() []Predicate { return slices.Clone(rollScalarPredicates) }

// effectScalarPredicates is the rule-matched projection of an applied effect
// batch: the batch identity, and nothing else.
//
// The identity is what the applier's own idempotency guard reads and what a
// rule chains on ("effects landed → narrate"). The intent list is bulky, is
// consumed only by the applier and the ledger, and carries the adjudicator's
// proposals rather than the world's facts — so it rides behind TurnEffectsRef,
// the same discipline the verdict's bands and the roll's dice follow. The
// COMMITTED changes are on the target entities themselves, which is where a
// reader asking what the world looks like should be looking anyway.
var effectScalarPredicates = []Predicate{
	TurnEffectsBatch,
}

// EffectScalarPredicates returns the predicates an applied effect batch is
// allowed to write as triples on the turn entity.
func EffectScalarPredicates() []Predicate { return slices.Clone(effectScalarPredicates) }

// narrationScalarPredicates is the rule-matched projection of a narration, and
// it is EMPTY — deliberately, and stated here rather than left to be inferred
// from an absent list.
//
// The narrator voices a committed outcome. Everything it produces is prose,
// which is fiction (M1) and cannot become a triple at all; every structural fact
// about the turn — the verdict's classes, the roll's band, the applied batch —
// was already landed by the stage that decided it, and a narrator re-stating one
// would be a persona writing to the rule-matching surface about a decision it
// did not make. So the narration's only mark on the graph is a POINTER at its
// prose, and the turn's closing rule matches the arrival of that pointer.
//
// An empty list is a claim, and the projection makes the caller declare it: a
// scalar-less projection is a distinct, explicit mode, so a payload whose
// scalars were forgotten is not silently indistinguishable from one that has
// none.
var narrationScalarPredicates []Predicate

// NarrationScalarPredicates returns the predicates a narration is allowed to
// write as triples on the turn entity. It is empty; see the discussion above.
func NarrationScalarPredicates() []Predicate { return slices.Clone(narrationScalarPredicates) }

// The turn entity's OWN state, in the three shapes it is ever written in.
//
// Three lists rather than one because the shapes carry different facts, and a
// single list would force every write to restate facts it has no business
// restating: a phase transition that had to resend the player and scene could
// rewrite them, and a transition that had to send a failure reason would need
// one for a turn that did not fail.
//
// None of the three carries a reference predicate, which is why the shared
// projection had to learn a REF-LESS shape rather than being handed a token ref
// to satisfy it. The turn's state has no bulky half at all — a phase, a player,
// a scene, and a closed failure code are the whole record — and inventing a
// reference so the gate would accept it would have made the gate a formality.
var (
	// turnAcceptedPredicates is the turn's birth record: the phase it is
	// created in, plus the two entity references every later stage needs to
	// find the turn's player and its scene. It is written exactly once, by the
	// atomic create, and never again.
	turnAcceptedPredicates = []Predicate{
		TurnPhaseCurrent,
		TurnActionPlayer,
		TurnActionScene,
	}

	// turnPhasePredicates is an ordinary phase transition: one single-valued
	// fact, replacing its own prior value.
	turnPhasePredicates = []Predicate{TurnPhaseCurrent}

	// turnFailurePredicates is a terminal failure: the phase and the closed
	// reason land in ONE write, because a crash between two writes would leave
	// either a failed turn nobody can explain or a reason on a turn that is
	// still reported as running.
	turnFailurePredicates = []Predicate{TurnPhaseCurrent, TurnFailureReason}

	// turnResumePredicates is the stranded-turn pass's attempt counter, written
	// WITHOUT the phase.
	//
	// Deliberately without it: the pass re-triggers a turn into the phase it is
	// already in, so it has no phase to record, and a write that resent the phase
	// would make the boot pass a second owner of the single-valued fact the turn
	// recorder owns.
	turnResumePredicates = []Predicate{TurnResumeAttempts}
)

// TurnAcceptedPredicates returns the predicates the turn's birth record writes.
func TurnAcceptedPredicates() []Predicate { return slices.Clone(turnAcceptedPredicates) }

// TurnPhasePredicates returns the predicates an ordinary phase transition writes.
func TurnPhasePredicates() []Predicate { return slices.Clone(turnPhasePredicates) }

// TurnFailurePredicates returns the predicates a terminal failure writes.
func TurnFailurePredicates() []Predicate { return slices.Clone(turnFailurePredicates) }

// TurnResumePredicates returns the predicates the stranded-turn pass writes.
func TurnResumePredicates() []Predicate { return slices.Clone(turnResumePredicates) }

// ValidatePredicate delegates to the semstreams predicate contract — the same
// parser the ENTITY_STATES write gate runs. Callers get the identical
// accept/reject decision the graph will make, before issuing a write.
func ValidatePredicate(p Predicate) error {
	_, err := ssvocab.ParsePredicate(string(p))
	return err
}
