package vocabulary_test

import (
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The FSM table is the turn's whole correctness story compressed into a map, so
// what it is tested for is the properties a phase writer relies on — not the
// literal contents, which a test that restated them would only prove had been
// copied twice.

func TestPhaseRank_CoversEveryPhaseInTheClosedSet(t *testing.T) {
	for _, phase := range vocabulary.TurnPhases() {
		if _, ok := vocabulary.PhaseRank(phase); !ok {
			t.Fatalf("phase %q is in the closed set but has no rank; a guard comparing it would "+
				"read zero and treat a finished turn as brand new", phase)
		}
	}
	if _, ok := vocabulary.PhaseRank(vocabulary.TurnPhase("adjudicatin")); ok {
		t.Fatal("a value outside the closed phase set was given a rank")
	}
}

// Rank has exactly one job: separate "you are late" from "you skipped a stage".
// It can only do it if the forward path is strictly increasing.
func TestPhaseRank_IncreasesAlongEveryLegalForwardTransition(t *testing.T) {
	for _, to := range vocabulary.TurnPhases() {
		if to == vocabulary.PhaseFailed {
			// Failure is reachable from every non-terminal phase, so it is
			// deliberately not ordered against them.
			continue
		}
		toRank, _ := vocabulary.PhaseRank(to)
		predecessors, ok := vocabulary.PhasePredecessors(to)
		if !ok {
			t.Fatalf("no predecessor set for %q", to)
		}
		for _, from := range predecessors {
			fromRank, _ := vocabulary.PhaseRank(from)
			if fromRank >= toRank {
				t.Fatalf("%q (rank %d) is a legal predecessor of %q (rank %d); a guard that used rank "+
					"to detect a stale trigger would decline a legitimate advance", from, fromRank, to, toRank)
			}
		}
	}
}

func TestPhasePredecessors_AcceptedIsCreatedAndNeverTransitionedInto(t *testing.T) {
	predecessors, ok := vocabulary.PhasePredecessors(vocabulary.PhaseAccepted)
	if !ok {
		t.Fatal("accepted has no predecessor entry at all")
	}
	if len(predecessors) != 0 {
		t.Fatalf("accepted is reachable from %v; a turn that can be moved back to accepted can be "+
			"re-adjudicated after it already resolved", predecessors)
	}
}

// The no-roll path is a design decision (D3) that lives in this table and
// nowhere else: a verdict that declines the dice goes straight from adjudicating
// to applying. Without this edge the applier could never be reached for an auto
// band and every no-roll turn would wedge.
func TestPhasePredecessors_ApplyingIsReachableWithoutARoll(t *testing.T) {
	if !vocabulary.PhaseFollows(vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying) {
		t.Fatal("a no-roll verdict cannot reach the applier: adjudicating is not a predecessor of applying")
	}
	if !vocabulary.PhaseFollows(vocabulary.PhaseResolving, vocabulary.PhaseApplying) {
		t.Fatal("a rolled turn cannot reach the applier: resolving is not a predecessor of applying")
	}
}

// Bounded execution is only a guarantee if every stage can end the turn
// explicitly. A phase with no path to failed is a phase that can only stall.
func TestPhasePredecessors_EveryNonTerminalPhaseCanFail(t *testing.T) {
	for _, phase := range vocabulary.TurnPhases() {
		if phase.IsTerminal() {
			continue
		}
		if !vocabulary.PhaseFollows(phase, vocabulary.PhaseFailed) {
			t.Fatalf("phase %q has no transition to failed; cap exhaustion or a rejection there "+
				"would be a silent stall", phase)
		}
	}
}

// The mirror of the accepted rule, and the one no other test in this file
// covers: accepted aside, every phase needs at least one way IN.
//
// An orphaned phase is invisible to everything here. Rank would cover it, the
// closed-set test would enumerate it, the terminal test would pass over it, and
// the can-fail test would find its edge to failed — while nothing could ever
// enter it, because the writer's guard refuses any hop whose target names no
// predecessor. The turn would simply never reach that stage, and the stage would
// never be recorded as skipped either.
func TestPhasePredecessors_EveryPhaseButAcceptedIsReachable(t *testing.T) {
	for _, phase := range vocabulary.TurnPhases() {
		if phase == vocabulary.PhaseAccepted {
			// Written by the turn's atomic create, never transitioned into,
			// which its own test pins.
			continue
		}
		predecessors, ok := vocabulary.PhasePredecessors(phase)
		if !ok {
			t.Fatalf("no predecessor set for %q", phase)
		}
		if len(predecessors) == 0 {
			t.Fatalf("phase %q has no predecessor, so nothing can ever transition into it: the guard "+
				"refuses every hop whose target names no legal from-phase, and a turn would silently "+
				"never reach this stage", phase)
		}
	}
}

func TestPhasePredecessors_TerminalPhasesAreNotPredecessorsOfAnything(t *testing.T) {
	for _, to := range vocabulary.TurnPhases() {
		predecessors, _ := vocabulary.PhasePredecessors(to)
		for _, from := range predecessors {
			if from.IsTerminal() {
				t.Fatalf("terminal phase %q is a legal predecessor of %q; a finished turn could be "+
					"restarted by a stale trigger", from, to)
			}
		}
	}
}

// Every phase named in the table has to be a member of the closed set, or a
// guard would consult an edge nothing can ever be in.
func TestPhasePredecessors_NameOnlyClosedSetMembers(t *testing.T) {
	known := vocabulary.TurnPhases()
	for _, to := range known {
		predecessors, _ := vocabulary.PhasePredecessors(to)
		for _, from := range predecessors {
			if !slices.Contains(known, from) {
				t.Fatalf("predecessor %q of %q is not in the closed phase set", from, to)
			}
		}
	}
	if _, ok := vocabulary.PhasePredecessors(vocabulary.TurnPhase("resolvin")); ok {
		t.Fatal("a value outside the closed phase set reported a predecessor set")
	}
}

// PhasePredecessors returns a clone; a caller that mutated the answer would be
// editing the FSM for the whole process.
func TestPhasePredecessors_AreNotAliasedToTheTable(t *testing.T) {
	first, _ := vocabulary.PhasePredecessors(vocabulary.PhaseApplying)
	if len(first) == 0 {
		t.Fatal("applying has no predecessors")
	}
	first[0] = vocabulary.PhaseComplete

	second, _ := vocabulary.PhasePredecessors(vocabulary.PhaseApplying)
	if slices.Contains(second, vocabulary.PhaseComplete) {
		t.Fatal("mutating a returned predecessor set changed the table")
	}
}
