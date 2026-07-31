// Package playersocket is the WebSocket adapter: the first code an untrusted
// client touches, and the place this engine's security posture is STATED rather
// than assumed.
//
// It is one adapter of N. The gateway owns what a session means and the egress
// router owns who a result belongs to; what lives here is everything a socket
// makes true — frame bounds, decode failures, abnormal closes, slow readers, and
// where a connection id comes from.
//
// # THE POSTURE IS LOCAL-ONLY, AND LOCAL-ONLY IS A SCOPE, NOT A PROPERTY
//
// Everything below is sized for the MVP's deployment: one box, one world, one
// process, a known cast, and a listener nothing off the machine can reach.
// CLAUDE.md says the hosted MVP is THE SAME IMAGE ON A RENTED BOX, so the day
// this port is exposed is a day that arrives by configuration, not by a rewrite —
// and every assumption resting on "nobody else can reach this port" becomes a
// vulnerability at that moment, silently.
//
// So the assumption is not recorded in a comment and left there. It is ENFORCED:
// Config.Addr must be a loopback address or NewServer refuses to build, Serve
// re-checks the address actually bound rather than the one configured, and a
// request arriving from a non-loopback peer is refused before its credential is
// read. Turning that off takes Config.AllowNonLoopback, which is not a flag that
// merely permits something — it logs, at WARN, the enumerated list of controls
// that do not exist yet (see exposureWarnings). An operator who exposes this port
// is told what they just took responsibility for.
//
// Those three read three different inputs, and they are not always a stack of
// three. A composition that mounts ServeHTTP on ITS OWN http.Server passes
// through neither address check by construction — this package never sees that
// listener — so on that path the peer check is the entire posture. That is not a
// gap so much as the strongest of the three standing alone: the port may be open
// to the world and a client off the box still cannot use it.
//
// One assumption could not be made self-announcing, and it is stated here rather
// than left implicit: a socket that arrives THROUGH something — an SSH tunnel, a
// reverse proxy, a container port forward — presents a loopback peer address, so
// the runtime gate sees a local client and admits it. Nothing in this process can
// distinguish that from a client on the box, because by the time the bytes arrive
// the distinction has been erased by the thing forwarding them. The bind gate is
// what covers it: a tunnel has to land somewhere, and this server will only bind
// where a tunnel would have to be built deliberately rather than found lying open.
//
// # What authenticates a session
//
// A bearer credential from the Authorization header of the HTTP request that
// becomes the WebSocket, checked against gateway.Roster: credential → player
// entity id, compared in constant time, with every player id validated as a
// canonical six-part entity id at construction so a config typo is a boot failure
// rather than a session for a player nobody imported.
//
// It is a HEADER and not a first frame, and that is the decision that makes the
// next section true. It is a bearer credential and never a cookie, deliberately:
// a cookie is ambient authority, so a page on another origin could open a socket
// that authenticates itself, and the browser would attach the credential without
// anybody choosing to. Nothing here has ambient authority — a client that does
// not present the credential is nobody.
//
// # What an unauthenticated socket may do, and for how long
//
// Nothing, and it does not exist. Authentication happens during the HTTP
// handshake, BEFORE the upgrade, so a client with a wrong or missing credential
// is answered 401 and never becomes a socket at all. There is no pre-auth
// connection state, no first-frame handshake to get wrong, and no window to bound
// with a timeout — the whole class of "what may it do before it authenticates"
// questions is answered by there being no it.
//
// That is also why gateway.Verify and gateway.Bind are separate calls. Verifying
// needs no socket and binding needs one, so each happens at the only moment it
// can: the credential is judged while a status code is still available, and the
// session is bound the instant there is something to write to.
//
// A wrong credential is answered with a bare 401 whose body is the status text.
// It names no player, does not say whether the credential was unknown or named a
// player this world does not have (gateway.ErrUnauthenticated is one sentinel for
// exactly that reason — the two have the same remedy and telling them apart is a
// membership oracle), and never echoes what was sent.
//
// There is deliberately NO rate limit and no lockout, and this is the control
// most obviously missing the day the port is exposed. Both alternatives are worse
// at the MVP's scale: a per-credential lockout is a way to lock a player out of
// their own campaign with guesses, and a global one is a way to lock out the
// whole cast. On loopback the compensating control is the loopback gate itself —
// which is why exposing the port announces this by name.
//
// # What a second connection claiming the same player does
//
// It connects, and it receives. Multi-device is the posture: a player's sockets
// are ALL delivery targets, resolved from their entity id at delivery time, so a
// result reaches every screen they have open. What bounds it is
// gateway.DefaultMaxConnectionsPerPlayer, enforced inside the session table under
// its own lock, refusing the NEWEST connection rather than evicting an existing
// one — because eviction would turn a leaked credential into a way to take a
// player's campaign away from them.
//
// # What a malformed frame does
//
// It is ANSWERED, and the socket survives. A frame that is not JSON, not a
// SubmitAction, or carries a server-owned field comes back as a closed refusal
// code on a live connection, because a client that cannot see why it was refused
// will retry the same mistake forever.
//
// An OVERSIZE frame is the exception and it ends the connection, because there is
// no way to answer it: the reader cannot resynchronise in the middle of a frame
// it refused to read, so the peer is sent a 1009 and the socket closes. The two
// are distinguishable from the client's side by exactly that — a refusal frame
// means the engine read your submission and declined it; a 1009 close means it
// never read it at all.
//
// # The three obligations this transport inherited, and where each is discharged
//
//  1. gateway.MaxRequestBytes is a bound THE READER must apply. It is applied
//     with websocket.Conn.SetReadLimit, which enforces it against the frame
//     HEADER's declared length — so an oversize frame's payload is never
//     allocated, which is the whole point the gateway's own check (running after
//     Submit already holds the bytes) cannot achieve.
//
//  2. A session leaks unless Disconnect is called. Every close path here unwinds
//     through one deferred Disconnect on the connection goroutine — read error,
//     abnormal close, oversize frame, write failure, server shutdown, panic. The
//     defer is registered immediately after Bind returns, so no path exists
//     between binding and the defer.
//
//  3. A CONNECTION ID MUST NEVER BE REUSED WITHOUT GOING THROUGH BIND. This is
//     the sharp one, because the gateway cannot defend itself against it: the
//     session table is indexed by player as well as by connection, so a recycled
//     id inherits the previous session — a socket that can both submit as the old
//     player and receive that player's fiction, presenting as an ordinary session
//     with nothing anywhere to report it.
//
//     It is discharged by construction rather than by discipline: connection ids
//     are MINTED HERE, from a per-server random nonce and a monotonically
//     increasing counter, and nothing on the wire can influence one. There is no
//     header, no query parameter and no frame field a client can set to name a
//     connection. The counter never decreases, so an id is used once per process;
//     the nonce means two servers in one process (or one after a restart) cannot
//     collide either. Reuse is not avoided — it is unrepresentable.
//
// # What bounds the session table, given that nothing may expire for silence
//
// The bound is (roster size × connections per player), which is a number known at
// boot. It holds without any notion of how long a player has been quiet, which is
// the requirement: email-cadence play is valid, so a session that expired because
// its player did not act would be exactly the interactive-pacing assumption this
// engine forbids.
//
// What IS reaped is a socket whose PEER has gone away, and that is a different
// fact from a player being quiet. The server pings on an interval and requires a
// pong within a timeout; a client that is running answers automatically without
// its player doing anything, so a player who is away from the keyboard for three
// weeks keeps their session while a laptop that slept loses it in seconds. Both
// are correct, and neither measures the gap between turns.
//
// The write deadline is the same kind of clock: it bounds how long the ENGINE
// waits on a socket's write buffer, never how long a player takes to act. A
// socket that cannot absorb one document is closed rather than allowed to hold
// the egress consumer's ack deadline open for every player queued behind it.
//
// # What travels on this socket
//
// Client to server: either a bare payload.SubmitAction, retained exactly for v1
// compatibility so the gateway's strict decoding applies to the client's own
// bytes, or an explicitly typed RetrieveRequest. Retrieval never accepts a
// player id: Latest derives it from authentication, and named lookups authorize
// the turn's ownership scalar against that same session BEFORE resolving dice or
// prose. A foreign id and an absent id receive the same not-found refusal, so the
// operation is not a cross-player state oracle.
//
// Server to client: a Frame, which is a typed envelope, because this direction
// carries submission answers, pushed turns, retrieval answers, and typed
// operation refusals, and a client
// that had to infer which one arrived would guess wrong on the path it exercises
// least. The envelope is this adapter's and not the protocol's: multiplexing is a
// property of a duplex socket, and an email adapter delivering one document per
// message needs none of it.
package playersocket
