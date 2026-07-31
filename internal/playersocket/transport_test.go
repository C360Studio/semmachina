package playersocket_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ---------------------------------------------------------- submissions

// The whole ingress path over a real socket: a client's bytes become one
// canonical action with every server-owned field stamped from the session.
func TestSubmit_AWellFormedFrameBecomesOneCanonicalAction(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	response := h.submit(t, ws, "key-1", "I lever the gate open with the crowbar.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the submission was refused: %+v", response.Refusal)
	}

	actions := h.publisher.actions(t)
	if len(actions) != 1 {
		t.Fatalf("one submission produced %d published actions", len(actions))
	}
	action := actions[0]
	if action.PlayerID != testPlayerID {
		t.Fatalf("the action carries player %q, want the session's %q", action.PlayerID, testPlayerID)
	}
	if action.ActionID != response.ActionID {
		t.Fatalf("the action id on the wire is %q and the answer says %q", action.ActionID, response.ActionID)
	}
	if action.Channel.Adapter != vocabulary.AdapterWebSocket {
		t.Fatalf("the channel binding names adapter %q", action.Channel.Adapter)
	}
	// The reply-to is the MINTED connection id, which is what makes it a hint
	// this process owns rather than anything the client said.
	if got := h.connIDs(testPlayerID); len(got) != 1 || action.Channel.ReplyTo != got[0] {
		t.Fatalf("the channel binding replies to %q, want the minted connection %v", action.Channel.ReplyTo, got)
	}
}

// Two sockets, two players, and each submission is attributed to the session it
// arrived on. A transport that submitted under a connection id other than its
// own would publish one player's action as another's, and the gateway could not
// tell — the connection id IS the session lookup.
func TestSubmit_EachSocketSubmitsAsItsOwnPlayer(t *testing.T) {
	h := newHarness(t, nil)
	pat := h.dial(t, patCredential, testPlayerID)
	alex := h.dial(t, alexCredential, testOtherID)

	if got := h.submit(t, pat, "key-1", "I open the gate."); got.Status != payload.StatusAccepted {
		t.Fatalf("the first player was refused: %+v", got.Refusal)
	}
	if got := h.submit(t, alex, "key-1", "I watch the gate."); got.Status != payload.StatusAccepted {
		t.Fatalf("the second player was refused: %+v", got.Refusal)
	}

	actions := h.publisher.actions(t)
	if len(actions) != 2 {
		t.Fatalf("two submissions produced %d actions", len(actions))
	}
	players := map[string]bool{actions[0].PlayerID: true, actions[1].PlayerID: true}
	if !players[testPlayerID] || !players[testOtherID] {
		t.Fatalf("the two actions were attributed to %v, want one to each player", players)
	}
	// The same idempotency key from two players must not collide: the action id
	// is derived from the SESSION's player, so a transport that used the wrong
	// connection would produce two actions with one id.
	if actions[0].ActionID == actions[1].ActionID {
		t.Fatal("two players using the same idempotency key produced one action id; the derivation is not " +
			"reading the session's own player")
	}
}

// A malformed frame is ANSWERED and the socket lives. A client that cannot see
// why it was refused repeats the mistake forever.
func TestSubmit_AMalformedFrameIsAnsweredAndTheSocketSurvives(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	malformed := map[string][]byte{
		"not json at all":      []byte("{not json"),
		"not a submission":     []byte(`{"protocol":"player/v1","text":"","idempotency_key":""}`),
		"a server-owned field": []byte(`{"protocol":"player/v1","text":"x","idempotency_key":"k","player_id":"p"}`),
		"an unknown field":     []byte(`{"protocol":"player/v1","text":"x","idempotency_key":"k","spell":"fire"}`),
		"another protocol":     []byte(`{"protocol":"player/v9","text":"x","idempotency_key":"k"}`),
	}
	for name, raw := range malformed {
		t.Run(name, func(t *testing.T) {
			if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
				t.Fatalf("write: %v", err)
			}
			response := readResponse(t, ws)
			if response.Status != payload.StatusRefused {
				t.Fatalf("%q was accepted", raw)
			}
			if response.Refusal.Code == payload.RefusalUnavailable {
				t.Fatalf("a client error was reported as an engine failure: %+v", response.Refusal)
			}
		})
	}

	// The socket is still usable, which is the half a close would have taken.
	if got := h.submit(t, ws, "key-1", "I try again, correctly."); got.Status != payload.StatusAccepted {
		t.Fatalf("the connection could not be used after a refusal: %+v", got.Refusal)
	}
	if len(h.publisher.actions(t)) != 1 {
		t.Fatalf("%d actions were published; only the well-formed submission should have been",
			len(h.publisher.actions(t)))
	}
}

func TestRequest_APresentUnknownTypeIsNotMisreportedAsASubmission(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	for name, test := range map[string]struct {
		raw  []byte
		code playersocket.OperationRefusalCode
	}{
		"unknown string": {[]byte(`{"protocol":"player/v1","type":"cast_spell"}`), playersocket.OperationUnsupported},
		"empty string":   {[]byte(`{"protocol":"player/v1","type":""}`), playersocket.OperationMalformed},
		"number":         {[]byte(`{"protocol":"player/v1","type":42}`), playersocket.OperationMalformed},
		"null":           {[]byte(`{"protocol":"player/v1","type":null}`), playersocket.OperationMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ws.WriteMessage(websocket.TextMessage, test.raw); err != nil {
				t.Fatalf("write: %v", err)
			}
			frame := readFrame(t, ws)
			if frame.Type != playersocket.FrameOperationResponse || frame.Operation == nil {
				t.Fatalf("typed operation was answered as %+v", frame)
			}
			if frame.Operation.Status != playersocket.OperationRefused {
				t.Fatalf("unknown operation was not refused: %+v", frame.Operation)
			}
			if frame.Operation.Refusal == nil || frame.Operation.Refusal.Code != test.code {
				t.Fatalf("operation refusal = %+v, want code %q", frame.Operation.Refusal, test.code)
			}
		})
	}

	if published := len(h.publisher.actions(t)); published != 0 {
		t.Fatalf("unknown operations published %d actions", published)
	}
	if got := h.submit(t, ws, "key-after-operation", "I use the ordinary submission path."); got.Status != payload.StatusAccepted {
		t.Fatalf("socket did not survive operation refusal: %+v", got.Refusal)
	}
}

// A binary frame is not a submission on this protocol, and it is answered rather
// than closed for the same reason.
func TestSubmit_ABinaryFrameIsRefusedAndTheSocketSurvives(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	if err := ws.WriteMessage(websocket.BinaryMessage, submission("key-1", "I open the gate.")); err != nil {
		t.Fatalf("write: %v", err)
	}
	response := readResponse(t, ws)
	if response.Refusal == nil || response.Refusal.Code != payload.RefusalMalformedRequest {
		t.Fatalf("a binary frame was answered %+v", response)
	}
	if published := len(h.publisher.actions(t)); published != 0 {
		t.Fatalf("a binary frame published %d actions", published)
	}
	if got := h.submit(t, ws, "key-1", "I open the gate."); got.Status != payload.StatusAccepted {
		t.Fatalf("the connection was unusable after a binary frame: %+v", got.Refusal)
	}
}

// Obligation (1): the READER carries the bound, so an oversize frame never
// becomes bytes this process holds.
//
// The close code is the assertion that distinguishes the two possible
// implementations. 1009 means gorilla refused the frame from its HEADER, before
// reading the body; a refusal FRAME would have meant the gateway's own check
// caught it after the whole thing was already in memory, which is the bound one
// allocation too late.
func TestSubmit_AnOversizeFrameEndsTheConnectionWithoutBeingRead(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	oversize := make([]byte, gateway.MaxRequestBytes+1)
	for i := range oversize {
		oversize[i] = 'a'
	}
	if err := ws.WriteMessage(websocket.TextMessage, oversize); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ws.SetReadDeadline(deadline()); err != nil {
		t.Fatalf("set the read deadline: %v", err)
	}
	_, raw, err := ws.ReadMessage()
	if err == nil {
		t.Fatalf("the server answered an oversize frame with %q; the reader was supposed to refuse it", raw)
	}
	if code := closeCodeOf(err); code != websocket.CloseMessageTooBig {
		t.Fatalf("an oversize frame closed with code %d (%v), want %d — a different code means the frame was "+
			"read into memory before anything refused it", code, err, websocket.CloseMessageTooBig)
	}
	if published := len(h.publisher.actions(t)); published != 0 {
		t.Fatalf("an oversize frame published %d actions", published)
	}
	// And the session is released, on a path nothing closed cleanly.
	h.awaitTargets(t, testPlayerID, 0)

	// The client was told by a close code and the OPERATOR by a log line;
	// without the second, a client that cannot connect has no visible cause.
	if missing := containsAll(h.logs.String(), "request budget"); len(missing) != 0 {
		t.Errorf("the operator log does not record the refused frame; logged:\n%s", h.logs.String())
	}
}

// The other side of the same bound: a frame AT the budget is read and answered.
// A reader limit set one byte low would kill a submission the engine's own
// contract says it accepts, and no test of the oversize case would notice.
func TestSubmit_AFrameAtExactlyTheRequestBudgetIsRead(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	// Padded with a field the protocol does not define, so the frame is exactly
	// at the budget and the engine's answer proves it was READ: an unknown-field
	// refusal can only be reached by parsing the whole document.
	prefix := fmt.Sprintf(`{"protocol":%q,"text":"I open the gate.","idempotency_key":"key-1","pad":"`,
		payload.PlayerProtocolV1)
	suffix := `"}`
	padding := gateway.MaxRequestBytes - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatalf("the fixture prefix is already %d bytes, past the %d-byte budget",
			len(prefix)+len(suffix), gateway.MaxRequestBytes)
	}
	frame := []byte(prefix + strings.Repeat("p", padding) + suffix)
	if len(frame) != gateway.MaxRequestBytes {
		t.Fatalf("the fixture is %d bytes, want exactly %d", len(frame), gateway.MaxRequestBytes)
	}

	if err := ws.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("write: %v", err)
	}
	response := readResponse(t, ws)
	if response.Refusal == nil || response.Refusal.Code != payload.RefusalUnknownField {
		t.Fatalf("a frame at exactly the budget was answered %+v, want an unknown-field refusal — anything "+
			"else means it was not parsed", response)
	}
}

// ---------------------------------------------------------- connection lifecycle

// Obligation (2), on the path that has no close handshake at all: the client's
// TCP connection is destroyed underneath the WebSocket, which is what a killed
// process or a yanked cable looks like.
func TestDisconnect_AnAbnormalCloseStopsDelivery(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	if err := ws.UnderlyingConn().Close(); err != nil {
		t.Fatalf("destroy the client's TCP connection: %v", err)
	}
	h.awaitTargets(t, testPlayerID, 0)

	outcome, err := h.router.Deliver(t.Context(),
		terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1")))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 0 {
		t.Fatalf("a turn was delivered to %d recipients after the socket died; a session that outlives its "+
			"socket is a delivery target that can never receive", outcome.Recipients)
	}
}

func TestDisconnect_AGracefulCloseStopsDelivery(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)

	if err := ws.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done for now")); err != nil {
		t.Fatalf("write the close frame: %v", err)
	}
	h.awaitTargets(t, testPlayerID, 0)
}

// A shutdown must reach sockets http.Server.Shutdown will not touch: every live
// WebSocket is a hijacked connection, and hijacked connections are exactly what
// Shutdown leaves alone.
func TestShutdown_CancellingTheServersContextEndsEverySession(t *testing.T) {
	h := newHarness(t, nil)
	pat := h.dial(t, patCredential, testPlayerID)
	h.dial(t, alexCredential, testOtherID)

	h.stop()

	h.awaitTargets(t, testPlayerID, 0)
	h.awaitTargets(t, testOtherID, 0)

	if err := pat.SetReadDeadline(deadline()); err != nil {
		t.Fatalf("set the read deadline: %v", err)
	}
	if _, _, err := pat.ReadMessage(); closeCodeOf(err) != websocket.CloseGoingAway {
		t.Fatalf("a shutdown ended the socket with %v, want a going-away close so a client can tell a "+
			"shutdown from a network fault", err)
	}
}

// ---------------------------------------------------------- liveness, not idleness

// A peer that stops answering is reaped, and its seat is released.
//
// The client here never READS, which is what stops gorilla answering the pings —
// so this is a socket whose peer is gone in every sense that matters, and the
// short interval is what makes it observable inside a test rather than in 45
// seconds.
//
// ON ITS OWN THIS TEST CANNOT TELL LIVENESS FROM IDLENESS, and that was found by
// mutation rather than assumed: deleting the ping probe entirely leaves this
// passing, because the read deadline alone reaps a socket that has SENT nothing.
// Its partner below is what makes the probe load-bearing — with the pings gone,
// a player who is merely quiet is disconnected, which is the failure this pair
// exists to catch. Neither test is redundant and neither is sufficient.
func TestLiveness_APeerThatStopsAnsweringIsReaped(t *testing.T) {
	h := newHarness(t, nil,
		playersocket.WithPingInterval(30*time.Millisecond),
		playersocket.WithPongTimeout(30*time.Millisecond))
	h.dial(t, patCredential, testPlayerID)

	h.awaitTargets(t, testPlayerID, 0)
}

// The pin that says the reaping above is LIVENESS and not an idle timeout.
//
// This client answers pings — which a running client does without its player
// doing anything — and submits nothing for many multiples of the probe interval.
// Email-cadence play is valid, so the session must survive; if this ever fails,
// somebody has turned the liveness probe into the interactive-pacing assumption
// this engine forbids.
func TestLiveness_APlayerWhoSaysNothingKeepsTheirSession(t *testing.T) {
	const probe = 20 * time.Millisecond
	h := newHarness(t, nil,
		playersocket.WithPingInterval(probe),
		playersocket.WithPongTimeout(probe))
	ws := h.dial(t, patCredential, testPlayerID)

	// A client that is merely RUNNING: it reads, so gorilla answers the pings,
	// and it submits nothing at all.
	frames := make(chan []byte, 4)
	go func() {
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				close(frames)
				return
			}
			frames <- raw
		}
	}()

	// Twenty probe intervals of silence. The wall-clock wait is the point of the
	// test rather than an artefact of it: what is being asserted is that elapsed
	// time alone does not end a session.
	time.Sleep(20 * probe)

	if got := len(h.gateway.SessionsFor(testPlayerID)); got != 1 {
		t.Fatalf("a player who said nothing for %v resolves to %d connections; an idle timeout is exactly "+
			"the interactive-pacing assumption email-cadence play forbids", 20*probe, got)
	}

	// And the session still works, which "the table has an entry" alone would
	// not prove.
	outcome, err := h.router.Deliver(t.Context(),
		terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1")))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 1 {
		t.Fatalf("the quiet player's socket received %d of 1 delivery", outcome.Recipients)
	}
	select {
	case raw, ok := <-frames:
		if !ok {
			t.Fatal("the client's socket ended while it was quiet")
		}
		if !strings.Contains(string(raw), string(playersocket.FrameTurnDelivery)) {
			t.Fatalf("the quiet player received %q", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delivery never reached the quiet player")
	}
}

// ---------------------------------------------------------- delivery

// The requirement the egress path exists for, asserted at the WIRE: one player's
// fiction reaches their socket and nobody else's. A single-connection test
// cannot state it, and neither can one that stops at the sink.
func TestDeliver_ReachesItsOwnPlayersSocketAndNoOther(t *testing.T) {
	h := newHarness(t, nil)
	pat := h.dial(t, patCredential, testPlayerID)
	alex := h.dial(t, alexCredential, testOtherID)

	turnID := turnIDFor(t, testPlayerID, "key-1")
	outcome, err := h.router.Deliver(t.Context(), terminalDelivery(t, testPlayerID, turnID))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 1 {
		t.Fatalf("the result reached %d sockets, want exactly the one belonging to its player",
			outcome.Recipients)
	}

	delivered := deliveredTurn(t, pat)
	if delivered.Result.TurnID != turnID || delivered.Result.PlayerID != testPlayerID {
		t.Fatalf("the player received %+v", delivered.Result)
	}
	// The assertion that matters.
	expectNothing(t, alex, "a second connected player received another player's turn")
}

// A delivery to a socket that has gone is an ERROR rather than a silent success.
// The router counts acceptances, and a sink that swallowed this would report a
// delivery nobody received.
func TestDeliver_AWriteToADeadSocketIsReportedRatherThanSwallowed(t *testing.T) {
	h := newHarness(t, nil)
	ws := h.dial(t, patCredential, testPlayerID)
	session := h.gateway.SessionsFor(testPlayerID)[0]

	if err := ws.UnderlyingConn().Close(); err != nil {
		t.Fatalf("destroy the client's TCP connection: %v", err)
	}
	h.awaitTargets(t, testPlayerID, 0)

	// Delivering through the SESSION rather than the router, because the router
	// can no longer resolve this connection at all — which is the structural
	// guarantee. What is under test here is the sink's own honesty.
	err := playersocket.NewSink().Deliver(t.Context(), session,
		terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1")))
	if err == nil {
		t.Fatal("a write to a closed socket reported success")
	}
}

// A socket that cannot absorb what it is sent is CLOSED rather than allowed to
// hold the egress consumer's ack deadline open for everybody behind it.
//
// The client here never reads, so the kernel buffers fill and the write blocks;
// with a short write deadline that becomes a failed write, and a failed write
// ends the connection. The loop bound is generous — the point is that the socket
// stops being a delivery target, not how many writes it took.
func TestDeliver_ASocketThatCannotAbsorbTheDocumentIsClosed(t *testing.T) {
	h := newHarness(t, nil, playersocket.WithWriteTimeout(50*time.Millisecond))
	h.dial(t, patCredential, testPlayerID)

	delivery := proseDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1"))
	failed := false
	for attempt := range 4000 {
		outcome, err := h.router.Deliver(t.Context(), delivery)
		if err != nil || outcome.Recipients == 0 {
			t.Logf("the wedged socket failed on attempt %d: %v", attempt, err)
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("4000 documents were accepted by a socket nobody was reading; either the write deadline is " +
			"not being applied or this machine's buffers are larger than this test can fill")
	}
	h.awaitTargets(t, testPlayerID, 0)
}

// ---------------------------------------------------------- routing

func TestServeHTTP_AnotherPathIsNotASocket(t *testing.T) {
	h := newHarness(t, nil)

	dialer := &websocket.Dialer{}
	_, response, err := dialer.DialContext(t.Context(),
		strings.TrimSuffix(h.url, playersocket.DefaultPath)+"/", nil)
	if err == nil {
		t.Fatal("a request to another path was upgraded")
	}
	if response.StatusCode != 404 {
		t.Fatalf("a request to another path was answered %d, want 404", response.StatusCode)
	}
}
