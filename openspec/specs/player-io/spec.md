# player-io Specification

## Purpose
TBD - created by archiving change turn-loop-vertical-slice. Update Purpose after archive.
## Requirements
### Requirement: Public submission contract distinct from the canonical action
An untrusted client SHALL submit a versioned `SubmitAction` request carrying only
client-owned fields — the action text and an idempotency key — and the gateway SHALL inject
every server-owned field: `player_id` (from the authenticated session), `campaign_id`,
`scene_id`, the arrival timestamp, the derived `action_id`, and the channel binding. A
request that carries a server-owned field SHALL be refused rather than having it ignored.

The canonical `PlayerAction` is the ENGINE's input contract, not the wire format. Every one
of its identity fields is server-owned, and `action_id` is the sharpest: it is derived
deterministically so a redelivered channel message produces one turn rather than two, which
means a client able to choose it can pre-claim another player's action id and have their
turn silently absorbed as a duplicate.

#### Scenario: A client-supplied identity field is refused
- **WHEN** a client submits a request carrying `player_id`, `action_id`, or `arrived_at`
- **THEN** the request is refused naming the field, and no `PlayerAction` is published

#### Scenario: The server injects what the client cannot know
- **WHEN** a client submits a well-formed `SubmitAction` over an authenticated session
- **THEN** the published `PlayerAction` carries the session's `player_id`, the gateway's
  arrival timestamp, and an `action_id` derived from the channel-native message identity

### Requirement: Canonical transport-neutral action payload
Every player action SHALL enter the engine as a canonical `PlayerAction` payload carrying
`action_id`, `player_id` (a graph entity ID, never a connection identifier), `campaign_id`,
`scene_id`, free-text action, arrival timestamp, and a channel metadata block (adapter
type, reply-to address). Ingress adapters SHALL only authenticate, normalize, and publish;
no engine component downstream of the action stream SHALL read transport-specific fields
except the egress adapter resolving the channel binding.

#### Scenario: WebSocket action is normalized
- **WHEN** a player submits an action over the WebSocket adapter
- **THEN** a `PlayerAction` with all identity fields and an arrival timestamp is published
  to the player-action stream, and no downstream component behavior depends on the
  adapter type

#### Scenario: Player identity is not a connection
- **WHEN** the same player reconnects with a new WebSocket connection and submits an action
- **THEN** the `PlayerAction` carries the same `player_id` entity ID as before the
  reconnect

### Requirement: Player actions are requests on a stream
Player actions SHALL be published to a JetStream stream (not KV) and consumed by a durable
consumer. After a process restart, consumption SHALL resume from the last acknowledged
message and SHALL NOT re-deliver an acknowledged action.

#### Scenario: Restart does not replay acknowledged actions
- **WHEN** the engine restarts after a turn completed and its action was acknowledged
- **THEN** the completed action is not redelivered and no second turn is created for it

### Requirement: One active turn per player is enforced, not assumed
A gateway SHALL refuse a player's action while that player holds a non-terminal turn,
answering with a structured in-progress response naming the active `turn_id`. The refusal
SHALL happen at ingress, before publication.

The engine's correctness rests on one player having one turn at a time, and today that is
an assumption rather than a guard: a duplicate action is safe by construction, but a second
DISTINCT action creates a second turn. Refusal belongs at ingress because that is the only
place a player is still connected to be told; a refusal after publication has no path back
to them.

#### Scenario: A second distinct action is refused while a turn is live
- **WHEN** a player submits an action while holding a turn whose phase is non-terminal
- **THEN** the request is refused with a structured in-progress response naming the active
  `turn_id`, and no second `PlayerAction` is published

#### Scenario: A resubmitted action is not a second turn
- **WHEN** a player resubmits the same action with the same idempotency key
- **THEN** it resolves to the same `action_id` and does not create a second turn

### Requirement: Every submission is answered with one closed response document
A gateway SHALL answer every submission with a single versioned response document whose
outcome is drawn from a closed set — accepted or refused — and a refusal SHALL name its
reason with a closed code, including the case where the player already holds a live turn,
plus, where the cause is a field, that field structurally rather than in prose.

One document with a discriminator rather than a shape per outcome, for the same reason
`TurnResult` covers success and failure together: a client that must guess which shape
arrived will guess wrong on the path it exercises least, which is the refusal path. Closed
codes and a structural field reference are what let a client act on a refusal — retry,
correct a field, or wait — instead of matching on a message it will misparse the day the
wording improves.

#### Scenario: A refusal names its cause structurally
- **WHEN** a submission is refused because a field is invalid
- **THEN** the response carries a closed refusal code and identifies the offending field as
  data, not only inside a human-readable message

#### Scenario: An accepted submission is distinguishable without inference
- **WHEN** a submission is accepted
- **THEN** the response states that outcome explicitly rather than leaving it to be inferred
  from the absence of a refusal

### Requirement: A turn's result is delivered to exactly the player it belongs to
The egress path SHALL deliver a turn's result to the player that turn belongs to and to no
other connected player. Delivery SHALL resolve the player's current connection from
`player_id` at delivery time rather than from a connection identifier captured at
submission.

Broadcast egress is a disclosure defect, not a performance one: a component that fans every
message out to all connected sockets would satisfy "the result was delivered" while showing
one player's narration to everyone in the campaign. The delivery path SHALL therefore be
given a session lookup keyed on `player_id` and no way to enumerate every session, so that
broadcast is unexpressible rather than merely avoided.

`ChannelBinding.ReplyTo` is PER-ADAPTER, and each adapter SHALL state which of the two it
is. For a WebSocket it is a connection identifier and a delivery HINT only — invalid after
reconnect, and never dialled. For an adapter whose transport carries a durable address (an
email box, a chat channel) it IS an address, and that adapter's sink may deliver to it
directly because there is no live session to resolve. The engine records it either way and
resolves nothing from it.

#### Scenario: One player's narration does not reach another
- **WHEN** two players are connected and a turn belonging to the first resolves
- **THEN** the first player receives the result and the second receives nothing

#### Scenario: Delivery survives a reconnect
- **WHEN** a player reconnects on a new connection before their in-flight turn resolves
- **THEN** the result is delivered to the new connection, resolved from `player_id`

#### Scenario: A player with no live connection is not a delivery failure
- **WHEN** a turn resolves for a player who is not connected
- **THEN** the delivery is acknowledged with no recipients, nothing is queued in adapter
  memory, and the result remains retrievable from durable state

### Requirement: A delivered result carries the prose it references
A delivered document SHALL carry the canonical `TurnResult` UNMODIFIED plus the narration
prose that result references, resolved by the SERVER. The delivered result SHALL be
byte-identical to the published one; the prose SHALL be present exactly when the result
carries a `narration_ref`, and SHALL be refused when it belongs to another turn or voices a
band the turn did not land on.

`TurnResult.narration_ref` is an `obj://` storage reference and no client can resolve one, so
something must dereference it. Adding the prose to `TurnResult` at delivery would make the
delivered document differ from the published and archived one — two documents wearing one
name, which is the failure `TurnResult` was made a single discriminated type to avoid — so
the prose travels in an envelope AROUND the unmodified result.

The server dereferences rather than the client fetching, because a fetch-back protocol is one
only an interactive adapter can speak: an email or SMS adapter has no second round trip, and
no step may assume interactive pacing.

One delivery carries ONE turn's prose, bounded by the same budget that bounds the stored
artifact, and nothing else bulky — the verdict body, the effect batch and the stored action
stay behind their references. Nothing accumulates across turns, so per-turn wire cost is flat
for the same reason per-turn token cost is.

#### Scenario: The delivered result is the published result
- **WHEN** a turn's result is delivered
- **THEN** the result inside the delivered document encodes byte-identically to the canonical
  result composed from durable state

#### Scenario: Prose presence follows the reference
- **WHEN** a result carries a narration reference
- **THEN** the delivered document carries that prose, and a delivery carrying a reference with
  no prose — or prose with no reference — is refused

#### Scenario: Prose belonging to another turn is refused
- **WHEN** a result's narration reference resolves to prose recorded against a different turn
- **THEN** the delivery is refused rather than sent, naming both turns

### Requirement: Canonical terminal result and retrieval
A turn's result SHALL be durably stored independent of any connection, and a player SHALL
be able to retrieve it by `action_id` or `turn_id`, plus their most recent TERMINAL result.
A WebSocket client SHALL perform those lookups through an explicitly discriminated
`RetrieveRequest` on its authenticated connection; the adapter SHALL derive the player for
`latest` from that session and SHALL authorize results found by `action_id` or `turn_id`
against the turn's ownership scalar before resolving private roll or narration artifacts.
A retrieval SHALL NOT be inferred from the shape of `SubmitAction`: absence of `type` is the
compatible bare submission form, while a present unknown, empty, or non-string `type`
receives a typed operation refusal and never a `SubmitResponse`. Another player's result and an absent
result SHALL produce the same not-found code and message without returning a delivery, so a
guessed identifier is not a cross-player state oracle.
A successful result comprises the narration prose reference plus a resolution summary
(verdict class, roll values, outcome band); a failed turn's result comprises its recorded
failure reason, plus whatever narration and resolution the turn had produced before it
ended.

Terminal rather than completed, deliberately. A failed turn's narration is not guaranteed —
a turn that died before the narrator ran has none, and one abandoned after its prose landed
has some — so a retrieval surface keyed on narration cannot answer for it at all, and one
keyed on completion leaves the player who most needs an answer with silence indistinguishable
from a turn still running. The ledger already archives failed turns, so the data exists.

#### Scenario: Result survives disconnect
- **WHEN** a player disconnects after submitting an action and reconnects after the turn
  completes
- **THEN** the reconnected player retrieves the completed turn's narration and resolution
  summary through the authenticated public adapter

#### Scenario: A named result remains player-private
- **WHEN** an authenticated player requests a `turn_id` or `action_id` whose result belongs
  to another player
- **THEN** the adapter returns the same not-found refusal as an absent id, returns none of
  that result's delivery document, and does not read its roll or narration artifacts

#### Scenario: An unknown typed operation is not a submission
- **WHEN** a player/v1 document carries a non-empty `type` other than `retrieve_result`, or
  an empty/non-string `type`
- **THEN** the adapter returns a typed unsupported/malformed operation response and does
  not route the document through `SubmitAction`

#### Scenario: A failed turn still yields a player-visible result
- **WHEN** a player's turn ends in the `failed` phase with a recorded reason
- **THEN** retrieving their most recent terminal result returns that failure rather than
  the previous successful turn or an empty answer

#### Scenario: Retrieval distinguishes waiting from a turn that does not exist
- **WHEN** a result is retrieved for a turn that has not reached a terminal phase
- **THEN** the answer says the turn is still running, and is distinguishable without
  inference from the answer for a turn id nothing ever created

### Requirement: The most recent terminal turn is a durable fact, not a scan
A player entity SHALL carry a single-valued pointer at the turn that most recently RESOLVED
for them, written on the terminal transition by the same component that writes the terminal
phase. Retrieval of a player's most recent terminal result SHALL read that pointer TOGETHER
with the pointer at their most recently accepted turn, treat both as addresses rather than
answers, and decide between them by each named turn's own recorded phase and phase timestamp.

The accepted-turn pointer alone cannot answer this. It names the most recent turn, so it is
an answer only while that turn happens to be terminal; the moment the player acts again it
names a live one and nothing else in the graph names the terminal turn before it. The
alternative — scanning the archive — is O(campaign history) per retrieval, on a request path,
growing forever, because the archive is ordered by when turns were archived rather than
indexed by player.

It is a POINTER at a turn and never a flag about one, for the reason the accepted-turn
pointer is: "which turn resolved last" needs no clearing, so it cannot go stale in the
direction that strands a player. The phase is written before the pointer, so a failure in the
gap leaves the pointer naming an OLDER terminal turn — never a missing or a non-terminal one
— and reading both pointers is what closes that gap, because in exactly that window the
accepted-turn pointer still names the turn that just ended.

Retrieval SHALL read a BOUNDED number of candidate turns, so that a pointer written on an
appending lane degrades into a slightly stale answer rather than into a history scan.

#### Scenario: The last terminal result is answerable while the next turn is running
- **WHEN** a player's turn has resolved and they have since submitted another that is still
  in flight
- **THEN** retrieving their most recent terminal result returns the turn that ended, not
  silence and not the running turn

#### Scenario: A pointer that has not caught up still yields the right answer
- **WHEN** the resolved-turn pointer names an older terminal turn and the accepted-turn
  pointer names one that has since ended
- **THEN** retrieval answers with the turn that ended most recently

#### Scenario: Retrieval cost does not grow with a corrupted pointer
- **WHEN** a player's pointers hold more values than the two the engine writes
- **THEN** retrieval reads at most a bounded number of turns and still answers

### Requirement: A client SHALL be authenticated before it becomes a socket
The player transport SHALL authenticate a client during the connection handshake, before the
connection is upgraded. A client whose credential is missing, unknown, or names a player this
world does not have SHALL be refused with a transport-level status and SHALL NOT be upgraded,
SHALL NOT be bound to a session, and SHALL NOT become a delivery target.

There is then no such thing as an unauthenticated socket, which is the strongest available
answer to "what may one do, and for how long". It removes a pre-authentication connection
state, a first-frame handshake to get wrong, and any need for a timeout bounding that window.

The refusal SHALL NOT distinguish an unknown credential from one naming a player this world
does not have: the two have the same remedy and telling them apart is a membership oracle. It
SHALL NOT echo the credential, name a player, or carry an engine diagnosis.

Verifying a credential and binding a connection happen at different moments — verification needs
no socket and binding needs one — and where they are separate operations, the binding operation
SHALL require PROOF of verification rather than a player identifier. A player identifier passed
between the two connects nothing: binding an unverified player would compile, run, and produce a
working session for a player nobody ever read out of the graph, which is the invariant
"`player_id` is a graph entity" silently lost. The proof SHALL be unforgeable outside the
verifying operation, so that skipping verification is not expressible rather than merely
avoided.

#### Scenario: A wrong credential produces no socket and no session
- **WHEN** a client presents a missing, unknown, or wrong credential
- **THEN** the handshake is refused with an unauthorized status, no session exists for any
  player, and the client never receives a turn result

#### Scenario: Two different authentication failures are indistinguishable
- **WHEN** one client presents an unknown credential and another presents a credential naming
  a player that is not an entity of this world
- **THEN** both are answered identically, and neither answer names a player or echoes what was
  sent

#### Scenario: A session names a player the graph was read for
- **WHEN** a connection is bound to a session
- **THEN** its `player_id` is one a credential resolved to and a graph read proved real and
  non-stub, and a player identifier that has not been through that read cannot be bound at all

### Requirement: The local-only posture SHALL announce itself when it stops holding
The player transport SHALL refuse to bind or serve an address that is not loopback, and SHALL
refuse a request whose peer address is not loopback, unless the instance explicitly
acknowledges that it is exposed. The acknowledgement SHALL log the enumerated controls this
transport does not have, so that an operator exposing the port is told what they have taken
responsibility for.

Local-only is a SCOPE this code may assume, never a property it has: the hosted MVP is the same
image on a rented box, so the assumption ends by configuration rather than by a rewrite. An
assumption recorded only in a comment would be read by everybody except the person who changes
the address.

A peer arriving through a tunnel or proxy presents as loopback and cannot be distinguished from
a local one inside the process; that limit SHALL be stated at the site rather than papered over
with a client-settable forwarding header.

#### Scenario: A wildcard bind is a boot failure
- **WHEN** an instance is configured to listen on every interface without acknowledging it
- **THEN** the transport refuses to start, naming the acknowledgement that would allow it

#### Scenario: An acknowledged exposure enumerates what is missing
- **WHEN** an instance acknowledges that it is exposed beyond loopback
- **THEN** the operator log names each control that does not exist — rate limiting, transport
  encryption, credential rotation and revocation, an origin allow-list, an audit trail

#### Scenario: A non-loopback peer is refused before its credential is read
- **WHEN** a request arrives from a non-loopback address carrying a VALID credential
- **THEN** it is refused, no session is bound, and the refusal is recorded for the operator

### Requirement: A connection identifier SHALL be minted by the transport and never reused
The transport SHALL mint every connection identifier itself, SHALL NOT derive one from anything
a client can influence, and SHALL NOT issue the same identifier twice within a process.

A recycled identifier inherits the previous session: the session table is indexed by player as
well as by connection, so a socket bound to a reused id can both submit actions as the previous
player and receive that player's fiction, presenting as an ordinary session with nothing
anywhere to report it. The gateway cannot defend itself against this — establishing a
connection's identity is the only place it happens — so the transport SHALL make reuse
unrepresentable rather than merely avoided.

#### Scenario: Connections that come and go never repeat an identifier
- **WHEN** a player connects and disconnects repeatedly
- **THEN** no connection identifier is issued twice

#### Scenario: A client cannot name its own connection
- **WHEN** a client supplies a connection identifier in a header or query parameter
- **THEN** the session is bound to a minted identifier and the client's value is not used

### Requirement: A session SHALL end on every connection close path
The transport SHALL release a connection's session on every way a connection can end —
graceful close, abnormal close, a refused oversize frame, a failed write, and server shutdown.
A connection that has ended SHALL NOT be a delivery target.

A session outliving its socket is not a leak of memory alone: it is a live delivery target that
can never receive, so every later result for that player is written into nothing.

#### Scenario: A socket destroyed without a close handshake releases its session
- **WHEN** a client's connection is destroyed with no close frame
- **THEN** the player stops resolving to that connection, and a later result for that player is
  delivered to no recipients

#### Scenario: Shutdown ends every session
- **WHEN** the transport's context is cancelled
- **THEN** every live connection is closed with a shutdown reason and no session remains bound

### Requirement: The request budget SHALL be enforced by the transport's own reader
The transport SHALL apply the engine's maximum request size to the socket reader itself, so a
frame past the budget is refused before its payload is read. An oversize frame SHALL end the
connection, because a reader cannot resynchronise inside a frame it refused to read. A frame
within the budget that is malformed SHALL be answered with a closed refusal code on a LIVE
connection, so a client learns rather than reconnecting into the same mistake.

The gateway's own size check runs after the bytes are already in memory; it is a second line of
defence and never the bound.

#### Scenario: An oversize frame is never read
- **WHEN** a client sends a frame larger than the request budget
- **THEN** the connection is closed with a too-large close code, no action is published, and the
  session is released

#### Scenario: A frame at exactly the budget is read
- **WHEN** a client sends a frame of exactly the request budget
- **THEN** it is parsed and answered rather than closing the connection

#### Scenario: A malformed frame is answered and the connection survives
- **WHEN** a client sends bytes that are not a valid submission
- **THEN** it receives a refusal naming a closed code, and the same connection can still submit

### Requirement: The session table SHALL be bounded without an idle timeout
A player MAY hold several connections at once and every one of them SHALL be a delivery target.
The number SHALL be capped per player, and reaching the cap SHALL refuse the NEWEST connection
rather than evicting an existing one, so that the table is bounded by (roster size × cap).

No session SHALL be expired because of elapsed silence from its player: email-cadence play is
valid, and an idle-session timeout is exactly the interactive-pacing assumption this engine
forbids. A connection whose PEER has stopped answering a transport liveness probe MAY be ended,
which is a different fact — a running client answers without its player doing anything.

Evicting the oldest would make a leaked credential into a way to take a player's campaign away
from them; refusing the newest costs the newcomer a connection and costs the player nothing.

#### Scenario: The cap refuses the newest and leaves the others deliverable
- **WHEN** a player at their connection cap opens another connection
- **THEN** the new connection is closed with a reason, and every connection they already held
  still receives that player's results

#### Scenario: A quiet player keeps their session
- **WHEN** a connected player submits nothing for many multiples of the liveness probe interval
- **THEN** their session survives and a result delivered afterwards reaches them

#### Scenario: A peer that stops answering is reaped
- **WHEN** a connected client stops answering the transport's liveness probe
- **THEN** the connection is ended and its session released

### Requirement: Every server-to-client message SHALL say which document it is
Each server-to-client message SHALL carry an explicit type discriminator wherever one
connection carries more than one kind of document, so a client SHALL NOT have to infer which
document arrived from its shape. The documents inside SHALL be the canonical ones, unmodified.

A client that guesses by shape guesses wrong first on the path it exercises least, which is the
refusal path. The discriminator belongs to the ADAPTER rather than to the protocol: multiplexing
is a property of a duplex connection, and an adapter that delivers one document per message
needs none of it.

#### Scenario: A submission answer and a turn result are distinguishable
- **WHEN** a client receives a message on the player socket
- **THEN** it can tell a submission answer from a turn delivery from the message's own type
  field, and the canonical result inside encodes byte-identically to the published one

### Requirement: No interactive-pacing assumptions
No turn-processing step SHALL fail, time out, or degrade a turn because of elapsed
wall-clock time between a turn's completion and the player's next action.

#### Scenario: Email-cadence gap between turns
- **WHEN** a player submits their next action an arbitrarily long time after the previous
  turn completed
- **THEN** the action is processed identically to one submitted immediately

### Requirement: Terminal result carries a structural companion summary
The canonical terminal result SHALL carry a `CompanionResolution` when an active companion
bond produced a committed decision for the turn, including `silent`. A turn with no active
bond SHALL omit `CompanionResolution`. The summary SHALL contain `companion_id`, a decision
kind from the closed companion vocabulary, and a hint level exactly when the kind is
`hint`. Companion dialogue SHALL remain in ordinary narration prose rather than in the
structural result.

The player/v1 protocol tests SHALL pin the summary's fields, optionality, and closed sets.
Delivery and retrieval SHALL return the same canonical result without adapter-specific
rewriting.

#### Scenario: Hint result is machine-readable and voiced once
- **WHEN** a turn commits a companion `hint` at level `connect` and narration voices it
- **THEN** the canonical result contains the companion ID, `hint`, and `connect`, while the
  delivered prose contains the voice

#### Scenario: No active bond omits companion resolution
- **WHEN** the turn has no active companion bond
- **THEN** the canonical result omits `CompanionResolution`

#### Scenario: Active silent companion remains visible
- **WHEN** an active companion commits a `silent` decision
- **THEN** the canonical result contains `CompanionResolution` with that companion ID and
  kind `silent`, with no hint level or fabricated dialogue

#### Scenario: Retrieval preserves companion resolution
- **WHEN** a player retrieves a terminal turn that carried a companion resolution
- **THEN** the retrieved canonical result encodes the same companion summary as the
  originally published result
