package payload

import (
	"encoding/json"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnNarration is the narrator's whole mark on the graph: a pointer at the
// prose it wrote, and nothing else.
//
// It is a payload for the same reason TurnState is — the shared triple
// projection is where "only registered, closed, scalar facts reach the graph's
// rule-matching surface" is enforced, and a hand-built triple would be the one
// write in the engine that skipped both the storage-reference grammar and the
// turn-id/turn-entity pairing. On the one predicate whose object comes from a
// stage that just finished reading fiction, that is the last place to make an
// exception.
//
// It is a SEPARATE type from TurnState rather than a fourth shape of it because
// the two are written by different stages for different reasons: TurnState is
// the recorder's phase record and requires a phase, while this is the narrator's
// artifact reference and carries none. Folding them together would mean a
// narration write choosing a phase it is not making, which is precisely the
// coupling the turn's stage guards exist to keep apart.
//
// It carries NO rule-matched scalar, and the projection makes that an explicit
// claim rather than an inference (see vocabulary.NarrationScalarPredicates). The
// turn's structural facts were landed by the stages that decided them; a
// narrator restating one would put a second, softer copy of the world's state
// where rules match.
type TurnNarration struct {
	// TurnID is the turn this narration belongs to. It is the instance segment
	// of the turn entity's six-part ID, which the projection checks.
	TurnID string `json:"turn_id"`
	// NarrationRef references the stored prose. Required: this record exists
	// only to carry it, and the projection refuses anything that is not a
	// storage reference — a sentence handed to a *.ref predicate otherwise
	// passes every shape gate on the way to the graph.
	NarrationRef string `json:"narration_ref"`
}

// Schema implements message.Payload.
func (n *TurnNarration) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryTurnNarration, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (n *TurnNarration) Validate() error {
	if err := requireIDSegment("turn_id", n.TurnID); err != nil {
		return err
	}
	return requireNonEmpty("narration ref", n.NarrationRef)
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (n *TurnNarration) MarshalJSON() ([]byte, error) {
	type Alias TurnNarration
	return json.Marshal((*Alias)(n))
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *TurnNarration) UnmarshalJSON(data []byte) error {
	type Alias TurnNarration
	return json.Unmarshal(data, (*Alias)(n))
}

// Triples projects the narration reference onto the turn entity.
//
// Exactly one triple, committed on the entity merge lane. The triple-add lanes
// append, so a resumed narration through one would leave a turn holding two
// narration references — and the turn-closing rule would fire on the arrival of
// a second pointer to prose the player has already been shown.
func (n *TurnNarration) Triples(turnEntityID, source string, at time.Time) ([]message.Triple, error) {
	return tripleProjection{
		payload: n,
		subject: turnEntityID,
		turnID:  n.TurnID,
		source:  source,
		at:      at,

		registered: vocabulary.NarrationScalarPredicates(),
		scalarless: true,

		refPredicate: vocabulary.TurnNarrationRef,
		refName:      "narration ref",
		ref:          n.NarrationRef,
	}.build()
}
