package turn_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// "Exactly one turn per action" and "exactly one phase per turn" are claims
// about the GRAPH and about a real durable consumer, and both are where they can
// be false. The mutation API offers two lanes that accept the same triples — one
// merges by (subject, predicate) and one appends — and picking the wrong one
// leaves a turn holding two phases, with a success response and no error
// anywhere. A fake with one lane cannot see that, and neither can a fake
// consumer see an acknowledgment that never happened.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

// Each test gets its own world namespace and its own stream, so tests sharing
// one broker cannot see each other's turns or each other's actions.
var integrationCounter atomic.Int64

// countingStore is the REAL graph store with one number added: how many times
// the recorder tried to bring a turn into existence.
//
// It exists because the restart assertion below cannot fail without it. The
// consumer's delivery count is a property of the CONSUMER INSTANCE, not of the
// action, so a consumer that was deleted and recreated starts counting at one
// again and a genuine full replay reads as "one before, one after" — the check
// passes while the handler ran twice. The requirement is that the HANDLER DID
// NOT RUN AGAIN, and the only witness to that is the handler's own work.
//
// CreateEntity is the right thing to count: Accept issues exactly one per call,
// win or lose, so one create is one handler run that got past decode. Everything
// else is delegated to the real store, so nothing about the seam under test is
// simulated.
type countingStore struct {
	turn.Store
	creates atomic.Int64
}

func (s *countingStore) CreateEntity(
	ctx context.Context,
	entity *graph.EntityState,
) (graphio.CreateResult, error) {
	s.creates.Add(1)
	return s.Store.CreateEntity(ctx, entity)
}

// liveTurns is one world namespace with a recorder over the real graph and the
// real content store.
type liveTurns struct {
	harness  *testinfra.Harness
	store    *graphio.Store
	counted  *countingStore
	content  *content.Store
	recorder *turn.Recorder

	identity  turn.Identity
	namespace string
}

func startTurns(t *testing.T) *liveTurns {
	t.Helper()
	harness := testinfra.Require(t)

	namespace := fmt.Sprintf("turnw%d", integrationCounter.Add(1))
	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// A bucket per namespace, because artifact keys are derived from turn_id
	// and instance-per-world is what makes that unique in production. Tests
	// share one broker, so they take the isolation the deployment gives them.
	backend, err := content.NewObjectStore(t.Context(), harness.Client,
		content.WithBucket("TURN_CONTENT_"+namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	contentStore, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	identity := turn.Identity{Org: testOrg, WorldNS: namespace, Template: testTemplate}
	counted := &countingStore{Store: store}
	recorder, err := turn.NewRecorder(counted, contentStore, identity)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	return &liveTurns{
		harness: harness, store: store, counted: counted, content: contentStore, recorder: recorder,
		identity: identity, namespace: namespace,
	}
}

// action builds a canonical action whose identity fields live in this test's own
// namespace, so the turn it produces cannot collide with another test's.
func (w *liveTurns) action(actionID string) *payload.PlayerAction {
	compose := func(kind, instance string) string {
		id, err := vocabulary.ComposeEntityID(testOrg, w.namespace, testTemplate, kind, instance)
		if err != nil {
			panic(err)
		}
		return id
	}
	return &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   compose("player", "p1"),
		CampaignID: compose("campaign", "main"),
		SceneID:    compose("scene", "gatehouse"),
		Text:       "I lever the gate open with the crowbar.",
		ArrivedAt:  time.Now().UTC(),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-7"},
	}
}

func (w *liveTurns) entityIDFor(t *testing.T, actionID string) string {
	t.Helper()
	_, entityID, err := w.identity.EntityIDForAction(actionID)
	if err != nil {
		t.Fatalf("EntityIDForAction: %v", err)
	}
	return entityID
}

// accept creates a turn through the production path and returns its acceptance.
func (w *liveTurns) accept(t *testing.T, actionID string) turn.Acceptance {
	t.Helper()
	acceptance, err := w.recorder.Accept(t.Context(), w.action(actionID))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	w.harness.AwaitEntity(t, acceptance.TurnEntityID)
	return acceptance
}

func (w *liveTurns) advance(t *testing.T, a turn.Acceptance, phases ...vocabulary.TurnPhase) {
	t.Helper()
	for _, phase := range phases {
		transition, err := w.recorder.Advance(t.Context(), a.TurnID, a.TurnEntityID, phase)
		if err != nil {
			t.Fatalf("Advance to %q: %v", phase, err)
		}
		if transition.Outcome != turn.OutcomeAdvanced {
			t.Fatalf("Advance to %q reported %q", phase, transition.Outcome)
		}
	}
}

// phaseValues reads every phase object the graph holds for a turn. A slice
// rather than a single value on purpose: "how many values does this predicate
// hold?" is the question that distinguishes a replace-lane write from an
// append-lane one.
func (w *liveTurns) phaseValues(t *testing.T, turnEntityID string) []any {
	t.Helper()
	state := w.harness.AwaitEntity(t, turnEntityID)
	return testinfra.ObjectsFor(state, vocabulary.TurnPhaseCurrent.String())
}

// ---------------------------------------------------------------- intake

// liveIntake is one intake bound to its own stream and durable consumer.
type liveIntake struct {
	*liveTurns
	stream   string
	subject  string
	consumer string
	intake   *turn.Intake
}

func startIntake(t *testing.T, opts ...turn.IntakeOption) *liveIntake {
	t.Helper()
	turns := startTurns(t)

	suffix := turns.namespace
	stream := "PLAYER_ACTIONS_" + suffix
	// Deliberately OUTSIDE the canonical `player.action.>` space: NATS refuses a
	// stream whose subjects overlap an existing one, so a per-test stream under
	// the real prefix would make the canonical stream uncreatable. The subject is
	// deployment configuration — WithStream exists for exactly this — and the
	// canonical coordinates are pinned by their own test.
	subject := "itest.player.action." + suffix
	consumer := "intake-" + suffix

	// The stream is created here rather than auto-created by the consumer,
	// because retention is a DEPLOYMENT decision and not a component's: limits
	// retention with no age or size ceiling keeps an acknowledged action
	// readable, which the ledger's action reference will need.
	if _, err := turns.harness.Client.EnsureStream(t.Context(), jetstream.StreamConfig{
		Name:      stream,
		Subjects:  []string{subject},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
	}); err != nil {
		t.Fatalf("EnsureStream %s: %v", stream, err)
	}

	options := append([]turn.IntakeOption{
		turn.WithStream(stream, subject),
		turn.WithConsumerName(consumer),
	}, opts...)
	intake, err := turn.NewIntake(
		turns.harness.Client, turns.recorder, message.NewDecoder(turns.harness.Registry), options...)
	if err != nil {
		t.Fatalf("NewIntake: %v", err)
	}
	return &liveIntake{liveTurns: turns, stream: stream, subject: subject, consumer: consumer, intake: intake}
}

func (i *liveIntake) start(t *testing.T) {
	t.Helper()
	if err := i.intake.Start(t.Context()); err != nil {
		t.Fatalf("Start intake: %v", err)
	}
	t.Cleanup(func() { i.harness.Client.StopConsumer(i.stream, i.consumer) })
}

// publish puts the action on the stream the way an ingress adapter will:
// BaseMessage-wrapped, on the canonical subject, with a broker acknowledgment.
func (i *liveIntake) publish(t *testing.T, action *payload.PlayerAction) {
	t.Helper()
	data, err := json.Marshal(
		message.NewBaseMessage(action.Schema(), action, "ingress-test", message.WithTime(action.ArrivedAt)))
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if _, err := i.harness.Client.PublishToStreamWithAck(t.Context(), i.subject, data); err != nil {
		t.Fatalf("publish action: %v", err)
	}
}

// awaitDrained waits until the durable consumer has nothing pending and nothing
// unacknowledged. Both halves matter: pending zero alone would be satisfied by a
// consumer that took every message and never acked one.
func (i *liveIntake) awaitDrained(t *testing.T) {
	t.Helper()
	js, err := i.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	stream, err := js.Stream(t.Context(), i.stream)
	if err != nil {
		t.Fatalf("stream %s: %v", i.stream, err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		consumer, err := stream.Consumer(t.Context(), i.consumer)
		if err != nil {
			last = err.Error()
		} else {
			info, infoErr := consumer.Info(t.Context())
			switch {
			case infoErr != nil:
				last = infoErr.Error()
			case info.NumPending == 0 && info.NumAckPending == 0:
				return
			default:
				last = fmt.Sprintf("pending=%d ack_pending=%d", info.NumPending, info.NumAckPending)
			}
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("context cancelled waiting for the consumer to drain: %v", t.Context().Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("consumer %s never drained: %s", i.consumer, last)
}

// deliveries is how many messages the durable consumer has been handed, ever.
//
// It sees something the turn entity cannot: a replay would lose the atomic
// create and write nothing, so the graph looks identical whether the action was
// redelivered or not.
//
// It has a blind spot of its own, and it is the reason the restart test does not
// rest on this number alone. Delivered.Consumer is scoped to the CONSUMER
// INSTANCE — delete the durable and recreate it under the same name and the
// count starts over at one — so a full replay after a genuine consumer rebuild
// reads exactly like no replay at all. The ack floor that makes this number
// truthful lives on the server, which is what makes it nearly unfalsifiable by
// any change to this repo. countingStore is what closes that: it counts the
// handler's own work, which no server-side bookkeeping can fake.
func (i *liveIntake) deliveries(t *testing.T) uint64 {
	t.Helper()
	js, err := i.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	stream, err := js.Stream(t.Context(), i.stream)
	if err != nil {
		t.Fatalf("stream %s: %v", i.stream, err)
	}
	consumer, err := stream.Consumer(t.Context(), i.consumer)
	if err != nil {
		t.Fatalf("consumer %s: %v", i.consumer, err)
	}
	info, err := consumer.Info(t.Context())
	if err != nil {
		t.Fatalf("consumer info: %v", err)
	}
	return info.Delivered.Consumer
}

// The whole point of the atomic create: the same action delivered twice is one
// turn. Both deliveries travel the real stream, the real durable consumer, the
// real decoder, and real graph-ingest.
func TestIntegration_ADuplicateActionDeliveryCreatesExactlyOneTurn(t *testing.T) {
	live := startIntake(t)
	live.start(t)

	action := live.action("act-dup")
	live.publish(t, action)
	live.publish(t, action)
	live.awaitDrained(t)

	entityID := live.entityIDFor(t, action.ActionID)
	phases := live.phaseValues(t, entityID)
	if len(phases) != 1 {
		t.Fatalf("two deliveries left %d phase values on one turn: %v", len(phases), phases)
	}
	if phases[0] != string(vocabulary.PhaseAccepted) {
		t.Fatalf("the turn is in phase %v", phases[0])
	}

	state := live.harness.AwaitEntity(t, entityID)
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.TurnPhaseCurrent, vocabulary.TurnActionPlayer, vocabulary.TurnActionScene,
	} {
		if got := testinfra.ObjectsFor(state, predicate.String()); len(got) != 1 {
			t.Fatalf("the turn holds %d values for %s after two deliveries: %v", len(got), predicate, got)
		}
	}
}

// A duplicate arriving at a turn that has MOVED ON is the redelivery that
// matters: acknowledge it, and do not reset an in-flight turn to accepted.
func TestIntegration_ARedeliveryIntoAnAdvancedTurnDoesNotResetIt(t *testing.T) {
	live := startIntake(t)
	live.start(t)

	action := live.action("act-late")
	live.publish(t, action)
	live.awaitDrained(t)

	entityID := live.entityIDFor(t, action.ActionID)
	turnID := payload.TurnIDForAction(action.ActionID)
	live.advance(t, turn.Acceptance{TurnID: turnID, TurnEntityID: entityID},
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving)

	live.publish(t, action)
	live.awaitDrained(t)

	// The graph is written asynchronously, so a phase that was about to be reset
	// would not necessarily be visible yet. Poll for long enough that a reset
	// would have landed — an order of magnitude beyond the sub-100ms
	// materialization the other tests in this file observe.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		phases := live.phaseValues(t, entityID)
		if len(phases) != 1 || phases[0] != string(vocabulary.PhaseResolving) {
			t.Fatalf("a redelivery left the turn holding %v; it was in %q",
				phases, vocabulary.PhaseResolving)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The request half of facts-vs-requests: a restart resumes from the last
// acknowledgment and must NOT replay an action that already became a turn.
//
// Two assertions, because neither is sufficient alone. The delivery count is the
// broker's answer and cannot see a handler that ran on a rebuilt consumer; the
// handler's create count is this process's answer and cannot see a message
// redelivered to a handler that never got to work. Together they say the thing
// the requirement says: the action was not handed over again, and the work was
// not done again.
func TestIntegration_ARestartDoesNotReplayAnAcknowledgedAction(t *testing.T) {
	live := startIntake(t)
	live.start(t)

	action := live.action("act-restart")
	live.publish(t, action)
	live.awaitDrained(t)

	entityID := live.entityIDFor(t, action.ActionID)
	live.harness.AwaitEntity(t, entityID)
	before := live.deliveries(t)
	if before != 1 {
		t.Fatalf("the consumer was handed %d messages for one published action", before)
	}
	if got := live.counted.creates.Load(); got != 1 {
		t.Fatalf("one published action ran the handler %d time(s) before any restart", got)
	}

	// The restart: the consumer stops and the same durable name is bound again,
	// which is exactly what a process restart does. The durable survives, so the
	// ack floor it resumes from is the one the first run left.
	live.harness.Client.StopConsumer(live.stream, live.consumer)
	live.start(t)
	live.awaitDrained(t)

	// The graph is not the assertion here and cannot be: a replay would lose the
	// atomic create and write nothing, so the turn entity is byte-identical
	// whether the action was redelivered or not.
	if after := live.deliveries(t); after != before {
		t.Fatalf("the consumer was handed %d messages after a restart, %d before; the acknowledged "+
			"action was replayed, and a restart that re-runs a completed turn is the dragon eating you twice",
			after, before)
	}
	// And the half the delivery count cannot prove: the handler did not run
	// again. A second run would create-and-lose silently — no second entity, no
	// error, no trace in the graph — so counting the create is the only place a
	// re-run is visible from inside the process.
	if got := live.counted.creates.Load(); got != 1 {
		t.Fatalf("the intake handler ran %d time(s) across the restart, want 1; the acknowledged action "+
			"was processed again, and only the atomic create stopped it becoming a second turn",
			got)
	}
	if got := len(live.phaseValues(t, entityID)); got != 1 {
		t.Fatalf("after restart the turn holds %d phase values", got)
	}
}

// No turn-loop step may assume interactive pacing. The gap here is deliberately
// longer than the consumer's whole ack deadline — the interval after which a
// consumer with a delivery in flight would be assumed dead — with nothing in
// flight, which is the state a world spends most of its life in when play runs
// at email cadence. Wall-clock: about 3x AckWait, chosen as the smallest gap
// that actually crosses the deadline it is testing.
func TestIntegration_AnIdleGapLongerThanTheAckDeadlineDoesNotDegradeTheNextTurn(t *testing.T) {
	const ackWait = time.Second

	live := startIntake(t, turn.WithAckTimings(ackWait, ackWait/3))
	live.start(t)

	first := live.action("act-monday")
	live.publish(t, first)
	live.awaitDrained(t)
	firstPhases := live.phaseValues(t, live.entityIDFor(t, first.ActionID))

	time.Sleep(3 * ackWait)

	second := live.action("act-friday")
	live.publish(t, second)
	live.awaitDrained(t)
	secondPhases := live.phaseValues(t, live.entityIDFor(t, second.ActionID))

	if len(secondPhases) != len(firstPhases) || secondPhases[0] != firstPhases[0] {
		t.Fatalf("an action submitted after an idle gap was processed differently: %v vs %v",
			secondPhases, firstPhases)
	}
	if secondPhases[0] != string(vocabulary.PhaseAccepted) {
		t.Fatalf("the second action produced phase %v", secondPhases[0])
	}
}

// ----------------------------------------------------------------- phases

// The property the whole design rests on: a duplicate stage trigger leaves the
// turn holding exactly one phase. The guard declines the second trigger, and
// even a caller that wrote anyway converges, because the merge lane replaces.
func TestIntegration_ADuplicateStageTriggerLeavesExactlyOnePhase(t *testing.T) {
	live := startTurns(t)
	acceptance := live.accept(t, "act-stage")

	live.advance(t, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving)

	second, err := live.recorder.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseResolving)
	if err != nil {
		t.Fatalf("duplicate Advance: %v", err)
	}
	if second.Outcome != turn.OutcomeResumed {
		t.Fatalf("the duplicate trigger reported %q, want resumed", second.Outcome)
	}

	// And a caller that wrote regardless must still converge.
	state := &payload.TurnState{TurnID: acceptance.TurnID, Phase: vocabulary.PhaseResolving}
	triples, err := state.Triples(acceptance.TurnEntityID, turn.Source, time.Now().UTC())
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if _, err := live.store.MergeTriples(t.Context(), acceptance.TurnEntityID, triples); err != nil {
		t.Fatalf("MergeTriples: %v", err)
	}

	phases := live.phaseValues(t, acceptance.TurnEntityID)
	if len(phases) != 1 {
		t.Fatalf("the turn holds %d phase values after a duplicate trigger and a duplicate write: %v",
			len(phases), phases)
	}
	if phases[0] != string(vocabulary.PhaseResolving) {
		t.Fatalf("the turn records phase %v", phases[0])
	}
}

// The other half of the lane argument, pinned against the real broker: the
// APPEND lane really does leave a turn holding two phases, with a success
// response and no error anywhere. Choosing between the two lanes is therefore a
// correctness decision, and if a future semstreams version changes this, the
// reason the phase writer uses MergeTriples changes with it and this test says
// so.
func TestIntegration_TheAppendLaneLeavesTwoPhasesWhichIsWhyTheMergeLaneIsUsed(t *testing.T) {
	live := startTurns(t)
	acceptance := live.accept(t, "act-append")

	state := &payload.TurnState{TurnID: acceptance.TurnID, Phase: vocabulary.PhaseAdjudicating}
	triples, err := state.Triples(acceptance.TurnEntityID, turn.Source, time.Now().UTC())
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	for attempt := range 2 {
		request, err := json.Marshal(graph.AddTriplesBatchRequest{Triples: triples})
		if err != nil {
			t.Fatalf("encode add_batch: %v", err)
		}
		reply, err := live.harness.Client.RequestClassified(
			t.Context(), graphingest.SubjectTripleAddBatch, request, 5*time.Second)
		if err != nil {
			t.Fatalf("add_batch attempt %d: %v", attempt+1, err)
		}
		var response graph.AddTriplesBatchResponse
		if err := json.Unmarshal(reply, &response); err != nil {
			t.Fatalf("decode add_batch response: %v", err)
		}
		// A partial commit is a SUCCESS body with a nil error, so the
		// failed-subject map is the only signal that a write did not land.
		if len(response.FailedSubjects) != 0 {
			t.Fatalf("add_batch reported failed subjects: %v", response.FailedSubjects)
		}
	}

	phases := live.phaseValues(t, acceptance.TurnEntityID)
	if len(phases) < 2 {
		t.Fatalf("two append-lane writes left %d phase value(s) (%v); if the append lane now replaces, "+
			"the reason the phase writer uses the merge lane is no longer true and its doc comment is stale",
			len(phases), phases)
	}

	// And the guard reads exactly this as an unreadable record rather than
	// picking one of the two values.
	if _, err := live.recorder.Current(t.Context(), acceptance.TurnEntityID); err == nil {
		t.Fatal("a turn holding two phase values reported a phase; the guard would be reading a coin flip")
	}
}

// Crash-at-any-phase: the process dies mid-turn, and a brand-new recorder with
// no memory has to resume from the graph alone. The completed stage declines,
// the interrupted stage resumes, and the turn goes on to finish.
func TestIntegration_ACrashResumesFromTheRecordedPhaseWithoutRerunningAStage(t *testing.T) {
	live := startTurns(t)
	acceptance := live.accept(t, "act-crash")
	live.advance(t, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving)

	// The crash. Everything in memory is gone; the turn's phase is a KV fact, so
	// it is what the new process re-delivers and reads.
	restarted, err := turn.NewRecorder(live.store, live.content, live.identity)
	if err != nil {
		t.Fatalf("NewRecorder after restart: %v", err)
	}

	recovered, err := restarted.Current(t.Context(), acceptance.TurnEntityID)
	if err != nil {
		t.Fatalf("reading the recorded phase after restart: %v", err)
	}
	if recovered != vocabulary.PhaseResolving {
		t.Fatalf("the restart read phase %q, want the recorded %q", recovered, vocabulary.PhaseResolving)
	}

	completed, err := restarted.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("re-entering a completed stage: %v", err)
	}
	if completed.Outcome != turn.OutcomeDeclined {
		t.Fatalf("a completed stage reported %q after restart; the adjudicator would run again and be "+
			"billed again", completed.Outcome)
	}

	interrupted, err := restarted.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseResolving)
	if err != nil {
		t.Fatalf("re-entering the interrupted stage: %v", err)
	}
	if interrupted.Outcome != turn.OutcomeResumed {
		t.Fatalf("the interrupted stage reported %q after restart; it would never finish",
			interrupted.Outcome)
	}

	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseApplying, vocabulary.PhaseNarrating, vocabulary.PhaseComplete,
	} {
		transition, err := restarted.Advance(t.Context(), acceptance.TurnID, acceptance.TurnEntityID, phase)
		if err != nil {
			t.Fatalf("advancing to %q after restart: %v", phase, err)
		}
		if transition.Outcome != turn.OutcomeAdvanced {
			t.Fatalf("advancing to %q after restart reported %q", phase, transition.Outcome)
		}
	}

	phases := live.phaseValues(t, acceptance.TurnEntityID)
	if len(phases) != 1 || phases[0] != string(vocabulary.PhaseComplete) {
		t.Fatalf("the resumed turn ended holding %v", phases)
	}
}

// ---------------------------------------------------------------- failure

// The reason has to be retrievable from the turn entity, and it has to be a
// code, with the explanation reachable THROUGH the reference rather than in it.
// The detail is stored for real, because a placeholder string would prove the
// recorder can write a string and nothing about whether anything is behind it.
func TestIntegration_AFailedTurnRecordsAClosedReasonAndAResolvableDetailReference(t *testing.T) {
	live := startTurns(t)
	acceptance := live.accept(t, "act-fail")
	live.advance(t, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)

	detail := &content.FailureDetail{
		TurnID:  acceptance.TurnID,
		Reason:  vocabulary.FailureEffectInvalid,
		Message: "intent 0 (set_attribute health=43 on the courier) exceeds the registered bound",
	}
	ref, err := live.content.PutFailureDetail(t.Context(), acceptance.TurnEntityID, detail)
	if err != nil {
		t.Fatalf("PutFailureDetail: %v", err)
	}

	if _, err := live.recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailureEffectInvalid, ref); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	state := live.harness.AwaitEntity(t, acceptance.TurnEntityID)
	want := map[vocabulary.Predicate]string{
		vocabulary.TurnPhaseCurrent:  string(vocabulary.PhaseFailed),
		vocabulary.TurnFailureReason: string(vocabulary.FailureEffectInvalid),
		vocabulary.TurnFailureRef:    ref.String(),
	}
	for predicate, expected := range want {
		got := testinfra.ObjectsFor(state, predicate.String())
		if len(got) != 1 {
			t.Fatalf("the failed turn holds %d values for %s: %v", len(got), predicate, got)
		}
		if got[0] != expected {
			t.Fatalf("%s reads %v, want %q", predicate, got[0], expected)
		}
	}

	// The reason on the graph is a member of the closed set, which is what makes
	// a rule able to match it and a reader able to interpret it.
	stored, _ := testinfra.FirstObject(state, vocabulary.TurnFailureReason.String()).(string)
	if _, err := vocabulary.ParseFailureReason(stored); err != nil {
		t.Fatalf("the graph carries a failure reason outside the closed set: %v", err)
	}

	// And the explanation is reachable from the graph alone, which is what makes
	// the closed code survivable: an operator follows the reference the turn
	// carries and reads the sentence that never touched a triple.
	recorded, _ := testinfra.FirstObject(state, vocabulary.TurnFailureRef.String()).(string)
	parsed, err := content.ParseRef(recorded)
	if err != nil {
		t.Fatalf("the reference on the turn entity is not a storage reference: %v", err)
	}
	resolved, err := live.content.GetFailureDetail(t.Context(), parsed)
	if err != nil {
		t.Fatalf("resolving the failure detail the turn points at: %v", err)
	}
	if resolved.Message != detail.Message {
		t.Fatalf("the resolved detail reads %q, want %q", resolved.Message, detail.Message)
	}
}

// The player's words have to be reachable from the graph alone after the turn
// exists — that is the entire content of "acknowledged only after the turn
// durably exists", applied to the move rather than to the paperwork.
func TestIntegration_AnAcceptedTurnCarriesAResolvableReferenceToThePlayersWords(t *testing.T) {
	live := startTurns(t)
	action := live.action("act-words")

	acceptance, err := live.recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	state := live.harness.AwaitEntity(t, acceptance.TurnEntityID)

	refs := testinfra.ObjectsFor(state, vocabulary.TurnActionRef.String())
	if len(refs) != 1 {
		t.Fatalf("the accepted turn holds %d action references: %v", len(refs), refs)
	}
	recorded, _ := refs[0].(string)
	ref, err := content.ParseRef(recorded)
	if err != nil {
		t.Fatalf("the reference on the turn entity is not a storage reference: %v", err)
	}

	// Read through a store this test builds fresh, the way a restarted process
	// would: only the reference on the graph survives a crash.
	backend, err := content.NewObjectStore(t.Context(), live.harness.Client,
		content.WithBucket("TURN_CONTENT_"+live.namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	reader, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	got, err := reader.GetAction(t.Context(), ref)
	if err != nil {
		t.Fatalf("resolving %s from the turn entity: %v", ref, err)
	}
	if got.Text != action.Text {
		t.Fatalf("the recovered action reads %q, want %q", got.Text, action.Text)
	}
	if got.Channel.ReplyTo != action.Channel.ReplyTo {
		t.Fatalf("the recovered action's reply address is %q, want %q; the egress adapter resolves the "+
			"player's delivery target from it", got.Channel.ReplyTo, action.Channel.ReplyTo)
	}
	if !got.ArrivedAt.Equal(action.ArrivedAt) {
		t.Fatalf("the recovered action arrived at %v, want %v; deadline evaluation uses arrival time and "+
			"never processing time", got.ArrivedAt, action.ArrivedAt)
	}
}

// The applier classifies, the recorder records, and this is the seam between
// them. It is exercised end to end against a real graph because the alternative
// — each side tested against its own idea of the other — is exactly how a
// closed-code contract turns into a free-text one.
func TestIntegration_AnApplierRejectionFailsTheTurnWithTheCodeTheApplierClassified(t *testing.T) {
	live := startTurns(t)
	acceptance := live.accept(t, "act-reject")
	live.advance(t, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)

	// A batch naming an entity that does not exist in this namespace: the
	// applier refuses it, and the refusal carries a closed code.
	applier, err := effect.NewApplier(live.store)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	missing, err := vocabulary.ComposeEntityID(testOrg, live.namespace, testTemplate, "character", "nobody")
	if err != nil {
		t.Fatalf("compose target: %v", err)
	}
	batch := payload.NewEffectBatch(acceptance.TurnID, vocabulary.BandAuto, []payload.EffectIntent{{
		Type:   vocabulary.EffectSetStatus,
		Target: missing,
		Status: vocabulary.StatusWounded,
	}})

	_, applyErr := applier.Apply(t.Context(), batch, acceptance.TurnEntityID, "obj://batches/act-reject")
	if applyErr == nil {
		t.Fatal("the applier committed a batch naming an entity that does not exist")
	}

	reason, ok := effect.FailureReasonFor(applyErr)
	if !ok {
		t.Fatalf("the applier's rejection carries no closed failure code: %v", applyErr)
	}
	if _, err := live.recorder.Fail(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, reason, content.Ref{}); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	state := live.harness.AwaitEntity(t, acceptance.TurnEntityID)
	if got := testinfra.FirstObject(state, vocabulary.TurnFailureReason.String()); got != string(reason) {
		t.Fatalf("the turn records reason %v, the applier classified %q", got, reason)
	}
	if got := testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String()); got !=
		string(vocabulary.PhaseFailed) {
		t.Fatalf("a rejected batch left the turn in phase %v", got)
	}
}

// FailureReasonFor returning false is a real answer, not a gap: the caller
// escalates or retries rather than burning the player's turn. This pins the
// wiring so a future caller cannot quietly turn it into a default.
func TestIntegration_AnUnclassifiedApplierFailureDoesNotFailTheTurn(t *testing.T) {
	live := startTurns(t)
	acceptance := live.accept(t, "act-anomaly")
	live.advance(t, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)

	applier, err := effect.NewApplier(live.store)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	// A turn entity that does not exist: reading the turn record fails, which is
	// a turn-record anomaly rather than a game outcome.
	absent, err := vocabulary.ComposeEntityID(testOrg, live.namespace, testTemplate, "turn", "turn-act-ghost")
	if err != nil {
		t.Fatalf("compose turn id: %v", err)
	}
	batch := payload.NewEffectBatch("turn-act-ghost", vocabulary.BandAuto, nil)

	_, applyErr := applier.Apply(t.Context(), batch, absent, "obj://batches/act-ghost")
	if applyErr == nil {
		t.Fatal("the applier accepted a turn entity that does not exist")
	}
	if reason, ok := effect.FailureReasonFor(applyErr); ok {
		t.Fatalf("a turn-record anomaly was classified as failure reason %q; recording it would state a "+
			"game outcome on evidence that the paperwork is unreadable", reason)
	}

	// The real turn is untouched: no phase was burned for an anomaly on another
	// entity.
	phases := live.phaseValues(t, acceptance.TurnEntityID)
	if len(phases) != 1 || phases[0] != string(vocabulary.PhaseApplying) {
		t.Fatalf("the turn is in %v after an unclassified failure elsewhere", phases)
	}
}

// A referential stub is queryable and factless, so "no phase recorded" read off
// one would be a false negative — and this component's answer decides whether a
// stage runs. Reachable only against real graph-ingest, which is what mints
// stubs.
func TestIntegration_AStubAtATurnsKeyIsRefusedRatherThanReadAsAPhaselessTurn(t *testing.T) {
	live := startTurns(t)

	// Referencing a turn id from another entity is what mints a stub at that
	// key. Nothing in the engine does this — turn references point outward — so
	// this is the shape of an anomaly, deliberately provoked.
	stubbedTurn, err := vocabulary.ComposeEntityID(testOrg, live.namespace, testTemplate, "turn", "turn-act-stub")
	if err != nil {
		t.Fatalf("compose turn id: %v", err)
	}
	referrer, err := vocabulary.ComposeEntityID(testOrg, live.namespace, testTemplate, "character", "rook")
	if err != nil {
		t.Fatalf("compose referrer id: %v", err)
	}
	if _, err := live.store.CreateEntity(t.Context(), &graph.EntityState{
		ID:          referrer,
		MessageType: turn.EntityMessageType,
		Version:     1,
		UpdatedAt:   time.Now().UTC(),
		Triples: []message.Triple{{
			Subject:    referrer,
			Predicate:  vocabulary.WorldRelationKnows.String(),
			Object:     stubbedTurn,
			Source:     "integration-test",
			Timestamp:  time.Now().UTC(),
			Confidence: 1.0,
		}},
	}); err != nil {
		t.Fatalf("create referrer: %v", err)
	}

	// Wait for the stub to exist at all — it is created by the reference, not by
	// its own message.
	deadline := time.Now().Add(20 * time.Second)
	var stub *graph.EntityState
	for time.Now().Before(deadline) {
		state, err := live.harness.QueryEntity(t.Context(), stubbedTurn)
		if err == nil {
			stub = state
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if stub == nil {
		t.Fatal("no stub was ever minted at the referenced turn key; this test proves nothing")
	}
	if !stub.IsStub() {
		t.Fatalf("the referenced turn key holds a real entity, not a stub: %+v", stub.MessageType)
	}

	if _, err := live.recorder.Current(t.Context(), stubbedTurn); err == nil {
		t.Fatal("a referential stub was read as a turn phase")
	}
}

// The compiler cannot check that the intake's stream constants describe a stream
// that exists, so the defaults are pinned here against a real broker: an ingress
// adapter publishing on the canonical subject must land on the canonical stream.
func TestIntegration_TheDefaultStreamCoordinatesAgree(t *testing.T) {
	harness := testinfra.Require(t)

	stream, err := harness.Client.EnsureStream(t.Context(), jetstream.StreamConfig{
		Name:      turn.ActionStream,
		Subjects:  []string{turn.ActionSubjectPrefix},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
	})
	if err != nil {
		t.Fatalf("EnsureStream %s: %v", turn.ActionStream, err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}

	matched := false
	for _, subject := range info.Config.Subjects {
		if subjectMatches(subject, turn.ActionSubject) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("the canonical publish subject %q does not fall under the stream's subjects %v; every "+
			"published action would be rejected by the broker", turn.ActionSubject, info.Config.Subjects)
	}
}

// subjectMatches reports whether a published subject falls under a stream
// subject filter, for the two wildcard forms NATS defines.
func subjectMatches(filter, subject string) bool {
	if filter == subject {
		return true
	}
	if len(filter) > 2 && filter[len(filter)-2:] == ".>" {
		prefix := filter[:len(filter)-1]
		return len(subject) > len(prefix) && subject[:len(prefix)] == prefix
	}
	return false
}
