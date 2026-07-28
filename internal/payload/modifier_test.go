package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// mechanicSpec is the registered contract for the mechanic under test. The
// reachability argument the modifier bounds encode is a property OF a mechanic
// (how many dice, how many faces, where the thresholds fall), so the tests read
// the same registry the validator reads instead of restating 2d6's numbers.
func mechanicSpec(t *testing.T) vocabulary.MechanicSpec {
	t.Helper()
	spec, err := vocabulary.MechanicSpecFor(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("MechanicSpecFor: %v", err)
	}
	return spec
}

// registeredSpecs returns every registered mechanic contract, refusing an empty
// registry. Both bound tests below are loops over this slice, and a loop over
// nothing passes while proving nothing.
func registeredSpecs(t *testing.T) []vocabulary.MechanicSpec {
	t.Helper()
	specs := vocabulary.MechanicSpecs()
	if len(specs) == 0 {
		t.Fatal("no registered mechanics; the modifier-bound tests would be vacuous")
	}
	return specs
}

// reachableBands returns every band a roll under spec can still land in once
// the modifier total is applied.
//
// It walks the total RANGE rather than enumerating face combinations, which is
// equivalent: the sums of N dice with F ≥ 2 faces cover [N, N*F] contiguously.
func reachableBands(spec vocabulary.MechanicSpec, modifierTotal int) map[vocabulary.OutcomeBand]bool {
	reachable := make(map[vocabulary.OutcomeBand]bool, len(vocabulary.RollBands()))
	for total := spec.MinDiceTotal() + modifierTotal; total <= spec.MaxDiceTotal()+modifierTotal; total++ {
		reachable[spec.BandForTotal(total)] = true
	}
	return reachable
}

// The modifier bound exists to keep the dice deciding, so the test asserts
// that property rather than the numbers: at every legal total, all three bands
// must remain reachable. The per-modifier cap alone does not deliver this —
// three legal +3s sum to +9 and put a miss out of reach — which is why the
// bound is on the sum.
//
// It runs over EVERY registered mechanic. The bounds are package constants
// worked out for 2d6-pbta/v1, so a second mechanic with different dice would
// silently inherit a window that does not fit it; this fails instead, which is
// the reminder that the bounds have to become mechanic-keyed at that point.
func TestModifierTotalBounds_KeepEveryBandReachableUnderEveryRegisteredMechanic(t *testing.T) {
	for _, spec := range registeredSpecs(t) {
		for total := payload.MinModifierTotal; total <= payload.MaxModifierTotal; total++ {
			reachable := reachableBands(spec, total)
			for _, band := range vocabulary.RollBands() {
				if !reachable[band] {
					t.Fatalf("under %s, modifier total %d puts band %q out of reach; the dice stopped deciding. "+
						"If a new mechanic was added, the modifier bounds now need to be keyed by mechanic too.",
						spec.Mechanic, total, band)
				}
			}
		}
	}
}

// And the bound is tight, not merely safe: one step outside it in either
// direction, a band becomes unreachable. A looser assertion would pass for a
// bound of zero.
//
// This runs over every registered mechanic for the same reason the reachability
// test does, read from the other side. Reachability catches a window too WIDE
// for a new mechanic — the dice stop deciding, which is the loud failure.
// Tightness catches one too NARROW, which is the quiet one: under a
// hypothetical 3d6 spec (range 3–18, miss ≤9, partial ≤12) every band stays
// reachable at both -2 and +4, so the reachability test says nothing while
// [-2, +4] refuses modifier totals that mechanic supports and Roller.Roll
// rejects verdicts that were legal under their own mechanic. Both directions
// have the same remedy: key the bounds by mechanic.
func TestModifierTotalBounds_AreTightUnderEveryRegisteredMechanic(t *testing.T) {
	for _, spec := range registeredSpecs(t) {
		for _, total := range []int{payload.MinModifierTotal - 1, payload.MaxModifierTotal + 1} {
			reachable := reachableBands(spec, total)
			unreachable := 0
			for _, band := range vocabulary.RollBands() {
				if !reachable[band] {
					unreachable++
				}
			}
			if unreachable == 0 {
				t.Fatalf("under %s, modifier total %d still reaches every band, so the bound is tighter than that "+
					"mechanic needs and legal modifiers would be refused at the roller. "+
					"If a new mechanic was added, the modifier bounds now need to be keyed by mechanic too.",
					spec.Mechanic, total)
			}
		}
	}
}

// The failure the per-modifier cap missed, stated as behavior: modifier sets
// that are legal member-by-member and inside MaxModifiers, yet fix the outcome
// before the dice are thrown.
func TestVerdict_RejectsModifierSetsThatPredetermineTheBand(t *testing.T) {
	cases := []struct {
		name      string
		modifiers []payload.Modifier
	}{
		{
			name: "three legal bonuses guarantee a full success",
			modifiers: []payload.Modifier{
				{Source: vocabulary.ModifierTrait, Value: payload.MaxModifierValue},
				{Source: vocabulary.ModifierEquipment, Value: payload.MaxModifierValue},
				{Source: vocabulary.ModifierPosition, Value: payload.MaxModifierValue},
			},
		},
		{
			name: "two legal penalties guarantee a miss",
			modifiers: []payload.Modifier{
				{Source: vocabulary.ModifierCondition, Value: payload.MinModifierValue},
				{Source: vocabulary.ModifierPosition, Value: payload.MinModifierValue},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Guard: the case must be legal under every OTHER modifier rule,
			// or it would prove nothing about the summed bound.
			if len(tc.modifiers) > payload.MaxModifiers {
				t.Fatalf("the case uses %d modifiers, beyond the cap; it would fail for the wrong reason",
					len(tc.modifiers))
			}
			for _, m := range tc.modifiers {
				if err := m.Validate(); err != nil {
					t.Fatalf("modifier %+v is individually illegal; the case would fail for the wrong reason: %v", m, err)
				}
			}

			verdict := validVerdict()
			verdict.Modifiers = tc.modifiers

			decoded := decode(t, publishUnvalidated(t, verdict))
			err := decoded.Validate()
			if err == nil {
				t.Fatal("a modifier set that fixes the outcome before the roll was accepted")
			}
			if !strings.Contains(err.Error(), "summed modifiers") {
				t.Fatalf("rejection reason %q does not name the summed bound", err.Error())
			}
		})
	}
}

// The same bound on the replay record. A roll whose modifiers could not have
// reached every band is not a roll, and replay would faithfully reproduce a
// foregone conclusion.
func TestRollResult_RejectsAModifierTotalThatCouldNotReachEveryBand(t *testing.T) {
	roll := validRollResult()
	roll.Modifiers = []payload.Modifier{
		{Source: vocabulary.ModifierTrait, Value: payload.MaxModifierValue},
		{Source: vocabulary.ModifierEquipment, Value: payload.MaxModifierValue},
		{Source: vocabulary.ModifierPosition, Value: payload.MaxModifierValue},
	}
	roll.ModifierTotal = payload.SumModifiers(roll.Modifiers)
	roll.Total = roll.Dice[0] + roll.Dice[1] + roll.ModifierTotal
	roll.Band = mechanicSpec(t).BandForTotal(roll.Total)

	// The record is internally consistent — it fails only on the bound.
	decoded := decode(t, publishUnvalidated(t, roll))
	err := decoded.Validate()
	if err == nil {
		t.Fatal("a roll record whose modifiers predetermined the band was accepted")
	}
	if !strings.Contains(err.Error(), "summed modifiers") {
		t.Fatalf("rejection reason %q does not name the summed bound", err.Error())
	}
}

// Legal totals must still be accepted end to end, or the bound is just a
// stricter way of rejecting everything.
func TestRollResult_AcceptsEveryLegalModifierTotal(t *testing.T) {
	spec := mechanicSpec(t)
	for total := payload.MinModifierTotal; total <= payload.MaxModifierTotal; total++ {
		roll := validRollResult()
		roll.Modifiers = nil
		remaining := total
		for remaining != 0 {
			step := max(min(remaining, payload.MaxModifierValue), payload.MinModifierValue)
			roll.Modifiers = append(roll.Modifiers,
				payload.Modifier{Source: vocabulary.ModifierPosition, Value: step})
			remaining -= step
		}
		roll.ModifierTotal = total
		roll.Total = roll.Dice[0] + roll.Dice[1] + total
		roll.Band = spec.BandForTotal(roll.Total)

		if err := roll.Validate(); err != nil {
			t.Fatalf("a roll with the legal modifier total %d was rejected: %v", total, err)
		}
	}
}

// The note is LLM-authored free text, and rule-opaque is not the same as
// bounded. It is correctly never projected into a triple, but it rides on the
// Verdict and on every RollResult that preserves the verdict's modifiers, so
// unbounded it is unbounded bytes on two stored payloads per turn. Authored
// world text has a budget (MaxFactTextBytes) and player text has one
// (MaxActionTextBytes); this is the third free-text source in the loop, and it
// is checked on BOTH carriers because a bound enforced on one of them is a
// bound the other silently does not have.
func TestModifierNote_IsBoundedOnEveryPayloadThatCarriesIt(t *testing.T) {
	oversized := strings.Repeat("a", payload.MaxModifierNoteBytes+1)

	t.Run("verdict", func(t *testing.T) {
		verdict := validVerdict()
		verdict.Modifiers[0].Note = oversized
		assertNoteRejected(t, decode(t, publishUnvalidated(t, verdict)))
	})

	t.Run("roll_result", func(t *testing.T) {
		roll := validRollResult()
		roll.Modifiers[0].Note = oversized
		assertNoteRejected(t, decode(t, publishUnvalidated(t, roll)))
	})
}

func assertNoteRejected(t *testing.T, decoded message.Payload) {
	t.Helper()
	err := decoded.Validate()
	if err == nil {
		t.Fatalf("a %T carrying an over-budget modifier note was accepted", decoded)
	}
	if !strings.Contains(err.Error(), "note") {
		t.Fatalf("rejection reason %q does not name the note", err.Error())
	}
}

// And the budget admits a note exactly at it. An off-by-one here rejects the
// longest legal justification an adjudicator can write, which is a failure
// nobody sees until a persona writes one.
func TestModifierNote_AcceptsANoteExactlyAtTheBudget(t *testing.T) {
	atBound := strings.Repeat("a", payload.MaxModifierNoteBytes)

	verdict := validVerdict()
	verdict.Modifiers[0].Note = atBound
	if err := verdict.Validate(); err != nil {
		t.Fatalf("a verdict note of exactly %d bytes was rejected: %v", payload.MaxModifierNoteBytes, err)
	}

	roll := validRollResult()
	roll.Modifiers[0].Note = atBound
	if err := roll.Validate(); err != nil {
		t.Fatalf("a roll-record note of exactly %d bytes was rejected: %v", payload.MaxModifierNoteBytes, err)
	}
}

// SumModifiers is what both bounds are computed from, so it must not drift
// from the list it sums.
func TestSumModifiers_TotalsTheDeclaredList(t *testing.T) {
	if got := payload.SumModifiers(nil); got != 0 {
		t.Fatalf("SumModifiers(nil) = %d, want 0", got)
	}
	modifiers := []payload.Modifier{
		{Source: vocabulary.ModifierTrait, Value: 2},
		{Source: vocabulary.ModifierCondition, Value: -3},
		{Source: vocabulary.ModifierAssistance, Value: 1},
	}
	if got := payload.SumModifiers(modifiers); got != 0 {
		t.Fatalf("SumModifiers = %d, want 0", got)
	}
}
