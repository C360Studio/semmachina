package stage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/processor/rule"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/resume"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// parkedWorld is a turn engine with the RULE PROCESSOR and nothing else: real
// graph-ingest, the real pack, the real stage stream — and no stage runners at
// all.
//
// The omission is the instrument. With stages bound, every trigger is consumed
// and every parked turn walks on, which is exactly the state these tests need to
// hold still. Without them the stage stream becomes a LEDGER of triggers: the
// stream is limits-retained, so counting messages per subject answers "was this
// hop published?" independently of whether anybody acted on it.
type parkedWorld struct {
	harness    *testinfra.Harness
	graph      *graphio.Store
	content    *content.Store
	recorder   *turn.Recorder
	stream     jetstream.Stream
	agent      jetstream.Stream
	namespace  string
	campaignID string

	// stopRules stops the ONE rule processor this world runs.
	//
	// One, and holding the handle is what makes that true. An earlier version of
	// this file "restarted" the processor by starting a second and stopping it,
	// leaving the first running the whole time — so nothing was ever offline, a
	// write made during the supposed downtime was processed live, and the rule
	// state it left behind was the state a real restart would never have seen.
	// Every claim about what a BOOT does needs the boot to have happened.
	stopRules func()
}

func startParkedWorld(t *testing.T) *parkedWorld {
	t.Helper()
	harness := testinfra.Require(t)
	namespace := nextTestNamespace("p")

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	backend, err := content.NewObjectStore(t.Context(), harness.Client, content.WithBucket("PARKED_"+namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}
	recorder, err := turn.NewRecorder(store, artifacts,
		turn.Identity{Org: testOrg, WorldNS: namespace, Template: testTemplate})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	stream := harness.EnsureArchivalStream(t, rulepack.StageStream,
		[]string{rulepack.StageSubjectFilter}, "")
	agent, err := harness.Client.EnsureStream(t.Context(), stage.AgentStreamConfig())
	if err != nil {
		t.Fatalf("ensure the agent stream: %v", err)
	}
	// A durable consumer on agent.task.*, standing in for the agentic loop's.
	//
	// It binds no handler and consumes nothing, which is a deployment whose loop
	// is present and idle. It has to EXIST because the boot pass refuses to run
	// without one: nothing bound to that subject means no persona can ever run,
	// and answering "nothing queued" for it would re-trigger every turn whose
	// persona is mid-flight. The pass finds it by FILTER SUBJECT rather than by
	// name, which is why this stand-in need not reproduce upstream's naming.
	clearTaskConsumers(t, agent)
	if _, err := agent.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Durable:       "test-idle-loop-" + namespace,
		FilterSubject: persona.TaskSubjectFilter,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
	}); err != nil {
		t.Fatalf("create the stand-in task consumer: %v", err)
	}
	t.Cleanup(func() {
		_ = agent.DeleteConsumer(context.Background(), "test-idle-loop-"+namespace)
	})

	world := &parkedWorld{
		harness: harness, graph: store, content: artifacts, recorder: recorder,
		stream: stream, agent: agent, namespace: namespace,
		campaignID: composeID(t, namespace, "campaign", "instance"),
	}
	world.startRules(t)
	t.Cleanup(func() { world.drainStageTriggers(t) })
	return world
}

// clearTaskConsumers gives the AGENT stream a KNOWN consumer set.
//
// A durable consumer outlives the test that made it, and the boot pass takes the
// MINIMUM acknowledgement floor across every consumer on agent.task.* — so one
// leftover with an untouched floor makes every task ever published read as still
// in flight, and every turn read as queued. That is not a quirk of the test
// harness: taking the minimum is deliberate, because work above ANY consumer's
// floor is still owed to somebody. A real deployment has one loop consumer; a
// shared broker has to be told.
func clearTaskConsumers(t *testing.T, agent jetstream.Stream) {
	t.Helper()
	for info := range agent.ListConsumers(t.Context()).Info() {
		if info == nil || info.Config.FilterSubject != persona.TaskSubjectFilter {
			continue
		}
		if err := agent.DeleteConsumer(t.Context(), info.Name); err != nil {
			t.Fatalf("clear the leftover task consumer %s: %v", info.Name, err)
		}
	}
}

// restartRules stops this world's rule processor and starts a fresh one, which
// is a boot in every way the entity watcher can tell.
func (w *parkedWorld) restartRules(t *testing.T) {
	t.Helper()
	w.stopRules()
	// The watcher's own teardown is asynchronous; without a beat the new
	// processor can bind its KV watch while the old one still holds it.
	time.Sleep(500 * time.Millisecond)
	w.startRules(t)
}

// drainStageTriggers acknowledges everything this world published, on the way
// out.
//
// It is housekeeping for a SHARED broker rather than anything about the engine.
// The stage consumers are the engine's — one durable per phase, named for the
// phase and not for the world — so triggers this world leaves unconsumed become
// the next test's backlog, and a stage runner handed a turn from a world with no
// scene in it naks forever. The tests that assert "every trigger acknowledged"
// would then be reporting this file's litter.
//
// It binds the REAL runner with no-op stages, so the consumers it advances are
// configured exactly as production configures them; a hand-written consumer
// config here would be a second copy that could drift into creating a different
// consumer beside the real one.
func (w *parkedWorld) drainStageTriggers(t *testing.T) {
	t.Helper()
	stages := make([]stage.Stage, 0, len(rulepack.StagePhases()))
	for _, phase := range rulepack.StagePhases() {
		stages = append(stages, drainStage{phase: phase})
	}
	runner, err := stage.NewRunner(w.harness.Client, w.harness.Client, stages,
		stage.WithAckTimings(20*time.Second, 5*time.Second))
	if err != nil {
		t.Fatalf("build the draining runner: %v", err)
	}
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("start the draining runner: %v", err)
	}
	if err := w.harness.Client.ConsumeDurable(context.Background(), natsclient.StreamConsumerConfig{
		StreamName: rulepack.StageStream, ConsumerName: rulepack.KnowledgeConsumerName,
		FilterSubject: rulepack.SubjectKnowledge, DeliverPolicy: "all", AckPolicy: "explicit",
		MaxDeliver: 0, AckWait: 20 * time.Second,
	}, 5*time.Second, func(context.Context, []byte) error { return nil }); err != nil {
		t.Fatalf("start the draining knowledge consumer: %v", err)
	}
	defer w.harness.Client.StopAllConsumers()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if w.stageConsumersIdle(t) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("this world's stage triggers were still pending after 30s; the next test would inherit them")
}

// stageConsumersIdle reads the engine's stage consumers on a background context.
//
// Not t.Context(): this runs from a Cleanup, and by then the test's context is
// already cancelled — every read would fail with "context canceled" and report
// litter that is not there.
func (w *parkedWorld) stageConsumersIdle(t *testing.T) bool {
	t.Helper()
	ctx := context.Background()
	for _, phase := range rulepack.StagePhases() {
		consumer, err := w.stream.Consumer(ctx, rulepack.StageConsumerName(phase))
		if err != nil {
			t.Fatalf("read the %s consumer: %v", phase, err)
		}
		info, err := consumer.Info(ctx)
		if err != nil {
			t.Fatalf("read the %s consumer info: %v", phase, err)
		}
		if info.NumPending != 0 || info.NumAckPending != 0 {
			return false
		}
	}
	consumer, err := w.stream.Consumer(ctx, rulepack.KnowledgeConsumerName)
	if err != nil {
		t.Fatalf("read the knowledge consumer: %v", err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		t.Fatalf("read the knowledge consumer info: %v", err)
	}
	if info.NumPending != 0 || info.NumAckPending != 0 {
		return false
	}
	return true
}

// drainStage owns a phase and does nothing with it, which is what makes the
// trigger acknowledged.
type drainStage struct{ phase vocabulary.TurnPhase }

func (d drainStage) Phase() vocabulary.TurnPhase            { return d.phase }
func (drainStage) Run(context.Context, stage.Trigger) error { return nil }

// startRules boots the REAL rule processor as this world's only one.
func (w *parkedWorld) startRules(t *testing.T) {
	t.Helper()
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatalf("ProcessorConfig: %v", err)
	}
	created, err := rule.CreateRuleProcessor(raw, component.Dependencies{
		NATSClient:      w.harness.Client,
		PayloadRegistry: w.harness.Registry,
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("CreateRuleProcessor: %v", err)
	}
	processor, ok := created.(component.LifecycleComponent)
	if !ok {
		t.Fatalf("rule processor is a %T, not a LifecycleComponent", created)
	}
	if err := processor.Initialize(); err != nil {
		t.Fatalf("initialize rule processor: %v", err)
	}
	if err := processor.Start(context.Background()); err != nil {
		t.Fatalf("start rule processor: %v", err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = processor.Stop(5 * time.Second)
		}
	}
	w.stopRules = stop
	t.Cleanup(stop)
}

// triggers counts the stage triggers stored on one subject that name ONE turn.
//
// Per turn rather than per subject, because the stage subjects are the engine's
// and not this world's: every test in this package shares one broker and
// therefore one TURN_STAGES stream, so a bare per-subject count is a count of
// everybody's triggers and a straggler from another world reads as a rescue
// here. Filtering by the entity the trigger names is what makes these
// assertions about this turn.
//
// The consumer is EPHEMERAL and acknowledges nothing, so counting never removes
// a trigger a stage would otherwise receive.
func (w *parkedWorld) triggers(t *testing.T, subject, entityID string) int {
	t.Helper()
	consumer, err := w.stream.CreateConsumer(t.Context(), jetstream.ConsumerConfig{
		FilterSubject:     subject,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("create counting consumer on %s: %v", subject, err)
	}
	batch, err := consumer.Fetch(1000, jetstream.FetchMaxWait(500*time.Millisecond))
	if err != nil {
		t.Fatalf("fetch %s: %v", subject, err)
	}
	count := 0
	for msg := range batch.Messages() {
		var published struct {
			EntityID string `json:"entity_id"`
		}
		if err := json.Unmarshal(msg.Data(), &published); err != nil {
			t.Fatalf("decode a stage trigger on %s: %v", subject, err)
		}
		if published.EntityID == entityID {
			count++
		}
	}
	if err := batch.Error(); err != nil {
		t.Fatalf("fetch %s: %v", subject, err)
	}
	return count
}

func (w *parkedWorld) awaitTriggers(t *testing.T, subject, entityID string, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		last = w.triggers(t, subject, entityID)
		if last >= want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("turn %s holds %d triggers on %s, want at least %d", entityID, last, subject, want)
}

// accept creates a turn exactly as intake does.
func (w *parkedWorld) accept(t *testing.T, actionID string) (turnID, entityID string) {
	t.Helper()
	acceptance, err := w.recorder.Accept(t.Context(), &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   composeID(t, w.namespace, "player", "one"),
		CampaignID: w.campaignID,
		SceneID:    composeID(t, w.namespace, "scene", "gatehouse"),
		Text:       "I test the hinges.",
		ArrivedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "ws://parked/1"},
	})
	if err != nil {
		t.Fatalf("accept %s: %v", actionID, err)
	}
	if !acceptance.Created {
		t.Fatalf("action %s did not create a turn", actionID)
	}
	return acceptance.TurnID, acceptance.TurnEntityID
}

// writeCaseDecision lands the deterministic non-mystery interpretation artifact
// through the same content and graph lanes as the production stage.
func (w *parkedWorld) writeCaseDecision(t *testing.T, turnID, actionID, entityID string) {
	t.Helper()
	record := &payload.CaseDecisionRecord{
		TurnID: turnID, ActionID: actionID,
		Status: payload.CaseDecisionStatusNotApplicable,
	}
	ref, err := w.content.PutCaseDecisionRecord(t.Context(), entityID, record)
	if err != nil {
		t.Fatalf("PutCaseDecisionRecord: %v", err)
	}
	triples, err := record.Triples(entityID, ref.String(), "test-executor", time.Now().UTC())
	if err != nil {
		t.Fatalf("case decision triples: %v", err)
	}
	if _, err := w.graph.MergeTriples(t.Context(), entityID, triples); err != nil {
		t.Fatalf("merge case decision: %v", err)
	}
}

// writeVerdict lands an adjudication artifact on a turn the way the terminal
// tool executor does — through the real projection and the real merge lane.
func (w *parkedWorld) writeVerdict(t *testing.T, turnID, actionID, entityID string) {
	t.Helper()
	target := composeID(t, w.namespace, "character", "rook")
	verdict := &payload.Verdict{
		TurnID:   turnID,
		ActionID: actionID,
		SceneID:  composeID(t, w.namespace, "scene", "gatehouse"),
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskModerate,
			Consequence:  vocabulary.ConsequenceHarm,
			RequiresRoll: true,
		},
		Bands: map[vocabulary.OutcomeBand][]payload.EffectIntent{
			vocabulary.BandMiss:    {{Type: vocabulary.EffectSetStatus, Target: target, Status: vocabulary.StatusWounded}},
			vocabulary.BandPartial: {{Type: vocabulary.EffectSetStatus, Target: target, Status: vocabulary.StatusExhausted}},
			vocabulary.BandFull:    {{Type: vocabulary.EffectSetStatus, Target: target, Status: vocabulary.StatusHealthy}},
		},
		Rationale: "The gate is heavy but the fiction allows it.",
	}
	ref, err := w.content.PutVerdict(t.Context(), entityID, verdict)
	if err != nil {
		t.Fatalf("PutVerdict: %v", err)
	}
	triples, err := verdict.Triples(entityID, ref.String(), "test-executor", time.Now().UTC())
	if err != nil {
		t.Fatalf("verdict triples: %v", err)
	}
	if _, err := w.graph.MergeTriples(t.Context(), entityID, triples); err != nil {
		t.Fatalf("merge verdict: %v", err)
	}
}

// reconciler builds the pass over the REAL measured stage stream.
//
// The settle window is shortened, not removed. Its production size is chosen for
// a rule processor replaying a whole world; here the world is small and nothing
// else is publishing, so a short quiet window reaches the same conclusion in less
// wall clock — and keeping it non-zero is what makes these tests exercise the
// settle path at all.
// publishTask puts one persona task on AGENT, in the envelope the spawner
// publishes and with the identity metadata it stamps.
func (w *parkedWorld) publishTask(t *testing.T, turnID string) string {
	t.Helper()
	entityID := w.prefix(t) + turnID
	body, err := json.Marshal(map[string]any{
		"payload": map[string]any{
			"metadata": map[string]any{persona.MetadataKeyTurnEntityID: entityID},
		},
	})
	if err != nil {
		t.Fatalf("encode task: %v", err)
	}
	if err := w.harness.Client.PublishToStream(
		t.Context(), persona.TaskSubjectFor(persona.RoleAdjudicator), body); err != nil {
		t.Fatalf("publish task: %v", err)
	}
	return entityID
}

// prefix is this world's turn-entity prefix.
func (w *parkedWorld) prefix(t *testing.T) string {
	t.Helper()
	id, err := vocabulary.SiblingTypePrefix(w.campaignID, "turn")
	if err != nil {
		t.Fatalf("SiblingTypePrefix: %v", err)
	}
	return id + "."
}

// consumeTasks binds the stand-in loop consumer and acknowledges want tasks.
func (w *parkedWorld) consumeTasks(t *testing.T, want int) {
	t.Helper()
	got := make(chan struct{}, want+4)
	if err := w.harness.Client.ConsumeDurable(context.Background(), natsclient.StreamConsumerConfig{
		StreamName:    persona.TaskStream,
		ConsumerName:  "test-idle-loop-" + w.namespace,
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
	defer w.harness.Client.StopAllConsumers()

	for i := 0; i < want; i++ {
		select {
		case <-got:
		case <-time.After(30 * time.Second):
			t.Fatalf("the stand-in loop received %d of %d tasks", i, want)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		consumer, err := w.agent.Consumer(context.Background(), "test-idle-loop-"+w.namespace)
		if err != nil {
			t.Fatalf("read the stand-in loop consumer: %v", err)
		}
		info, err := consumer.Info(context.Background())
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

func (w *parkedWorld) reconciler(t *testing.T, opts ...resume.Option) *resume.Reconciler {
	t.Helper()
	queued, err := resume.NewWorkQueues(w.stream, w.agent, resume.WithSettleWindow(time.Second, 30*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	pass, err := resume.NewReconciler(
		w.graph, w.harness.Client, w.recorder, w.content, queued, w.campaignID, opts...)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	return pass
}

func (w *parkedWorld) attemptsOn(t *testing.T, entityID string) []any {
	t.Helper()
	state, err := w.harness.QueryEntity(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read turn %s: %v", entityID, err)
	}
	return testinfra.ObjectsFor(state, vocabulary.TurnResumeAttempts.String())
}

// THE FINDING (F22), MEASURED RATHER THAN ARGUED, AND WIDER THAN IT WAS WRITTEN.
//
// Three turns are parked in the three shapes a real crash produces, a rule
// processor is restarted against the same broker, and twenty seconds pass.
// Bootstrap replay rescues NONE of them — including the one whose artifact IS
// present, which the finding did not claim.
//
// Two conditions have to hold together for a rule to re-fire at boot, and a
// parked turn satisfies neither. It must currently match, and every mid-chain
// rule is a phase AND an artifact; and something must have written to the entity
// since the rule last evaluated it, which upstream's durable stale-replay guard
// enforces by skipping an evaluation whose source revision it already recorded.
// A parked turn is precisely a turn nobody has touched.
//
// The twenty seconds are not a guess at a race. The assertion is that NOTHING
// happens, so the window has to be long enough that a rescue would have arrived;
// the same world's first hops land in well under a second (awaitTriggers, above).
func TestBootstrapReplay_RescuesNoParkedTurn(t *testing.T) {
	world := startParkedWorld(t)
	interpreting := subjectForPhase(t, vocabulary.PhaseInterpreting)
	adjudicating := subjectForPhase(t, vocabulary.PhaseAdjudicating)
	resolving := subjectForPhase(t, vocabulary.PhaseResolving)

	// A: parked in `accepted`. The pack's first hop already fired for it.
	_, acceptedTurn := world.accept(t, "parked-accepted")
	// B: parked in `adjudicating` with NO verdict — the persona loop died.
	strandedID, strandedTurn := world.accept(t, "parked-stranded")
	// C: parked in `adjudicating` WITH a verdict. Its hop fired and the message
	// is treated as lost.
	stalledID, stalledTurn := world.accept(t, "parked-stalled")

	for _, entityID := range []string{acceptedTurn, strandedTurn, stalledTurn} {
		world.awaitTriggers(t, interpreting, entityID, 1)
	}
	for _, parked := range []struct{ turnID, actionID, entityID string }{
		{strandedID, "parked-stranded", strandedTurn},
		{stalledID, "parked-stalled", stalledTurn},
	} {
		if _, err := world.recorder.Advance(
			t.Context(), parked.turnID, parked.entityID, vocabulary.PhaseInterpreting); err != nil {
			t.Fatalf("advance %s: %v", parked.entityID, err)
		}
		world.writeCaseDecision(t, parked.turnID, parked.actionID, parked.entityID)
		world.awaitTriggers(t, adjudicating, parked.entityID, 1)
		if _, err := world.recorder.Advance(
			t.Context(), parked.turnID, parked.entityID, vocabulary.PhaseAdjudicating); err != nil {
			t.Fatalf("advance %s: %v", parked.entityID, err)
		}
	}
	world.writeVerdict(t, stalledID, "parked-stalled", stalledTurn)
	world.awaitTriggers(t, resolving, stalledTurn, 1)

	type watched struct {
		subject  string
		entityID string
		before   int
	}
	before := []watched{
		{interpreting, acceptedTurn, world.triggers(t, interpreting, acceptedTurn)},
		{adjudicating, strandedTurn, world.triggers(t, adjudicating, strandedTurn)},
		{resolving, stalledTurn, world.triggers(t, resolving, stalledTurn)},
	}

	// Restart the rule processor against the same broker: a boot, in every way
	// that matters to the entity watcher.
	world.restartRules(t)
	time.Sleep(20 * time.Second)

	for _, watch := range before {
		if now := world.triggers(t, watch.subject, watch.entityID); now != watch.before {
			t.Fatalf("bootstrap replay published %d new triggers on %s for turn %s (was %d, now %d). If this "+
				"starts failing here, the stranded-turn pass is still correct but its justification has changed "+
				"and internal/resume's package doc must be corrected rather than left describing a broker that "+
				"no longer behaves this way",
				now-watch.before, watch.subject, watch.entityID, watch.before, now)
		}
	}

	// Every one of the three is still exactly where it was parked, which is what
	// makes the counts above a statement about recovery rather than about timing.
	for _, entityID := range []string{acceptedTurn, strandedTurn, stalledTurn} {
		phase, err := world.recorder.Current(t.Context(), entityID)
		if err != nil {
			t.Fatalf("read %s: %v", entityID, err)
		}
		if phase.IsTerminal() {
			t.Fatalf("turn %s ended during the quiet window; it was supposed to be parked", entityID)
		}
	}
}

// The measurement the ordering constraint rests on: bootstrap replay DOES
// publish for one shape, and Start returning tells you nothing about whether it
// has.
//
// The shape is the narrow one — a rule already matching, still matching, whose
// entity was written while the processor was down. Timing is what matters here,
// and it is NOT stable: this has been observed publishing 1.02 seconds after
// Start returned, and observed having already published by the time Start
// returned. That instability is the argument. Anything reading the stage stream
// at boot has to wait for the STREAM to go quiet, because there is no signal on
// the processor to wait for and no safe constant to sleep.
//
// So the assertion is only that replay publishes at all; the timing is logged.
// A test that asserted either ordering would be asserting a race.
func TestBootstrapReplay_PublishesForATurnWrittenWhileItWasDown(t *testing.T) {
	world := startParkedWorld(t)
	interpreting := subjectForPhase(t, vocabulary.PhaseInterpreting)
	adjudicating := subjectForPhase(t, vocabulary.PhaseAdjudicating)
	resolving := subjectForPhase(t, vocabulary.PhaseResolving)

	turnID, entityID := world.accept(t, "replay-timing")
	world.awaitTriggers(t, interpreting, entityID, 1)
	if _, err := world.recorder.Advance(t.Context(), turnID, entityID, vocabulary.PhaseInterpreting); err != nil {
		t.Fatalf("advance: %v", err)
	}
	world.writeCaseDecision(t, turnID, "replay-timing", entityID)
	world.awaitTriggers(t, adjudicating, entityID, 1)
	if _, err := world.recorder.Advance(t.Context(), turnID, entityID, vocabulary.PhaseAdjudicating); err != nil {
		t.Fatalf("advance: %v", err)
	}
	world.writeVerdict(t, turnID, "replay-timing", entityID)
	world.awaitTriggers(t, resolving, entityID, 1)

	// Genuinely offline, which is the part an earlier version of this file got
	// wrong: the write must land while NO processor is running, or it is handled
	// live and the boot has nothing left to replay.
	world.stopRules()
	time.Sleep(500 * time.Millisecond)
	record := &payload.TurnResume{TurnID: turnID, Attempts: 1}
	triples, err := record.Triples(entityID, "test", time.Now().UTC())
	if err != nil {
		t.Fatalf("triples: %v", err)
	}
	if _, err := world.graph.MergeTriples(t.Context(), entityID, triples); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := world.triggers(t, resolving, entityID); got != 1 {
		t.Fatalf("the offline write published %d triggers; nothing was running to publish one", got-1)
	}

	started := time.Now()
	world.startRules(t)
	returned := time.Since(started)
	if world.triggers(t, resolving, entityID) > 1 {
		t.Logf("Start returned after %s with bootstrap replay ALREADY published", returned)
		return
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if world.triggers(t, resolving, entityID) > 1 {
			t.Logf("Start returned after %s; bootstrap replay published %s after that",
				returned, time.Since(started)-returned)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("bootstrap replay never published for the one shape it is documented to rescue. If THIS is now " +
		"true, nothing publishes into the queued set at boot and internal/resume's settle wait is " +
		"over-cautious rather than load-bearing — correct the package doc rather than deleting the wait")
}

// A turn the substrate is still holding a trigger for is measured as such and
// left entirely alone — no re-trigger, no attempt, no ending.
func TestResume_LeavesTurnsWithQueuedTriggersAlone(t *testing.T) {
	world := startParkedWorld(t)
	interpreting := subjectForPhase(t, vocabulary.PhaseInterpreting)

	// Nothing consumes in this world, so the first hop stays queued.
	_, acceptedTurn := world.accept(t, "queued-accepted")
	world.awaitTriggers(t, interpreting, acceptedTurn, 1)
	before := world.triggers(t, interpreting, acceptedTurn)

	report, err := world.reconciler(t).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Queued != 1 || report.StageRetriggered != 0 || report.Abandoned != 0 {
		t.Fatalf("report = %+v; the turn has a queued trigger and must be left alone", report)
	}
	if got := world.triggers(t, interpreting, acceptedTurn); got != before {
		t.Errorf("turn %s holds %d triggers, want %d; a second delivery on the first hop is a second billed "+
			"adjudicator spawn, because no artifact exists yet for the resume guard to skip on",
			acceptedTurn, got, before)
	}
	if got := world.attemptsOn(t, acceptedTurn); len(got) != 0 {
		t.Errorf("a turn the pass did not act on spent an attempt: %v", got)
	}
}

// And what the boot pass does once the substrate is holding nothing: each turn
// gets the disposition its evidence earns.
func TestResume_DisposesEveryShapeOnMeasuredEvidence(t *testing.T) {
	world := startParkedWorld(t)
	interpreting := subjectForPhase(t, vocabulary.PhaseInterpreting)
	adjudicating := subjectForPhase(t, vocabulary.PhaseAdjudicating)
	resolving := subjectForPhase(t, vocabulary.PhaseResolving)

	_, acceptedTurn := world.accept(t, "dispose-accepted")
	strandedID, strandedTurn := world.accept(t, "dispose-stranded")
	stalledID, stalledTurn := world.accept(t, "dispose-stalled")

	for _, entityID := range []string{acceptedTurn, strandedTurn, stalledTurn} {
		world.awaitTriggers(t, interpreting, entityID, 1)
	}
	for _, parked := range []struct{ turnID, actionID, entityID string }{
		{strandedID, "dispose-stranded", strandedTurn},
		{stalledID, "dispose-stalled", stalledTurn},
	} {
		if _, err := world.recorder.Advance(
			t.Context(), parked.turnID, parked.entityID, vocabulary.PhaseInterpreting); err != nil {
			t.Fatalf("advance %s: %v", parked.entityID, err)
		}
		world.writeCaseDecision(t, parked.turnID, parked.actionID, parked.entityID)
		world.awaitTriggers(t, adjudicating, parked.entityID, 1)
		if _, err := world.recorder.Advance(
			t.Context(), parked.turnID, parked.entityID, vocabulary.PhaseAdjudicating); err != nil {
			t.Fatalf("advance %s: %v", parked.entityID, err)
		}
	}
	world.writeVerdict(t, stalledID, "dispose-stalled", stalledTurn)
	world.awaitTriggers(t, resolving, stalledTurn, 1)

	// Acknowledge everything, which is what a stage that RAN leaves behind. Until
	// this, all three turns have queued triggers and the pass correctly leaves
	// them alone; afterwards the substrate is holding nothing for any of them,
	// which is what makes this a test of the acting branches.
	world.drainStageTriggers(t)

	before := map[string]int{
		acceptedTurn: world.triggers(t, interpreting, acceptedTurn),
		strandedTurn: world.triggers(t, adjudicating, strandedTurn),
		stalledTurn:  world.triggers(t, resolving, stalledTurn),
	}

	report, err := world.reconciler(t).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if report.Queued != 0 {
		t.Errorf("counted %d queued turns after draining every trigger: %+v", report.Queued, report)
	}
	// A (accepted, never started) and B (adjudicating, no verdict) are both
	// stranded and both re-runnable. A is owed the FIRST stage, which is the one
	// thing this pass derives; B is owed its own.
	if report.StageRetriggered != 2 {
		t.Errorf("re-triggered %d stages, want 2 (the turn that never started and the one whose persona "+
			"produced nothing): %+v", report.StageRetriggered, report)
	}
	// C carries its verdict, so re-running adjudication would resume a phase it is
	// already in and skip on the artifact. Its sighting is COUNTED rather than
	// acted on: the queued set is a snapshot taken before the pages were read, and
	// a turn ended on a reading that old could be one seconds from resolving. It
	// ends on a later pass if it is still in this state.
	if report.Unadvanceable != 1 {
		t.Errorf("counted %d unadvanceable turns, want 1 (the one carrying its verdict with no hop): %+v",
			report.Unadvanceable, report)
	}
	if report.Abandoned != 0 || len(report.Failures) != 0 {
		t.Errorf("report = %+v; nothing here has exhausted its budget on a first sighting", report)
	}

	for _, want := range []struct {
		subject  string
		entityID string
		delta    int
		why      string
	}{
		{interpreting, acceptedTurn, 1, "it never started, and the first stage is what starts a turn"},
		{adjudicating, strandedTurn, 1, "nothing is queued for it and its persona produced nothing"},
		{resolving, stalledTurn, 0, "its stage finished; re-running it would change nothing"},
	} {
		got := world.triggers(t, want.subject, want.entityID)
		if wantCount := before[want.entityID] + want.delta; got != wantCount {
			t.Errorf("turn %s holds %d triggers on %s, want %d — %s",
				want.entityID, got, want.subject, wantCount, want.why)
		}
	}

	// Both re-triggers consume one bounded recovery attempt.
	for _, entityID := range []string{acceptedTurn, strandedTurn} {
		if got := world.attemptsOn(t, entityID); len(got) != 1 || fmt.Sprint(got[0]) != "1" {
			t.Errorf("the re-triggered turn %s records attempts %v, want exactly [1]", entityID, got)
		}
	}
	// The unadvanceable one spent a SIGHTING — a graph write and no publish —
	// rather than being ended or re-run.
	if got := world.attemptsOn(t, stalledTurn); len(got) != 1 || fmt.Sprint(got[0]) != "1" {
		t.Errorf("the unadvanceable turn records attempts %v, want exactly [1]", got)
	}
	if phase, err := world.recorder.Current(t.Context(), stalledTurn); err != nil {
		t.Fatalf("read turn: %v", err)
	} else if phase != vocabulary.PhaseAdjudicating {
		t.Errorf("the unadvanceable turn moved to %q on its first sighting; a turn ended on a snapshot that "+
			"old could be one seconds from resolving", phase)
	}
}

// The budget ends. A stranded turn that never produces its artifact is failed on
// the record with a closed code, not left waiting — and the code lands on the
// graph through the real recorder, with its explanation behind a real reference.
func TestResume_AbandonsATurnItCannotRescue(t *testing.T) {
	world := startParkedWorld(t)
	interpreting := subjectForPhase(t, vocabulary.PhaseInterpreting)
	adjudicating := subjectForPhase(t, vocabulary.PhaseAdjudicating)

	turnID, entityID := world.accept(t, "resume-doomed")
	world.awaitTriggers(t, interpreting, entityID, 1)
	if _, err := world.recorder.Advance(t.Context(), turnID, entityID, vocabulary.PhaseInterpreting); err != nil {
		t.Fatalf("advance: %v", err)
	}
	world.writeCaseDecision(t, turnID, "resume-doomed", entityID)
	world.awaitTriggers(t, adjudicating, entityID, 1)
	if _, err := world.recorder.Advance(t.Context(), turnID, entityID, vocabulary.PhaseAdjudicating); err != nil {
		t.Fatalf("advance: %v", err)
	}

	// Draining before each pass is what a real boot looks like: the stage
	// consumed its trigger and produced nothing, so the next pass finds the turn
	// with nothing queued. WITHOUT the drain the pass correctly counts the turn
	// as queued and never reaches the budget at all — which is the measurement
	// working, and is why this test has to model a stage that ran.
	pass := world.reconciler(t, resume.WithMaxAttempts(1))

	world.drainStageTriggers(t)
	first, err := pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if first.StageRetriggered != 1 || first.Abandoned != 0 {
		t.Fatalf("first pass = %+v; the turn had budget left", first)
	}

	world.drainStageTriggers(t)
	second, err := pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if second.Abandoned != 1 || second.StageRetriggered != 0 {
		t.Fatalf("second pass = %+v; the budget was gone and the turn must end", second)
	}

	state := world.harness.AwaitEntity(t, entityID)
	if got := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String())); got !=
		string(vocabulary.PhaseFailed) {
		t.Fatalf("the abandoned turn is in phase %q, want %q", got, vocabulary.PhaseFailed)
	}
	if got := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnFailureReason.String())); got !=
		string(vocabulary.FailureTurnStranded) {
		t.Errorf("failure reason %q, want %q", got, vocabulary.FailureTurnStranded)
	}
	ref := testinfra.FirstObject(state, vocabulary.TurnFailureRef.String())
	if ref == nil {
		t.Fatal("the abandoned turn carries no explanation reference")
	}
	parsed, err := content.ParseRef(fmt.Sprint(ref))
	if err != nil {
		t.Fatalf("the abandoned turn's detail reference does not parse: %v", err)
	}
	detail, err := world.content.GetFailureDetail(t.Context(), parsed)
	if err != nil {
		t.Fatalf("the abandoned turn points at an explanation nobody stored: %v", err)
	}
	if detail.Reason != vocabulary.FailureTurnStranded {
		t.Errorf("the stored explanation carries reason %q", detail.Reason)
	}
	// This sentence is the record of why a player's turn died, so its arithmetic
	// has to be the arithmetic that happened: with a budget of one, the pass
	// re-triggered once and saw the turn twice. Saying "2 boots" — the budget, or
	// the sighting count under a boots label — would be wrong in both the number
	// and the unit, since these three passes all ran in one process.
	if !strings.Contains(detail.Message, "re-triggered 1 time(s) across 2 recovery passes") {
		t.Errorf("the stored explanation does not state what actually happened: %q", detail.Message)
	}

	// A third pass must leave the ended turn alone. The recorder declines a
	// terminal turn, so re-running is safe — but the pass should not be reaching
	// for it at all.
	world.drainStageTriggers(t)
	third, err := pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("third Reconcile: %v", err)
	}
	if third.Abandoned != 0 || third.StageRetriggered != 0 || third.Queued != 0 {
		t.Fatalf("third pass = %+v; an ended turn owes nobody a stage", third)
	}
	if third.Resolved == 0 {
		t.Fatalf("third pass = %+v; the abandoned turn should be counted as resolved", third)
	}
}

// A publish into a subject no stream captures is REFUSED rather than delivered,
// which is what makes running this pass before the stage stream exists a loud
// error instead of a pass that reports every turn as resumed and resumes none.
//
// This is the same footgun that made a core-NATS test pass while proving
// nothing: js.PublishMsg still puts the bytes on the wire for core subscribers,
// and only the acknowledgement fails.
func TestJetStreamPublish_IntoNoStreamIsRefused(t *testing.T) {
	harness := testinfra.Require(t)

	// A subject in the engine's own namespace that no stream captures. The
	// stage stream's filter is semmachina.turn.>, so this sits deliberately
	// outside it.
	subject := fmt.Sprintf("semmachina.resume.probe.%d", worldCounter.Add(1))
	if err := harness.Client.PublishToStream(t.Context(), subject, []byte(`{"entity_id":"x"}`)); err == nil {
		t.Fatal("a JetStream publish into a subject no stream captures reported success. Everything that " +
			"republishes a lost message depends on the opposite: the bytes still reach core subscribers, so " +
			"only the refused acknowledgement distinguishes a durable republish from a message into nothing")
	}
}

// THE DEFECT A ONE-QUEUE PASS HAS, end to end.
//
// Spawner.Run acknowledges the stage trigger when it PUBLISHES the persona task,
// not when the persona finishes, so a turn whose adjudication is in flight has
// NOTHING queued on TURN_STAGES and an unacknowledged task on AGENT that upstream
// will redeliver. A pass that measured only the stage stream would call that turn
// stranded and re-trigger it — a second adjudicator spawn, racing the first to
// write turn.verdict.* through a last-write-wins merge.
//
// The bridge HOLDS the task here rather than acking it, which is what the real
// loop does while a persona runs. That mode did not exist in the stand-in, and
// its absence is exactly why nothing caught this.
func TestTurnLoop_APersonaStillRunningIsLeftAlone(t *testing.T) {
	world := startLoop(t)
	hold(t, persona.RoleAdjudicator)

	_, entityID := world.submit(t, "act-inflight", "I weigh the door with my shoulder.")
	select {
	case <-world.spawned:
	case <-time.After(45 * time.Second):
		t.Fatal("the adjudicator was never spawned")
	}
	world.awaitPhase(t, entityID, vocabulary.PhaseAdjudicating)

	stream, err := world.harness.Client.GetStream(t.Context(), rulepack.StageStream)
	if err != nil {
		t.Fatalf("get the stage stream: %v", err)
	}
	knowledgeConsumer, err := stream.Consumer(t.Context(), rulepack.KnowledgeConsumerName)
	if err != nil {
		t.Fatalf("get the knowledge consumer: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		info, infoErr := knowledgeConsumer.Info(t.Context())
		if infoErr != nil {
			t.Fatalf("read the knowledge consumer: %v", infoErr)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("knowledge work did not retire before measuring a genuinely stranded turn: %+v", info)
		}
		time.Sleep(100 * time.Millisecond)
	}
	agent, err := world.harness.Client.GetStream(t.Context(), persona.TaskStream)
	if err != nil {
		t.Fatalf("get the agent stream: %v", err)
	}
	queued, err := resume.NewWorkQueues(stream, agent, resume.WithSettleWindow(time.Second, 30*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}

	// The measurement itself, before the pass: the task IS queued for this turn,
	// and no stage trigger is.
	if err := queued.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := queued.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending[entityID] == 0 {
		t.Fatalf("the queued set does not include turn %s, whose adjudication is unacknowledged on %s; the "+
			"pass would re-trigger it into a second billed spawn", entityID, persona.TaskStream)
	}

	pass, err := resume.NewReconciler(
		world.graph, world.harness.Client, world.recorder, world.content, queued,
		composeID(t, world.namespace, "campaign", "instance"))
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	report, err := pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.StageRetriggered != 0 || report.Abandoned != 0 || report.Unadvanceable != 0 {
		t.Fatalf("report = %+v; a turn whose persona is mid-flight must be left entirely alone", report)
	}
	if report.Queued == 0 {
		t.Fatalf("report = %+v; the in-flight turn was not counted as queued", report)
	}
	state, err := world.harness.QueryEntity(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read turn: %v", err)
	}
	if got := testinfra.ObjectsFor(state, vocabulary.TurnResumeAttempts.String()); len(got) != 0 {
		t.Errorf("a turn nothing acted on spent an attempt: %v", got)
	}
	if phase, err := world.recorder.Current(t.Context(), entityID); err != nil {
		t.Fatalf("read turn: %v", err)
	} else if phase != vocabulary.PhaseAdjudicating {
		t.Errorf("the in-flight turn moved to %q", phase)
	}
}

// End to end, through the whole engine: a persona loop that RAN and gave up —
// task acknowledged, nothing produced — leaves a turn nothing will ever run
// again, and the boot pass is what makes the next boot finish it.
//
// The three seconds of silence in the middle are the part that would be missing
// from a weaker version of this test. Without them, a turn that completed after
// the pass ran would be indistinguishable from one that was always going to
// complete on its own — and "it recovers eventually" was the belief this whole
// task exists to correct.
func TestTurnLoop_AStrandedTurnResumesAndCompletesAfterTheBootPass(t *testing.T) {
	world := startLoop(t)
	drop(t, persona.RoleAdjudicator)

	turnID, entityID := world.submit(t, "act-stranded", "I lean on the gate and wait.")
	select {
	case <-world.spawned:
	case <-time.After(45 * time.Second):
		t.Fatal("the adjudicator was never spawned")
	}
	world.awaitPhase(t, entityID, vocabulary.PhaseAdjudicating)

	// The loop RAN and gave up: the stage acknowledged its trigger when it
	// published the task, and the bridge acknowledged the task without producing
	// an artifact. Nothing is queued on either stream, which is what makes this
	// turn genuinely stranded rather than merely in flight.
	undrop(t)
	time.Sleep(3 * time.Second)
	if phase, err := world.recorder.Current(t.Context(), entityID); err != nil {
		t.Fatalf("read turn: %v", err)
	} else if phase != vocabulary.PhaseAdjudicating {
		t.Fatalf("the turn left %q on its own; this test cannot then show what the boot pass is for",
			vocabulary.PhaseAdjudicating)
	}

	stream, err := world.harness.Client.GetStream(t.Context(), rulepack.StageStream)
	if err != nil {
		t.Fatalf("get the stage stream: %v", err)
	}
	agent, err := world.harness.Client.GetStream(t.Context(), persona.TaskStream)
	if err != nil {
		t.Fatalf("get the agent stream: %v", err)
	}
	queued, err := resume.NewWorkQueues(stream, agent, resume.WithSettleWindow(time.Second, 30*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	pass, err := resume.NewReconciler(
		world.graph, world.harness.Client, world.recorder, world.content, queued,
		composeID(t, world.namespace, "campaign", "instance"))
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	report, err := pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.StageRetriggered != 1 {
		t.Fatalf("report = %+v; the stranded turn's stage was not re-triggered", report)
	}
	select {
	case resumed := <-world.spawned:
		if want := string(persona.RoleAdjudicator) + "/" + turnID + "/resume/1"; resumed.TaskID != want {
			t.Fatalf("recovered task id = %q, want %q", resumed.TaskID, want)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("the persisted recovery attempt did not spawn a new adjudicator task")
	}
	state := world.awaitPhase(t, entityID, vocabulary.PhaseComplete)
	if got := testinfra.ObjectsFor(state, vocabulary.TurnResumeAttempts.String()); len(got) != 1 ||
		fmt.Sprint(got[0]) != "1" {
		t.Errorf("the resumed turn records attempts %v, want exactly [1]", got)
	}
	// The rescue must not have cost a second verdict or a second narration.
	for _, predicate := range []vocabulary.Predicate{vocabulary.TurnVerdictRef, vocabulary.TurnNarrationRef} {
		if got := testinfra.ObjectsFor(state, predicate.String()); len(got) != 1 {
			t.Errorf("the resumed turn holds %d values for %s, want exactly one", len(got), predicate)
		}
	}
}

func subjectForPhase(t *testing.T, phase vocabulary.TurnPhase) string {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("SubjectForPhase(%s): %v", phase, err)
	}
	return subject
}

// THE PROPERTY THE FILTER MECHANISM RESTS ON, which one consumer cannot exercise.
//
// The boot pass finds the agentic loop's consumer by WHAT IT FILTERS rather than
// by its name, and then takes the MINIMUM acknowledgement floor across every
// consumer that matches. Both halves are deliberate — a name is per-deployment
// configuration while a filter is a contract, and work above any matching
// consumer's floor is still owed to somebody — but together they mean a filter
// test that matches too widely is a silent stall: an unrelated cursor on a
// different subject drags the minimum down, every task reads as unacknowledged,
// every turn reads as queued, and the pass leaves the whole world alone.
//
// A stream with one consumer cannot see that, because "match the right one" and
// "match any" return the same answer. So this builds the PRODUCTION shape: the
// agentic loop's task consumer on agent.task.*, which has finished a task, and
// this engine's REAL loop-failure watcher on agent.failed.*, which has finished
// nothing. The finished task must read as finished.
func TestResume_ReadsTheTaskConsumersFloorAndNotTheFailureWatchers(t *testing.T) {
	world := startParkedWorld(t)

	// A persona task, published and then finished — exactly what a completed
	// adjudication leaves behind.
	entityID := world.publishTask(t, "turn-adjudicated")
	world.consumeTasks(t, 1)

	// The engine's own failure watcher, on a DIFFERENT subject, having finished
	// nothing: its floor is zero. The name is per-test only so parallel worlds on
	// one broker do not share a cursor; the FILTER is production's, and the
	// filter is the whole point.
	watcher, err := stage.NewLoopFailureWatcher(
		world.harness.Client, world.harness.Client,
		message.NewDecoder(world.harness.Registry), world.recorder, world.content,
		stage.WithLoopFailureConsumerName("test-loop-failures-"+world.namespace),
	)
	if err != nil {
		t.Fatalf("NewLoopFailureWatcher: %v", err)
	}
	if err := watcher.Start(context.Background()); err != nil {
		t.Fatalf("start the loop-failure watcher: %v", err)
	}
	t.Cleanup(func() {
		world.harness.Client.StopAllConsumers()
		_ = world.agent.DeleteConsumer(context.Background(), "test-loop-failures-"+world.namespace)
	})

	// Anti-vacuity: both consumers really are on the stream, with different
	// filters, or this test is the one-consumer case again wearing a longer body.
	filters := map[string]bool{}
	for info := range world.agent.ListConsumers(t.Context()).Info() {
		if info != nil {
			filters[info.Config.FilterSubject] = true
		}
	}
	for _, want := range []string{persona.TaskSubjectFilter, stage.LoopFailedSubject} {
		if !filters[want] {
			t.Fatalf("no consumer on %s filters %q; the stream carries %v and this test would prove nothing",
				persona.TaskStream, want, filters)
		}
	}

	queued, err := resume.NewWorkQueues(world.stream, world.agent,
		resume.WithSettleWindow(time.Second, 30*time.Second))
	if err != nil {
		t.Fatalf("NewWorkQueues: %v", err)
	}
	if err := queued.Settle(t.Context()); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	pending, err := queued.Pending(t.Context())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if got := pending[entityID]; got != 0 {
		t.Fatalf("turn %s reads as having %d pieces of work queued, but its task was finished. The floor came "+
			"from a consumer on another subject — the loop-failure watcher's, which has finished nothing — so "+
			"every task ever published reads as in flight and the pass leaves the entire world alone",
			entityID, got)
	}
}
