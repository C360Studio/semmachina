package vocabulary

import "slices"

// stageArtifacts is, per phase, every predicate the stage that OWNS that phase
// lands on the turn entity when it finishes.
//
// It answers a question two very different consumers ask in the same words, and
// they are the reason it lives here rather than in either of them:
//
//   - The turn-sequencing pack's load gate needs it to tell a mid-chain rule
//     that is genuinely gated on the previous stage's OUTPUT from one that
//     merely names some second predicate. `turn.action.player` is present from
//     the turn's birth, so a rule conjoining it with a phase looks gated and
//     races exactly as a phase-only rule does (F21).
//   - The boot-time stranded-turn pass needs it to tell "the stage finished and
//     the chain stalled" from "the stage never produced anything". Those two get
//     different dispositions, and only one of them spends an attempt.
//
// Every list is COMPOSED from the projection lists the stage itself writes
// through, never restated. That is what makes the mapping unable to drift: a
// scalar added to the verdict's rule-matched projection joins this set the same
// day, because it is the same slice.
//
// The three phases with no entry are statements rather than omissions:
//
//   - `accepted` is written by intake's atomic CREATE, which is a completed fact
//     rather than a stage-entry marker. Nothing is pending when a turn is
//     accepted; the pack's first hop is the one rule that legitimately gates on
//     a phase alone.
//   - `complete` and `failed` are terminal. A terminal turn has no stage owing
//     it anything, and asking what artifact would prove one finished is a
//     category error — which StageArtifacts reports as a false bool rather than
//     as an empty list, so "this phase has no artifacts" and "this is not a
//     phase" stay distinguishable.
var stageArtifacts = map[TurnPhase][]Predicate{
	PhaseAccepted: nil,
	// Both real and not-applicable interpretation records land this reference;
	// only real decisions also carry a kind.
	PhaseInterpreting: {TurnCaseDecisionRef},
	// The adjudicator's rule-matched scalars plus the pointer at the banded
	// intents. All five land in one merge write, which is why any one of them
	// witnesses the whole stage.
	PhaseAdjudicating: append(slices.Clone(verdictScalarPredicates), TurnVerdictRef),
	// The band and the total the pack routes on, plus the pointer at the
	// replayable roll record.
	PhaseResolving: append(slices.Clone(rollScalarPredicates), TurnRollRef),
	// The batch marker the applier lands LAST, after every per-entity merge
	// succeeded, plus the pointer at the committed intent set.
	PhaseApplying: append(slices.Clone(effectScalarPredicates), TurnEffectsRef),
	// Every companion path, including exact no-op and exhausted outcomes, lands
	// one stage record. A real or exhausted outcome may also land a decision.
	PhaseCompanion: {TurnCompanionStageRef, TurnCompanionDecisionRef},
	// The narrator's projection is deliberately scalar-less: everything
	// structural was decided by an earlier stage, so its only mark is the
	// pointer at its prose.
	PhaseNarrating: append(slices.Clone(narrationScalarPredicates), TurnNarrationRef),
	PhaseComplete:  nil,
	PhaseFailed:    nil,
}

// stageWitness is, per phase, the ONE predicate a reader checks to answer "did
// the stage that owns this phase finish?".
//
// It is the REFERENCE in every case where the stage produces one, and that is
// not a coincidence. A reference is written only once the artifact behind it is
// durable, so it is the predicate whose presence carries the strongest claim;
// it is also the predicate persona.Spec.Artifact already names for the two
// personas, and TestStageWitness_AgreesWithEveryPersonaSpec holds the two
// statements together from the persona package's own test binary.
//
// `applying` is the exception and it points the same way: the applier lands
// turn.effects.batch after every per-entity merge has committed, so the marker
// — not the reference beside it — is the thing that says the world actually
// changed.
var stageWitness = map[TurnPhase]Predicate{
	PhaseInterpreting: TurnCaseDecisionRef,
	PhaseAdjudicating: TurnVerdictRef,
	PhaseResolving:    TurnRollRef,
	PhaseApplying:     TurnEffectsBatch,
	PhaseCompanion:    TurnCompanionStageRef,
	PhaseNarrating:    TurnNarrationRef,
}

// StageArtifacts returns every predicate the stage owning p lands on the turn
// when it finishes, in projection order.
//
// The bool is false for any value outside the closed phase set. A known phase
// with no owning stage — `accepted` and the two terminal phases — returns an
// empty slice with a true bool.
func StageArtifacts(p TurnPhase) ([]Predicate, bool) {
	predicates, known := stageArtifacts[p]
	if !known {
		return nil, false
	}
	return slices.Clone(predicates), true
}

// StageWitness returns the single predicate whose presence proves the stage
// owning p finished. The bool is false for a phase no stage owns, which
// includes both terminal phases and `accepted`.
func StageWitness(p TurnPhase) (Predicate, bool) {
	predicate, ok := stageWitness[p]
	return predicate, ok
}
