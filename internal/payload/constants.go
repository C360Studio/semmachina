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
	// CategoryTurnResult is the canonical terminal turn result — the public
	// counterpart of CategoryPlayerAction. A player action is the canonical
	// thing that comes IN and a turn result is the canonical thing that goes
	// OUT, and both are registered for the same reason: an adapter is added by
	// writing a component that consumes the canonical message, never by changing
	// the engine.
	CategoryTurnResult = "turn_result"

	// CategorySubmitAction is the untrusted client's submission, and it is
	// deliberately NOT registered — for a different reason than the entity-only
	// categories below, which is why it is named apart from them.
	//
	// A SubmitAction never crosses NATS. It is decoded from client bytes at the
	// player-session gateway, which publishes a canonical PlayerAction and
	// discards it; the client is not a NATS participant and never sees a
	// BaseMessage envelope. A registered factory would advertise a decodable
	// message shape that nothing on the bus ever sends, and would invite a
	// component to consume the UNTRUSTED shape where it should only ever see the
	// gateway-stamped one. TestSubmitAction_IsDeliberatelyUnregistered keeps that
	// a decision rather than an omission.
	CategorySubmitAction = "submit_action"

	// CategoryTurnState is the turn entity's own state record, and like
	// CategoryCampaignEntity it is deliberately NOT registered: no TurnState
	// message is ever published. The turn entity is created through the atomic
	// mutation lane and advanced through the merge lane, so its state travels as
	// triples, never as a decodable wire type. Registering it would advertise a
	// message shape nothing sends and nothing reads.
	CategoryTurnState = "turn_state"

	// CategoryTurnResume is the stranded-turn pass's attempt-counter record, and
	// it is deliberately NOT registered for the reason CategoryTurnState is not:
	// no message of this type is ever published. It exists so a counter reaches
	// the graph through the shared triple projection rather than as a hand-built
	// triple that would skip the turn-entity pairing check and the shape gate —
	// and a counter is precisely the predicate where skipping the merge-lane
	// discipline turns a bound into a turn holding N counters.
	//
	// It is named beside the registered categories so the (domain, category,
	// version) namespace has one home; a bare literal in another package would
	// make a future collision invisible.
	CategoryTurnResume = "turn_resume"

	// CategoryTurnNarration is the narrator's artifact-reference record, and it
	// is deliberately NOT registered for exactly the reason CategoryTurnState is
	// not: no message of this type is ever published. It exists so the narration
	// reference reaches the graph through the shared triple projection rather
	// than as a hand-built triple that would skip the storage-reference grammar
	// and the turn-entity pairing.
	//
	// It is named here, beside the registered categories, so the (domain,
	// category, version) namespace has one home: as a bare literal in another
	// package a future collision would be invisible, because the registry cannot
	// report an overlap it never sees.
	CategoryTurnNarration = "turn_narration"

	// CategoryCampaignEntity is the campaign entity's provenance envelope, and
	// it is deliberately NOT registered: no message of this type is ever
	// published, because the campaign entity is created through the atomic
	// mutation lane rather than the fact lane. It is entity provenance, not a
	// decodable wire type.
	//
	// It lives beside the registered categories anyway, because the thing that
	// must not happen is a future registered payload claiming the same
	// coordinates. As a bare literal in another package that collision is
	// invisible — two different things answer to one (domain, category,
	// version), and the registry cannot report an overlap it never sees. Named
	// here, the namespace has one home and
	// TestCategoryCampaignEntity_IsDeliberatelyUnregistered keeps the
	// non-registration a decision rather than an omission.
	CategoryCampaignEntity = "campaign_entity"
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
