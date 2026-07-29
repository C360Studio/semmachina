package egress_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/egress"
)

// The requirement this whole package exists for, and it can only be stated with
// TWO connected players: the assertion that matters is what does NOT arrive.
//
// A single-connection test cannot make it. Upstream's output/websocket would pass
// one — it delivers the result, to everybody — and the defect is invisible until
// a second player is on the wire.
func TestDeliver_ReachesTheTurnsOwnPlayerAndNoOtherConnectedPlayer(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.directory.connect(testPlayerID, "conn-pat")
	h.directory.connect(testOtherID, "conn-alex")

	delivered, err := h.router.Deliver(t.Context(), h.mustDeliver(t, testTurnID))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivered.Recipients != 1 {
		t.Fatalf("the result reached %d connections, want exactly the one belonging to its player",
			delivered.Recipients)
	}

	if got := len(h.sink.to("conn-pat")); got != 1 {
		t.Fatalf("the turn's own player received %d documents, want 1", got)
	}
	// The assertion that matters.
	if got := h.sink.to("conn-alex"); len(got) != 0 {
		t.Fatalf("a second connected player received %+v; broadcast egress is a DISCLOSURE defect — that is "+
			"one player reading another's fiction, not a performance problem", got)
	}
}

// Two players, one resolving turn each, delivered in sequence: each document goes
// to its own player and neither crosses. This is the shape a fan-out bug produces
// that the single-turn test above would still pass — everybody gets something,
// and half of it is somebody else's.
func TestDeliver_TwoPlayersTurnsDoNotCross(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, "turn-act-1", testPlayerID, testTime)
	h.completedTurn(t, "turn-act-2", testOtherID, testTime)
	h.directory.connect(testPlayerID, "conn-pat")
	h.directory.connect(testOtherID, "conn-alex")

	for _, turnID := range []string{"turn-act-1", "turn-act-2"} {
		if _, err := h.router.Deliver(t.Context(), h.mustDeliver(t, turnID)); err != nil {
			t.Fatalf("Deliver %s: %v", turnID, err)
		}
	}

	pat := h.sink.to("conn-pat")
	alex := h.sink.to("conn-alex")
	if len(pat) != 1 || pat[0].TurnID != "turn-act-1" || pat[0].PlayerID != testPlayerID {
		t.Fatalf("one player received %+v, want only their own turn", pat)
	}
	if len(alex) != 1 || alex[0].TurnID != "turn-act-2" || alex[0].PlayerID != testOtherID {
		t.Fatalf("the other player received %+v, want only their own turn", alex)
	}
}

// Resolution happens long after submission, so the connection is resolved NOW.
// TurnResult carries no channel binding precisely so this cannot be got wrong:
// there is nothing captured at submission to dial.
func TestDeliver_ResolvesTheConnectionAtDeliveryTimeNotAtSubmission(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)

	// The connection the player submitted on is gone; they are on a new one.
	h.directory.connect(testPlayerID, "conn-after-reconnect")

	if _, err := h.router.Deliver(t.Context(), h.mustDeliver(t, testTurnID)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got := len(h.sink.to("conn-after-reconnect")); got != 1 {
		t.Fatalf("the reconnected connection received %d documents, want 1", got)
	}
	if got := len(h.sink.to("conn-before-reconnect")); got != 0 {
		t.Fatalf("a connection that no longer exists received %d documents", got)
	}
}

// Nobody connected is an ordinary outcome. At email cadence the usual state of a
// player is "away", and an error here would make a normal Tuesday look like a
// fault — or worse, invite a retry loop that is adapter memory in disguise.
func TestDeliver_NobodyConnectedIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)

	delivered, err := h.router.Deliver(t.Context(), h.mustDeliver(t, testTurnID))
	if err != nil {
		t.Fatalf("delivering to an absent player reported an error: %v", err)
	}
	if delivered.Recipients != 0 {
		t.Fatalf("Recipients = %d with nobody connected", delivered.Recipients)
	}
	if delivered.PlayerID != testPlayerID {
		t.Fatalf("PlayerID = %q, want the player the result was addressed to", delivered.PlayerID)
	}
	if len(h.sink.sent) != 0 {
		t.Fatalf("something was delivered to nobody: %+v", h.sink.sent)
	}
}

// One wedged socket must not silence a player's other one. Abandoning the fan-out
// at the first error would turn a broken connection into a delivery outage.
func TestDeliver_OneConnectionsFailureDoesNotSilenceTheOthers(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.directory.connect(testPlayerID, "conn-broken", "conn-good")
	h.sink.err["conn-broken"] = errors.New("write: broken pipe")

	delivered, err := h.router.Deliver(t.Context(), h.mustDeliver(t, testTurnID))
	if err == nil {
		t.Fatal("a failed write was reported as success; the caller would acknowledge a delivery that " +
			"half happened")
	}
	if !strings.Contains(err.Error(), "conn-broken") {
		t.Errorf("the failure %q does not name the connection that broke", err)
	}
	if delivered.Recipients != 1 {
		t.Fatalf("Recipients = %d, want the one connection that did receive it", delivered.Recipients)
	}
	if got := len(h.sink.to("conn-good")); got != 1 {
		t.Fatalf("the working connection received %d documents; one broken socket silenced the player", got)
	}
}

// The last point at which an incoherent document can be stopped. Past here it is
// whatever arrived in front of a player.
func TestDeliver_RefusesAnIncoherentDocumentRatherThanSendingIt(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.directory.connect(testPlayerID, "conn-pat")

	delivery := h.mustDeliver(t, testTurnID)
	// Prose belonging to another turn — the disclosure shape, injected past the
	// composer that would have refused it.
	delivery.Narration.TurnID = "turn-act-somebodyelse"

	if _, err := h.router.Deliver(t.Context(), delivery); err == nil {
		t.Fatal("an incoherent delivery was sent")
	}
	if len(h.sink.sent) != 0 {
		t.Fatalf("the refusal happened after the write: %+v", h.sink.sent)
	}

	if _, err := h.router.Deliver(t.Context(), nil); err == nil {
		t.Fatal("a nil delivery was accepted")
	}
}

// The delivered document is the composed one, unmodified — asserted at the sink,
// which is the last place it can be observed before a transport touches it.
func TestDeliver_HandsTheSinkTheDocumentTheComposerBuilt(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.directory.connect(testPlayerID, "conn-pat")

	composed := h.mustDeliver(t, testTurnID)
	if _, err := h.router.Deliver(t.Context(), composed); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	sent := h.sink.to("conn-pat")
	if len(sent) != 1 {
		t.Fatalf("the sink saw %d documents", len(sent))
	}
	if sent[0].TurnID != composed.Result.TurnID || sent[0].PlayerID != composed.Result.PlayerID {
		t.Fatalf("the sink saw %+v, want the composed result's identity", sent[0])
	}
	if sent[0].Prose != composed.Narration.Prose {
		t.Fatalf("the sink saw prose %q, want the resolved prose %q", sent[0].Prose, composed.Narration.Prose)
	}
}

func TestNewRouter_RefusesAHalfWiredDeliveryPath(t *testing.T) {
	h := newHarness(t)
	if _, err := egress.NewRouter(nil, h.sink); err == nil {
		t.Error("a router with no directory was accepted; the only way to deliver without one is to " +
			"deliver to everybody")
	}
	if _, err := egress.NewRouter(h.directory, nil); err == nil {
		t.Error("a router with no sink was accepted")
	}
}

// A compile-time statement of the structural claim: the delivery path is handed a
// lookup BY PLAYER and nothing else, so it cannot enumerate every session even if
// somebody later wanted it to.
func TestDirectory_OffersNoWayToReachEverybody(t *testing.T) {
	var directory egress.Directory = newFakeDirectory()
	if _, isEnumerable := directory.(interface{ AllSessions() []any }); isEnumerable {
		t.Fatal("the directory exposes a way to enumerate every session; targeting must be structural")
	}
	// Anti-vacuity: the one method it does have works.
	fake := newFakeDirectory()
	fake.connect(testPlayerID, "conn-pat")
	if len(fake.SessionsFor(testPlayerID)) != 1 {
		t.Fatal("the directory's only method does not answer, so the assertion above proves nothing")
	}
}
