package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// reachableBands returns every band a 2d6 roll can still land in once the
// modifier total is applied.
func reachableBands(modifierTotal int) map[vocabulary.OutcomeBand]bool {
	reachable := make(map[vocabulary.OutcomeBand]bool, len(vocabulary.RollBands()))
	for first := 1; first <= payload.DieFaces; first++ {
		for second := 1; second <= payload.DieFaces; second++ {
			reachable[vocabulary.BandForTotal(first+second+modifierTotal)] = true
		}
	}
	return reachable
}

// The modifier bound exists to keep the dice deciding, so the test asserts
// that property rather than the numbers: at every legal total, all three bands
// must remain reachable. The per-modifier cap alone does not deliver this —
// three legal +3s sum to +9 and put a miss out of reach — which is why the
// bound is on the sum.
func TestModifierTotalBounds_KeepEveryBandReachable(t *testing.T) {
	for total := payload.MinModifierTotal; total <= payload.MaxModifierTotal; total++ {
		reachable := reachableBands(total)
		for _, band := range vocabulary.RollBands() {
			if !reachable[band] {
				t.Fatalf("modifier total %d puts band %q out of reach; the dice stopped deciding", total, band)
			}
		}
	}
}

// And the bound is tight, not merely safe: one step outside it in either
// direction, a band becomes unreachable. A looser assertion would pass for a
// bound of zero.
func TestModifierTotalBounds_AreTight(t *testing.T) {
	for _, total := range []int{payload.MinModifierTotal - 1, payload.MaxModifierTotal + 1} {
		reachable := reachableBands(total)
		unreachable := 0
		for _, band := range vocabulary.RollBands() {
			if !reachable[band] {
				unreachable++
			}
		}
		if unreachable == 0 {
			t.Fatalf("modifier total %d still reaches every band, so the bound is tighter than it needs to be", total)
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
	roll.Band = vocabulary.BandForTotal(roll.Total)

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
		roll.Band = vocabulary.BandForTotal(roll.Total)

		if err := roll.Validate(); err != nil {
			t.Fatalf("a roll with the legal modifier total %d was rejected: %v", total, err)
		}
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
