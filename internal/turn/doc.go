// Package turn owns the turn's durable identity, its phase record, and the
// intake that brings a player action into the engine as one turn.
//
// It is where the slice's correctness story closes. Everything before it
// produces artifacts — a verdict, a roll, an effect batch — and this is the part
// that makes duplicate delivery and crash-at-any-phase safe.
//
// # Facts and requests meet here
//
// A player action is a REQUEST: it arrives on a JetStream stream, is consumed by
// a durable consumer, and is acknowledged exactly once. A turn is a FACT: it is
// an entity in ENTITY_STATES whose single-valued phase predicate is the durable
// record of how far it got. Intake is the seam between the two, and the ordering
// across that seam is the whole guarantee:
//
//	create the turn entity  →  then acknowledge the action
//
// A crash before the create redelivers the action and the turn is created on the
// retry. A crash after the create redelivers the action, the create loses to the
// existing turn, and intake acknowledges without touching a turn that may
// already be halfway through. Neither order's failure is silent, and the reverse
// order's would be: an acknowledged action with no turn is a player's move that
// simply never happened.
//
// Resume comes from the other side of the seam. The turn's phase is a KV fact,
// so a restart re-delivers it, and the rule pack matches on it to re-enter the
// stage that was in flight. The action stream is not what resumes a turn — it is
// what must NOT be replayed.
//
// # What the phase guard is, and what it is not
//
// The phase predicate is single-valued and every write to it takes the entity
// MERGE lane, so a duplicate stage trigger leaves exactly one phase value. The
// triple-add lanes append: through those, the same duplicate leaves a turn
// holding two phases with a success response and no error, and the predicate
// stops being an idempotency guard at all.
//
// The guard is CONVERGENT, not mutually exclusive. Advance reads the recorded
// phase and then writes one, and two writers that read the same phase both pass.
// Closing that window needs a compare-and-swap on the read revision, and the
// entity query surface does not return a revision to swap on (graph-ingest reads
// entry.Revision for its own validation and discards it), so the primitive is
// not available to a caller. That is an engine ask, not a local retry loop.
//
// Nothing here works around it because nothing here needs to: this slice runs
// one player, one turn at a time, and every stage under the guard is idempotent
// by construction — the dice are seeded from the turn id, the effect batch is
// keyed by the turn id, and every write replaces. That is the same posture
// upstream takes for the identical problem shape in its gated-dag claim marker,
// including the CAS-upgrade point: if cross-writer exclusion is ever needed, the
// change is to pass the read revision into
// graph.UpdateEntityWithTriplesRequest.ExpectedRevision, and the rest of this
// package is unaffected.
//
// # No interactive pacing
//
// Nothing here measures the gap between turns. The consumer's deliver policy is
// "all", so an action published while the engine was down is processed on the
// next boot rather than skipped; the ack deadline is held open by heartbeats
// while a turn is being processed, and is irrelevant while nobody is playing.
// Play at email cadence is a supported pace, not a degraded one.
package turn
