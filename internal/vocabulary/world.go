package vocabulary

import (
	"fmt"
	"slices"
	"strings"

	"github.com/c360studio/semstreams/pkg/types"
)

// EntityKind is the declared type of a world entity.
//
// It is doing two jobs at once, deliberately. It is the object of
// WorldEntityKind (so a rule can match on what a thing IS without parsing an
// ID), and it is the `type` segment of the six-part entity ID the world
// importer composes. Deriving both from one declaration is what makes them
// unable to disagree: an entity cannot be a `character` in the graph and an
// `item` in its own address.
type EntityKind string

// The closed entity-kind set, sized to the starter scene.
//
// Deliberately short. `location`, `campaign`, and `faction` are the obvious
// next members, and each should arrive with the capability that consumes it
// rather than as speculative vocabulary nothing reads.
const (
	// EntityKindCharacter is any acting creature — the player's character and
	// every NPC. NPCs are not a separate kind: the difference between a player
	// character and an NPC is which player entity points at it, not what it is.
	EntityKindCharacter EntityKind = "character"
	// EntityKindItem is a carriable or usable object.
	EntityKindItem EntityKind = "item"
	// EntityKindScene is a bounded place-and-situation. In this slice the scene
	// doubles as the place entities occupy, so WorldLocationCurrent points at
	// a scene.
	EntityKindScene EntityKind = "scene"
	// EntityKindPlayer is the human at the table, as a durable graph entity.
	// Player identity is never a connection ID, so it needs a home in the
	// graph; the binding to a played character is PlayerCharacterCurrent.
	EntityKindPlayer EntityKind = "player"
)

var entityKindEnum = newEnum(KindEntityKind,
	EntityKindCharacter, EntityKindItem, EntityKindScene, EntityKindPlayer)

// EntityKinds returns the closed entity-kind set.
func EntityKinds() []EntityKind { return entityKindEnum.all() }

// Valid reports whether k is in the closed entity-kind set.
func (k EntityKind) Valid() bool { return entityKindEnum.valid(k) }

// ParseEntityKind accepts only registered entity kinds.
func ParseEntityKind(s string) (EntityKind, error) { return entityKindEnum.parse(s) }

// worldFactSubjectKinds records which entity kinds may legally carry each
// world-fact predicate.
//
// This is the "health does not land on a scene" rule, stated once. Two
// independent consumers need it — the world loader rejecting a bad starting
// fact at import, and the effect applier rejecting a bad intent at runtime —
// and a rule that important, re-derived per consumer, is a rule that ends up
// enforced on one path and not the other.
//
// Only WORLD FACTS appear here. Turn and campaign predicates describe engine
// bookkeeping entities that carry no EntityKind, so asking which kind may hold
// turn.phase.current is a category error, and SubjectKindsFor says so by
// returning false rather than an empty list.
var worldFactSubjectKinds = map[Predicate][]EntityKind{
	WorldEntityName:        {EntityKindCharacter, EntityKindItem, EntityKindScene, EntityKindPlayer},
	WorldEntityKind:        {EntityKindCharacter, EntityKindItem, EntityKindScene, EntityKindPlayer},
	WorldEntityDescription: {EntityKindCharacter, EntityKindItem, EntityKindScene, EntityKindPlayer},

	PlayerCharacterCurrent: {EntityKindPlayer},

	CharacterAttributeHealth:  {EntityKindCharacter},
	CharacterAttributeStamina: {EntityKindCharacter},
	CharacterAttributeResolve: {EntityKindCharacter},
	CharacterStatusCurrent:    {EntityKindCharacter},
	ItemAttributeQuantity:     {EntityKindItem},
	SceneAttributeTension:     {EntityKindScene},

	// A character or an item can be somewhere. A scene IS the somewhere, and a
	// player is a person at a table, not a thing in the fiction.
	WorldLocationCurrent: {EntityKindCharacter, EntityKindItem},

	// Relationships are asserted BY actors. An item does not befriend anything;
	// "the sword is carried by Rook" is recorded as Rook carrying the sword, so
	// the edge has one canonical direction and queries do not have to check both.
	WorldRelationAlliedWith: {EntityKindCharacter},
	WorldRelationHostileTo:  {EntityKindCharacter},
	WorldRelationKnows:      {EntityKindCharacter},
	WorldRelationCarries:    {EntityKindCharacter},
	WorldRelationOwesDebt:   {EntityKindCharacter},
}

// SubjectKindsFor returns the entity kinds that may carry p as a fact. The
// bool is false for any predicate that is not a world fact.
func SubjectKindsFor(p Predicate) ([]EntityKind, bool) {
	kinds, ok := worldFactSubjectKinds[p]
	if !ok {
		return nil, false
	}
	return slices.Clone(kinds), true
}

// WorldFactPredicates returns every predicate that describes a world entity,
// in AllPredicates() order so callers and error messages are deterministic.
func WorldFactPredicates() []Predicate {
	out := make([]Predicate, 0, len(worldFactSubjectKinds))
	for _, p := range allPredicates {
		if _, ok := worldFactSubjectKinds[p]; ok {
			out = append(out, p)
		}
	}
	return out
}

// AllowsSubjectKind reports whether an entity of kind k may carry p.
func AllowsSubjectKind(p Predicate, k EntityKind) bool {
	kinds, ok := worldFactSubjectKinds[p]
	if !ok {
		return false
	}
	return slices.Contains(kinds, k)
}

// AttributeForPredicate inverts the registered attribute-to-predicate mapping.
//
// The applier needs it to find the bounds governing a predicate it is about to
// write, and the world loader needs it to reject an out-of-bounds starting
// value at import. Both would otherwise re-derive the inverse by hand, and two
// hand-derived copies of one mapping is how a bound stops being enforced on
// one of the two paths.
func AttributeForPredicate(p Predicate) (Attribute, bool) {
	for attribute, predicate := range attributePredicates {
		if predicate == p {
			return attribute, true
		}
	}
	return "", false
}

// RelationForPredicate inverts the registered relation-to-predicate mapping.
func RelationForPredicate(p Predicate) (Relation, bool) {
	for relation, predicate := range relationPredicates {
		if predicate == p {
			return relation, true
		}
	}
	return "", false
}

// ValidateIDSegment rejects anything that cannot serve as one position of a
// canonical six-part entity ID.
//
// This is the F9 seam. Entity-ID segments and triple predicates have DIFFERENT
// alphabets — a segment may carry uppercase and underscores (`rusty_sword` is a
// fine local id), a predicate may not (`item.condition.rust_level` is rejected
// at the write gate) — and the difference is invisible until a write fails far
// from the file that caused it. Every producer of a segment goes through here
// so the decision is made once, against the upstream parser, rather than
// approximated by a local regex per caller.
//
// The alphabet check delegates to types.ValidateEntityIDPrefix, the same parser
// the composed ID will face. That parser accepts one to six dot-separated
// positions, so the dot rejection is ours: a dotted value is legal as a prefix
// and catastrophic as a segment, because it silently changes the composed ID's
// arity.
func ValidateIDSegment(s string) error {
	if s == "" {
		return fmt.Errorf("entity-ID segment is required")
	}
	if strings.Contains(s, ".") {
		return fmt.Errorf(
			"entity-ID segment %q contains a dot, which would add positions rather than name one", s)
	}
	if err := types.ValidateEntityIDPrefix(s); err != nil {
		return fmt.Errorf("%q is not a legal entity-ID segment: %w", s, err)
	}
	return nil
}
