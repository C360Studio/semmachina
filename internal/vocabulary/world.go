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

// referenceObjectKinds records which entity kinds may be the OBJECT of each
// entity-valued world predicate.
//
// It is the object-side twin of worldFactSubjectKinds, and it exists for the
// same reason: the predicate alone does not say that a character is moved into
// a scene rather than into a crowbar, or that `carries` points at an item while
// `knows` points at a character. Without a registered rule the effect applier —
// the runtime gate against an LLM-proposed intent — would accept any entity on
// the far end of a reference, and the resulting world would be well-formed and
// nonsense.
//
// Membership in this map is also the definition of "entity-valued": a predicate
// here takes a six-part entity ID as its object, and one absent from it takes a
// literal. The two facts are the same fact, so they are stated once.
var referenceObjectKinds = map[Predicate][]EntityKind{
	// A scene doubles as the place entities occupy in this slice; when place
	// becomes first-class (roadmap stage 2) this is the line that changes.
	WorldLocationCurrent: {EntityKindScene},

	PlayerCharacterCurrent: {EntityKindCharacter},

	// Relationships between actors point at actors; possession points at a
	// thing. "Rook carries Wren" is not a v1 sentence.
	WorldRelationAlliedWith: {EntityKindCharacter},
	WorldRelationHostileTo:  {EntityKindCharacter},
	WorldRelationKnows:      {EntityKindCharacter},
	WorldRelationCarries:    {EntityKindItem},
	WorldRelationOwesDebt:   {EntityKindCharacter},
}

// sceneMembershipPredicates is the closed set of edges that put an entity IN a
// scene.
//
// Membership is asserted BY the member — a character carries
// world.location.current pointing at the scene, and the scene says nothing about
// who is in it — so "who is here" is answered by reading the edges pointing at
// the scene. Those edges are not all membership: the engine's own turn entities
// point at the scene too (turn.action.scene), one per turn ever taken there, and
// a reader that took every incoming edge as a member would hand a persona every
// turn in the campaign's history and call it the room.
//
// So the set is CLOSED and stated here, next to the ontology it belongs to,
// rather than inferred at the reader.
//
// Growing it is NOT sufficient on its own, and the earlier version of this
// comment said the opposite. When place becomes first-class (roadmap stage 2)
// the reverse lookup is still keyed on the SCENE, so adding a second membership
// predicate here changes which incoming edges count and nothing else — the
// assembler would still ask the scene who is in it. Re-keying that read onto the
// location is the assembler's change, and this list is the vocabulary half of
// the same change rather than the whole of it.
var sceneMembershipPredicates = []Predicate{WorldLocationCurrent}

// SceneMembershipPredicates returns the closed set of edges that put an entity
// in a scene.
func SceneMembershipPredicates() []Predicate { return slices.Clone(sceneMembershipPredicates) }

// IsSceneMembership reports whether an edge pointing at a scene means its source
// is IN that scene.
func IsSceneMembership(p Predicate) bool { return slices.Contains(sceneMembershipPredicates, p) }

// SubjectKindsFor returns the entity kinds that may carry p as a fact. The
// bool is false for any predicate that is not a world fact.
func SubjectKindsFor(p Predicate) ([]EntityKind, bool) {
	kinds, ok := worldFactSubjectKinds[p]
	if !ok {
		return nil, false
	}
	return slices.Clone(kinds), true
}

// ObjectKindsFor returns the entity kinds that may be the object of p. The bool
// is false for any predicate whose object is a literal rather than a reference.
func ObjectKindsFor(p Predicate) ([]EntityKind, bool) {
	kinds, ok := referenceObjectKinds[p]
	if !ok {
		return nil, false
	}
	return slices.Clone(kinds), true
}

// AllowsObjectKind reports whether an entity of kind k may be the object of p.
func AllowsObjectKind(p Predicate, k EntityKind) bool {
	return slices.Contains(referenceObjectKinds[p], k)
}

// IsEntityReference reports whether p's object is a six-part entity ID rather
// than a literal value.
func IsEntityReference(p Predicate) bool {
	_, ok := referenceObjectKinds[p]
	return ok
}

// IsMultiValued reports whether p may hold more than one object on one entity.
//
// This is not a cosmetic distinction. The graph's merge lane replaces a
// predicate's WHOLE value set per write, so a writer that treats a multi-valued
// predicate as single-valued deletes every sibling value and gets a success
// response: adding a second carried item would drop the first. A writer owning a
// multi-valued predicate must publish the complete desired set.
//
// The relations are exactly the multi-valued predicates in v1 — a character
// carries several things and knows several people, while health, status, and
// location each hold one value by definition.
func IsMultiValued(p Predicate) bool {
	_, ok := RelationForPredicate(p)
	return ok
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

// PlatformSegment is the platform position of every entity ID this engine
// mints — position two of org.platform.domain.system.type.instance.
//
// It is a constant rather than a parameter because an instance-per-world
// deployment has exactly one platform, and a caller that could choose it could
// mint an ID no other component would find. It shares its spelling with the
// payload-registry domain and is NOT the same contract: one addresses entities,
// the other addresses message schemas.
const PlatformSegment = "semmachina"

// MaxIDSegmentBytes bounds one position of a composed entity ID.
//
// Upstream has no per-segment bound: types.MaxEntityIDBytes is a property of
// the WHOLE composed string. That makes an oversized segment invisible to every
// check that sees only the segment, and legal-or-not to every check that sees
// the composition, depending on how long the other five positions happen to be.
// So the bound is drawn here, as a RESERVE: half the upstream budget for the
// five leading positions and their separators, half for the instance position.
//
// Being a reserve rather than the exact upstream failure point is the whole
// reason it must live with the segment rule. A 129-byte local id composes a
// 165-byte ID under the starter world's short prefix — accepted by
// types.ValidateEntityID, accepted by every composition check — and an
// oversized one under a long world namespace. Without the reserve, whether an
// author's world imports depends on a prefix they never see.
const MaxIDSegmentBytes = types.MaxEntityIDBytes / 2

// ValidateIDSegment rejects anything that cannot serve as one position of a
// canonical six-part entity ID.
//
// This is the F9 seam, and it is the ONLY statement of the rule: the world
// package's local ids, the manifest's template id, the instance config's
// org/namespace/player id, and every untrusted identifier an ingress adapter
// turns into an instance segment all come through here. A second copy of this
// rule is not redundancy, it is a divergence waiting to happen — the length
// bound was enforced on one of the two paths for exactly one task group, and
// the gap put a partial world in the graph.
//
// Entity-ID segments and triple predicates have DIFFERENT alphabets — a segment
// may carry uppercase and underscores (`rusty_sword` is a fine local id), a
// predicate may not (`item.condition.rust_level` is rejected at the write
// gate) — and the difference is invisible until a write fails far from the file
// that caused it.
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
	// Length first, so a rejection reason never echoes an oversized value.
	if len(s) > MaxIDSegmentBytes {
		return fmt.Errorf(
			"entity-ID segment is %d bytes, which exceeds the %d-byte segment budget",
			len(s), MaxIDSegmentBytes)
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

// ComposeEntityID builds the documented six-part ID and validates it against
// the same contract the graph will apply.
//
// The type segment is a plain string, not an EntityKind, because the engine's
// own entities are addressed the same way world entities are while carrying
// type segments that are deliberately outside the world vocabulary — a turn is
// not a thing in the fiction, and a campaign is not an entity kind a template
// may declare. Composition is one rule for both, or the two grow apart.
//
// Position order is org.platform.domain.system.type.instance, which places the
// WORLD NAMESPACE in the domain slot and the TEMPLATE in the system slot: two
// campaigns from one template are disjoint at the domain level, and one
// campaign's entities share a system.
//
// Validation happens on the COMPOSED id, not just on its parts, because the
// 256-byte limit is a property of the whole string: six individually legal
// positions can still compose an ID the graph refuses, and finding that out at
// write time means finding it out after a partial import.
func ComposeEntityID(org, worldNS, template, typeSegment, instance string) (string, error) {
	id := strings.Join(
		[]string{org, PlatformSegment, worldNS, template, typeSegment, instance}, ".")
	if err := types.ValidateEntityID(id); err != nil {
		return "", fmt.Errorf("composed entity id %q is not canonical: %w", id, err)
	}
	return id, nil
}

// SiblingTypePrefix returns the five-part entity-ID prefix shared by every
// entity of typeSegment in the same world instance as entityID.
//
// It is the enumeration half of ComposeEntityID: composition fixes six
// positions, and this drops the instance position to name the SET. Two boot
// passes need it — the campaign ledger enumerating turns to archive, and the
// stranded-turn reconciliation enumerating turns to rescue — and a second copy
// of the arithmetic would be a second place for "which world's turns" to be
// answered differently.
//
// Deriving it from a SIBLING entity's id rather than from configuration is the
// point. Both callers already hold a validated campaign entity id and already
// stamp it on what they write; deriving the scan prefix from the same value
// makes it impossible to enumerate one world's turns on behalf of another,
// which a second configuration field would make merely unlikely.
func SiblingTypePrefix(entityID, typeSegment string) (string, error) {
	if err := types.ValidateEntityID(entityID); err != nil {
		return "", fmt.Errorf("cannot derive a %s prefix from %q: %w", typeSegment, entityID, err)
	}
	if err := ValidateIDSegment(typeSegment); err != nil {
		return "", fmt.Errorf("cannot derive a sibling prefix for type %q: %w", typeSegment, err)
	}
	parts := strings.Split(entityID, ".")
	// org.platform.domain.system + the requested type segment. The instance
	// position is dropped, which is what makes this a prefix over every sibling
	// rather than one id.
	prefix := strings.Join(append(parts[:len(parts)-2:len(parts)-2], typeSegment), ".")
	if err := types.ValidateEntityIDPrefix(prefix); err != nil {
		return "", fmt.Errorf("%s prefix %q derived from %q is not valid: %w", typeSegment, prefix, entityID, err)
	}
	return prefix, nil
}
