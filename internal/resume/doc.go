// Package resume finds turns that stopped and puts them back in motion.
//
// It is the boot-time answer to a hole the substrate does not close: a turn
// whose phase is non-terminal and for which no stage trigger is queued has
// nothing anywhere that will ever run it again.
//
// # The two resume mechanisms that DO exist, and what each covers
//
// JetStream redelivery of an unacked stage trigger resumes a stage that CRASHED
// WHILE HOLDING ITS TRIGGER. That is durable and correct, and it is the reason
// stage triggers are a stream rather than core NATS. It covers nothing once the
// trigger has been acknowledged — and the persona stages acknowledge as soon as
// the task is published, because the loop that will produce the artifact runs
// somewhere else entirely.
//
// The rule engine's bootstrap replay re-fires a rule at boot, but only one that
// is CURRENTLY MATCHING and whose entity has been WRITTEN SINCE the rule last
// evaluated it — upstream's durable stale-replay guard skips an evaluation whose
// source revision it has already recorded. A turn parked mid-stage satisfies
// neither: every mid-chain rule is a conjunction of a phase and the artifact the
// previous stage produces, and nobody has touched the turn since the rule fired.
//
// Measured, not inferred, twice. A rule processor restarted against a broker
// holding three parked turns — one in `accepted` whose first hop had already
// fired, one in `adjudicating` with no verdict, one in `adjudicating` WITH a
// verdict — published nothing for any of them in twenty seconds
// (TestBootstrapReplay_RescuesNoParkedTurn, in internal/stage, where the harness
// that boots a real rule processor lives). And when replay DOES fire — a turn
// written while the processor was down — its timing relative to the processor's
// Start is NOT stable: observed publishing 1.02 seconds after Start returned, and
// observed having already published by the time Start returned. That instability
// is what the ordering constraint below rests on.
//
// # What this package does instead: measure, then dispose
//
// One pass over every turn in the world, at boot. It reads the set of turns the
// substrate still holds WORK for — across every durable queue that can deliver
// work for a turn, from each consumer's acknowledgement floor
// (WorkQueues.Pending) — and sorts every non-terminal turn against it:
//
//   - WORK IS QUEUED. The substrate owns the delivery. Counted, untouched.
//   - NOTHING IS QUEUED, and the phase's artifact is ABSENT. The stage never
//     produced anything and nothing is coming. This is the case nothing covers,
//     and the only one that gets a message: the stage the turn is owed is
//     re-triggered and an attempt is spent, because this one CAN re-run a billed
//     model call. For every phase a stage enters, the stage owed is that phase's
//     own; a turn still in `accepted` is owed the FIRST stage, which is one
//     unconditional fact and the only derivation this pass makes — see
//     resumeSubject for why index 0 is safe and no other index is.
//   - NOTHING IS QUEUED, and the phase's artifact is PRESENT. The stage finished
//     and the hop that should have followed does not exist. Re-running its own
//     stage cannot help — the recorder reads the re-entry as a resume and the
//     persona guard skips on the artifact — so the sighting is counted, without a
//     publish or a model call, and the turn ends once the budget is gone. It is
//     not ended on the first sighting, because the queued set is one snapshot
//     taken before the turn pages are read: a redelivered persona task can land a
//     verdict and the pack can publish a hop in between, and a turn ended on that
//     reading would be one seconds from resolving, failed terminally with its
//     live trigger then declined.
//
// # "Is work still coming" spans every queue, not one of them
//
// The general form, and the sentence to keep: this question must be answered
// across EVERY durable queue that can deliver work for a turn. Today there are
// two, and reading only the first is a real defect rather than a hypothetical
// one.
//
//   - TURN_STAGES carries stage triggers.
//   - AGENT carries persona tasks. Spawner.Run acknowledges the stage trigger
//     when it PUBLISHES the task, not when the persona finishes, so a turn whose
//     adjudication is in flight has nothing queued on TURN_STAGES and an unacked
//     task on AGENT that upstream will redeliver. Reading only the stage stream
//     would re-trigger it with a fresh task id and buy two to three billed
//     adjudications for one turn, two of them racing to write turn.verdict.*
//     through a last-write-wins merge.
//
// A third queue must be added the day one exists, and the question to ask of any
// new durable input is exactly that: can it deliver work for a turn?
//
// AGENT_LOOPS cannot answer it, and this is recorded so nobody re-derives it.
// Only a handler transitions a loop out of `state=running`
// (processor/agentic-loop/component.go), so a process that died mid-loop leaves a
// stale `running` entry indistinguishable from a live one — a liveness claim read
// off it would be the same inference this package was rebuilt to remove.
//
// Attempts are bounded (MaxAttempts) and the bound has an ending: a turn whose
// stage produces no artifact across that many re-triggers is failed on the record
// with vocabulary.FailureTurnStranded rather than left waiting forever. That is
// the same trade the persona iteration cap makes, taken one level up.
//
// # Why it MEASURES rather than reads the rule pack
//
// Worth stating because two earlier versions of this package did the latter, and
// each was wrong in the opposite direction. Both asked the pack whether a rule
// matched the turn and inferred a message from the answer: first that the message
// had gone missing (so republish it — which on the first hop is a second billed
// adjudicator spawn for a hop nothing dropped), then that the message was durably
// pending (so leave it — which strands every turn whose rule fired and whose
// publish did not).
//
// The inference is unavailable in either direction, because a rule firing and a
// message existing are different events. natsclient's publish path fails on an
// open circuit breaker, on a disconnected client, and on a refused PubAck; the
// circuit breaker in particular opens after repeated failures and then
// short-circuits every subsequent publish, so one broker wobble strands a batch
// of turns at once. The rule engine persists match state whether or not its
// action succeeded. A rule's own action-level max_iterations can exhaust. Each of
// those leaves a matching rule and no message, and no reading of the pack finds
// any of them.
//
// So the question "is a message coming for this turn" is answered by looking at
// the messages. Nothing here derives a hop, a successor phase, or a routing
// decision from the pack — vocabulary.PhaseRank and PhasePredecessors are
// deliberately unused, because using them to publish the NEXT phase's trigger
// would rebuild the second FSM this design exists without.
//
// # The assumptions this rests on, stated so they can be checked
//
// "No stage is running" is decided from stored state alone — no liveness probe,
// no staleness clock, no heartbeat. That is sound because of ONE property of the
// MVP deployment: instance-per-world means one process per world, so at the
// moment this pass runs there is no other process, and a turn with no queued
// trigger is idle because the only thing that could have been working on it is
// the process now booting.
//
// What breaks it is more than one process per world. Two engines against one
// broker would let this pass read a turn a live stage is mid-way through and
// re-trigger it: harmless for the dice and the applier, which are idempotent by
// construction, and a second billed spawn for a persona whose artifact has not
// landed yet. If instance-per-world ever stops holding — hosted multi-worker, an
// active-active pair, a second engine sharing a campaign — this pass needs a
// liveness signal before it needs anything else.
//
// Note what instance-per-world does NOT buy, because assuming it did was a real
// defect: it says nothing about work already in flight INSIDE the booting
// process. A persona task sitting unacknowledged on AGENT will be redelivered to
// this very process, so the thing still working on that turn is not another
// engine — it is a message. That is measured, not assumed, which is why the
// queued set spans both queues.
//
// # The ordering constraint, and which half is enforced
//
// The pass must run after the stage-trigger stream exists, after the rule
// processor has STARTED, and before the player-action consumer binds.
//
// The middle one is the one that is easy to get wrong and impossible to check
// from here. The rule processor's bootstrap replay publishes into the very set
// this pass reads, and WHEN it does so relative to Start returning is a race —
// measured both ways on the same code. A pass that read the queued set before the
// processor was up would find an empty set for a turn about to receive a trigger,
// and would then either re-trigger it (a duplicate) or end it (worse). An
// unstarted processor is indistinguishable from a quiet one, so this is a
// composition constraint rather than a check.
//
// The gap between "started" and "finished replaying" IS enforced, and without
// anybody picking a sleep: StageStream.Settle waits for the stage stream to stop
// moving before the queued set is read, and refuses to proceed if it will not
// hold still. Upstream exposes no bootstrap-replay completion signal; that is the
// engine ask this works around by observation rather than by guess.
//
// # What this pass does NOT do
//
// It never derives what comes NEXT. The one derivation it makes is what STARTS a
// turn — a single unconditional fact, because a turn is created in `accepted` and
// the first stage is the first stage. Everything after that has a condition
// attached (a verdict's roll gate sends the turn to the dice or straight to the
// applier), and reconstructing those is the inference this package was rebuilt
// without. If a turn is stuck somewhere the pass has no move for, it ends the
// turn rather than guessing the hop.
package resume
