// Package gateway is the player-session gateway: the seam where an untrusted
// client becomes an authenticated player and one submission becomes one
// canonical action on the engine's request stream.
//
// # Why this is SemMachina's and not SemStreams'
//
// Because the framework's WebSocket pair is federation plumbing and this is a
// session concern. `input/websocket` authenticates the COMPONENT — one bearer
// token in configuration, `authenticateRequest` returning a bool — and then
// publishes the client's payload bytes VERBATIM to a configured subject
// (websocket_input.go, handleMessage). Pointed at the action stream, that hands
// the client authorship of every identity field on a PlayerAction, including
// action_id, which is the pre-claim attack SubmitAction's doc names. It has no
// notion of WHICH player is on the socket and no place to put one.
//
// Per-recipient delivery — the egress half — is filed upstream as a generic ask
// rather than pretended to be a game concern. The session concern is genuinely
// ours: what a session means, what it is bound to, and what one player may do
// while their last turn is still running are game rules.
//
// # Identity is a graph entity, never a connection
//
// A session binds a connection to a player ENTITY ID, and everything downstream
// carries that. A reconnect authenticates a new connection to the same player,
// so nothing the engine records changes when a socket drops. The connection id
// travels only as ChannelBinding.ReplyTo, which is delivery metadata: no engine
// component downstream of the action stream may branch on it.
//
// A session is minted only after the player entity is READ from the graph and
// found to be real — present and not a referential stub. That read is what makes
// "player_id is a graph entity" a property rather than a naming convention: a
// credential that resolves to an id nothing ever imported produces no session,
// rather than a stream of actions attributed to a player who does not exist.
//
// # The three server-owned things this package decides
//
//  1. The arrival timestamp. Deadline evaluation uses arrival time, so the
//     engine's clock decides it, never the client's.
//  2. The channel binding, from the connection the session is on.
//  3. The action id — see ActionIDFor, which is the sharp one.
//
// # One active turn, enforced convergently
//
// Submit refuses an action while the player's current turn is non-terminal,
// answering a structured turn_in_progress that names it. The state it reads is
// vocabulary.PlayerTurnCurrent on the player entity — a pointer AT a turn, not a
// flag about one — and the terminality is read from that turn, which owns it.
//
// The guard is CONVERGENT, not exclusive, exactly as the phase guard in
// internal/turn is, and for the same missing primitive. Two submissions racing
// from one player can both read a terminal pointer and both publish, because the
// read and the write are separated by a publish and an intake, and there is no
// compare-and-swap to close it (the entity query surface returns no revision to
// swap on — an engine ask, not a local retry loop). The window is wider here
// than in a single component: the pointer is written by the RECORDER, when the
// turn is created, so between a publish and the turn's creation the pointer
// still names the player's previous, terminal turn.
//
// That cost is deliberate, and the alternative is worse. A pointer written at
// ingress, before publishing, would close the window and open a lockout: the
// gateway would then have to decide what a pointer at a turn that does not exist
// YET means, and "admit" reopens the same double-turn while "refuse" strands the
// player permanently the first time a publish fails after the write. What the
// window actually costs is one extra turn for a player who submits two DISTINCT
// actions inside the engine's own intake latency — milliseconds, not the human
// pacing this design is built for — and the engine stays correct in that case:
// two actions make two turns, each idempotent, each resolvable. Every other
// failure in this path degrades in the same direction, toward admitting rather
// than toward locking somebody out of their campaign.
//
// One extra turn IN TOTAL, not one per failure, and that bound is held on the
// writing side rather than here: a late pointer write does not move a player off
// a turn they are still running. Recorder.pointPlayerAtTurn carries the
// argument, and turn.HeldTurns is the reader both sides share so that "which
// turns does this player hold" has one answer rather than two.
//
// # No interactive pacing
//
// Nothing here measures the gap between a player's turns, and nothing expires a
// session for being quiet. The only clock is the arrival stamp, which is
// recorded rather than compared. Play at email cadence is a supported pace.
package gateway
