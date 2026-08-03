## Context

Stage 3 leaves SemMachina with an authenticated `player/v1` WebSocket, canonical terminal
deliveries, selected world experiences, and a place graph with directed topology and optional point
geometry. SemStreams `v1.0.0-beta.159` exposes GraphQL reads for entities, prefixes,
relationships, and spatial search. The new surface must compose those contracts without becoming a
second engine, query service, or source of world truth. See `proposal.md` for the product need and
the four delta specs for observable behavior.

The MVP remains one active world and experience per process/broker. That makes the deployed server,
not the browser, the authority for organization, world namespace, template, player session, and
graph prefix.

## Goals / Non-Goals

**Goals:**

- Provide one accessible SvelteKit journey from authenticated action submission to narration and a
  legible resolution card.
- Make connection, progress, refusal, protocol failure, and reconnect recovery explicit client
  states with no uncertain automatic resubmission.
- Expose least-authority server projections for the active world's public place graph and optional
  configured clock fact.
- Preserve authored geometry and produce a stable labelled schematic when geometry is absent.
- Prove the surface first against protocol/query fixtures and then against the real starter stack.

**Non-Goals:**

- Any client adjudication, sequencing, graph mutation, clock behavior, authoring editor, companion
  orchestration, multi-world support, direct NATS use, or new query service.
- A progressive-discovery map, regional artwork/media pack, route finder, clock policy, or deadline
  UI; those remain later roadmap work.
- Changes to the existing player protocol, engine payloads, graph vocabulary, or world package.

## Decisions

### D1 — SvelteKit is a projection and adapter boundary

The browser is a strict TypeScript/Svelte 5 presentation client. SvelteKit server routes own graph
credentials, deployment configuration, upstream GraphQL calls, scope validation, and conversion to
closed view models. No route accepts a raw GraphQL document or arbitrary entity prefix, and the
browser receives no upstream credentials or general graph record.

The deployable uses adapter-node with a custom Node server because SvelteKit request handlers do not
own the raw WebSocket upgrade required by the player bridge. The custom server is part of the
SemMachina application boundary and composes SvelteKit HTTP handling with one fixed upgrade route;
it is not a new SemStreams service.

This keeps secrets and scope enforcement server-side while avoiding a new Go/query service. A
browser-direct GraphQL client was rejected because it would expose upstream authority and require
the browser to police its own world prefix. A general proxy was rejected because an allowlist of
operation names is not enough when arbitrary queries and variables remain expressible.

### D2 — Player traffic remains on the unchanged authenticated WebSocket

The browser connects only to a fixed same-origin bridge route. Operator configuration supplies a
creator login credential separately from the upstream player Bearer; startup refuses a missing
creator credential or one that aliases the Bearer. An HTTPS bootstrap/login endpoint verifies exact
Host, Origin, and a server-issued pre-authentication CSRF proof before authenticating that creator
credential. Missing and invalid credentials, including presentation of the upstream Bearer, receive
one indistinguishable refusal, create no session, and dial nothing upstream.

Successful authentication mints or rotates an opaque `HttpOnly`, `Secure`, `SameSite=Strict`
server session with an explicit lifetime and a session-bound CSRF proof. Rotation invalidates the
old ID; logout and expiry remove the mapping before later HTTP or upgrade authorization. The raw
upgrade handler checks the exact configured Host, Origin, CSRF proof, and live session before
dialing upstream. The session maps immutably to one deployment's upstream Bearer, fixed player
WebSocket endpoint, player identity, and graph/world scope. Browser input cannot override any part
of the mapping. Both credentials are redacted from errors and structured logs; the Bearer never
appears in HTML, JavaScript, browser-readable storage, or client-visible cookies.

The adapter-node custom server owns both the SvelteKit handler and raw upgrade path. The bridge
admits text frames only, with explicit frame, per-direction queue, per-session/process socket,
backpressure, handshake, idle/liveness, and close bounds. Once connected, it relays `player/v1`
bytes unchanged in both directions. It does not parse action/result semantics, sequence turns,
replay messages, or correlate results. This BFF is local SemMachina security work, not an upstream
auth gap to file against SemStreams.

Actions, accepted/refused answers, terminal deliveries, and result retrieval therefore remain on
unchanged `player/v1`. Outgoing requests are exact: submit requires only `protocol`, `text`, and
`idempotency_key`, with only text/key being client-owned action data. Incoming server frames use a
forward-compatible TypeScript parser: it tolerates additive unknown fields in known frames/results
but validates every known field and rejects unknown discriminators, unknown closed values,
malformed structures, or contradictory known fields. A pure client state machine consumes parsed
events plus socket events; Svelte components render state but do not decide transitions.

The client creates one idempotency key per user intent and retains it across connection loss, but
does not mistake that key for a retrieval key: `player/v1` retrieves only by canonical `action_id`,
canonical `turn_id`, or `latest`. Before sending, the state machine retrieves the current latest
terminal identity and records it as the intent's pre-submit watermark. If that watermark cannot be
established, the send remains disabled because a later `latest` answer could not be distinguished
from an older turn. A canonical no-terminal answer establishes an explicit empty watermark, so a
creator's first action does not require a prior result.

If acceptance supplied an action or turn ID before disconnect, reconnect retrieves that exact ID.
If disconnect happened before either intended canonical ID was learned, both `latest` and
player-scoped delivery are evidence only. The watermark can say whether player activity changed,
but neither an unchanged nor a new identity is correlated: another connection for the same player
may have created the new turn, so one-active-turn admission does not prove which client intent owns
it. An `active_turn_id` in a typed refusal is likewise the blocking turn, not the intended turn.

Exact convergence therefore requires intended IDs learned from an accepted submission response.
Reconnect never automatically sends the action again. Instead, the UI offers a user-authorized
recovery replay locked to the retained text and idempotency key, and explains both outcomes: an
originally accepted action converges idempotently to the same IDs; an unaccepted action may be
submitted now. Once an accepted replay supplies those IDs, exact-ID retrieval and matching delivery
can resolve the intent. A refusal remains typed and leaves correlation unresolved. Automatic replay
was rejected because transport uncertainty must stay visible; binding a changed `latest` or a
player-scoped delivery was rejected because same-player multi-connection activity makes both
ambiguous.

### D3 — Resolution rendering is a lossless view over delivery

The terminal view model copies the canonical turn/action identity, terminal phase, delivered
verdict scalars, band, roll, modifiers, and narration. It performs no band lookup, arithmetic,
consequence selection, or prose generation. A no-roll verdict produces an explicit no-roll view;
missing or invalid required data produces a protocol error rather than placeholders.

Terminal entries are keyed by the canonical turn/action pair. Delivery and retrieval with
equivalent known canonical fields coalesce even when one carries additive unknown fields;
conflicting known content for one key is an error. This closes the common reconnect failure where
the same result is appended once from live delivery and once from recovery without turning a future
additive field into a compatibility break.

### D4 — The graph adapter has a closed operation and identity envelope

The adapter may call only upstream beta.159 `entity`, `entitiesByPrefix`, `relationships`, and
`spatialSearch`. Each server route builds a fixed query and admits only bounded projection inputs
such as a viewport/radius where required. Organization, world namespace, template, entity prefix,
and any exact clock entity are server-derived. Requested identifiers and every returned entity or
relationship endpoint are checked against the active prefix before a view model is returned.

The place view model contains only location identity, a player-safe label, optional authored point,
and directed connections. It does not expose arbitrary triples, hidden contents, or current entity
state. If upstream authentication or query behavior cannot support this boundary, the task stops
and files a SemStreams issue. Direct KV/NATS reads, a local graph index, and an unrestricted proxy
are not fallback options.

### D5 — Authored positions win; fallback layout is pure and deterministic

Valid authored latitude/longitude pairs are immutable inputs to rendering. Topology-only locations
are placed by a pure schematic layout keyed by stable location identity and directed edges; inputs
are sorted before layout so GraphQL return order cannot move nodes. Mixed graphs anchor authored
points and apply fallback only to unpositioned nodes. Disconnected nodes receive stable labelled
positions instead of disappearing.

The layout result is ephemeral presentation state. It is never sent to graph ingest, stored as a
map artifact, or treated as authored geometry. A spatial-only layout was rejected because distance
does not express traversability; a graph-only layout for all nodes was rejected because it would
override author intent.

### D6 — Clock is an optional typed fact projection, not a clock feature

Optional server configuration identifies one exact in-scope entity/predicate projection plus its
display label and declared unit. The adapter returns a discriminated configured view only when one
valid typed value is present. No configuration returns `not_configured`; bad configured data returns
an error. The client neither selects the fact nor advances it.

No timer updates the readout. Clock vocabulary, policy, ticking, deadlines, and world-time mutation
remain Stage 9 concerns. Using host time or deriving elapsed time was rejected because it would show
a fact the world never recorded.

### D7 — Accessibility is part of the component contract

Session status uses textual live-region announcements without repeatedly announcing unchanged
progress. The action form has a programmatic label, explicit disabled/busy state, and predictable
focus after refusal or terminal delivery. Resolution data uses semantic headings and definition
lists; the map has a labelled non-visual topology list equivalent to its visual marks; errors never
depend on color alone. Component tests exercise keyboard flow and accessible names.

## Risks / Trade-offs

- **Beta GraphQL authentication or filtering is insufficient** → stop the affected adapter task,
  file a focused SemStreams issue with a reproducer, and keep the gap visible rather than widening
  local authority.
- **The browser cannot safely hold the upstream player Bearer** → terminate TLS and session
  authentication at the bounded same-origin SemMachina bridge; authenticate with a distinct creator
  credential, keep the deployment mapping immutable, and redact both secrets rather than changing
  the upstream protocol.
- **A stale or rotated session retains authority** → give every session a bounded lifetime and
  invalidate its mapping on expiry, logout, and successful rotation before accepting HTTP or raw
  upgrade work.
- **A slow or hostile socket consumes server memory** → enforce text/frame/socket/queue bounds,
  backpressure, exact upgrade checks, and liveness deadlines in the raw Node upgrade path.
- **Another connection changes latest or receives the blocking turn** → treat latest, player-scoped
  delivery, and refusal `active_turn_id` as evidence only until an explicit replay is accepted and
  returns the intended canonical IDs.
- **Reconnect timing yields matching delivery and retrieval together** → after intended IDs are
  known, canonical identity/content dedupe makes arrival order irrelevant and reports conflicts.
- **Schematic layout suggests geography that was not authored** → label it schematic, preserve
  directed edges, anchor authored points, and never persist fallback coordinates.
- **Map projection leaks non-public state** → use a closed location/topology view model and reject
  general triples or entity details.
- **A configured clock disappears or becomes ambiguous** → return a typed error, not host time or
  `not_configured`, so operator drift is visible.
- **One-world deployment limits preview workflows** → retain the proven broker/process boundary;
  multi-world switching waits for instance-scoped identity and persona storage.

## Migration Plan

1. Add the surface behind a development-only entry point and validate all wire/query fixtures.
2. Run mock acceptance against deterministic player and graph fixtures, including disconnect and
   duplicate terminal delivery.
3. Run the real starter-stack journey with the existing instance configuration and WebSocket.
4. Enable the surface for the single-world deployment after security, accessibility, Go/TypeScript,
   and architecture reviews pass.
5. Roll back by removing or disabling the presentation process; engine state and protocols require
   no migration or rollback because the surface performs no writes and changes no contract.
