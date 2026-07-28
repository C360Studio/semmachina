# player-io — Delta Spec

## ADDED Requirements

### Requirement: Canonical transport-neutral action payload
Every player action SHALL enter the system as a canonical `PlayerAction` payload carrying
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

### Requirement: Canonical result egress and retrieval
A turn's result SHALL be durably stored independent of any connection, comprising the
narration prose reference plus a resolution summary (verdict class, roll values, outcome
band). The egress adapter SHALL deliver it to the player's channel binding, and a
reconnecting player SHALL be able to retrieve the most recent completed turn's result.

#### Scenario: Result survives disconnect
- **WHEN** a player disconnects after submitting an action and reconnects after the turn
  completes
- **THEN** the reconnected player receives the completed turn's narration and resolution
  summary

### Requirement: No interactive-pacing assumptions
No turn-processing step SHALL fail, time out, or degrade a turn because of elapsed
wall-clock time between a turn's completion and the player's next action.

#### Scenario: Email-cadence gap between turns
- **WHEN** a player submits their next action an arbitrarily long time after the previous
  turn completed
- **THEN** the action is processed identically to one submitted immediately
