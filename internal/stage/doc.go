// Package stage is the executing half of the turn loop.
//
// The rule pack (internal/rulepack) watches the turn entity and decides WHEN a
// hop should happen; this package decides WHAT the hop does. That split is the
// house rule — rules trigger, components execute — and it is the reason a rule
// here never writes a phase, never carries content, and never reads a verdict:
// it publishes a turn entity ID onto a stage subject and stops.
//
// # One stage, one phase, entered first
//
// Every stage owns exactly one phase and enters it before doing anything else,
// through the turn recorder's guard. The recorder is the only thing that knows
// the FSM, so it is the only thing that can tell a stale trigger (decline), a
// resumed stage (run it — the phase was written on ENTRY and never claimed the
// stage finished), and a wiring bug (an illegal hop) apart. Every stage under
// that guard is idempotent by construction rather than by suppression: the dice
// re-derive, the applier keys its batch on the turn, the content keys are
// derived, and every single-valued write goes down the merge lane.
//
// # The guard and the recorder answer different questions
//
// The persona stages consult a second guard, and the ORDER is load-bearing —
// see Spawner.Run. The recorder answers "is this transition legal"; the artifact
// guard answers "did the interrupted attempt finish". Entering the phase first
// means the guard is only ever asked about the stage it governs, and a skip
// means "do not pay for this again", never "advance past it".
//
// # Both persona endings are recorded, and neither waits for a replay
//
// LoopFailureWatcher ends the turn for a loop that exhausted its cap AND for one
// that failed for any other cause, with a different closed reason code for each.
// The second used to be logged and left, on the stated grounds that the rule
// pack's recovery replay would pick the turn up on the next boot. That replay
// does not happen: bootstrap replay re-fires a rule that is currently matching
// AND whose entity has been written since the rule last evaluated it, and a turn
// parked mid-stage satisfies neither. Measured against a live broker, not
// inferred — see internal/resume, which is the boot pass that actually finds
// those turns.
package stage
