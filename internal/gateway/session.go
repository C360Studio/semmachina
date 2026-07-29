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
// what an unauthenticated socket is told — is the local-only security posture
// task 9.3 owns and states. What this task fixes is the MECHANISM either way:
// a credential resolves to a player entity id or to nothing, and an id that is
// not a real graph entity produces no session (see Gateway.Authenticate).
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
// behaviour of the standard primitive and it is left as it is — hiding it would
// mean hashing the credential to a fixed width, which is a credential-format
// decision, and credential format is task 9.3's to state.
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
// What a SECOND concurrent connection claiming one player means is still task
// 9.3's contract to state; this holds both and delivers to both, which is the
// answer that decides nothing.
//
// # It has no bound, no expiry, and no way to notice a connection that left
//
// An entry is created by Gateway.Authenticate and removed only by
// Gateway.Disconnect, so a TRANSPORT THAT DROPS A SOCKET WITHOUT CALLING
// Disconnect LEAKS THE ENTRY PERMANENTLY. That is an obligation on the adapter,
// stated here because the adapter does not exist yet and would otherwise inherit
// it by inference.
//
// It is not solved here on purpose. The three ways to solve it are a session
// TTL, a cap, and an eviction policy, and every one of them is a decision about
// what happens to a player who goes quiet — which this project's pacing rule
// makes a game decision, not a hygiene one: no turn-processing step may degrade
// because of elapsed wall-clock time, so an idle-timeout on a session is exactly
// the interactive-pacing assumption email-cadence play forbids. Connection
// lifecycle belongs with the rest of the local-only posture in task 9.3.
type sessions struct {
	mu       sync.RWMutex
	byConnID map[string]*Session
	// byPlayerID is a set of connection ids per player. A SET rather than a
	// slice because the same socket authenticating twice must not become two
	// delivery targets — every result to that player would then be written down
	// one wire twice — and because removal is by connection id.
	byPlayerID map[string]map[string]bool
}

func newSessions() *sessions {
	return &sessions{byConnID: map[string]*Session{}, byPlayerID: map[string]map[string]bool{}}
}

// put binds a connection to a player, replacing whatever that connection was
// bound to before.
//
// The unbind-first step is not bookkeeping. A connection that re-authenticates
// as a DIFFERENT player would otherwise stay in the first player's index, which
// makes that player's next result deliverable to a socket that is now somebody
// else's — a disclosure defect with no visible cause and no error anywhere.
func (s *sessions) put(session *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unbind(session.Connection.ID)
	s.byConnID[session.Connection.ID] = session
	connections, ok := s.byPlayerID[session.PlayerID]
	if !ok {
		connections = map[string]bool{}
		s.byPlayerID[session.PlayerID] = connections
	}
	connections[session.Connection.ID] = true
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
// not accumulate one map per player who has ever connected. That is the only
// unbounded growth this table could have that the connection index does not
// already have — and the connection index's own lack of a bound is stated above
// and owned by task 9.3.
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
