package rulepack

import (
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The stage-trigger stream's coordinates.
//
// A stage trigger is a REQUEST — "run the adjudicator for this turn" — and it
// goes on a JetStream stream rather than core NATS for the reason the intake's
// player actions do: exactly one handler should do the work, the work has real
// side effects (a stage trigger can cost a billed model call), and the handler
// must be able to resume from its last acknowledgment. Core NATS would make a
// trigger delivered while a stage runner was restarting simply vanish, and the
// turn would sit in its phase until the next process restart replayed the rule.
//
// Re-processing an accepted trigger is safe, which is the one answer that points
// the other way, and it is safe BY CONSTRUCTION rather than by luck: every stage
// enters its own phase through the recorder's guard, the dice re-derive rather
// than re-roll, the applier keys its batch on the turn, and the persona spawner
// consults the artifact guard before it pays for anything. At-least-once is what
// those guarantees were built for.
const (
	// StageStream is the durable home of stage triggers.
	StageStream = "TURN_STAGES"
	// StageSubjectPrefix is the subject space every stage trigger lives under.
	StageSubjectPrefix = "semmachina.turn."
	// StageSubjectFilter is the stream's subject filter.
	StageSubjectFilter = StageSubjectPrefix + ">"
)

// SubjectResolved is published when a turn REACHES a terminal phase.
//
// It is a notification about a fact rather than a command to a stage — nothing
// downstream of it changes the turn — and it is deliberately one subject for
// both endings: the campaign ledger records completed and failed turns alike
// (design D6), and so does the egress adapter that owes the player an answer
// either way. A consumer that cares which ending it was reads the turn's phase.
const SubjectResolved = StageSubjectPrefix + "resolved"

// stagePhases are the phases a stage runner ENTERS, in turn order.
//
// Two phases are deliberately absent. `accepted` is written by the turn
// entity's atomic create at intake, so nothing is ever triggered to enter it;
// `failed` is written by whichever stage gave up, from inside that stage, so a
// trigger to enter it would be a stage asking another component to record its
// own refusal.
var stagePhases = []vocabulary.TurnPhase{
	vocabulary.PhaseInterpreting,
	vocabulary.PhaseAdjudicating,
	vocabulary.PhaseResolving,
	vocabulary.PhaseApplying,
	vocabulary.PhaseNarrating,
	vocabulary.PhaseComplete,
}

// StagePhases returns the phases a stage runner is triggered to enter.
func StagePhases() []vocabulary.TurnPhase {
	out := make([]vocabulary.TurnPhase, len(stagePhases))
	copy(out, stagePhases)
	return out
}

// StageConsumerName returns the durable consumer one stage binds.
//
// DERIVED here rather than composed at the binding site, because two components
// now have to agree on it and they are not the same component. The stage runner
// binds it; the boot-time stranded-turn pass reads its acknowledgement floor to
// find out which triggers are still queued. A second copy of this string would
// make the pass read a consumer nobody binds, report every queued trigger as
// absent, and re-trigger a stage on top of work already coming to it.
func StageConsumerName(phase vocabulary.TurnPhase) string {
	return "semmachina-stage-" + string(phase)
}

// SubjectForPhase returns the trigger subject that drives a turn into a phase.
//
// It is DERIVED from the phase rather than tabulated, so the pack, the stream
// filter, and the consumer that binds to it cannot disagree about which subject
// belongs to which stage. A table would be a third place to state a pairing that
// the phase vocabulary already fixes.
func SubjectForPhase(phase vocabulary.TurnPhase) (string, error) {
	for _, known := range stagePhases {
		if known == phase {
			return StageSubjectPrefix + string(phase), nil
		}
	}
	return "", fmt.Errorf(
		"turn phase %q has no stage trigger subject (the triggered phases are %v); `accepted` is written by "+
			"intake's atomic create and `failed` by whichever stage gave up, so neither is entered on a trigger",
		phase, stagePhases)
}

// PhaseForSubject returns the phase a stage trigger drives a turn INTO.
//
// It is the inverse of SubjectForPhase and is derived by running it, so the two
// cannot disagree — the pairing stays stated once. The load-time FSM-edge check
// needs this direction: a rule declares the subject it publishes, and the edge it
// expresses is only legal if the phase behind that subject follows the phase the
// rule is gated on.
//
// The bool is false for any subject no stage enters, which deliberately includes
// SubjectResolved: that subject announces a turn that has ALREADY ended and
// drives nothing, so it is not an edge and has no legality to check.
func PhaseForSubject(subject string) (vocabulary.TurnPhase, bool) {
	for _, phase := range stagePhases {
		if got, err := SubjectForPhase(phase); err == nil && got == subject {
			return phase, true
		}
	}
	return "", false
}
