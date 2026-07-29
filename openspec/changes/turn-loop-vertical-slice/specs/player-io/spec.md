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

### Requirement: A turn's result is delivered to exactly the player it belongs to
The egress path SHALL deliver a turn's result to the player that turn belongs to and to no
other connected player. Delivery SHALL resolve the player's current connection from
`player_id` at delivery time rather than from a connection identifier captured at
submission.

Broadcast egress is a disclosure defect, not a performance one: a component that fans every
message out to all connected sockets would satisfy "the result was delivered" while showing
one player's narration to everyone in the campaign. `ChannelBinding.ReplyTo` is a DURABLE
address where the adapter has one (an email address, a chat channel); for a WebSocket it is
not, because a connection identifier is invalid after reconnect.

#### Scenario: One player's narration does not reach another
- **WHEN** two players are connected and a turn belonging to the first resolves
- **THEN** the first player receives the result and the second receives nothing

#### Scenario: Delivery survives a reconnect
- **WHEN** a player reconnects on a new connection before their in-flight turn resolves
- **THEN** the result is delivered to the new connection, resolved from `player_id`

### Requirement: Canonical terminal result and retrieval
A turn's result SHALL be durably stored independent of any connection, and a player SHALL
be able to retrieve it by `action_id` or `turn_id`, plus their most recent TERMINAL result.
A successful result comprises the narration prose reference plus a resolution summary
(verdict class, roll values, outcome band); a failed turn's result comprises its recorded
failure reason.

Terminal rather than completed, deliberately. A failed turn has no narration by design, and
a retrieval surface that only answers for completed turns leaves the player who most needs
an answer — the one whose turn died — with silence indistinguishable from a turn still
running. The ledger already archives failed turns, so the data exists.

#### Scenario: Result survives disconnect
- **WHEN** a player disconnects after submitting an action and reconnects after the turn
  completes
- **THEN** the reconnected player receives the completed turn's narration and resolution
  summary

#### Scenario: A failed turn still yields a player-visible result
- **WHEN** a player's turn ends in the `failed` phase with a recorded reason
- **THEN** retrieving their most recent terminal result returns that failure rather than
  the previous successful turn or an empty answer

### Requirement: No interactive-pacing assumptions
No turn-processing step SHALL fail, time out, or degrade a turn because of elapsed
wall-clock time between a turn's completion and the player's next action.

#### Scenario: Email-cadence gap between turns
- **WHEN** a player submits their next action an arbitrarily long time after the previous
  turn completed
- **THEN** the action is processed identically to one submitted immediately
