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
//   - accepted: phase + player + scene. Written once, by the atomic create that
//     brings the turn into existence.
//   - any other non-terminal or complete phase: phase alone.
//   - failed: phase + closed reason, in ONE write, optionally with a reference
//     to detail.
//
// None of the three carries a payload reference, which is why the projection
// had to learn a reference-less shape. A turn's state has no bulky half: it is
// four closed-vocabulary values and two entity IDs. The prose, the verdict's
// banded intents, and the roll's dice all live behind their own references,
// written by the stages that produce them.
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

	// Reason is the closed failure code, set on a failed record and on no
	// other. A code, never a sentence: this lands on rule-matching surface, and
	// the projection gates an object's SHAPE (scalar, bounded) but not its
	// CLOSURE, so an applier- or persona-authored explanation would pass every
	// gate and put free text where rules match.
	Reason vocabulary.FailureReason `json:"reason,omitempty"`
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
	} else if s.PlayerID != "" || s.SceneID != "" {
		return fmt.Errorf(
			"phase %q carries a player or scene reference; those are established once, by the accepted "+
				"record, so a later transition that resent them could rewrite who is playing", s.Phase)
	}

	if s.Phase == vocabulary.PhaseFailed {
		if _, err := vocabulary.ParseFailureReason(string(s.Reason)); err != nil {
			return err
		}
	} else if s.Reason != "" {
		return fmt.Errorf("phase %q carries failure reason %q; only a failed turn has a reason",
			s.Phase, s.Reason)
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
// detailRef is the optional reference to stored detail about a failure — the
// batch that was refused, the loop that exhausted its cap. It is the escape
// hatch that makes the closed reason code survivable: without somewhere for
// "which intent?" to live, the only place left for it is the reason itself.
// Empty is normal and produces a reference-less projection.
//
// Every triple here MUST be committed on the entity merge lane
// (entity.update_with_triples). The triple-add lanes append, so a duplicate
// stage trigger through them leaves a turn holding two phases, with a success
// response and no error — and the phase predicate stops being the idempotency
// guard the design rests on.
func (s *TurnState) Triples(turnEntityID, detailRef, source string, at time.Time) ([]message.Triple, error) {
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
	case vocabulary.PhaseFailed:
		projection.registered = vocabulary.TurnFailurePredicates()
		projection.objects = map[vocabulary.Predicate]any{
			vocabulary.TurnPhaseCurrent:  string(s.Phase),
			vocabulary.TurnFailureReason: string(s.Reason),
		}
	default:
		projection.registered = vocabulary.TurnPhasePredicates()
		projection.objects = map[vocabulary.Predicate]any{
			vocabulary.TurnPhaseCurrent: string(s.Phase),
		}
	}

	switch {
	case detailRef == "":
		projection.refless = true
	case s.Phase != vocabulary.PhaseFailed:
		return nil, fmt.Errorf(
			"phase %q carries a failure detail reference; %s explains a failure and a turn that did not "+
				"fail has none", s.Phase, vocabulary.TurnFailureRef)
	default:
		projection.refPredicate = vocabulary.TurnFailureRef
		projection.refName = "failure detail ref"
		projection.ref = detailRef
	}
	return projection.build()
}
