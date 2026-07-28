package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestRollResult_ValidFixtureIsAccepted(t *testing.T) {
	if err := validRollResult().Validate(); err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}
}

// A roll record is the replay contract. If it can claim a band its own dice
// and modifiers do not produce, replay "reproduces" a different outcome and
// nobody notices — so the arithmetic and the banding are recomputed, never
// trusted.
func TestRollResult_RejectsRecordsThatContradictThemselves(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*payload.RollResult)
		wantErr string
	}{
		{
			name:    "band does not match the total",
			mutate:  func(r *payload.RollResult) { r.Band = vocabulary.BandFull },
			wantErr: "does not match total",
		},
		{
			name:    "total does not match dice plus modifiers",
			mutate:  func(r *payload.RollResult) { r.Total = 12 },
			wantErr: "total",
		},
		{
			name:    "modifier total does not match the modifiers",
			mutate:  func(r *payload.RollResult) { r.ModifierTotal = 3 },
			wantErr: "modifier_total",
		},
		{
			name:    "auto band on a roll record",
			mutate:  func(r *payload.RollResult) { r.Band = vocabulary.BandAuto },
			wantErr: "not selectable by a roll",
		},
		{
			name:    "band outside the vocabulary",
			mutate:  func(r *payload.RollResult) { r.Band = "critical" },
			wantErr: "outcome_band",
		},
		{
			name:    "wrong dice count for the mechanic",
			mutate:  func(r *payload.RollResult) { r.Dice = []int{4} },
			wantErr: "dice",
		},
		{
			name:    "die face above the maximum",
			mutate:  func(r *payload.RollResult) { r.Dice = []int{7, 2}; r.Total = 10; r.Band = vocabulary.BandFull },
			wantErr: "outside",
		},
		{
			name:    "die face below the minimum",
			mutate:  func(r *payload.RollResult) { r.Dice = []int{0, 8}; r.Total = 9 },
			wantErr: "outside",
		},
		{
			name:    "unregistered mechanic",
			mutate:  func(r *payload.RollResult) { r.Mechanic = "2d6-pbta/v2" },
			wantErr: "mechanic",
		},
		{
			name:    "unregistered rng",
			mutate:  func(r *payload.RollResult) { r.RNGVersion = "mt19937" },
			wantErr: "rng",
		},
		{
			name:    "seed turn does not match the roll's turn",
			mutate:  func(r *payload.RollResult) { r.Seed.TurnID = "turn-act-2" },
			wantErr: "seed.turn_id",
		},
		{
			name:    "seed campaign is not a canonical entity id",
			mutate:  func(r *payload.RollResult) { r.Seed.CampaignID = "main" },
			wantErr: "campaign_id",
		},
		{
			name:    "turn id missing",
			mutate:  func(r *payload.RollResult) { r.TurnID = ""; r.Seed.TurnID = "" },
			wantErr: "turn_id",
		},
		{
			name: "modifier outside the per-modifier cap",
			mutate: func(r *payload.RollResult) {
				r.Modifiers[0].Value = 20
				r.ModifierTotal = 20
				r.Total = 28
				r.Band = vocabulary.BandFull
			},
			wantErr: "outside",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roll := validRollResult()
			tc.mutate(roll)

			decoded := decode(t, publishUnvalidated(t, roll))
			err := decoded.Validate()
			if err == nil {
				t.Fatal("Validate accepted a self-contradictory roll record")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// The band boundaries are where a mis-typed comparison changes every outcome,
// so the payload contract is exercised across them with real dice pairs.
func TestRollResult_AcceptsEveryBandAtItsBoundary(t *testing.T) {
	cases := []struct {
		name          string
		dice          []int
		modifierValue int
		wantBand      vocabulary.OutcomeBand
	}{
		{name: "total 2 is a miss", dice: []int{1, 1}, wantBand: vocabulary.BandMiss},
		{name: "total 6 is a miss", dice: []int{3, 3}, wantBand: vocabulary.BandMiss},
		{name: "total 7 is a partial", dice: []int{3, 4}, wantBand: vocabulary.BandPartial},
		{name: "total 9 is a partial", dice: []int{4, 5}, wantBand: vocabulary.BandPartial},
		{name: "total 10 is a full", dice: []int{5, 5}, wantBand: vocabulary.BandFull},
		{name: "total 12 is a full", dice: []int{6, 6}, wantBand: vocabulary.BandFull},
		{name: "a negative modifier can drop a partial to a miss", dice: []int{4, 4}, modifierValue: -2, wantBand: vocabulary.BandMiss},
		{name: "a positive modifier can lift a partial to a full", dice: []int{4, 5}, modifierValue: 1, wantBand: vocabulary.BandFull},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roll := &payload.RollResult{
				TurnID:        testTurnID,
				Mechanic:      vocabulary.Mechanic2d6PbtaV1,
				RNGVersion:    vocabulary.RNGPCGV1,
				Seed:          payload.SeedSource{CampaignID: testCampaignID, TurnID: testTurnID},
				Dice:          tc.dice,
				ModifierTotal: tc.modifierValue,
				Total:         tc.dice[0] + tc.dice[1] + tc.modifierValue,
				Band:          tc.wantBand,
			}
			if tc.modifierValue != 0 {
				roll.Modifiers = []payload.Modifier{
					{Source: vocabulary.ModifierPosition, Value: tc.modifierValue},
				}
			}

			if err := roll.Validate(); err != nil {
				t.Fatalf("a consistent roll at the %q boundary was rejected: %v", tc.wantBand, err)
			}
			if got := vocabulary.BandForTotal(roll.Total); got != tc.wantBand {
				t.Fatalf("total %d bands as %q, expected %q", roll.Total, got, tc.wantBand)
			}
		})
	}
}

// The seed source records what the roll derived from, never the seed itself:
// the campaign seed lives on the campaign entity, and copying it into every
// ledger record would spread the one secret replay depends on.
func TestRollResult_SeedSourceRecordsInputsNotSeedMaterial(t *testing.T) {
	roll := validRollResult()
	decoded, ok := decode(t, publish(t, roll)).(*payload.RollResult)
	if !ok {
		t.Fatal("decoder produced the wrong type")
	}

	if decoded.Seed.CampaignID != testCampaignID {
		t.Fatalf("seed campaign = %q, want %q", decoded.Seed.CampaignID, testCampaignID)
	}
	if decoded.Seed.TurnID != decoded.TurnID {
		t.Fatalf("seed turn %q does not identify the roll's turn %q", decoded.Seed.TurnID, decoded.TurnID)
	}
}
