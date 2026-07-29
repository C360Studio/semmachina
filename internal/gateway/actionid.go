package gateway

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"hash"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/payload"
)

// actionIDDomain separates this derivation from every other use of SHA-256 in
// the engine, and VERSIONS it.
//
// The version is in the preimage rather than in the output because that is where
// it does work: changing the derivation changes every id it produces, so a v2
// gateway cannot mint an id that collides with a v1 one for a different
// (player, key) pair. A visible prefix would be prettier in a log and would
// guarantee nothing.
const actionIDDomain = "semmachina/action-id/v1\x00"

// actionIDEncoding is RFC 4648 base32 without padding.
//
// The alphabet matters: an entity-ID segment is [A-Za-z0-9][A-Za-z0-9_-]*, and
// base32's A-Z2-7 sits inside it with a first byte that is always alphanumeric,
// so no derived id can fail segment validation for its shape. Padding is dropped
// because `=` is not in that alphabet at all.
var actionIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ActionIDFor derives an action identity from the AUTHENTICATED player and the
// client's idempotency key.
//
// # Why the player id is in the preimage
//
// Because action_id is the engine's duplicate guard: turn and action are 1:1
// through payload.TurnIDForAction, and an action id that already has a turn is
// absorbed silently and acknowledged, which is exactly what makes redelivery
// safe. Handing a client any influence over which id it lands on turns that
// safety into an attack — a client that could determine another player's
// action_id could pre-claim it, and the victim's genuine action would then be
// taken for a duplicate of a turn they never had. There is no downstream check
// that could tell those two apart, which is why the defence has to be the
// derivation itself.
//
// Scoping to the authenticated player PARTITIONS the id space by player, which
// is a stronger property than secrecy and depends on none: the derivation is
// public and an attacker may know both inputs, and it still cannot help them,
// because their own submissions derive from their own player id and can only
// ever address turns inside their own partition. Two players choosing the same
// idempotency key address two different turns, and neither can address the
// other's.
//
// # Why the inputs are length-framed
//
// Because concatenation is ambiguous and the ambiguity IS a collision. Unframed,
// player "ab" with key "c" and player "a" with key "bc" hash the same preimage —
// two different players, one action id, and the pre-claim above reappears by
// arithmetic rather than by design. Same lesson as the campaign seed's fixed
// width, in the one other place this engine joins two identifiers.
//
// # Why the whole digest
//
// 256 bits encodes to 52 base32 characters, and the derived turn id
// (payload.TurnIDPrefix + this) has a 128-byte entity-ID segment budget to fit
// in. There is room, so there is no truncation to defend and no birthday
// argument to get wrong.
func ActionIDFor(playerID, idempotencyKey string) (string, error) {
	// Both inputs face their OWN full contract here rather than being assumed
	// from the caller, because this is the derivation of record: an input nobody
	// checked produces a preimage nobody can reproduce, and an id nobody can
	// reproduce silently stops being a duplicate guard. Submit has already
	// applied both, so this is a second application and not the only one — which
	// is the point, since ActionIDFor is exported and a caller that reached it
	// another way gets the same guarantees.
	if err := types.ValidateEntityID(playerID); err != nil {
		return "", fmt.Errorf(
			"deriving an action id requires the authenticated player's canonical entity id, got %q: %w",
			playerID, err)
	}
	if err := payload.RequireIdempotencyKey(idempotencyKey); err != nil {
		return "", fmt.Errorf("deriving an action id: %w", err)
	}

	digest := sha256.New()
	digest.Write([]byte(actionIDDomain))
	writeFramed(digest, playerID)
	writeFramed(digest, idempotencyKey)

	actionID := actionIDEncoding.EncodeToString(digest.Sum(nil))

	// The derivation is only worth anything if what it produces survives every
	// gate between here and the turn entity's instance segment. Checking it here
	// turns a change to the encoding into a failure at the boundary that owns the
	// contract, instead of a poison message intake redelivers forever.
	if err := payload.RequireActionID(actionID); err != nil {
		return "", fmt.Errorf("the derived action id is not usable as a turn identity: %w", err)
	}
	return actionID, nil
}

// writeFramed writes a length-prefixed value, so two values cannot be
// re-partitioned into the same byte sequence.
func writeFramed(digest hash.Hash, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	digest.Write(length[:])
	digest.Write([]byte(value))
}
