package vocabulary_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The roll gate is exhaustively specified over the closed product set. This
// enumerates all 16 (plausibility, risk) pairs rather than spot-checking,
// because the mapping is the authority the adjudicator's self-reported
// requires_roll is checked against — a hole here is a turn that silently
// skips or invents a roll.
func TestRequiresRoll_IsTotalOverTheClosedProductSet(t *testing.T) {
	// Roll exactly when the outcome is uncertain AND something is at stake.
	want := map[vocabulary.Plausibility]map[vocabulary.Risk]bool{
		vocabulary.PlausibilityImpossible: {
			vocabulary.RiskNone: false, vocabulary.RiskLow: false,
			vocabulary.RiskModerate: false, vocabulary.RiskHigh: false,
		},
		vocabulary.PlausibilityUnlikely: {
			vocabulary.RiskNone: false, vocabulary.RiskLow: true,
			vocabulary.RiskModerate: true, vocabulary.RiskHigh: true,
		},
		vocabulary.PlausibilityPlausible: {
			vocabulary.RiskNone: false, vocabulary.RiskLow: true,
			vocabulary.RiskModerate: true, vocabulary.RiskHigh: true,
		},
		vocabulary.PlausibilityCertain: {
			vocabulary.RiskNone: false, vocabulary.RiskLow: false,
			vocabulary.RiskModerate: false, vocabulary.RiskHigh: false,
		},
	}

	covered := 0
	for _, p := range vocabulary.Plausibilities() {
		for _, r := range vocabulary.Risks() {
			expected, ok := want[p][r]
			if !ok {
				t.Fatalf("the expectation table does not cover (%s, %s)", p, r)
			}
			got, err := vocabulary.RequiresRoll(p, r)
			if err != nil {
				t.Fatalf("RequiresRoll(%s, %s) errored on in-vocabulary input: %v", p, r, err)
			}
			if got != expected {
				t.Fatalf("RequiresRoll(%s, %s) = %v want %v", p, r, got, expected)
			}
			covered++
		}
	}
	if covered != len(vocabulary.Plausibilities())*len(vocabulary.Risks()) {
		t.Fatalf("covered %d pairs, expected the full product set", covered)
	}
}

func TestRequiresRoll_FailsClosedOutsideTheVocabulary(t *testing.T) {
	cases := []struct {
		name string
		p    vocabulary.Plausibility
		r    vocabulary.Risk
		kind vocabulary.Kind
	}{
		{"unknown plausibility", "likely", vocabulary.RiskHigh, vocabulary.KindPlausibility},
		{"unknown risk", vocabulary.PlausibilityPlausible, "severe", vocabulary.KindRisk},
		{"empty plausibility", "", vocabulary.RiskHigh, vocabulary.KindPlausibility},
		{"empty risk", vocabulary.PlausibilityPlausible, "", vocabulary.KindRisk},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vocabulary.RequiresRoll(tc.p, tc.r)
			if err == nil {
				t.Fatalf("expected rejection for (%q, %q)", tc.p, tc.r)
			}
			if got {
				t.Fatal("a rejected verdict must not report that it requires a roll")
			}
			var unknown *vocabulary.UnknownValueError
			if !errors.As(err, &unknown) {
				t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
			}
			if unknown.Kind != tc.kind {
				t.Fatalf("error names kind %q, want %q", unknown.Kind, tc.kind)
			}
		})
	}
}

// BandsForVerdict is what the verdict validator uses to require the exactly
// right band keys, so it must agree with the roll gate in both directions.
func TestBandsForVerdict_MatchesTheRollGate(t *testing.T) {
	rolled := vocabulary.BandsForVerdict(true)
	if len(rolled) != 3 {
		t.Fatalf("roll-requiring verdict bands: got %v want three", rolled)
	}
	for _, b := range rolled {
		if !b.IsRollBand() {
			t.Fatalf("band %q is not roll-selectable but was required of a rolling verdict", b)
		}
	}

	auto := vocabulary.BandsForVerdict(false)
	if len(auto) != 1 || auto[0] != vocabulary.BandAuto {
		t.Fatalf("no-roll verdict bands: got %v want exactly [auto]", auto)
	}
}

// specFor is the test's own lookup, failing loudly rather than returning a
// zero spec whose thresholds would silently band everything as a full success.
func specFor(t *testing.T, m vocabulary.Mechanic) vocabulary.MechanicSpec {
	t.Helper()
	spec, err := vocabulary.MechanicSpecFor(m)
	if err != nil {
		t.Fatalf("MechanicSpecFor(%q): %v", m, err)
	}
	return spec
}

// Boundary behavior is the whole content of the banding rule: 6/7 and 9/10
// are where a mis-typed comparison silently changes every outcome.
func TestMechanicSpec_BandForTotalBoundariesAndFullRange(t *testing.T) {
	spec := specFor(t, vocabulary.Mechanic2d6PbtaV1)

	cases := []struct {
		total int
		want  vocabulary.OutcomeBand
	}{
		{-5, vocabulary.BandMiss},
		{2, vocabulary.BandMiss},
		{5, vocabulary.BandMiss},
		{6, vocabulary.BandMiss},
		{7, vocabulary.BandPartial},
		{8, vocabulary.BandPartial},
		{9, vocabulary.BandPartial},
		{10, vocabulary.BandFull},
		{12, vocabulary.BandFull},
		{25, vocabulary.BandFull},
	}

	for _, tc := range cases {
		if got := spec.BandForTotal(tc.total); got != tc.want {
			t.Fatalf("BandForTotal(%d) = %q want %q", tc.total, got, tc.want)
		}
	}
}

func TestMechanicSpec_BandForTotalNeverReturnsAutoAndAlwaysReturnsAValidBand(t *testing.T) {
	spec := specFor(t, vocabulary.Mechanic2d6PbtaV1)
	for total := -20; total <= 40; total++ {
		band := spec.BandForTotal(total)
		if band == vocabulary.BandAuto {
			t.Fatalf("BandForTotal(%d) returned the no-roll band", total)
		}
		if !band.IsRollBand() {
			t.Fatalf("BandForTotal(%d) returned %q, which is not roll-selectable", total, band)
		}
	}
}

// Banding is keyed by mechanic version, which is the property that stops a
// future v2 record from being re-banded under v1's thresholds. The registry is
// the only way to reach a threshold, so an unregistered mechanic must produce
// no spec at all rather than a zero one — a zero spec bands EVERY total as a
// full success, which is the exact silent failure the keying exists to prevent.
func TestMechanicSpecFor_RejectsAnUnregisteredMechanicRatherThanReturningZeroThresholds(t *testing.T) {
	spec, err := vocabulary.MechanicSpecFor("2d6-pbta/v2")
	if err == nil {
		t.Fatal("an unregistered mechanic returned a spec")
	}
	var unknown *vocabulary.UnknownValueError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
	}
	if unknown.Kind != vocabulary.KindMechanic {
		t.Fatalf("error names kind %q, want %q", unknown.Kind, vocabulary.KindMechanic)
	}
	if spec != (vocabulary.MechanicSpec{}) {
		t.Fatalf("a refused lookup returned thresholds: %+v", spec)
	}
	// The zero spec is exactly the hazard: prove it would mis-band, so nobody
	// is tempted to "just use the zero value" on the error path.
	if got := (vocabulary.MechanicSpec{}).BandForTotal(2); got != vocabulary.BandFull {
		t.Fatalf("zero spec bands a total of 2 as %q; this test's premise is stale", got)
	}
}

func TestMechanicSpecs_CoverExactlyTheClosedMechanicSet(t *testing.T) {
	enumerated := vocabulary.Mechanics()
	registered := vocabulary.RegisteredMechanics()

	if len(enumerated) != len(registered) {
		t.Fatalf("%d mechanics are enumerated but %d have specs: %v vs %v",
			len(enumerated), len(registered), enumerated, registered)
	}
	for _, m := range enumerated {
		if !slices.Contains(registered, m) {
			t.Fatalf("mechanic %q is in the closed set but has no registered spec; its recorded rolls could not be banded", m)
		}
	}
	for _, m := range registered {
		if !slices.Contains(enumerated, m) {
			t.Fatalf("mechanic %q has a spec but is not in the closed set", m)
		}
	}
}

// Every registered mechanic must be internally coherent, or its dice cannot
// produce the bands it declares.
func TestMechanicSpecs_AreInternallyCoherent(t *testing.T) {
	for _, spec := range vocabulary.MechanicSpecs() {
		if spec.Dice < 1 {
			t.Fatalf("%s rolls %d dice", spec.Mechanic, spec.Dice)
		}
		if spec.Faces < 2 {
			t.Fatalf("%s has %d-faced dice, which is not a die", spec.Mechanic, spec.Faces)
		}
		if spec.MaxMissTotal >= spec.MaxPartialTotal {
			t.Fatalf("%s has an empty partial band: miss ends at %d, partial at %d",
				spec.Mechanic, spec.MaxMissTotal, spec.MaxPartialTotal)
		}
		// Without modifiers, the raw dice must be able to reach every band —
		// otherwise the mechanic declares a band its own dice can never select.
		if spec.MinDiceTotal() > spec.MaxMissTotal {
			t.Fatalf("%s can never miss: its lowest total is %d and the miss band ends at %d",
				spec.Mechanic, spec.MinDiceTotal(), spec.MaxMissTotal)
		}
		if spec.MaxDiceTotal() <= spec.MaxPartialTotal {
			t.Fatalf("%s can never fully succeed: its highest total is %d and the partial band ends at %d",
				spec.Mechanic, spec.MaxDiceTotal(), spec.MaxPartialTotal)
		}
		if !spec.ValidDie(1) || !spec.ValidDie(spec.Faces) {
			t.Fatalf("%s rejects its own extreme faces", spec.Mechanic)
		}
		if spec.ValidDie(0) || spec.ValidDie(spec.Faces+1) {
			t.Fatalf("%s accepts a face outside [1, %d]", spec.Mechanic, spec.Faces)
		}
	}
}

// The one registered mechanic is 2d6-pbta/v1 as the spec names it. Pinned so a
// silent edit to the registry — the numbers every recorded roll is validated
// against — fails here rather than re-banding history.
func TestMechanicSpec_2d6PbtaV1MatchesTheSpecifiedContract(t *testing.T) {
	spec := specFor(t, vocabulary.Mechanic2d6PbtaV1)
	want := vocabulary.MechanicSpec{
		Mechanic:        vocabulary.Mechanic2d6PbtaV1,
		Dice:            2,
		Faces:           6,
		MaxMissTotal:    6,
		MaxPartialTotal: 9,
	}
	if spec != want {
		t.Fatalf("2d6-pbta/v1 spec = %+v, want %+v", spec, want)
	}
}
