# player-io — Delta Spec

## ADDED Requirements

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
- **THEN** the reconnected player receives the completed turn's narration and resolution
  summary

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

### Requirement: No interactive-pacing assumptions
No turn-processing step SHALL fail, time out, or degrade a turn because of elapsed
wall-clock time between a turn's completion and the player's next action.

#### Scenario: Email-cadence gap between turns
- **WHEN** a player submits their next action an arbitrarily long time after the previous
  turn completed
- **THEN** the action is processed identically to one submitted immediately
