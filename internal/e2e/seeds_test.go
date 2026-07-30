package e2e_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// pinnedSeedHex is the campaign seed the three band scenarios are played under.
//
// Arbitrary and deliberate: it is not a secret (a campaign seed never is — an
// operator holding ENTITY_STATES can predict the dice, and instance-per-world
// makes the operator and the player the same person) and it is not random,
// because a random one cannot be paired with a turn id in advance.
const pinnedSeedHex = "5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e5e2e"

// bandFixture is one pinned (campaign_seed, turn_id) pair and the roll it must
// produce.
//
// # Why the pair is pinned rather than searched at runtime
//
// Because a runtime search proves nothing. A verdict declares intents for all
// three bands and the seeded dice select one (design D3/F19), so the band is not
// scriptable at the model boundary — the only way to choose it is to supply the
// inputs whose derived roll lands there. A test that searched for a key at run
// time would find a DIFFERENT key the moment the derivation changed and would go
// on passing, which is exactly the shape of a test that passes for the wrong
// reason. Pinned, a changed derivation is a failure.
//
// # What each field is doing
//
// The namespace is here because it is an INPUT: the turn id is derived from the
// player entity id and the client's idempotency key, and the player entity id
// carries the world namespace. Change the namespace and the pinned turn id is
// wrong. That is also why each band gets its own namespace rather than a shared
// one — isolation and pinning happen to want the same thing.
//
// Dice and Total are pinned alongside the band because the band is a three-way
// classification and would survive a derivation that moved a total from 4 to 6.
type bandFixture struct {
	// Band is the outcome this pair produces.
	Band vocabulary.OutcomeBand
	// WorldNS is the world namespace the pair is derived under.
	WorldNS string
	// Scenario is the scripted model pack this band is played with.
	Scenario string
	// Key is the client's idempotency key.
	Key string
	// TurnID is the turn id the engine must derive from (player, key).
	TurnID string
	// Dice and Total are the roll SeedFor + the mechanic must produce.
	Dice  []int
	Total int
}

// bandFixtures are the three pinned pairs, found by an exhaustive search over
// idempotency keys under pinnedSeedHex and recorded here.
//
// The verdicts the band scenarios script declare `equipment +1` and
// `condition -1`, so the modifier sum is ZERO and the dice alone decide. That is
// deliberate: a non-zero sum would make these constants depend on the fixture's
// modifier list as well as on the seed, and a fixture edit would then look like a
// determinism failure.
var bandFixtures = []bandFixture{
	{
		Band:     vocabulary.BandMiss,
		WorldNS:  "e2emiss",
		Scenario: "miss",
		Key:      "band-miss-002",
		TurnID:   "turn-N4TO6UNPKH2TIAALWJPUEU5LLBFKKOB5QDKQDTWBVOYO5W7BOPCA",
		Dice:     []int{2, 2},
		Total:    4,
	},
	{
		Band:     vocabulary.BandPartial,
		WorldNS:  "e2epartial",
		Scenario: "partial",
		Key:      "band-partial-009",
		TurnID:   "turn-TH22QSEEM4CTDSPCAFN52WBTBUCS6C72DNNCFI5MAB6J2QG7SBIA",
		Dice:     []int{2, 5},
		Total:    7,
	},
	{
		Band:     vocabulary.BandFull,
		WorldNS:  "e2efull",
		Scenario: "full",
		Key:      "band-full-006",
		TurnID:   "turn-BGOQ2I7KGZR2F47UGVXLMASFVH44M3IFDSJZ5LG7676OSDKLNJQA",
		Dice:     []int{5, 6},
		Total:    11,
	},
}

// bandModifiers is the modifier list the band scenarios' verdict declares. It is
// restated here because the pinned rolls were derived under it, and a fixture
// that changed it would change the totals below without changing the seed.
func bandModifiers() []payload.Modifier {
	return []payload.Modifier{
		{Source: vocabulary.ModifierEquipment, Value: 1, Note: "the crowbar bites on the winch housing"},
		{Source: vocabulary.ModifierCondition, Value: -1, Note: "Hollis is already watching him"},
	}
}

func pinnedSeed(t *testing.T) campaign.Seed {
	t.Helper()
	seed, err := campaign.ParseSeed(pinnedSeedHex)
	if err != nil {
		t.Fatalf("the pinned campaign seed does not parse: %v", err)
	}
	return seed
}

// The pinned pairs still produce their bands, checked against the production
// derivation with no broker in sight.
//
// This test IS the seeded-replay claim in its smallest form, and it is the reason
// the end-to-end band tests are allowed to assert a band at all. It walks the
// whole chain the engine walks — the authenticated player id and the client's key
// into an action id, the action id into a turn id, the campaign seed and turn id
// into a roll — so a change anywhere along it fails here, in a test that runs in
// milliseconds, rather than as three confusing end-to-end failures.
func TestPinnedBandFixtures_StillProduceTheirBands(t *testing.T) {
	seed := pinnedSeed(t)
	roller, err := dice.NewRoller(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("dice.NewRoller: %v", err)
	}
	if sum := payload.SumModifiers(bandModifiers()); sum != 0 {
		t.Fatalf("the band scenarios' modifiers sum to %d, not 0; the pinned totals were derived under a "+
			"zero sum, so every constant below is now describing a different roll", sum)
	}

	for _, fixture := range bandFixtures {
		t.Run(string(fixture.Band), func(t *testing.T) {
			playerID := "c360.semmachina." + fixture.WorldNS + "." + templateID + ".player." + playerLocalID
			actionID, err := gateway.ActionIDFor(playerID, fixture.Key)
			if err != nil {
				t.Fatalf("derive the action id: %v", err)
			}
			turnID := payload.TurnIDForAction(actionID)
			if turnID != fixture.TurnID {
				t.Fatalf("player %q with key %q now derives turn %q, and the pinned pair names %q; the "+
					"action-id derivation moved, so the pinned seeds no longer describe the turns the engine "+
					"will actually run", playerID, fixture.Key, turnID, fixture.TurnID)
			}

			campaignID := "c360.semmachina." + fixture.WorldNS + "." + templateID + ".campaign.main"
			roll, err := roller.Roll(seed, campaignID, turnID, bandModifiers())
			if err != nil {
				t.Fatalf("roll: %v", err)
			}
			if roll.Band != fixture.Band {
				t.Fatalf("the pinned pair for %q now rolls %v = %d, band %q. Seeded replay has changed: the "+
					"same campaign seed and turn id no longer produce the same dice, which breaks every "+
					"recorded roll in every archive",
					fixture.Band, roll.Dice, roll.Total, roll.Band)
			}
			if roll.Total != fixture.Total || len(roll.Dice) != len(fixture.Dice) {
				t.Fatalf("the pinned pair for %q rolls %v = %d, want %v = %d",
					fixture.Band, roll.Dice, roll.Total, fixture.Dice, fixture.Total)
			}
			for i, die := range fixture.Dice {
				if roll.Dice[i] != die {
					t.Fatalf("the pinned pair for %q rolls %v, want %v", fixture.Band, roll.Dice, fixture.Dice)
				}
			}
		})
	}
}

// Anti-vacuity for the table above: the three pairs must actually be three
// different bands, under three different namespaces, with three different keys.
//
// A table that had drifted into naming one band three times would satisfy every
// assertion in the test above and would prove a third of what it claims.
func TestPinnedBandFixtures_CoverEveryRollBandExactlyOnce(t *testing.T) {
	if !strings.EqualFold(pinnedSeedHex, pinnedSeedHex) {
		t.Fatal("unreachable")
	}
	seenBand := map[vocabulary.OutcomeBand]bool{}
	seenNS := map[string]bool{}
	seenKey := map[string]bool{}
	for _, fixture := range bandFixtures {
		if seenBand[fixture.Band] {
			t.Errorf("band %q is pinned twice", fixture.Band)
		}
		if seenNS[fixture.WorldNS] {
			t.Errorf("namespace %q is used twice; the two campaigns would share a world and a player",
				fixture.WorldNS)
		}
		if seenKey[fixture.Key] {
			t.Errorf("idempotency key %q is used twice", fixture.Key)
		}
		seenBand[fixture.Band] = true
		seenNS[fixture.WorldNS] = true
		seenKey[fixture.Key] = true
	}
	for _, band := range vocabulary.RollBands() {
		if !seenBand[band] {
			t.Errorf("no pinned pair produces the %q band, so no end-to-end run reaches those intents", band)
		}
	}
}
