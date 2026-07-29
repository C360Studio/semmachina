package payload

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// DeliveredNarration is one turn's prose, resolved from the reference its result
// carries.
//
// It is a COPY of the stored artifact's readable half rather than the artifact
// type itself, and the split is deliberate on two counts. The stored artifact
// lives in internal/content, which consumes this package, so the protocol cannot
// name it without a cycle — and should not want to: what a client is shown and
// what an ObjectStore holds are allowed to diverge in a later version, and a
// protocol that aliased the storage type would make every storage change a
// protocol change.
//
// The two identity fields are carried, not dropped, and they are what makes a
// delivery checkable rather than merely assembled: a narration filed against
// another turn is the reference resolving to somebody else's fiction, and one
// filed against another band is a story about a turn that did not happen.
type DeliveredNarration struct {
	// TurnID is the turn this prose belongs to, as the stored artifact records
	// it. Checked against the result's turn — never assumed to agree.
	TurnID string `json:"turn_id"`
	// Band is the outcome the prose voices. ENGINE knowledge: the dice chose it
	// or the verdict declined them, and the narrator is told it rather than
	// asked for it.
	Band vocabulary.OutcomeBand `json:"band"`
	// Prose is the narrator's own words. Fiction, and therefore rule-opaque: it
	// travels to a player and to nothing else.
	Prose string `json:"prose"`
}

// Validate holds delivered prose to the same contract the stored artifact meets.
func (n *DeliveredNarration) Validate() error {
	if err := requireIDSegment("turn_id", n.TurnID); err != nil {
		return err
	}
	if _, err := vocabulary.ParseOutcomeBand(string(n.Band)); err != nil {
		return err
	}
	if n.Prose == "" {
		return fmt.Errorf(
			"the narration delivered for turn %s carries no prose; a delivery that resolved a reference to "+
				"nothing would show the player an empty story rather than saying the prose is missing", n.TurnID)
	}
	if len(n.Prose) > MaxProseBytes {
		return fmt.Errorf("delivered prose is %d bytes, which exceeds the %d-byte prose budget",
			len(n.Prose), MaxProseBytes)
	}
	return nil
}

// TurnDelivery is what a player actually receives: the canonical TurnResult,
// unmodified, plus the prose that result only REFERENCES.
//
// # Why an envelope rather than a fatter result
//
// Because the alternative is two documents wearing one name. TurnResult carries
// narration_ref — a storage reference — and no client can resolve an `obj://`,
// so something has to dereference it. Adding the prose to TurnResult at delivery
// would mean the document a client receives is not the document the engine
// composed, published or archived, which is exactly the failure TurnResult was
// made a single discriminated type to avoid. Embedding it whole keeps one
// canonical result with one shape everywhere, and makes the difference between
// "the record" and "the record plus what it points at" visible in the type.
//
// # Why the SERVER dereferences, rather than the client fetching
//
// A fetch-back protocol — deliver the reference, let the client ask for the prose
// — is unavailable to this engine by an invariant rather than by preference: no
// turn-processing step may assume interactive pacing, and player I/O is
// adapter-shaped. An email or SMS adapter has no second round trip to make. A
// protocol whose result is only complete after the client asks again is a
// protocol only an interactive adapter can speak, which would make the WebSocket
// the reference implementation instead of one adapter of N.
//
// # What this costs, stated rather than assumed
//
// One delivery carries one turn's prose, bounded by MaxProseBytes (16 KiB), and
// nothing else bulky: the verdict's banded intents, the effect batch and the
// stored action stay behind their references and never travel. Crucially it is
// not CUMULATIVE — a delivery carries this turn's prose and no previous turn's —
// so the per-turn wire cost is flat, which is the same claim the context budget
// makes about tokens and the thing "append everything" would break.
//
// # A failed turn's prose is optional, and that is the point
//
// A turn that died before the narrator ran has no prose; one abandoned after its
// prose landed has some. Narration is present exactly when the result carries a
// reference, so both are deliverable and neither is guessed at.
type TurnDelivery struct {
	// Protocol is the version of the public player protocol this delivery is
	// written against.
	//
	// It is stated on the envelope as well as on the embedded result because a
	// client reads the envelope first and must be able to decide whether it can
	// speak this document at all before decoding what is inside it. The two are
	// NOT checked against each other, and that is a decision rather than an
	// omission — see Validate.
	Protocol PlayerProtocolVersion `json:"protocol"`

	// Result is the canonical terminal result, embedded verbatim.
	Result *TurnResult `json:"result"`

	// Narration is the prose behind Result.NarrationRef, present exactly when
	// that reference is.
	Narration *DeliveredNarration `json:"narration,omitempty"`
}

// Validate implements the delivery's contract.
//
// The identity checks between the envelope's two halves are the load-bearing
// part. Everything else is each half holding to its own contract; these are the
// checks that only exist because the two were assembled from different reads —
// the turn entity and the ObjectStore — and a mismatch between them is a player
// being shown the wrong turn's fiction.
//
// # There is deliberately no envelope-vs-result VERSION check
//
// One was written, and it survived mutation, which is how it was found. Both
// versions must parse, and exactly one version parses, so no input exists in
// which the two differ and both are valid: the check could never fire, and a
// check nothing can reach reads as coverage and is not. It becomes real the day a
// v2 exists — a v1 result inside a v2 envelope would be two promises in one
// message — and that is the change that should add it back, with the test that
// can finally construct the state.
func (d *TurnDelivery) Validate() error {
	if _, err := ParsePlayerProtocolVersion(string(d.Protocol)); err != nil {
		return err
	}
	if d.Result == nil {
		return errors.New(
			"a delivery carries no result; the prose is what the result POINTS at, so a delivery without one " +
				"is fiction with nothing to say which turn it belongs to")
	}
	if err := d.Result.Validate(); err != nil {
		return fmt.Errorf("result: %w", err)
	}

	if d.Narration == nil {
		if d.Result.NarrationRef != "" {
			return fmt.Errorf(
				"turn %s records narration at %s and the delivery carries none; no client can resolve an %s "+
					"reference, so this is a result the player cannot read",
				d.Result.TurnID, d.Result.NarrationRef, RefScheme)
		}
		return nil
	}
	if d.Result.NarrationRef == "" {
		return fmt.Errorf(
			"the delivery for turn %s carries prose, but the result references none; the prose came from "+
				"somewhere other than this turn's own record", d.Result.TurnID)
	}
	if err := d.Narration.Validate(); err != nil {
		return fmt.Errorf("narration: %w", err)
	}
	if d.Narration.TurnID != d.Result.TurnID {
		return fmt.Errorf(
			"the delivery for turn %s carries prose belonging to turn %s; a reference that resolves to another "+
				"turn's fiction puts it in front of whoever this result was addressed to",
			d.Result.TurnID, d.Narration.TurnID)
	}
	// Only when the turn recorded a band at all. A turn that ended before an
	// outcome was selected has none to disagree with, and manufacturing one here
	// would refuse a legitimate failed turn.
	if d.Result.Resolution != nil && d.Result.Resolution.Band != "" &&
		d.Narration.Band != d.Result.Resolution.Band {
		return fmt.Errorf(
			"the delivery for turn %s carries prose voicing the %q band while the turn landed on %q; a "+
				"narration filed against a band the turn did not reach is a story about a turn that did not happen",
			d.Result.TurnID, d.Narration.Band, d.Result.Resolution.Band)
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the embedded result marshals through its own marshaler, which is what keeps
// the delivered bytes identical to the published ones.
func (d *TurnDelivery) MarshalJSON() ([]byte, error) {
	type Alias TurnDelivery
	return json.Marshal((*Alias)(d))
}

// UnmarshalJSON implements json.Unmarshaler.
//
// Permissive about fields it does not know, for the reason TurnResult's
// unmarshaler is: this is a RESULT direction document, and the protocol's stated
// asymmetry lets a later version add fields an older adapter must keep working
// through.
func (d *TurnDelivery) UnmarshalJSON(data []byte) error {
	type Alias TurnDelivery
	return json.Unmarshal(data, (*Alias)(d))
}
