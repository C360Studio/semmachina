package turn

import (
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// RejectedActionError reports an action that cannot become a turn at all.
//
// It is a PERMANENT rejection, and the distinction matters more than it looks.
// Intake acknowledges only once the turn entity exists, so a deterministic
// failure to build one is a poison message: the durable consumer redelivers it
// forever and the same composition fails identically every time. A rejected
// action is terminated on its delivery instead, which converts an infinite
// redelivery loop into one refused action at the boundary that owns the
// contract.
//
// It carries no vocabulary.FailureReason, and that is not an oversight: a
// failure reason lands on a turn entity, and the whole content of this error is
// that there is no turn entity to land it on. An action whose identity cannot
// compose a six-part ID has nowhere for its own failure to be recorded.
type RejectedActionError struct {
	// ActionID is the offending action, when one could be read at all.
	ActionID string
	// Err is the specific violation.
	Err error
}

// Error implements error.
func (e *RejectedActionError) Error() string {
	if e.ActionID == "" {
		return fmt.Sprintf("player action rejected: %v", e.Err)
	}
	return fmt.Sprintf("player action %q rejected: %v", e.ActionID, e.Err)
}

// Unwrap exposes the underlying violation.
func (e *RejectedActionError) Unwrap() error { return e.Err }

// TransitionFault names WHICH wiring bug a refused transition is.
//
// It exists because four different bugs produce the same refusal, and one
// message for all four sends a reader to the wrong place: "that hop skips a
// stage" points at the rule pack's sequencing, which is exactly the wrong thing
// to go read when the real fault is a phase spelled wrong or a caller reaching
// for the create lane through Advance.
//
// It is an in-process answer and never reaches the graph — a turn records its
// phase, not the transitions it refused — so it is deliberately not
// vocabulary.
type TransitionFault string

// The four ways a transition is refused.
const (
	// FaultSkippedStage is the sequencing bug: both phases are in the closed
	// set and the hop jumps over a stage the turn has not run.
	FaultSkippedStage TransitionFault = "skipped-stage"
	// FaultUnknownPhase is a vocabulary bug: one of the two phases is outside
	// the closed set, so the FSM table has no edge to consult about it. This is
	// not a sequencing question and reporting it as one would send a reader to
	// read the rule pack for a typo.
	FaultUnknownPhase TransitionFault = "unknown-phase"
	// FaultCreatedNotAdvanced is a lane bug: the target is `accepted`, which is
	// written by the create and never transitioned into.
	FaultCreatedNotAdvanced TransitionFault = "created-not-advanced"
	// FaultFailureNeedsReason is a method bug: the target is `failed`, asked for
	// through Advance, which carries no failure reason.
	FaultFailureNeedsReason TransitionFault = "failure-needs-reason"
)

// IllegalTransitionError reports a phase write the turn FSM does not allow.
//
// It is deliberately distinct from a declined transition. A trigger arriving for
// a stage the turn has already left is ORDINARY — that is what at-least-once
// delivery produces, and the answer is to do nothing. A write that would skip a
// stage the turn has not reached, name a phase that does not exist, or reach a
// terminal phase through the wrong method is a WIRING BUG: nothing in the rule
// pack should ever ask a turn to go from accepted straight to narrating, and
// swallowing it would leave a turn that quietly never adjudicated.
//
// ONE type carries all four faults so that `errors.As` for the wiring-bug class
// catches the whole class. Two of them are refused before the turn's phase is
// even read — the target alone is illegal — so From is empty there, and that is
// a statement rather than a gap: where the turn currently is has no bearing on
// whether Advance may write those two phases at all.
type IllegalTransitionError struct {
	// TurnEntityID names the turn.
	TurnEntityID string
	// From is the phase the turn is recorded in, or empty when the target alone
	// is illegal.
	From vocabulary.TurnPhase
	// To is the phase the caller asked for.
	To vocabulary.TurnPhase
	// Fault names which of the four bugs this is.
	Fault TransitionFault
}

// Error implements error.
func (e *IllegalTransitionError) Error() string {
	switch e.Fault {
	case FaultCreatedNotAdvanced:
		return fmt.Sprintf(
			"turn %s cannot be advanced into %q: a turn is CREATED in that phase and never transitioned "+
				"into it, and a turn that could be moved back to accepted could be re-adjudicated after it "+
				"had already resolved", e.TurnEntityID, e.To)
	case FaultFailureNeedsReason:
		return fmt.Sprintf(
			"turn %s cannot be advanced into %q: use Fail, because a failed turn carries a closed reason "+
				"and a failure with no reason is the silent stall bounded execution exists to prevent",
			e.TurnEntityID, e.To)
	case FaultUnknownPhase:
		return fmt.Sprintf(
			"turn %s cannot move from %q to %q: one of those is not in the closed phase set, so the FSM "+
				"table has no edge to consult; this is a vocabulary bug, not a sequencing one",
			e.TurnEntityID, e.From, e.To)
	default:
		return fmt.Sprintf(
			"turn %s cannot move from %q to %q: that hop skips a stage, so the turn would advance without "+
				"having run one", e.TurnEntityID, e.From, e.To)
	}
}

// RecordError reports a turn entity whose own record cannot be trusted.
//
// It is UNCODED on purpose, and for the same reason the effect applier's
// anomalies are: recording a failure reason is a write to the very record this
// error says is unreadable. A turn holding two phase values, a turn holding
// none, a turn that is only a referential stub — each says the paperwork is
// corrupt, and marking it `failed` would state a game outcome on that evidence
// while possibly contradicting a world the turn already changed. The caller
// escalates; it does not burn the player's turn.
type RecordError struct {
	// TurnEntityID names the turn.
	TurnEntityID string
	// Err is the specific anomaly.
	Err error
}

// Error implements error.
func (e *RecordError) Error() string {
	return fmt.Sprintf("turn %s: %v", e.TurnEntityID, e.Err)
}

// Unwrap exposes the underlying anomaly.
func (e *RecordError) Unwrap() error { return e.Err }
