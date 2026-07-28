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
)

// ObjectShapeFor returns the object shape a template author must write for p,
// or ShapeNone when p is not author-writable.
func ObjectShapeFor(p vocabulary.Predicate) ObjectShape {
	if _, ok := vocabulary.AttributeForPredicate(p); ok {
		return ShapeAttribute
	}
	if _, ok := vocabulary.RelationForPredicate(p); ok {
		return ShapeReference
	}
	switch p {
	case vocabulary.WorldLocationCurrent:
		return ShapeReference
	case vocabulary.CharacterStatusCurrent:
		return ShapeStatus
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
