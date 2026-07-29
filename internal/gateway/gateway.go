package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
)

// Source names the gateway as the producer of the actions it publishes.
const Source = "player-gateway"

// MaxRequestBytes is the largest submission frame this engine accepts, and it is
// a limit THE TRANSPORT MUST APPLY TO ITS READER.
//
// Read that as an obligation on the adapter, because this package cannot
// discharge it. Submit receives a []byte, which means the frame is already
// wholly in memory: by the time the check below runs, whatever a client sent has
// been allocated once. An adapter must set this bound on the reader itself —
// websocket.Conn.SetReadLimit, an http.MaxBytesReader, whatever its transport
// calls it — or a client can spend the engine's memory before a single line of
// this package executes.
//
// The check in Submit is the SECOND line of defence, and it is worth keeping on
// its own terms: it prevents the second allocation json.Unmarshal would make,
// and it covers a caller that is not a socket at all. It is not a substitute for
// the first, and an adapter author who reads "the gateway bounds the frame" and
// skips their reader limit has moved the bound one allocation too late.
//
// It is DERIVED from the two client-owned budgets rather than picked, so
// widening the action-text budget cannot silently leave the frame bound behind.
// The multiplier covers JSON's worst honest expansion: every byte of a UTF-8
// string escaped as \uXXXX is six bytes on the wire, and the constant absorbs
// the field names, braces and the protocol string.
const MaxRequestBytes = 6*(payload.MaxActionTextBytes+payload.MaxIdempotencyKeyBytes) + 512

// Store is the graph read surface the gateway needs.
//
// READS ONLY, and that is a boundary rather than an accident: the gateway
// answers a question about the world and never changes it. The one durable fact
// ingress depends on — which turn a player currently holds — is written by the
// turn recorder, at the moment the turn becomes real. A gateway that could write
// would be able to point a player at a turn that does not exist.
type Store interface {
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Store = (*graphio.Store)(nil)

// Publisher appends one canonical action to the request stream.
//
// The msg-id variant, so a re-publish of the same derived action inside the
// stream's duplicate window collapses to one stored message. That is a NARROW
// second guard and explicitly not the duplicate guarantee: the durable one is
// intake's atomic turn create, which converges no matter how late a redelivery
// arrives.
type Publisher interface {
	PublishToStreamWithMsgID(ctx context.Context, subject string, data []byte, msgID string) error
}

// Config is the instance-scoped state every published action carries.
type Config struct {
	// CampaignID is the campaign entity this instance serves.
	CampaignID string
	// SceneID is the scene actions are taken in.
	//
	// Configuration rather than a lookup, because this slice has one scene by
	// design (the change's non-goals: no scene transitions, one scene played
	// until the process stops). When scenes move, this becomes a read of the
	// player's character's location and the field goes away; it is stated here so
	// that change has one place to happen rather than a default to discover.
	SceneID string
}

// Validate checks both positions are canonical entity ids.
func (c Config) Validate() error {
	if err := types.ValidateEntityID(c.CampaignID); err != nil {
		return fmt.Errorf("gateway campaign id %q is not a canonical six-part entity id: %w", c.CampaignID, err)
	}
	if err := types.ValidateEntityID(c.SceneID); err != nil {
		return fmt.Errorf("gateway scene id %q is not a canonical six-part entity id: %w", c.SceneID, err)
	}
	return nil
}

// Gateway turns authenticated submissions into canonical player actions.
type Gateway struct {
	auth      Authenticator
	store     Store
	publisher Publisher
	config    Config
	subject   string
	logger    *slog.Logger
	now       func() time.Time
	sessions  *sessions
}

// Option configures a Gateway.
type Option func(*Gateway)

// WithClock overrides the arrival clock. Tests use it so a stamped arrival time
// is an assertable value, and so an arbitrarily long gap between a player's
// turns can be exercised without waiting for one.
func WithClock(now func() time.Time) Option {
	return func(g *Gateway) { g.now = now }
}

// WithSubject overrides the publish subject. Deployment configuration: NATS
// refuses overlapping stream subjects, so a test that needs its own stream needs
// its own subject.
func WithSubject(subject string) Option {
	return func(g *Gateway) { g.subject = subject }
}

// WithLogger overrides the operator log. The gateway logs the CAUSE of every
// unavailable refusal here, because the client is told only that the engine
// could not take the action — an engine error string names buckets, subjects and
// entity ids, and a submission surface is where a curious client would go
// looking for them.
func WithLogger(logger *slog.Logger) Option {
	return func(g *Gateway) { g.logger = logger }
}

// New builds a gateway for one world instance.
func New(auth Authenticator, store Store, publisher Publisher, config Config, opts ...Option) (*Gateway, error) {
	if auth == nil {
		return nil, errors.New("the player gateway requires an authenticator; an unauthenticated session " +
			"has no player id to stamp, and every action it published would be anonymous")
	}
	if store == nil {
		return nil, errors.New("the player gateway requires a graph read surface; without one it cannot " +
			"prove a player is a real entity and cannot answer whether their turn is still running")
	}
	if publisher == nil {
		return nil, errors.New("the player gateway requires a publisher")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	gateway := &Gateway{
		auth:      auth,
		store:     store,
		publisher: publisher,
		config:    config,
		subject:   turn.ActionSubject,
		logger:    slog.Default(),
		now:       time.Now,
		sessions:  newSessions(),
	}
	for _, opt := range opts {
		opt(gateway)
	}
	if gateway.subject == "" {
		return nil, errors.New("the player gateway requires a publish subject")
	}
	if gateway.now == nil {
		return nil, errors.New("the player gateway requires a clock")
	}
	if gateway.logger == nil {
		return nil, errors.New("the player gateway requires a logger")
	}
	return gateway, nil
}

// Start makes the action stream exist with the limits this engine requires,
// before any action is published onto it.
//
// It must run BEFORE intake binds its consumer. natsclient auto-creates a
// missing stream for a consumer from its own defaults — file storage, limits
// retention, and a seven-day MaxAge nobody chose — and EnsureStream does not
// update an existing stream, so an intake that bound first would leave this call
// silently satisfied by the wrong stream. turn.EnsureActionStream reads the
// configuration back for exactly that reason and refuses a mismatch by name.
func (g *Gateway) Start(ctx context.Context, streams turn.StreamEnsurer) error {
	return turn.EnsureActionStream(ctx, streams)
}

// Authenticate binds a connection to the player entity its credential names.
//
// The player entity is READ, and that read is what makes "player_id is a graph
// entity" true rather than merely intended. A credential resolving to an id
// nothing imported, or to a key holding only a referential stub, produces no
// session at all — instead of a stream of actions attributed to a player who
// does not exist and a turn whose context assembler finds nobody.
//
// A RECONNECT is just this call again on a new connection. It returns a session
// carrying the same player id, so nothing the engine records changes when a
// socket drops. What a second CONCURRENT connection claiming one player should
// do is the disclosure question task 9.3 states and tests; this task neither
// refuses nor privileges one, because guessing that contract here is how it ends
// up decided by accident.
func (g *Gateway) Authenticate(ctx context.Context, credential string, conn Connection) (*Session, error) {
	if err := conn.Validate(); err != nil {
		return nil, err
	}
	playerID, err := g.auth.Authenticate(ctx, credential)
	if err != nil {
		return nil, err
	}
	if err := types.ValidateEntityID(playerID); err != nil {
		return nil, fmt.Errorf(
			"%w: the credential names %q, which is not a canonical six-part entity id",
			ErrUnauthenticated, playerID)
	}

	state, err := g.store.GetEntity(ctx, playerID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return nil, fmt.Errorf("%w: player %s is not an entity of this world", ErrUnauthenticated, playerID)
	case err != nil:
		return nil, fmt.Errorf("read player %s: %w", playerID, err)
	case state.IsStub():
		// A referential stub is queryable and factless: something MENTIONED this
		// player without ever importing them. An existence poll alone would take
		// it for a real player, and every read the turn depends on would come
		// back empty.
		return nil, fmt.Errorf(
			"%w: player %s is a referential stub — something references them but nothing imported them",
			ErrUnauthenticated, playerID)
	}

	session := &Session{PlayerID: playerID, Connection: conn}
	g.sessions.put(session)
	return session, nil
}

// Session returns the session bound to a connection.
func (g *Gateway) Session(connID string) (*Session, bool) { return g.sessions.get(connID) }

// SessionsFor resolves a player's live delivery targets from their ENTITY ID.
//
// This is the lookup targeted egress is built on, and the direction is the whole
// point: a turn result names a player, never a connection, so delivery asks this
// question at DELIVERY time rather than dialling an address captured when the
// action was submitted. A reconnect between the two is therefore invisible — the
// player's result reaches whatever socket they are on now.
//
// There is deliberately no companion that enumerates every session. Broadcast is
// a disclosure defect rather than a performance one (upstream's output/websocket
// fans every message out to every connected client), and the cheapest way to
// make it impossible is to give the delivery path no way to name anybody but the
// player it was handed.
//
// An empty answer means nobody is connected as that player, which is an ordinary
// state and not an error: the durable retrieval surface is what answers a player
// who was away, and email-cadence play makes "away" the common case.
func (g *Gateway) SessionsFor(playerID string) []Session { return g.sessions.forPlayer(playerID) }

// Disconnect drops a connection's session. The player entity is untouched: a
// player who is not connected is not a player who stopped existing.
func (g *Gateway) Disconnect(connID string) { g.sessions.remove(connID) }

// Submit validates one client submission and publishes the canonical action.
//
// # The return contract
//
// The response is ALWAYS non-nil and is always the answer to send the client. A
// refusal is a response, not an error, because it is a normal outcome the player
// must be told about; returning it as an error invites a caller to log it and
// send nothing.
//
// A non-nil error accompanies exactly the RefusalUnavailable responses, and
// carries the operator's copy of the cause. The client's copy never does: an
// engine error names buckets, subjects and entity ids, and this is the surface a
// curious client would probe for them.
//
// # The order of the checks, which is not arbitrary
//
//  1. Session, because an unauthenticated request has no player to judge
//     anything against and its body is not read at all.
//  2. Frame size, before parsing, because every other bound is enforced after
//     the decoder has already allocated.
//  3. Decode, which is where a server-owned field is REFUSED by name rather than
//     ignored.
//  4. The payload's own contract.
//  5. The derived action id, BEFORE admission — because a resubmission of the
//     turn the player already holds has to be recognised as that rather than
//     refused as a conflict with itself.
//  6. Admission.
//  7. Publish.
func (g *Gateway) Submit(ctx context.Context, connID string, raw []byte) (*payload.SubmitResponse, error) {
	session, ok := g.sessions.get(connID)
	if !ok {
		return refuse("", payload.SubmitRefusal{
			Code:    payload.RefusalUnauthenticated,
			Message: "this connection has no session; authenticate before submitting an action",
		}), nil
	}

	submission, refusal := g.decode(raw)
	if refusal != nil {
		return refuse(echoableKey(raw), *refusal), nil
	}

	actionID, err := ActionIDFor(session.PlayerID, submission.IdempotencyKey)
	if err != nil {
		return g.unavailable(submission.IdempotencyKey,
			fmt.Errorf("derive the action id for player %s: %w", session.PlayerID, err))
	}
	turnID := payload.TurnIDForAction(actionID)

	admission, err := g.admit(ctx, session.PlayerID, turnID)
	if err != nil {
		return g.unavailable(submission.IdempotencyKey, err)
	}
	if admission != nil {
		return refuse(submission.IdempotencyKey, *admission), nil
	}

	action := g.canonical(session, submission, actionID)
	if err := action.Validate(); err != nil {
		// A canonical action this gateway composed and cannot validate is a
		// GATEWAY bug, not a client one: every field it disagrees with is one the
		// server owns. Publishing it anyway would put a poison message on the
		// stream that intake redelivers until somebody notices.
		return g.unavailable(submission.IdempotencyKey,
			fmt.Errorf("the gateway composed an invalid canonical action: %w", err))
	}
	if err := g.publish(ctx, action); err != nil {
		return g.unavailable(submission.IdempotencyKey, err)
	}

	response := &payload.SubmitResponse{
		Protocol:       payload.PlayerProtocolV1,
		Status:         payload.StatusAccepted,
		IdempotencyKey: submission.IdempotencyKey,
		ActionID:       actionID,
		TurnID:         turnID,
		ArrivedAt:      action.ArrivedAt,
	}
	if err := response.Validate(); err != nil {
		// The action is already durably on the stream, so the turn WILL happen.
		// Failing here would tell the player their move was refused while the
		// engine plays it, which is the one answer that is worse than either.
		g.logger.Error("the gateway composed an acceptance it cannot validate; the action is already published",
			"action", actionID, "error", err)
		return nil, fmt.Errorf("compose the acceptance for action %s: %w", actionID, err)
	}
	return response, nil
}

// decode turns client bytes into a submission, classifying every refusal.
func (g *Gateway) decode(raw []byte) (*payload.SubmitAction, *payload.SubmitRefusal) {
	if len(raw) > MaxRequestBytes {
		return nil, &payload.SubmitRefusal{
			Code: payload.RefusalMalformedRequest,
			Message: fmt.Sprintf(
				"the submission is %d bytes, which exceeds the %d-byte request budget",
				len(raw), MaxRequestBytes),
		}
	}

	var submission payload.SubmitAction
	if err := json.Unmarshal(raw, &submission); err != nil {
		return nil, classify(err, payload.RefusalMalformedRequest)
	}
	if err := submission.Validate(); err != nil {
		return nil, classify(err, payload.RefusalInvalidField)
	}
	return &submission, nil
}

// classify maps a decode or validation failure onto the closed refusal set.
//
// The protocol sentinels are checked in the order their remedies diverge, and
// the version is FIRST for the reason SubmitAction.Validate checks it first: a
// client speaking a version this engine does not know may have every other field
// wrong as a consequence, and naming one of those would send them chasing a
// symptom.
//
// The message is the payload package's own diagnosis, which is composed entirely
// from the client's request and this engine's published contract. Nothing
// internal reaches a client through this path.
func classify(err error, fallback payload.SubmitRefusalCode) *payload.SubmitRefusal {
	refusal := &payload.SubmitRefusal{Code: fallback, Message: err.Error()}
	switch {
	case errors.Is(err, payload.ErrUnsupportedProtocol):
		refusal.Code = payload.RefusalUnsupportedProtocol
	case errors.Is(err, payload.ErrServerOwnedField):
		refusal.Code = payload.RefusalServerOwnedField
	case errors.Is(err, payload.ErrUnknownField):
		refusal.Code = payload.RefusalUnknownField
	}
	var field *payload.FieldError
	if errors.As(err, &field) {
		refusal.Field = field.Name
	}
	return refusal
}

// admit answers whether this player may take a turn right now.
//
// It returns nil to admit, a refusal to decline, and an error only when it could
// not find out. Two reads: the player names their current turn, and the turn
// states its own phase. Neither is a scan, which is the whole reason the pointer
// exists — the graph edge runs turn → player, so "which turns does this player
// have" is otherwise the player's entire history filtered by phase, on every
// submission.
//
// # Every degenerate reading fails OPEN, deliberately
//
// A player with no pointer has never taken a turn: admit. A pointer at a turn
// that no longer exists means somebody deleted a turn entity — a graph anomaly,
// logged loudly, and ADMITTED, because refusing would lock that player out of
// their own campaign permanently for a fault they cannot see and cannot clear,
// while admitting self-heals on the next accept. A pointer holding several
// values means an append-lane write got at it; that one needs no policy choice
// at all, because the right answer is to check them ALL and refuse if any names
// a live turn.
//
// The one case that is not fail-open is a turn record that is CORRUPT — two
// phases, none, a stub. That is turn.PhaseOf's judgement, taken here rather than
// re-derived, and it is an error rather than an admission because the phase is
// the fact the whole turn design rests on and guessing at it would start a
// second turn on top of one that may still be running.
func (g *Gateway) admit(ctx context.Context, playerID, turnID string) (*payload.SubmitRefusal, error) {
	state, err := g.store.GetEntity(ctx, playerID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return nil, fmt.Errorf(
			"player %s authenticated but their entity is gone; the admission gate cannot answer", playerID)
	case err != nil:
		return nil, fmt.Errorf("read player %s: %w", playerID, err)
	case state.IsStub():
		return nil, fmt.Errorf(
			"player %s is a referential stub, so it holds no facts and the admission gate cannot answer",
			playerID)
	}

	held, unreadable := turn.HeldTurns(state)
	if len(held) > 1 {
		g.logger.Warn("a player holds several current-turn pointers; a write took an appending lane",
			"player", playerID, "pointers", held)
	}
	if unreadable > 0 {
		// Unreachable through the projection, which admits only a string object,
		// and reported anyway: a pointer this gate cannot read is a pointer this
		// gate ignores, and "ignored" plus "silent" is how a player ends up
		// holding two live turns with nothing anywhere to say why.
		g.logger.Warn("a player holds current-turn pointers this gate cannot read; they are not being checked",
			"player", playerID, "unreadable", unreadable)
	}

	for _, turnEntityID := range held {
		// The turn this submission derives is not a conflict with itself. A
		// client that resubmits the same idempotency key is retrying, and the
		// retry resolves to the same turn — so it is republished (idempotent at
		// intake) rather than refused, which also heals a first publish that was
		// lost.
		if payload.RequireTurnEntityID(turnID, turnEntityID) == nil {
			continue
		}
		refusal, err := g.refuseIfLive(ctx, playerID, turnEntityID)
		if err != nil || refusal != nil {
			return refusal, err
		}
	}
	return nil, nil
}

// refuseIfLive reads one held turn and refuses when it has not ended.
func (g *Gateway) refuseIfLive(ctx context.Context, playerID, turnEntityID string) (*payload.SubmitRefusal, error) {
	state, err := g.store.GetEntity(ctx, turnEntityID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		g.logger.Warn("a player points at a turn that no longer exists; admitting rather than locking them out",
			"player", playerID, "turn", turnEntityID)
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read turn %s held by player %s: %w", turnEntityID, playerID, err)
	}

	phase, err := turn.PhaseOf(state, turnEntityID)
	if err != nil {
		return nil, fmt.Errorf("read the phase of turn %s held by player %s: %w", turnEntityID, playerID, err)
	}
	if phase.IsTerminal() {
		return nil, nil
	}

	activeTurnID := turnIDOf(turnEntityID)
	return &payload.SubmitRefusal{
		Code: payload.RefusalTurnInProgress,
		Message: fmt.Sprintf(
			"your turn %s is still %s; one action at a time, and this one was not taken", activeTurnID, phase),
		ActiveTurnID: activeTurnID,
	}, nil
}

// turnIDOf returns the instance segment of a turn entity id — the turn id the
// player is told about.
//
// The turn id rather than the entity id, deliberately: the entity id carries
// this deployment's org, world namespace and template positions, and a refusal
// is not the place to hand an untrusted client the shape of the instance it is
// talking to.
func turnIDOf(turnEntityID string) string {
	parts := strings.Split(turnEntityID, ".")
	return parts[len(parts)-1]
}

// canonical composes the engine's input contract from the client's submission
// and the session, which is where every server-owned field is stamped.
func (g *Gateway) canonical(
	session *Session,
	submission *payload.SubmitAction,
	actionID string,
) *payload.PlayerAction {
	return &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   session.PlayerID,
		CampaignID: g.config.CampaignID,
		SceneID:    g.config.SceneID,
		Text:       submission.Text,
		// The ENGINE's clock, never the client's. Deadline evaluation uses
		// arrival time, so a client that could stamp it could arrive before a
		// deadline it had already missed.
		ArrivedAt: g.now().UTC(),
		Channel: payload.ChannelBinding{
			Adapter: session.Connection.Adapter,
			ReplyTo: session.Connection.ReplyTo,
		},
	}
}

// publish puts the canonical action on the request stream, wrapped in the
// envelope the intake decoder requires.
//
// The action id is the Nats-Msg-Id, so a re-publish inside the stream's
// duplicate window collapses to one stored message. Narrow, and not the
// guarantee: the durable one is intake's atomic turn create, which converges for
// a redelivery of any age.
func (g *Gateway) publish(ctx context.Context, action *payload.PlayerAction) error {
	envelope := message.NewBaseMessage(
		action.Schema(), action, Source, message.WithTime(action.ArrivedAt))
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal action %s: %w", action.ActionID, err)
	}
	if err := g.publisher.PublishToStreamWithMsgID(ctx, g.subject, data, action.ActionID); err != nil {
		return fmt.Errorf("publish action %s to %s: %w", action.ActionID, g.subject, err)
	}
	return nil
}

// refuse builds a refusal response.
func refuse(idempotencyKey string, refusal payload.SubmitRefusal) *payload.SubmitResponse {
	return &payload.SubmitResponse{
		Protocol:       payload.PlayerProtocolV1,
		Status:         payload.StatusRefused,
		IdempotencyKey: idempotencyKey,
		Refusal:        &refusal,
	}
}

// unavailable is the one refusal whose cause the client is NOT told.
//
// The operator gets the real error twice — logged here, and returned to the
// caller — and the client gets a fixed sentence. Everything an engine failure
// names is infrastructure: bucket names, subjects, entity ids, the shape of the
// instance. A submission surface is exactly where a curious client would go
// looking for those, and there is no version of "helpful diagnosis" that is
// worth handing them over.
func (g *Gateway) unavailable(idempotencyKey string, cause error) (*payload.SubmitResponse, error) {
	g.logger.Error("a submission could not be taken", "error", cause)
	return refuse(idempotencyKey, payload.SubmitRefusal{
		Code:    payload.RefusalUnavailable,
		Message: "the engine could not take this action; nothing was published, so it is safe to retry",
	}), cause
}

// echoableKey recovers the idempotency key from a request that was REFUSED, so
// the answer can still be matched to the submission that caused it.
//
// It is a best effort on purpose. A refused request never became a SubmitAction
// — the refusal happens on the object's key set, before any value is mapped —
// so there is no struct to read the key off, and a client on an asynchronous
// transport would otherwise have to guess which of its outstanding submissions a
// refusal belongs to. Matching by action id is not available: a refused
// submission has none.
//
// It is validated before it is echoed, and that is not a formality: this is the
// one value the engine takes from an untrusted request and writes back out. An
// unvalidated echo would put whatever a client sent — control bytes, a payload
// pretending to be a name — into the answer and into every operator diagnostic
// that quotes it. payload.MaxIdempotencyKeyBytes and the character rule exist
// for exactly this call site.
func echoableKey(raw []byte) string {
	var fields struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	if err := payload.RequireIdempotencyKey(fields.IdempotencyKey); err != nil {
		return ""
	}
	return fields.IdempotencyKey
}
