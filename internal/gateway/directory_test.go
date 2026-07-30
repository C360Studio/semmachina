package gateway_test

import (
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/gateway"
)

// The reading side of targeted egress: resolving a player's live connections
// from their entity id, at delivery time. The delivery itself lives in
// internal/egress; these tests prove the index answers by PLAYER and that a
// connection that left is not in the answer.

func connIDs(sessions []gateway.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.Connection.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestSessionsFor_ResolvesThePlayersOwnConnectionsAndNobodyElses(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, "conn-pat")
	h.authenticate(t, "alex-local-key", "conn-alex")

	pat := h.gateway.SessionsFor(testPlayerID)
	if got := connIDs(pat); !slices.Equal(got, []string{"conn-pat"}) {
		t.Fatalf("SessionsFor(%s) = %v, want only that player's own connection", testPlayerID, got)
	}

	// The assertion that matters, and the one a single-player fixture cannot
	// make: the OTHER connected player is absent. A directory that returned
	// every session would satisfy "the result was delivered" while showing one
	// player's narration to everyone in the campaign.
	if slices.Contains(connIDs(pat), "conn-alex") {
		t.Fatal("one player's lookup returned another player's connection; that is the broadcast defect " +
			"arriving through the directory instead of through the socket")
	}

	alex := h.gateway.SessionsFor(testOtherID)
	if got := connIDs(alex); !slices.Equal(got, []string{"conn-alex"}) {
		t.Fatalf("SessionsFor(%s) = %v, want only that player's own connection", testOtherID, got)
	}
}

func TestSessionsFor_AnUnknownOrUnconnectedPlayerResolvesToNothing(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	// Not an error, deliberately. A player who is not connected is a player the
	// durable retrieval surface answers, and email-cadence play means "nobody is
	// on the socket" is an ordinary Tuesday rather than a fault.
	if sessions := h.gateway.SessionsFor(testOtherID); len(sessions) != 0 {
		t.Fatalf("an unconnected player resolved to %v", connIDs(sessions))
	}
	if sessions := h.gateway.SessionsFor("c360.semmachina.world1.starter.player.nobody"); len(sessions) != 0 {
		t.Fatalf("a player of no world resolved to %v", connIDs(sessions))
	}
	if sessions := h.gateway.SessionsFor(""); len(sessions) != 0 {
		t.Fatalf("the empty player id resolved to %v; an unauthenticated lookup must not match everybody",
			connIDs(sessions))
	}
}

// The reconnect property from the DELIVERY side. Identity is not a connection, so
// a result addressed to the player reaches whatever socket they are on now — not
// the one they submitted from.
func TestSessionsFor_ARecconnectMovesTheDeliveryTargetWithoutChangingIdentity(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, "conn-1")
	h.gateway.Disconnect("conn-1")
	h.authenticate(t, testCredential, "conn-2")

	sessions := h.gateway.SessionsFor(testPlayerID)
	if got := connIDs(sessions); !slices.Equal(got, []string{"conn-2"}) {
		t.Fatalf("after a reconnect the player resolves to %v, want only the live connection; a delivery to "+
			"a socket that dropped is a result the player never sees", got)
	}
}

// A second concurrent connection claiming one player CONNECTS and RECEIVES:
// multi-device is the posture, bounded by WithMaxConnectionsPerPlayer rather
// than refused. What this test fixes is that delivery follows the index rather
// than a captured connection: whatever sessions exist for that player are the
// targets, and none of anyone else's are.
func TestSessionsFor_TwoConnectionsForOnePlayerAreBothTargetsAndStillOnlyTheirs(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, "conn-a")
	h.authenticate(t, testCredential, "conn-b")
	h.authenticate(t, "alex-local-key", "conn-alex")

	if got := connIDs(h.gateway.SessionsFor(testPlayerID)); !slices.Equal(got, []string{"conn-a", "conn-b"}) {
		t.Fatalf("SessionsFor = %v, want both of that player's connections", got)
	}

	h.gateway.Disconnect("conn-a")
	if got := connIDs(h.gateway.SessionsFor(testPlayerID)); !slices.Equal(got, []string{"conn-b"}) {
		t.Fatalf("after one socket dropped the player resolves to %v, want the surviving one; an index that "+
			"kept a dead connection would deliver into a closed socket forever", got)
	}

	h.gateway.Disconnect("conn-b")
	if sessions := h.gateway.SessionsFor(testPlayerID); len(sessions) != 0 {
		t.Fatalf("after every socket dropped the player still resolves to %v", connIDs(sessions))
	}
	// The other player is untouched by any of it.
	if got := connIDs(h.gateway.SessionsFor(testOtherID)); !slices.Equal(got, []string{"conn-alex"}) {
		t.Fatalf("disconnecting one player's sockets changed another player's targets: %v", got)
	}
}

// One connection id is one session. A transport that re-authenticated the same
// socket must not leave the index holding two entries for it, or every delivery
// to that player would be sent twice down one wire.
func TestSessionsFor_ReauthenticatingOneConnectionDoesNotDoubleIt(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.authenticate(t, testCredential, testConnID)

	if got := connIDs(h.gateway.SessionsFor(testPlayerID)); !slices.Equal(got, []string{testConnID}) {
		t.Fatalf("SessionsFor = %v, want one entry for one socket", got)
	}
}

// A connection that re-authenticates as a DIFFERENT player must leave the first
// player's index. Otherwise that player keeps a delivery target which is now
// somebody else's socket — a disclosure defect with no visible cause.
func TestSessionsFor_RebindingAConnectionLeavesNoTargetOnTheOldPlayer(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.authenticate(t, "alex-local-key", testConnID)

	if sessions := h.gateway.SessionsFor(testPlayerID); len(sessions) != 0 {
		t.Fatalf("the first player still resolves to %v after that socket rebound to another player; their "+
			"next result would be delivered to somebody else's connection", connIDs(sessions))
	}
	if got := connIDs(h.gateway.SessionsFor(testOtherID)); !slices.Equal(got, []string{testConnID}) {
		t.Fatalf("the rebound player resolves to %v, want the connection", got)
	}
}

// Delivery order is an assertable fact rather than a map iteration, so a test
// that fans out over two connections can say what it expects.
func TestSessionsFor_AnswersInAStableOrder(t *testing.T) {
	h := newHarness(t)
	for _, connID := range []string{"conn-z", "conn-a", "conn-m"} {
		h.authenticate(t, testCredential, connID)
	}

	want := []string{"conn-a", "conn-m", "conn-z"}
	for attempt := range 8 {
		got := make([]string, 0, 3)
		for _, session := range h.gateway.SessionsFor(testPlayerID) {
			got = append(got, session.Connection.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("attempt %d answered %v, want %v in a stable order", attempt, got, want)
		}
	}
}
