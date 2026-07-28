package dice

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// RNGVersion is the generator this component rolls with, recorded on every
// roll result. It is versioned for the same reason the mechanic is: a record
// replayed under a different generator would produce different dice from the
// same seed, and the mismatch would look like reproduction.
const RNGVersion = vocabulary.RNGPCGV1

// Source names the dice component as the producer of the triples it projects.
const Source = "dice"

// SeedFor derives one roll's seed: SHA-256(campaign_seed ‖ turn_id).
//
// The campaign seed is FIXED WIDTH, which is what makes the plain concatenation
// unambiguous — there is exactly one way to split the input, so no two
// (campaign, turn) pairs can collide by re-partitioning the same bytes.
//
// Both inputs are recorded on the roll result (as the campaign ID and turn ID,
// never as the seed material itself), so re-deriving this digest from a ledger
// entry plus the campaign entity is all replay needs.
func SeedFor(campaignSeed campaign.Seed, turnID string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if campaignSeed.IsZero() {
		return digest, errors.New(
			"campaign seed is the zero value; rolling from it would be perfectly deterministic and completely wrong")
	}
	if err := vocabulary.ValidateIDSegment(turnID); err != nil {
		return digest, fmt.Errorf("turn id: %w", err)
	}

	hash := sha256.New()
	// Hash.Write never returns an error (documented on hash.Hash), so the
	// errcheck-shaped alternative would be noise around an impossible path.
	hash.Write(campaignSeed.Bytes()) //nolint:errcheck // hash.Hash never errors
	hash.Write([]byte(turnID))       //nolint:errcheck // hash.Hash never errors
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

// Roller rolls one registered mechanic.
type Roller struct {
	spec vocabulary.MechanicSpec
}

// NewRoller builds a roller for a registered mechanic.
//
// The mechanic is resolved through the registry rather than assumed, so dice
// count, faces, and band thresholds all come from the version being rolled —
// the property that stops a future v2's record from being produced or read
// under v1's numbers.
func NewRoller(mechanic vocabulary.Mechanic) (*Roller, error) {
	spec, err := vocabulary.MechanicSpecFor(mechanic)
	if err != nil {
		return nil, err
	}
	return &Roller{spec: spec}, nil
}

// Mechanic returns the version this roller rolls.
func (r *Roller) Mechanic() vocabulary.Mechanic { return r.spec.Mechanic }

// Spec returns the registered contract this roller rolls under.
func (r *Roller) Spec() vocabulary.MechanicSpec { return r.spec }

// Roll produces the resolution record for one turn.
//
// It is a pure function of its arguments. In particular the Roller carries NO
// generator state between calls: each roll builds its own PCG from its own
// derived seed, so rolling turn A, then turn B, then turn A again returns
// identical records for both A calls. A component that seeded one generator at
// construction and kept drawing from it would pass a naive "roll twice, compare"
// test and fail that one — and would make replay depend on the order turns
// happened to be resolved in.
func (r *Roller) Roll(
	campaignSeed campaign.Seed,
	campaignID string,
	turnID string,
	modifiers []payload.Modifier,
) (*payload.RollResult, error) {
	digest, err := SeedFor(campaignSeed, turnID)
	if err != nil {
		return nil, err
	}

	dice, diceTotal := r.roll(digest)
	modifierTotal := payload.SumModifiers(modifiers)
	total := diceTotal + modifierTotal

	recorded := make([]payload.Modifier, len(modifiers))
	copy(recorded, modifiers)

	roll := &payload.RollResult{
		TurnID:        turnID,
		Mechanic:      r.spec.Mechanic,
		RNGVersion:    RNGVersion,
		Seed:          payload.SeedSource{CampaignID: campaignID, TurnID: turnID},
		Dice:          dice,
		Modifiers:     recorded,
		ModifierTotal: modifierTotal,
		Total:         total,
		Band:          r.spec.BandForTotal(total),
	}
	// The roller validates its own output before returning it. A record the
	// engine's own validator would reject must never reach a ledger, and the
	// modifier bounds — which the dice consume rather than re-derive — are
	// enforced here as a refusal to emit, not as a second implementation.
	if err := roll.Validate(); err != nil {
		return nil, fmt.Errorf("dice produced a record that fails its own contract: %w", err)
	}
	return roll, nil
}

// roll draws the dice from a generator seeded with the derived digest.
//
// The 128-bit PCG state comes from the digest's first 16 bytes, big-endian; the
// remaining 16 are unused and are kept in the digest only because SHA-256
// produces them.
//
// Die faces are reduced from raw Uint64 draws with an explicit modulo rather
// than through rand.Rand.IntN. That is deliberate: PCG's output STREAM is a
// documented, stable algorithm, but the bias-elimination inside IntN is a
// standard-library implementation detail with no compatibility covenant — and
// replay determinism has to survive a Go upgrade. Reducing here means a
// recorded roll depends only on SHA-256, PCG, and this line.
//
// The cost is modulo bias: with a 64-bit draw and a 6-sided die, four residues
// are over-represented by one value out of 2^64/6 — about 2 parts in 10^19,
// which is not a fairness question any table will ever notice.
func (r *Roller) roll(digest [sha256.Size]byte) (dice []int, total int) {
	generator := rand.NewPCG(
		binary.BigEndian.Uint64(digest[0:8]),
		binary.BigEndian.Uint64(digest[8:16]),
	)

	dice = make([]int, r.spec.Dice)
	for idx := range dice {
		dice[idx] = int(generator.Uint64()%uint64(r.spec.Faces)) + 1
		total += dice[idx]
	}
	return dice, total
}
