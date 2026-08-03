package playersocket_test

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
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The two claims this transport makes that only real infrastructure can settle:
// a submission that arrives on a socket becomes a canonical action on a REAL
// JetStream stream, and a result composed from a REAL graph reaches the socket
// of the player it belongs to and no other.
//
// A fake publisher cannot say the first — it accepts whatever it is handed, and
// what matters is that the broker did. A hand-built delivery cannot say the
// second, because the document a player receives is composed from durable state
// by a path with its own opinions about what a terminal turn is.

func TestMain(m *testing.M) {
	if err := vocabulary.RegisterPredicates(); err != nil {
		fmt.Fprintf(os.Stderr, "register semmachina predicates: %v\n", err)
		os.Exit(1)
	}
	os.Exit(testinfra.RunTests(m))
}

var worldCounter atomic.Int64

const (
	liveOrg      = "c360"
	liveTemplate = "starter"
)

// liveSocket is one world's whole player-facing edge over a real broker.
type liveSocket struct {
	harness *testinfra.Harness
	graph   *graphio.Store
	gateway *gateway.Gateway
	router  *egress.Router
	results *egress.Results

	recorder   *turn.Recorder
	identity   turn.Identity
	namespace  string
	campaignID string
	sceneID    string
	playerOne  string
	playerTwo  string
	stream     string
	subject    string
	url        string
}

func startLive(t *testing.T, socketOpts ...playersocket.Option) *liveSocket {
	t.Helper()
	h := testinfra.Require(t)
	namespace := fmt.Sprintf("ws%d", worldCounter.Add(1))

	store, err := graphio.NewStore(h.Client)
	if err != nil {
		t.Fatalf("graphio.NewStore: %v", err)
	}
	backend, err := content.NewObjectStore(t.Context(), h.Client, content.WithBucket("WS_"+namespace))
	if err != nil {
		t.Fatalf("content.NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	live := &liveSocket{
		harness:    h,
		graph:      store,
		identity:   turn.Identity{Org: liveOrg, WorldNS: namespace, Template: liveTemplate},
		namespace:  namespace,
		campaignID: liveID(t, namespace, "campaign", "main"),
		sceneID:    liveID(t, namespace, "scene", "gatehouse"),
		playerOne:  liveID(t, namespace, "player", "pat"),
		playerTwo:  liveID(t, namespace, "player", "alex"),
		// Deliberately outside the canonical action space: NATS refuses a stream
		// whose subjects overlap an existing one, so a per-test stream under the
		// real prefix would make the canonical stream uncreatable.
		stream:  "WS_ACTIONS_" + namespace,
		subject: "itest.ws.action." + namespace,
	}
	if live.recorder, err = turn.NewRecorder(store, artifacts, live.identity); err != nil {
		t.Fatalf("turn.NewRecorder: %v", err)
	}
	live.bornPlayers(t)

	if _, err := h.Client.EnsureStream(t.Context(), jetstream.StreamConfig{
		Name:      live.stream,
		Subjects:  []string{live.subject},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    turn.ActionMaxAge,
		MaxBytes:  turn.ActionMaxBytes,
		Discard:   jetstream.DiscardNew,
	}); err != nil {
		t.Fatalf("EnsureStream %s: %v", live.stream, err)
	}

	capture := &logCapture{}
	roster, err := gateway.NewRoster(map[string]string{
		patCredential:  live.playerOne,
		alexCredential: live.playerTwo,
	})
	if err != nil {
		t.Fatalf("gateway.NewRoster: %v", err)
	}
	gw, err := gateway.New(roster, store, h.Client,
		gateway.Config{CampaignID: live.campaignID, SceneID: live.sceneID},
		gateway.WithSubject(live.subject),
		gateway.WithLogger(newLogger(capture)))
	if err != nil {
		t.Fatalf("gateway.New: %v", err)
	}
	live.gateway = gw

	if live.results, err = egress.NewResults(store, artifacts, live.identity, live.campaignID); err != nil {
		t.Fatalf("egress.NewResults: %v", err)
	}
	// The REAL gateway is the directory and the REAL socket sink is the
	// transport; nothing on this path is a stand-in.
	if live.router, err = egress.NewRouter(gw, playersocket.NewSink(),
		egress.WithRouterLogger(newLogger(capture))); err != nil {
		t.Fatalf("egress.NewRouter: %v", err)
	}

	server, err := playersocket.NewServer(gw, playersocket.Config{Addr: "127.0.0.1:0"},
		append([]playersocket.Option{
			playersocket.WithLogger(newLogger(capture)),
			playersocket.WithResultRetriever(live.results),
		}, socketOpts...)...)
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
			t.Error("the player socket did not stop serving")
		}
	})
	live.url = "ws://" + listener.Addr().String() + playersocket.DefaultPath
	return live
}

func liveID(t *testing.T, namespace, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(liveOrg, namespace, liveTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose the %s id: %v", kind, err)
	}
	return id
}

// bornPlayers materializes both players through graph-ingest, so the gateway's
// "player_id is a graph entity" read has something true to find.
func (l *liveSocket) bornPlayers(t *testing.T) {
	t.Helper()
	at := time.Now().UTC()
	for _, id := range []string{l.playerOne, l.playerTwo} {
		if _, err := l.graph.CreateEntity(t.Context(), &graph.EntityState{
			ID: id,
			MessageType: message.Type{
				Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
			},
			Version:   1,
			UpdatedAt: at,
			Triples: []message.Triple{{
				Subject:    id,
				Predicate:  vocabulary.WorldEntityKind.String(),
				Object:     string(vocabulary.EntityKindPlayer),
				Source:     "integration-world-import",
				Timestamp:  at,
				Confidence: 1.0,
			}},
		}); err != nil {
			t.Fatalf("create player %s: %v", id, err)
		}
		l.harness.AwaitEntity(t, id)
	}
}

func (l *liveSocket) dial(t *testing.T, credential, playerID string) *liveClient {
	t.Helper()
	before := len(l.gateway.SessionsFor(playerID))
	client := dialClient(t, l.url, credential)
	l.awaitTargets(t, playerID, before+1)
	return client
}

func (l *liveSocket) awaitTargets(t *testing.T, playerID string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.gateway.SessionsFor(playerID)) == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("player %s resolves to %d connections, want %d",
		playerID, len(l.gateway.SessionsFor(playerID)), want)
}

// storedAction reads one message back off the REAL stream and decodes it the way
// intake does.
func (l *liveSocket) storedAction(t *testing.T, sequence uint64) *payload.PlayerAction {
	t.Helper()
	js, err := l.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.Stream(t.Context(), l.stream)
	if err != nil {
		t.Fatalf("open stream %s: %v", l.stream, err)
	}
	stored, err := stream.GetMsg(t.Context(), sequence)
	if err != nil {
		t.Fatalf("read message %d from %s: %v", sequence, l.stream, err)
	}
	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(stored.Data, &envelope); err != nil {
		t.Fatalf("the stored bytes are not a BaseMessage envelope: %v", err)
	}
	var action payload.PlayerAction
	if err := json.Unmarshal(envelope.Payload, &action); err != nil {
		t.Fatalf("the stored envelope's payload is not a player action: %v", err)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("the stored action does not satisfy the engine's own contract: %v", err)
	}
	return &action
}

// resolvedTurn drives one turn to a real terminal phase through the real
// recorder, which is what makes the delivery below a composition of durable
// state rather than a fixture.
func (l *liveSocket) resolvedTurn(t *testing.T, playerID, key string) string {
	t.Helper()
	actionID, err := gateway.ActionIDFor(playerID, key)
	if err != nil {
		t.Fatalf("derive the action id: %v", err)
	}
	acceptance, err := l.recorder.Accept(t.Context(), &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   playerID,
		CampaignID: l.campaignID,
		SceneID:    l.sceneID,
		Text:       "I lever the gate open with the crowbar.",
		ArrivedAt:  time.Now().UTC(),
		Channel: payload.ChannelBinding{
			Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-at-submission",
		},
	})
	if err != nil {
		t.Fatalf("accept the action: %v", err)
	}
	if _, err := l.recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailureEffectInvalid, content.Ref{}); err != nil {
		t.Fatalf("fail the turn: %v", err)
	}
	return acceptance.TurnID
}

// ---------------------------------------------------------------- the tests

// The ingress half, end to end: bytes on a socket become one canonical action a
// real broker is holding, with every server-owned field stamped from the session.
func TestIntegration_ASubmissionOverARealSocketBecomesAnActionOnTheRealStream(t *testing.T) {
	live := startLive(t)
	client := live.dial(t, patCredential, live.playerOne)

	response := client.submit(t, "key-1", "I lever the gate open with the crowbar.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the submission was refused: %+v", response.Refusal)
	}

	action := live.storedAction(t, 1)
	if action.ActionID != response.ActionID {
		t.Fatalf("the stored action is %q and the client was told %q", action.ActionID, response.ActionID)
	}
	if action.PlayerID != live.playerOne {
		t.Fatalf("the stored action carries player %q, want the session's %q", action.PlayerID, live.playerOne)
	}
	if action.CampaignID != live.campaignID || action.SceneID != live.sceneID {
		t.Fatalf("the stored action names campaign %q / scene %q", action.CampaignID, action.SceneID)
	}
	if action.Channel.Adapter != vocabulary.AdapterWebSocket {
		t.Fatalf("the stored action names adapter %q", action.Channel.Adapter)
	}
	sessions := live.gateway.SessionsFor(live.playerOne)
	if len(sessions) != 1 || action.Channel.ReplyTo != sessions[0].Connection.ID {
		t.Fatalf("the stored action replies to %q, want this transport's minted connection id",
			action.Channel.ReplyTo)
	}
}

// The egress half, end to end: a result composed from the REAL graph reaches the
// socket of the player it belongs to, and the other connected player's socket
// stays silent.
func TestIntegration_ARealResultReachesItsOwnPlayersSocketAndNoOther(t *testing.T) {
	live := startLive(t)
	pat := live.dial(t, patCredential, live.playerOne)
	alex := live.dial(t, alexCredential, live.playerTwo)

	turnID := live.resolvedTurn(t, live.playerOne, "key-1")
	delivery, err := live.results.ByTurn(t.Context(), turnID)
	if err != nil {
		t.Fatalf("compose the result for turn %s: %v", turnID, err)
	}

	outcome, err := live.router.Deliver(t.Context(), delivery)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 1 {
		t.Fatalf("the result reached %d sockets, want exactly its own player's", outcome.Recipients)
	}

	received := pat.delivery(t)
	if received.Result.TurnID != turnID || received.Result.PlayerID != live.playerOne {
		t.Fatalf("the player received %+v", received.Result)
	}
	if received.Result.Phase != vocabulary.PhaseFailed ||
		received.Result.FailureReason != vocabulary.FailureEffectInvalid {
		t.Fatalf("the delivered result does not carry the turn's own ending: %+v", received.Result)
	}
	// The assertion that matters, over a real socket.
	alex.expectNothing(t, "a second connected player received another player's turn")
}

// A reconnect between submission and resolution is invisible, because delivery
// resolves the socket from the player entity id at DELIVERY time — and over a
// real socket that means the OLD connection receives nothing.
func TestIntegration_AResultFollowsAReconnectRatherThanTheSubmittingConnection(t *testing.T) {
	live := startLive(t)
	first := live.dial(t, patCredential, live.playerOne)
	turnID := live.resolvedTurn(t, live.playerOne, "key-1")

	first.close(t)
	live.awaitTargets(t, live.playerOne, 0)
	second := live.dial(t, patCredential, live.playerOne)

	delivery, err := live.results.ByTurn(t.Context(), turnID)
	if err != nil {
		t.Fatalf("compose the result: %v", err)
	}
	outcome, err := live.router.Deliver(t.Context(), delivery)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 1 {
		t.Fatalf("the result reached %d sockets after a reconnect", outcome.Recipients)
	}
	if got := second.delivery(t).Result.TurnID; got != turnID {
		t.Fatalf("the reconnected socket received turn %q, want %q", got, turnID)
	}
}

// Durable retrieval is spoken by the reconnected CLIENT, not called as an
// in-process Results helper. Named lookups are authorized from the turn's owner
// before private artifacts are composed, so knowing another player's turn id is
// not enough even to read their stored fiction on the server side.
func TestIntegration_AReconnectedPlayerRetrievesTheirResultAndNotAnotherPlayers(t *testing.T) {
	live := startLive(t)
	turnID := live.resolvedTurn(t, live.playerOne, "retrieval-key")
	actionID, err := payload.ActionIDForTurn(turnID)
	if err != nil {
		t.Fatalf("derive action id: %v", err)
	}

	first := live.dial(t, patCredential, live.playerOne)
	first.close(t)
	live.awaitTargets(t, live.playerOne, 0)
	reconnected := live.dial(t, patCredential, live.playerOne)

	for _, request := range []*playersocket.RetrieveRequest{
		{Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: playersocket.RetrieveByTurn, ID: turnID},
		{Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: playersocket.RetrieveByAction, ID: actionID},
		{Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: playersocket.RetrieveLatest},
	} {
		response := reconnected.retrieve(t, request)
		if response.Status != playersocket.RetrieveFound || response.Delivery == nil {
			t.Fatalf("%s retrieval was not found: %+v", request.By, response)
		}
		if response.Delivery.Result.TurnID != turnID {
			t.Fatalf("%s retrieval answered with turn %q, want %q",
				request.By, response.Delivery.Result.TurnID, turnID)
		}
	}

	other := live.dial(t, alexCredential, live.playerTwo)
	for _, lookup := range []struct {
		by      playersocket.RetrieveBy
		foreign string
		missing string
	}{
		{playersocket.RetrieveByTurn, turnID, "turn-act-no-such-result"},
		{playersocket.RetrieveByAction, actionID, "act-no-such-result"},
	} {
		foreign := other.retrieve(t, &playersocket.RetrieveRequest{
			Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: lookup.by, ID: lookup.foreign,
		})
		missing := other.retrieve(t, &playersocket.RetrieveRequest{
			Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: lookup.by, ID: lookup.missing,
		})
		for name, response := range map[string]*playersocket.RetrieveResponse{
			"foreign": foreign, "missing": missing,
		} {
			if response.Status != playersocket.RetrieveRefused || response.Refusal == nil ||
				response.Refusal.Code != playersocket.RetrieveNotFound {
				t.Fatalf("%s %s lookup was not uniformly hidden: %+v", name, lookup.by, response)
			}
			if response.Delivery != nil {
				t.Fatalf("%s %s refusal disclosed a result", name, lookup.by)
			}
		}
		if foreign.Refusal.Message != missing.Refusal.Message {
			t.Fatalf("%s lookup is a state oracle: foreign says %q, missing says %q",
				lookup.by, foreign.Refusal.Message, missing.Refusal.Message)
		}
	}
}

func TestIntegration_LatestDistinguishesNoTurnHistoryFromARunningFirstTurn(t *testing.T) {
	live := startLive(t)
	empty := live.dial(t, patCredential, live.playerOne)

	response := empty.retrieve(t, &playersocket.RetrieveRequest{
		Protocol: payload.PlayerProtocolV1,
		Type:     playersocket.RequestRetrieve,
		By:       playersocket.RetrieveLatest,
	})
	if response.Status != playersocket.RetrieveRefused || response.Refusal == nil ||
		response.Refusal.Code != playersocket.RetrieveNotFound {
		t.Fatalf("empty latest retrieval = %+v, want immediate refused/not_found", response)
	}

	actionID, err := gateway.ActionIDFor(live.playerTwo, "running-first-turn")
	if err != nil {
		t.Fatalf("derive action id: %v", err)
	}
	if _, err := live.recorder.Accept(t.Context(), &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   live.playerTwo,
		CampaignID: live.campaignID,
		SceneID:    live.sceneID,
		Text:       "I listen at the gate before entering.",
		ArrivedAt:  time.Now().UTC(),
		Channel: payload.ChannelBinding{
			Adapter: vocabulary.AdapterWebSocket,
			ReplyTo: "running-first-turn",
		},
	}); err != nil {
		t.Fatalf("accept the running turn: %v", err)
	}

	running := live.dial(t, alexCredential, live.playerTwo)
	response = running.retrieve(t, &playersocket.RetrieveRequest{
		Protocol: payload.PlayerProtocolV1,
		Type:     playersocket.RequestRetrieve,
		By:       playersocket.RetrieveLatest,
	})
	if response.Status != playersocket.RetrieveRefused || response.Refusal == nil ||
		response.Refusal.Code != playersocket.RetrieveNotReady {
		t.Fatalf("running latest retrieval = %+v, want refused/not_ready", response)
	}
}
