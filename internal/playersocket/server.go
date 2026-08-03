package playersocket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Transport defaults. Every duration here bounds the ENGINE's patience with a
// socket; none of them measures a player's pace. See the package doc.
const (
	// DefaultPath is where the player socket lives. A fixed path rather than "any
	// path", so a stray browser request to / is a 404 instead of a handshake.
	DefaultPath = "/play"

	// DefaultPingInterval is how often a live socket is probed.
	DefaultPingInterval = 30 * time.Second
	// DefaultPongTimeout is how long a probed socket has to answer.
	//
	// A client that is RUNNING answers automatically — every conformant WebSocket
	// implementation pongs without its application doing anything — so this
	// reaps a peer that has gone away and never a player who has gone quiet.
	DefaultPongTimeout = 15 * time.Second
	// DefaultWriteTimeout bounds one write to one socket.
	DefaultWriteTimeout = 10 * time.Second
	// DefaultHandshakeTimeout bounds the HTTP half of a connection.
	DefaultHandshakeTimeout = 10 * time.Second
)

// credentialScheme is the Authorization scheme the credential arrives under.
const credentialScheme = "Bearer "

// Gate is the gateway surface this transport drives, and the shape of it is
// deliberate.
//
// Four methods, and no way to enumerate sessions or to look up another player's
// connections. The transport can bind the socket in front of it, submit for the
// socket in front of it, and drop the socket in front of it — a handler holding
// one client's connection cannot reach another client's, which is the same
// structural argument that keeps broadcast out of the egress path.
type Gate interface {
	// Verify answers which player a credential names, binding nothing. It is the
	// only thing that mints a gateway.VerifiedPlayer.
	Verify(ctx context.Context, credential string) (gateway.VerifiedPlayer, error)
	// Bind makes a verified player and a live connection into a session.
	//
	// It takes the PROOF rather than an id, so this transport cannot bind a
	// player it did not verify even by accident: the two calls below are coupled
	// by the type system rather than by the order they happen to appear in.
	Bind(player gateway.VerifiedPlayer, conn gateway.Connection) (*gateway.Session, error)
	// Disconnect drops a connection's session.
	Disconnect(connID string)
	// Submit validates one client submission and publishes the canonical action.
	Submit(ctx context.Context, connID string, raw []byte) (*payload.SubmitResponse, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Gate = (*gateway.Gateway)(nil)

// ResultRetriever is the durable, read-only player result surface. Authorization
// remains here at the authenticated transport boundary: ByTurn and ByAction are
// globally addressable inside one world, so merely possessing an id never grants
// access to its result.
type ResultRetriever interface {
	ByTurnForPlayer(ctx context.Context, playerID, turnID string) (*payload.TurnDelivery, error)
	ByActionForPlayer(ctx context.Context, playerID, actionID string) (*payload.TurnDelivery, error)
	Latest(ctx context.Context, playerID string) (*payload.TurnDelivery, error)
}

var _ ResultRetriever = (*egress.Results)(nil)

// Config is one instance's transport configuration.
type Config struct {
	// Addr is the TCP address to bind, host and port.
	//
	// It MUST be a loopback address unless AllowNonLoopback says otherwise. A
	// wildcard (":8080", "0.0.0.0:8080") is not loopback: it is every interface
	// the box has, which is the configuration that turns a local-only posture
	// into an exposed one with no other visible change.
	Addr string
	// Path is where the socket is served. Defaults to DefaultPath.
	//
	// It is compared against the WHOLE request path, so a composition mounting
	// this handler under a prefix must set the full path here rather than the
	// suffix its mux strips.
	Path string
	// AllowNonLoopback acknowledges that this instance is reachable from off the
	// box, and it is an ACKNOWLEDGEMENT rather than a feature flag.
	//
	// Setting it logs, at WARN, every control this transport does not have (see
	// exposureWarnings). Read that list before setting it: the local-only posture
	// is the reason several of those controls do not exist yet, and this field is
	// the moment that reason stops applying.
	AllowNonLoopback bool
}

// Server is the WebSocket transport for one world instance.
type Server struct {
	gate     Gate
	results  ResultRetriever
	config   Config
	logger   *slog.Logger
	upgrader websocket.Upgrader

	pingInterval time.Duration
	pongTimeout  time.Duration
	writeTimeout time.Duration

	// nonce and counter mint connection ids. See mintConnectionID — this pair is
	// the whole of obligation (3) in the package doc.
	nonce   string
	counter atomic.Uint64
}

// Option configures a Server.
type Option func(*Server)

// WithLogger overrides the operator log.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithPingInterval overrides how often a socket is probed for liveness.
func WithPingInterval(d time.Duration) Option {
	return func(s *Server) { s.pingInterval = d }
}

// WithPongTimeout overrides how long a probed socket has to answer.
func WithPongTimeout(d time.Duration) Option {
	return func(s *Server) { s.pongTimeout = d }
}

// WithWriteTimeout overrides how long one write to one socket may take.
func WithWriteTimeout(d time.Duration) Option {
	return func(s *Server) { s.writeTimeout = d }
}

// WithResultRetriever enables authenticated reconnect retrieval. It is optional
// for adapter-only compositions, which continue to speak the pre-retrieval v1
// submission path; a retrieval request to an unwired server is explicitly
// refused as unavailable.
func WithResultRetriever(results ResultRetriever) Option {
	return func(s *Server) { s.results = results }
}

// NewServer builds the transport, refusing a configuration that would quietly
// stop being local-only.
func NewServer(gate Gate, config Config, opts ...Option) (*Server, error) {
	if gate == nil {
		return nil, errors.New("the player socket requires a gateway; without one it would accept connections " +
			"it cannot authenticate and submissions it cannot attribute")
	}
	if config.Path == "" {
		config.Path = DefaultPath
	}
	if !strings.HasPrefix(config.Path, "/") {
		return nil, fmt.Errorf("the player socket path %q must begin with /", config.Path)
	}

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf(
			"the player socket could not seed its connection-id nonce: %w; without it two servers in one "+
				"process could mint the same connection id, and a connection id is what a session is bound to", err)
	}

	server := &Server{
		gate:         gate,
		config:       config,
		logger:       slog.Default(),
		pingInterval: DefaultPingInterval,
		pongTimeout:  DefaultPongTimeout,
		writeTimeout: DefaultWriteTimeout,
		nonce:        hex.EncodeToString(nonce),
	}
	for _, opt := range opts {
		opt(server)
	}
	switch {
	case server.pingInterval <= 0:
		return nil, errors.New("the player socket requires a positive ping interval; without one a socket " +
			"whose peer has gone away is never noticed and its session is never released")
	case server.pongTimeout <= 0:
		return nil, errors.New("the player socket requires a positive pong timeout")
	case server.writeTimeout <= 0:
		return nil, errors.New("the player socket requires a positive write timeout; an unbounded write is a " +
			"slow reader holding the egress consumer's ack deadline open for every player behind them")
	}
	server.upgrader = websocket.Upgrader{
		HandshakeTimeout: DefaultHandshakeTimeout,
		// CheckOrigin is deliberately LEFT UNSET, which selects gorilla's
		// same-origin default. A browser page on another origin is the realistic
		// attacker against a local port, and `func(*http.Request) bool { return
		// true }` is the one-line change that admits it. Nothing here rests on it
		// alone — the credential is a bearer header rather than a cookie, so a
		// cross-origin socket carries no ambient authority — but the day a real
		// browser client lands, this becomes an allow-LIST, never a `return true`.
		EnableCompression: false,
	}

	if err := server.checkConfiguredAddr(); err != nil {
		return nil, err
	}
	return server, nil
}

// checkConfiguredAddr refuses, or loudly acknowledges, an address beyond
// loopback.
//
// This is the EARLY half of the exposure gate and it reads a string. The
// authoritative half is in Serve, which reads the address actually bound — a
// hostname can resolve somewhere this cannot predict, and what matters is what
// the kernel accepted rather than what the config said.
func (s *Server) checkConfiguredAddr() error {
	if s.config.Addr == "" {
		// Legitimate: a caller mounting ServeHTTP on their own listener never
		// gives one. Serve still checks what it is handed.
		return nil
	}
	host, _, err := net.SplitHostPort(s.config.Addr)
	if err != nil {
		return fmt.Errorf("the player socket address %q is not host:port: %w", s.config.Addr, err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if !s.config.AllowNonLoopback {
		return fmt.Errorf(
			"the player socket refuses to bind %q: %q is not a loopback address, and this transport's whole "+
				"security posture assumes nothing off this box can reach the port (see the package doc). "+
				"Bind 127.0.0.1, or set AllowNonLoopback to state that you are exposing it deliberately",
			s.config.Addr, host)
	}
	s.warnExposed(s.config.Addr)
	return nil
}

// warnExposed says, by name, what stops being true.
//
// A comment recording the assumption would be read by whoever changed the
// address, which is exactly the person who does not read it. This runs at the
// moment the assumption is dropped, on the operator's own log.
func (s *Server) warnExposed(addr string) {
	s.logger.Warn(
		"the player socket is bound beyond loopback; its local-only security posture NO LONGER HOLDS",
		"address", addr,
		"missing", exposureWarnings())
}

// exposureWarnings enumerates the controls this transport does not have, each of
// which the local-only scope is the reason for.
func exposureWarnings() []string {
	return []string{
		"no rate limit and no lockout: credentials can be guessed as fast as the network allows",
		"no TLS: unless something terminates it in front of this port, every credential and every " +
			"player's fiction crosses the network in clear text",
		"static credentials: a roster entry has no expiry, no rotation and no revocation short of a restart",
		"one credential is one player's whole campaign, in both directions — submit as them and read their fiction",
		"same-origin browser check only: a real browser client needs an allow-list decision nobody has made",
		"no audit trail beyond this log: a successful authentication is not recorded as a fact anywhere",
		"no admission limit: concurrent handshakes are bounded only by the handshake timeout, so a client " +
			"can hold file descriptors without ever authenticating — the one resource on this path with no ceiling",
	}
}

// isLoopbackHost reports whether a host names this machine and nothing else.
//
// "localhost" is accepted by name because refusing it would be pedantry about a
// string every operator uses for exactly this; everything else must be an IP
// literal that says so. An empty host is the wildcard — every interface the box
// has — and is the single most likely way this posture is lost by accident.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}

// Listen binds the configured address.
func (s *Server) Listen() (net.Listener, error) {
	if s.config.Addr == "" {
		return nil, errors.New("the player socket has no configured address to bind")
	}
	listener, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return nil, fmt.Errorf("bind the player socket on %s: %w", s.config.Addr, err)
	}
	return listener, nil
}

// Serve serves the player socket until ctx is done.
//
// It checks the listener it was HANDED rather than trusting the configuration,
// because that is the address clients can actually reach: a hostname that
// resolved somewhere unexpected, a wildcard bind, or a listener built by a caller
// who never saw Config all arrive here and nowhere else.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("the player socket requires a listener")
	}
	if err := s.checkBoundAddr(listener.Addr()); err != nil {
		return err
	}

	server := &http.Server{
		Handler: s,
		// The handshake is an ordinary HTTP request until it is upgraded, so it
		// gets an ordinary header deadline. After the upgrade, timeouts are the
		// connection's own — see conn and the liveness probe.
		ReadHeaderTimeout: DefaultHandshakeTimeout,
		// Every connection's context descends from this one, which is how a
		// cancelled ctx reaches a socket that http.Server.Shutdown deliberately
		// will not touch: Shutdown does not close hijacked connections, and every
		// live WebSocket is one.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	// Closed on the way out so the watcher below never outlives the server it
	// watches — including when Serve returns for a reason that is not a
	// cancellation.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			//nolint:errcheck // the listener is going away; the serve error is the one that matters
			server.Close()
		case <-stopped:
		}
	}()

	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Run binds the configured address and serves until ctx is done.
func (s *Server) Run(ctx context.Context) error {
	listener, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(ctx, listener)
}

// checkBoundAddr is the authoritative half of the exposure gate.
func (s *Server) checkBoundAddr(addr net.Addr) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		if s.config.AllowNonLoopback {
			s.warnExposed(addr.String())
			return nil
		}
		return fmt.Errorf(
			"the player socket refuses to serve on %s (%s): this transport can only prove a %q listener is "+
				"loopback, and it refuses what it cannot prove", addr, addr.Network(), "tcp")
	}
	ip, ok := netip.AddrFromSlice(tcp.IP)
	if !ok || !ip.Unmap().IsLoopback() {
		if s.config.AllowNonLoopback {
			s.warnExposed(addr.String())
			return nil
		}
		return fmt.Errorf(
			"the player socket refuses to serve on %s: that address is reachable from off this box, and this "+
				"transport's security posture assumes nothing off this box can reach the port (see the package "+
				"doc). Bind 127.0.0.1, or set AllowNonLoopback to state that you are exposing it deliberately",
			addr)
	}
	return nil
}

// ServeHTTP upgrades one authenticated client into one session.
//
// The order is the posture, and none of it is arbitrary:
//
//  1. The path, so a stray request is a 404 rather than a handshake.
//  2. WHERE THE CLIENT IS, before anything about who they are — a peer this
//     instance should not be hearing from is refused before its credential is
//     read, let alone compared.
//  3. The credential, during the HTTP handshake, so a client that fails it is
//     answered with a status code and NEVER BECOMES A SOCKET.
//  4. The upgrade.
//  5. The bind, which is the only thing that makes this connection a delivery
//     target, immediately followed by the defer that ends it.
//
// A connection lives until its request context ends or its socket does. Served
// through Serve, that context is the one Serve was given, so cancelling it closes
// every live socket. Mounted on somebody ELSE's http.Server, it is whatever that
// server provides — and a host that never cancels request contexts will hold
// these sockets open until the process exits, because http.Server.Shutdown does
// not close hijacked connections.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.config.Path {
		http.NotFound(w, r)
		return
	}
	if !s.peerIsAllowed(r) {
		// 403 rather than 404: the operator's log is where this is diagnosed, and
		// a client that reached a port it should not have reached is told plainly
		// rather than left to conclude the engine is down.
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	credential, ok := bearerCredential(r)
	if !ok {
		s.refuseUnauthenticated(w, r, "no bearer credential")
		return
	}
	player, err := s.gate.Verify(r.Context(), credential)
	switch {
	case errors.Is(err, gateway.ErrUnauthenticated):
		s.refuseUnauthenticated(w, r, "the credential names no player of this world")
		return
	case err != nil:
		// An engine failure, not a client one. The operator gets the cause; the
		// client gets a status, because an engine error names buckets, subjects
		// and entity ids and this is the surface somebody would probe for them.
		s.logger.Error("a connection could not be authenticated", "error", err)
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written its own response. Nothing was bound, so
		// there is nothing to release.
		s.logger.Warn("a verified client could not be upgraded", "error", err)
		return
	}
	s.serve(r.Context(), ws, player)
}

// serve binds one upgraded socket and runs it until it ends.
func (s *Server) serve(ctx context.Context, ws *websocket.Conn, player gateway.VerifiedPlayer) {
	connID := s.mintConnectionID()
	c := newConn(connID, ws, s.writeTimeout)

	session, err := s.gate.Bind(player, gateway.Connection{
		ID:      connID,
		Adapter: vocabulary.AdapterWebSocket,
		// The connection id, which for a WebSocket is a DELIVERY HINT and not a
		// durable address — nothing dials it, and targeted egress resolves the
		// live socket from the player entity id instead.
		ReplyTo: connID,
		Writer:  c,
	})
	if err != nil {
		if errors.Is(err, gateway.ErrTooManyConnections) {
			s.logger.Warn("a connection was refused by the per-player cap", "player", player.PlayerID())
			c.closeWith(websocket.ClosePolicyViolation,
				"this player already holds the maximum number of connections")
			return
		}
		s.logger.Error("a verified player could not be bound to a socket",
			"player", player.PlayerID(), "error", err)
		c.closeWith(websocket.CloseInternalServerErr, "the engine could not open this session")
		return
	}

	// Registered LIFO, so they run: stop the watcher, stop being a delivery
	// target, then close the socket. Every way out of readLoop — a read error,
	// an abnormal close, a refused oversize frame, a failed write, a cancelled
	// context, a panic — unwinds through all three. This is the whole of
	// obligation (2) in the package doc.
	stopped := make(chan struct{})
	defer c.close()
	defer s.gate.Disconnect(connID)
	defer close(stopped)

	go s.watch(ctx, c, stopped)
	go s.probe(c, stopped)

	s.logger.Info("a player connected", "player", session.PlayerID, "connection", connID)
	s.readLoop(ctx, c, session.PlayerID)
	s.logger.Info("a player's connection ended", "player", session.PlayerID, "connection", connID)
}

// mintConnectionID produces an identifier no client can influence and this
// process will not produce twice.
//
// The counter never decreases and the nonce is per SERVER, so neither two
// servers in one process nor one server after a restart can mint an id another
// session was bound to. That is obligation (3) in the package doc, and it is
// deliberately arithmetic rather than a rule somebody has to follow: there is no
// header, query parameter or frame field anywhere in this package that a client
// could use to name a connection, so an id cannot be reused without coming back
// through Bind — because it cannot be named at all.
func (s *Server) mintConnectionID() string {
	return fmt.Sprintf("ws-%s-%d", s.nonce, s.counter.Add(1))
}

// watch closes the socket when the server's context ends.
//
// It exists because http.Server.Shutdown deliberately does not touch hijacked
// connections, and every live WebSocket is one. Without it, a cancelled context
// would leave every player connected and every session bound until the process
// died.
func (s *Server) watch(ctx context.Context, c *conn, stopped <-chan struct{}) {
	select {
	case <-ctx.Done():
		c.closeWith(websocket.CloseGoingAway, "the engine is shutting down")
	case <-stopped:
	}
}

// probe pings the peer on an interval.
//
// It is a LIVENESS check and not an idle timeout, and the difference is the whole
// reason it is allowed to exist here. A conformant client answers a ping without
// its application doing anything, so a player who has not acted in three weeks
// keeps their session while a laptop that slept loses it in seconds. Nothing in
// this loop measures the gap between a player's turns.
func (s *Server) probe(c *conn, stopped <-chan struct{}) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopped:
			return
		case <-ticker.C:
			// WriteControl is documented as safe alongside any other method, so
			// this does not contend with a delivery in flight.
			if err := c.ws.WriteControl(
				websocket.PingMessage, nil, time.Now().Add(s.writeTimeout)); err != nil {
				// The read loop will see the same failure and unwind; closing
				// here just stops it waiting for a read deadline first.
				c.close()
				return
			}
		}
	}
}

// readLoop reads submissions until the socket ends.
func (s *Server) readLoop(ctx context.Context, c *conn, playerID string) {
	// The bound the gateway cannot apply to itself. Gorilla enforces it against
	// the frame HEADER's declared length, so an oversize frame's payload is never
	// read and never allocated — and the peer is sent a 1009 close, which is what
	// distinguishes "too big to read" from a submission the engine read and
	// refused.
	c.ws.SetReadLimit(int64(gateway.MaxRequestBytes))

	deadline := s.pingInterval + s.pongTimeout
	extend := func() {
		//nolint:errcheck // a failure here surfaces as the next read's error
		c.ws.SetReadDeadline(time.Now().Add(deadline))
	}
	extend()
	c.ws.SetPongHandler(func(string) error {
		extend()
		return nil
	})

	for {
		messageType, raw, err := c.ws.ReadMessage()
		if err != nil {
			s.logReadEnd(c, err)
			return
		}
		// Traffic is liveness too: a client that is submitting is plainly there.
		extend()

		if messageType != websocket.TextMessage {
			// Answered rather than closed, for the reason a malformed frame is:
			// a client that cannot see why it was refused repeats the mistake.
			if err := c.send(ctx, responseFrame(refusal(payload.SubmitRefusal{
				Code:    payload.RefusalMalformedRequest,
				Message: "this protocol speaks JSON text frames; a binary frame carries no submission",
			}))); err != nil {
				s.logger.Warn("a refusal could not be delivered", "connection", c.id, "error", err)
				return
			}
			continue
		}
		operation, typed, typeErr := inspectRequestType(raw)
		if typed && typeErr != nil {
			if err := c.send(ctx, operationFrame(operationRefused("", OperationMalformed,
				"the operation type must be a non-empty string"))); err != nil {
				s.logger.Warn("an operation refusal could not be delivered", "connection", c.id, "error", err)
				return
			}
			continue
		}
		if typed && operation != RequestRetrieve {
			if err := c.send(ctx, operationFrame(operationRefused(string(operation), OperationUnsupported,
				"this player protocol does not support that operation"))); err != nil {
				s.logger.Warn("an operation refusal could not be delivered", "connection", c.id, "error", err)
				return
			}
			continue
		}
		if typed {
			response := s.retrieve(ctx, playerID, raw)
			if err := c.send(ctx, retrievalFrame(response)); err != nil {
				s.logger.Warn("a retrieval answer could not be delivered", "connection", c.id, "error", err)
				return
			}
			continue
		}

		response, submitErr := s.gate.Submit(ctx, c.id, raw)
		if submitErr != nil {
			// The gateway has already logged the cause and composed the client's
			// answer; this is the transport's own record that a player was told
			// the engine could not take their action.
			s.logger.Error("a submission was refused as unavailable", "connection", c.id, "error", submitErr)
		}
		if err := c.send(ctx, responseFrame(response)); err != nil {
			s.logger.Warn("a submission answer could not be delivered", "connection", c.id, "error", err)
			return
		}
	}
}

// retrieve answers one lookup through a surface that authorizes the turn's
// ownership scalar against the authenticated session before composing private
// artifacts. Latest cannot name another player at all.
func (s *Server) retrieve(ctx context.Context, playerID string, raw []byte) *RetrieveResponse {
	request, err := decodeRetrieveRequest(raw)
	if err != nil {
		return retrievalRefused(RetrieveLatest, "", RetrieveMalformed, err.Error())
	}
	if s.results == nil {
		return retrievalRefused(request.By, request.ID, RetrieveUnavailable,
			"result retrieval is unavailable on this server")
	}

	var delivery *payload.TurnDelivery
	switch request.By {
	case RetrieveByTurn:
		delivery, err = s.results.ByTurnForPlayer(ctx, playerID, request.ID)
	case RetrieveByAction:
		delivery, err = s.results.ByActionForPlayer(ctx, playerID, request.ID)
	case RetrieveLatest:
		delivery, err = s.results.Latest(ctx, playerID)
	}
	switch {
	case errors.Is(err, egress.ErrNoTurnHistory), errors.Is(err, egress.ErrTurnNotFound),
		errors.Is(err, egress.ErrResultNotAccessible):
		return retrievalRefused(request.By, request.ID, RetrieveNotFound,
			"no accessible result exists for that lookup")
	case errors.Is(err, egress.ErrNoResult):
		return retrievalRefused(request.By, request.ID, RetrieveNotReady,
			"that lookup has no terminal result yet")
	case err != nil:
		s.logger.Error("a durable player result could not be retrieved",
			"player", playerID, "by", request.By, "error", err)
		return retrievalRefused(request.By, request.ID, RetrieveUnavailable,
			"the result could not be retrieved")
	case delivery == nil || delivery.Result == nil:
		s.logger.Error("the result surface returned an empty delivery",
			"player", playerID, "by", request.By)
		return retrievalRefused(request.By, request.ID, RetrieveUnavailable,
			"the result could not be retrieved")
	case delivery.Result.PlayerID != playerID:
		s.logger.Error("the scoped result surface returned another player's result",
			"player", playerID, "result_player", delivery.Result.PlayerID, "by", request.By)
		return retrievalRefused(request.By, request.ID, RetrieveUnavailable,
			"the result could not be retrieved")
	default:
		return retrievalFound(request, delivery)
	}
}

// logReadEnd records how a connection ended, at the level the reason deserves.
func (s *Server) logReadEnd(c *conn, err error) {
	switch {
	case errors.Is(err, websocket.ErrReadLimit):
		s.logger.Warn("a client sent a frame past the request budget; the connection was closed",
			"connection", c.id, "budget_bytes", gateway.MaxRequestBytes)
	case websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway):
		s.logger.Debug("a client closed its connection", "connection", c.id)
	default:
		// Abnormal closes land here, and they are the ordinary case on a network:
		// a dropped socket, a killed client, a slept laptop. The session is
		// released by the same defer either way.
		s.logger.Debug("a connection ended", "connection", c.id, "reason", err)
	}
}

// peerIsAllowed reports whether this request came from somewhere this instance
// is willing to hear from.
//
// It fails CLOSED: an address this cannot parse is not admitted. The one thing
// it cannot see is a peer arriving through a tunnel or proxy, which presents as
// loopback because by then it is — see the package doc, where that limit is
// stated rather than papered over.
func (s *Server) peerIsAllowed(r *http.Request) bool {
	addr, err := netip.ParseAddrPort(r.RemoteAddr)
	loopback := err == nil && addr.Addr().Unmap().IsLoopback()
	if loopback {
		return true
	}
	if s.config.AllowNonLoopback {
		// Logged per connection deliberately. Under the intended posture this
		// never fires; under an exposed one it is the only record that somebody
		// off the box is talking to this campaign.
		s.logger.Warn("a non-loopback client connected to the player socket",
			"remote", r.RemoteAddr, "parse_error", err)
		return true
	}
	s.logger.Error(
		"refused a player-socket request from a non-loopback address; this instance is configured local-only",
		"remote", r.RemoteAddr, "parse_error", err)
	return false
}

// bearerCredential reads the credential out of the handshake request.
//
// A header rather than a query parameter, because a query string is written to
// every access log and proxy trace that ever sees the URL, and a credential in
// one is a credential on disk.
func bearerCredential(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if len(header) <= len(credentialScheme) || !strings.EqualFold(header[:len(credentialScheme)], credentialScheme) {
		return "", false
	}
	credential := strings.TrimSpace(header[len(credentialScheme):])
	return credential, credential != ""
}

// refuseUnauthenticated answers a client that did not authenticate.
//
// The body is the bare status text. It names no player, does not distinguish an
// unknown credential from one naming a player this world does not have, and
// never echoes what was sent — the operator's log carries the reason, and the
// client carries none of it.
func (s *Server) refuseUnauthenticated(w http.ResponseWriter, r *http.Request, reason string) {
	s.logger.Warn("refused an unauthenticated player-socket connection",
		"remote", r.RemoteAddr, "reason", reason)
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// refusal composes the transport's own refusal answer.
//
// The transport composes one only for a frame the gateway never sees — a binary
// frame is not a submission at all. Everything the gateway can judge is judged
// there, so this is one shape and not a second refusal vocabulary.
func refusal(reason payload.SubmitRefusal) *payload.SubmitResponse {
	return &payload.SubmitResponse{
		Protocol: payload.PlayerProtocolV1,
		Status:   payload.StatusRefused,
		Refusal:  &reason,
	}
}
