package boot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	sspersona "github.com/c360studio/semstreams/persona"
	"github.com/c360studio/semstreams/pkg/lifecycle"
	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/mockmodel"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// These tests boot the REAL engine — graph-ingest, graph-index, the rule
// processor and the agentic loop, all of it — against a real broker, because a
// composition is the one thing a unit test cannot be wrong about usefully. Every
// substitute here would hide the failure it was meant to catch: a fake broker
// cannot refuse a consumer whose filter its stream does not capture, a stub
// graph-ingest mints no referential stubs, and a hand-called step proves the step
// and nothing about the order.
//
// Nothing in this file uses internal/testinfra's harness, and that is not an
// oversight: that harness starts graph-ingest and graph-index ITSELF, and a
// second pair against the same broker would bind the same durable consumers. What
// is shared with it is the opt-out policy, so a run without Docker is loud here
// too.

var broker struct {
	client *natsclient.TestClient
	err    error
}

func TestMain(m *testing.M) {
	// Registration precedes any rule config load, in every binary and every test:
	// the rule processor rejects a canonical-but-undeclared predicate, so without
	// this the turn-sequencing pack simply does not start.
	if err := vocabulary.RegisterPredicates(); err != nil {
		fmt.Fprintf(os.Stderr, "register semmachina predicates: %v\n", err)
		os.Exit(1)
	}
	broker.client, broker.err = startBroker()
	if broker.err == nil {
		broker.err = keepComponentStatusHistory(broker.client)
	}
	if broker.err != nil && testinfra.Skipped() {
		fmt.Fprintf(os.Stderr,
			"\n================================================================\n"+
				" REAL-INFRASTRUCTURE BOOT TESTS SKIPPED BY %s\n reason: %v\n"+
				" The boot ORDER is not exercised by this run.\n"+
				"================================================================\n\n",
			testinfra.SkipEnv, broker.err)
	}
	code := m.Run()
	if broker.client != nil {
		_ = broker.client.Terminate()
	}
	os.Exit(code)
}

func startBroker() (*natsclient.TestClient, error) {
	if testinfra.Skipped() {
		return nil, fmt.Errorf("%s is set", testinfra.SkipEnv)
	}
	ctx := context.Background()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return nil, fmt.Errorf("docker provider unavailable: %w", err)
	}
	if err := provider.Health(ctx); err != nil {
		return nil, fmt.Errorf("docker daemon is not reachable: %w", err)
	}
	// A bare broker: no streams, no components. Everything the engine needs, the
	// engine creates — which is the claim these tests are checking.
	return natsclient.NewSharedTestClient(natsclient.WithJetStream())
}

// keepComponentStatusHistory pre-creates COMPONENT_STATUS in the catalog shape.
//
// beta.159's framework bucket catalog owns its History=1 declaration and
// reconciles every reporter acquisition to it, so a delete followed by the
// processor's status write intentionally leaves only the write observable.
func keepComponentStatusHistory(client *natsclient.TestClient) error {
	_, err := client.Client.CreateKeyValueBucket(context.Background(), jetstream.KeyValueConfig{
		Bucket:      "COMPONENT_STATUS",
		Description: "Component lifecycle status tracking",
		History:     1,
	})
	return err
}

func requireBroker(t *testing.T) *natsclient.TestClient {
	t.Helper()
	if broker.client != nil {
		return broker.client
	}
	if testinfra.Skipped() {
		t.Skipf("SKIPPED by %s — this test proved nothing in this run", testinfra.SkipEnv)
		return nil
	}
	t.Fatalf("a real NATS broker is required and unavailable: %v\n"+
		"This test boots the production composition; there is no substitute for it.\n"+
		"Start Docker, or set %s=1 to run the rest of the suite without this proof.",
		broker.err, testinfra.SkipEnv)
	return nil
}

var worldCounter atomic.Int64

type bootTestActionStore struct{ instance string }

func (s bootTestActionStore) PutAction(
	_ context.Context, turnEntityID string, _ *payload.PlayerAction,
) (content.Ref, error) {
	return content.Ref{Instance: s.instance, Key: "turn/" + turnEntityID + "/action"}, nil
}

// bootConfig gives each test its own world namespace, so two boots against the
// shared broker are disjoint campaigns rather than a race for one.
func bootConfig(t *testing.T) boot.Config {
	t.Helper()
	client := requireBroker(t)
	cfg := testConfig(t)
	cfg.NATSURL = client.URL
	cfg.WorldNS = fmt.Sprintf("w%d", worldCounter.Add(1))
	cfg.ContentBucket = "BOOT_" + strings.ToUpper(cfg.WorldNS)
	// Short enough that a genuine failure is a test failure rather than a
	// timeout, long enough for a real import to land on a container.
	cfg.ReadyTimeout = 45 * time.Second
	cfg.ReadyPoll = 100 * time.Millisecond
	return cfg
}

func startEngine(t *testing.T, cfg boot.Config) *boot.Engine {
	t.Helper()
	engine := newTestEngine(t, cfg)
	t.Cleanup(engine.Stop)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatalf("boot: %v", err)
	}
	return engine
}

// The whole composition starts, in order, against real infrastructure — and
// every stream, bucket and consumer it needs is one it created itself.
func TestBoot_StartsTheWholeEngineOnABareBroker(t *testing.T) {
	cfg := bootConfig(t)
	engine := startEngine(t, cfg)

	if engine.Addr() == "" {
		t.Fatal("the player socket is not listening; ingress is the last step and it did not run")
	}

	js := jetStream(t)
	for _, name := range []string{
		world.EntityStream, rulepack.StageStream, persona.TaskStream, turn.ActionStream,
	} {
		if _, err := js.Stream(t.Context(), name); err != nil {
			t.Errorf("the %s stream does not exist after boot: %v", name, err)
		}
	}

	// Every stage bound a durable consumer, and none of them is holding an
	// unacknowledged delivery. A JetStream publish is a core publish underneath,
	// so a missing stream can look like it worked while leaving deliveries
	// unacked — the ack floor is where that shows.
	requireStagesIdle(t, js)

	// The agentic loop bound its task consumer. Without one, the stranded-turn
	// pass cannot tell an in-flight persona from an idle one — and it refuses
	// rather than guessing, which means this is also why the boot got this far.
	if !consumerExistsOn(t, js, persona.TaskStream, persona.TaskSubjectFilter) {
		t.Error("no consumer filters " + persona.TaskSubjectFilter + "; no persona could ever run")
	}
}

// Lifecycle attachment happens after world import and is attach-only: a restart
// may register the workflow again, but it must never reset the case's phase.
func TestBoot_CaseLifecycleAttachesAfterImportAndPreservesPhaseOnRestart(t *testing.T) {
	worldFS, err := fs.Sub(fixtures.Worlds(), "worlds/bellweather-maze")
	if err != nil {
		t.Fatalf("Bellweather fixture: %v", err)
	}
	cfg := bootConfig(t)
	cfg.World = worldFS
	cfg.SceneLocalID = "fete-green"
	cfg.Player.Character = "local:rowan-vale"

	first := newTestEngine(t, cfg)
	if err := first.StartThrough(t.Context(), boot.StepLifecycle); err != nil {
		t.Fatalf("first boot through lifecycle: %v", err)
	}
	caseID := fmt.Sprintf("%s.semmachina.%s.bellweather-maze.case.bellweather-case", cfg.Org, cfg.WorldNS)

	manager := lifecycle.NewManager(requireBroker(t).Client, nil)
	if err := manager.Register(caseflow.Workflow()); err != nil {
		t.Fatalf("register independent lifecycle reader: %v", err)
	}
	state, err := manager.Get(t.Context(), caseflow.WorkflowName, caseID)
	if err != nil {
		t.Fatalf("read attached case: %v", err)
	}
	if state.Phase() != string(vocabulary.CasePhaseColdOpen) {
		t.Fatalf("initial phase = %q", state.Phase())
	}
	if err := manager.Transition(t.Context(), caseflow.WorkflowName, caseID,
		string(vocabulary.CasePhaseDiscovery), lifecycle.TransitionSourceComponent, "restart proof"); err != nil {
		t.Fatalf("advance case before restart: %v", err)
	}
	first.Stop()

	second := newTestEngine(t, cfg)
	t.Cleanup(second.Stop)
	if err := second.StartThrough(t.Context(), boot.StepLifecycle); err != nil {
		t.Fatalf("restart through lifecycle: %v", err)
	}
	state, err = manager.Get(t.Context(), caseflow.WorkflowName, caseID)
	if err != nil {
		t.Fatalf("read case after restart: %v", err)
	}
	if state.Phase() != string(vocabulary.CasePhaseDiscovery) {
		t.Fatalf("phase after restart = %q, want discovery", state.Phase())
	}
}

// The private casekeeper boundary is proved from authored Bellweather data all
// the way to the provider-shaped bytes. A hand-written chat request can prove
// only that the mock records what it is handed; it says nothing about whether
// Scope, Projector, Builder.Interpret, or Casekeeper().Task leaked or omitted a
// fact before the production model client serialized the request.
func TestBoot_BellweatherCasekeeperProjectionReachesTheActualModelCallBody(t *testing.T) {
	worldFS, err := fs.Sub(fixtures.Worlds(), "worlds/bellweather-maze")
	if err != nil {
		t.Fatalf("Bellweather fixture: %v", err)
	}
	cfg := bootConfig(t)
	cfg.World = worldFS
	cfg.SceneLocalID = "fete-green"
	cfg.Player.Character = "local:rowan-vale"
	prefix := fmt.Sprintf("%s.semmachina.%s.bellweather-maze", cfg.Org, cfg.WorldNS)
	fixture := bellweatherPrivateWireFixture(t, prefix)
	handler, err := mockmodel.New(fixture, "bellweather-private-wire")
	if err != nil {
		t.Fatalf("start casekeeper fixture: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg.Models = testModels()
	cfg.Models.Endpoints["stub"] = mockmodel.EndpointConfig(server.URL, "one-local-model")
	engine := startEngine(t, cfg)

	foreignID := prefix + ".evidence.foreign-wire-canary"
	const foreignText = "FOREIGN-UNAUTHORIZED-WIRE-CANARY"
	seedForeignPrivateCanary(t, foreignID, foreignText)

	const actionText = "ACTION-PRIVATE-WIRE-CANARY: I inspect the freshly cut wire."
	response := submit(t, engine, cfg, "bellweather-private-wire", actionText)
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the Bellweather action: %+v", response.Refusal)
	}

	call := awaitCasekeeperCall(t, handler)
	var rawRequest struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(call.Body, &rawRequest); err != nil {
		t.Fatalf("decode production casekeeper Call.Body: %v", err)
	}
	offered := make([]string, 0, len(rawRequest.Tools))
	for _, tool := range rawRequest.Tools {
		offered = append(offered, tool.Function.Name)
	}
	if !slices.Equal(offered, []string{persona.CaseDecisionToolName}) {
		t.Fatalf("casekeeper Call.Body offered tools %v, want only %q", offered, persona.CaseDecisionToolName)
	}
	for _, authorized := range []string{
		prefix + ".case.bellweather-case",
		"Death in the Bellweather Maze",
		prefix + ".character.judith-bell",
		"Judith Bell",
		prefix + ".evidence.evidence-wire",
		"Freshly cut wire end",
		actionText,
	} {
		if !bytes.Contains(call.Body, []byte(authorized)) {
			t.Fatalf("production casekeeper Call.Body lacks authorized canary %q: %s", authorized, call.Body)
		}
	}
	for _, forbidden := range []string{foreignID, foreignText} {
		if bytes.Contains(call.Body, []byte(forbidden)) {
			t.Fatalf("production casekeeper Call.Body leaked unauthorized canary %q: %s", forbidden, call.Body)
		}
	}
	failed := awaitBellweatherTurnFailed(t, cfg, response.TurnID)
	if got := fmt.Sprint(testinfra.FirstObject(failed, vocabulary.TurnFailureReason.String())); got != string(vocabulary.FailureCaseProgressInvalid) {
		t.Fatalf("investigate-first turn failure = %q, want %q", got, vocabulary.FailureCaseProgressInvalid)
	}
	if got := testinfra.FirstObject(failed, vocabulary.TurnCaseProgressRef.String()); got != nil {
		t.Fatalf("failed investigate-first turn carried case-progress ref %v", got)
	}
	caseState, err := graphStore(t).GetEntity(t.Context(), prefix+".case.bellweather-case")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(testinfra.FirstObject(caseState, vocabulary.CaseLifecyclePhase.String())); got != string(vocabulary.CasePhaseColdOpen) {
		t.Fatalf("case phase after invalid investigate-first turn = %q, want cold_open", got)
	}
	awaitStageConsumersSettled(t, time.Now().Add(30*time.Second))
	engine.Stop()

	// The global durable may retain old messages across sequential world boots.
	// A later world must terminate this already-handled delivery by identity
	// before its world-bound content store or recorder sees it.
	later := bootConfig(t)
	startEngine(t, later)
	requireStagesIdle(t, jetStream(t))
}

func bellweatherPrivateWireFixture(t *testing.T, prefix string) *mockmodel.Fixture {
	t.Helper()
	raw := fmt.Sprintf(`{
	  "roles": [
	    {"name":"casekeeper","match":{"tools":["submit_case_decision"]}},
	    {"name":"adjudicator","match":{"tools":["submit_verdict"]}},
	    {"name":"narrator","match":{"tools":["submit_narration"]}}
	  ],
	  "scenarios": [{
	    "name":"bellweather-private-wire",
	    "scripts":[
	      {"role":"casekeeper","steps":[{
	        "kind":"tool_call",
	        "usage":{"prompt_tokens":10,"completion_tokens":5},
	        "tool_calls":[{"name":"submit_case_decision","arguments":{
	          "kind":"investigate","target_refs":[],"reveal_refs":[],
	          "culprit_ref":"","method_ref":"","motive_ref":""
	        }}]
	      }]},
	      {"role":"adjudicator","steps":[{
	        "kind":"tool_call",
	        "usage":{"prompt_tokens":10,"completion_tokens":5},
	        "tool_calls":[{"name":"submit_verdict","arguments":{
	          "scalars":{"plausibility":"certain","risk":"none","consequence":"none","requires_roll":false},
	          "modifiers":[],
	          "bands":{"auto":[{"type":"set_status","target":"%s.character.rowan-vale","status":"healthy"}]},
	          "rationale":"The clue is visible and no uncertainty remains."
	        }}]
	      }]},
	      {"role":"narrator","steps":[{
	        "kind":"tool_call",
	        "usage":{"prompt_tokens":10,"completion_tokens":5},
	        "tool_calls":[{"name":"submit_narration","arguments":{
	          "prose":"The bright wire end catches the afternoon light."
	        }}]
	      }]}
	    ]
	  }]
	}`, prefix)
	fixture, err := mockmodel.ParseFixture([]byte(raw))
	if err != nil {
		t.Fatalf("parse complete Bellweather wire fixture: %v", err)
	}
	return fixture
}

func awaitBellweatherTurnFailed(t *testing.T, cfg boot.Config, turnID string) *graph.EntityState {
	t.Helper()
	identity := turn.Identity{Org: cfg.Org, WorldNS: cfg.WorldNS, Template: "bellweather-maze"}
	entityID, err := identity.EntityID(turnID)
	if err != nil {
		t.Fatalf("compose Bellweather turn entity ID: %v", err)
	}
	store := graphStore(t)
	deadline := time.Now().Add(30 * time.Second)
	for {
		state, readErr := store.GetEntity(t.Context(), entityID)
		if readErr == nil && !state.IsStub() {
			phase := vocabulary.TurnPhase(fmt.Sprint(
				testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String()),
			))
			switch phase {
			case vocabulary.PhaseComplete:
				t.Fatalf("invalid investigate-first Bellweather turn unexpectedly completed: %v", state)
			case vocabulary.PhaseFailed:
				return state
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Bellweather wire-proof turn %s did not reach failed: %v", entityID, readErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func awaitStageConsumersSettled(t *testing.T, deadline time.Time) {
	t.Helper()
	js := jetStream(t)
	for {
		settled := true
		for _, phase := range rulepack.StagePhases() {
			consumer, err := js.Consumer(t.Context(), rulepack.StageStream, rulepack.StageConsumerName(phase))
			if err != nil {
				t.Fatalf("read %s stage consumer while settling Bellweather turn: %v", phase, err)
			}
			info, err := consumer.Info(t.Context())
			if err != nil {
				t.Fatalf("inspect %s stage consumer while settling Bellweather turn: %v", phase, err)
			}
			if info.NumPending != 0 || info.NumAckPending != 0 {
				settled = false
				break
			}
		}
		if settled {
			for _, name := range []string{
				rulepack.KnowledgeConsumerName, rulepack.AccusationConsumerName, rulepack.CaseProgressConsumerName,
			} {
				consumer, err := js.Consumer(t.Context(), rulepack.StageStream, name)
				if err != nil {
					t.Fatalf("read auxiliary consumer %s while settling Bellweather turn: %v", name, err)
				}
				info, err := consumer.Info(t.Context())
				if err != nil {
					t.Fatalf("inspect auxiliary consumer %s while settling Bellweather turn: %v", name, err)
				}
				if info.NumPending != 0 || info.NumAckPending != 0 {
					settled = false
					break
				}
			}
		}
		if settled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Bellweather wire-proof turn reached complete but stage consumers did not settle")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func seedForeignPrivateCanary(t *testing.T, entityID, text string) {
	t.Helper()
	at := time.Now().UTC()
	state := &graph.EntityState{
		ID: entityID,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version: 1, UpdatedAt: at,
		Triples: []message.Triple{
			{Subject: entityID, Predicate: vocabulary.WorldEntityKind.String(),
				Object: string(vocabulary.EntityKindEvidence), Source: "test", Timestamp: at, Confidence: 1},
			{Subject: entityID, Predicate: vocabulary.WorldEntityName.String(),
				Object: text, Source: "test", Timestamp: at, Confidence: 1},
		},
	}
	store := graphStore(t)
	if _, err := store.CreateEntity(t.Context(), state); err != nil {
		t.Fatalf("seed foreign private canary: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, err := store.GetEntity(t.Context(), entityID)
		if err == nil && !got.IsStub() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("foreign private canary never became readable: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func awaitCasekeeperCall(t *testing.T, handler *mockmodel.Handler) mockmodel.Call {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		calls := handler.CallsFor(string(persona.RoleCasekeeper))
		if len(calls) > 0 {
			if len(calls) != 1 {
				t.Fatalf("casekeeper made %d calls before its first decision landed", len(calls))
			}
			return calls[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("production Bellweather turn never reached the casekeeper model endpoint")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A failure emitted before a restart is terminal evidence from a persona that
// already ran. Boot must settle that evidence before it exposes an older stage
// trigger to the spawner, or one turn can buy a second model call during the
// restart window.
func TestBoot_QueuedLoopFailureWinsOverAnOlderPersonaTrigger(t *testing.T) {
	engine, cfg, modelCalls := startFailureRestartProof(t)

	store, recorder := restartProofRecorder(t, cfg)
	actionID := "queued-failure"
	accepted := acceptAndParkRestartProofTurn(t, recorder, engine, actionID)
	triggerSequence := queueOldAdjudicatingTrigger(t, accepted)
	taskID := string(persona.RoleAdjudicator) + "-" + accepted.TurnID
	queueRestartProofFailure(t, engine, accepted, actionID, taskID)

	requireFailureCatchUp(t, engine, store, accepted.TurnEntityID)
	agentStream := streamNamed(t, persona.TaskStream)
	agentBefore := requireStreamInfo(t, agentStream, "AGENT before stage binding")
	if err := engine.StartThrough(t.Context(), boot.StepStages); err != nil {
		t.Fatalf("bind stages after failure catch-up: %v", err)
	}
	requireStageAcknowledged(t, triggerSequence)
	agentAfter := requireStreamInfo(t, agentStream, "AGENT after old trigger settled")
	if published := countStoredTask(t, agentStream, agentBefore.State.LastSeq+1, agentAfter.State.LastSeq, taskID); published != 0 {
		t.Fatalf("old trigger published %d persona tasks after its queued failure won", published)
	}
	if got := modelCalls.Load(); got != 0 {
		t.Fatalf("model endpoint received %d calls after queued failure terminalized the turn", got)
	}
}

func startFailureRestartProof(t *testing.T) (*boot.Engine, boot.Config, *atomic.Int64) {
	t.Helper()
	var modelCalls atomic.Int64
	modelServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		modelCalls.Add(1)
	}))
	t.Cleanup(modelServer.Close)

	cfg := bootConfig(t)
	cfg.Models = testModels()
	cfg.Models.Endpoints["stub"].URL = modelServer.URL
	engine := newTestEngine(t, cfg)
	t.Cleanup(engine.Stop)
	if err := engine.StartThrough(t.Context(), boot.StepResume); err != nil {
		t.Fatalf("boot through stranded-turn reconciliation: %v", err)
	}
	return engine, cfg, &modelCalls
}

func restartProofRecorder(t *testing.T, cfg boot.Config) (*graphio.Store, *turn.Recorder) {
	t.Helper()
	store := graphStore(t)
	recorder, err := turn.NewRecorder(
		store,
		bootTestActionStore{instance: cfg.ContentBucket},
		turn.Identity{Org: cfg.Org, WorldNS: cfg.WorldNS, Template: "starter"},
	)
	if err != nil {
		t.Fatalf("turn.NewRecorder: %v", err)
	}
	return store, recorder
}

func acceptAndParkRestartProofTurn(
	t *testing.T, recorder *turn.Recorder, engine *boot.Engine, actionID string,
) turn.Acceptance {
	t.Helper()
	action := &payload.PlayerAction{
		ActionID: actionID, PlayerID: engine.PlayerID(), CampaignID: engine.CampaignID(),
		SceneID: engine.SceneID(), Text: "I try the gate.", ArrivedAt: time.Now().UTC(),
		Channel: payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "restart-proof"},
	}
	accepted, err := recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("accept test turn: %v", err)
	}
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseInterpreting, vocabulary.PhaseAdjudicating,
	} {
		if _, err := recorder.Advance(
			t.Context(), accepted.TurnID, accepted.TurnEntityID, phase,
		); err != nil {
			t.Fatalf("park test turn in %s: %v", phase, err)
		}
	}
	return accepted
}

func queueOldAdjudicatingTrigger(t *testing.T, accepted turn.Acceptance) uint64 {
	t.Helper()
	stageSubject, err := rulepack.SubjectForPhase(vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("adjudicating subject: %v", err)
	}
	trigger, err := json.Marshal(map[string]any{
		"entity_id": accepted.TurnEntityID,
		"subject":   stageSubject,
		"source":    "restart-proof",
	})
	if err != nil {
		t.Fatalf("encode old stage trigger: %v", err)
	}
	triggerAck, err := requireBroker(t).Client.PublishToStreamWithAck(t.Context(), stageSubject, trigger)
	if err != nil {
		t.Fatalf("queue old persona-stage trigger: %v", err)
	}
	return triggerAck.Sequence
}

func queueRestartProofFailure(
	t *testing.T, engine *boot.Engine, accepted turn.Acceptance, actionID, taskID string,
) {
	t.Helper()
	loopID := "loop-" + taskID
	failure := &agentic.LoopFailedEvent{
		LoopID: loopID, TaskID: taskID, Outcome: agentic.OutcomeFailed,
		Reason: "handler_error", Error: "the pre-restart loop stopped",
		Role: string(persona.RoleAdjudicator), Iterations: 1,
		Metadata: map[string]any{
			persona.MetadataKeyTurnID:       accepted.TurnID,
			persona.MetadataKeyTurnEntityID: accepted.TurnEntityID,
			persona.MetadataKeyActionID:     actionID,
			persona.MetadataKeySceneID:      engine.SceneID(),
		},
	}
	failureData, err := json.Marshal(message.NewBaseMessage(failure.Schema(), failure, "agentic-loop"))
	if err != nil {
		t.Fatalf("encode queued loop failure: %v", err)
	}
	if err := requireBroker(t).Client.PublishToStream(
		t.Context(), "agent.failed."+loopID, failureData,
	); err != nil {
		t.Fatalf("queue loop failure: %v", err)
	}
}

func requireFailureCatchUp(
	t *testing.T, engine *boot.Engine, store *graphio.Store, turnEntityID string,
) {
	t.Helper()
	if err := engine.StartThrough(t.Context(), boot.StepLoopFailures); err != nil {
		t.Fatalf("boot did not catch up queued loop failures: %v", err)
	}
	failed, err := store.GetEntity(t.Context(), turnEntityID)
	if err != nil {
		t.Fatalf("read failed turn after catch-up: %v", err)
	}
	if got := testinfra.FirstObject(failed, vocabulary.TurnPhaseCurrent.String()); got !=
		string(vocabulary.PhaseFailed) {
		t.Fatalf("phase after failure catch-up = %v, want %s before stage consumers bind",
			got, vocabulary.PhaseFailed)
	}
}

func streamNamed(t *testing.T, name string) jetstream.Stream {
	t.Helper()
	stream, err := jetStream(t).Stream(t.Context(), name)
	if err != nil {
		t.Fatalf("read stream %s: %v", name, err)
	}
	return stream
}

func requireStreamInfo(t *testing.T, stream jetstream.Stream, label string) *jetstream.StreamInfo {
	t.Helper()
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read %s: %v", label, err)
	}
	return info
}

func requireStageAcknowledged(t *testing.T, triggerSequence uint64) {
	t.Helper()
	stageStream := streamNamed(t, rulepack.StageStream)
	consumer, err := stageStream.Consumer(
		t.Context(), rulepack.StageConsumerName(vocabulary.PhaseAdjudicating),
	)
	if err != nil {
		t.Fatalf("read adjudicating consumer: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, infoErr := consumer.Info(t.Context())
		if infoErr != nil {
			t.Fatalf("read adjudicating consumer state: %v", infoErr)
		}
		if info.AckFloor.Stream >= triggerSequence {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("old trigger sequence %d was not acknowledged; ack floor=%d pending=%d",
				triggerSequence, info.AckFloor.Stream, info.NumAckPending)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func countStoredTask(
	t *testing.T, agentStream jetstream.Stream, first, last uint64, taskID string,
) int {
	t.Helper()
	publishedTasks := 0
	for seq := first; seq <= last; seq++ {
		raw, getErr := agentStream.GetMsg(t.Context(), seq)
		if getErr != nil {
			if errors.Is(getErr, jetstream.ErrMsgNotFound) {
				continue
			}
			t.Fatalf("read AGENT sequence %d: %v", seq, getErr)
		}
		var envelope struct {
			Payload agentic.TaskMessage `json:"payload"`
		}
		if json.Unmarshal(raw.Data, &envelope) == nil && envelope.Payload.TaskID == taskID {
			publishedTasks++
		}
	}
	return publishedTasks
}

// The world lands whole: every planned entity queryable and NON-STUB, the
// membership edges readable from the reverse-edge index, and the campaign
// carrying both the seed and the completion marker.
func TestBoot_ImportsTheWorldAndMarksItComplete(t *testing.T) {
	cfg := bootConfig(t)
	engine := startEngine(t, cfg)
	store := graphStore(t)

	for target, sources := range engine.MembershipEdges() {
		entries, err := store.IncomingRelationships(t.Context(), target)
		if err != nil {
			t.Fatalf("read the incoming edges of %s: %v", target, err)
		}
		present := map[string]bool{}
		for _, entry := range entries {
			if entry.Predicate == vocabulary.WorldLocationCurrent.String() {
				present[entry.FromEntityID] = true
			}
		}
		for _, source := range sources {
			if !present[source] {
				t.Errorf("the reverse-edge index does not carry %s -> %s after boot; \"who is here\" would "+
					"answer with a shorter list rather than an error", source, target)
			}
		}
	}

	state, err := store.GetEntity(t.Context(), engine.CampaignID())
	if err != nil {
		t.Fatalf("read the campaign entity: %v", err)
	}
	if seed := testinfra.FirstObject(state, vocabulary.CampaignSeedValue.String()); seed == nil {
		t.Error("the campaign carries no seed after boot")
	}
	markers := testinfra.ObjectsFor(state, vocabulary.CampaignImportCompleted.String())
	if len(markers) != 1 {
		t.Fatalf("the campaign carries %d import markers, want exactly one", len(markers))
	}
}

// A SECOND boot of the same world must not re-import. Re-import into a living
// campaign resets every predicate the template declares, dropping the
// relationships play has since changed — the exact inverse of "a restart must not
// replay the dragon eating you".
//
// The proof is a play-created fact that survives the second boot. Asserting only
// that the marker is unchanged would pass against an engine that re-imported and
// re-marked.
func TestBoot_ASecondBootDoesNotImportOverALivingCampaign(t *testing.T) {
	cfg := bootConfig(t)
	first := startEngine(t, cfg)
	store := graphStore(t)

	character := "c360.semmachina." + cfg.WorldNS + ".starter.character." + starterCharacter
	before, err := store.GetEntity(t.Context(), character)
	if err != nil {
		t.Fatalf("read the character: %v", err)
	}
	if got := fmt.Sprint(testinfra.FirstObject(before, vocabulary.CharacterStatusCurrent.String())); got !=
		string(vocabulary.StatusHealthy) {
		t.Fatalf("the imported character's status is %q, want the template's %q", got, vocabulary.StatusHealthy)
	}

	// Play happens: the character is wounded. A re-import would put them back.
	at := time.Now().UTC()
	if _, err := store.MergeTriples(t.Context(), character, []message.Triple{{
		Subject:    character,
		Predicate:  vocabulary.CharacterStatusCurrent.String(),
		Object:     string(vocabulary.StatusWounded),
		Source:     "boot-test-play",
		Timestamp:  at,
		Confidence: 1.0,
	}}); err != nil {
		t.Fatalf("record a play-created fact: %v", err)
	}
	first.Stop()

	second := startEngine(t, cfg)
	if second.CampaignID() != first.CampaignID() {
		t.Fatalf("the second boot claimed campaign %s, want %s", second.CampaignID(), first.CampaignID())
	}
	after, err := store.GetEntity(t.Context(), character)
	if err != nil {
		t.Fatalf("read the character after the second boot: %v", err)
	}
	if got := fmt.Sprint(testinfra.FirstObject(after, vocabulary.CharacterStatusCurrent.String())); got !=
		string(vocabulary.StatusWounded) {
		t.Errorf("after a second boot the character's status is %q, want the play-created %q; the second boot "+
			"re-imported the template over a living campaign", got, vocabulary.StatusWounded)
	}
}

// A campaign that was CLAIMED and never marked is a crashed import, and this boot
// refuses to serve it rather than choosing between the two wrong answers:
// importing would run a template over a world another process may be writing, and
// marking would certify a world this boot never read.
func TestBoot_RefusesToServeAnInterruptedImport(t *testing.T) {
	cfg := bootConfig(t)
	cfg.MarkerWait = 2 * time.Second
	client := requireBroker(t)

	// Stand in for the claimant that died between the create and the import:
	// claim the campaign through the production gate and stop.
	store := graphStore(t)
	gate, err := campaign.NewGate(store, campaign.Identity{
		Org: cfg.Org, WorldNS: cfg.WorldNS, Template: "starter",
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	// The graph must be running for the claim to land, and on this path the
	// engine is not yet started — so the claim goes through a boot of its own
	// whose only job is graph-ingest.
	helper := startEngine(t, interruptedHelperConfig(cfg))
	_ = client
	claim, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claim.Fresh {
		t.Fatal("the stand-in claim was not fresh; this test needs to be the claimant")
	}
	helper.Stop()

	engine := newTestEngine(t, cfg)
	t.Cleanup(engine.Stop)
	err = engine.Start(t.Context())
	if err == nil {
		t.Fatal("a boot served a campaign whose import never completed")
	}
	if !strings.Contains(err.Error(), "import") {
		t.Errorf("the refusal does not name the interrupted import: %v", err)
	}
	if engine.Addr() != "" {
		t.Error("the player socket opened on a boot that refused the world")
	}
}

// interruptedHelperConfig is the same instance with a DIFFERENT campaign, used
// only to have graph-ingest running while the stand-in claim is made.
func interruptedHelperConfig(cfg boot.Config) boot.Config {
	helper := cfg
	helper.WorldNS = cfg.WorldNS + "h"
	helper.ContentBucket = cfg.ContentBucket + "H"
	// The helper's only job is to run graph-ingest and graph-index, so it boots
	// the ordinary starter world rather than whatever the test under way is
	// refusing.
	world, err := fixtures.StarterWorld()
	if err == nil {
		helper.World = world
		helper.SceneLocalID = ""
	}
	return helper
}

// The stranded-turn pass must not run against a rule processor this boot cannot
// confirm started — an unstarted processor is indistinguishable from a quiet one,
// and the pass would read an empty work set for turns about to receive a trigger
// and END them.
//
// # The stale key is the point, and it is planted rather than hoped for
//
// A previous boot leaves a lifecycle status behind, and the earlier version of
// this check compared its timestamp. This one deletes the key before starting the
// processor and waits for it to reappear, so the property under test is that a
// LEFTOVER key — from a processor this boot never started — is not an answer. The
// leftover is planted by a real processor in another namespace rather than
// depending on which tests ran first on the shared broker, because a test whose
// premise is supplied by its neighbours is a test that stops proving anything
// when somebody reorders the file.
func TestBoot_TheResumePreconditionRefusesAnUnstartedRuleProcessor(t *testing.T) {
	cfg := bootConfig(t)

	// Plant the leftover: a real rule processor, in its own namespace, started
	// and stopped. Its lifecycle status is now in COMPONENT_STATUS under the same
	// key the check below reads.
	planter := newTestEngine(t, interruptedHelperConfig(cfg))
	if err := planter.StartThrough(t.Context(), boot.StepRules); err != nil {
		t.Fatalf("plant a previous boot's lifecycle status: %v", err)
	}
	requireRuleStatusPresent(t)
	planter.Stop()

	engine := newTestEngine(t, cfg)
	t.Cleanup(engine.Stop)

	var check func(context.Context) error
	for _, step := range engine.Steps() {
		if step.ID == boot.StepResume {
			check = step.Check
		}
	}
	if check == nil {
		t.Fatal("the resume step declares no precondition; the one ordering constraint that ends live turns " +
			"would be a comment")
	}

	// Everything the pass needs EXCEPT the rule processor: connect, the streams,
	// the graph, the loop. Running the sequence up to but not including the rule
	// processor is what makes this the real state rather than a mocked one — and
	// the planted status is sitting in the bucket the whole time.
	if err := engine.StartThrough(t.Context(), boot.StepStageStream); err != nil {
		t.Fatalf("start through %s: %v", boot.StepStageStream, err)
	}
	requireRuleStatusPresent(t)

	err := check(t.Context())
	if err == nil {
		t.Fatal("the resume precondition passed with no rule processor started by this boot, against a status " +
			"an earlier one left behind; the pass would read an empty work set for turns about to receive a " +
			"trigger and fail them terminally")
	}
	if !strings.Contains(err.Error(), "rule processor") {
		t.Errorf("the refusal does not name the unstarted processor: %v", err)
	}

	// And the same check passes once the processor HAS started, which is what
	// keeps the refusal above from being a check that always fails.
	if err := engine.StartThrough(t.Context(), boot.StepRules); err != nil {
		t.Fatalf("start through %s: %v", boot.StepRules, err)
	}
	if err := check(t.Context()); err != nil {
		t.Fatalf("the resume precondition refused a rule processor this boot DID start: %v", err)
	}
}

// requireRuleStatusPresent asserts the leftover status is actually in the bucket.
//
// Without it the test above could pass because nothing was ever planted, which is
// the vacuous version of the same assertion: "a check refuses when there is no
// status" is a far weaker claim than "a check refuses when there IS one and this
// boot did not write it".
func requireRuleStatusPresent(t *testing.T) {
	t.Helper()
	bucket, err := requireBroker(t).Client.GetKeyValueBucket(t.Context(), "COMPONENT_STATUS")
	if err != nil {
		t.Fatalf("the COMPONENT_STATUS bucket does not exist, so no leftover status was planted: %v", err)
	}
	if _, err := bucket.Get(t.Context(), "rule-processor"); err != nil {
		t.Fatalf("no rule-processor status is in the bucket, so this test's premise is missing: %v", err)
	}
}

// The rule processor's lifecycle status is what the precondition reads, and this
// pins the two coordinates upstream does not export — the bucket and the key —
// plus the mechanism built on them.
//
// Without it, a rename upstream would turn the precondition into a check that
// always fails — a boot that refuses every deployment — and nothing would say
// which of the two strings had moved.
//
// The REVISION proves this boot wrote a fresh report rather than merely reading
// the planted one. The preceding stale-status test is the functional proof that
// the leftover alone cannot satisfy the resume precondition. beta.159's catalog
// fixes COMPONENT_STATUS at History=1, so the intermediate delete marker is no
// longer retained after the subsequent write.
func TestRuleProcessorStatus_IsDeletedAndRewrittenByEachBoot(t *testing.T) {
	cfg := bootConfig(t)

	planter := newTestEngine(t, interruptedHelperConfig(cfg))
	if err := planter.StartThrough(t.Context(), boot.StepRules); err != nil {
		t.Fatalf("plant a previous boot's lifecycle status: %v", err)
	}
	planted := ruleStatusRevision(t)
	planter.Stop()

	engine := newTestEngine(t, cfg)
	t.Cleanup(engine.Stop)
	if err := engine.StartThrough(t.Context(), boot.StepRules); err != nil {
		t.Fatalf("start through %s: %v", boot.StepRules, err)
	}

	bucket, err := requireBroker(t).Client.GetKeyValueBucket(t.Context(), "COMPONENT_STATUS")
	if err != nil {
		t.Fatalf("the COMPONENT_STATUS bucket does not exist after the rule processor started: %v", err)
	}
	entry, err := bucket.Get(t.Context(), "rule-processor")
	if err != nil {
		t.Fatalf("the rule processor reported no lifecycle status under the key %q: %v", "rule-processor", err)
	}
	if entry.Revision() <= planted {
		t.Errorf("the status is still at revision %d, the one an earlier boot left; this boot neither deleted "+
			"nor rewrote it, so its precondition is reading somebody else's report", entry.Revision())
	}

	var status struct {
		Component string `json:"component"`
		Stage     string `json:"stage"`
	}
	if err := json.Unmarshal(entry.Value(), &status); err != nil {
		t.Fatalf("decode the reported status: %v", err)
	}
	if status.Component != "rule-processor" {
		t.Errorf("the status names component %q", status.Component)
	}
	if status.Stage == "" {
		t.Error("the status carries no stage; the check requires one, so an upstream that stopped writing it " +
			"would refuse every boot")
	}
}

// ruleStatusRevision reads the current revision of the rule processor's status.
func ruleStatusRevision(t *testing.T) uint64 {
	t.Helper()
	bucket, err := requireBroker(t).Client.GetKeyValueBucket(t.Context(), "COMPONENT_STATUS")
	if err != nil {
		t.Fatalf("the COMPONENT_STATUS bucket does not exist, so no status was planted: %v", err)
	}
	entry, err := bucket.Get(t.Context(), "rule-processor")
	if err != nil {
		t.Fatalf("no rule-processor status is in the bucket, so this test's premise is missing: %v", err)
	}
	return entry.Revision()
}

// A player action submitted through the real socket becomes a turn.
//
// It is deliberately the SHALLOWEST end-to-end assertion — the turn exists in
// `accepted` — because everything past that point is the E2E task's, which owns
// the mock model. What this proves is the composition: the socket authenticated,
// the gateway derived an action id and published, the action stream existed,
// intake consumed, and the recorder created the turn. Any one of those unwired
// and there is no turn.
func TestBoot_AnActionThroughTheSocketBecomesATurn(t *testing.T) {
	cfg := bootConfig(t)
	engine := startEngine(t, cfg)

	response := submit(t, engine, cfg, "boot-e2e-1", "I put my shoulder to the gate.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}

	store := graphStore(t)
	identity := turn.Identity{Org: cfg.Org, WorldNS: cfg.WorldNS, Template: "starter"}
	turnEntityID, err := identity.EntityID(response.TurnID)
	if err != nil {
		t.Fatalf("compose the turn entity id: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		state, readErr := store.GetEntity(t.Context(), turnEntityID)
		if readErr == nil && !state.IsStub() {
			if phase := testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String()); phase != nil {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the submitted action never became a turn (%s): %v", turnEntityID, readErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// helpers -------------------------------------------------------------------

func jetStream(t *testing.T) jetstream.JetStream {
	t.Helper()
	js, err := requireBroker(t).Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	return js
}

func graphStore(t *testing.T) *graphio.Store {
	t.Helper()
	store, err := graphio.NewStore(requireBroker(t).Client)
	if err != nil {
		t.Fatalf("graphio.NewStore: %v", err)
	}
	return store
}

// requireStagesIdle proves every stage consumer bound AND finished whatever it
// was given.
//
// "The engine started" is a weaker claim than it looks: a stage whose handler
// failed after doing the work leaves an unacknowledged delivery JetStream hands
// back forever, and nothing else in a boot test would notice.
func requireStagesIdle(t *testing.T, js jetstream.JetStream) {
	t.Helper()
	for _, phase := range rulepack.StagePhases() {
		consumer, err := js.Consumer(t.Context(), rulepack.StageStream, rulepack.StageConsumerName(phase))
		if err != nil {
			t.Errorf("the %s stage bound no consumer: %v", phase, err)
			continue
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Errorf("read the %s consumer: %v", phase, err)
			continue
		}
		if info.NumAckPending != 0 {
			t.Errorf("the %s stage holds %d unacknowledged trigger(s) on a boot that ran no turn",
				phase, info.NumAckPending)
		}
	}
	for label, name := range map[string]string{
		"knowledge":     rulepack.KnowledgeConsumerName,
		"accusation":    rulepack.AccusationConsumerName,
		"case-progress": rulepack.CaseProgressConsumerName,
	} {
		consumer, err := js.Consumer(t.Context(), rulepack.StageStream, name)
		if err != nil {
			t.Errorf("the %s auxiliary path bound no consumer: %v", label, err)
			continue
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Errorf("read the %s auxiliary consumer: %v", label, err)
			continue
		}
		if info.NumAckPending != 0 {
			t.Errorf("the %s auxiliary path holds %d unacknowledged trigger(s) on a boot that ran no turn",
				label, info.NumAckPending)
		}
	}
}

func consumerExistsOn(t *testing.T, js jetstream.JetStream, stream, filter string) bool {
	t.Helper()
	s, err := js.Stream(t.Context(), stream)
	if err != nil {
		t.Fatalf("read stream %s: %v", stream, err)
	}
	lister := s.ListConsumers(t.Context())
	for info := range lister.Info() {
		if info == nil {
			continue
		}
		if info.Config.FilterSubject == filter {
			return true
		}
		for _, subject := range info.Config.FilterSubjects {
			if subject == filter {
				return true
			}
		}
	}
	if err := lister.Err(); err != nil {
		t.Fatalf("list consumers on %s: %v", stream, err)
	}
	return false
}

// submit sends one action through the REAL socket: an authenticated handshake
// and a JSON text frame, exactly as a client would.
func submit(t *testing.T, engine *boot.Engine, cfg boot.Config, key, text string) *payload.SubmitResponse {
	t.Helper()
	conn := dialSocket(t, engine, cfg)
	defer conn.Close() //nolint:errcheck // test teardown

	body, err := json.Marshal(map[string]any{
		"protocol":        payload.PlayerProtocolV1,
		"idempotency_key": key,
		"text":            text,
	})
	if err != nil {
		t.Fatalf("encode submission: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("write submission: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, frame, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read the engine's answer: %v", err)
	}
	var envelope playersocket.Frame
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("decode the engine's answer (%s): %v", frame, err)
	}
	if envelope.Type != playersocket.FrameSubmitResponse || envelope.Response == nil {
		t.Fatalf("the engine answered with a %q frame carrying no response: %s", envelope.Type, frame)
	}
	return envelope.Response
}

// dialSocket opens an authenticated connection to the engine's own listener.
//
// The credential rides on the HANDSHAKE, which is what makes "there is no
// unauthenticated socket" true: a wrong credential is a 401 and never an upgrade.
func dialSocket(t *testing.T, engine *boot.Engine, cfg boot.Config) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Player.Credential)
	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	url := "ws://" + engine.Addr() + playersocket.DefaultPath
	conn, response, err := dialer.DialContext(t.Context(), url, header)
	if err != nil {
		t.Fatalf("dial %s: %v (response %v)", url, err, response)
	}
	return conn
}

// The completion marker is written LAST, after both readbacks — so a boot whose
// readback refuses leaves the campaign claimed and UNMARKED, which is exactly the
// state the next boot is supposed to refuse.
//
// The refusal is produced structurally rather than by a short timeout: a world
// where nobody is anywhere declares no membership edge, so the reverse-edge gate
// refuses immediately and deterministically. A timeout-based version of this test
// would fail whenever the import happened to land quickly, which is the wrong
// direction for a flake.
func TestBoot_DoesNotMarkAnImportWhoseReadbackRefused(t *testing.T) {
	cfg := bootConfig(t)
	cfg.World = noMembershipPackage(t)
	cfg.SceneLocalID = "cellar"

	engine := newTestEngine(t, cfg)
	t.Cleanup(engine.Stop)
	err := engine.Start(t.Context())
	if err == nil {
		t.Fatal("a boot marked an import whose readiness gate had nothing to read")
	}
	if !strings.Contains(err.Error(), "membership") {
		t.Errorf("the refusal does not name the gate that refused: %v", err)
	}

	// A failed boot stops its own components, so the graph query surface goes with
	// it. A second instance in its own namespace brings graph-ingest back up to
	// answer the one question this test is about.
	engine.Stop()
	reader := startEngine(t, interruptedHelperConfig(cfg))
	_ = reader

	// The campaign WAS claimed — this boot got that far — and carries no marker.
	store := graphStore(t)
	state, readErr := store.GetEntity(t.Context(), engine.CampaignID())
	if readErr != nil {
		t.Fatalf("read the campaign entity: %v", readErr)
	}
	if markers := testinfra.ObjectsFor(state, vocabulary.CampaignImportCompleted.String()); len(markers) != 0 {
		t.Fatalf("the campaign carries %v after a refused readback; the marker is written LAST for exactly this "+
			"reason, and one written before the readback would certify a world nobody checked", markers)
	}
}

// The declarative stream manager owns AGENT provisioning and reconciliation.
// A stream somebody else created with narrower subjects and a stale retention
// horizon is repaired before the direct component-boundary guard binds it.
//
// Nothing else would notice. The server ACCEPTS a consumer whose filter lies
// outside its stream's subjects (measured), so the loop and agentic-tools both
// start, bind everything, and look healthy — until the first tool call is
// published onto `tool.execute.*`, is captured by nothing, and the persona burns
// its whole budget waiting for a result that cannot arrive. The manager must
// make that state impossible before the loop starts.
func TestBoot_ReconcilesTheAgentStreamSubjectsAndRetentionHorizon(t *testing.T) {
	client := requireBroker(t)
	js := jetStream(t)

	// Replace the correct stream with a narrow one, and restore it afterwards so
	// the next boot in this package creates it properly.
	_ = js.DeleteStream(t.Context(), persona.TaskStream)
	t.Cleanup(func() {
		_ = js.DeleteStream(context.Background(), persona.TaskStream)
	})
	narrow := stage.AgentStreamConfig()
	narrow.Subjects = []string{persona.AgentSubjectFilter}
	narrow.MaxAge = time.Hour
	narrow.Duplicates = 2 * time.Minute
	if _, err := client.Client.EnsureStream(t.Context(), narrow); err != nil {
		t.Fatalf("create the narrow %s stream: %v", persona.TaskStream, err)
	}

	engine := newTestEngine(t, bootConfig(t))
	t.Cleanup(engine.Stop)
	if err := engine.StartThrough(t.Context(), boot.StepAgentStream); err != nil {
		t.Fatalf("boot did not repair the narrow AGENT stream: %v", err)
	}
	stream, err := js.Stream(t.Context(), persona.TaskStream)
	if err != nil {
		t.Fatalf("read the repaired AGENT stream: %v", err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read the repaired AGENT configuration: %v", err)
	}
	if !slices.Contains(info.Config.Subjects, persona.ToolExecuteSubjectFilter) {
		t.Fatalf("repaired AGENT subjects = %v, still missing %s",
			info.Config.Subjects, persona.ToolExecuteSubjectFilter)
	}
	if info.Config.Duplicates != stage.AgentStreamMaxAge {
		t.Fatalf("repaired AGENT duplicate window = %v, want retained-task horizon %v",
			info.Config.Duplicates, stage.AgentStreamMaxAge)
	}
	if info.Config.MaxAge != stage.AgentStreamMaxAge {
		t.Fatalf("repaired AGENT max age = %v, want retained-task horizon %v",
			info.Config.MaxAge, stage.AgentStreamMaxAge)
	}
}

// The marker gates INGRESS, not just the importer, and it does so by READING the
// graph rather than remembering what an earlier step decided.
func TestBoot_TheIngressGateReadsTheMarkerRatherThanRememberingIt(t *testing.T) {
	cfg := bootConfig(t)
	engine := startEngine(t, cfg)

	var check func(context.Context) error
	for _, step := range engine.Steps() {
		if step.ID == boot.StepIntake {
			check = step.Check
		}
	}
	if check == nil {
		t.Fatal("the intake step declares no precondition; the marker would gate the importer and nothing else")
	}
	if err := check(t.Context()); err != nil {
		t.Fatalf("the ingress gate refused a marked world: %v", err)
	}

	// Clear the marker on the graph. Nothing in production does this; what it
	// stands in for is a boot that reached this step against a campaign whose
	// import never completed.
	store := graphStore(t)
	if _, err := store.MergeTriples(t.Context(), engine.CampaignID(), nil,
		graphio.WithClearedPredicates(vocabulary.CampaignImportCompleted.String())); err != nil {
		t.Fatalf("clear the marker: %v", err)
	}

	err := check(t.Context())
	if err == nil {
		t.Fatal("the ingress gate passed with no marker on the campaign; it is remembering an earlier step's " +
			"answer rather than reading the fact")
	}
	if !strings.Contains(err.Error(), vocabulary.CampaignImportCompleted.String()) {
		t.Errorf("the refusal does not name the missing marker: %v", err)
	}
}

// noMembershipPackage is a world where nobody is anywhere: a scene, a character,
// and no world.location.current between them.
func noMembershipPackage(t *testing.T) fstest.MapFS {
	t.Helper()
	manifest := "id: nowhere\nname: Nowhere\nversion: 0.1.0\n" +
		"engine_compat: \">=v1.0.0-beta.150 <v2.0.0\"\ndescription: nobody is anywhere\n"
	entities := strings.Join([]string{
		`{"local_id":"cellar","type":"scene","triples":[{"predicate":"world.entity.name","object":"The Cellar"}]}`,
		`{"local_id":"` + starterCharacter + `","type":"character","triples":[` +
			`{"predicate":"world.entity.name","object":"Rook"}]}`,
	}, "\n") + "\n"

	return fstest.MapFS{
		"manifest.yaml":             {Data: []byte(manifest)},
		"entities.jsonl":            {Data: []byte(entities)},
		"personas/adjudicator.json": {Data: personaRecord("nowhere/adjudicator", "adjudicator")},
		"personas/narrator.json":    {Data: personaRecord("nowhere/narrator", "narrator")},
	}
}

// The tool-dispatch lane is WIRED, end to end, on the real broker.
//
// This is the composition's least visible dependency and the one that had no
// caller until it was looked for. The agentic loop does not execute a tool: it
// publishes the call onto `tool.execute.<name>` and waits for `tool.result.*`,
// and the component in between is agentic-tools. A composition that starts the
// loop and not the executor boots cleanly, advertises both terminal tools to the
// model, and then swallows every exit — the persona burns its whole iteration
// budget and the turn ends as a cap exhaustion, which describes the symptom and
// hides the cause.
//
// So the assertion is a ROUND TRIP rather than a consumer count: a call goes out
// on the lane the loop uses, and an answer comes back on the lane the loop reads.
// The call is deliberately malformed — this test is about the WIRE, and the
// terminal tool's own refusal is as good a proof that something executed it as a
// success would be, without needing a turn or a model.
func TestBoot_TheToolDispatchLaneAnswers(t *testing.T) {
	cfg := bootConfig(t)
	startEngine(t, cfg)
	client := requireBroker(t)
	js := jetStream(t)

	stream, err := js.Stream(t.Context(), persona.TaskStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", persona.TaskStream, err)
	}
	// Bound BEFORE the publish, and ephemeral so it consumes nothing anybody else
	// is owed.
	results, err := stream.CreateConsumer(t.Context(), jetstream.ConsumerConfig{
		FilterSubject:     "tool.result.*",
		DeliverPolicy:     jetstream.DeliverNewPolicy,
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("bind a reader on the tool-result lane: %v", err)
	}

	call := agentic.ToolCall{
		ID:        "boot-lane-probe",
		Name:      persona.VerdictToolName,
		Arguments: map[string]any{},
	}
	body, err := json.Marshal(message.NewBaseMessage(call.Schema(), &call, "boot-test"))
	if err != nil {
		t.Fatalf("encode the tool call: %v", err)
	}
	if err := client.Client.PublishToStream(t.Context(), "tool.execute."+persona.VerdictToolName, body); err != nil {
		t.Fatalf("publish onto the tool-execute lane: %v; the AGENT stream does not capture it", err)
	}

	batch, err := results.Fetch(1, jetstream.FetchMaxWait(30*time.Second))
	if err != nil {
		t.Fatalf("read the tool-result lane: %v", err)
	}
	var answer struct {
		Payload agentic.ToolResult `json:"payload"`
	}
	answered := false
	for msg := range batch.Messages() {
		answered = true
		if err := json.Unmarshal(msg.Data(), &answer); err != nil {
			t.Fatalf("decode the tool result (%s): %v", msg.Data(), err)
		}
	}
	if err := batch.Error(); err != nil {
		t.Fatalf("read the tool-result lane: %v", err)
	}
	if !answered {
		t.Fatal("nothing answered on tool.result.* within 30s. The agentic loop publishes every tool call onto " +
			"tool.execute.* rather than executing it, so an unanswered lane means no tool executor is running — " +
			"and every persona's terminal exit would be published into silence while the loop burned its budget")
	}

	// An answer alone is not the claim. A tool executor with no shared registry
	// answers too — with tool-not-found — so the assertion is that THIS engine's
	// verdict executor is what ran: the error is its own refusal, naming the
	// identity metadata the engine injects and never asks the model for.
	if answer.Payload.Name != persona.VerdictToolName {
		t.Errorf("the lane answered about tool %q, want %q", answer.Payload.Name, persona.VerdictToolName)
	}
	if !strings.Contains(answer.Payload.Error, persona.MetadataKeyTurnID) {
		t.Errorf("the tool result is %q, which is not this engine's verdict executor refusing a call with no "+
			"injected identity; a registry the components were never handed answers with tool-not-found, and "+
			"an engine wired that way advertises both terminal tools and can execute neither",
			answer.Payload.Error)
	}
}

// The lane that reaches the MODEL is wired, and a boot without it refuses rather
// than parking every turn.
//
// # The failure this closes, found end to end
//
// The agentic loop does not call an endpoint any more than it executes a tool.
// deps.ModelRegistry is how it RESOLVES which endpoint a capability means; when it
// needs a completion it publishes an AgentRequest onto `agent.request.>` and waits
// on `agent.response.>`. The component in between is agentic-model, and this
// composition did not run it.
//
// Nothing said so. The AGENT stream captures `agent.request.>` under `agent.>`
// whether or not anything consumes it, so every stream-level check this engine
// makes passed; both personas resolved their endpoints; the boot looked healthy.
// What happened was that every turn reached `adjudicating`, published a request
// nobody answered, and sat there until the loop's own timeout ended it — reported
// as a cap exhaustion, a code that describes the symptom and hides the cause. It
// was found by the first test that ran a whole turn.
//
// So the check is about CONSUMERS rather than subjects, and this test is in two
// halves for the reason every gate here is: that it passes on a correct boot
// proves nothing about whether it can refuse.
func TestBoot_RefusesAModelRequestLaneNobodyConsumes(t *testing.T) {
	cfg := bootConfig(t)
	engine := startEngine(t, cfg)
	js := jetStream(t)

	if !consumerExistsOn(t, js, persona.TaskStream, persona.ModelRequestSubjectFilter) {
		t.Fatalf("no consumer filters %s after boot; the agentic loop would publish every model request into "+
			"silence and every turn would end as a cap exhaustion", persona.ModelRequestSubjectFilter)
	}

	var check func(context.Context) error
	for _, step := range engine.Steps() {
		if step.ID == boot.StepStages {
			check = step.Check
		}
	}
	if check == nil {
		t.Fatal("the stages step declares no precondition, so nothing stands between a boot and a composition " +
			"whose personas can never receive an answer")
	}
	if err := check(t.Context()); err != nil {
		t.Fatalf("the stages precondition refused a correctly composed engine: %v", err)
	}

	// Take the bridge away. Deleting the consumer is what a composition that never
	// started agentic-model looks like from the stream, which is the only place
	// this check can look.
	consumer := consumerNameOn(t, js, persona.TaskStream, persona.ModelRequestSubjectFilter)
	stream, err := js.Stream(t.Context(), persona.TaskStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", persona.TaskStream, err)
	}
	if err := stream.DeleteConsumer(t.Context(), consumer); err != nil {
		t.Fatalf("delete the model-request consumer: %v", err)
	}

	err = check(t.Context())
	if err == nil {
		t.Fatal("the stages precondition passed with nothing consuming the model-request lane; every persona " +
			"would publish a request nobody answers, and the turn would end on the loop's timeout as a cap " +
			"exhaustion rather than as the composition error it is")
	}
	if !strings.Contains(err.Error(), persona.ModelRequestSubjectFilter) {
		t.Errorf("the refusal does not name the unconsumed lane: %v", err)
	}
}

// consumerNameOn returns the name of a consumer filtering one subject.
func consumerNameOn(t *testing.T, js jetstream.JetStream, stream, filter string) string {
	t.Helper()
	s, err := js.Stream(t.Context(), stream)
	if err != nil {
		t.Fatalf("read stream %s: %v", stream, err)
	}
	lister := s.ListConsumers(t.Context())
	for info := range lister.Info() {
		if info == nil {
			continue
		}
		for _, subject := range append([]string{info.Config.FilterSubject}, info.Config.FilterSubjects...) {
			if subject == filter {
				return info.Name
			}
		}
	}
	if err := lister.Err(); err != nil {
		t.Fatalf("list consumers on %s: %v", stream, err)
	}
	t.Fatalf("no consumer on %s filters %q", stream, filter)
	return ""
}

// The world's VOICE reaches the model, which is a seam with no other caller.
//
// Persona fragments are what make tone and judging stance world DATA rather than
// engine code. The agentic loop reads them from the PERSONAS bucket and falls
// back to the framework's own defaults when the bucket is empty — silently, and
// with a perfectly working turn loop that narrates in nobody's voice. Nothing
// else in this composition would notice.
func TestBoot_SeedsTheWorldsPersonaFragments(t *testing.T) {
	cfg := bootConfig(t)
	startEngine(t, cfg)

	manager, err := sspersona.NewManager(requireBroker(t).Client)
	if err != nil {
		t.Fatalf("open the persona bucket: %v", err)
	}
	stored, err := manager.List(t.Context())
	if err != nil {
		t.Fatalf("list personas: %v", err)
	}

	// The starter world ships one fragment per role, and the ROLE is what the
	// loop filters by: a fragment whose role does not match the spawned persona's
	// is text the model never sees.
	for _, role := range []persona.Role{persona.RoleAdjudicator, persona.RoleNarrator} {
		found := false
		for _, record := range stored {
			if slices.Contains(record.Roles, string(role)) && record.Content != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("the PERSONAS bucket holds no fragment for role %q after boot; the loop would assemble its "+
				"system prompt from the framework's defaults and this world would have no voice", role)
		}
	}
}
