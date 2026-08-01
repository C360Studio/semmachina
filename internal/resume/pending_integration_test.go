package resume_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/resume"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The queued-set read is the fact the whole pass rests on, so it is exercised
// against a real stream and a real durable consumer rather than a fake.
//
// A fake cannot be wrong about the one thing that matters here: the stage stream
// never evicts, so it holds every trigger the world has ever published, and
// "what is queued" is a property of a CONSUMER rather than of the subject. A read
// that ignored that would report every turn in the campaign's history as waiting.
type pendingWorld struct {
	client       *natsclient.Client
	stream       jetstream.Stream
	agent        jetstream.Stream
	prefix       string
	namespace    string
	consumerName string
}

func startPendingWorld(t *testing.T) *pendingWorld {
	t.Helper()
	harness := testinfra.Require(t)
	stream := harness.EnsureArchivalStream(t, rulepack.StageStream,
		[]string{rulepack.StageSubjectFilter}, "")
	agent, err := harness.Client.EnsureStream(t.Context(), jetstream.StreamConfig{
		Name:      persona.TaskStream,
		Subjects:  []string{persona.AgentSubjectFilter},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		MaxBytes:  1 << 30,
		Discard:   jetstream.DiscardOld,
	})
	if err != nil {
		t.Fatalf("ensure the agent stream: %v", err)
	}
	namespace := fmt.Sprintf("q%d", worldCounter.Add(1))
	// A KNOWN starting state on a shared broker. A durable consumer outlives the
	// test that made it, and one left on agent.task.* with an untouched ack floor
	// drags the MINIMUM across consumers back to zero — so the next test reads
	// every task ever published as still queued, and the test that wants NO
	// consumer can never have one. Both failures were seen before this existed.
	for info := range agent.ListConsumers(t.Context()).Info() {
		if info == nil || !declaresTaskFilter(info.Config) {
			continue
		}
		if err := agent.DeleteConsumer(t.Context(), info.Name); err != nil {
			t.Fatalf("clear the leftover task consumer %s: %v", info.Name, err)
		}
	}
	return &pendingWorld{
		client:    harness.Client,
		stream:    stream,
		agent:     agent,
		namespace: namespace,
		prefix:    fmt.Sprintf("c360.semmachina.%s.starter.turn.", namespace),
	}
}

// declaresTaskFilter mirrors what the production read matches, in BOTH filter
// shapes, so the sweep cannot leave behind a consumer the read would find.
func declaresTaskFilter(cfg jetstream.ConsumerConfig) bool {
	if cfg.FilterSubject == persona.TaskSubjectFilter {
		return true
	}
	for _, subject := range cfg.FilterSubjects {
		if subject == persona.TaskSubjectFilter {
			return true
		}
	}
	return false
}

// loopConsumer stands in for the agentic loop's task consumer, under a name that
// deliberately does NOT match upstream's.
//
// That mismatch is the point. The pass finds this consumer by its FILTER SUBJECT,
// because upstream's name is an unexported sanitisation plus an operator suffix
// (semstreams#733) — so a test that named it upstream's way would be proving the
// wrong mechanism works.
func (w *pendingWorld) loopConsumer(t *testing.T) {
	t.Helper()
	if w.consumerName == "" {
		w.consumerName = "not-the-upstream-name-" + w.namespace
	}
	if _, err := w.agent.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Durable:       w.consumerName,
		FilterSubject: persona.TaskSubjectFilter,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
	}); err != nil {
		t.Fatalf("create the stand-in loop consumer: %v", err)
	}
	// DELETED on the way out, and it matters more than it looks. These tests
	// share one broker and therefore one AGENT stream, and a durable consumer
	// outlives the test that made it: a leftover one with an untouched ack floor
	// drags the MINIMUM across consumers back to zero, so the next test reads
	// every task ever published as still queued — and a test that wants NO
	// consumer at all can never have one.
	t.Cleanup(func() {
		_ = w.agent.DeleteConsumer(context.Background(), w.consumerName)
	})
}

// publishTask puts one persona task on AGENT, in the envelope the spawner
// publishes and with the identity metadata it stamps.
func (w *pendingWorld) publishTask(t *testing.T, turnID string) string {
	t.Helper()
	entityID := w.prefix + turnID
	body, err := json.Marshal(map[string]any{
		"payload": map[string]any{
			"metadata": map[string]any{persona.MetadataKeyTurnEntityID: entityID},
		},
	})
	if err != nil {
		t.Fatalf("encode task: %v", err)
	}
	subject := persona.TaskSubjectFor(persona.RoleAdjudicator)
	if err := w.client.PublishToStream(t.Context(), subject, body); err != nil {
		t.Fatalf("publish task: %v", err)
	}
	return entityID
}

// publish puts one stage trigger on the stream, in the rule engine's own shape.
func (w *pendingWorld) publish(t *testing.T, phase vocabulary.TurnPhase, turnID string) string {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	entityID := w.prefix + turnID
	body, err := json.Marshal(map[string]any{
		"entity_id": entityID, "subject": subject, "source": "rule_engine",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := w.client.PublishToStream(t.Context(), subject, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return entityID
}

func (w *pendingWorld) publishKnowledge(t *testing.T, turnID string) string {
	return w.publishAuxiliary(t, turnID, rulepack.SubjectKnowledge, "knowledge")
}

func (w *pendingWorld) publishAccusation(t *testing.T, turnID string) string {
	return w.publishAuxiliary(t, turnID, rulepack.SubjectAccusation, "accusation")
}

func (w *pendingWorld) publishAuxiliary(t *testing.T, turnID, subject, label string) string {
	t.Helper()
	entityID := w.prefix + turnID
	body, err := json.Marshal(map[string]any{
		"entity_id": entityID, "subject": subject, "source": "rule_engine",
	})
	if err != nil {
		t.Fatalf("encode %s trigger: %v", label, err)
	}
	if err := w.client.PublishToStream(t.Context(), subject, body); err != nil {
		t.Fatalf("publish %s trigger: %v", label, err)
	}
	return entityID
}

// consume binds the REAL stage consumer name and acknowledges want messages,
// which is what a stage that ran leaves behind.
func (w *pendingWorld) consume(t *testing.T, phase vocabulary.TurnPhase, want int) {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	got := make(chan struct{}, want+4)
	if err := w.client.ConsumeDurable(context.Background(), natsclient.StreamConsumerConfig{
		StreamName:    rulepack.StageStream,
		ConsumerName:  rulepack.StageConsumerName(phase),
		FilterSubject: subject,
		DeliverPolicy: "all",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       20 * time.Second,
	}, 5*time.Second, func(context.Context, []byte) error {
		select {
		case got <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("bind the %s consumer: %v", phase, err)
	}
	defer w.client.StopAllConsumers()

	for i := 0; i < want; i++ {
		select {
		case <-got:
		case <-time.After(30 * time.Second):
			t.Fatalf("the %s consumer received %d of %d triggers", phase, i, want)
		}
	}
	// The handler returning is not the acknowledgement; wait for the floor to
	// actually move, or the read under test races the ack.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		consumer, err := w.stream.Consumer(t.Context(), rulepack.StageConsumerName(phase))
		if err != nil {
			t.Fatalf("read the %s consumer: %v", phase, err)
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the %s consumer info: %v", phase, err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the %s consumer never settled", phase)
}

func (w *pendingWorld) consumeKnowledge(t *testing.T, want int) {
	w.consumeAuxiliary(t, rulepack.KnowledgeConsumerName, rulepack.SubjectKnowledge, "knowledge", want)
}

func (w *pendingWorld) consumeAccusation(t *testing.T, want int) {
	w.consumeAuxiliary(t, rulepack.AccusationConsumerName, rulepack.SubjectAccusation, "accusation", want)
}

func (w *pendingWorld) consumeAuxiliary(t *testing.T, consumerName, subject, label string, want int) {
	t.Helper()
	got := make(chan struct{}, want+4)
	if err := w.client.ConsumeDurable(context.Background(), natsclient.StreamConsumerConfig{
		StreamName: rulepack.StageStream, ConsumerName: consumerName,
		FilterSubject: subject, DeliverPolicy: "all", AckPolicy: "explicit",
		MaxDeliver: 0, AckWait: 20 * time.Second,
	}, 5*time.Second, func(context.Context, []byte) error {
		select {
		case got <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("bind the %s consumer: %v", label, err)
	}
	defer w.client.StopAllConsumers()

	for i := 0; i < want; i++ {
		select {
		case <-got:
		case <-time.After(30 * time.Second):
			t.Fatalf("the %s consumer received %d of %d triggers", label, i, want)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		consumer, err := w.stream.Consumer(t.Context(), consumerName)
		if err != nil {
			t.Fatalf("read the %s consumer: %v", label, err)
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the %s consumer info: %v", label, err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the %s consumer never settled", label)
}

// consumeTasks binds the stand-in loop consumer and acknowledges want tasks.
func (w *pendingWorld) consumeTasks(t *testing.T, want int) {
	t.Helper()
	got := make(chan struct{}, want+4)
	if err := w.client.ConsumeDurable(context.Background(), natsclient.StreamConsumerConfig{
		StreamName:    persona.TaskStream,
		ConsumerName:  w.consumerName,
		FilterSubject: persona.TaskSubjectFilter,
		DeliverPolicy: "all",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       20 * time.Second,
	}, 5*time.Second, func(context.Context, []byte) error {
		select {
		case got <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("bind the stand-in loop consumer: %v", err)
	}
	defer w.client.StopAllConsumers()

	for i := 0; i < want; i++ {
		select {
		case <-got:
		case <-time.After(30 * time.Second):
			t.Fatalf("the stand-in loop received %d of %d tasks", i, want)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		consumer, err := w.agent.Consumer(t.Context(), w.consumerName)
		if err != nil {
			t.Fatalf("read the stand-in loop consumer: %v", err)
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the stand-in loop consumer info: %v", err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the stand-in loop consumer never settled")
}

func (w *pendingWorld) view(t *testing.T) *resume.WorkQueues {
	t.Helper()
	w.loopConsumer(t)
	view, err := resume.NewWorkQueues(w.stream, w.agent,
		resume.WithSettleWindow(500*time.Millisecond, 30*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	return view
}

// The read must report a trigger nobody has consumed, and must NOT report one a
// stage has finished with.
//
// The second half is the one a fake would miss and the one that was wrong on
// first writing: the read begins at the consumer's acknowledgement FLOOR, which
// is the last sequence acknowledged rather than the first unacknowledged one, so
// starting at it rather than after it re-reads exactly one finished trigger per
// subject. One is enough — it is always the most recently completed turn, which
// is the turn most likely to be the one waiting.
func TestWorkQueues_ReportsOnlyWhatNoStageHasFinished(t *testing.T) {
	world := startPendingWorld(t)

	// Order matters, and it is the only way to get a durable consumer to hold a
	// floor between two messages: publish and fully consume the first, THEN
	// publish the second with nothing bound. A consumer with DeliverPolicy "all"
	// takes everything on the stream, so publishing both first would leave the
	// floor past both of them.
	done := world.publish(t, vocabulary.PhaseAdjudicating, "turn-done")
	world.consume(t, vocabulary.PhaseAdjudicating, 1)
	waiting := world.publish(t, vocabulary.PhaseAdjudicating, "turn-waiting")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	if pending[waiting] != 1 {
		t.Errorf("the unconsumed trigger for %s is reported %d times, want 1; a turn missing from this set is a "+
			"turn the pass re-triggers on top of work already coming to it", waiting, pending[waiting])
	}
	if got := pending[done]; got != 0 {
		t.Errorf("a trigger a stage already finished with is reported %d times for %s; every turn the campaign "+
			"has ever run would read as waiting, and the pass would never resume anything", got, done)
	}
}

func TestWorkQueues_ReportsOnlyKnowledgeNoGranterHasFinished(t *testing.T) {
	world := startPendingWorld(t)

	done := world.publishKnowledge(t, "knowledge-done")
	world.consumeKnowledge(t, 1)
	waiting := world.publishKnowledge(t, "knowledge-waiting")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending[waiting] != 1 {
		t.Errorf("unacknowledged knowledge trigger for %s reported %d times, want 1", waiting, pending[waiting])
	}
	if pending[done] != 0 {
		t.Errorf("acknowledged knowledge trigger for %s reported %d times", done, pending[done])
	}
}

func TestWorkQueues_AccusationAckFloorCoversAbsentUnackedAckedAndCombined(t *testing.T) {
	world := startPendingWorld(t)
	_ = world.stream.DeleteConsumer(t.Context(), rulepack.AccusationConsumerName)
	absent := world.publishAccusation(t, "accusation-absent")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle absent: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil || pending[absent] != 1 {
		t.Fatalf("absent accusation consumer pending=%v err=%v", pending[absent], err)
	}

	world.consumeAccusation(t, 1)
	unacked := world.publishAccusation(t, "accusation-unacked")
	combined := world.publishAccusation(t, "accusation-combined")
	world.publishKnowledge(t, "accusation-combined")
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle queued: %v", err)
	}
	pending, err = view.Pending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if pending[absent] != 0 || pending[unacked] != 1 || pending[combined] != 2 {
		t.Fatalf("accusation ack accounting: absent=%d unacked=%d combined=%d",
			pending[absent], pending[unacked], pending[combined])
	}
}

// Nothing consumed at all is not an empty answer: everything on the subject is
// queued, and a consumer that does not exist yet must read that way rather than
// silently reporting nothing.
func TestWorkQueues_ReportsEverythingWhenNoStageHasEverConsumed(t *testing.T) {
	world := startPendingWorld(t)

	first := world.publish(t, vocabulary.PhaseNarrating, "turn-a")
	second := world.publish(t, vocabulary.PhaseNarrating, "turn-b")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	for _, entityID := range []string{first, second} {
		if pending[entityID] != 1 {
			t.Errorf("turn %s is reported %d times with no consumer bound, want 1", entityID, pending[entityID])
		}
	}
}

// Counting must not consume the work it counts.
//
// The property is that the read binds an EPHEMERAL consumer with its own cursor,
// never the stage's durable one — a reader that took the obvious shortcut and
// bound `semmachina-stage-applying` would advance the very floor it was reading
// and eat the trigger. Repeated counting must leave the stage's delivery intact,
// which is what the consume below proves: without it, this test would only be
// asserting that a map lookup returns the same number twice.
func TestWorkQueues_CountingLeavesTheStagesOwnDeliveryIntact(t *testing.T) {
	world := startPendingWorld(t)
	entityID := world.publish(t, vocabulary.PhaseApplying, "turn-untouched")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	for i := 0; i < 3; i++ {
		pending, err := view.Pending(t.Context())
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if pending[entityID] != 1 {
			t.Fatalf("read %d reports %s %d times, want 1; counting moved the floor it was reading",
				i+1, entityID, pending[entityID])
		}
	}

	// And the stage still receives it. A read that bound the stage's own durable
	// consumer would have acknowledged this trigger three times over, and this
	// would hang.
	world.consume(t, vocabulary.PhaseApplying, 1)
}

// The settle wait is what stands in for a bootstrap-replay completion signal
// upstream does not expose. A stream still being published to must refuse rather
// than hand back a set that is a sample of something in flight.
func TestWorkQueues_RefusesAStreamThatWillNotHoldStill(t *testing.T) {
	world := startPendingWorld(t)

	world.loopConsumer(t)
	view, err := resume.NewWorkQueues(world.stream, world.agent,
		resume.WithSettleWindow(2*time.Second, 3*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}

	// The noise runs on a background context and reports nothing: a t.Fatalf from
	// a goroutine the test does not own turns a teardown race into a failure
	// about the wrong thing.
	subject, err := rulepack.SubjectForPhase(vocabulary.PhaseResolving)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
				body, _ := json.Marshal(map[string]any{
					"entity_id": fmt.Sprintf("%sturn-noisy-%d", world.prefix, i),
					"subject":   subject, "source": "rule_engine",
				})
				_ = world.client.PublishToStream(context.Background(), subject, body)
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })

	if err := view.Settle(t.Context()); err == nil {
		t.Fatal("a stream that never stopped moving was read as settled; the queued set would be a snapshot of " +
			"something in flight, and a turn about to receive a trigger would read as stranded")
	}
}

// And it returns promptly once nothing is publishing.
func TestWorkQueues_SettlesOnAQuietStream(t *testing.T) {
	world := startPendingWorld(t)
	world.publish(t, vocabulary.PhaseComplete, "turn-quiet")

	world.loopConsumer(t)
	view, err := resume.NewWorkQueues(world.stream, world.agent,
		resume.WithSettleWindow(500*time.Millisecond, 20*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	started := time.Now()
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle on a quiet stream: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("settling a quiet stream took %s; the pass would be waiting out its own timeout", elapsed)
	}
}

func TestNewWorkQueues_RefusesAnIncoherentSettleWindow(t *testing.T) {
	world := startPendingWorld(t)

	for _, tc := range []struct {
		name           string
		quiet, timeout time.Duration
	}{
		{"no quiet window", 0, time.Second},
		{"no timeout", time.Second, 0},
		{"a quiet window longer than the time allowed to reach it", 10 * time.Second, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resume.NewWorkQueues(world.stream, world.agent,
				resume.WithSettleWindow(tc.quiet, tc.timeout)); err == nil {
				t.Fatal("a settle window that can never be satisfied was accepted")
			}
		})
	}
}

// The real-infrastructure tests in this package need the shared harness, and the
// per-world counter keeps their entity IDs disjoint on one broker.
var worldCounter atomic.Int64

func TestMain(m *testing.M) {
	os.Exit(testinfra.RunTests(m))
}

// THE UNION. A persona task the loop has not acknowledged is work queued for that
// turn, and a pass that measured only the stage stream would miss it entirely.
//
// This is the shape T1 named: Spawner.Run acks the stage trigger when it
// PUBLISHES the task, so a turn mid-adjudication has nothing on TURN_STAGES and
// an unacked task on AGENT that upstream will redeliver.
func TestWorkQueues_CountAnUnacknowledgedPersonaTask(t *testing.T) {
	world := startPendingWorld(t)
	entityID := world.publishTask(t, "turn-adjudicating")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending[entityID] != 1 {
		t.Fatalf("turn %s is reported %d times, want 1; its adjudication is unacknowledged on %s and a pass "+
			"that missed it would re-trigger the stage into a second billed spawn",
			entityID, pending[entityID], persona.TaskStream)
	}
}

// And the union really is a union: one turn with work on BOTH queues is counted
// from both, and neither queue alone would answer for the other's turns.
func TestWorkQueues_SumAcrossBothQueues(t *testing.T) {
	world := startPendingWorld(t)

	bothID := world.publishTask(t, "turn-both")
	world.publish(t, vocabulary.PhaseNarrating, "turn-both")
	taskOnly := world.publishTask(t, "turn-task-only")
	triggerOnly := world.publish(t, vocabulary.PhaseApplying, "turn-trigger-only")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	if pending[bothID] != 2 {
		t.Errorf("turn %s is reported %d times, want 2; the set sums across queues so a turn can be left alone "+
			"without the pass knowing WHICH work it is owed", bothID, pending[bothID])
	}
	if pending[taskOnly] != 1 {
		t.Errorf("turn %s is reported %d times, want 1", taskOnly, pending[taskOnly])
	}
	if pending[triggerOnly] != 1 {
		t.Errorf("turn %s is reported %d times, want 1", triggerOnly, pending[triggerOnly])
	}
}

// A task the loop has FINISHED is not queued, which is what makes the count a
// measure of work outstanding rather than of work ever done.
func TestWorkQueues_DoNotCountAFinishedPersonaTask(t *testing.T) {
	world := startPendingWorld(t)
	world.loopConsumer(t)

	done := world.publishTask(t, "turn-finished")
	world.consumeTasks(t, 1)
	waiting := world.publishTask(t, "turn-still-going")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending[done] != 0 {
		t.Errorf("a task the loop finished is reported %d times for %s; every turn the campaign has ever "+
			"adjudicated would read as in flight", pending[done], done)
	}
	if pending[waiting] != 1 {
		t.Errorf("the unfinished task for %s is reported %d times, want 1", waiting, pending[waiting])
	}
}

// NO consumer on agent.task.* must REFUSE, not read as an empty queue.
//
// Nothing bound to that subject means no persona can ever run — upstream's task
// consumers deliver only NEW messages, so a task published while none exists is
// never delivered at all. Answering "nothing queued" would be the silent inverse
// of the fact, and would re-trigger every turn whose persona is mid-flight.
//
// This is also the failure mode a reconstructed CONSUMER NAME would produce on
// every drift: ErrConsumerNotFound is indistinguishable from "no agentic loop
// here". Finding the consumer by filter subject is what avoids it; refusing when
// none is found is what keeps the remaining case loud.
func TestWorkQueues_RefuseWhenNothingConsumesPersonaTasks(t *testing.T) {
	world := startPendingWorld(t)
	world.publishTask(t, "turn-orphan")

	// Deliberately NOT calling loopConsumer.
	view, err := resume.NewWorkQueues(world.stream, world.agent,
		resume.WithSettleWindow(500*time.Millisecond, 20*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if _, err := view.Pending(t.Context()); err == nil {
		t.Fatal("a deployment with nothing consuming persona tasks read as an empty queue. Every turn whose " +
			"persona is mid-flight would be re-triggered into a second billed spawn, and the reading would be " +
			"the exact inverse of the fact")
	}
}

// An absent STAGE consumer is the opposite call, and the difference is not
// arbitrary: this engine creates the stage consumers itself with DeliverPolicy
// "all", so a trigger published while one was absent IS delivered once it binds.
// Everything on the subject is genuinely queued, and reading from the beginning
// is the honest answer rather than a convenient one.
func TestWorkQueues_CountEverythingWhenNoStageConsumerExists(t *testing.T) {
	world := startPendingWorld(t)
	entityID := world.publish(t, vocabulary.PhaseNarrating, "turn-unconsumed")

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending[entityID] != 1 {
		t.Fatalf("turn %s is reported %d times with no stage consumer bound, want 1", entityID, pending[entityID])
	}
}

// Two consumers on agent.task.* means two loops bound, and work above EITHER
// floor is still owed to somebody. The floor taken is therefore the MINIMUM.
//
// Taking the maximum would read a task one loop has finished as finished
// everywhere, and the turn would be re-triggered while the other loop still had
// it — a second billed spawn racing the first.
func TestWorkQueues_TakeTheLowestFloorWhenTwoLoopsAreBound(t *testing.T) {
	world := startPendingWorld(t)
	world.loopConsumer(t)

	// One consumer consumes and acknowledges the task; a second is bound and has
	// finished nothing, so the same task is still owed to it.
	entityID := world.publishTask(t, "turn-owed-to-one-of-two")
	world.consumeTasks(t, 1)

	idle := "second-loop-" + world.namespace
	if _, err := world.agent.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Durable:       idle,
		FilterSubject: persona.TaskSubjectFilter,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
	}); err != nil {
		t.Fatalf("create the second loop consumer: %v", err)
	}
	t.Cleanup(func() { _ = world.agent.DeleteConsumer(context.Background(), idle) })

	view, err := resume.NewWorkQueues(world.stream, world.agent,
		resume.WithSettleWindow(500*time.Millisecond, 20*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending[entityID] == 0 {
		t.Fatalf("turn %s is reported as having nothing queued, but one of the two bound loops has not finished "+
			"its task; re-triggering it would race a spawn that is still coming", entityID)
	}
}

// A consumer may declare its filter in EITHER shape — FilterSubject or
// FilterSubjects — and they are mutually exclusive on the wire, so which one a
// component chooses is not this engine's business. The plural branch existed
// unexercised, which in the function that decides which cursor to trust is one
// case too few: a read that missed a plural-declared loop consumer would refuse
// to run against a deployment that had one.
func TestWorkQueues_FindTheLoopConsumerDeclaredWithPluralFilters(t *testing.T) {
	world := startPendingWorld(t)

	name := "plural-filter-loop-" + world.namespace
	if _, err := world.agent.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Durable:        name,
		FilterSubjects: []string{persona.TaskSubjectFilter},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckPolicy:      jetstream.AckExplicitPolicy,
	}); err != nil {
		t.Fatalf("create a plural-filter loop consumer: %v", err)
	}
	t.Cleanup(func() { _ = world.agent.DeleteConsumer(context.Background(), name) })

	entityID := world.publishTask(t, "turn-plural")

	view, err := resume.NewWorkQueues(world.stream, world.agent,
		resume.WithSettleWindow(500*time.Millisecond, 20*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v; a loop consumer that declares its filter in the plural shape was not found, so "+
			"a deployment with one would be refused as having no agentic loop at all", err)
	}
	if pending[entityID] != 1 {
		t.Fatalf("turn %s is reported %d times, want 1", entityID, pending[entityID])
	}
}

// The measurement must survive being taken TWICE, and the reason is that a
// crash-restart is exactly two passes in quick succession.
//
// Pending reads each subject through an EPHEMERAL, AckNone consumer carrying the
// task filter, with a thirty-second inactive threshold. Those readers acknowledge
// nothing, so their acknowledgement floor is zero — and a minimum taken over
// EVERY consumer on the filter is therefore zero for half a minute after any
// previous call. Every persona task ever published then reads as still queued, for
// every turn, and the pass leaves every stranded turn exactly where it found it.
//
// The failing window is the one the pass exists for: a process that dies and comes
// back inside thirty seconds runs against the floor its own previous run left
// behind. So the second reading below must equal the first, and the assertion is
// the finished task staying finished rather than merely the call succeeding.
func TestWorkQueues_AreNotDisarmedByTheirOwnPreviousReading(t *testing.T) {
	world := startPendingWorld(t)
	world.loopConsumer(t)

	// Pass one, over a queue where the loop has finished everything. It leaves an
	// ephemeral reader behind whose cursor sits at the floor it read.
	settled := world.publishTask(t, "turn-finished-before-the-first-pass")
	world.consumeTasks(t, 1)

	view := world.view(t)
	if err := view.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	first, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("first Pending: %v", err)
	}
	if first[settled] != 0 {
		t.Fatalf("the first reading already reports a finished task as queued (%s=%d); this test's premise "+
			"is that it does not", settled, first[settled])
	}

	// Work happens and finishes: the loop's own floor MOVES PAST the stale reader.
	done := world.publishTask(t, "turn-finished-between-the-two-passes")
	world.consumeTasks(t, 1)

	// Pass two, inside the ephemeral readers' thirty-second inactive threshold —
	// which is exactly what a process that died and came back does.
	second, err := view.Pending(t.Context())
	if err != nil {
		t.Fatalf("second Pending: %v", err)
	}
	if second[done] != 0 {
		t.Errorf("a task the loop FINISHED is reported %d times for %s on the second reading. The first "+
			"reading's own ephemeral, AckNone cursor is still on the stream at the older floor, and the "+
			"minimum is taken over every consumer on the filter — so a pass that runs within thirty seconds "+
			"of another reads finished work as in flight, calls every such turn 'queued', and rescues none of "+
			"the stranded ones. A crash-restart is precisely two passes in quick succession",
			second[done], done)
	}
}
