// Package e2e is where a turn goes all the way through, for real, with a
// scripted model on the far end of the socket and nothing else replaced.
//
// Everything before this package proved a component alone or a pair of them
// together. What runs here is the composition `cmd/semmachina` boots — real
// graph-ingest, real graph-index, the real rule processor evaluating the real
// turn-sequencing pack, the real agentic loop and tool executor, the real stage
// runners, the real ledger, the real player socket — against a real broker. The
// ONE substitution is the process answering the model endpoint
// (internal/mockmodel), which is a `model_registry` retarget and not a code path.
//
// # Three constraints shape every test here
//
// **A band is chosen by the SEED, not by the script (design F19).** A verdict
// declares intents for all three outcome bands and the seeded dice select one, and
// modifier sums are bounded precisely so a verdict cannot pre-determine the
// result. So the per-band scenarios supply the (campaign_seed, turn_id) pair whose
// derived roll lands where they need it, pinned in seeds_test.go. If those pairs
// stop producing their bands, seeded replay has broken — which makes the pinning
// a proof rather than a convenience.
//
// **Provider shape is asserted AT THE WIRE (design F18).** The framework's model
// client normalizes: it recomputes token totals, infers a tool call from the
// presence of tool_calls regardless of finish_reason, and substitutes an empty
// argument map for malformed arguments. Every one of those is correct client
// behaviour and every one of them makes a wrong response indistinguishable from a
// right one when asserted through the client. So the stub is wrapped in a
// recorder and the bytes on the socket are what the fidelity assertions read.
//
// **A turn completing is compatible with almost any defect upstream of the last
// stage.** So the assertions here are specific facts — which band's effects
// landed and which two bands' did not, how many times each persona was billed,
// which closed code a refused turn ended with, whether a stage consumer is still
// holding an unacknowledged delivery — rather than "the phase reached complete".
//
// # Isolation
//
// Each test gets its own world namespace, which means its own campaign, its own
// player, its own copy of the starter world's mutable facts, and its own scripted
// model. The scenario pack's effect targets are six-part entity ids, so they are
// rebound per test through mockmodel.TurnLoopScenariosIn. Sharing one namespace
// would make a scenario that wounds a character change what the next scenario
// reads, and order-dependent world state is the cheapest way for an end-to-end
// test to pass for the wrong reason.
//
// What is NOT isolated is the broker: one container serves the package, and the
// durable stage consumers are the ENGINE's — one per phase, named for the phase
// and not for the world. That is why the idleness assertions filter by turn and
// why every test leaves its triggers acknowledged.
//
// # How a crash is staged
//
// A crash is a process that stops between two durable facts, and the hard part of
// testing one is landing in the intended gap rather than near it. Polling for a
// fact and then stopping the engine is a race whose loser is silent: the kill
// lands after the next stage ran and the test proves something weaker than it
// says.
//
// So the gap is made rather than caught. JetStream can PAUSE a consumer, and a
// paused consumer is indistinguishable from a dead process to everything upstream
// of it: the trigger is published, it is captured by the stream, and nothing
// consumes it. The engine then runs the turn up to exactly that lane, the test
// asserts the pre-crash state it wanted (the roll is recorded, the effect batch is
// not), the engine stops, the consumer resumes, and a second boot picks the turn
// up from the durable queue. See resume_test.go.
package e2e
