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
// # Known gap: a persona loop that fails for a reason other than its cap
//
// CapWatcher records cap exhaustion, which is the one persona failure the closed
// vocabulary has a reason code for. A loop that fails for any other cause — a
// model error, a handler fault — is logged loudly and leaves its turn in its
// stage until the rule pack's recovery replay picks it up on the next boot.
// Closing that properly needs a new vocabulary.FailureReason, which is a
// vocabulary change rather than a wiring one.
package stage
