package playersocket_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/playersocket"
)

// The posture, tested rather than documented. Every test here is an answer to
// one of task 9.3's questions, and the ones that matter most assert what does
// NOT happen.

// ---------------------------------------------------------- local-only

// The bind gate: an address anything off the box could reach is a boot failure,
// not a warning nobody reads.
func TestNewServer_RefusesAnAddressBeyondLoopback(t *testing.T) {
	exposed := []string{
		":8080",          // every interface, and the likeliest accident
		"0.0.0.0:8080",   // the same thing said out loud
		"[::]:8080",      // and again, over v6
		"192.168.1.5:80", // a LAN address somebody typed deliberately
	}
	for _, addr := range exposed {
		t.Run(addr, func(t *testing.T) {
			_, err := playersocket.NewServer(newGate(t, newFakeStore(), &fakePublisher{}, &logCapture{}),
				playersocket.Config{Addr: addr})
			if err == nil {
				t.Fatalf("the player socket accepted %q; the local-only posture would be gone with no other "+
					"visible change to this deployment", addr)
			}
			if !strings.Contains(err.Error(), "AllowNonLoopback") {
				t.Errorf("the refusal for %q does not name the acknowledgement that would allow it: %v", addr, err)
			}
		})
	}
}

func TestNewServer_AcceptsLoopbackAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		t.Run(addr, func(t *testing.T) {
			if _, err := playersocket.NewServer(
				newGate(t, newFakeStore(), &fakePublisher{}, &logCapture{}),
				playersocket.Config{Addr: addr}); err != nil {
				t.Fatalf("the player socket refused the loopback address %q: %v", addr, err)
			}
		})
	}
}

// The acknowledgement is not a quiet flag. Setting it prints the list of
// controls that do not exist, because the operator setting it is the last person
// who will read the package doc.
func TestNewServer_AcknowledgedExposureAnnouncesWhatStopsBeingTrue(t *testing.T) {
	capture := &logCapture{}
	if _, err := playersocket.NewServer(
		newGate(t, newFakeStore(), &fakePublisher{}, capture),
		playersocket.Config{Addr: "0.0.0.0:8080", AllowNonLoopback: true},
		playersocket.WithLogger(newLogger(capture))); err != nil {
		t.Fatalf("an acknowledged exposure was still refused: %v", err)
	}

	logged := capture.String()
	if missing := containsAll(logged,
		"WARN",
		"NO LONGER HOLDS",
		"rate limit",
		"TLS",
		"revocation",
		"allow-list",
		// The one resource on this path with no ceiling: a handshake needs no
		// credential to consume a file descriptor, and nothing caps how many are
		// in flight. It is inert on loopback and belongs on this list precisely
		// because this list is read at the moment loopback stops being the case.
		"handshakes",
	); len(missing) != 0 {
		t.Fatalf("exposing the port did not announce %v; the warning is the whole mechanism that keeps "+
			"local-only from becoming a silent assumption. Logged:\n%s", missing, logged)
	}
}

// The authoritative gate reads what the KERNEL bound, not what the config said.
// A caller who builds their own listener never passes through Config at all.
func TestServe_RefusesAListenerBoundBeyondLoopback(t *testing.T) {
	server, err := playersocket.NewServer(
		newGate(t, newFakeStore(), &fakePublisher{}, &logCapture{}),
		playersocket.Config{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close() //nolint:errcheck // best effort in teardown

	if err := server.Serve(t.Context(), listener); err == nil {
		t.Fatal("the player socket served a wildcard listener it was handed; the configured-address check " +
			"cannot see a listener somebody else built, which is exactly why this one reads the bound address")
	}
}

// The runtime gate: even on a loopback listener, a request whose peer is not
// local is refused BEFORE its credential is read. It is the layer that survives
// a port forward that the bind gate never saw.
func TestServeHTTP_RefusesANonLoopbackPeerBeforeReadingItsCredential(t *testing.T) {
	capture := &logCapture{}
	gw := newGate(t, newFakeStore(), &fakePublisher{}, capture)
	server, err := playersocket.NewServer(gw, playersocket.Config{},
		playersocket.WithLogger(newLogger(capture)))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, playersocket.DefaultPath, nil)
	request.RemoteAddr = "203.0.113.7:44321"
	// A GOOD credential, deliberately: the refusal must not depend on the
	// credential being wrong, or a remote client with a leaked one would be in.
	request.Header.Set("Authorization", "Bearer "+patCredential)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("a non-loopback peer with a valid credential was answered %d, want %d",
			recorder.Code, http.StatusForbidden)
	}
	if sessions := gw.SessionsFor(testPlayerID); len(sessions) != 0 {
		t.Fatalf("the refused request bound %d sessions", len(sessions))
	}
	if !strings.Contains(capture.String(), "local-only") {
		t.Errorf("the refusal was not announced to the operator; logged:\n%s", capture.String())
	}
}

// ---------------------------------------------------------- authentication

// A wrong credential never becomes a socket. That is the whole answer to "what
// may an unauthenticated socket do, and for how long" — there is no socket.
func TestDial_AWrongCredentialIsRefusedBeforeTheUpgrade(t *testing.T) {
	h := newHarness(t, nil)

	for _, credential := range []string{"", "not-a-credential", patCredential + "x"} {
		ws, response, err := h.rawDial(t, credential)
		if err == nil {
			ws.Close() //nolint:errcheck // the test has already failed
			t.Fatalf("credential %q was upgraded into a socket", credential)
		}
		if !errors.Is(err, websocket.ErrBadHandshake) {
			t.Fatalf("credential %q failed with %v, want a refused handshake", credential, err)
		}
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("credential %q was answered %d, want %d", credential, response.StatusCode,
				http.StatusUnauthorized)
		}
	}
	if sessions := h.gateway.SessionsFor(testPlayerID); len(sessions) != 0 {
		t.Fatalf("a refused credential left %d sessions bound", len(sessions))
	}
}

// The refusal is not a membership oracle. It tells a client nothing about who
// exists, what the credential resembled, or which half failed.
func TestDial_ARefusalTellsTheClientNothingItDidNotAlreadyKnow(t *testing.T) {
	h := newHarness(t, nil)

	// A credential naming a player this world does not have would be the other
	// half; the roster cannot express one, so the closest reachable case is a
	// credential that resolves to a player whose entity is gone.
	h.store.mu.Lock()
	delete(h.store.entities, testPlayerID)
	h.store.mu.Unlock()

	_, unknown, err := h.rawDial(t, "not-a-credential")
	if err == nil {
		t.Fatal("an unknown credential was upgraded")
	}
	_, absent, err := h.rawDial(t, patCredential)
	if err == nil {
		t.Fatal("a credential naming an absent player was upgraded")
	}
	if unknown.StatusCode != absent.StatusCode {
		t.Fatalf("an unknown credential is answered %d and one naming an absent player %d; the difference is "+
			"a membership oracle", unknown.StatusCode, absent.StatusCode)
	}

	body := readBody(t, absent)
	for _, secret := range []string{testPlayerID, patCredential, "stub", "entity"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(secret)) {
			t.Fatalf("the refusal body %q names %q", body, secret)
		}
	}
}

// The proof by absence for an unauthenticated client: a turn resolves for the
// player whose credential was guessed at, and nothing is delivered anywhere.
func TestDial_ARefusedClientIsNotADeliveryTarget(t *testing.T) {
	h := newHarness(t, nil)
	if _, _, err := h.rawDial(t, "not-a-credential"); err == nil {
		t.Fatal("the wrong credential connected")
	}

	outcome, err := h.router.Deliver(t.Context(),
		terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1")))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 0 {
		t.Fatalf("a turn was delivered to %d recipients after every connection attempt was refused",
			outcome.Recipients)
	}
}

// ---------------------------------------------------------- connection identity

// Obligation (3): a connection id must never be reused without going back
// through Bind. It is discharged by minting ids here, so this is a test that the
// mint never repeats itself across sessions that come and go.
func TestDial_ConnectionIDsAreMintedHereAndNeverReused(t *testing.T) {
	h := newHarness(t, nil)

	seen := map[string]bool{}
	for range 12 {
		ws := h.dial(t, patCredential, testPlayerID)
		ids := h.connIDs(testPlayerID)
		if len(ids) != 1 {
			t.Fatalf("one live socket resolves to %v", ids)
		}
		if seen[ids[0]] {
			t.Fatalf("connection id %q was minted twice; a recycled id inherits the previous session, which is "+
				"a socket that can submit as the old player and read their fiction", ids[0])
		}
		seen[ids[0]] = true

		ws.Close() //nolint:errcheck // the drop is the point
		h.awaitTargets(t, testPlayerID, 0)
	}
}

// Two servers over one gateway must not collide either. The nonce is what makes
// that true, and a counter alone would not.
func TestDial_TwoServersOverOneGatewayMintDisjointConnectionIDs(t *testing.T) {
	first := newHarness(t, nil)
	second := newServerOn(t, first.gateway)

	firstWS := first.dial(t, patCredential, testPlayerID)
	defer firstWS.Close() //nolint:errcheck // best effort in teardown
	secondWS := second.dial(t, patCredential, testPlayerID)
	defer secondWS.Close() //nolint:errcheck // best effort in teardown

	first.awaitTargets(t, testPlayerID, 2)
	ids := first.connIDs(testPlayerID)
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("two servers minted %v; a collision would have one socket's Disconnect release the other's "+
			"session", ids)
	}
}

// Nothing on the wire can name a connection. There is no header, parameter or
// field for it — this test states that as behaviour rather than as an absence in
// the source.
func TestDial_AClientCannotChooseItsConnectionID(t *testing.T) {
	h := newHarness(t, nil)

	header := http.Header{}
	header.Set("Authorization", "Bearer "+patCredential)
	header.Set("X-Connection-Id", "ws-stolen-1")
	dialer := &websocket.Dialer{}
	ws, _, err := dialer.DialContext(t.Context(), h.url+"?conn_id=ws-stolen-1&connection=ws-stolen-1", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck // best effort in teardown
	h.awaitTargets(t, testPlayerID, 1)

	if got := h.connIDs(testPlayerID); got[0] == "ws-stolen-1" {
		t.Fatalf("the client named its own connection %q", got[0])
	}
}

// ---------------------------------------------------------- the per-player cap

// Multi-device is the posture: several sockets for one player, all of them
// delivery targets.
func TestDial_APlayersSocketsAreAllDeliveryTargets(t *testing.T) {
	h := newHarness(t, []gateway.Option{gateway.WithMaxConnectionsPerPlayer(2)})
	laptop := h.dial(t, patCredential, testPlayerID)
	phone := h.dial(t, patCredential, testPlayerID)

	turnID := turnIDFor(t, testPlayerID, "key-1")
	outcome, err := h.router.Deliver(t.Context(), terminalDelivery(t, testPlayerID, turnID))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 2 {
		t.Fatalf("a player on two sockets was delivered to %d of them", outcome.Recipients)
	}
	for name, ws := range map[string]*websocket.Conn{"laptop": laptop, "phone": phone} {
		if got := deliveredTurn(t, ws).Result.TurnID; got != turnID {
			t.Fatalf("the %s received turn %q, want %q", name, got, turnID)
		}
	}
}

// The cap refuses the NEWEST socket and leaves the existing ones untouched. The
// second half is the one that matters: eviction would make a leaked credential
// into a way to take somebody's campaign away from them.
func TestDial_TheCapRefusesTheNewestSocketAndLeavesTheOthersDeliverable(t *testing.T) {
	h := newHarness(t, []gateway.Option{gateway.WithMaxConnectionsPerPlayer(2)})
	laptop := h.dial(t, patCredential, testPlayerID)
	phone := h.dial(t, patCredential, testPlayerID)
	held := h.connIDs(testPlayerID)

	// The handshake SUCCEEDS — the cap is not an authentication decision, and it
	// cannot be answered with a status code because it is only knowable once
	// there is a socket to bind. The client learns from the close.
	third, _, err := h.rawDial(t, patCredential)
	if err != nil {
		t.Fatalf("the third dial failed at the handshake: %v", err)
	}
	defer third.Close() //nolint:errcheck // best effort in teardown

	if err := third.SetReadDeadline(deadline()); err != nil {
		t.Fatalf("set the read deadline: %v", err)
	}
	if _, _, err := third.ReadMessage(); closeCodeOf(err) != websocket.ClosePolicyViolation {
		t.Fatalf("the capped connection ended with %v, want a policy-violation close naming the cap", err)
	}

	h.awaitTargets(t, testPlayerID, 2)
	if got := h.connIDs(testPlayerID); !sameSet(got, held) {
		t.Fatalf("the player now holds %v, held %v before the refused connection; a cap that evicts is a way "+
			"to take somebody's campaign away from them with a stolen credential", got, held)
	}

	// And the sockets they held still receive.
	outcome, err := h.router.Deliver(t.Context(),
		terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1")))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if outcome.Recipients != 2 {
		t.Fatalf("after a refused connection the player's own sockets received %d of 2", outcome.Recipients)
	}
	readFrame(t, laptop)
	readFrame(t, phone)
}

// One player at their cap does not close the instance to anybody else.
func TestDial_TheCapIsPerPlayer(t *testing.T) {
	h := newHarness(t, []gateway.Option{gateway.WithMaxConnectionsPerPlayer(1)})
	h.dial(t, patCredential, testPlayerID)

	alex := h.dial(t, alexCredential, testOtherID)
	if err := alex.WriteMessage(websocket.TextMessage, submission("key-1", "I wait.")); err != nil {
		t.Fatalf("the second player could not submit: %v", err)
	}
	readResponse(t, alex)
}
