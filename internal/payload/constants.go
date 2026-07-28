package payload

import (
	"fmt"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Payload-registry coordinates. Domain, Category, and Version must match
// between each type's Schema() and its Registration, or the decoder
// reconstructs the wrong type (or nothing at all).
const (
	// Domain is the payload-registry domain for every SemMachina payload.
	Domain = "semmachina"
	// SchemaVersion is the current turn-loop payload schema version.
	SchemaVersion = "v1"

	// CategoryPlayerAction is the canonical transport-neutral player action.
	CategoryPlayerAction = "player_action"
	// CategoryVerdict is the fiction adjudicator's single structured exit.
	CategoryVerdict = "verdict"
	// CategoryRollResult is the dice component's resolution record.
	CategoryRollResult = "roll_result"
	// CategoryEffectBatch is the applier's validated, committed intent set.
	CategoryEffectBatch = "effect_batch"
	// CategoryTurnManifest is the append-only ledger record for one turn.
	CategoryTurnManifest = "turn_manifest"
	// CategoryWorldEntity is one materialized world entity from a template
	// package. It is the Graphable the importer publishes so graph-ingest
	// remains the sole ENTITY_STATES writer.
	CategoryWorldEntity = "world_entity"
)

// Bounds on the untrusted identifiers that become entity-ID segments.
//
// action_id is assigned by an ingress adapter from channel-native message
// identity — a Slack ts, an email Message-ID, an SMS id — so it is untrusted
// input, and TurnIDForAction turns it into turn_id, which becomes the
// INSTANCE SEGMENT of the turn entity's six-part ID. An unchecked action_id
// therefore reaches types.ValidateEntityID as structure: `a.b` composes a
// seven-part ID and a 300-byte one blows MaxEntityIDBytes.
//
// That failure would land after intake has committed to the message. Intake
// acks only once the turn entity exists (design D2), so a deterministic
// materialization failure is a poison message: the durable consumer redelivers
// it forever and the same composition fails identically every time. Bounding
// it here converts that loop into one rejected action at the boundary that
// owns the contract, which is also the boundary every future adapter
// normalizes into.
//
// The segment bound itself is vocabulary.MaxIDSegmentBytes — the payload
// boundary consumes the rule, it does not restate it.
const (
	// MaxActionIDBytes is tighter than vocabulary.MaxIDSegmentBytes by exactly
	// the prefix TurnIDForAction adds. Without this, an action_id could pass
	// here and its derived turn_id fail on the very next payload —
	// reintroducing the poison message one hop downstream.
	MaxActionIDBytes = vocabulary.MaxIDSegmentBytes - len(TurnIDPrefix)
)

// requireNonEmpty rejects a missing opaque identifier or storage reference.
func requireNonEmpty(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// requireIDSegment rejects an identifier that cannot survive composition into
// a canonical entity ID.
//
// The rule itself — non-empty, single segment, inside the upstream alphabet,
// inside the segment budget — is vocabulary.ValidateIDSegment's, and this
// wrapper adds exactly one thing: the name of the field that carried the bad
// value. Restating the rule here is what produced the divergence this
// delegation closes (the payload path bounded length, the world path did not,
// so an oversized local id survived every local check and failed at publish).
func requireIDSegment(field, value string) error {
	if err := vocabulary.ValidateIDSegment(value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

// requireActionID validates an action identifier as a segment AND validates
// the turn identifier derived from it, because the derived value is the one
// that actually becomes an entity-ID segment.
func requireActionID(field, value string) error {
	if err := requireIDSegment(field, value); err != nil {
		return err
	}
	if len(value) > MaxActionIDBytes {
		return fmt.Errorf(
			"%s is %d bytes; the derived turn id %q would exceed the %d-byte entity-ID segment budget",
			field, len(value), TurnIDForAction(value), vocabulary.MaxIDSegmentBytes)
	}
	return nil
}

// requireEntityID rejects anything that is not a canonical six-part entity
// ID. Player, campaign, scene, and every effect target are graph entities;
// accepting a connection ID or a bare name here is how identity quietly stops
// being durable.
func requireEntityID(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if err := types.ValidateEntityID(value); err != nil {
		return fmt.Errorf("%s %q is not a canonical six-part entity ID: %w", field, value, err)
	}
	return nil
}
