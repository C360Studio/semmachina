package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/model"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/mockmodel"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/resume"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The starter world's own names, restated rather than derived: a fixture that
// silently renamed a character should fail these tests rather than pass against
// whatever it now calls itself.
const (
	starterCharacter = "rook"
	starterSentry    = "hollis"
	starterScene     = "gatehouse"
	starterCrowbar   = "crowbar"
	starterLantern   = "lantern"
	starterRations   = "rations"
	testCredential   = "e2e-test-credential"
	playerLocalID    = "one"
	templateID       = "starter"
)

// turnBudget bounds how long one turn may take end to end.
//
// It bounds a WAIT, never a pace. Every hop here is a durable queue and a
// container's scheduling, and the only thing this number decides is how long a
// wedged turn takes to be reported as one. Generous, because a failure that
// reports "the turn never completed" is more useful than a flake that reports it
// on a slow machine.
const turnBudget = 90 * time.Second

var broker struct {
	client *natsclient.TestClient
	err    error
}

func TestMain(m *testing.M) {
	// Before any rule config loads, in every binary and every test: the rule
	// processor rejects a canonical-but-undeclared predicate, so without this the
	// turn-sequencing pack does not start and no turn moves at all.
	if err := vocabulary.RegisterPredicates(); err != nil {
		fmt.Fprintf(os.Stderr, "register semmachina predicates: %v\n", err)
		os.Exit(1)
	}
	broker.client, broker.err = startBroker()
	if broker.err != nil && testinfra.Skipped() {
		fmt.Fprintf(os.Stderr,
			"\n================================================================\n"+
				" END-TO-END TURN TESTS SKIPPED BY %s\n reason: %v\n"+
				" NO TURN WAS RUN IN THIS RUN.\n"+
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
	// A bare broker. Every stream, bucket and consumer a turn needs is one the
	// engine creates, which is part of what these tests are checking.
	return natsclient.NewSharedTestClient(natsclient.WithJetStream())
}

func requireBroker(t *testing.T) *natsclient.TestClient {
	t.Helper()
	if broker.client != nil {
		return broker.client
	}
	if testinfra.Skipped() {
		t.Skipf("SKIPPED by %s — no turn ran in this test", testinfra.SkipEnv)
		return nil
	}
	t.Fatalf("a real NATS broker is required and unavailable: %v\n"+
		"These tests run the production composition end to end; there is no substitute for it.\n"+
		"Start Docker, or set %s=1 to run the rest of the suite without this proof.",
		broker.err, testinfra.SkipEnv)
	return nil
}

// world is one instance under test: a scripted model, a booted engine, and the
// identifiers everything else is asserted against.
type world struct {
	ns       string
	cfg      boot.Config
	mock     *mockmodel.Handler
	wire     *wireLog
	engine   *boot.Engine
	identity turn.Identity

	playerID   string
	sceneID    string
	campaignID string
}

// worldOption configures a world before it boots.
type worldOption func(*boot.Config)

// withCampaignSeed pins the campaign seed a FRESH campaign is created with. It is
// how a band scenario chooses its band — see seeds_test.go.
func withCampaignSeed(seed campaign.Seed) worldOption {
	return func(cfg *boot.Config) { cfg.CampaignSeed = seed }
}

// newWorld builds an instance and boots it.
//
// The namespace is the caller's and is fixed per test rather than generated,
// because a band scenario's pinned turn id is derived from the player entity id,
// which contains the namespace. A generated one would make the pinned constants
// unpinnable.
func newWorld(t *testing.T, ns, scenario string, opts ...worldOption) *world {
	t.Helper()
	w := newWorldUnstarted(t, ns, scenario, opts...)
	w.boot(t)
	return w
}

// newWorldUnstarted builds an instance without booting it, for a test that wants
// to arrange something before ingress opens.
func newWorldUnstarted(t *testing.T, ns, scenario string, opts ...worldOption) *world {
	t.Helper()
	client := requireBroker(t)

	fixture, err := mockmodel.TurnLoopScenariosIn(ns)
	if err != nil {
		t.Fatalf("rebind the scenario pack onto %q: %v", ns, err)
	}
	handler, err := mockmodel.New(fixture, scenario)
	if err != nil {
		t.Fatalf("start the scripted model on scenario %q: %v", scenario, err)
	}
	wire := &wireLog{}
	server := httptest.NewServer(wire.wrap(handler))
	t.Cleanup(server.Close)

	pkg, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("StarterWorld: %v", err)
	}
	cfg := boot.Config{
		NATSURL: client.URL,
		Org:     "c360",
		WorldNS: ns,
		Player: boot.PlayerConfig{
			LocalID:    playerLocalID,
			Name:       "The Player",
			Character:  "local:" + starterCharacter,
			Credential: testCredential,
		},
		Models:        mockModels(server.URL),
		World:         pkg,
		Registry:      testRegistry(t),
		Logger:        quietLogger(),
		Socket:        playersocket.Config{Addr: "127.0.0.1:0"},
		ContentBucket: "E2E_" + strings.ToUpper(ns),
		ReadyTimeout:  45 * time.Second,
		ReadyPoll:     100 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &world{
		ns:       ns,
		cfg:      cfg,
		mock:     handler,
		wire:     wire,
		identity: turn.Identity{Org: cfg.Org, WorldNS: ns, Template: templateID},
	}
}

// boot starts a fresh engine over this world's configuration.
//
// A NEW Engine every time, never a restarted one: boot.Engine is single-use by
// construction, and a "crash" here has to look like a process that died and a
// process that started, not like an object somebody reset.
func (w *world) boot(t *testing.T) {
	t.Helper()
	engine, err := boot.New(w.cfg)
	if err != nil {
		t.Fatalf("boot.New: %v", err)
	}
	w.engine = engine
	t.Cleanup(engine.Stop)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatalf("boot the engine: %v", err)
	}
	w.playerID = engine.PlayerID()
	w.sceneID = engine.SceneID()
	w.campaignID = engine.CampaignID()
}

// crash stops the engine the way a process death would leave it: no draining, no
// finishing what is in flight beyond what a graceful component stop does.
func (w *world) crash() {
	if w.engine != nil {
		w.engine.Stop()
		w.engine = nil
	}
}

// entity composes one starter-world entity id in this world's namespace.
func (w *world) entity(kind, localID string) string {
	return fmt.Sprintf("c360.semmachina.%s.%s.%s.%s", w.ns, templateID, kind, localID)
}

// turnEntity composes a turn entity id in this world's namespace.
func (w *world) turnEntity(t *testing.T, turnID string) string {
	t.Helper()
	id, err := w.identity.EntityID(turnID)
	if err != nil {
		t.Fatalf("compose the turn entity id for %s: %v", turnID, err)
	}
	return id
}

// mockModels points both persona capabilities at the running stub.
//
// Two endpoints rather than one, with distinct model names, because the stub
// routes on the model field the endpoint config puts on the wire — which is
// deployment configuration and therefore the same seam a live retarget uses.
func mockModels(baseURL string) *model.Registry {
	return &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"mock-adjudicator": mockmodel.EndpointConfig(baseURL, "semmachina-mock-adjudicator"),
			"mock-narrator":    mockmodel.EndpointConfig(baseURL, "semmachina-mock-narrator"),
		},
		Capabilities: map[string]*model.CapabilityConfig{
			persona.CapabilityCasekeeping:  {Preferred: []string{"mock-adjudicator"}},
			persona.CapabilityCompanion:    {Preferred: []string{"mock-adjudicator"}},
			persona.CapabilityAdjudication: {Preferred: []string{"mock-adjudicator"}},
			persona.CapabilityNarration:    {Preferred: []string{"mock-narrator"}},
		},
		Defaults: model.DefaultsConfig{Model: "mock-adjudicator"},
	}
}

func testRegistry(t *testing.T) *payloadregistry.Registry {
	t.Helper()
	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		t.Fatalf("register framework payloads: %v", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		t.Fatalf("register semmachina payloads: %v", err)
	}
	return registry
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// the wire ------------------------------------------------------------------

// wireLog records the bytes the stub actually put on the socket.
//
// It exists because of F18: the framework's model client normalizes token totals,
// infers a tool call from the presence of tool_calls regardless of finish_reason,
// and substitutes an empty argument map for arguments it cannot parse. Asserting
// provider fidelity THROUGH that client cannot distinguish a right response from
// a wrong one, and three mock mutations survived on exactly that basis. So the
// fidelity assertions read what went over the wire.
type wireLog struct {
	mu        sync.Mutex
	responses []wireResponse
}

// wireResponse is one answer as the socket carried it.
type wireResponse struct {
	Status int
	Body   []byte
}

// wrap returns the handler to serve, recording every response it produces.
func (l *wireLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &capturingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(capture, r)
		l.mu.Lock()
		defer l.mu.Unlock()
		l.responses = append(l.responses, wireResponse{Status: capture.status, Body: capture.body.Bytes()})
	})
}

// Responses returns every recorded answer, in the order the stub sent them.
func (l *wireLog) Responses() []wireResponse {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]wireResponse(nil), l.responses...)
}

type capturingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capturingWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturingWriter) Write(p []byte) (int, error) {
	c.body.Write(p)
	return c.ResponseWriter.Write(p)
}

// Bytes returns the captured body.
func (c *capturingWriter) Bytes() []byte { return c.body.Bytes() }

// wireChoice is the part of a provider response the framework client branches on,
// decoded from raw bytes rather than through the client's own types, so a field
// the client tolerates is still visible here.
type wireChoice struct {
	FinishReason string `json:"finish_reason"`
	Message      struct {
		Content   any `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
}

type wireCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// the player -----------------------------------------------------------------

// client is one authenticated socket with a reader goroutine behind it.
//
// The goroutine matters for two unrelated reasons. Frames arrive unsolicited — a
// turn resolves minutes after its submission was answered — so a test that only
// read after writing would deadlock on the delivery it is waiting for. And
// gorilla answers a server ping from inside ReadMessage, so a client that is not
// reading is a client that does not pong, which is the difference between a quiet
// player and a dead peer (F24).
type client struct {
	conn   *websocket.Conn
	frames chan *playersocket.Frame
	closed chan struct{}
}

func (w *world) dial(t *testing.T) *client {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+w.cfg.Player.Credential)
	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	url := "ws://" + w.engine.Addr() + playersocket.DefaultPath
	conn, response, err := dialer.DialContext(t.Context(), url, header)
	if err != nil {
		t.Fatalf("dial %s: %v (response %v)", url, err, response)
	}

	c := &client{conn: conn, frames: make(chan *playersocket.Frame, 8), closed: make(chan struct{})}
	go c.read()
	t.Cleanup(c.close)
	return c
}

func (w *world) awaitPlayerConnections(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := w.engine.PlayerConnectionCount(); got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("engine reports %d player connections, want %d", w.engine.PlayerConnectionCount(), want)
}

func (c *client) read() {
	defer close(c.frames)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var frame playersocket.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			return
		}
		select {
		case c.frames <- &frame:
		case <-c.closed:
			return
		}
	}
}

func (c *client) close() {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	_ = c.conn.Close()
}

// submit sends one action and returns the engine's answer to it.
func (c *client) submit(t *testing.T, key, text string) *payload.SubmitResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"protocol":        payload.PlayerProtocolV1,
		"idempotency_key": key,
		"text":            text,
	})
	if err != nil {
		t.Fatalf("encode submission: %v", err)
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("write submission: %v", err)
	}
	frame := c.await(t, playersocket.FrameSubmitResponse, 20*time.Second)
	return frame.Response
}

// retrieve asks through the authenticated public socket. This is intentionally
// not an egress.Results helper: reconnect recovery is a player capability only
// if the reconnected client can speak it on the real wire.
func (c *client) retrieve(t *testing.T, by playersocket.RetrieveBy, id string) *playersocket.RetrieveResponse {
	t.Helper()
	body, err := json.Marshal(&playersocket.RetrieveRequest{
		Protocol: payload.PlayerProtocolV1,
		Type:     playersocket.RequestRetrieve,
		By:       by,
		ID:       id,
	})
	if err != nil {
		t.Fatalf("encode retrieval: %v", err)
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("write retrieval: %v", err)
	}
	frame := c.await(t, playersocket.FrameRetrieveResponse, 20*time.Second)
	if frame.Retrieval == nil {
		t.Fatal("retrieve_response frame carries no response")
	}
	return frame.Retrieval
}

// await reads until a frame of the wanted type arrives.
func (c *client) await(t *testing.T, want playersocket.FrameType, budget time.Duration) *playersocket.Frame {
	t.Helper()
	deadline := time.After(budget)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				t.Fatalf("the socket closed while waiting for a %q frame", want)
			}
			if frame.Type == want {
				return frame
			}
		case <-deadline:
			t.Fatalf("no %q frame arrived within %s", want, budget)
		case <-t.Context().Done():
			t.Fatalf("the test context ended while waiting for a %q frame", want)
		}
	}
}

// noFrameWithin asserts nothing arrives in a window. Used where the absence is
// the claim — a player who must NOT receive another player's result, or a delivery
// for a turn that was refused before it produced one.
func (c *client) noFrameWithin(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case frame, ok := <-c.frames:
		if !ok {
			return
		}
		t.Fatalf("a %q frame arrived when none should have", frame.Type)
	case <-time.After(window):
	}
}

// reading the world ----------------------------------------------------------

func graphStore(t *testing.T) *graphio.Store {
	t.Helper()
	store, err := graphio.NewStore(requireBroker(t).Client)
	if err != nil {
		t.Fatalf("graphio.NewStore: %v", err)
	}
	return store
}

func jetStream(t *testing.T) jetstream.JetStream {
	t.Helper()
	js, err := requireBroker(t).Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	return js
}

// entityState reads one entity through graph-ingest's query surface.
func entityState(t *testing.T, id string) *graph.EntityState {
	t.Helper()
	state, err := graphStore(t).GetEntity(t.Context(), id)
	if err != nil {
		t.Fatalf("read entity %s: %v", id, err)
	}
	return state
}

// soleObject returns the single object an entity records for a predicate, failing
// when it holds several.
//
// Several is never a shrug here. A single-valued predicate holding two values is
// the signature of a write that took the APPENDING lane rather than the replace
// lane (F14), and a reader that took the first would report the world as correct
// while it held two phases, two bands, or two healths.
func soleObject(t *testing.T, state *graph.EntityState, predicate vocabulary.Predicate) (any, bool) {
	t.Helper()
	objects := testinfra.ObjectsFor(state, predicate.String())
	switch len(objects) {
	case 0:
		return nil, false
	case 1:
		return objects[0], true
	default:
		t.Fatalf("entity %s holds %d values for the single-valued predicate %s (%v); that is an append-lane "+
			"write, and every reader downstream of it sees whichever value it happens to reach first",
			state.ID, len(objects), predicate, objects)
		return nil, false
	}
}

// stringObject returns a predicate's single object as a string, or "" when absent.
func stringObject(t *testing.T, state *graph.EntityState, predicate vocabulary.Predicate) string {
	t.Helper()
	object, ok := soleObject(t, state, predicate)
	if !ok {
		return ""
	}
	return fmt.Sprint(object)
}

// objectsFor returns every object recorded for a multi-valued predicate.
func objectsFor(state *graph.EntityState, predicate vocabulary.Predicate) []string {
	var out []string
	for _, object := range testinfra.ObjectsFor(state, predicate.String()) {
		out = append(out, fmt.Sprint(object))
	}
	return out
}

// phaseOf reads a turn's current phase.
func phaseOf(t *testing.T, turnEntityID string) vocabulary.TurnPhase {
	t.Helper()
	return vocabulary.TurnPhase(stringObject(t, entityState(t, turnEntityID), vocabulary.TurnPhaseCurrent))
}

// awaitTerminal waits for a turn to reach a terminal phase and returns it.
//
// It waits for TERMINAL rather than for `complete`, so a turn that failed reports
// the failure it recorded instead of timing out with nothing to say. A turn that
// simply stops is the one failure mode that has no error anywhere, and the
// diagnosis it prints is the phase it is stuck in.
func awaitTerminal(t *testing.T, turnEntityID string) vocabulary.TurnPhase {
	t.Helper()
	deadline := time.Now().Add(turnBudget)
	last := vocabulary.TurnPhase("")
	for {
		state, err := graphStore(t).GetEntity(t.Context(), turnEntityID)
		if err == nil && !state.IsStub() {
			last = vocabulary.TurnPhase(stringObject(t, state, vocabulary.TurnPhaseCurrent))
			if last == vocabulary.PhaseComplete || last == vocabulary.PhaseFailed {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn %s never reached a terminal phase within %s; it is parked in %q",
				turnEntityID, turnBudget, last)
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("the test context ended while waiting for turn %s", turnEntityID)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// awaitFact polls until an entity records an object for a predicate, and returns
// it. Used to stage a crash: it is how a test knows the roll landed.
func awaitFact(t *testing.T, entityID string, predicate vocabulary.Predicate, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		state, err := graphStore(t).GetEntity(t.Context(), entityID)
		if err == nil && !state.IsStub() {
			if value := stringObject(t, state, predicate); value != "" {
				return value
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("entity %s never recorded %s within %s", entityID, predicate, budget)
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("the test context ended while waiting for %s on %s", predicate, entityID)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// requireAbsent asserts an entity records nothing for a predicate.
func requireAbsent(t *testing.T, entityID string, predicate vocabulary.Predicate, why string) {
	t.Helper()
	if value := stringObject(t, entityState(t, entityID), predicate); value != "" {
		t.Fatalf("entity %s records %s = %q; %s", entityID, predicate, value, why)
	}
}

// the queues -----------------------------------------------------------------

// requireNothingQueuedFor asserts the substrate is holding no more work for one
// turn.
//
// # Why this is the assertion "the turn completed" cannot make
//
// A JetStream publish is a core publish underneath, so a missing stream or a
// mis-subjected lane can look like it worked while deliveries go unacknowledged —
// and a stage whose handler failed AFTER doing the work leaves a delivery
// JetStream hands back forever. A turn can therefore look finished on the graph
// while a lane redelivers its trigger every thirty seconds until the heat death of
// the campaign.
//
// # Why it is per TURN rather than per consumer
//
// Because the stage consumers are the ENGINE's — one durable per phase, named for
// the phase and not for the world — and this package runs many worlds against one
// broker. A bare NumAckPending reads every world's work at once, so one test's
// leftover fails the next test with a message about a turn it has never heard of.
// That is not a hypothetical: it is what the first version of this helper did.
//
// The per-turn view is also the STRONGER claim, and it is upstream of the
// stranded-turn pass rather than beside it: resume.WorkQueues is the same
// measurement the boot-time pass makes to decide whether the substrate still owns
// a turn, over the same two queues (stage triggers and persona tasks). Asking it
// here means the suite and the engine agree about what "still owed" means.
func requireNothingQueuedFor(t *testing.T, turnEntityID string) {
	t.Helper()
	js := jetStream(t)
	stages, err := js.Stream(t.Context(), rulepack.StageStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", rulepack.StageStream, err)
	}
	agent, err := js.Stream(t.Context(), persona.TaskStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", persona.TaskStream, err)
	}
	queues, err := resume.NewWorkQueues(stages, agent)
	if err != nil {
		t.Fatalf("resume.NewWorkQueues: %v", err)
	}

	// A settle window rather than a single read: the last hop's acknowledgement
	// and this read are two round trips.
	deadline := time.Now().Add(30 * time.Second)
	for {
		pending, err := queues.Pending(t.Context())
		if err != nil {
			t.Fatalf("measure the work queues: %v", err)
		}
		if pending[turnEntityID] == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the substrate is still holding %d piece(s) of work for turn %s after it ended. A stage "+
				"that failed after doing its work leaves a delivery JetStream redelivers forever, and a turn "+
				"that looks finished on the graph is compatible with that",
				pending[turnEntityID], turnEntityID)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// consumerNameFor finds the one DURABLE consumer that filters a subject.
//
// By FILTER rather than by name because the agentic loop's consumer name is an
// unexported sanitisation plus an operator suffix upstream does not export
// (semstreams#733) — the same reason internal/resume discovers it this way.
//
// # Two refusals, and the first one cost a whole debugging session
//
// DURABLE, because the measurement surface this suite shares with the engine
// (resume.WorkQueues) reads subjects through EPHEMERAL consumers that acknowledge
// nothing, and those carry the same filter. The first version of this took the
// first match and periodically handed back an ephemeral reader — so the crash
// staged in this package paused a consumer nobody delivers through, the turn ran
// to completion, and the case failed with a message about the artifact it found
// rather than about the instrument that missed.
//
// AMBIGUOUS is fatal rather than resolved by order, for the same reason: consumer
// listing has no promised order, so "take the first" is a coin flip whose losing
// side is a test that proves something other than its name.
func consumerNameFor(t *testing.T, stream, filter string) string {
	t.Helper()
	s, err := jetStream(t).Stream(t.Context(), stream)
	if err != nil {
		t.Fatalf("read stream %s: %v", stream, err)
	}
	var matched []string
	lister := s.ListConsumers(t.Context())
	for info := range lister.Info() {
		if info == nil || info.Config.Durable == "" {
			continue
		}
		for _, subject := range append([]string{info.Config.FilterSubject}, info.Config.FilterSubjects...) {
			if subject == filter {
				matched = append(matched, info.Name)
				break
			}
		}
	}
	if err := lister.Err(); err != nil {
		t.Fatalf("list consumers on %s: %v", stream, err)
	}
	switch len(matched) {
	case 1:
		return matched[0]
	case 0:
		t.Fatalf("no durable consumer on %s filters %q", stream, filter)
	default:
		t.Fatalf("%d durable consumers on %s filter %q (%v); which one owns the lane is not decidable here",
			len(matched), stream, filter, matched)
	}
	return ""
}

// pauseConsumer stops one lane from delivering, and returns the resume.
//
// This is how a crash is staged. A paused consumer is indistinguishable from a
// dead process to everything upstream of it — the trigger is published, the stream
// captures it, and nothing consumes it — so the turn stops in exactly the gap the
// test names instead of somewhere near it. The alternative, polling for a fact and
// then stopping the engine, is a race whose loser is SILENT: the kill lands after
// the next stage already ran and the test proves something weaker than it claims.
func pauseConsumer(t *testing.T, stream, consumer string) (resume func()) {
	t.Helper()
	js := jetStream(t)
	if _, err := js.PauseConsumer(t.Context(), stream, consumer, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatalf("pause the %s consumer on %s: %v", consumer, stream, err)
	}
	// Read it back. A pause that did not take is the worst available failure here:
	// the turn runs to completion, the crash lands nowhere near the gap the case
	// names, and every later assertion is about a different experiment. It has
	// happened — a consumer whose owning component re-creates it clears the pause —
	// so the instrument checks itself rather than being trusted.
	requirePaused(t, stream, consumer)
	resumed := false
	release := func() {
		if resumed {
			return
		}
		resumed = true
		if _, err := js.ResumeConsumer(context.Background(), stream, consumer); err != nil {
			t.Errorf("resume the %s consumer on %s: %v", consumer, stream, err)
		}
	}
	// Registered as a cleanup as well as returned: a test that fails between the
	// pause and its own resume would otherwise leave a lane stopped for every test
	// after it, and the failure they report would be this one wearing a disguise.
	t.Cleanup(release)
	return release
}

// requireResolvedNotificationAcked asserts the player-delivery consumer FINISHED
// with one turn's resolved notification.
//
// It exists because the per-turn queue view above covers the stage runners and the
// agentic loop and NOT the delivery consumer, so "zero connected recipients is an
// ordinary outcome" — the claim email-cadence play rests on — had nothing
// measuring it. A delivery path that treated no recipients as an error would nak,
// redeliver every thirty seconds forever, and leave every other assertion in that
// test passing.
//
// Per turn rather than per consumer, for the reason requireNothingQueuedFor is:
// the delivery consumer is the engine's and this package runs many worlds. So the
// question asked is the specific one — is THIS turn's notification below the
// consumer's acknowledgement floor — rather than "is the consumer idle".
func requireResolvedNotificationAcked(t *testing.T, turnEntityID string) {
	t.Helper()
	stream, err := jetStream(t).Stream(t.Context(), rulepack.StageStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", rulepack.StageStream, err)
	}

	// The notification's own sequence, found by reading the resolved subject
	// through an ephemeral, acknowledge-nothing cursor.
	reader, err := stream.CreateConsumer(t.Context(), jetstream.ConsumerConfig{
		FilterSubject:     rulepack.SubjectResolved,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("read the resolved-turn lane: %v", err)
	}
	batch, err := reader.Fetch(4096, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch the resolved-turn lane: %v", err)
	}
	var sequence uint64
	for msg := range batch.Messages() {
		trigger, parseErr := stage.ParseTrigger(msg.Data())
		if parseErr != nil || trigger.TurnEntityID != turnEntityID {
			continue
		}
		meta, metaErr := msg.Metadata()
		if metaErr != nil {
			t.Fatalf("read the notification's metadata: %v", metaErr)
		}
		if meta.Sequence.Stream > sequence {
			sequence = meta.Sequence.Stream
		}
	}
	if sequence == 0 {
		t.Fatalf("no resolved-turn notification names %s; the turn ended and nothing announced it, so neither "+
			"the player nor the archive would ever hear about it", turnEntityID)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		consumer, err := jetStream(t).Consumer(t.Context(), rulepack.StageStream, egress.ConsumerName)
		if err != nil {
			t.Fatalf("read the %s consumer: %v", egress.ConsumerName, err)
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the %s consumer info: %v", egress.ConsumerName, err)
		}
		if info.AckFloor.Stream >= sequence {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s consumer has finished everything below sequence %d and this turn's resolved "+
				"notification is at %d, so it has NOT acknowledged it. A delivery with no connected recipient "+
				"is an ordinary outcome — at email cadence the usual state of a player is away — and a path "+
				"that treats it as a failure redelivers forever",
				egress.ConsumerName, info.AckFloor.Stream, sequence)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// requirePaused asserts the server agrees a consumer is paused.
func requirePaused(t *testing.T, stream, consumer string) {
	t.Helper()
	c, err := jetStream(t).Consumer(t.Context(), stream, consumer)
	if err != nil {
		t.Fatalf("read the %s consumer on %s: %v", consumer, stream, err)
	}
	info, err := c.Info(t.Context())
	if err != nil {
		t.Fatalf("read the %s consumer info: %v", consumer, err)
	}
	if !info.Paused {
		t.Fatalf("the %s consumer on %s is not paused after a pause request; the gap this test needs would "+
			"never open and the crash would land wherever the turn happened to be", consumer, stream)
	}
}

// requireQueued asserts a consumer is holding work nobody has taken.
//
// It is the pre-crash assertion that makes a kill point provable rather than
// hoped for: the trigger for the next stage EXISTS and has not been delivered.
func requireQueued(t *testing.T, stream, consumer string, budget time.Duration) {
	t.Helper()
	js := jetStream(t)
	deadline := time.Now().Add(budget)
	for {
		c, err := js.Consumer(t.Context(), stream, consumer)
		if err != nil {
			t.Fatalf("read the %s consumer on %s: %v", consumer, stream, err)
		}
		info, err := c.Info(t.Context())
		if err != nil {
			t.Fatalf("read the %s consumer info: %v", consumer, err)
		}
		if info.NumPending > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s consumer on %s has nothing queued after %s; the turn never reached the lane this "+
				"crash is supposed to happen in, so the kill point is not the one this test names",
				consumer, stream, budget)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// the archive ----------------------------------------------------------------

// manifestsFor returns how many manifests the archive holds for one turn.
func manifestsFor(t *testing.T, turnID string) uint64 {
	t.Helper()
	subject, err := ledger.SubjectFor(turnID)
	if err != nil {
		t.Fatalf("ledger subject: %v", err)
	}
	stream, err := jetStream(t).Stream(t.Context(), ledger.Stream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", ledger.Stream, err)
	}
	info, err := stream.Info(t.Context(), jetstream.WithSubjectFilter(subject))
	if err != nil {
		t.Fatalf("read the %s stream state for %s: %v", ledger.Stream, subject, err)
	}
	return info.State.Subjects[subject]
}

// awaitManifest waits for the archive to hold exactly one manifest for a turn and
// returns it, decoded through the production registry.
func awaitManifest(t *testing.T, turnID string) *payload.TurnManifest {
	t.Helper()
	subject, err := ledger.SubjectFor(turnID)
	if err != nil {
		t.Fatalf("ledger subject: %v", err)
	}
	stream, err := jetStream(t).Stream(t.Context(), ledger.Stream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", ledger.Stream, err)
	}

	deadline := time.Now().Add(turnBudget)
	for {
		raw, err := stream.GetLastMsgForSubject(t.Context(), subject)
		if err == nil {
			decoded, decodeErr := message.NewDecoder(testRegistry(t)).Decode(raw.Data)
			if decodeErr != nil {
				t.Fatalf("decode the manifest for %s: %v", turnID, decodeErr)
			}
			manifest, ok := decoded.Payload().(*payload.TurnManifest)
			if !ok {
				t.Fatalf("the archive holds a %T for turn %s", decoded.Payload(), turnID)
			}
			return manifest
		}
		if time.Now().After(deadline) {
			t.Fatalf("the archive holds no manifest for turn %s after %s: %v", turnID, turnBudget, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// retrieval ------------------------------------------------------------------

// contentStore opens this world's artifact store the way the engine opens it.
func (w *world) contentStore(t *testing.T) *content.Store {
	t.Helper()
	backend, err := content.NewObjectStore(
		t.Context(), requireBroker(t).Client, content.WithBucket(w.cfg.ContentBucket))
	if err != nil {
		t.Fatalf("open the content bucket: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	store, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}
	return store
}

// replayReader builds the campaign archive's replay reader over this world.
//
// Its seed source is the production campaign gate, which reads the seed off the
// campaign ENTITY — so a replay here proves the seed the dice used is the seed the
// campaign stored, rather than the one this test happened to hand the engine.
func (w *world) replayReader(t *testing.T) *ledger.Reader {
	t.Helper()
	gate, err := campaign.NewGate(graphStore(t), campaign.Identity{
		Org: w.cfg.Org, WorldNS: w.ns, Template: templateID,
	})
	if err != nil {
		t.Fatalf("campaign.NewGate: %v", err)
	}
	reader, err := ledger.NewReader(
		requireBroker(t).Client, message.NewDecoder(testRegistry(t)), w.contentStore(t), gate)
	if err != nil {
		t.Fatalf("ledger.NewReader: %v", err)
	}
	return reader
}
