package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxActionTextBytes bounds the player's free-text action.
//
// The text is the adjudicator's prompt input, so an unbounded action is
// unbounded spend, and cost is a policy rather than a forecast: a bound the
// contract states is a bound every adapter, prompt budget, and admission gate
// can reason about. Unbounded, the first thing that would stop a pasted novel
// is NATS's 1MB max payload — a transport failure, opaque and far from the
// boundary that owns the contract. Bytes rather than runes, because spend and
// the transport limit are both byte-shaped.
//
// 4 KiB is roughly 700 words: far more than a turn's declared action needs and
// far less than a prompt-flooding paste.
const MaxActionTextBytes = 4 * 1024

// ChannelBinding is delivery metadata, never identity. Player identity is
// PlayerAction.PlayerID, a graph entity that survives reconnects; the binding
// only tells the egress adapter where to send the result. No engine component
// downstream of the action stream may branch on it except that adapter.
type ChannelBinding struct {
	// Adapter names the transport this action arrived on.
	Adapter vocabulary.ChannelAdapter `json:"adapter"`
	// ReplyTo is the adapter-specific address the result is delivered to.
	ReplyTo string `json:"reply_to"`
}

// Validate checks adapter membership and a present reply address.
func (c ChannelBinding) Validate() error {
	if _, err := vocabulary.ParseChannelAdapter(string(c.Adapter)); err != nil {
		return err
	}
	return requireNonEmpty("channel.reply_to", c.ReplyTo)
}

// PlayerAction is the canonical transport-neutral player action: the engine's
// entire input contract. WebSocket is one adapter of N — Slack, email, and
// SMS ingress components normalize to exactly this shape, so adding a channel
// is a new component, never an engine change.
//
// A player action is a REQUEST, not a fact: it is published to JetStream and
// consumed by a durable consumer, so a restart resumes from the last ack and
// never replays the dragon eating you.
type PlayerAction struct {
	// ActionID is assigned by the ingress adapter, derived deterministically
	// from the channel-native message identity so a redelivered channel
	// message produces the same action, not a second turn.
	ActionID string `json:"action_id"`
	// PlayerID is a graph entity ID. Never a connection identifier.
	PlayerID string `json:"player_id"`
	// CampaignID is the campaign entity; it carries the campaign seed.
	CampaignID string `json:"campaign_id"`
	// SceneID is the scene entity the action is taken in.
	SceneID string `json:"scene_id"`

	// Text is the player's free-text action. Only personas may read it; it
	// travels downstream as an opaque stored reference.
	Text string `json:"text"`

	// ArrivedAt is the ingress timestamp. Deadline evaluation uses arrival
	// time, never processing time, so an action does not miss a deadline
	// because the engine was busy.
	ArrivedAt time.Time `json:"arrived_at"`

	// Channel is delivery metadata for the egress adapter.
	Channel ChannelBinding `json:"channel"`
}

// Schema implements message.Payload.
func (a *PlayerAction) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryPlayerAction, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (a *PlayerAction) Validate() error {
	if err := requireActionID("action_id", a.ActionID); err != nil {
		return err
	}
	if err := requireEntityID("player_id", a.PlayerID); err != nil {
		return err
	}
	if err := requireEntityID("campaign_id", a.CampaignID); err != nil {
		return err
	}
	if err := requireEntityID("scene_id", a.SceneID); err != nil {
		return err
	}
	if err := requireNonEmpty("text", a.Text); err != nil {
		return err
	}
	if len(a.Text) > MaxActionTextBytes {
		return fmt.Errorf("text is %d bytes, which exceeds the %d-byte action-text budget",
			len(a.Text), MaxActionTextBytes)
	}
	if a.ArrivedAt.IsZero() {
		return errors.New("arrived_at is required")
	}
	return a.Channel.Validate()
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (a *PlayerAction) MarshalJSON() ([]byte, error) {
	type Alias PlayerAction
	return json.Marshal((*Alias)(a))
}

// UnmarshalJSON implements json.Unmarshaler.
func (a *PlayerAction) UnmarshalJSON(data []byte) error {
	type Alias PlayerAction
	return json.Unmarshal(data, (*Alias)(a))
}

// TurnIDPrefix is what TurnIDForAction prepends. It is a constant rather than
// a literal because MaxActionIDBytes reserves exactly its length.
const TurnIDPrefix = "turn-"

// TurnIDForAction derives the turn identity from the action identity. Turn
// and action are 1:1, and the derivation is total and pure so a redelivered
// action always resolves to the same turn entity — which is what makes the
// duplicate-delivery guard work at all.
//
// The result becomes the instance segment of the turn entity's six-part ID,
// which is why action_id is validated as an entity-ID segment on the way in
// (requireActionID).
func TurnIDForAction(actionID string) string {
	if actionID == "" {
		return ""
	}
	return TurnIDPrefix + actionID
}
