package vocabulary

import "slices"

// The turn's forward progression, and the transitions that are legal into each
// phase.
//
// This is the turn FSM stated ONCE, as data. It lives here rather than in the
// component that writes phases for the same reason every other closed set does:
// three consumers need it and each would otherwise re-derive it. The phase
// writer needs it to guard a transition, the rule pack (task 8.1) needs it to
// author `transition` conditions with matching `from` sets, and a reader
// diagnosing a stuck turn needs to know which hop was skipped.
//
// Note what the table does NOT do: it is not mutual exclusion. Two writers that
// read the same phase both pass the guard, and the merge lane leaves one phase
// value either way. Exclusion in this slice comes from single-flight execution
// (one intake consumer, one turn at a time), which is exactly the posture
// upstream's gated-dag claim marker documents for the identical problem shape,
// including its CAS-upgrade point. See internal/turn for where that upgrade
// would land if it is ever needed.
var (
	// phaseRanks is each phase's position in the turn's forward progression.
	//
	// Both terminal phases share the last rank on purpose: a turn that has ended
	// is past every stage, and which way it ended does not make it more or less
	// past. The rank is what separates "you are late" (a stale trigger for a
	// stage the turn already left — benign, and a no-op) from "you skipped a
	// stage" (a wiring bug that must be loud).
	phaseRanks = map[TurnPhase]int{
		PhaseAccepted:     0,
		PhaseInterpreting: 1,
		PhaseAdjudicating: 2,
		PhaseResolving:    3,
		PhaseApplying:     4,
		PhaseNarrating:    5,
		PhaseComplete:     6,
		PhaseFailed:       6,
	}

	// phasePredecessors is the legal from-set for each phase.
	//
	// PhaseAccepted has none, and that is a statement rather than an omission:
	// the accepted phase is written by the turn entity's atomic CREATE, so
	// nothing ever transitions into it. A turn that could be moved back to
	// accepted could be re-adjudicated after it had already resolved.
	//
	// PhaseApplying admits PhaseAdjudicating directly because a no-roll verdict
	// skips the dice entirely (design D3): the auto band goes straight to the
	// applier, and no roll-result triple is ever created for that turn.
	//
	// PhaseFailed admits every non-terminal phase, because bounded execution
	// means any stage may end the turn explicitly rather than stalling.
	phasePredecessors = map[TurnPhase][]TurnPhase{
		PhaseAccepted:     nil,
		PhaseInterpreting: {PhaseAccepted},
		PhaseAdjudicating: {PhaseInterpreting},
		PhaseResolving:    {PhaseAdjudicating},
		PhaseApplying:     {PhaseAdjudicating, PhaseResolving},
		PhaseNarrating:    {PhaseApplying},
		PhaseComplete:     {PhaseNarrating},
		PhaseFailed: {
			PhaseAccepted, PhaseInterpreting, PhaseAdjudicating, PhaseResolving,
			PhaseApplying, PhaseNarrating,
		},
	}
)

// PhaseRank returns p's position in the turn's forward progression. The bool is
// false for any value outside the closed phase set.
func PhaseRank(p TurnPhase) (int, bool) {
	rank, ok := phaseRanks[p]
	return rank, ok
}

// PhasePredecessors returns the phases a turn may enter p from. The bool is
// false for any value outside the closed phase set; an empty slice with a true
// bool means p is never transitioned into.
func PhasePredecessors(p TurnPhase) ([]TurnPhase, bool) {
	if _, known := phaseRanks[p]; !known {
		return nil, false
	}
	return slices.Clone(phasePredecessors[p]), true
}

// PhaseFollows reports whether from is a legal predecessor of to.
func PhaseFollows(from, to TurnPhase) bool {
	return slices.Contains(phasePredecessors[to], from)
}
