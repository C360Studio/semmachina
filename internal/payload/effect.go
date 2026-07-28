package payload

import (
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// EffectIntent is one proposed world change, drawn from the closed v1 effect
// vocabulary. Intents are proposals: the adjudicator declares them per
// outcome band, and the deterministic effect applier is what validates and
// commits them. Prose quality and graph integrity therefore never share a
// transaction.
//
// The per-type field sets are disjoint and enforced. An intent that carries
// fields belonging to another effect type is rejected rather than silently
// ignored, because a silently-ignored field is an effect the fiction promised
// and the world never received.
type EffectIntent struct {
	// Type selects which fields below are meaningful.
	Type vocabulary.EffectType `json:"type"`
	// Target is the entity the effect acts on. Required for every type.
	Target string `json:"target"`

	// Attribute and Value belong to set_attribute.
	Attribute vocabulary.Attribute `json:"attribute,omitempty"`
	// Value is a pointer so an explicit zero (health 0 — the death
	// threshold) is distinguishable from an omitted field.
	Value *int `json:"value,omitempty"`

	// Status belongs to set_status.
	Status vocabulary.Status `json:"status,omitempty"`

	// Location belongs to move_entity and is a location entity ID.
	Location string `json:"location,omitempty"`

	// Relation and Object belong to add_relationship and
	// remove_relationship.
	Relation vocabulary.Relation `json:"relation,omitempty"`
	Object   string              `json:"object,omitempty"`
}

// Validate checks vocabulary membership, per-type field shape, and
// registered bounds. It deliberately does NOT check target existence: that
// requires a graph read and belongs to the applier, which performs it for
// every intent before issuing any write.
func (i *EffectIntent) Validate() error {
	if _, err := vocabulary.ParseEffectType(string(i.Type)); err != nil {
		return err
	}
	if err := requireEntityID("target", i.Target); err != nil {
		return err
	}

	switch i.Type {
	case vocabulary.EffectSetAttribute:
		if err := i.requireOnly("attribute", "value"); err != nil {
			return err
		}
		if i.Value == nil {
			// requireOnly already rejects a missing value; this guard keeps
			// the dereference below safe if that ever changes.
			return fmt.Errorf("%s requires value", i.Type)
		}
		return vocabulary.CheckAttributeValue(i.Attribute, *i.Value)

	case vocabulary.EffectSetStatus:
		if err := i.requireOnly("status"); err != nil {
			return err
		}
		_, err := vocabulary.ParseStatus(string(i.Status))
		return err

	case vocabulary.EffectMoveEntity:
		if err := i.requireOnly("location"); err != nil {
			return err
		}
		return requireEntityID("location", i.Location)

	case vocabulary.EffectAddRelationship, vocabulary.EffectRemoveRelationship:
		if err := i.requireOnly("relation", "object"); err != nil {
			return err
		}
		if _, err := vocabulary.ParseRelation(string(i.Relation)); err != nil {
			return err
		}
		return requireEntityID("object", i.Object)

	default:
		// Unreachable: ParseEffectType above admits only the cases handled.
		return fmt.Errorf("effect type %q has no validation rule", i.Type)
	}
}

// Mutation returns the canonical predicate this intent writes together with
// the object to write, or an error. The applier turns the pair straight into a
// graph mutation; every predicate it can return is registered in
// vocabulary.AllPredicates() and therefore accepted by the graph write gate.
//
// Predicate and object come back TOGETHER on purpose. Splitting them would
// leave the applier to run its own type switch over the same closed set
// (set_attribute→Value, set_status→Status, move_entity→Location,
// *_relationship→Object), and two switches over one closed set kept in sync by
// hand is a leak waiting for the next effect type.
//
// It validates first, and that is the whole point of the method's shape. A
// predicate-only accessor that checked Attribute and Relation but not Status
// or Location reads as self-guarding while handing back a rule-matched
// predicate for an out-of-vocabulary status — an M2 leak: LLM-authored text
// reaching the graph's rule-matching surface. Validating first makes the guard
// symmetric over every field the pair is built from.
func (i *EffectIntent) Mutation() (vocabulary.Predicate, any, error) {
	if err := i.Validate(); err != nil {
		return "", nil, err
	}

	switch i.Type {
	case vocabulary.EffectSetAttribute:
		spec, ok := vocabulary.AttributeSpecFor(i.Attribute)
		if !ok {
			// Unreachable: Validate rejects an unregistered attribute. Kept
			// fail-closed so a future edit to Validate cannot open a hole here.
			return "", nil, &vocabulary.UnknownValueError{
				Kind:    vocabulary.KindAttribute,
				Value:   string(i.Attribute),
				Allowed: stringsOf(vocabulary.Attributes()),
			}
		}
		return spec.Predicate, *i.Value, nil

	case vocabulary.EffectSetStatus:
		// Objects are emitted as plain JSON scalars, never as the Go named
		// types, so a rule comparing against "wounded" matches.
		return vocabulary.CharacterStatusCurrent, string(i.Status), nil

	case vocabulary.EffectMoveEntity:
		return vocabulary.WorldLocationCurrent, i.Location, nil

	case vocabulary.EffectAddRelationship, vocabulary.EffectRemoveRelationship:
		predicate, ok := vocabulary.RelationPredicate(i.Relation)
		if !ok {
			// Unreachable for the same reason as the attribute case above.
			return "", nil, &vocabulary.UnknownValueError{
				Kind:    vocabulary.KindRelation,
				Value:   string(i.Relation),
				Allowed: stringsOf(vocabulary.Relations()),
			}
		}
		return predicate, i.Object, nil

	default:
		// Unreachable: Validate admits only the cases handled above.
		return "", nil, &vocabulary.UnknownValueError{
			Kind:    vocabulary.KindEffectType,
			Value:   string(i.Type),
			Allowed: stringsOf(vocabulary.EffectTypes()),
		}
	}
}

// requireOnly asserts that exactly the named type-specific fields are
// populated and every other type-specific field is empty.
func (i *EffectIntent) requireOnly(allowed ...string) error {
	populated := map[string]bool{
		"attribute": i.Attribute != "",
		"value":     i.Value != nil,
		"status":    i.Status != "",
		"location":  i.Location != "",
		"relation":  i.Relation != "",
		"object":    i.Object != "",
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = true
	}

	for _, field := range []string{"attribute", "value", "status", "location", "relation", "object"} {
		if populated[field] && !allowedSet[field] {
			return fmt.Errorf("%s must not carry %s", i.Type, field)
		}
		if !populated[field] && allowedSet[field] {
			return fmt.Errorf("%s requires %s", i.Type, field)
		}
	}
	return nil
}

func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}
