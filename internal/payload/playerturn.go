package payload

import (
	"encoding/json"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// PlayerTurn is the pointer a player entity carries at the turn they currently
// hold: the ingress admission gate's whole durable state.
//
// It is a payload rather than a hand-built triple for the reason TurnState is
// one: the shared projection is where "only registered, closed, scalar facts
// reach the graph's rule-matching surface" is enforced, and a second, weaker
// write path into that surface is how the discipline gets lost one predicate at
// a time. This one carries the extra hazard of a FOREIGN SUBJECT — it is the
// only projection in the engine that does not land on the turn entity — so the
// pairing it needs (subject is this player) is the projection's to check rather
// than this file's to remember.
//
// # What it is for
//
// One player holds one turn at a time. Enforcing that at ingress means asking,
// on every submission, "is this player's most recent turn still running?" — and
// the graph edge that exists runs the wrong way: TurnActionPlayer points turn →
// player, so the question is an incoming lookup returning the player's entire
// history, filtered by phase. This turns it into two reads: the player names the
// turn, and the turn states its own phase.
//
// # Why it names a turn instead of asserting activity
//
// Because a pointer never needs clearing and a flag does. See
// vocabulary.PlayerTurnCurrent — the argument lives on the predicate, where a
// future author looking at the graph will find it.
type PlayerTurn struct {
	// PlayerID is the player entity these facts land on. It is stated on the
	// payload as well as passed to Triples so the two can be checked against
	// each other; a caller that could only pass it would have nothing to
	// disagree with, and a pointer written onto the wrong player is a live turn
	// granted to a bystander and taken from its owner.
	PlayerID string `json:"player_id"`

	// TurnID is the turn's instance segment, stamped as the correlation context
	// on the triple.
	TurnID string `json:"turn_id"`

	// TurnEntityID is the OBJECT: the six-part id of the turn the player holds.
	// An entity id rather than a bare turn id, because the gateway's next act is
	// to read that entity, and a reader that had to recompose the id from
	// instance configuration would be a second place the composition rule is
	// stated.
	TurnEntityID string `json:"turn_entity_id"`
}

// Schema implements message.Payload.
func (p *PlayerTurn) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryPlayerTurn, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (p *PlayerTurn) Validate() error {
	if err := requireEntityID("player_id", p.PlayerID); err != nil {
		return err
	}
	// The turn id and the turn entity id are one turn wearing two shapes, and a
	// pointer that disagreed with itself would send the gateway to read a turn
	// other than the one it reports.
	return RequireTurnEntityID(p.TurnID, p.TurnEntityID)
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (p *PlayerTurn) MarshalJSON() ([]byte, error) {
	type Alias PlayerTurn
	return json.Marshal((*Alias)(p))
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *PlayerTurn) UnmarshalJSON(data []byte) error {
	type Alias PlayerTurn
	return json.Unmarshal(data, (*Alias)(p))
}

// Triples projects the pointer onto the PLAYER entity.
//
// playerEntityID is taken as an argument rather than read off the payload, for
// the reason TurnState.Triples takes the turn entity: the write's DESTINATION
// and the payload's CLAIM arrive from different places in the caller — one from
// the merge call it is about to issue, the other from the action it just
// accepted — and a projection that derived both from one field would have
// nothing to check. A pointer landed on the wrong player is a live turn granted
// to a bystander and silently taken from its owner, so the two are stated twice
// and required to agree.
//
// It MUST be committed on the entity merge lane. The predicate is single-valued
// and a triple-add lane appends, so through one the second turn a player takes
// leaves them pointing at two turns — with a success response and no error —
// and the gate that reads it is then reading a coin flip about whether they are
// free to act.
func (p *PlayerTurn) Triples(playerEntityID, source string, at time.Time) ([]message.Triple, error) {
	return tripleProjection{
		payload:       p,
		subject:       playerEntityID,
		playerSubject: p.PlayerID,
		turnID:        p.TurnID,
		source:        source,
		at:            at,
		registered:    vocabulary.PlayerTurnPredicates(),
		objects: map[vocabulary.Predicate]any{
			vocabulary.PlayerTurnCurrent: p.TurnEntityID,
		},
		// The turn entity id IS the whole fact; there is no bulky half behind it.
		refless: true,
	}.build()
}
