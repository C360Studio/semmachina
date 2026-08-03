## Purpose

Defines the smallest honest player session: authenticate, submit an action, observe typed state,
and recover its durable result without moving engine authority into the client.

## ADDED Requirements

### Requirement: The creator surface uses the existing authenticated player protocol
The surface SHALL connect through the existing authenticated `player/v1` WebSocket and SHALL use
its current submission, delivery, and retrieval documents without extending or translating the
engine protocol. A submission SHALL contain only the action text and a client-owned idempotency key;
the server SHALL continue to supply player, campaign, scene, arrival, action, and channel identity.

#### Scenario: A creator submits an action through the canonical ingress
- **WHEN** an authenticated creator submits non-empty action text from the surface
- **THEN** the client sends one valid `player/v1` submission and displays its typed accepted or
  refused response

#### Scenario: The browser does not claim engine identity
- **WHEN** the surface constructs a submission
- **THEN** it sends no player, campaign, scene, action, arrival, or connection identity as
  client-authored data

### Requirement: Session progress and failures are explicit states
The surface SHALL represent connection, submission, waiting, terminal delivery, typed refusal,
malformed-message failure, and reconnecting as distinct client states. It SHALL transition from
validated protocol discriminators and connection events rather than matching human-readable text.
An error SHALL preserve enough typed context to distinguish a correctable input refusal, a running
turn, a transport interruption, and a malformed or unsupported server document.

#### Scenario: An accepted action remains visibly in progress
- **WHEN** the server accepts a submission and no terminal result has arrived
- **THEN** the surface shows a waiting state associated with that submission rather than success,
  failure, or an enabled second submission

#### Scenario: A structured refusal stays structured
- **WHEN** the server refuses a submission with a closed code and optional field
- **THEN** the surface exposes that code and field to the error view without parsing its message

#### Scenario: An unknown wire document fails closed
- **WHEN** the socket receives an unknown discriminator, invalid closed value, or malformed document
- **THEN** the surface enters a typed protocol-error state and renders no partial result

### Requirement: Reconnect never resubmits an uncertain action automatically
If a connection closes after submission, the surface SHALL retain the action's local correlation
and idempotency state, mark its outcome uncertain, reconnect, and use only the existing
authenticated result-retrieval operations: exact `action_id`, exact `turn_id`, or `latest`. It SHALL
NOT attempt retrieval by idempotency key and SHALL NOT automatically submit the action again.

Before sending a new action, the surface SHALL retrieve and store the current latest terminal
turn/action identity as that intent's pre-submit watermark; failure to establish the watermark
SHALL prevent the send. A canonical response that no terminal result exists SHALL establish an empty
watermark. If the client learned the intended action or turn identity before disconnect, it SHALL
recover by that exact identity. If neither intended canonical identity was learned, both `latest`
and player-scoped delivery SHALL be treated only as evidence that player activity occurred. Neither
an unchanged nor a changed identity SHALL be bound to the uncertain intent. The watermark MAY tell
the UI whether latest terminal activity changed, but SHALL NOT establish correlation.

Exact convergence SHALL require the intended canonical `action_id` or `turn_id`. Within the
unchanged protocol, the surface SHALL offer an explicit user-authorized recovery replay containing
exactly the retained action text and idempotency key; it SHALL NOT edit, regenerate, or send that
replay automatically. Before authorization, the UI SHALL explain that if the original submission
was accepted, replay converges idempotently and returns the same canonical IDs, while if it was not
accepted, authorization may submit the action now. An accepted replay SHALL supply the intended
canonical IDs, after which delivery or retrieval may correlate only an exact match. A replay refusal
SHALL remain a typed refusal; an `active_turn_id` carried by that refusal SHALL NOT be treated as the
intended turn identity. Delivery and retrieval remain subject to terminal-result identity/content
deduplication.

#### Scenario: Disconnect after send does not duplicate intent
- **WHEN** the socket closes after the client sends an action but before it receives acceptance
- **THEN** reconnect performs no automatic submission and the surface reports that acceptance is
  uncertain while it attempts canonical result recovery

#### Scenario: A completed turn survives reconnect
- **WHEN** the surface learned the intended canonical identity before disconnect and that turn
  resolves while its creator is disconnected
- **THEN** the reconnected surface obtains the exact authorized terminal result through delivery or
  retrieval and presents it once

#### Scenario: Latest does not bind the previous terminal to an uncertain action
- **WHEN** a previous terminal result exists, the surface records it as the pre-submit watermark,
  sends a new action, disconnects before learning acceptance or canonical IDs, and `latest` still
  returns the previous result while the new turn runs
- **THEN** the surface keeps the new action uncertain, does not render or associate the previous
  result with it, and requires explicit replay acceptance to learn the intended canonical identity

#### Scenario: Another connection cannot satisfy this surface's uncertain intent
- **WHEN** another connection for the same player submits an action after this surface records its
  watermark, this surface loses its own submission response or receives an active-turn refusal, and
  the other action changes `latest` or produces a player-scoped delivery
- **THEN** the changed result is evidence only and is not associated with this surface's uncertain
  intent; an `active_turn_id` is not adopted, and only a user-authorized replay that is accepted with
  canonical IDs can establish exact correlation

#### Scenario: The creator authorizes an exact recovery replay
- **WHEN** an action remains uncertain without intended canonical IDs and the creator authorizes
  replay after reading its idempotent-or-submit-now explanation
- **THEN** the surface sends exactly the retained text and idempotency key once, and an accepted
  answer supplies the intended IDs used for exact retrieval or delivery matching

### Requirement: One deployed surface represents one active world
The surface SHALL derive its active world from server deployment configuration and authenticated
session context. It SHALL expose no browser-supplied world namespace, arbitrary entity prefix, or
world-switching control.

#### Scenario: A creator cannot switch graph scope
- **WHEN** a creator opens the deployed surface
- **THEN** every session and projection is scoped to that deployment's one active world and no world
  selector is rendered
