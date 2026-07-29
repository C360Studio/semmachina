package payload

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnResume is the boot-time stranded-turn pass's mark on a turn: how many
// times it has re-triggered this turn's parked stage.
//
// It is a payload rather than a hand-built triple for the reason every other
// turn-entity write is: the shared projection is where "only registered, closed,
// bounded facts reach the graph's rule-matching surface" is enforced, and a
// second, weaker write path into that surface is how the discipline stops
// holding. That argument is stronger here than anywhere else in the loop,
// because the fact being written is a BOUND. A counter that lands on an
// appending lane leaves the turn holding two counts, both true, with a success
// response and no error — and "this turn has been rescued twice" stops being
// answerable, which makes the cap it exists to enforce unenforceable.
//
// It carries no phase, deliberately. The pass re-triggers a turn into the phase
// it is already parked in, so it has no transition to record, and a write that
// resent the phase would make a boot pass the second owner of a single-valued
// fact the turn recorder owns.
type TurnResume struct {
	// TurnID is the turn this count belongs to. It is the instance segment of
	// the turn entity's six-part ID, which the projection checks.
	TurnID string `json:"turn_id"`
	// Attempts is how many times the pass has re-triggered this turn, INCLUDING
	// the one being recorded. It is written before the trigger is published, so
	// a crash between the two spends an attempt rather than repeating one — the
	// only direction of error a bound may tolerate.
	Attempts int `json:"attempts"`
}

// Schema implements message.Payload.
func (r *TurnResume) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryTurnResume, Version: SchemaVersion}
}

// Validate implements message.Payload.
//
// A zero count is refused rather than accepted as "none yet". The absence of the
// predicate is what says a turn has never been rescued; a recorded zero would be
// the same claim written down, and two spellings of one fact is how a reader
// ends up treating "not yet counted" and "counted none" as different states.
func (r *TurnResume) Validate() error {
	if err := requireIDSegment("turn_id", r.TurnID); err != nil {
		return err
	}
	if r.Attempts < 1 {
		return fmt.Errorf(
			"turn %s records %d resume attempts; the predicate is written only when an attempt is being made, "+
				"so an absent count and a zero count would be two spellings of the same fact",
			r.TurnID, r.Attempts)
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (r *TurnResume) MarshalJSON() ([]byte, error) {
	type Alias TurnResume
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *TurnResume) UnmarshalJSON(data []byte) error {
	type Alias TurnResume
	return json.Unmarshal(data, (*Alias)(r))
}

// Triples projects the count onto the turn entity.
//
// The result MUST be committed on the entity merge lane
// (entity.update_with_triples). Through a triple-add lane it appends, and a
// counter that appends is not a counter.
func (r *TurnResume) Triples(turnEntityID, source string, at time.Time) ([]message.Triple, error) {
	projection := tripleProjection{
		payload:    r,
		subject:    turnEntityID,
		turnID:     r.TurnID,
		source:     source,
		at:         at,
		registered: vocabulary.TurnResumePredicates(),
		objects:    map[vocabulary.Predicate]any{vocabulary.TurnResumeAttempts: r.Attempts},
		// No bulky half: the count IS the record, and the reason the turn needed
		// rescuing is the phase it was parked in, which is already on the graph.
		refless: true,
	}
	return projection.build()
}
