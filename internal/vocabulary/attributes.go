package vocabulary

import "fmt"

// Attribute names a bounded numeric attribute a set_attribute effect may
// write. Attributes use short model-friendly names in payloads and tool
// schemas; each maps to exactly one canonical predicate.
//
// The attribute NAMES are engine vocabulary (closed, rule-matchable, shared by
// the tool schema and the rule pack); their BOUNDS are world data — see
// AttributeSpecSet.
type Attribute string

// The closed attribute set, sized to the starter scene.
const (
	// AttributeHealth is bodily integrity; zero is the death threshold.
	AttributeHealth Attribute = "health"
	// AttributeStamina is capacity for exertion.
	AttributeStamina Attribute = "stamina"
	// AttributeResolve is capacity to withstand pressure.
	AttributeResolve Attribute = "resolve"
	// AttributeQuantity is a countable item stack.
	AttributeQuantity Attribute = "quantity"
	// AttributeTension is how close the scene is to breaking.
	AttributeTension Attribute = "tension"
)

var attributeEnum = newEnum(KindAttribute,
	AttributeHealth, AttributeStamina, AttributeResolve, AttributeQuantity, AttributeTension)

// Attributes returns the closed attribute set.
func Attributes() []Attribute { return attributeEnum.all() }

// Valid reports whether a is in the closed attribute set.
func (a Attribute) Valid() bool { return attributeEnum.valid(a) }

// ParseAttribute accepts only registered attributes.
func ParseAttribute(s string) (Attribute, error) { return attributeEnum.parse(s) }

// AttributeSpec is the registered contract for one attribute: which predicate
// it writes and the inclusive range it may hold. The applier rejects any
// intent outside these bounds, so an adjudicator cannot heal a character to
// 900 or drive a stack negative.
type AttributeSpec struct {
	Attribute Attribute
	Predicate Predicate
	Min       int
	Max       int
}

// InBounds reports whether value is within the registered inclusive range.
func (s AttributeSpec) InBounds(value int) bool {
	return value >= s.Min && value <= s.Max
}

// AttributeBounds is the inclusive range a world package sets for one
// attribute. Bounds are the only part of a spec a world owns: the predicate is
// engine vocabulary — rule-matchable, registered, and shared by every world —
// so a world tunes numbers and can never redirect a write.
type AttributeBounds struct {
	Min int
	Max int
}

// AttributeSpecSet is one world's attribute contracts. Worlds are data and the
// engine is world-agnostic, so bounds are a VALUE the engine is handed, not a
// constant compiled into it: a grim survival world wants health 0-4 and a
// heroic one wants 0-40, and neither should require an engine change.
//
// Nothing is world-scoped yet. Every caller today (EffectIntent.Validate, the
// applier) goes through the package-level AttributeSpecFor and
// CheckAttributeValue, which read DefaultAttributeSpecs. Loading a world's set
// is world-loading's job (task group 3); this type exists so that arrives as
// wiring rather than as a rewrite of the bounds contract.
type AttributeSpecSet struct {
	specs map[Attribute]AttributeSpec
}

// attributePredicates is the engine-owned attribute-to-predicate mapping.
// Not overridable: an attribute's predicate is rule-matching surface.
var attributePredicates = map[Attribute]Predicate{
	AttributeHealth:   CharacterAttributeHealth,
	AttributeStamina:  CharacterAttributeStamina,
	AttributeResolve:  CharacterAttributeResolve,
	AttributeQuantity: ItemAttributeQuantity,
	AttributeTension:  SceneAttributeTension,
}

// defaultAttributeBounds are the engine's v1 numbers, sized to the starter
// scene. They are DEFAULTS, not the definition of these attributes: a world
// package supplies its own via NewAttributeSpecSet at load.
var defaultAttributeBounds = map[Attribute]AttributeBounds{
	AttributeHealth:   {Min: 0, Max: 10},
	AttributeStamina:  {Min: 0, Max: 10},
	AttributeResolve:  {Min: 0, Max: 10},
	AttributeQuantity: {Min: 0, Max: 99},
	AttributeTension:  {Min: 0, Max: 10},
}

// NewAttributeSpecSet builds a world's attribute contracts from its declared
// bounds, pairing each with the engine's registered predicate.
//
// It is total over the closed attribute set: a world must declare every
// registered attribute. Silently inheriting engine defaults for the ones a
// world forgot is exactly the drift that makes "worlds are data" untrue — the
// world would run under numbers it never authored and nobody would see it.
func NewAttributeSpecSet(bounds map[Attribute]AttributeBounds) (AttributeSpecSet, error) {
	specs := make(map[Attribute]AttributeSpec, len(attributePredicates))
	for _, a := range Attributes() {
		b, ok := bounds[a]
		if !ok {
			return AttributeSpecSet{}, fmt.Errorf(
				"attribute %q has no declared bounds; a world must declare every registered attribute", a)
		}
		if b.Min > b.Max {
			return AttributeSpecSet{}, fmt.Errorf(
				"attribute %q has an inverted range [%d, %d]", a, b.Min, b.Max)
		}
		specs[a] = AttributeSpec{Attribute: a, Predicate: attributePredicates[a], Min: b.Min, Max: b.Max}
	}
	for a := range bounds {
		if !a.Valid() {
			return AttributeSpecSet{}, &UnknownValueError{
				Kind: KindAttribute, Value: string(a), Allowed: attributeEnum.strings(),
			}
		}
	}
	return AttributeSpecSet{specs: specs}, nil
}

// DefaultAttributeSpecs returns the engine's v1 bounds. Used until a world
// package supplies its own.
func DefaultAttributeSpecs() AttributeSpecSet {
	set, err := NewAttributeSpecSet(defaultAttributeBounds)
	if err != nil {
		// Unreachable: the defaults are declared over the same closed set the
		// constructor validates against, and the closure test proves it.
		panic("vocabulary: default attribute bounds are invalid: " + err.Error())
	}
	return set
}

var engineAttributeSpecs = DefaultAttributeSpecs()

// For returns this set's contract for a. The bool is false for any attribute
// outside the closed set.
func (s AttributeSpecSet) For(a Attribute) (AttributeSpec, bool) {
	spec, ok := s.specs[a]
	return spec, ok
}

// Check is the bounds gate: it rejects an unknown attribute and an
// out-of-range value with distinguishable errors.
func (s AttributeSpecSet) Check(a Attribute, value int) error {
	spec, ok := s.For(a)
	if !ok {
		return &UnknownValueError{Kind: KindAttribute, Value: string(a), Allowed: attributeEnum.strings()}
	}
	if !spec.InBounds(value) {
		return &OutOfBoundsError{Attribute: a, Value: value, Min: spec.Min, Max: spec.Max}
	}
	return nil
}

// AttributeSpecFor returns the engine-default contract for a. The bool is
// false for any attribute outside the closed set.
func AttributeSpecFor(a Attribute) (AttributeSpec, bool) {
	return engineAttributeSpecs.For(a)
}

// CheckAttributeValue is the applier's bounds gate against the engine
// defaults: it rejects an unknown attribute and an out-of-range value with
// distinguishable errors.
func CheckAttributeValue(a Attribute, value int) error {
	return engineAttributeSpecs.Check(a, value)
}

// OutOfBoundsError reports a set_attribute value outside its registered
// range. Distinct from UnknownValueError so the applier's recorded rejection
// reason names the actual violation.
type OutOfBoundsError struct {
	Attribute Attribute
	Value     int
	Min       int
	Max       int
}

// Error implements error.
func (e *OutOfBoundsError) Error() string {
	return fmt.Sprintf("attribute %q value %d is outside registered bounds [%d, %d]",
		e.Attribute, e.Value, e.Min, e.Max)
}

// Relation names a registered relationship a relationship effect may write.
// The value doubles as the property segment of its predicate, which is why
// relations are lower-kebab rather than snake_case.
type Relation string

// The closed relation set.
const (
	// RelationAlliedWith asserts alliance.
	RelationAlliedWith Relation = "allied-with"
	// RelationHostileTo asserts hostility.
	RelationHostileTo Relation = "hostile-to"
	// RelationKnows asserts acquaintance.
	RelationKnows Relation = "knows"
	// RelationCarries asserts possession.
	RelationCarries Relation = "carries"
	// RelationOwesDebt asserts obligation.
	RelationOwesDebt Relation = "owes-debt"
)

var relationEnum = newEnum(KindRelation,
	RelationAlliedWith, RelationHostileTo, RelationKnows, RelationCarries, RelationOwesDebt)

// Relations returns the closed relation set.
func Relations() []Relation { return relationEnum.all() }

// Valid reports whether r is in the closed relation set.
func (r Relation) Valid() bool { return relationEnum.valid(r) }

// ParseRelation accepts only registered relations.
func ParseRelation(s string) (Relation, error) { return relationEnum.parse(s) }

var relationPredicates = map[Relation]Predicate{
	RelationAlliedWith: WorldRelationAlliedWith,
	RelationHostileTo:  WorldRelationHostileTo,
	RelationKnows:      WorldRelationKnows,
	RelationCarries:    WorldRelationCarries,
	RelationOwesDebt:   WorldRelationOwesDebt,
}

// RelationPredicate returns the canonical predicate for a registered
// relation. The bool is false for any relation outside the closed set.
func RelationPredicate(r Relation) (Predicate, bool) {
	p, ok := relationPredicates[r]
	return p, ok
}
