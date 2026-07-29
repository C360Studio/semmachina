package content

import (
	"encoding/json"
	"fmt"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxProseBytes bounds one turn's narration.
//
// It is payload.MaxProseBytes, consumed rather than restated, for the reason
// RefScheme is: the bound has TWO enforcement points now — the stored artifact
// here, and the delivered document that carries the prose to a player — and a
// store that bounded prose by one number while the protocol bounded it by
// another would let a narration land durably and then be undeliverable. The
// argument for the number lives on the constant.
const MaxProseBytes = payload.MaxProseBytes

// Narration is one turn's narration prose as the narrator committed it.
//
// Like FailureDetail it is deliberately NOT a registered payload: no message of
// this type is ever published. It is written to the content store and referenced
// from the turn entity, and the only thing that reads it is a reader following
// that reference — the egress adapter delivering the turn's result, the ledger's
// replay path, and eventually the chronicler.
//
// Every field is either engine knowledge or prose. There is no third category on
// purpose: the narrator voices a committed outcome, so the structural facts of
// the turn were decided and recorded by the stages that decided them, and a
// narration restating one would be a second, softer copy of the world's state
// with nothing keeping the two in agreement.
type Narration struct {
	// TurnID is the turn this narration belongs to, so a stored artifact read
	// without its reference is still self-describing.
	TurnID string `json:"turn_id"`
	// Band is the outcome the narration voices, carried for the same reason:
	// a narration filed against the wrong band is detectable rather than merely
	// wrong. It is ENGINE knowledge — the dice chose it, or the verdict declined
	// them — and is never supplied by the narrator.
	Band vocabulary.OutcomeBand `json:"band"`
	// Prose is the narrator's own words. Fiction, and therefore rule-opaque by
	// construction: it lives here rather than on a triple, and no rule or
	// component may branch on it.
	Prose string `json:"prose"`
}

// Validate holds the narration to its contract on the way in and on the way
// out.
func (n *Narration) Validate() error {
	if err := vocabulary.ValidateIDSegment(n.TurnID); err != nil {
		return fmt.Errorf("turn_id: %w", err)
	}
	if _, err := vocabulary.ParseOutcomeBand(string(n.Band)); err != nil {
		return err
	}
	if n.Prose == "" {
		return fmt.Errorf(
			"narration for turn %s carries no prose; the turn would advertise a narration the player cannot "+
				"be shown", n.TurnID)
	}
	if len(n.Prose) > MaxProseBytes {
		return fmt.Errorf("narration prose is %d bytes, which exceeds the %d-byte prose budget",
			len(n.Prose), MaxProseBytes)
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (n *Narration) MarshalJSON() ([]byte, error) {
	type Alias Narration
	return json.Marshal((*Alias)(n))
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *Narration) UnmarshalJSON(data []byte) error {
	type Alias Narration
	return json.Unmarshal(data, (*Alias)(n))
}
