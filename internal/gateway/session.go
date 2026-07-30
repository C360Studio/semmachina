package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ErrUnauthenticated reports a connection that has no session.
//
// It is one sentinel for "who are you" regardless of which half failed —
// unknown credential, or a credential naming a player this world does not have.
// Distinguishing them for the client would be a membership oracle, and the two
// have the same remedy anyway.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrTooManyConnections reports a player who already holds as many live
// connections as this instance allows.
//
// It is NOT an authentication failure and is deliberately its own sentinel: the
// credential was good, the player is real, and what ran out is a seat. A
// transport that reported it as ErrUnauthenticated would send a player who
// simply left a tab open somewhere off to check their credential.
var ErrTooManyConnections = errors.New("too many connections for this player")

// Writer is the live half of a connection: the thing a delivery is actually
// written to.
//
// It exists so that resolving a player's delivery targets and reaching them are
// ONE lookup rather than two. The alternative is a transport-side map from
// connection id to socket, and that map is a second registry with the failure
// mode this package's session table exists to avoid: it can hold a socket the
// session table has dropped, or drop one the session table still lists, and
// neither shows up as an error. Hanging the writer on the connection makes the
// session table the only thing that has to be right — a socket is reachable
// exactly while a session names it, and Disconnect ends both in one call.
//
// It is nil for an adapter that has no live socket. An email or SMS session is
// delivered to ChannelBinding.ReplyTo by that adapter's own sink, and there is
// nothing here to write to; a sink handed a session with no writer refuses
// rather than reporting a delivery it did not make.
type Writer interface {
	// Write sends one document to the client, or reports why it could not.
	//
	// The context is the delivery's, and an implementation is expected to bound
	// the write on its own terms as well: the caller is a durable consumer with
	// an ack deadline, and a socket that never drains would otherwise hold that
	// deadline open for every player queued behind it.
	Write(ctx context.Context, document []byte) error
}

// Connection is the transport's handle on one client, and everything in it is
// EPHEMERAL.
//
// It exists so the gateway can be given a connection without being given a
// transport: a WebSocket handler, a test, and a future Slack adapter all produce
// one of these. Nothing here is identity — that is the Session's player id — and
// nothing downstream of the action stream may branch on any of it.
type Connection struct {
	// ID distinguishes one live connection from another. It is a name for a
	// socket and dies with it, so it is never recorded as a fact about a player.
	ID string
	// Adapter names the transport, from the closed channel vocabulary.
	Adapter vocabulary.ChannelAdapter
	// ReplyTo is the adapter-specific delivery address stamped onto the action's
	// channel binding, and WHAT IT IS WORTH IS PER ADAPTER.
	//
	// For a WebSocket it is the connection id, and that is a DELIVERY HINT and
	// not a durable address: it is invalid the moment the socket drops, so
	// nothing may dial it. Targeted egress resolves the live connection from the
	// PLAYER ID at delivery time instead (Gateway.SessionsFor), which is why a
	// reconnect between submission and resolution is invisible.
	//
	// For an adapter whose transport has a durable address — an email box, a chat
	// channel — it IS an address, and that adapter's sink may deliver to it
	// directly, because there is no live session to resolve. The engine records
	// it either way and decides nothing: which of the two an adapter is, is the
	// adapter's own contract to state, and this comment is the fixed point both
	// halves are stated against.
	ReplyTo string

	// Writer is the live socket this connection delivers over, or nil for an
	// adapter that has none. See Writer.
	//
	// It is deliberately NOT checked by Validate. A transport that has a socket
	// supplies one and its sink refuses a session without one; an adapter with a
	// durable address has nothing to put here and must still be bindable.
	Writer Writer
}

// Validate checks a connection carries the three things an action's channel
// binding needs.
func (c Connection) Validate() error {
	if c.ID == "" {
		return errors.New("a connection needs an id to bind a session to")
	}
	if _, err := vocabulary.ParseChannelAdapter(string(c.Adapter)); err != nil {
		return err
	}
	if c.ReplyTo == "" {
		return errors.New("a connection needs a reply-to address for the action's channel binding")
	}
	return nil
}

// Session is one authenticated client: a durable player identity bound to an
// ephemeral connection.
//
// The asymmetry is the whole design. PlayerID is a graph entity that outlives
// every socket, and Connection is a socket that outlives nothing. A reconnect
// produces a new Session with the same PlayerID, so nothing the engine records
// changes when a client drops.
type Session struct {
	// PlayerID is the six-part entity id of the player, read out of the graph
	// and proven real before this session existed.
	PlayerID string
	// Connection is the transport handle this session is currently on.
	Connection Connection
}

// Authenticator resolves an opaque client credential to a player entity id.
//
// It is a seam rather than a fixed rule because the credential POLICY on an
// instance-per-world box — what a credential is made of, how it is provisioned,
// what an unauthenticated client is told — belongs to the TRANSPORT that carries
// it, and is stated in internal/playersocket's package doc for the WebSocket
// one. What this package fixes is the MECHANISM either way: a credential
// resolves to a player entity id or to nothing, and an id that is not a real
// graph entity produces no session (see Gateway.Verify).
type Authenticator interface {
	// Authenticate returns the player entity id a credential names, or an error
	// wrapping ErrUnauthenticated.
	Authenticate(ctx context.Context, credential string) (string, error)
}

// Roster is the instance-per-world Authenticator: a fixed set of credentials
// from instance configuration, each naming one player entity.
//
// It matches the MVP's shape — one box, one world, a known cast — and nothing
// more. Every player id is validated as a canonical six-part entity id at
// CONSTRUCTION, so a typo in instance configuration is a boot failure rather
// than a session that authenticates to an entity nobody imported.
type Roster struct {
	// Deliberately a slice rather than a map. A map lookup on the credential
	// short-circuits on the first differing byte, and the whole comparison is
	// done with subtle.ConstantTimeCompare instead; the cast on an
	// instance-per-world box is small enough that walking it costs nothing worth
	// measuring.
	entries []rosterEntry
}

type rosterEntry struct {
	credential string
	playerID   string
}

// NewRoster builds an authenticator from credential → player entity id.
func NewRoster(entries map[string]string) (*Roster, error) {
	if len(entries) == 0 {
		return nil, errors.New(
			"a roster with no entries authenticates nobody; an instance with no players cannot be played")
	}
	roster := &Roster{entries: make([]rosterEntry, 0, len(entries))}
	// Sorted so construction is deterministic and a duplicate player id is
	// reported by the same name on every boot.
	for _, credential := range slices.Sorted(maps.Keys(entries)) {
		playerID := entries[credential]
		if credential == "" {
			return nil, errors.New("a roster entry with an empty credential would authenticate a silent client")
		}
		if err := types.ValidateEntityID(playerID); err != nil {
			return nil, fmt.Errorf(
				"roster entry for a credential names player %q, which is not a canonical six-part entity id: "+
					"%w; player identity is a graph entity, never a name", playerID, err)
		}
		roster.entries = append(roster.entries, rosterEntry{credential: credential, playerID: playerID})
	}
	return roster, nil
}

// Authenticate implements Authenticator.
//
// Every entry is compared, and each comparison is constant-time, so neither the
// number of comparisons nor their duration depends on how much of a credential
// an attacker guessed right.
//
// Across CONTENT, not across LENGTH: subtle.ConstantTimeCompare returns 0
// immediately when the two lengths differ, so a wrong-length guess is
// distinguishable in principle from a right-length one. That is the standard
// behaviour of the standard primitive and it is left as it is.
//
// The credential FORMAT is deliberately unconstrained — an opaque operator-chosen
// string, provisioned in instance configuration, with no expiry, rotation or
// revocation short of a restart. Every one of those absences is a consequence of
// the local-only scope, and they are enumerated where an operator will meet them:
// internal/playersocket logs the list by name the moment an instance is bound
// beyond loopback. Hiding the length would mean hashing to a fixed width, which
// only becomes worth its complexity in the same breath as rate limiting — that
// is, the day this port is exposed.
func (r *Roster) Authenticate(_ context.Context, credential string) (string, error) {
	playerID := ""
	for _, entry := range r.entries {
		if subtle.ConstantTimeCompare([]byte(entry.credential), []byte(credential)) == 1 {
			playerID = entry.playerID
		}
	}
	if playerID == "" {
		return "", fmt.Errorf("%w: the credential names no player of this world", ErrUnauthenticated)
	}
	return playerID, nil
}

// sessions is the live session table, indexed BOTH ways.
//
// By CONNECTION because that is what the transport has when a frame arrives, and
// by PLAYER because that is what a turn result is addressed to. Two questions,
// one table, one lifecycle — and that is the whole design decision here.
//
// A separate egress-side registry was the alternative and it is worse in a way
// that only appears at the wrong moment: the transport would have to register a
// connection twice and drop it twice, so the two tables would drift, and a
// connection present in the delivery index and absent from the session index is
// a socket that can receive a player's fiction without being able to submit as
// them. One table cannot drift from itself.
//
// The index deliberately offers NO way to enumerate every session. That is the
// structural half of targeted delivery: the egress path is handed a lookup by
// player id and nothing else, so broadcast is not a thing it chooses against —
// it is a thing it cannot express. Upstream's output/websocket iterates its whole
// client map, which is exactly the shape this omission forecloses.
//
// A player MAY hold several connections at once and every one of them is a
// delivery target: multi-device is the posture, not an accident of the index.
// What bounds it is maxPerPlayer.
//
// # What bounds this table, and what deliberately does not
//
// An entry is created by Gateway.Bind and removed only by Gateway.Disconnect, so
// a TRANSPORT THAT DROPS A SOCKET WITHOUT CALLING Disconnect LEAKS THE ENTRY
// PERMANENTLY. That is an obligation on the adapter and it is discharged in
// internal/playersocket by a defer on the connection goroutine, which every
// close path — graceful, abnormal, panicking, shutting down — unwinds through.
//
// The bound itself is maxPerPlayer, and the whole table is therefore bounded by
// (roster size × maxPerPlayer). That is a hard number known at boot, because
// only a player named in the roster can be bound at all — which is what lets
// this table have a bound WITHOUT a session TTL.
//
// There is no idle expiry and there will not be one. An idle timeout is a
// decision about a player who goes quiet, and this project's pacing rule makes
// that a game decision rather than a hygiene one: no turn-processing step may
// degrade because of elapsed wall-clock time, so expiring a session for silence
// is exactly the interactive-pacing assumption email-cadence play forbids. A
// socket whose PEER has gone away is a different fact entirely and is reaped on
// transport liveness (ping/pong) rather than on player silence — see
// internal/playersocket.
type sessions struct {
	mu       sync.RWMutex
	byConnID map[string]*Session
	// byPlayerID is a set of connection ids per player. A SET rather than a
	// slice because the same socket authenticating twice must not become two
	// delivery targets — every result to that player would then be written down
	// one wire twice — and because removal is by connection id.
	byPlayerID map[string]map[string]bool
	// maxPerPlayer is how many live connections one player may hold.
	maxPerPlayer int
}

func newSessions(maxPerPlayer int) *sessions {
	return &sessions{
		byConnID:     map[string]*Session{},
		byPlayerID:   map[string]map[string]bool{},
		maxPerPlayer: maxPerPlayer,
	}
}

// put binds a connection to a player, replacing whatever that connection was
// bound to before, and refuses once that player is at their cap.
//
// The count and the bind happen under ONE lock, which is the only reason the cap
// is a bound rather than a suggestion: a transport that asked "how many does
// this player have" and then bound would leave a gap two of its own goroutines
// can drive through, and every connection admitted through that gap is a
// delivery target.
//
// The NEWEST connection is refused rather than the oldest evicted, and that
// direction is the decision. Eviction would turn a leaked credential into a way
// to take a player's campaign away from them — connect until the cap and every
// socket they hold drops — while refusal costs the newcomer a connection and
// costs the player nothing.
//
// The unbind-first step is not bookkeeping. A connection that re-authenticates
// as a DIFFERENT player would otherwise stay in the first player's index, which
// makes that player's next result deliverable to a socket that is now somebody
// else's — a disclosure defect with no visible cause and no error anywhere. It
// also releases that player's seat, so a rebound socket does not hold a slot on
// a player it no longer belongs to.
func (s *sessions) put(session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unbind(session.Connection.ID)
	if len(s.byPlayerID[session.PlayerID]) >= s.maxPerPlayer {
		// Counted deliberately AFTER the unbind: a connection that was already
		// this player's released its seat above and reclaims it here, so
		// re-authenticating an existing socket is never a new slot.
		//
		// The delete is what keeps the two indexes from drifting on the refusal
		// path. The unbind above already took this connection out of whatever
		// player it belonged to, so leaving it in the connection index would be
		// precisely the shape this table exists to make impossible: a session
		// present as an identity and absent as a delivery target.
		delete(s.byConnID, session.Connection.ID)
		return fmt.Errorf(
			"%w: player %s already holds %d connections",
			ErrTooManyConnections, session.PlayerID, s.maxPerPlayer)
	}
	s.byConnID[session.Connection.ID] = session
	connections, ok := s.byPlayerID[session.PlayerID]
	if !ok {
		connections = map[string]bool{}
		s.byPlayerID[session.PlayerID] = connections
	}
	connections[session.Connection.ID] = true
	return nil
}

func (s *sessions) get(connID string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.byConnID[connID]
	return session, ok
}

func (s *sessions) remove(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unbind(connID)
	delete(s.byConnID, connID)
}

// unbind drops a connection from its player's index. The caller holds the lock.
//
// The empty player entry is deleted rather than left behind, so the index does
// not accumulate one map per player who has ever connected. The per-player cap
// bounds how many connections a player may hold AT ONCE and says nothing about
// how many the table remembers after they leave, so this is the hygiene that
// keeps the table's bound the one stated above.
func (s *sessions) unbind(connID string) {
	previous, ok := s.byConnID[connID]
	if !ok {
		return
	}
	connections := s.byPlayerID[previous.PlayerID]
	delete(connections, connID)
	if len(connections) == 0 {
		delete(s.byPlayerID, previous.PlayerID)
	}
}

// forPlayer returns COPIES of the sessions currently bound to a player, sorted
// by connection id.
//
// Copies because the caller fans out over the answer while other connections
// come and go, and a slice of pointers into the live table is a race the caller
// cannot see. Sorted so delivery order is an assertable fact rather than a map
// iteration.
//
// An empty answer is a normal outcome, not an error: a player who is not
// connected is a player the durable retrieval surface answers, and at
// email cadence "nobody is on the socket" is the usual case rather than a fault.
func (s *sessions) forPlayer(playerID string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	connections := s.byPlayerID[playerID]
	if len(connections) == 0 {
		return nil
	}
	out := make([]Session, 0, len(connections))
	for connID := range connections {
		if session, ok := s.byConnID[connID]; ok {
			out = append(out, *session)
		}
	}
	slices.SortFunc(out, func(a, b Session) int {
		return strings.Compare(a.Connection.ID, b.Connection.ID)
	})
	return out
}
