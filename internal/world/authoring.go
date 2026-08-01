package world

import (
	"slices"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ObjectShape names the wire shape a world-fact predicate's object takes.
//
// It exists because the object's meaning is not recoverable from JSON alone:
// "local:rook" and "Rook" are both strings, and only the predicate says which
// one is an address. Deriving the shape from the vocabulary's own registries
// (rather than listing it per predicate) means a new attribute or relation
// gets its object rules for free and cannot be added with the wrong ones.
type ObjectShape uint8

// The closed object-shape set.
const (
	// ShapeNone means the predicate is not author-writable.
	ShapeNone ObjectShape = iota
	// ShapeText is bounded literal text.
	ShapeText
	// ShapeReference is a `local:` template reference that becomes an entity ID.
	ShapeReference
	// ShapeAttribute is a bounded integer governed by an AttributeSpec.
	ShapeAttribute
	// ShapeStatus is a member of the closed status set.
	ShapeStatus
	// ShapeOrdinal is a positive authored sequence position or cardinality.
	ShapeOrdinal
	// ShapeEvidenceTruthStatus is the closed evidence classification.
	ShapeEvidenceTruthStatus
	// ShapeBeliefStance is the closed actor-belief stance.
	ShapeBeliefStance
	// ShapeCompanionPolicy is the closed companion admission policy.
	ShapeCompanionPolicy
	// ShapeHintLevel is the closed bounded hint level.
	ShapeHintLevel
)

// ObjectShapeFor returns the object shape a template author must write for p,
// or ShapeNone when p has no registered shape.
//
// The reference case delegates to vocabulary.IsEntityReference rather than
// listing the entity-valued predicates again. "Which predicates take an entity
// ID as their object" is one question with one answer, and the vocabulary owns
// it — the effect applier asks the same question at runtime, and a second list
// here would be a second chance for the two to disagree.
func ObjectShapeFor(p vocabulary.Predicate) ObjectShape {
	if _, ok := vocabulary.AttributeForPredicate(p); ok {
		return ShapeAttribute
	}
	if vocabulary.IsEntityReference(p) {
		return ShapeReference
	}
	switch p {
	case vocabulary.CharacterStatusCurrent:
		return ShapeStatus
	case vocabulary.CaseRequirementSuspects,
		vocabulary.CaseRequirementEvidence,
		vocabulary.CaseTimelineOrder:
		return ShapeOrdinal
	case vocabulary.CompanionCandidatePolicy:
		return ShapeCompanionPolicy
	case vocabulary.EvidenceTruthStatusCurrent:
		return ShapeEvidenceTruthStatus
	case vocabulary.BeliefStanceCurrent:
		return ShapeBeliefStance
	case vocabulary.CompanionBondPolicy:
		return ShapeCompanionPolicy
	case vocabulary.CompanionBondHintLevel:
		return ShapeHintLevel
	case vocabulary.WorldEntityName, vocabulary.WorldEntityDescription:
		return ShapeText
	default:
		return ShapeNone
	}
}

// engineOwnedFacts are world-fact predicates a TEMPLATE may not declare.
//
// Both are engine-derived rather than authored, and both would break a real
// contract if a package could set them:
//
//   - world.entity.kind is projected from the entity's declared type, so an
//     authored copy could disagree with the type position of its own ID.
//   - player.character.current is INSTANCE configuration. A template that named
//     its own player could be instantiated exactly once — which is precisely
//     the claim "a template is instantiable into multiple worlds unmodified"
//     denies.
var engineOwnedFacts = []vocabulary.Predicate{
	vocabulary.WorldEntityKind,
	vocabulary.PlayerCharacterCurrent,
	// Revelations are committed turn receipts, never package seeds.
	vocabulary.RevelationEvidenceRef,
	vocabulary.RevelationActorHolder,
	vocabulary.RevelationTurnID,
	// A bond names an instance-configured player, so the template can only mark
	// a character as eligible; the durable bond itself is runtime state.
	vocabulary.CompanionBondPlayer,
	vocabulary.CompanionBondCharacter,
	vocabulary.CompanionBondPolicy,
	vocabulary.CompanionBondHintLevel,
	// Case lifecycle state and transition receipts are owned by the lifecycle
	// manager and the in-process receipt seam, never by downloaded packages.
	vocabulary.CaseLifecyclePhase,
	vocabulary.CaseLifecycleEventID,
	vocabulary.CaseLifecycleEventKindPredicate,
	vocabulary.CaseLifecycleFromPhase,
	vocabulary.CaseLifecycleToPhase,
	vocabulary.CaseTransitionSource,
	vocabulary.CaseTransitionAt,
	vocabulary.CaseTransitionFrom,
}

// AuthorWritable reports whether a template package may declare p.
func AuthorWritable(p vocabulary.Predicate) bool {
	if slices.Contains(engineOwnedFacts, p) {
		return false
	}
	if _, isWorldFact := vocabulary.SubjectKindsFor(p); !isWorldFact {
		return false
	}
	return ObjectShapeFor(p) != ShapeNone
}

// AuthorWritablePredicates returns every predicate a template may declare, in
// vocabulary order so error messages listing them are stable.
func AuthorWritablePredicates() []vocabulary.Predicate {
	var out []vocabulary.Predicate
	for _, p := range vocabulary.WorldFactPredicates() {
		if AuthorWritable(p) {
			out = append(out, p)
		}
	}
	return out
}
