package stage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/processor/rule"
	ssvocab "github.com/c360studio/semstreams/vocabulary"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The turn loop is a claim about two things a fake cannot be wrong about
// together: that the ENGINE's own rule pack loads into the REAL rule processor,
// and that a turn walks its phases with nothing calling a stage by hand.
//
// Every substitute considered here would have hidden the failure it was meant to
// catch. A stub rule engine cannot reject an undeclared predicate, which is the
// whole content of 8.0. A fake KV cannot deliver the entity-watch event that
// makes the chain a chain. And a hand-called stage proves the stage and nothing
// about the wiring — which is exactly the class of bug this task exists to close,
// since the two components it wires had no production caller at all.
//
// ONE bridge is stood in for, deliberately and visibly: the agentic loop. Its
// job is to call a model and dispatch a tool call, and both belong to the E2E
// task that owns the mock model endpoint. What runs here is the REAL terminal
// tool executor — the thing that actually writes the verdict and the narration —
// driven by a subscriber on the same task subject the loop consumes. Everything
// between the turn entity and that executor is production code on production
// wire.

func TestMain(m *testing.M) {
	// Registration must precede any rule config load, in every binary and every
	// test — the rule processor rejects a canonical-but-undeclared predicate, so
	// without this the pack simply does not start.
	if err := vocabulary.RegisterPredicates(); err != nil {
		fmt.Fprintf(os.Stderr, "register semmachina predicates: %v\n", err)
		os.Exit(1)
	}
	os.Exit(testinfra.RunTests(m))
}

var worldCounter atomic.Int64

const (
	testOrg      = "c360"
	testTemplate = "starter"
)

// loop is one whole turn engine over a real broker: real graph-ingest, the real
// rule processor running the engine's own pack, and every stage bound to the
// stage stream.
type loop struct {
	harness   *testinfra.Harness
	graph     *graphio.Store
	content   *content.Store
	recorder  *turn.Recorder
	namespace string

	sceneID     string
	characterID string
	playerID    string

	spawned chan agentic.TaskMessage
	runs    *runLog

	// verdict is what the persona bridge scripts the adjudicator's exit as. It
	// is a function rather than a value so a test can choose the SHAPE of the
	// judgment (roll or no roll, valid or refused) — never the outcome band,
	// which only the seed decides.
	verdict func() map[string]any
}

func startLoop(t *testing.T) *loop {
	t.Helper()
	harness := testinfra.Require(t)
	harness.RequireIndex(t)
	namespace := fmt.Sprintf("w%d", worldCounter.Add(1))

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	backend, err := content.NewObjectStore(t.Context(), harness.Client, content.WithBucket("STAGE_"+namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	identity := turn.Identity{Org: testOrg, WorldNS: namespace, Template: testTemplate}
	recorder, err := turn.NewRecorder(store, artifacts, identity)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	world := &loop{
		harness: harness, graph: store, content: artifacts, recorder: recorder, namespace: namespace,
		sceneID:     composeID(t, namespace, "scene", "gatehouse"),
		characterID: composeID(t, namespace, "character", "rook"),
		playerID:    composeID(t, namespace, "player", "one"),
		spawned:     make(chan agentic.TaskMessage, 8),
		runs:        &runLog{counts: map[vocabulary.TurnPhase]int{}},
	}
	world.verdict = world.rollingVerdict
	world.seedWorld(t)
	world.startRules(t)
	world.startStages(t, artifacts)
	world.startPersonaBridge(t, artifacts)
	return world
}

func composeID(t *testing.T, namespace, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(testOrg, namespace, testTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose %s id: %v", kind, err)
	}
	return id
}

// seedWorld creates the smallest world the assembler and the applier both accept:
// somewhere to be, someone to be it, and a player bound to them.
func (l *loop) seedWorld(t *testing.T) {
	t.Helper()
	l.createEntity(t, l.sceneID, map[string]any{
		vocabulary.WorldEntityKind.String(): string(vocabulary.EntityKindScene),
		vocabulary.WorldEntityName.String(): "The Gatehouse",
	})
	l.createEntity(t, l.characterID, map[string]any{
		vocabulary.WorldEntityKind.String():        string(vocabulary.EntityKindCharacter),
		vocabulary.WorldEntityName.String():        "Rook",
		vocabulary.WorldLocationCurrent.String():   l.sceneID,
		vocabulary.CharacterStatusCurrent.String(): string(vocabulary.StatusHealthy),
	})
	l.createEntity(t, l.playerID, map[string]any{
		vocabulary.WorldEntityKind.String():        string(vocabulary.EntityKindPlayer),
		vocabulary.PlayerCharacterCurrent.String(): l.characterID,
	})
	l.harness.AwaitEntity(t, l.characterID)
	l.harness.AwaitEntity(t, l.playerID)
}

func (l *loop) createEntity(t *testing.T, id string, facts map[string]any) {
	t.Helper()
	at := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	triples := make([]message.Triple, 0, len(facts))
	for predicate, object := range facts {
		triples = append(triples, message.Triple{
			Subject: id, Predicate: predicate, Object: object,
			Source: "test", Timestamp: at, Confidence: 1.0,
		})
	}
	if _, err := l.graph.CreateEntity(t.Context(), &graph.EntityState{
		ID: id,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version: 1, UpdatedAt: at, Triples: triples,
	}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

// startRules boots the REAL rule processor on the engine's own pack.
func (l *loop) startRules(t *testing.T) {
	t.Helper()
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatalf("the turn-sequencing rule config did not build: %v", err)
	}
	created, err := rule.CreateRuleProcessor(raw, component.Dependencies{
		NATSClient:      l.harness.Client,
		PayloadRegistry: l.harness.Registry,
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("the turn-sequencing rule pack did not load into the rule processor: %v", err)
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
	t.Cleanup(func() { _ = processor.Stop(5 * time.Second) })
}

// startStages binds every stage to the stage stream.
func (l *loop) startStages(t *testing.T, artifacts *content.Store) {
	t.Helper()
	assembler, err := scene.NewAssembler(l.graph)
	if err != nil {
		t.Fatalf("NewAssembler: %v", err)
	}
	guard, err := persona.NewGuard(l.graph)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	builder, err := persona.NewBuilder(artifacts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	adjudicator, err := stage.NewSpawner(
		persona.Adjudicator(), l.recorder, guard, assembler, builder, l.harness.Client)
	if err != nil {
		t.Fatalf("NewSpawner(adjudicator): %v", err)
	}
	narrator, err := stage.NewSpawner(
		persona.Narrator(), l.recorder, guard, assembler, builder, l.harness.Client)
	if err != nil {
		t.Fatalf("NewSpawner(narrator): %v", err)
	}

	seed, err := campaign.NewSeed()
	if err != nil {
		t.Fatalf("NewSeed: %v", err)
	}
	roller, err := dice.NewRoller(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("NewRoller: %v", err)
	}
	resolver, err := dice.NewResolver(roller, l.graph, campaign.Instantiation{
		CampaignID: composeID(t, l.namespace, "campaign", "instance"), Seed: seed,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	diceStage, err := stage.NewResolver(l.recorder, l.graph, artifacts, artifacts, l.graph, resolver)
	if err != nil {
		t.Fatalf("NewResolver stage: %v", err)
	}

	applier, err := effect.NewApplier(l.graph)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	effectStage, err := stage.NewEffector(
		l.recorder, l.recorder, l.graph, artifacts, artifacts, artifacts, applier)
	if err != nil {
		t.Fatalf("NewEffector: %v", err)
	}
	completer, err := stage.NewCompleter(l.recorder)
	if err != nil {
		t.Fatalf("NewCompleter: %v", err)
	}

	runner, err := stage.NewRunner(
		l.harness.Client, l.harness.Client,
		l.runs.wrap(adjudicator, diceStage, effectStage, narrator, completer),
		stage.WithAckTimings(20*time.Second, 5*time.Second),
	)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("start stage runner: %v", err)
	}
	t.Cleanup(l.harness.Client.StopAllConsumers)
}

// startPersonaBridge stands in for the agentic loop.
//
// It consumes the task off the SAME JetStream stream the loop's input port binds
// and drives the REAL terminal tool executor with a scripted exit. Consuming
// from the stream rather than from the raw subject is not a detail: a JetStream
// publish is a core-NATS publish underneath, so a core subscriber sees the task
// even when the stream does not exist and the publish is never acknowledged.
// That is exactly the shape this test hit before the stream was created here —
// the chain completed while both persona stage deliveries silently nak'd.
func (l *loop) startPersonaBridge(t *testing.T, artifacts *content.Store) {
	t.Helper()
	verdict, err := persona.NewVerdictExecutor(artifacts, l.graph)
	if err != nil {
		t.Fatalf("NewVerdictExecutor: %v", err)
	}
	narration, err := persona.NewNarrationExecutor(artifacts, l.graph)
	if err != nil {
		t.Fatalf("NewNarrationExecutor: %v", err)
	}

	// The agentic-loop component owns this stream in production; here the
	// stand-in owns it, for the same reason and at the same moment.
	if _, err := l.harness.Client.EnsureStream(t.Context(), jetstream.StreamConfig{
		Name:      stage.TaskStream,
		Subjects:  []string{stage.TaskSubjectPrefix + "*"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
	}); err != nil {
		t.Fatalf("ensure the %s stream: %v", stage.TaskStream, err)
	}

	handle := func(msgCtx context.Context, data []byte) error {
		task, ok := decodeTask(data)
		if !ok {
			return nil
		}
		select {
		case l.spawned <- task:
		default:
		}
		if l.paused(task) {
			return nil
		}
		call := agentic.ToolCall{ID: task.TaskID, Name: task.Tools[0].Name, Metadata: task.Metadata}
		switch task.Role {
		case string(persona.RoleAdjudicator):
			call.Arguments = l.verdict()
			if _, err := verdict.Execute(msgCtx, call); err != nil {
				return fmt.Errorf("the verdict tool refused a scripted exit: %w", err)
			}
		case string(persona.RoleNarrator):
			call.Arguments = map[string]any{"prose": "Rook shoulders the gate open."}
			if _, err := narration.Execute(msgCtx, call); err != nil {
				return fmt.Errorf("the narration tool refused a scripted exit: %w", err)
			}
		}
		return nil
	}

	if err := l.harness.Client.ConsumeDurable(context.Background(), natsclient.StreamConsumerConfig{
		StreamName:    stage.TaskStream,
		ConsumerName:  "test-persona-bridge-" + l.namespace,
		FilterSubject: stage.TaskSubjectPrefix + "*",
		// "new", not "all". These tests share one broker and therefore one
		// AGENT stream, and a fresh durable consumer with "all" replays every
		// earlier test's task into THIS world's bridge — which then drives the
		// real terminal tools against another namespace's turns and hands this
		// test somebody else's task to assert against. Production intake uses
		// "all" for the opposite reason (an action published while the engine
		// was down must not be skipped); a per-test stand-in wants only what it
		// caused.
		DeliverPolicy: "new",
		AckPolicy:     "explicit",
		AckWait:       20 * time.Second,
	}, 5*time.Second, handle); err != nil {
		t.Fatalf("bind the persona bridge: %v", err)
	}
}

// pausedRoles lets a test hold a persona stage still so the turn stops in a
// known phase.
var pausedRoles atomic.Value

func (l *loop) paused(task agentic.TaskMessage) bool {
	roles, _ := pausedRoles.Load().(map[string]bool)
	return roles[task.Role]
}

func pause(t *testing.T, roles ...persona.Role) {
	t.Helper()
	held := map[string]bool{}
	for _, role := range roles {
		held[string(role)] = true
	}
	pausedRoles.Store(held)
	t.Cleanup(func() { pausedRoles.Store(map[string]bool{}) })
}

func decodeTask(data []byte) (agentic.TaskMessage, bool) {
	var envelope struct {
		Payload agentic.TaskMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return agentic.TaskMessage{}, false
	}
	if len(envelope.Payload.Tools) == 0 {
		return agentic.TaskMessage{}, false
	}
	return envelope.Payload, true
}

// rollingVerdict calls for the dice and declares all three bands, which is the
// only shape the engine accepts when a roll is required — the band is chosen by
// the seed, never by the script (F19).
func (l *loop) rollingVerdict() map[string]any {
	return map[string]any{
		"scalars": map[string]any{
			"plausibility":  string(vocabulary.PlausibilityPlausible),
			"risk":          string(vocabulary.RiskModerate),
			"consequence":   string(vocabulary.ConsequenceHarm),
			"requires_roll": true,
		},
		"modifiers": []any{},
		"bands": map[string]any{
			string(vocabulary.BandMiss):    l.statusIntent(l.characterID, vocabulary.StatusWounded),
			string(vocabulary.BandPartial): l.statusIntent(l.characterID, vocabulary.StatusExhausted),
			string(vocabulary.BandFull):    l.statusIntent(l.characterID, vocabulary.StatusHealthy),
		},
		"rationale": "The gate is heavy but the fiction allows it.",
	}
}

// autoVerdict declines the dice, so the turn carries one `auto` band and no roll
// triple is ever created for it.
func (l *loop) autoVerdict(target string) map[string]any {
	return map[string]any{
		"scalars": map[string]any{
			"plausibility":  string(vocabulary.PlausibilityCertain),
			"risk":          string(vocabulary.RiskNone),
			"consequence":   string(vocabulary.ConsequenceNone),
			"requires_roll": false,
		},
		"modifiers": []any{},
		"bands":     map[string]any{string(vocabulary.BandAuto): l.statusIntent(target, vocabulary.StatusHidden)},
		"rationale": "Nothing is at stake; the fiction already decided.",
	}
}

func (l *loop) statusIntent(target string, status vocabulary.Status) []any {
	return []any{map[string]any{
		"type": string(vocabulary.EffectSetStatus), "target": target, "status": string(status),
	}}
}

// submit accepts a player action exactly as intake would, and returns the turn
// the chain will carry.
func (l *loop) submit(t *testing.T, actionID, text string) (turnID, entityID string) {
	t.Helper()
	action := &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   l.playerID,
		CampaignID: composeID(t, l.namespace, "campaign", "instance"),
		SceneID:    l.sceneID,
		Text:       text,
		ArrivedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "ws://local/1"},
	}
	acceptance, err := l.recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("accept action: %v", err)
	}
	if !acceptance.Created {
		t.Fatalf("action %s did not create a turn", actionID)
	}
	return acceptance.TurnID, acceptance.TurnEntityID
}

// awaitPhase polls the turn's recorded phase, which is the only place the chain's
// progress is authoritative.
func (l *loop) awaitPhase(t *testing.T, entityID string, want vocabulary.TurnPhase) *graph.EntityState {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		state, err := l.harness.QueryEntity(t.Context(), entityID)
		if err == nil && !state.IsStub() {
			if object := testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String()); object != nil {
				last = fmt.Sprint(object)
				if last == string(want) {
					return state
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("turn %s never reached phase %q (last seen %q)", entityID, want, last)
	return nil
}

// The whole point. Nothing below calls a stage: a player action becomes a turn
// entity, the rule pack sees it in ENTITY_STATES, and the turn walks every phase
// on rule triggers alone.
func TestTurnLoop_WalksItsPhasesOnRuleTriggersAlone(t *testing.T) {
	world := startLoop(t)
	_, entityID := world.submit(t, "act-walk", "I shoulder the gate open.")

	state := world.awaitPhase(t, entityID, vocabulary.PhaseComplete)
	world.requireEveryTriggerAcknowledged(t)
	if runs := world.runs.snapshot(); len(runs) != len(rulepack.StagePhases()) {
		t.Errorf("stage runs = %v; every hop should have run exactly once", runs)
	} else {
		for phase, count := range runs {
			if count != 1 {
				t.Errorf("the %s stage ran %d times for one clean turn; each extra run is duplicate work",
					phase, count)
			}
		}
	}

	for _, predicate := range []vocabulary.Predicate{
		vocabulary.TurnVerdictRef,
		vocabulary.TurnRollBand,
		vocabulary.TurnRollRef,
		vocabulary.TurnEffectsBatch,
		vocabulary.TurnEffectsRef,
		vocabulary.TurnNarrationRef,
	} {
		objects := testinfra.ObjectsFor(state, predicate.String())
		if len(objects) != 1 {
			t.Errorf("completed turn holds %d values for %s, want exactly one", len(objects), predicate)
		}
	}
	if phases := testinfra.ObjectsFor(state, vocabulary.TurnPhaseCurrent.String()); len(phases) != 1 {
		t.Fatalf("completed turn holds %d phases; single-valued facts must replace, never append", len(phases))
	}

	// The world actually changed, through the effect the verdict declared for
	// whichever band the seeded dice selected.
	character := world.harness.AwaitEntity(t, world.characterID)
	statuses := testinfra.ObjectsFor(character, vocabulary.CharacterStatusCurrent.String())
	if len(statuses) != 1 {
		t.Fatalf("character holds %d statuses after one turn, want one", len(statuses))
	}
	band := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnRollBand.String()))
	want := map[string]string{
		string(vocabulary.BandMiss):    string(vocabulary.StatusWounded),
		string(vocabulary.BandPartial): string(vocabulary.StatusExhausted),
		string(vocabulary.BandFull):    string(vocabulary.StatusHealthy),
	}[band]
	if got := fmt.Sprint(statuses[0]); got != want {
		t.Errorf("the %s band committed status %q, want %q; the applier committed the wrong band's intents",
			band, got, want)
	}
}

// A redelivered stage trigger must not buy a second billed model call. The turn
// is held in `adjudicating` with its verdict already recorded, the trigger is
// replayed by hand onto the real stream, and the spawner is expected to skip.
func TestTurnLoop_ARedeliveredPersonaTriggerDoesNotRespawnTheModel(t *testing.T) {
	world := startLoop(t)
	_, entityID := world.submit(t, "act-resume", "I listen at the door.")

	// Wait for the first adjudication task, then for its verdict to land.
	select {
	case <-world.spawned:
	case <-time.After(45 * time.Second):
		t.Fatal("the adjudicator was never spawned")
	}
	world.awaitPhase(t, entityID, vocabulary.PhaseComplete)
	drain(world.spawned)

	subject, err := rulepack.SubjectForPhase(vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	replay, err := json.Marshal(map[string]any{"entity_id": entityID, "subject": subject, "source": "test-replay"})
	if err != nil {
		t.Fatalf("encode replay: %v", err)
	}
	if err := world.harness.Client.PublishToStream(t.Context(), subject, replay); err != nil {
		t.Fatalf("replay the adjudication trigger: %v", err)
	}

	select {
	case task := <-world.spawned:
		t.Fatalf("a redelivered trigger spawned %s again; every duplicate delivery would cost a model call",
			task.Role)
	case <-time.After(5 * time.Second):
	}

	state, err := world.harness.QueryEntity(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read turn: %v", err)
	}
	if got := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String())); got != string(vocabulary.PhaseComplete) {
		t.Errorf("the replayed trigger moved a completed turn to %q", got)
	}
}

// A stage trigger naming something that is not a turn can never become a stage,
// so it is terminated rather than redelivered forever.
func TestStageRunner_TerminatesATriggerThatNamesNoTurn(t *testing.T) {
	world := startLoop(t)

	subject, err := rulepack.SubjectForPhase(vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	bad, err := json.Marshal(map[string]any{"entity_id": world.sceneID, "subject": subject, "source": "test"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := world.harness.Client.PublishToStream(t.Context(), subject, bad); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The turn loop keeps working afterwards, which is the property that matters:
	// a poison trigger must not wedge the stage.
	_, entityID := world.submit(t, "act-after-poison", "I step through the gate.")
	world.awaitPhase(t, entityID, vocabulary.PhaseComplete)
}

// The pack is data the rule processor accepts, and the registration pass is what
// makes that true.
func TestRulePack_LoadsIntoTheRealRuleProcessorAndRefusesAnUndeclaredPredicate(t *testing.T) {
	testinfra.Require(t)

	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatalf("ProcessorConfig: %v", err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("the pack's own config did not validate: %v", err)
	}

	// Now the anti-vacuity control, against the same validator: clear the
	// registry and the identical config is refused.
	restore := ssvocab.SnapshotRegistry()
	ssvocab.ClearRegistry()
	err = config.Validate()
	restore()
	if err == nil {
		t.Fatal("the rule config validated with an empty predicate registry")
	}
}

// runLog counts how often each stage actually RAN, because "the turn reached
// complete" says nothing about how many times a hop was driven to get there —
// and a duplicate stage run is a duplicate bill.
type runLog struct {
	mu     sync.Mutex
	counts map[vocabulary.TurnPhase]int
}

func (r *runLog) wrap(stages ...stage.Stage) []stage.Stage {
	out := make([]stage.Stage, 0, len(stages))
	for _, s := range stages {
		out = append(out, countingStage{inner: s, log: r})
	}
	return out
}

func (r *runLog) note(phase vocabulary.TurnPhase) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[phase]++
}

func (r *runLog) snapshot() map[vocabulary.TurnPhase]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[vocabulary.TurnPhase]int, len(r.counts))
	for phase, count := range r.counts {
		out[phase] = count
	}
	return out
}

type countingStage struct {
	inner stage.Stage
	log   *runLog
}

func (c countingStage) Phase() vocabulary.TurnPhase { return c.inner.Phase() }

func (c countingStage) Run(ctx context.Context, trigger stage.Trigger) error {
	c.log.note(c.inner.Phase())
	return c.inner.Run(ctx, trigger)
}

// requireEveryTriggerAcknowledged proves the stage consumers actually finished.
//
// It exists because "the turn reached complete" is a WEAKER claim than it looks:
// a stage whose handler failed AFTER doing the work leaves an unacknowledged
// delivery that JetStream will hand back forever, and the turn still completes.
// The ack floor is the only place that shows it.
func (l *loop) requireEveryTriggerAcknowledged(t *testing.T) {
	t.Helper()
	js, err := l.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	for _, phase := range rulepack.StagePhases() {
		consumer, err := js.Consumer(t.Context(), rulepack.StageStream, "semmachina-stage-"+string(phase))
		if err != nil {
			t.Fatalf("read the %s consumer: %v", phase, err)
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the %s consumer info: %v", phase, err)
		}
		if info.NumAckPending != 0 || info.NumPending != 0 {
			t.Errorf("the %s stage left %d unacknowledged and %d unread trigger(s); the turn completed anyway, "+
				"which is how a failing stage hides", phase, info.NumAckPending, info.NumPending)
		}
	}
}

// The no-roll path (design D3): the dice are skipped entirely and no roll
// triple is ever created for the turn.
func TestTurnLoop_ANoRollVerdictReachesApplicationWithoutTheDice(t *testing.T) {
	world := startLoop(t)
	world.verdict = func() map[string]any { return world.autoVerdict(world.characterID) }

	_, entityID := world.submit(t, "act-noroll", "I stand still and listen.")
	state := world.awaitPhase(t, entityID, vocabulary.PhaseComplete)

	for _, predicate := range []vocabulary.Predicate{
		vocabulary.TurnRollBand, vocabulary.TurnRollTotal, vocabulary.TurnRollRef,
	} {
		if objects := testinfra.ObjectsFor(state, predicate.String()); len(objects) != 0 {
			t.Errorf("a no-roll turn carries %s = %v; the dice were never authorised", predicate, objects)
		}
	}
	if runs := world.runs.snapshot(); runs[vocabulary.PhaseResolving] != 0 {
		t.Errorf("the dice stage ran %d times for a verdict that declined them", runs[vocabulary.PhaseResolving])
	}
	world.requireEveryTriggerAcknowledged(t)

	character := world.harness.AwaitEntity(t, world.characterID)
	if got := fmt.Sprint(testinfra.FirstObject(character, vocabulary.CharacterStatusCurrent.String())); got !=
		string(vocabulary.StatusHidden) {
		t.Errorf("the auto band committed status %q, want %q", got, vocabulary.StatusHidden)
	}
}

// Rejection is a normal path. A well-formed intent naming a wrong-KIND target
// ends the turn explicitly, with a closed code and a stored explanation, and
// leaves the world alone.
func TestTurnLoop_ARefusedEffectBatchEndsTheTurnExplicitly(t *testing.T) {
	world := startLoop(t)
	world.verdict = func() map[string]any { return world.autoVerdict(world.sceneID) }

	_, entityID := world.submit(t, "act-refused", "I make the room itself go quiet.")
	state := world.awaitPhase(t, entityID, vocabulary.PhaseFailed)

	reason := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnFailureReason.String()))
	if _, err := vocabulary.ParseFailureReason(reason); err != nil {
		t.Fatalf("the turn failed with %q, which is not a closed reason code: %v", reason, err)
	}
	if reason != string(vocabulary.FailureEffectEntityKind) {
		t.Errorf("failure reason = %q, want %q", reason, vocabulary.FailureEffectEntityKind)
	}

	detail := testinfra.FirstObject(state, vocabulary.TurnFailureRef.String())
	if detail == nil {
		t.Fatal("the failed turn carries no explanation reference")
	}
	ref, err := content.ParseRef(fmt.Sprint(detail))
	if err != nil {
		t.Fatalf("the recorded detail reference does not resolve: %v", err)
	}
	stored, err := world.content.GetFailureDetail(t.Context(), ref)
	if err != nil {
		t.Fatalf("the referenced explanation is not in the store: %v", err)
	}
	if stored.Reason != vocabulary.FailureEffectEntityKind {
		t.Errorf("the stored explanation reports %q", stored.Reason)
	}

	// The world is untouched, and narration never ran for a turn that failed.
	scene := world.harness.AwaitEntity(t, world.sceneID)
	if objects := testinfra.ObjectsFor(scene, vocabulary.CharacterStatusCurrent.String()); len(objects) != 0 {
		t.Errorf("the refused batch still wrote %v onto the scene", objects)
	}
	if objects := testinfra.ObjectsFor(state, vocabulary.TurnNarrationRef.String()); len(objects) != 0 {
		t.Errorf("a failed turn was narrated: %v", objects)
	}
}

// A persona that never exits must not leave a player waiting. The loop's failure
// event is published exactly as the agentic loop publishes it, and the turn ends
// with the closed cap-exhaustion code.
func TestTurnLoop_AnExhaustedPersonaEndsTheTurnRatherThanStalling(t *testing.T) {
	world := startLoop(t)
	pause(t, persona.RoleAdjudicator)

	_, entityID := world.submit(t, "act-capped", "I argue with the door for an hour.")

	var task agentic.TaskMessage
	select {
	case task = <-world.spawned:
	case <-time.After(45 * time.Second):
		t.Fatal("the adjudicator was never spawned")
	}
	world.awaitPhase(t, entityID, vocabulary.PhaseAdjudicating)

	watcher, err := stage.NewCapWatcher(
		world.harness.Client, message.NewDecoder(world.harness.Registry), world.recorder, world.content)
	if err != nil {
		t.Fatalf("NewCapWatcher: %v", err)
	}
	subscription, err := watcher.Start(context.Background())
	if err != nil {
		t.Fatalf("start cap watcher: %v", err)
	}
	t.Cleanup(func() { _ = subscription.Unsubscribe() })

	failure := &agentic.LoopFailedEvent{
		LoopID:     "loop-" + task.TaskID,
		TaskID:     task.TaskID,
		Outcome:    agentic.OutcomeFailed,
		Reason:     stage.ReasonMaxIterations,
		Error:      "max iterations (3) reached",
		Role:       task.Role,
		Iterations: persona.AdjudicatorMaxIterations,
		Metadata:   task.Metadata,
	}
	data, err := json.Marshal(message.NewBaseMessage(failure.Schema(), failure, "agentic-loop"))
	if err != nil {
		t.Fatalf("encode loop failure: %v", err)
	}
	if err := world.harness.Client.Publish(t.Context(), "agent.failed."+failure.LoopID, data); err != nil {
		t.Fatalf("publish loop failure: %v", err)
	}

	state := world.awaitPhase(t, entityID, vocabulary.PhaseFailed)
	if got := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnFailureReason.String())); got !=
		string(vocabulary.FailurePersonaCapExhausted) {
		t.Errorf("failure reason = %q, want %q", got, vocabulary.FailurePersonaCapExhausted)
	}
	if testinfra.FirstObject(state, vocabulary.TurnFailureRef.String()) == nil {
		t.Error("the cap-exhausted turn carries no explanation reference")
	}
}

func drain(tasks chan agentic.TaskMessage) {
	for {
		select {
		case <-tasks:
		default:
			return
		}
	}
}
