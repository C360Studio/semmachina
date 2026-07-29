package content

import (
	"encoding/json"
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxProseBytes bounds one turn's narration.
//
// The prose lives in ObjectStore rather than on a triple, so the triple-object
// budget does not apply and the bound is doing a different job: it is the
// narrator's half of the same argument every other LLM-authored field here
// makes. Unbounded, the first thing that would stop a runaway generation is
// NATS's 1 MB max payload — a transport failure, opaque, after the tokens are
// already spent — and the object it would leave behind is read back by the
// egress adapter, the ledger reader, and the chronicler.
//
// 16 KiB is roughly 2,700 words: an order of magnitude past the "two to four
// sentences for an ordinary turn" the starter world's narrator is told to
// write, and an order of magnitude short of a chapter. A narrator that needs
// more than this is not narrating a turn.
const MaxProseBytes = 16 * 1024

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
