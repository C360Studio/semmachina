package payload

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnState is the turn entity's own rule-matched record: the durable phase,
// the two entity references its birth establishes, and the closed reason it
// ended on when it ended badly.
//
// It is a payload rather than a bag of triples for one reason: the shared triple
// projection is where "only registered, closed, scalar facts reach the graph's
// rule-matching surface" is enforced, and the projection validates a payload
// before it projects anything. A phase write that skipped it would be a second,
// weaker write path into the exact surface the first one exists to protect —
// and the phase predicate is not an ordinary fact, it is the idempotency guard
// and the crash-diagnosis record the whole turn design rests on.
//
// Three record SHAPES, one type, because the shapes are three states of one
// fact rather than three different facts:
//
//   - accepted: phase + player + scene, plus the REQUIRED reference to the
//     stored action. Written once, by the atomic create that brings the turn
//     into existence.
//   - any other non-terminal or complete phase: phase alone.
//   - failed: phase + closed reason, in ONE write, optionally with a reference
//     to detail.
//
// The middle shape carries no reference at all, which is why the projection had
// to learn a reference-less mode: a phase transition has no bulky half, and
// inventing a reference so the gate would accept it would have made the gate a
// formality. The verdict's banded intents, the roll's dice, and the narration
// prose live behind their own references, written by the stages that produce
// them.
type TurnState struct {
	// TurnID is the turn this record describes. It is the instance segment of
	// the turn entity's six-part ID, which the projection checks.
	TurnID string `json:"turn_id"`
	// Phase is the durable phase being recorded.
	Phase vocabulary.TurnPhase `json:"phase"`

	// PlayerID and SceneID are the accepted record's entity references, and
	// they exist on the turn because nothing downstream can otherwise find
	// them: the action that carried them is a stream message that has been
	// acknowledged, and the world facts a stage needs are reached from the
	// scene. They are set on the accepted record and on no other, so a later
	// transition cannot rewrite who is playing or where.
	PlayerID string `json:"player_id,omitempty"`
	SceneID  string `json:"scene_id,omitempty"`

	// ActionRef references the stored player action, and it is REQUIRED on the
	// accepted record for the same reason PlayerID is: after the acknowledgment
	// there is nowhere else it lives.
	//
	// The text is fiction (M1) and far past the triple-object budget, so it can
	// only reach the graph as a pointer — and a turn born without one is a turn
	// the rule pack can re-trigger and the adjudicator cannot re-prompt. It
	// rides in the SAME write as the phase because a follow-up write would
	// reopen exactly the crash window the atomic create closes.
	ActionRef string `json:"action_ref,omitempty"`

	// Reason is the closed failure code, set on a failed record and on no
	// other. A code, never a sentence: this lands on rule-matching surface, and
	// the projection gates an object's SHAPE (scalar, bounded) but not its
	// CLOSURE, so an applier- or persona-authored explanation would pass every
	// gate and put free text where rules match.
	Reason vocabulary.FailureReason `json:"reason,omitempty"`

	// DetailRef optionally references stored detail about a failure — the batch
	// that was refused, the loop that exhausted its cap. It is what makes the
	// closed reason code survivable: without somewhere for "which intent?" to
	// live, the only place left for it is the reason itself.
	DetailRef string `json:"detail_ref,omitempty"`
}

// Schema implements message.Payload.
func (s *TurnState) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryTurnState, Version: SchemaVersion}
}

// Validate implements message.Payload.
//
// The field rules are stated as "set on exactly this shape and no other" in both
// directions, because a half-populated record is how a shape silently becomes a
// different shape: a phase transition carrying a stale player would rewrite the
// turn's player, and a failed record with no reason would be a failure nobody
// can explain.
func (s *TurnState) Validate() error {
	if err := requireIDSegment("turn_id", s.TurnID); err != nil {
		return err
	}
	if _, err := vocabulary.ParseTurnPhase(string(s.Phase)); err != nil {
		return err
	}

	if s.Phase == vocabulary.PhaseAccepted {
		if err := requireEntityID("player_id", s.PlayerID); err != nil {
			return err
		}
		if err := requireEntityID("scene_id", s.SceneID); err != nil {
			return err
		}
		if err := requireNonEmpty("action_ref", s.ActionRef); err != nil {
			return fmt.Errorf(
				"%w; the player's words are fiction and exceed the triple-object budget, so a turn born "+
					"without a pointer to them is one nothing can re-prompt", err)
		}
	} else {
		if s.PlayerID != "" || s.SceneID != "" {
			return fmt.Errorf(
				"phase %q carries a player or scene reference; those are established once, by the accepted "+
					"record, so a later transition that resent them could rewrite who is playing", s.Phase)
		}
		if s.ActionRef != "" {
			return fmt.Errorf(
				"phase %q carries an action reference; the action is stored once, before the turn exists, so "+
					"a later transition that resent it could repoint a turn at another turn's words", s.Phase)
		}
	}

	if s.Phase == vocabulary.PhaseFailed {
		if _, err := vocabulary.ParseFailureReason(string(s.Reason)); err != nil {
			return err
		}
	} else {
		if s.Reason != "" {
			return fmt.Errorf("phase %q carries failure reason %q; only a failed turn has a reason",
				s.Phase, s.Reason)
		}
		if s.DetailRef != "" {
			return fmt.Errorf(
				"phase %q carries a failure detail reference; %s explains a failure and a turn that did not "+
					"fail has none", s.Phase, vocabulary.TurnFailureRef)
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (s *TurnState) MarshalJSON() ([]byte, error) {
	type Alias TurnState
	return json.Marshal((*Alias)(s))
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *TurnState) UnmarshalJSON(data []byte) error {
	type Alias TurnState
	return json.Unmarshal(data, (*Alias)(s))
}

// Triples projects the record onto the turn entity.
//
// Every triple here MUST be committed on the entity merge lane
// (entity.update_with_triples) — or, for the birth record, ride inside the
// atomic entity create. The triple-add lanes append, so a duplicate stage
// trigger through them leaves a turn holding two phases, with a success
// response and no error — and the phase predicate stops being the idempotency
// guard the design rests on.
//
// The birth record's action reference is projected HERE rather than added
// afterwards, which is the whole point: the create is atomic, so a turn either
// exists carrying a pointer to the player's words or does not exist at all.
// A create-then-add-the-reference sequence would reopen the crash window
// between them, and the turn left behind would be one the rule pack can
// re-trigger and the adjudicator cannot re-prompt.
func (s *TurnState) Triples(turnEntityID, source string, at time.Time) ([]message.Triple, error) {
	projection := tripleProjection{
		payload: s,
		subject: turnEntityID,
		turnID:  s.TurnID,
		source:  source,
		at:      at,
	}

	switch s.Phase {
	case vocabulary.PhaseAccepted:
		projection.registered = vocabulary.TurnAcceptedPredicates()
		projection.objects = map[vocabulary.Predicate]any{
			vocabulary.TurnPhaseCurrent: string(s.Phase),
			vocabulary.TurnActionPlayer: s.PlayerID,
			vocabulary.TurnActionScene:  s.SceneID,
		}
		projection.refPredicate = vocabulary.TurnActionRef
		projection.refName = "action ref"
		projection.ref = s.ActionRef
	case vocabulary.PhaseFailed:
		projection.registered = vocabulary.TurnFailurePredicates()
		projection.objects = map[vocabulary.Predicate]any{
			vocabulary.TurnPhaseCurrent:  string(s.Phase),
			vocabulary.TurnFailureReason: string(s.Reason),
		}
		if s.DetailRef == "" {
			projection.refless = true
		} else {
			projection.refPredicate = vocabulary.TurnFailureRef
			projection.refName = "failure detail ref"
			projection.ref = s.DetailRef
		}
	default:
		projection.registered = vocabulary.TurnPhasePredicates()
		projection.objects = map[vocabulary.Predicate]any{
			vocabulary.TurnPhaseCurrent: string(s.Phase),
		}
		projection.refless = true
	}
	return projection.build()
}
