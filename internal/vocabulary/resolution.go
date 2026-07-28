package vocabulary

// Band thresholds for mechanic 2d6-pbta/v1.
const (
	// MaxMissTotal is the highest total that lands in the miss band.
	MaxMissTotal = 6
	// MaxPartialTotal is the highest total that lands in the partial band.
	MaxPartialTotal = 9
)

// BandForTotal maps a resolution total to its band under 2d6-pbta/v1:
// 6 or less is a miss, 7 through 9 is a partial, 10 or more is a full
// success. This is the single source the dice component bands with and the
// roll-result validator checks against, so a recorded band can never disagree
// with its recorded total.
func BandForTotal(total int) OutcomeBand {
	switch {
	case total <= MaxMissTotal:
		return BandMiss
	case total <= MaxPartialTotal:
		return BandPartial
	default:
		return BandFull
	}
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
