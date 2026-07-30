package gateway_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The half of task 9.3's posture that cannot live in the transport.
//
// How many sockets one player may hold at once is a bound on the SESSION TABLE,
// and the table is here. A transport that counted first and bound second would
// be enforcing it across a gap two of its own goroutines can drive through, and
// every connection admitted through that gap is a delivery target — so the count
// and the bind happen under one lock or the cap is decoration.

func TestBind_RefusesTheNewestConnectionPastTheCap(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(2))
	h.authenticate(t, testCredential, "conn-a")
	h.authenticate(t, testCredential, "conn-b")

	_, err := h.gateway.Authenticate(t.Context(), testCredential, conn("conn-c"))
	if !errors.Is(err, gateway.ErrTooManyConnections) {
		t.Fatalf("a third connection past a cap of two produced %v, want ErrTooManyConnections", err)
	}
	if _, bound := h.gateway.Session("conn-c"); bound {
		t.Fatal("the refused connection still has a session; a refusal that binds is not a refusal")
	}
}

// Refusing the NEWEST rather than evicting the oldest, and the direction is the
// decision rather than an implementation detail.
//
// Eviction would make a leaked credential into a way to kick the real player off
// their own campaign: connect until the cap, and every socket they hold is
// dropped. Refusal costs the newcomer a connection and costs the player nothing,
// and the sockets they already hold keep receiving.
func TestBind_ACapRefusalLeavesEveryExistingConnectionDeliverable(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(2))
	h.authenticate(t, testCredential, "conn-a")
	h.authenticate(t, testCredential, "conn-b")

	if _, err := h.gateway.Authenticate(t.Context(), testCredential, conn("conn-c")); err == nil {
		t.Fatal("the cap admitted a third connection")
	}

	if got := connIDs(h.gateway.SessionsFor(testPlayerID)); !slices.Equal(got, []string{"conn-a", "conn-b"}) {
		t.Fatalf("after a refused connection the player resolves to %v, want the two they already held; a cap "+
			"that evicts is a way to take somebody's campaign away from them with a stolen credential", got)
	}
}

// A connection that re-authenticates is not a new slot. It already occupies one,
// and counting it twice would refuse a socket that is allowed to be there.
func TestBind_RebindingAnExistingConnectionIsNotANewSlot(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(1))
	h.authenticate(t, testCredential, testConnID)
	h.authenticate(t, testCredential, testConnID)

	if got := connIDs(h.gateway.SessionsFor(testPlayerID)); !slices.Equal(got, []string{testConnID}) {
		t.Fatalf("SessionsFor = %v, want the one socket", got)
	}
}

// A connection that re-authenticates as a DIFFERENT player releases the first
// player's slot as it takes the second player's. Otherwise a socket that rebound
// would hold a slot on a player it no longer belongs to, and that player would
// be locked out by a connection that is not theirs.
func TestBind_RebindingReleasesTheFormerPlayersSlot(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(1))
	h.authenticate(t, testCredential, testConnID)
	h.authenticate(t, "alex-local-key", testConnID)

	if _, err := h.gateway.Authenticate(t.Context(), testCredential, conn("conn-second")); err != nil {
		t.Fatalf("the first player's slot was not released by the rebind: %v", err)
	}
}

// A rebind that is REFUSED leaves the connection holding nothing.
//
// The residue this rules out is invisible from every other angle: the connection
// has already been taken out of its former player's delivery index by the time
// the cap refuses, so an entry left in the connection index is a socket that can
// still SUBMIT as the player it used to be while receiving none of their
// results. It asked to become somebody else and was told no; carrying on as the
// previous player is not the answer, and nothing anywhere would report it.
func TestBind_ARefusedRebindLeavesTheConnectionWithNoSessionAtAll(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(1))
	h.authenticate(t, testCredential, testConnID)
	h.authenticate(t, "alex-local-key", "conn-alex")

	// The other player is at their cap, so this rebind cannot succeed.
	if _, err := h.gateway.Authenticate(t.Context(), "alex-local-key", conn(testConnID)); err == nil {
		t.Fatal("the rebind was admitted, so this test proves nothing about a refused one")
	}

	if session, bound := h.gateway.Session(testConnID); bound {
		t.Fatalf("the connection still holds a session for player %q after a refused rebind", session.PlayerID)
	}
	if sessions := h.gateway.SessionsFor(testPlayerID); len(sessions) != 0 {
		t.Fatalf("the former player still resolves to %v", connIDs(sessions))
	}
	// The behaviour that residue would produce, asserted at the surface a client
	// reaches: submitting as the player this connection used to be.
	if got := h.refusal(t, testConnID, submission("key-1", "I act as who I was.")); got.Code !=
		payload.RefusalUnauthenticated {
		t.Fatalf("a connection with no session submitted and was refused %q, want %q",
			got.Code, payload.RefusalUnauthenticated)
	}
}

// The cap is per player. One player filling theirs must not close the table to
// anybody else — an instance-per-world box has a known cast, and one of them
// leaving the tab open is not a reason the others cannot play.
func TestBind_TheCapIsPerPlayerAndNotGlobal(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(1))
	h.authenticate(t, testCredential, "conn-pat")

	if _, err := h.gateway.Authenticate(t.Context(), "alex-local-key", conn("conn-alex")); err != nil {
		t.Fatalf("a second PLAYER was refused because the first filled their cap: %v", err)
	}
}

// A slot is released by Disconnect, so a player who reconnects after their cap
// filled with dead sockets is not locked out for as long as the process lives.
func TestBind_DisconnectingReleasesASlot(t *testing.T) {
	h := newHarness(t, gateway.WithMaxConnectionsPerPlayer(1))
	h.authenticate(t, testCredential, "conn-a")
	if _, err := h.gateway.Authenticate(t.Context(), testCredential, conn("conn-b")); err == nil {
		t.Fatal("the cap admitted a second connection")
	}

	h.gateway.Disconnect("conn-a")
	if _, err := h.gateway.Authenticate(t.Context(), testCredential, conn("conn-b")); err != nil {
		t.Fatalf("a disconnect did not release the slot: %v", err)
	}
}

// Verify answers WHO a credential names and binds nothing.
//
// The split exists for the transport: it must be able to refuse a bad credential
// with an HTTP status before a socket exists at all, and it cannot bind a session
// until it has a socket to write to. A Verify that quietly bound would put a
// session in the delivery index for a connection that was never upgraded.
func TestVerify_AnswersTheIdentityAndBindsNothing(t *testing.T) {
	h := newHarness(t)

	player, err := h.gateway.Verify(t.Context(), testCredential)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if player.PlayerID() != testPlayerID {
		t.Fatalf("Verify answered %q, want %q", player.PlayerID(), testPlayerID)
	}
	if sessions := h.gateway.SessionsFor(testPlayerID); len(sessions) != 0 {
		t.Fatalf("Verify bound %v; a verified credential with no socket is not a delivery target",
			connIDs(sessions))
	}
}

func TestVerify_RefusesTheSameCredentialsAuthenticateDoes(t *testing.T) {
	h := newHarness(t)

	if _, err := h.gateway.Verify(t.Context(), "not-a-credential"); !errors.Is(err, gateway.ErrUnauthenticated) {
		t.Fatalf("an unknown credential produced %v, want ErrUnauthenticated", err)
	}
	delete(h.store.entities, testPlayerID)
	if _, err := h.gateway.Verify(t.Context(), testCredential); !errors.Is(err, gateway.ErrUnauthenticated) {
		t.Fatalf("a credential naming no entity produced %v, want ErrUnauthenticated", err)
	}
}

// Bind refuses a connection it cannot deliver to, for the reason Authenticate
// does: a session whose connection has no id cannot be disconnected, and one
// with no adapter cannot be delivered to.
func TestBind_RefusesAConnectionItCannotBind(t *testing.T) {
	h := newHarness(t)
	player, err := h.gateway.Verify(t.Context(), testCredential)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	for _, bad := range []gateway.Connection{
		{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "x"},
		{ID: "c", ReplyTo: "x"},
		{ID: "c", Adapter: vocabulary.ChannelAdapter("carrier-pigeon"), ReplyTo: "x"},
		{ID: "c", Adapter: vocabulary.AdapterWebSocket},
	} {
		if _, err := h.gateway.Bind(player, bad); err == nil {
			t.Fatalf("connection %+v was bound to a session", bad)
		}
	}
}

// The ONE input Bind still has to judge about its player, now that the type
// system judges the rest.
//
// Binding an unverified player is a compile error — VerifiedPlayer's field is
// unexported and only Verify mints one — so there is no test for that and there
// should not be: writing one would mean reintroducing the string path it
// replaced. What remains constructible is the ZERO value, which names nobody, and
// a session bound to it would be an anonymous delivery target on a working
// socket.
func TestBind_RefusesTheZeroVerifiedPlayer(t *testing.T) {
	h := newHarness(t)

	if _, err := h.gateway.Bind(gateway.VerifiedPlayer{}, conn(testConnID)); err == nil {
		t.Fatal("a session was bound to the zero VerifiedPlayer, which names nobody")
	}
	if _, bound := h.gateway.Session(testConnID); bound {
		t.Fatal("the refused bind left a session on the connection")
	}
	if sessions := h.gateway.SessionsFor(""); len(sessions) != 0 {
		t.Fatalf("the empty player id resolves to %v delivery targets", connIDs(sessions))
	}
}

// A cap of zero is a gateway nobody can connect to, and a negative one is a
// typo. Both are boot failures rather than a table that behaves surprisingly.
func TestNew_RefusesANonPositiveConnectionCap(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := newGateway(t, gateway.WithMaxConnectionsPerPlayer(limit)); err == nil {
			t.Fatalf("a cap of %d was accepted", limit)
		}
	}
}

// newGateway builds a gateway and RETURNS the construction error, which the
// harness swallows. Construction refusals are behavior here, not setup.
func newGateway(t *testing.T, opts ...gateway.Option) (*gateway.Gateway, error) {
	t.Helper()
	roster, err := gateway.NewRoster(map[string]string{testCredential: testPlayerID})
	if err != nil {
		t.Fatalf("NewRoster: %v", err)
	}
	return gateway.New(roster, newFakeStore(), &fakePublisher{},
		gateway.Config{CampaignID: testCampaignID, SceneID: testSceneID}, opts...)
}
