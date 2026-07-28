package payload

import (
	"encoding/json"
	"fmt"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// DiceCount is how many dice mechanic 2d6-pbta/v1 rolls.
const DiceCount = 2

// DieFaces is the number of faces on each die under 2d6-pbta/v1.
const DieFaces = 6

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
func (r *RollResult) Validate() error {
	if err := requireIDSegment("turn_id", r.TurnID); err != nil {
		return err
	}
	if _, err := vocabulary.ParseMechanic(string(r.Mechanic)); err != nil {
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

	if len(r.Dice) != DiceCount {
		return fmt.Errorf("mechanic %s rolls %d dice, got %d", r.Mechanic, DiceCount, len(r.Dice))
	}
	diceTotal := 0
	for idx, die := range r.Dice {
		if die < 1 || die > DieFaces {
			return fmt.Errorf("die %d value %d is outside [1, %d]", idx, die, DieFaces)
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
	if want := vocabulary.BandForTotal(r.Total); want != r.Band {
		return fmt.Errorf("band %q does not match total %d (expected %q)", r.Band, r.Total, want)
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
