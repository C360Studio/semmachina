package playersocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// These tests drive a REAL socket over a REAL TCP listener against the REAL
// gateway. The only stand-ins are the graph read surface and the action
// publisher, and both are behind the gateway rather than on the transport path —
// what this package is responsible for is bytes on a wire, and a mocked upgrade
// would prove none of it.
//
// The properties that matter here are mostly NEGATIVE: a socket that was refused
// receives nothing, a connection that dropped is no longer a delivery target, a
// second player is not told about the first player's turn. Those are asserted by
// what does not arrive, on a real socket, with a bounded wait.

const (
	testOrg        = "c360.semmachina.world1.starter"
	testPlayerID   = testOrg + ".player.pat"
	testOtherID    = testOrg + ".player.alex"
	testCampaignID = testOrg + ".campaign.instance"
	testSceneID    = testOrg + ".scene.gate"

	patCredential  = "pat-local-credential"
	alexCredential = "alex-local-credential"
)

var testTime = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// quiet is how long a proof-by-absence waits before concluding nothing arrived.
//
// It is a wall-clock assertion and it is a trade: too short and a slow machine
// calls a real delivery an absence, too long and every negative test pays for it.
// Everything it covers is in-process and loopback — a delivery that is going to
// happen has already happened by the time the write returns — so a fifth of a
// second is several orders of magnitude of headroom, and the failure direction is
// a flaky PASS rather than a flaky failure only if the delivery path is broken in
// the opposite direction, which the positive tests catch.
const quiet = 200 * time.Millisecond

// ------------------------------------------------------------------ fakes

// fakeStore is a graph read surface holding two born players.
type fakeStore struct {
	mu       sync.Mutex
	entities map[string]*graph.EntityState
}

func newFakeStore() *fakeStore {
	store := &fakeStore{entities: map[string]*graph.EntityState{}}
	for _, id := range []string{testPlayerID, testOtherID} {
		store.entities[id] = &graph.EntityState{
			ID: id,
			MessageType: message.Type{
				Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
			},
			Version: 1,
			Triples: []message.Triple{{
				Subject:   id,
				Predicate: vocabulary.WorldEntityKind.String(),
				Object:    string(vocabulary.EntityKindPlayer),
				Source:    "world-import",
				Timestamp: testTime,
			}},
		}
	}
	return store
}

func (s *fakeStore) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	copied := *stored
	copied.Triples = append([]message.Triple(nil), stored.Triples...)
	return &copied, nil
}

// fakePublisher records the actions that reached the stream.
type fakePublisher struct {
	mu        sync.Mutex
	published [][]byte
}

func (p *fakePublisher) PublishToStreamWithMsgID(_ context.Context, _ string, data []byte, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, append([]byte(nil), data...))
	return nil
}

func (p *fakePublisher) actions(t *testing.T) []*payload.PlayerAction {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*payload.PlayerAction, 0, len(p.published))
	for _, raw := range p.published {
		var envelope struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("the published bytes are not a BaseMessage envelope: %v", err)
		}
		var action payload.PlayerAction
		if err := json.Unmarshal(envelope.Payload, &action); err != nil {
			t.Fatalf("the envelope payload is not a player action: %v", err)
		}
		out = append(out, &action)
	}
	return out
}

// ------------------------------------------------------------------ harness

type harness struct {
	gateway   *gateway.Gateway
	router    *egress.Router
	store     *fakeStore
	publisher *fakePublisher
	url       string
	logs      *logCapture
	// stop cancels the server's context, which is how a shutdown is exercised.
	// Cleanup calls it too; a CancelFunc is idempotent.
	stop context.CancelFunc
}

// logCapture is a concurrency-safe sink for the operator log, so a test can
// assert that a posture decision ANNOUNCED itself rather than merely happened.
type logCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.Write(p)
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.String()
}

func newLogger(capture *logCapture) *slog.Logger {
	return slog.New(slog.NewTextHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// newGate builds the real gateway over the fakes, for tests that need a gateway
// without a server in front of it.
func newGate(
	t *testing.T,
	store *fakeStore,
	publisher *fakePublisher,
	capture *logCapture,
	opts ...gateway.Option,
) *gateway.Gateway {
	t.Helper()
	roster, err := gateway.NewRoster(map[string]string{
		patCredential:  testPlayerID,
		alexCredential: testOtherID,
	})
	if err != nil {
		t.Fatalf("NewRoster: %v", err)
	}
	options := append([]gateway.Option{
		gateway.WithClock(func() time.Time { return testTime }),
		gateway.WithLogger(newLogger(capture)),
	}, opts...)
	gw, err := gateway.New(roster, store, publisher,
		gateway.Config{CampaignID: testCampaignID, SceneID: testSceneID}, options...)
	if err != nil {
		t.Fatalf("gateway.New: %v", err)
	}
	return gw
}

func newHarness(t *testing.T, gatewayOpts []gateway.Option, socketOpts ...playersocket.Option) *harness {
	t.Helper()
	store := newFakeStore()
	publisher := &fakePublisher{}
	capture := &logCapture{}
	gw := newGate(t, store, publisher, capture, gatewayOpts...)
	router, err := egress.NewRouter(gw, playersocket.NewSink(), egress.WithRouterLogger(newLogger(capture)))
	if err != nil {
		t.Fatalf("egress.NewRouter: %v", err)
	}

	server, err := playersocket.NewServer(gw, playersocket.Config{Addr: "127.0.0.1:0"},
		append([]playersocket.Option{playersocket.WithLogger(newLogger(capture))}, socketOpts...)...)
	if err != nil {
		t.Fatalf("playersocket.NewServer: %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the player socket did not stop serving after its context was cancelled")
		}
	})

	return &harness{
		gateway:   gw,
		router:    router,
		store:     store,
		publisher: publisher,
		url:       "ws://" + listener.Addr().String() + playersocket.DefaultPath,
		logs:      capture,
		stop:      cancel,
	}
}

// newServerOn puts a SECOND transport in front of an existing gateway, which is
// how the connection-id nonce is exercised: a counter alone would have the two
// servers mint the same ids against one session table.
func newServerOn(t *testing.T, gw *gateway.Gateway, socketOpts ...playersocket.Option) *harness {
	t.Helper()
	capture := &logCapture{}
	server, err := playersocket.NewServer(gw, playersocket.Config{Addr: "127.0.0.1:0"},
		append([]playersocket.Option{playersocket.WithLogger(newLogger(capture))}, socketOpts...)...)
	if err != nil {
		t.Fatalf("playersocket.NewServer: %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the second player socket did not stop serving")
		}
	})
	return &harness{
		gateway: gw,
		url:     "ws://" + listener.Addr().String() + playersocket.DefaultPath,
		logs:    capture,
	}
}

// dial opens an authenticated socket and waits until it is a delivery target.
//
// The wait is not incidental: the handshake returns to the client the moment the
// upgrade completes, and the session is bound just after, so a test that asserted
// on the session table immediately would be racing the server's own handler.
func (h *harness) dial(t *testing.T, credential, playerID string) *websocket.Conn {
	t.Helper()
	before := len(h.gateway.SessionsFor(playerID))
	ws, response, err := h.rawDial(t, credential)
	if err != nil {
		t.Fatalf("dial: %v (response %v)", err, response)
	}
	t.Cleanup(func() { ws.Close() }) //nolint:errcheck // best effort in teardown
	h.awaitTargets(t, playerID, before+1)
	return ws
}

// rawDial attempts a connection and hands back whatever happened.
func (h *harness) rawDial(t *testing.T, credential string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	if credential != "" {
		header.Set("Authorization", "Bearer "+credential)
	}
	dialer := &websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	return dialer.DialContext(t.Context(), h.url, header)
}

// awaitTargets polls until a player resolves to the expected number of live
// connections.
func (h *harness) awaitTargets(t *testing.T, playerID string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(h.gateway.SessionsFor(playerID)); got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("player %s resolves to %d connections, want %d",
		playerID, len(h.gateway.SessionsFor(playerID)), want)
}

func (h *harness) connIDs(playerID string) []string {
	ids := make([]string, 0)
	for _, session := range h.gateway.SessionsFor(playerID) {
		ids = append(ids, session.Connection.ID)
	}
	return ids
}

// ------------------------------------------------------------------ wire

func submission(key, text string) []byte {
	return []byte(fmt.Sprintf(
		`{"protocol":%q,"text":%q,"idempotency_key":%q}`, payload.PlayerProtocolV1, text, key))
}

// readFrame reads one server frame and holds it to its own contract.
func readFrame(t *testing.T, ws *websocket.Conn) *playersocket.Frame {
	t.Helper()
	if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set the client read deadline: %v", err)
	}
	messageType, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read a frame: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("the server sent message type %d, want a text frame", messageType)
	}
	var frame playersocket.Frame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("the server sent %q, which is not a frame: %v", raw, err)
	}
	if err := frame.Validate(); err != nil {
		t.Fatalf("the server sent a frame that fails its own contract: %v", err)
	}
	return &frame
}

// readResponse reads one submission answer.
func readResponse(t *testing.T, ws *websocket.Conn) *payload.SubmitResponse {
	t.Helper()
	frame := readFrame(t, ws)
	if frame.Type != playersocket.FrameSubmitResponse {
		t.Fatalf("the server sent a %q frame, want a submission answer", frame.Type)
	}
	return frame.Response
}

// submit sends one submission and reads its answer.
func (h *harness) submit(t *testing.T, ws *websocket.Conn, key, text string) *payload.SubmitResponse {
	t.Helper()
	if err := ws.WriteMessage(websocket.TextMessage, submission(key, text)); err != nil {
		t.Fatalf("write a submission: %v", err)
	}
	return readResponse(t, ws)
}

// expectNothing is the proof-by-absence primitive: a socket that receives
// anything at all inside the quiet window fails the test.
func expectNothing(t *testing.T, ws *websocket.Conn, why string) {
	t.Helper()
	if err := ws.SetReadDeadline(time.Now().Add(quiet)); err != nil {
		t.Fatalf("set the client read deadline: %v", err)
	}
	_, raw, err := ws.ReadMessage()
	if err == nil {
		t.Fatalf("%s: the socket received %q", why, raw)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		// A CLOSED socket also receives nothing, and accepting that would let
		// this assertion pass for the wrong reason — including the reason that
		// the other player was disconnected by somebody else's delivery.
		t.Fatalf("%s: the socket ended rather than staying silent: %v", why, err)
	}
}

// ------------------------------------------------------------------ deliveries

// terminalDelivery composes a valid failed-turn delivery for a player.
//
// A FAILED turn, because it is the cheapest coherent result: it needs no
// narration artifact and no roll, so a transport test can produce a real,
// contract-valid document without standing up the whole turn machinery. The
// composition from durable state is proven against real infrastructure in
// internal/egress and again in this package's integration test.
func terminalDelivery(t *testing.T, playerID, turnID string) *payload.TurnDelivery {
	t.Helper()
	actionID, err := payload.ActionIDForTurn(turnID)
	if err != nil {
		t.Fatalf("derive the action id for turn %s: %v", turnID, err)
	}
	delivery := &payload.TurnDelivery{
		Protocol: payload.PlayerProtocolV1,
		Result: &payload.TurnResult{
			Protocol:      payload.PlayerProtocolV1,
			TurnID:        turnID,
			ActionID:      actionID,
			PlayerID:      playerID,
			Phase:         vocabulary.PhaseFailed,
			FailureReason: vocabulary.FailureEffectInvalid,
			ResolvedAt:    testTime,
		},
	}
	if err := delivery.Validate(); err != nil {
		t.Fatalf("the fixture delivery is not a valid document: %v", err)
	}
	return delivery
}

// proseDelivery is a terminal delivery carrying a full prose budget, for the
// tests that need a document big enough to fill a socket's buffers.
func proseDelivery(t *testing.T, playerID, turnID string) *payload.TurnDelivery {
	t.Helper()
	delivery := terminalDelivery(t, playerID, turnID)
	delivery.Result.NarrationRef = "obj://ARTIFACTS/narration/" + turnID
	delivery.Narration = &payload.DeliveredNarration{
		TurnID: turnID,
		Band:   vocabulary.BandMiss,
		Prose:  strings.Repeat("The hinges scream and the gate does not give. ", payload.MaxProseBytes/47),
	}
	if err := delivery.Validate(); err != nil {
		t.Fatalf("the prose fixture is not a valid document: %v", err)
	}
	return delivery
}

// turnIDFor makes a turn id that derives cleanly from an action id.
func turnIDFor(t *testing.T, playerID, key string) string {
	t.Helper()
	actionID, err := gateway.ActionIDFor(playerID, key)
	if err != nil {
		t.Fatalf("derive an action id: %v", err)
	}
	return payload.TurnIDForAction(actionID)
}

// deliveredTurn reads one turn delivery off a socket.
func deliveredTurn(t *testing.T, ws *websocket.Conn) *payload.TurnDelivery {
	t.Helper()
	frame := readFrame(t, ws)
	if frame.Type != playersocket.FrameTurnDelivery {
		t.Fatalf("the server sent a %q frame, want a turn delivery", frame.Type)
	}
	return frame.Delivery
}

// closeCodeOf reports the WebSocket close code a client observed, or -1.
func closeCodeOf(err error) int {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code
	}
	return -1
}

// containsAll reports which of the wanted substrings are missing from a log.
func containsAll(text string, wanted ...string) []string {
	var missing []string
	for _, want := range wanted {
		if !strings.Contains(text, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

// deadline is the bound on a read a test EXPECTS to complete.
func deadline() time.Time { return time.Now().Add(5 * time.Second) }

// liveClient is a raw WebSocket player — the client this spike's protocol is
// actually spoken by, kept to the same reads and writes a real one would make.
type liveClient struct{ ws *websocket.Conn }

func dialClient(t *testing.T, url, credential string) *liveClient {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+credential)
	dialer := &websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	ws, response, err := dialer.DialContext(t.Context(), url, header)
	if err != nil {
		t.Fatalf("dial %s: %v (response %v)", url, err, response)
	}
	client := &liveClient{ws: ws}
	t.Cleanup(func() { ws.Close() }) //nolint:errcheck // best effort in teardown
	return client
}

func (c *liveClient) submit(t *testing.T, key, text string) *payload.SubmitResponse {
	t.Helper()
	if err := c.ws.WriteMessage(websocket.TextMessage, submission(key, text)); err != nil {
		t.Fatalf("write a submission: %v", err)
	}
	return readResponse(t, c.ws)
}

func (c *liveClient) delivery(t *testing.T) *payload.TurnDelivery {
	t.Helper()
	return deliveredTurn(t, c.ws)
}

func (c *liveClient) retrieve(t *testing.T, request *playersocket.RetrieveRequest) *playersocket.RetrieveResponse {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode a retrieval: %v", err)
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write a retrieval: %v", err)
	}
	frame := readFrame(t, c.ws)
	if frame.Type != playersocket.FrameRetrieveResponse || frame.Retrieval == nil {
		t.Fatalf("retrieval was answered with %+v", frame)
	}
	return frame.Retrieval
}

func (c *liveClient) expectNothing(t *testing.T, why string) {
	t.Helper()
	expectNothing(t, c.ws, why)
}

func (c *liveClient) close(t *testing.T) {
	t.Helper()
	if err := c.ws.Close(); err != nil {
		t.Fatalf("close the client: %v", err)
	}
}

// readBody reads a refused handshake's response body.
func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	if response == nil {
		return ""
	}
	defer response.Body.Close() //nolint:errcheck // best effort in teardown
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		t.Fatalf("read the refusal body: %v", err)
	}
	return string(body)
}

// sameSet reports whether two collections of connection ids hold the same
// members, in any order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := slices.Clone(a)
	right := slices.Clone(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
