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

// RequiresRoll is the ADVISORY roll expectation (design D7): the mapping's
// view of when the dice should be consulted — when the outcome is uncertain
// AND something is at stake.
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
func RequiresRoll(p Plausibility, r Risk) (bool, error) {
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
	uncertain := p == PlausibilityUnlikely || p == PlausibilityPlausible
	return uncertain && r != RiskNone, nil
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
