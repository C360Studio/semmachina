## Purpose

Defines the smallest honest player session: authenticate, submit an action, observe typed state,
and recover its durable result without moving engine authority into the client.

## ADDED Requirements

### Requirement: The creator surface uses the existing authenticated player protocol
The surface SHALL reach the existing authenticated `player/v1` WebSocket through its same-origin
bridge and SHALL use the current submission, delivery, and retrieval documents without extending or
translating the engine protocol. A submission SHALL contain exactly the required `protocol`, `text`, and
`idempotency_key` fields. Only `text` and `idempotency_key` are client-owned action data;
`protocol` is the required wire-version discriminator. The server SHALL continue to supply player,
campaign, scene, arrival, action, and channel identity.

#### Scenario: A creator submits an action through the canonical ingress
- **WHEN** an authenticated creator submits non-empty action text from the surface
- **THEN** the client sends one strict `player/v1` request containing `protocol`, `text`, and
  `idempotency_key`, and displays its typed accepted or refused response

#### Scenario: The browser does not claim engine identity
- **WHEN** the surface constructs a submission
- **THEN** only its text and idempotency key are client-owned action data, and it sends no player,
  campaign, scene, action, arrival, or connection identity

#### Scenario: A request extension is refused locally
- **WHEN** client code attempts to add an unknown field to a submit or retrieval request
- **THEN** the strict request encoder refuses it rather than sending an extended request

### Requirement: A bounded same-origin bridge protects the upstream player credential
Before minting or rotating an opaque session, a same-origin HTTPS bootstrap/login endpoint SHALL
authenticate the creator with a separately configured creator credential. The upstream player
Bearer SHALL NOT be accepted or reused as a browser credential, and configuration SHALL
fail closed if the creator credential is absent or aliases that Bearer. Missing or invalid creator
authentication SHALL return one indistinguishable refusal, mint no session, and cause no upstream
dial. A successful authentication SHALL mint a bounded-lifetime opaque server session cookie with
`HttpOnly`, `Secure`, and `SameSite=Strict`. Rotation SHALL invalidate the prior session; explicit
logout and expiry SHALL invalidate the current session before any later HTTP or upgrade request.

The browser SHALL connect to a fixed same-origin WebSocket route through that session. The raw
upgrade handler SHALL require exact configured `Host` and `Origin` values and an exact
session-bound CSRF proof before opening a socket. A session SHALL map immutably to one deployment's
fixed upstream endpoint, Bearer credential, player identity, and graph/world scope. The Bearer SHALL
never be sent to browser code, placed in a browser-readable cookie, accepted from browser input, or
written to application logs.

Production SHALL use an adapter-node custom Node server that owns the raw WebSocket upgrade. The
bridge SHALL accept text messages only; enforce bounded complete-message payload size,
per-direction buffering, per-session and process socket counts, and liveness/close deadlines; and
apply backpressure or close rather than buffer without limit. After upgrade it SHALL relay the
payload bytes of each complete `player/v1` WebSocket text message unchanged in both directions to
the fixed upstream socket.
Fragmentation, masking, compression, and control frames SHALL terminate independently on each hop
and SHALL NOT be treated as `player/v1` document bytes. For the pinned current protocol version, the
local bridge SHALL enforce a 262,144-byte complete WebSocket message payload bound on its upstream
hop. This accommodates the current bounded 16 KiB prose envelope after escaping with deliberate
headroom. A future protocol expansion SHALL require deliberate review before that bound changes.
The bridge SHALL perform no player-protocol sequencing, automatic replay, adjudication, result
correlation, or graph access. This bridge is a SemMachina application security responsibility and
SHALL NOT be deferred as a SemStreams engine ask.

#### Scenario: Browser authentication never exposes the Bearer
- **WHEN** a valid creator opens the same-origin player socket
- **THEN** the opaque server session selects the immutable upstream credential and scope, while no
  browser request, response, cookie readable by script, or application log contains the Bearer

#### Scenario: Creator authentication is independent of the upstream Bearer
- **WHEN** bootstrap/login receives a missing or invalid creator credential, including the upstream
  player Bearer
- **THEN** it returns the same authentication refusal, creates no session, and causes no upstream
  WebSocket or graph dial

#### Scenario: Valid creator authentication mints one bounded session
- **WHEN** bootstrap/login receives the separately configured valid creator credential with its
  exact same-origin Host, Origin, and CSRF proof
- **THEN** it mints or rotates one opaque bounded-lifetime session without exposing either
  credential to browser-readable state

#### Scenario: Session lifecycle invalidates old authority
- **WHEN** a session expires, the creator logs out, or successful authentication rotates it
- **THEN** the expired, logged-out, or prior session is refused for HTTP projection and WebSocket
  upgrade requests and cannot cause an upstream dial

#### Scenario: Upgrade authority is exact and fail-closed
- **WHEN** the upgrade has a missing or mismatched session, Host, Origin, CSRF proof, deployment
  mapping, or fixed upstream endpoint
- **THEN** the server refuses the upgrade before opening an upstream socket

#### Scenario: The bridge preserves the player protocol
- **WHEN** a bounded valid text message crosses either direction of an established bridge
- **THEN** the other socket receives the same complete `player/v1` payload bytes, while hop-local
  framing terminates independently and the bridge creates no protocol document, replay, sequence,
  or correlation decision

#### Scenario: The pinned message payload bound is explicit
- **WHEN** the current protocol version carries prose within its bounded 16 KiB envelope
- **THEN** the bridge applies its 262,144-byte complete WebSocket message payload bound on the
  upstream hop, and any protocol expansion that would exceed the reviewed envelope requires a
  deliberate bound review

#### Scenario: Resource limits close abusive or dead sockets
- **WHEN** a client sends a binary or oversized message, exceeds a socket/buffer bound, ignores
  backpressure, or fails the liveness contract
- **THEN** the bridge closes the affected connection within its bounded policy without exposing the
  Bearer or degrading other sessions into unbounded buffering

### Requirement: Session progress and failures are explicit states
The surface SHALL represent connection, submission, waiting, terminal delivery, typed refusal,
malformed-message failure, and reconnecting as distinct client states. It SHALL transition from
validated protocol discriminators and connection events rather than matching human-readable text.
An error SHALL preserve enough typed context to distinguish a correctable input refusal, a running
turn, a transport interruption, and a malformed or unsupported server document. Request documents
SHALL be exact and strict. Server frames and nested results SHALL tolerate additive unknown fields
while validating every known field. Unknown discriminators, unknown closed values, malformed data,
or contradictory known fields SHALL fail closed.

#### Scenario: An accepted action remains visibly in progress
- **WHEN** the server accepts a submission and no terminal result has arrived
- **THEN** the surface shows a waiting state associated with that submission rather than success,
  failure, or an enabled second submission

#### Scenario: A structured refusal stays structured
- **WHEN** the server refuses a submission with a closed code and optional field
- **THEN** the surface exposes that code and field to the error view without parsing its message

#### Scenario: An unknown wire document fails closed
- **WHEN** the socket receives an unknown discriminator, invalid closed value, malformed document,
  or contradictory known fields
- **THEN** the surface enters a typed protocol-error state and renders no partial result

#### Scenario: An additive server field is compatible
- **WHEN** a valid known server frame or nested result contains an additional unknown field
- **THEN** the parser validates and projects the known contract without rejecting the frame or
  exposing the unknown field as trusted application state

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
