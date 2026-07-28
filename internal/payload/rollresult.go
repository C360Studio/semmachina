package payload

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// SeedSource records what the per-roll seed was derived from, not the seed
// itself. The campaign seed is stored exactly once, on the campaign entity;
// recording the campaign ID plus the turn ID is sufficient to re-derive the
// roll byte-identically without copying secret material into every ledger
// record.
type SeedSource struct {
	// CampaignID is the entity carrying campaign.seed.value.
	CampaignID string `json:"campaign_id"`
	// TurnID is the second seed input, which is what makes two turns in one
	// campaign roll independently.
	TurnID string `json:"turn_id"`
}

// RollResult is the dice component's record of one resolution. It carries
// everything needed to re-execute the roll byte-identically: mechanic
// version, RNG version, seed inputs, dice, modifiers, total, and band.
//
// Both versions are recorded because replay under a different mechanic or a
// different generator would silently produce a different band from the same
// inputs — which would look like reproduction and would not be.
type RollResult struct {
	// TurnID is the turn this roll resolves. At most one roll exists per
	// turn.
	TurnID string `json:"turn_id"`

	// Mechanic is the versioned resolution mechanic.
	Mechanic vocabulary.Mechanic `json:"mechanic"`
	// RNGVersion is the versioned generator.
	RNGVersion vocabulary.RNG `json:"rng_version"`
	// Seed records the derivation inputs.
	Seed SeedSource `json:"seed"`

	// Dice are the raw die faces, in roll order.
	Dice []int `json:"dice"`
	// Modifiers are the verdict's typed modifiers, preserved so the
	// resolution card can explain the total.
	Modifiers []Modifier `json:"modifiers,omitempty"`
	// ModifierTotal is the summed modifiers.
	ModifierTotal int `json:"modifier_total"`
	// Total is dice plus ModifierTotal.
	Total int `json:"total"`
	// Band is the selected outcome band. Never auto: auto belongs to
	// verdicts that skipped the dice entirely.
	Band vocabulary.OutcomeBand `json:"band"`
}

// Schema implements message.Payload.
func (r *RollResult) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryRollResult, Version: SchemaVersion}
}

// Validate implements message.Payload. It recomputes the arithmetic and the
// banding rather than trusting them, so a roll record can never claim a band
// its own dice and modifiers do not produce.
//
// Every dice-shaped check reads the spec registered for the record's OWN
// mechanic. That is what stops a future `2d6-pbta/v2` record — different dice,
// different thresholds — from being validated, and silently re-banded, under
// v1's numbers.
func (r *RollResult) Validate() error {
	if err := requireIDSegment("turn_id", r.TurnID); err != nil {
		return err
	}
	spec, err := vocabulary.MechanicSpecFor(r.Mechanic)
	if err != nil {
		return err
	}
	if _, err := vocabulary.ParseRNG(string(r.RNGVersion)); err != nil {
		return err
	}
	if err := requireEntityID("seed.campaign_id", r.Seed.CampaignID); err != nil {
		return err
	}
	if r.Seed.TurnID != r.TurnID {
		return fmt.Errorf("seed.turn_id %q does not match turn_id %q", r.Seed.TurnID, r.TurnID)
	}

	if len(r.Dice) != spec.Dice {
		return fmt.Errorf("mechanic %s rolls %d dice, got %d", r.Mechanic, spec.Dice, len(r.Dice))
	}
	diceTotal := 0
	for idx, die := range r.Dice {
		if !spec.ValidDie(die) {
			return fmt.Errorf("die %d value %d is outside [1, %d]", idx, die, spec.Faces)
		}
		diceTotal += die
	}

	if err := validateModifiers(r.Modifiers); err != nil {
		return err
	}
	if sum := SumModifiers(r.Modifiers); sum != r.ModifierTotal {
		return fmt.Errorf("modifier_total %d does not match the summed modifiers %d", r.ModifierTotal, sum)
	}
	// ModifierTotal needs no separate bound check: validateModifiers bounds the
	// list's sum and the equality above pins the recorded field to that sum, so
	// an out-of-range total is unreachable here. A check would be untestable —
	// if ModifierTotal ever becomes authoritative over the list, bound it then.
	if want := diceTotal + r.ModifierTotal; want != r.Total {
		return fmt.Errorf("total %d does not match dice %d plus modifiers %d", r.Total, diceTotal, r.ModifierTotal)
	}

	if _, err := vocabulary.ParseOutcomeBand(string(r.Band)); err != nil {
		return err
	}
	if !r.Band.IsRollBand() {
		return fmt.Errorf("band %q is not selectable by a roll", r.Band)
	}
	if want := spec.BandForTotal(r.Total); want != r.Band {
		return fmt.Errorf("band %q does not match total %d under mechanic %s (expected %q)",
			r.Band, r.Total, r.Mechanic, want)
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (r *RollResult) MarshalJSON() ([]byte, error) {
	type Alias RollResult
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *RollResult) UnmarshalJSON(data []byte) error {
	type Alias RollResult
	return json.Unmarshal(data, (*Alias)(r))
}

// Triples returns the roll's rule-matched projection as triples on the turn
// entity, plus one reference to the stored roll-result payload.
//
// Band and total are what the rule pack routes and threshold-compares on. The
// mechanic version, RNG version, seed inputs, dice, and modifiers are recorded
// on the payload behind rollRef — everything replay needs, none of it rule
// surface. Same discipline as the verdict, enforced by the same projection
// rather than by two authors remembering the same rule.
//
// Writing these triples MUST use the graph's replace lane
// (entity.update_with_triples, which merges by subject+predicate), never
// triple.add / triple.add_batch, which append: band and total are single-valued
// and a duplicate roll trigger that appended would leave a turn with two bands
// and no error.
func (r *RollResult) Triples(turnEntityID, rollRef, source string, at time.Time) ([]message.Triple, error) {
	return tripleProjection{
		payload: r,
		subject: turnEntityID,
		turnID:  r.TurnID,
		source:  source,
		at:      at,

		registered: vocabulary.RollScalarPredicates(),
		objects: map[vocabulary.Predicate]any{
			vocabulary.TurnRollBand:  string(r.Band),
			vocabulary.TurnRollTotal: r.Total,
		},

		refPredicate: vocabulary.TurnRollRef,
		refName:      "roll ref",
		ref:          rollRef,
	}.build()
}
