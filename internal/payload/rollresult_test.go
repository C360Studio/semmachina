package payload_test

import (
	"encoding/json"
	"strings"
	"testing"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

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
			if got := mechanicSpec(t).BandForTotal(roll.Total); got != tc.wantBand {
				t.Fatalf("total %d bands as %q, expected %q", roll.Total, got, tc.wantBand)
			}
		})
	}
}

// The roll's F6 split: band and total are what rules route and threshold on;
// mechanic version, RNG version, seed inputs, dice, and modifiers are RECORDED
// on the payload behind the reference, because replay needs them and no rule
// does.
func TestRollResultTriples_EmitOnlyTheRuleMatchedScalarsPlusOneReference(t *testing.T) {
	roll := validRollResult()
	const rollRef = "obj://rolls/turn-act-1"

	triples, err := roll.Triples(testTurnEntity, rollRef, "dice", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	want := map[string]any{
		vocabulary.TurnRollBand.String():  string(roll.Band),
		vocabulary.TurnRollTotal.String(): roll.Total,
		vocabulary.TurnRollRef.String():   rollRef,
	}
	if len(triples) != len(want) {
		t.Fatalf("emitted %d triples, want exactly %d", len(triples), len(want))
	}

	seen := make(map[string]bool, len(triples))
	for _, triple := range triples {
		expected, registered := want[triple.Predicate]
		if !registered {
			t.Fatalf("roll emitted unexpected predicate %q; everything replay needs rides behind the reference",
				triple.Predicate)
		}
		if seen[triple.Predicate] {
			t.Fatalf("predicate %q emitted twice; single-valued predicates must replace, not accumulate", triple.Predicate)
		}
		seen[triple.Predicate] = true

		if triple.Object != expected {
			t.Fatalf("predicate %q object = %#v, want %#v", triple.Predicate, triple.Object, expected)
		}
		if triple.Subject != testTurnEntity {
			t.Fatalf("predicate %q lands on %q, want the turn entity %q", triple.Predicate, triple.Subject, testTurnEntity)
		}
		if triple.Context != roll.TurnID {
			t.Fatalf("predicate %q carries context %q, want the turn id %q", triple.Predicate, triple.Context, roll.TurnID)
		}
		if _, parseErr := ssvocab.ParsePredicate(triple.Predicate); parseErr != nil {
			t.Fatalf("the write gate would reject emitted predicate %q: %v", triple.Predicate, parseErr)
		}
	}
}

// The total must reach the graph as a number so the rule engine's numeric
// comparison operators can branch on it; the band must reach it as a string so
// eq/in can. A total emitted as "9" would still write, and every threshold rule
// over it would quietly never fire.
func TestRollResultTriples_TotalIsNumericAndBandIsAString(t *testing.T) {
	triples, err := validRollResult().Triples(testTurnEntity, "obj://rolls/x", "dice", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	for _, triple := range triples {
		switch triple.Predicate {
		case vocabulary.TurnRollTotal.String():
			if _, ok := triple.Object.(int); !ok {
				t.Fatalf("total object is %T, want int", triple.Object)
			}
		case vocabulary.TurnRollBand.String():
			if _, ok := triple.Object.(string); !ok {
				t.Fatalf("band object is %T, want string", triple.Object)
			}
		}
	}
}

// Rule-opaque free text must have no path into the graph's rule-matching
// surface, even from a payload small enough that carrying it would be cheap.
func TestRollResultTriples_CarryNoRuleOpaqueContent(t *testing.T) {
	roll := validRollResult()
	triples, err := roll.Triples(testTurnEntity, "obj://rolls/turn-act-1", "dice", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	serialized, err := json.Marshal(triples)
	if err != nil {
		t.Fatalf("marshal triples: %v", err)
	}
	note := roll.Modifiers[0].Note
	if note == "" {
		t.Fatal("the fixture has no modifier note; this check would be vacuous")
	}
	if strings.Contains(string(serialized), note) {
		t.Fatalf("roll triples leak the modifier note %q into the graph: %s", note, serialized)
	}
}

// A roll record that contradicts itself must produce no triples at all: a
// partial set is a half-written turn entity, and the projection has no error
// channel once the triples are in the caller's hands.
func TestRollResultTriples_RefuseToEmitAnInvalidRoll(t *testing.T) {
	roll := validRollResult()
	roll.Band = vocabulary.BandFull // its total of 9 is a partial

	triples, err := roll.Triples(testTurnEntity, "obj://rolls/x", "dice", testTime)
	if err == nil {
		t.Fatal("a roll whose band contradicts its total produced triples")
	}
	if triples != nil {
		t.Fatalf("a refused projection returned %d triples", len(triples))
	}
}

func TestRollResultTriples_RequireATurnEntityAndAReference(t *testing.T) {
	roll := validRollResult()

	if _, err := roll.Triples("not-an-entity-id", "obj://rolls/x", "dice", testTime); err == nil {
		t.Fatal("triples must not be emitted onto a non-canonical subject")
	}
	if _, err := roll.Triples(testTurnEntity, "", "dice", testTime); err == nil {
		t.Fatal("the recorded roll must always be reachable; an empty reference loses it")
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
