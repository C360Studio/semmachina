package vocabulary

import "slices"

// MechanicSpec is the complete resolution contract for ONE mechanic version:
// how many dice it rolls, how many faces each die carries, and where the band
// boundaries fall.
//
// These numbers are keyed by mechanic version rather than declared as
// package-level constants, and that is the whole point of the type. A banding
// function that takes only a total cannot tell which mechanic produced it, so a
// roll recorded under a future `2d6-pbta/v2` with different thresholds would be
// re-banded under v1's numbers by the validator and by any replay — and the
// re-band would look exactly like agreement. Requiring the mechanic in hand at
// every banding site makes that mistake impossible to write. Today the registry
// has one member; the discipline exists for the second.
type MechanicSpec struct {
	// Mechanic is the version this contract belongs to.
	Mechanic Mechanic
	// Dice is how many dice are rolled.
	Dice int
	// Faces is the number of faces on each die; values run 1..Faces.
	Faces int
	// MaxMissTotal is the highest total that lands in the miss band.
	MaxMissTotal int
	// MaxPartialTotal is the highest total that lands in the partial band.
	MaxPartialTotal int
}

// BandForTotal maps a resolution total to its band under this mechanic. Under
// 2d6-pbta/v1 that is 6 or less miss, 7 through 9 partial, 10 or more full.
//
// It is the single source the dice component bands with and the roll-result
// validator checks against, so a recorded band can never disagree with its own
// recorded total — under its own mechanic.
func (s MechanicSpec) BandForTotal(total int) OutcomeBand {
	switch {
	case total <= s.MaxMissTotal:
		return BandMiss
	case total <= s.MaxPartialTotal:
		return BandPartial
	default:
		return BandFull
	}
}

// MinDiceTotal is the lowest total the dice alone can produce (all ones).
func (s MechanicSpec) MinDiceTotal() int { return s.Dice }

// MaxDiceTotal is the highest total the dice alone can produce (all faces).
func (s MechanicSpec) MaxDiceTotal() int { return s.Dice * s.Faces }

// ValidDie reports whether v is a face this mechanic's dice can show.
func (s MechanicSpec) ValidDie(v int) bool { return v >= 1 && v <= s.Faces }

// mechanicSpecs is the registry. A mechanic constant with no entry here is
// caught by TestMechanicSpecs_CoverExactlyTheClosedMechanicSet — an
// unregistered mechanic is one whose recorded rolls nothing can band.
var mechanicSpecs = map[Mechanic]MechanicSpec{
	Mechanic2d6PbtaV1: {
		Mechanic:        Mechanic2d6PbtaV1,
		Dice:            2,
		Faces:           6,
		MaxMissTotal:    6,
		MaxPartialTotal: 9,
	},
}

// MechanicSpecFor returns the registered contract for m.
//
// The error is an *UnknownValueError for any mechanic outside the closed set,
// which makes this both the membership check and the spec lookup: a caller
// cannot obtain thresholds without having proven the mechanic is one the engine
// knows.
func MechanicSpecFor(m Mechanic) (MechanicSpec, error) {
	spec, ok := mechanicSpecs[m]
	if !ok {
		return MechanicSpec{}, &UnknownValueError{
			Kind: KindMechanic, Value: string(m), Allowed: mechanicEnum.strings(),
		}
	}
	return spec, nil
}

// MechanicSpecs returns every registered contract in Mechanics() order, so
// callers and error messages are deterministic.
func MechanicSpecs() []MechanicSpec {
	out := make([]MechanicSpec, 0, len(mechanicSpecs))
	for _, m := range Mechanics() {
		if spec, ok := mechanicSpecs[m]; ok {
			out = append(out, spec)
		}
	}
	return out
}

// RegisteredMechanics returns the registry's own key set, sorted.
//
// It reports what the registry HOLDS rather than what the enumeration lists, so
// a test can compare the two and catch drift in either direction: a mechanic
// with no spec, and a spec for a mechanic that is not in the closed set.
func RegisteredMechanics() []Mechanic {
	out := make([]Mechanic, 0, len(mechanicSpecs))
	for m := range mechanicSpecs {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

// RollGateMapping names a VERSIONED advisory roll mapping — one specific
// answer to "when should the dice come out, given (plausibility, risk)?".
//
// It is versioned for exactly the reason Mechanic and RNG are, and the argument
// is the same one in a different costume. The mapping is advisory and expected
// to be TUNED with play (design D12): the whole point of letting the adjudicator
// hold the gate is that we learn, from the divergence rate, whether the
// persona's fiction judgment tracks the table. Tuning the table is therefore not
// a hypothetical — it is the plan.
//
// And a plan to change a mapping is a plan to invalidate every derived value
// recorded under it. The disagreement a ledger records IS derivable from the
// stored verdict today, so a version looks redundant until the day the mapping
// moves — at which point every historical turn's derived "the persona and the
// mapping disagreed" silently flips to "they agreed", and the archive quietly
// rewrites what was true at the time. Recording the version is what makes the
// ledger's account of a turn a record rather than a recomputation.
type RollGateMapping string

// The closed roll-gate mapping set.
//
// Old versions STAY here when a new one is added, exactly as old mechanics
// would: a manifest recorded under v1 must remain readable after v2 ships, and
// a version this engine never had is a corrupt or foreign record rather than
// an old one.
const (
	// RollGateMappingV1 is `plausibility ∈ {unlikely, plausible} AND risk ≠ none`,
	// pinned as a whole truth table by rollGateGolden in the package tests.
	RollGateMappingV1 RollGateMapping = "roll-gate/v1"
)

var rollGateMappingEnum = newEnum(KindRollGateMapping, RollGateMappingV1)

// RollGateMappings returns the closed roll-gate mapping set.
func RollGateMappings() []RollGateMapping { return rollGateMappingEnum.all() }

// Valid reports whether m is in the closed roll-gate mapping set.
func (m RollGateMapping) Valid() bool { return rollGateMappingEnum.valid(m) }

// ParseRollGateMapping accepts only registered mapping versions.
func ParseRollGateMapping(s string) (RollGateMapping, error) { return rollGateMappingEnum.parse(s) }

// rollGateMappings is the registry: each version paired with the table it
// names. Same shape as mechanicSpecs, for the same reason — a version whose
// implementation the engine no longer holds is a version whose records nothing
// can check.
//
// An old version's function STAYS here when a new one is added. That is the
// cost of an archive and it is deliberately paid: a ledgered turn from last
// month must be verifiable against the mapping it actually ran under, not
// re-decided under the one that replaced it.
var rollGateMappings = map[RollGateMapping]func(Plausibility, Risk) bool{
	RollGateMappingV1: func(p Plausibility, r Risk) bool {
		uncertain := p == PlausibilityUnlikely || p == PlausibilityPlausible
		return uncertain && r != RiskNone
	},
}

// RegisteredRollGateMappings returns the registry's own key set, sorted.
//
// It reports what the registry HOLDS rather than what the enumeration lists, so
// drift is catchable in both directions: an enumerated version with no table
// (records nothing can verify) and a table for a version outside the closed set
// (an implementation nothing can name).
func RegisteredRollGateMappings() []RollGateMapping {
	out := make([]RollGateMapping, 0, len(rollGateMappings))
	for mapping := range rollGateMappings {
		out = append(out, mapping)
	}
	slices.Sort(out)
	return out
}

// RollGateMappingVersion is the version RequiresRoll implements TODAY.
//
// RequiresRoll is DEFINED as this version's table rather than as a second copy
// of it, which is what closes the hazard a bare version constant would leave
// open: a stamp cannot lag its mapping, because there is only one mapping and
// the stamp names it. What remains is editing v1's table in place — a change to
// history rather than to the future — and the pinned golden table fails on that.
func RollGateMappingVersion() RollGateMapping { return RollGateMappingV1 }

// AdviseRollGate returns the NAMED mapping version's advice.
//
// Taking the version as an argument is the point. A recorded expectation
// carries the version it was computed under, so verifying one means asking the
// table it names — and asking today's table instead would re-decide a
// historical turn's disagreement every time the mapping is tuned, which is the
// exact silent rewrite the version exists to prevent.
//
// The error is an *UnknownValueError for any version outside the registry,
// which makes this both the membership check and the lookup: a caller cannot
// obtain advice without having proven the mapping is one the engine still
// holds.
func AdviseRollGate(mapping RollGateMapping, p Plausibility, r Risk) (bool, error) {
	advise, ok := rollGateMappings[mapping]
	if !ok {
		return false, &UnknownValueError{
			Kind: KindRollGateMapping, Value: string(mapping), Allowed: rollGateMappingEnum.strings(),
		}
	}
	if !p.Valid() {
		return false, &UnknownValueError{
			Kind: KindPlausibility, Value: string(p), Allowed: plausibilityEnum.strings(),
		}
	}
	if !r.Valid() {
		return false, &UnknownValueError{
			Kind: KindRisk, Value: string(r), Allowed: riskEnum.strings(),
		}
	}
	return advise(p, r), nil
}

// RequiresRoll is the ADVISORY roll expectation (design D7) under the CURRENT
// mapping: when the outcome is uncertain AND something is at stake.
//
//	roll = plausibility ∈ {unlikely, plausible} AND risk ≠ none
//
// The two no-roll cases are not "automatic success". An impossible or certain
// plausibility means the fiction already decided the outcome, and a risk of
// none means nothing hangs on it; in both cases the adjudicator's single
// `auto` band carries whatever the fiction dictates — for an impossible
// action that is a failure-shaped intent set.
//
// This function does NOT gate a verdict. Fiction decides, the mapping advises:
// narrative positioning dictates mechanics is the project's founding claim, so
// the persona that read the fiction holds the roll gate and its reported
// requires_roll is authoritative. This mapping is the recorded expectation the
// reported gate is COMPARED against (payload.VerdictScalars.RollGate), never
// the authority it is overruled by. A disagreement is data — the signal for
// whether the adjudicator's judgment tracks the mapping — not a rejection.
// Nothing in the dice path may call it as a precondition (design D12).
//
// The error is non-nil only for values outside the closed sets, which makes
// this total over the vocabulary and fail-closed outside it.
//
// TUNING THE MAPPING MEANS ADDING A VERSION, never editing v1's function: this
// call resolves through the registry, so a new table arrives as a new entry
// plus a new RollGateMappingVersion, and every ledgered turn stays verifiable
// against the table it actually ran under.
func RequiresRoll(p Plausibility, r Risk) (bool, error) {
	return AdviseRollGate(RollGateMappingVersion(), p, r)
}

// BandsForVerdict returns the exact set of bands a verdict must declare
// effect intents for: the three roll bands when the dice are consulted, and
// the single auto band when they are not.
//
// Its argument is the verdict's REPORTED requires_roll, never the mapping's
// advice, so band-shape validation keeps the verdict internally coherent and
// the engine never requires a band the persona did not author. The returned
// order is stable so validation messages are deterministic.
func BandsForVerdict(requiresRoll bool) []OutcomeBand {
	if requiresRoll {
		return RollBands()
	}
	return []OutcomeBand{BandAuto}
}
