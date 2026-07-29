package vocabulary_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// rollGateGolden pins each mapping VERSION's complete truth table.
//
// Keyed by version rather than written inline, because that is what turns
// RollGateMappingVersion() from a label into an enforced fact. The mapping is
// advisory and expected to be tuned with play, and every recorded expectation
// in the campaign ledger is stamped with the version it was computed under — so
// an edit to RequiresRoll that did NOT declare a new version would leave every
// historical stamp claiming a table that no longer exists. Here, that edit fails
// this test instead.
var rollGateGolden = map[vocabulary.RollGateMapping]map[vocabulary.Plausibility]map[vocabulary.Risk]bool{
	// v1: roll exactly when the outcome is uncertain AND something is at stake.
	vocabulary.RollGateMappingV1: {
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
	},
}

// The roll gate is exhaustively specified over the closed product set. This
// enumerates all 16 (plausibility, risk) pairs rather than spot-checking,
// because the mapping is the expectation the adjudicator's self-reported
// requires_roll is COMPARED against — a hole here is a recorded disagreement
// nobody can trust.
func TestRequiresRoll_IsTotalOverTheClosedProductSet(t *testing.T) {
	want, pinned := rollGateGolden[vocabulary.RollGateMappingVersion()]
	if !pinned {
		t.Fatalf("mapping version %q has no pinned truth table; RequiresRoll changed without one",
			vocabulary.RollGateMappingVersion())
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

// EVERY registered mapping version — not just the current one — must reproduce
// its pinned table exactly.
//
// This is what makes an archive checkable. A ledgered turn from before a tuning
// carries the version it ran under, and verifying it means asking that version's
// table; if an old table were edited in place, every historical record would be
// re-judged against a rule that did not exist when it was written. So the golden
// tables cover the whole registry, and editing v1 after v2 ships fails here.
func TestAdviseRollGate_EveryRegisteredVersionReproducesItsPinnedTable(t *testing.T) {
	versions := vocabulary.RollGateMappings()
	if len(versions) == 0 {
		t.Fatal("no roll-gate mapping versions are enumerated")
	}

	for _, version := range versions {
		want, pinned := rollGateGolden[version]
		if !pinned {
			t.Fatalf("mapping version %q is enumerated but has no pinned truth table", version)
		}
		for _, p := range vocabulary.Plausibilities() {
			for _, r := range vocabulary.Risks() {
				expected, covered := want[p][r]
				if !covered {
					t.Fatalf("version %q pins no expectation for (%s, %s)", version, p, r)
				}
				got, err := vocabulary.AdviseRollGate(version, p, r)
				if err != nil {
					t.Fatalf("AdviseRollGate(%s, %s, %s): %v", version, p, r, err)
				}
				if got != expected {
					t.Fatalf("AdviseRollGate(%s, %s, %s) = %v want %v", version, p, r, got, expected)
				}
			}
		}
	}
	if len(rollGateGolden) != len(versions) {
		t.Fatalf("%d tables are pinned for %d enumerated versions", len(rollGateGolden), len(versions))
	}
}

// The enumeration and the implementation registry must be the same set.
//
// Drift fails differently in each direction. An enumerated version with no
// implementation would parse off a stored manifest and then have nothing to
// verify it against; an implementation outside the closed set is a table nothing
// can name, so no record could ever be stamped with it.
func TestRollGateMappings_CoverExactlyTheClosedSet(t *testing.T) {
	enumerated := vocabulary.RollGateMappings()
	registered := vocabulary.RegisteredRollGateMappings()

	if len(enumerated) != len(registered) {
		t.Fatalf("%d versions are enumerated but %d have tables: %v vs %v",
			len(enumerated), len(registered), enumerated, registered)
	}
	for _, version := range enumerated {
		if !slices.Contains(registered, version) {
			t.Fatalf("version %q is in the closed set but has no table; a manifest stamped with it "+
				"could never be verified", version)
		}
	}
	for _, version := range registered {
		if !slices.Contains(enumerated, version) {
			t.Fatalf("version %q has a table but is not in the closed set", version)
		}
	}

	current := vocabulary.RollGateMappingVersion()
	if !slices.Contains(enumerated, current) {
		t.Fatalf("the current version %q is not in the closed set; every manifest this engine writes "+
			"would carry a value ParseRollGateMapping refuses", current)
	}
}

// RequiresRoll is DEFINED as the current version's table rather than a second
// copy of it, which is what stops a stamp from lagging its mapping. Prove the
// two are the same function over the whole product set.
func TestRequiresRoll_IsExactlyTheCurrentMappingVersion(t *testing.T) {
	current := vocabulary.RollGateMappingVersion()
	for _, p := range vocabulary.Plausibilities() {
		for _, r := range vocabulary.Risks() {
			viaCurrent, err := vocabulary.RequiresRoll(p, r)
			if err != nil {
				t.Fatalf("RequiresRoll(%s, %s): %v", p, r, err)
			}
			viaVersion, err := vocabulary.AdviseRollGate(current, p, r)
			if err != nil {
				t.Fatalf("AdviseRollGate(%s, %s, %s): %v", current, p, r, err)
			}
			if viaCurrent != viaVersion {
				t.Fatalf("RequiresRoll(%s, %s) = %v but version %s advises %v; the stamp has drifted from "+
					"the mapping it names", p, r, viaCurrent, current, viaVersion)
			}
		}
	}
}

// A version the engine no longer holds is refused rather than silently
// re-judged under the table it does hold.
func TestAdviseRollGate_RefusesAnUnregisteredVersion(t *testing.T) {
	advised, err := vocabulary.AdviseRollGate(
		"roll-gate/v2", vocabulary.PlausibilityPlausible, vocabulary.RiskHigh)
	if err == nil {
		t.Fatal("an unregistered mapping version returned advice")
	}
	if advised {
		t.Fatal("a refused lookup still advised a roll")
	}
	var unknown *vocabulary.UnknownValueError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
	}
	if unknown.Kind != vocabulary.KindRollGateMapping {
		t.Fatalf("error names kind %q, want %q", unknown.Kind, vocabulary.KindRollGateMapping)
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
