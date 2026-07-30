// Package boot composes one world instance and starts it in the one order that
// is correct.
//
// It is the whole engine in one process — instance-per-world is the MVP's
// deployment shape, so "the stack" and "the binary" are the same thing. What
// lives here is the wiring and the ORDER; every component it starts belongs to
// somebody else.
//
// # Why the order is a mechanism and not a comment
//
// Three of the steps below end live turns when they run in the wrong place, and
// none of those failures is loud on its own. The sharpest is the stranded-turn
// pass: it reads which turns the substrate still holds work for and acts on what
// is MISSING from that set, and the rule processor's bootstrap replay publishes
// into that very set. Run before the processor is up, the pass finds an empty set
// for a turn about to receive a trigger and FAILS that turn — terminally, after
// which the replayed trigger is declined. The pass cannot detect the violation;
// an unstarted processor is indistinguishable from a quiet one.
//
// So the order is stated as DATA. Each Step declares what must already have
// happened, Sequence executes in slice order and refuses a step whose
// prerequisites have not, and the refusal names the missing one. The edit this
// protects against is the realistic one — somebody moving a line in a list —
// which a slice alone records without explaining.
//
// # The sequence, and what each position protects
//
//  1. connect
//  2. entity-stream, then graph — graph-ingest binds its input consumer with
//     AutoCreate false, so the fact lane must exist before the sole
//     ENTITY_STATES writer can start.
//  3. agent-stream, personas, agentic — the agentic loop binds a consumer per
//     declared port, so its stream comes first; the world's persona fragments
//     are seeded before the loop reads the bucket at Start; and the loop must be
//     RUNNING before any persona stage can be triggered.
//  4. world — claim, import (or skip, or refuse), read the world back, mark it.
//  5. stage-stream, then rules — a rule that fires while the stage stream does
//     not exist publishes into nothing, and a stage that was never triggered
//     looks exactly like a stage that ran and did nothing.
//  6. resume — the position above.
//  7. stages and egress — the notifier's durable is DeliverPolicy "new", so a
//     turn resolving before it binds stays retrievable and is not pushed; binding
//     it before intake is what keeps that window empty.
//  8. ledger, action-stream, intake, ingress — ingress last, because it is the
//     only step that lets a stranger add work.
//
// # Which preconditions are CHECKED, and which are only ordered
//
// Stated plainly, because "the order protects it" and "something verifies it"
// are different claims and only one of them survives an edit.
//
// CHECKED, by reading the running system:
//
//   - The payload registry decodes this engine's own player action. A binary
//     that forgot payload.RegisterPayloads would consume every action, decode
//     none, and accept no turns, with no error anywhere.
//   - Every stream this engine publishes onto exists AND captures the subject.
//     Both halves are silent when wrong: an uncaptured publish reaches no
//     consumer, and a consumer filtered outside its stream's subjects is ACCEPTED
//     by the server (measured) and simply never delivered anything.
//   - Both persona capabilities are DECLARED in the model registry, and their
//     endpoints support tool calling. An undeclared capability resolves — to the
//     registry's default model — so "it started" says nothing about which model
//     the schema-bearing persona is on.
//   - The rule processor reported a lifecycle status stamped by THIS boot. See
//     checkRuleProcessorStarted: the obvious probe (Health) is worthless because
//     the processor reports Healthy from construction, and the status key
//     survives a restart, so freshness rather than presence is the check.
//   - The import-completion marker, re-read from the graph immediately before the
//     two steps that let play begin.
//   - Every planned entity is queryable AND non-stub, and every membership edge
//     the plan declares is readable from the reverse-edge index.
//
// NOT CHECKED, and why:
//
//   - That the rule processor's bootstrap replay has DRAINED. Upstream exposes no
//     completion signal for it, and its timing relative to Start returning is a
//     race measured both ways. The pass covers this itself by waiting for the work
//     queues to stop moving (resume.WorkQueues.Settle) — observation instead of a
//     sleep — which is why the composition only has to guarantee "started".
//   - That no player action is accepted during the pass. There is no way to ask
//     the broker "is anybody about to bind this durable?", so it is structural: the
//     intake consumer is not constructed until several steps later, and the
//     sequence refuses to reorder them.
//
// # The instantiation gate, in three answers
//
// campaign.Gate.Claim is one atomic create and answers "was this campaign created
// before?" — never "did the import that followed it finish?". The marker answers
// the second. Absent campaign means import; campaign with a marker means skip;
// campaign WITHOUT a marker means somebody else is mid-import or was, so this boot
// waits a bounded time and then fails loudly rather than choosing between two
// wrong answers. Only the claimant that saw Fresh may write the marker, and that
// is an argument to campaign.Gate.MarkImported rather than a rule to remember.
package boot
