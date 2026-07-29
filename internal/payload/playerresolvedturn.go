package payload

import (
	"encoding/json"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// PlayerResolvedTurn is the pointer a player entity carries at the turn that
// most recently RESOLVED for them: the retrieval surface's whole durable index
// into "what happened last?".
//
// # Why it is not PlayerTurn with a different predicate
//
// Because the two pointers are written at different moments, by different calls,
// about different facts, and a projection carrying both would force each write to
// restate the other's. The accept-time write would have to name a resolved turn
// it knows nothing about; the resolution-time write would have to restate a
// current pointer it has no business moving — and moving it is precisely the
// failure that matters, because the current pointer is what the admission gate
// reads to decide whether a player may act. Two payloads, two predicate lists,
// one writer.
//
// # Why the pointer exists at all
//
// PlayerTurnCurrent names the most recently ACCEPTED turn, so it answers "what
// happened last?" only while that turn is terminal. The moment the player acts
// again it names a live turn and nothing else in the graph names the terminal one
// before it, leaving a history scan — O(campaign history), filtered by phase, on
// a request path — as the only alternative. See vocabulary.PlayerTurnResolved for
// the full argument, including why this is a FACT and never a flag.
//
// # It carries the same foreign-subject hazard
//
// Like PlayerTurn this projection does not land on the turn entity, so the
// pairing it needs — the subject is the player this pointer is about — is the
// projection's to check. A resolved pointer landed on a bystander does not merely
// mislabel a turn: it makes retrieval hand that player another player's
// narration, which is the disclosure defect targeted egress exists to prevent,
// arriving through the durable surface instead of the live one.
type PlayerResolvedTurn struct {
	// PlayerID is the player entity these facts land on. Stated on the payload as
	// well as passed to Triples so the two can be checked against each other.
	PlayerID string `json:"player_id"`

	// TurnID is the turn's instance segment, stamped as the correlation context
	// on the triple.
	TurnID string `json:"turn_id"`

	// TurnEntityID is the OBJECT: the six-part id of the turn that resolved. An
	// entity id rather than a bare turn id, because the reader's next act is to
	// read that entity and compose its result.
	TurnEntityID string `json:"turn_entity_id"`
}

// Schema implements message.Payload.
func (p *PlayerResolvedTurn) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryPlayerResolvedTurn, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (p *PlayerResolvedTurn) Validate() error {
	if err := requireEntityID("player_id", p.PlayerID); err != nil {
		return err
	}
	// One turn wearing two shapes; a pointer that disagreed with itself would
	// send retrieval to read a turn other than the one it reports.
	return RequireTurnEntityID(p.TurnID, p.TurnEntityID)
}

// Triples projects the pointer onto the PLAYER entity.
//
// It MUST be committed on the entity merge lane. The predicate is single-valued
// and a triple-add lane appends, so through one a player would accumulate one
// resolved-turn pointer per turn they have ever finished — with a success
// response and no error — and the retrieval surface would then be choosing among
// them rather than reading a fact.
func (p *PlayerResolvedTurn) Triples(playerEntityID, source string, at time.Time) ([]message.Triple, error) {
	return tripleProjection{
		payload:       p,
		subject:       playerEntityID,
		playerSubject: p.PlayerID,
		turnID:        p.TurnID,
		source:        source,
		at:            at,
		registered:    vocabulary.PlayerResolvedTurnPredicates(),
		objects: map[vocabulary.Predicate]any{
			vocabulary.PlayerTurnResolved: p.TurnEntityID,
		},
		// The turn entity id IS the whole fact; there is no bulky half behind it.
		refless: true,
	}.build()
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (p *PlayerResolvedTurn) MarshalJSON() ([]byte, error) {
	type Alias PlayerResolvedTurn
	return json.Marshal((*Alias)(p))
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *PlayerResolvedTurn) UnmarshalJSON(data []byte) error {
	type Alias PlayerResolvedTurn
	return json.Unmarshal(data, (*Alias)(p))
}
