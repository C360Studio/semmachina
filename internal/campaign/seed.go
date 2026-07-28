package campaign

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// SeedBytes is the width of a campaign seed.
//
// The width is fixed, and that is load-bearing rather than aesthetic. Every
// per-roll seed is a hash of campaign_seed followed by turn_id; a
// variable-width prefix would make that concatenation ambiguous, so two
// different (seed, turn) pairs could hash to the same bytes and two different
// turns would roll identically. A fixed-width prefix removes the ambiguity by
// construction — the parser always knows where the seed ends.
//
// 32 bytes because it is the natural width beside SHA-256 and is far beyond any
// plausible need to enumerate campaign seeds.
const SeedBytes = 32

// Seed is the root of a campaign's replay determinism.
//
// It is generated ONCE, at world instantiation, and stored on the campaign
// entity. Every roll of every turn in the campaign's life derives from it plus
// the turn id, so it is the single value that makes the whole campaign
// reproducible — and the single value whose regeneration would silently
// invalidate every recorded roll in the ledger while every one of them still
// validated. Nothing in the engine may mint a second one for a campaign that
// has a first.
//
// It is not a secret. An operator holding ENTITY_STATES can predict the dice,
// and instance-per-world means the operator and the player are the same person.
// It is a REPLAY input, and it is treated with that weight instead.
type Seed [SeedBytes]byte

// NewSeed mints a campaign seed from the cryptographic random source.
//
// crypto/rand rather than a seeded PRNG: this is the one value in the engine
// that must NOT be reproducible, because everything reproducible hangs off it.
// A predictable campaign seed makes every campaign roll the same dice.
func NewSeed() (Seed, error) {
	var seed Seed
	if _, err := rand.Read(seed[:]); err != nil {
		return Seed{}, fmt.Errorf("generate campaign seed: %w", err)
	}
	if seed.IsZero() {
		// Unreachable in practice (2^-256); refused anyway, because the zero
		// seed is the value a FORGOTTEN generation produces and it must never
		// be mistaken for a real one.
		return Seed{}, errors.New("generated campaign seed is all zeroes")
	}
	return seed, nil
}

// ParseSeed reads a seed back from its stored hex form.
func ParseSeed(s string) (Seed, error) {
	if s == "" {
		return Seed{}, errors.New("campaign seed is empty")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return Seed{}, fmt.Errorf("campaign seed is not hex: %w", err)
	}
	if len(raw) != SeedBytes {
		return Seed{}, fmt.Errorf("campaign seed is %d bytes, want %d", len(raw), SeedBytes)
	}
	var seed Seed
	copy(seed[:], raw)
	if seed.IsZero() {
		return Seed{}, errors.New("campaign seed is all zeroes, which is the value an ungenerated seed has")
	}
	return seed, nil
}

// String returns the seed's hex form — how it is stored on the campaign entity.
func (s Seed) String() string { return hex.EncodeToString(s[:]) }

// Bytes returns a copy of the seed material. A copy, so a caller cannot mutate
// the seed a Gate is holding.
func (s Seed) Bytes() []byte {
	out := make([]byte, SeedBytes)
	copy(out, s[:])
	return out
}

// IsZero reports the never-generated seed. Callers that derive from a seed must
// refuse it: rolling from all zeroes is perfectly deterministic and completely
// wrong, which is the worst combination available.
func (s Seed) IsZero() bool { return s == Seed{} }
