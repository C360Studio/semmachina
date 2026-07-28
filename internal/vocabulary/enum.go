package vocabulary

import (
	"fmt"
	"slices"
	"strings"
)

// Kind names one closed value set. Kinds are the keys of Sets() — the
// enumeration the adjudicator tool schema and the rule pack build from.
type Kind string

// The closed value sets this package owns.
const (
	// KindPlausibility is the adjudicator's judgment of whether the fiction
	// admits the proposed action at all.
	KindPlausibility Kind = "plausibility"
	// KindRisk is the adjudicator's judgment of what is at stake.
	KindRisk Kind = "risk"
	// KindConsequence is the shape of the consequence a miss or partial
	// success carries.
	KindConsequence Kind = "consequence"
	// KindOutcomeBand is the resolution band an effect-intent set belongs to.
	KindOutcomeBand Kind = "outcome_band"
	// KindModifierSource is the typed provenance of a roll modifier.
	KindModifierSource Kind = "modifier_source"
	// KindEffectType is the v1 effect vocabulary the applier commits.
	KindEffectType Kind = "effect_type"
	// KindStatus is the condition enum set by a set_status effect.
	KindStatus Kind = "status"
	// KindAttribute is the bounded numeric attribute set by a set_attribute
	// effect.
	KindAttribute Kind = "attribute"
	// KindRelation is the registered relationship predicate list.
	KindRelation Kind = "relation"
	// KindEntityKind is the declared type of a world entity, which doubles as
	// the type segment of its six-part ID.
	KindEntityKind Kind = "entity_kind"
	// KindTurnPhase is the durable turn-phase vocabulary.
	KindTurnPhase Kind = "turn_phase"
	// KindChannelAdapter is the player-I/O adapter vocabulary.
	KindChannelAdapter Kind = "channel_adapter"
	// KindMechanic is the versioned resolution mechanic.
	KindMechanic Kind = "mechanic"
	// KindRNG is the versioned pseudo-random generator.
	KindRNG Kind = "rng"
)

// UnknownValueError reports a value outside a closed set. Every Parse* helper
// returns this so callers (notably the adjudicator tool boundary) can reject
// an out-of-vocabulary exit with an actionable, machine-readable reason
// instead of a bare string comparison.
type UnknownValueError struct {
	Kind    Kind
	Value   string
	Allowed []string
}

// Error implements error.
func (e *UnknownValueError) Error() string {
	return fmt.Sprintf("%s: %q is not in the closed vocabulary (allowed: %s)",
		e.Kind, e.Value, strings.Join(e.Allowed, ", "))
}

// enum carries one closed value set. It exists so membership, enumeration,
// and parse-with-a-good-error are implemented exactly once for every kind.
type enum[T ~string] struct {
	k      Kind
	values []T
}

func newEnum[T ~string](k Kind, values ...T) enum[T] {
	return enum[T]{k: k, values: values}
}

func (e enum[T]) all() []T { return slices.Clone(e.values) }

func (e enum[T]) valid(v T) bool { return slices.Contains(e.values, v) }

func (e enum[T]) parse(s string) (T, error) {
	v := T(s)
	if e.valid(v) {
		return v, nil
	}
	var zero T
	return zero, &UnknownValueError{Kind: e.k, Value: s, Allowed: e.strings()}
}

func (e enum[T]) kind() Kind { return e.k }

func (e enum[T]) strings() []string {
	out := make([]string, 0, len(e.values))
	for _, v := range e.values {
		out = append(out, string(v))
	}
	return out
}

// enumSet is the non-generic view of a closed set. enum[T] satisfies it for
// every T because its methods carry no type parameters, which is what lets
// Sets() enumerate every kind uniformly.
type enumSet interface {
	kind() Kind
	strings() []string
}

// registeredEnums is the authoritative list of closed sets. A new enum that
// is not added here is invisible to Sets(), to the generated tool schema, and
// to the rule pack — which is exactly what
// TestClosedSets_EveryDeclaredConstantIsEnumerated fails on.
var registeredEnums = []enumSet{
	plausibilityEnum,
	riskEnum,
	consequenceEnum,
	outcomeBandEnum,
	modifierSourceEnum,
	effectTypeEnum,
	statusEnum,
	attributeEnum,
	relationEnum,
	entityKindEnum,
	turnPhaseEnum,
	channelAdapterEnum,
	mechanicEnum,
	rngEnum,
}

// Sets returns every closed value set keyed by kind. This is the projection
// the adjudicator's JSON-Schema enum lists and the rule pack's allowed-value
// lists are generated from, so all three consumers share one definition.
func Sets() map[Kind][]string {
	out := make(map[Kind][]string, len(registeredEnums))
	for _, e := range registeredEnums {
		out[e.kind()] = e.strings()
	}
	return out
}

// Kinds returns every registered kind in declaration order.
func Kinds() []Kind {
	out := make([]Kind, 0, len(registeredEnums))
	for _, e := range registeredEnums {
		out = append(out, e.kind())
	}
	return out
}
