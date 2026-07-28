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
//	MinDiceTotal+s <= MaxMissTotal  and  MaxDiceTotal+s > MaxPartialTotal
//
// which is s ∈ [-2, +4] for 2d6-pbta/v1's registered spec. Bounding the sum is
// therefore the real constraint; the per-modifier bound stays because it keeps
// any single declared justification proportionate and readable on the
// resolution card.
//
// These numbers are the ONE mechanic's numbers, worked out by hand and locked
// by a test that recomputes reachability from the registered
// vocabulary.MechanicSpec — so adding a second mechanic with different dice
// fails that test rather than silently inheriting 2d6's window.
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

// MaxModifierNoteBytes bounds a modifier's free-text justification.
//
// The note is LLM-authored and rule-opaque — no projection turns it into a
// triple, and no rule or component may branch on it. That bounds what it can
// INFLUENCE; it says nothing about how large it can be. The note rides on the
// Verdict and on every RollResult that preserves the verdict's modifiers, so
// unbounded it is unbounded bytes on two stored payloads per turn, and the
// first thing that would stop a runaway one is NATS's 1 MB max payload: a
// transport failure, opaque, after the tokens are already spent, and far from
// the boundary that owns the contract. Same argument as MaxActionTextBytes at
// the other end of the loop.
//
// 256 bytes is a clause — "already wounded from the fall" — which is all the
// resolution card can show beside a single ±3, and MaxModifiers caps how many
// of them one turn can carry. Bytes rather than runes, because the transport
// limit and the spend it stands in for are both byte-shaped.
const MaxModifierNoteBytes = 256

// Modifier is one typed adjustment to a resolution roll. The source is a
// closed vocabulary value so the resolution card can explain the roll and so
// the adjudicator cannot invent justification categories. Note is free text,
// rule-opaque (no rule or component may branch on it) and bounded by
// MaxModifierNoteBytes — the two are separate properties and it needs both.
type Modifier struct {
	Source vocabulary.ModifierSource `json:"source"`
	Value  int                       `json:"value"`
	Note   string                    `json:"note,omitempty"`
}

// Validate checks source membership, the per-modifier bound, and the note
// budget.
func (m *Modifier) Validate() error {
	if _, err := vocabulary.ParseModifierSource(string(m.Source)); err != nil {
		return err
	}
	if m.Value < MinModifierValue || m.Value > MaxModifierValue {
		return fmt.Errorf("modifier %q value %d is outside [%d, %d]",
			m.Source, m.Value, MinModifierValue, MaxModifierValue)
	}
	if len(m.Note) > MaxModifierNoteBytes {
		return fmt.Errorf("modifier %q note is %d bytes, which exceeds the %d-byte note budget",
			m.Source, len(m.Note), MaxModifierNoteBytes)
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
