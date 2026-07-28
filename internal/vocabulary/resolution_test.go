package vocabulary_test

import (
	"errors"
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

// Boundary behavior is the whole content of the banding rule: 6/7 and 9/10
// are where a mis-typed comparison silently changes every outcome.
func TestBandForTotal_BoundariesAndFullRange(t *testing.T) {
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
		if got := vocabulary.BandForTotal(tc.total); got != tc.want {
			t.Fatalf("BandForTotal(%d) = %q want %q", tc.total, got, tc.want)
		}
	}
}

func TestBandForTotal_NeverReturnsAutoAndAlwaysReturnsAValidBand(t *testing.T) {
	for total := -20; total <= 40; total++ {
		band := vocabulary.BandForTotal(total)
		if band == vocabulary.BandAuto {
			t.Fatalf("BandForTotal(%d) returned the no-roll band", total)
		}
		if !band.IsRollBand() {
			t.Fatalf("BandForTotal(%d) returned %q, which is not roll-selectable", total, band)
		}
	}
}
