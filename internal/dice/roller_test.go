package dice_test

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testCampaignID = "c360.semmachina.world1.starter.campaign.main"
	testTurnEntity = "c360.semmachina.world1.starter.turn.turn-act-1"
	testTurnID     = "turn-act-1"
	otherTurnID    = "turn-act-2"
)

// seedOf builds a campaign seed of one repeated byte, so the independently
// computed digests below name their inputs exactly.
func seedOf(b byte) campaign.Seed {
	var seed campaign.Seed
	for idx := range seed {
		seed[idx] = b
	}
	return seed
}

func newRoller(t *testing.T) *dice.Roller {
	t.Helper()
	roller, err := dice.NewRoller(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("NewRoller: %v", err)
	}
	return roller
}

func mustRoll(t *testing.T, roller *dice.Roller, seed campaign.Seed, turnID string, modifiers ...payload.Modifier) *payload.RollResult {
	t.Helper()
	roll, err := roller.Roll(seed, testCampaignID, turnID, modifiers)
	if err != nil {
		t.Fatalf("Roll(%s): %v", turnID, err)
	}
	return roll
}

// The seed derivation is pinned against digests computed OUTSIDE Go — by
// shasum over the same bytes — so this test would catch the derivation being
// changed to something self-consistent but different (a different order, an
// extra separator, a truncated seed). A test that recomputed the digest with
// the same code it is checking would agree with any of those.
//
//	{ printf '\x01%.0s' $(seq 32); printf 'turn-act-1'; } | shasum -a 256
func TestSeedFor_MatchesIndependentlyComputedDigests(t *testing.T) {
	cases := []struct {
		name   string
		seed   campaign.Seed
		turnID string
		want   string
	}{
		{
			name:   "seed 0x01 x32 with turn-act-1",
			seed:   seedOf(0x01),
			turnID: testTurnID,
			want:   "8d12b0493142803097ffac580385fd716dd67a9e6fa13c1c242f7e66734cfea8",
		},
		{
			// Same campaign, different turn: the turn id must reach the hash.
			name:   "seed 0x01 x32 with turn-act-2",
			seed:   seedOf(0x01),
			turnID: otherTurnID,
			want:   "a47f31b83532e6eba62896938aa8ffa885f562cca3176cffb32ea9a540d82adf",
		},
		{
			// Same turn, different campaign: the seed must reach it too.
			name:   "seed 0x02 x32 with turn-act-1",
			seed:   seedOf(0x02),
			turnID: testTurnID,
			want:   "a3881e0e56c5642ea5ff4bb915b7da1b57146179bdebf3ffc1eb73245d79c91c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			digest, err := dice.SeedFor(tc.seed, tc.turnID)
			if err != nil {
				t.Fatalf("SeedFor: %v", err)
			}
			if got := hex.EncodeToString(digest[:]); got != tc.want {
				t.Fatalf("SeedFor = %s, want %s (computed outside Go over campaign_seed ‖ turn_id)", got, tc.want)
			}
		})
	}
}

// The zero seed is what a forgotten generation leaves behind. Rolling from it
// is perfectly deterministic and completely wrong, so it is refused rather than
// used.
func TestSeedFor_RefusesTheZeroSeedAndABadTurnID(t *testing.T) {
	if _, err := dice.SeedFor(campaign.Seed{}, testTurnID); err == nil {
		t.Fatal("the zero campaign seed was accepted")
	}
	if _, err := dice.SeedFor(seedOf(0x01), ""); err == nil {
		t.Fatal("an empty turn id was accepted")
	}
	if _, err := dice.SeedFor(seedOf(0x01), "turn.act.1"); err == nil {
		t.Fatal("a dotted turn id was accepted; it would compose the wrong entity id downstream")
	}
}

// The golden vector. It is the alarm for any change to the reduction from PCG
// output to die faces — swapping the explicit modulo for rand.Rand.IntN, or
// re-ordering the PCG seed words, would still produce "random" dice and would
// silently make every roll in every existing ledger unreproducible.
func TestRoll_GoldenVectorPinsTheWholeDerivation(t *testing.T) {
	roll := mustRoll(t, newRoller(t), seedOf(0x01), testTurnID)

	const wantDice = "[5 3]"
	if got := fmt.Sprint(roll.Dice); got != wantDice {
		t.Fatalf("dice = %s, want %s.\n"+
			"If this changed deliberately, every roll recorded under the old derivation stopped reproducing.",
			got, wantDice)
	}
	if roll.Total != 8 {
		t.Fatalf("total = %d, want 8", roll.Total)
	}
	if roll.Band != vocabulary.BandPartial {
		t.Fatalf("band = %q, want %q", roll.Band, vocabulary.BandPartial)
	}
}

// The golden vector alone cannot say WHY those dice are right. This does: it
// rebuilds the generator from the pinned digest, in the test, and reduces the
// draws the way the component documents — so the PCG seeding (first sixteen
// digest bytes, big-endian, low word first) and the modulo reduction are each
// pinned as a separate, inspectable step rather than as one opaque number.
func TestRoll_PCGSeedingAndReductionAreTheDocumentedComposition(t *testing.T) {
	const pinnedDigest = "8d12b0493142803097ffac580385fd716dd67a9e6fa13c1c242f7e66734cfea8"
	digest, err := hex.DecodeString(pinnedDigest)
	if err != nil {
		t.Fatalf("decode digest: %v", err)
	}

	generator := rand.NewPCG(
		binary.BigEndian.Uint64(digest[0:8]),
		binary.BigEndian.Uint64(digest[8:16]),
	)
	spec, err := vocabulary.MechanicSpecFor(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("MechanicSpecFor: %v", err)
	}
	want := make([]int, spec.Dice)
	for idx := range want {
		want[idx] = int(generator.Uint64()%uint64(spec.Faces)) + 1
	}

	roll := mustRoll(t, newRoller(t), seedOf(0x01), testTurnID)
	if fmt.Sprint(roll.Dice) != fmt.Sprint(want) {
		t.Fatalf("the component rolled %v; the documented composition over the pinned digest gives %v",
			roll.Dice, want)
	}
}

// Byte-identical re-execution, through the serialized form the ledger stores —
// not a field-by-field comparison, which would miss a field that stopped
// serializing.
func TestRoll_ReExecutesByteIdenticallyFromTheSameInputs(t *testing.T) {
	seed := seedOf(0x07)
	modifiers := []payload.Modifier{
		{Source: vocabulary.ModifierEquipment, Value: 1, Note: "crowbar"},
		{Source: vocabulary.ModifierCondition, Value: -1, Note: "exhausted"},
	}

	first, err := json.Marshal(mustRoll(t, newRoller(t), seed, testTurnID, modifiers...))
	if err != nil {
		t.Fatalf("marshal first roll: %v", err)
	}
	// A SECOND roller, built independently — replay happens in a different
	// process, so anything carried on the roller instance would break it.
	second, err := json.Marshal(mustRoll(t, newRoller(t), seed, testTurnID, modifiers...))
	if err != nil {
		t.Fatalf("marshal second roll: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("re-execution differs:\n first: %s\nsecond: %s", first, second)
	}
}

// The test a stateful roller fails. Seeding one generator at construction and
// drawing from it forever passes "roll twice, compare" when the two calls are
// adjacent, and fails here — because the second roll of turn A is drawn after
// an unrelated turn has consumed generator state.
//
// It is also the honest statement of the property replay needs: a turn's dice
// do not depend on the order turns were resolved in.
func TestRoll_IsIndependentOfTheOrderTurnsAreResolvedIn(t *testing.T) {
	roller := newRoller(t)
	seed := seedOf(0x11)

	first := mustRoll(t, roller, seed, testTurnID)

	// Interleave a run of other turns on the SAME roller.
	for idx := range 10 {
		mustRoll(t, roller, seed, fmt.Sprintf("turn-noise-%d", idx))
	}

	again := mustRoll(t, roller, seed, testTurnID)
	if fmt.Sprint(again.Dice) != fmt.Sprint(first.Dice) {
		t.Fatalf("turn %s rolled %v first and %v after ten intervening turns; "+
			"the roller is carrying generator state between rolls",
			testTurnID, first.Dice, again.Dice)
	}
}

// Distinct turns must roll independently, and "independently" has to be tested
// as a distribution rather than as one inequality: two turns differing is what
// a broken derivation produces half the time anyway.
func TestRoll_DistinctTurnsRollIndependently(t *testing.T) {
	roller := newRoller(t)
	seed := seedOf(0x23)

	const turns = 200
	digests := make(map[string]string, turns)
	counts := make(map[int]int, 11)

	for idx := range turns {
		turnID := fmt.Sprintf("turn-act-%d", idx)
		roll := mustRoll(t, roller, seed, turnID)

		digest, err := dice.SeedFor(seed, turnID)
		if err != nil {
			t.Fatalf("SeedFor: %v", err)
		}
		hexDigest := hex.EncodeToString(digest[:])
		if previous, clash := digests[hexDigest]; clash {
			t.Fatalf("turns %s and %s derive the same seed", previous, turnID)
		}
		digests[hexDigest] = turnID

		counts[roll.Total]++
	}

	if len(counts) < 6 {
		t.Fatalf("200 turns produced only %d distinct totals (%v); the dice are not varying with the turn id",
			len(counts), counts)
	}
	// 2d6 spans 2..12; a derivation that ignored one die, or reduced both from
	// the same draw, would collapse the range.
	if counts[2]+counts[12] == 0 {
		t.Fatalf("200 turns never produced an extreme total: %v", counts)
	}
	for total := range counts {
		if total < 2 || total > 12 {
			t.Fatalf("total %d is outside the 2d6 range", total)
		}
	}
}

// Same seed, same turn, DIFFERENT modifiers: the dice must not move. Modifiers
// shift the total, not the roll — otherwise a resolution card could never
// explain a roll, and a replay that reconstructed modifiers slightly
// differently would report different dice.
func TestRoll_ModifiersShiftTheTotalWithoutMovingTheDice(t *testing.T) {
	roller := newRoller(t)
	seed := seedOf(0x31)

	bare := mustRoll(t, roller, seed, testTurnID)
	modified := mustRoll(t, roller, seed, testTurnID,
		payload.Modifier{Source: vocabulary.ModifierTrait, Value: 2})

	if fmt.Sprint(modified.Dice) != fmt.Sprint(bare.Dice) {
		t.Fatalf("modifiers moved the dice: %v became %v", bare.Dice, modified.Dice)
	}
	if modified.Total != bare.Total+2 {
		t.Fatalf("total = %d, want %d", modified.Total, bare.Total+2)
	}
	if modified.ModifierTotal != 2 {
		t.Fatalf("modifier_total = %d, want 2", modified.ModifierTotal)
	}
}

// Every band must be reachable across turns; a roller that could only ever
// produce one band would satisfy every determinism test above.
func TestRoll_ReachesEveryBandAcrossTurns(t *testing.T) {
	roller := newRoller(t)
	seed := seedOf(0x41)

	seen := make(map[vocabulary.OutcomeBand]int)
	for idx := range 200 {
		roll := mustRoll(t, roller, seed, fmt.Sprintf("turn-band-%d", idx))
		seen[roll.Band]++
		if roll.Band == vocabulary.BandAuto {
			t.Fatal("a roll selected the auto band, which belongs to verdicts that skipped the dice")
		}
	}
	for _, band := range vocabulary.RollBands() {
		if seen[band] == 0 {
			t.Fatalf("band %q was never rolled in 200 turns: %v", band, seen)
		}
	}
}

// Every roll the component emits must satisfy the record contract, including
// the recomputed banding — the roller must not be able to produce something its
// own validator would reject.
func TestRoll_EmitsRecordsThatSatisfyTheirOwnContract(t *testing.T) {
	roller := newRoller(t)
	seed := seedOf(0x53)

	for idx := range 100 {
		roll := mustRoll(t, roller, seed, fmt.Sprintf("turn-contract-%d", idx))
		if err := roll.Validate(); err != nil {
			t.Fatalf("emitted roll %d fails its own contract: %v", idx, err)
		}
		if roll.Mechanic != vocabulary.Mechanic2d6PbtaV1 {
			t.Fatalf("roll records mechanic %q", roll.Mechanic)
		}
		if roll.RNGVersion != vocabulary.RNGPCGV1 {
			t.Fatalf("roll records rng %q", roll.RNGVersion)
		}
		if roll.Seed.CampaignID != testCampaignID || roll.Seed.TurnID != roll.TurnID {
			t.Fatalf("roll records seed source %+v", roll.Seed)
		}
	}
}

// The seed material itself must never be copied onto a record. The campaign
// seed lives in exactly one place; spreading it through every ledger entry
// would multiply the number of places replay's root can be corrupted.
func TestRoll_RecordsSeedDerivationInputsAndNeverTheSeedItself(t *testing.T) {
	seed := seedOf(0x67)
	roll := mustRoll(t, newRoller(t), seed, testTurnID)

	serialized, err := json.Marshal(roll)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(serialized), seed.String()) {
		t.Fatalf("the roll record carries the campaign seed: %s", serialized)
	}
	if strings.Contains(string(serialized), hex.EncodeToString(seed.Bytes()[:8])) {
		t.Fatalf("the roll record carries campaign seed material: %s", serialized)
	}
}

// Modifiers arrive from the verdict; the record must not alias the caller's
// slice, or a later mutation would rewrite history.
func TestRoll_DoesNotAliasTheCallersModifiers(t *testing.T) {
	modifiers := []payload.Modifier{{Source: vocabulary.ModifierTrait, Value: 2, Note: "sure-footed"}}
	roll := mustRoll(t, newRoller(t), seedOf(0x71), testTurnID, modifiers...)

	modifiers[0].Value = -3
	modifiers[0].Note = "rewritten"

	if roll.Modifiers[0].Value != 2 || roll.Modifiers[0].Note != "sure-footed" {
		t.Fatalf("mutating the caller's slice rewrote the recorded roll: %+v", roll.Modifiers[0])
	}
	if err := roll.Validate(); err != nil {
		t.Fatalf("the recorded roll stopped validating: %v", err)
	}
}

// The dice consume modifier bounds rather than re-deriving them: an
// out-of-contract modifier set produces no record at all.
func TestRoll_RefusesToEmitARecordWithOutOfContractModifiers(t *testing.T) {
	roller := newRoller(t)

	_, err := roller.Roll(seedOf(0x83), testCampaignID, testTurnID, []payload.Modifier{
		{Source: vocabulary.ModifierTrait, Value: payload.MaxModifierValue},
		{Source: vocabulary.ModifierEquipment, Value: payload.MaxModifierValue},
		{Source: vocabulary.ModifierPosition, Value: payload.MaxModifierValue},
	})
	if err == nil {
		t.Fatal("a modifier set that predetermines the band produced a roll record")
	}
	if !strings.Contains(err.Error(), "summed modifiers") {
		t.Fatalf("failure reason %q does not name the modifier bound", err.Error())
	}
}

func TestRoll_RefusesACampaignIDThatIsNotAnEntity(t *testing.T) {
	if _, err := newRoller(t).Roll(seedOf(0x91), "main", testTurnID, nil); err == nil {
		t.Fatal("a non-canonical campaign id produced a roll record")
	}
}

func TestNewRoller_RejectsAnUnregisteredMechanic(t *testing.T) {
	roller, err := dice.NewRoller("2d6-pbta/v2")
	if err == nil {
		t.Fatal("an unregistered mechanic produced a roller")
	}
	if roller != nil {
		t.Fatal("a refused constructor returned a roller")
	}
}

// The roller's numbers come from the registry, not from constants of its own.
func TestRoller_RollsTheRegisteredSpec(t *testing.T) {
	roller := newRoller(t)
	spec, err := vocabulary.MechanicSpecFor(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("MechanicSpecFor: %v", err)
	}
	if roller.Spec() != spec {
		t.Fatalf("roller spec %+v does not match the registry %+v", roller.Spec(), spec)
	}

	roll := mustRoll(t, roller, seedOf(0xA3), testTurnID)
	if len(roll.Dice) != spec.Dice {
		t.Fatalf("rolled %d dice, the spec declares %d", len(roll.Dice), spec.Dice)
	}
	for _, die := range roll.Dice {
		if !spec.ValidDie(die) {
			t.Fatalf("die %d is outside the spec's [1, %d]", die, spec.Faces)
		}
	}
}
