package payload

import (
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Modifier bounds. These exist because an unbounded modifier list lets the
// adjudicator pre-determine the band — declare "+40, source: position" and
// the dice stop deciding anything. Seeded determinism is worthless if the
// roll's outcome was already fixed by the persona that requested it, so the
// bound is part of the closed exit contract, not a style rule.
//
// Per-modifier bounds alone do not deliver that property. 2d6 spans [2, 12],
// so three legal +3s (inside MaxModifiers) guarantee a full success — 2+9 is
// already 11 — and two legal -3s guarantee a miss at 12-6. The property that
// actually matters is REACHABILITY: with modifier sum s the possible totals
// are [2+s, 12+s], and all three bands stay reachable exactly when
//
//	2+s <= MaxMissTotal (6)  and  12+s >= MaxPartialTotal+1 (10)
//
// which is s ∈ [-2, +4]. Bounding the sum is therefore the real constraint;
// the per-modifier bound stays because it keeps any single declared
// justification proportionate and readable on the resolution card.
const (
	// MinModifierValue is the most negative a single modifier may be.
	MinModifierValue = -3
	// MaxModifierValue is the most positive a single modifier may be.
	MaxModifierValue = 3
	// MaxModifiers caps how many modifiers one verdict may declare.
	MaxModifiers = 4

	// MinModifierTotal is the most negative the summed modifiers may be
	// before a full success becomes unreachable.
	MinModifierTotal = -2
	// MaxModifierTotal is the most positive the summed modifiers may be
	// before a miss becomes unreachable.
	MaxModifierTotal = 4
)

// Modifier is one typed adjustment to a resolution roll. The source is a
// closed vocabulary value so the resolution card can explain the roll and so
// the adjudicator cannot invent justification categories. Note is free text
// and is rule-opaque: no rule or component may branch on it.
type Modifier struct {
	Source vocabulary.ModifierSource `json:"source"`
	Value  int                       `json:"value"`
	Note   string                    `json:"note,omitempty"`
}

// Validate checks source membership and the per-modifier bound.
func (m *Modifier) Validate() error {
	if _, err := vocabulary.ParseModifierSource(string(m.Source)); err != nil {
		return err
	}
	if m.Value < MinModifierValue || m.Value > MaxModifierValue {
		return fmt.Errorf("modifier %q value %d is outside [%d, %d]",
			m.Source, m.Value, MinModifierValue, MaxModifierValue)
	}
	return nil
}

// validateModifiers checks the list bound, every member, and the summed
// bound that keeps every band reachable.
func validateModifiers(modifiers []Modifier) error {
	if len(modifiers) > MaxModifiers {
		return fmt.Errorf("%d modifiers exceeds the cap of %d", len(modifiers), MaxModifiers)
	}
	for idx := range modifiers {
		if err := modifiers[idx].Validate(); err != nil {
			return fmt.Errorf("modifier %d: %w", idx, err)
		}
	}
	return checkModifierTotal(SumModifiers(modifiers))
}

// checkModifierTotal rejects a summed modifier that would put an outcome band
// out of reach of every possible 2d6 result.
func checkModifierTotal(total int) error {
	if total < MinModifierTotal || total > MaxModifierTotal {
		return fmt.Errorf(
			"summed modifiers %d are outside [%d, %d]; the dice would no longer be able to reach every band",
			total, MinModifierTotal, MaxModifierTotal)
	}
	return nil
}

// SumModifiers totals a modifier list. The dice component adds this to the
// raw dice; the roll-result validator recomputes it, so a recorded total can
// never silently disagree with its recorded modifiers.
func SumModifiers(modifiers []Modifier) int {
	total := 0
	for _, m := range modifiers {
		total += m.Value
	}
	return total
}
