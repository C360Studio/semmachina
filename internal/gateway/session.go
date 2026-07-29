package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"slices"
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
	// channel binding.
	//
	// For a WebSocket it is the connection id, and that is a DELIVERY HINT
	// rather than a durable address: it is invalid the moment the socket drops.
	// Task 9.2's egress must resolve the live connection from the player id at
	// delivery time and must not treat this as an address it can dial. For an
	// adapter that has a durable address — an email box, a chat channel — it is
	// one, which is exactly why the per-adapter contract has to say which it is.
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

// sessions is the live connection → session table.
//
// Keyed by CONNECTION, because that is what the transport has when a frame
// arrives. It deliberately carries no player → connection index: resolving a
// delivery target from a player id is task 9.2's, and what a SECOND connection
// claiming the same player means is task 9.3's — building either here would be
// guessing at a contract those tasks exist to state. What this table promises is
// only what ingress needs: a connection either has a session or does not, and a
// closed connection has none.
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
}

func newSessions() *sessions {
	return &sessions{byConnID: map[string]*Session{}}
}

func (s *sessions) put(session *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byConnID[session.Connection.ID] = session
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
	delete(s.byConnID, connID)
}
