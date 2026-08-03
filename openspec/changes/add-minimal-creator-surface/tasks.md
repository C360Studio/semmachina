## 1. Surface and Protocol Test Harness

- [x] 1.1 Scaffold the strict TypeScript/SvelteKit surface with unit, component, and browser-test
  commands integrated into Taskfile and CI without changing the Go quality gates.
- [x] 1.2 Write failing parser fixtures for exact submit/retrieval requests and for accepted/refused,
  delivery, terminal failure, and narration/resolution server documents, covering additive unknown
  fields, malformed data, unknown discriminators/closed values, and contradictory known fields.
- [x] 1.3 Implement strict request encoders and forward-compatible server parsers that tolerate
  additive fields, validate every known field, return typed protocol failures, and never admit
  partial or contradictory documents into application state.

## 2. Secure Server Projection Adapter

- [x] 2.1 Write RED startup/transport tests for fixed endpoint selection, redirect refusal, normal TLS
  validation, loopback/Unix-local, enforced surface-only network policy, or authenticated-proxy
  posture; prove bare RFC1918/private classification and browser endpoint override are refused.
- [ ] 2.2 Add deployment acceptance that proves the browser-facing network and an untrusted sibling
  workload cannot directly connect to or read the graph endpoint; document that startup checks
  support but cannot replace firewall/network-policy/proxy enforcement.
- [x] 2.3 Write RED principal/scope tests proving production default-deny/no dial before Group 3,
  exact canonical component checks (`world1` never admits `world10`), and whole-response failure on
  any out-of-scope entity or relationship.
- [x] 2.4 Write RED raw-boundary tests proving full validation precedes DTO construction; raw bodies,
  upstream error details, unknown fields/triples, and secret values are never logged, cached,
  returned, or embedded; snake_case/corrected forms normalize and conflicting dual forms fail.
- [x] 2.5 Write RED capacity/topology tests for 0..999 success, 1000
  `projection_capacity_exceeded`, no `spatialSearch`, at most 999 relationship calls, 999 edges per
  location and 998001 total, and whole-map failure on exceeded, malformed, dangling, or unsafe edges.
- [x] 2.6 Implement the pure authenticated-principal adapter with fixed transport, exact-ID clock
  lookup, bounded exhaustive `entitiesByPrefix`/`relationships` map, complete validation, and closed
  DTO normalization; leave the production principal provider default-deny until Group 3.
- [x] 2.7 Track the five focused SemStreams issues: auth/scope
  [#882](https://github.com/C360Studio/semstreams/issues/882), selection minimization
  [#883](https://github.com/C360Studio/semstreams/issues/883), prefix pagination
  [#884](https://github.com/C360Studio/semstreams/issues/884), spatial scope/page
  [#885](https://github.com/C360Studio/semstreams/issues/885), and relationship schema/runtime
  [#886](https://github.com/C360Studio/semstreams/issues/886). Record starter-safe local constraints
  separately from public/scale blockers; add no direct NATS, local query/index, silent filter, or
  unrestricted proxy workaround.
- [x] 2.8 Install adapter-node configuration and projection runtime before listen, keep the lazy
  chunk registry process-stable, default-deny the projection route, explicitly refuse raw WebSocket
  upgrades until Group 3, and pass the production-server smoke.

## 3. Same-Origin Player WebSocket Bridge

- [x] 3.1 Write RED HTTPS authentication tests proving missing/invalid creator credentials, including
  the upstream player Bearer, receive one refusal, mint no session, and cause no upstream dial;
  prove valid separate authentication mints one bounded session.
- [x] 3.2 Write RED lifecycle tests proving successful rotation invalidates the prior session and
  expiry/logout invalidate the current session for both HTTP projection and WebSocket upgrade paths.
- [x] 3.3 Write failing upgrade tests for missing/mismatched opaque session, exact Host, exact Origin,
  session-bound CSRF proof, immutable deployment mapping, and fixed upstream endpoint; assert no
  upstream dial occurs on refusal.
- [x] 3.4 Write RED credential tests proving the upstream Bearer never appears browser-side and the
  creator credential appears only in its HTTPS login request, never in responses, browser storage,
  errors, or logs; prove browser input cannot override session player/world/graph scope.
- [x] 3.5 Write RED raw-relay tests proving bounded valid text bytes are identical in both directions
  and the bridge adds no protocol document, sequencing, replay, adjudication, or correlation.
- [x] 3.6 Write RED resource tests for binary/oversized frames, socket caps, per-direction queue
  bounds, backpressure, slow peers, handshake/idle/liveness deadlines, close propagation, and
  cleanup after every close path.
- [x] 3.7 Extend the existing pre-listen adapter-node bootstrap with separate HTTPS creator
  authentication, bounded opaque session mint/rotation/logout/expiry, the fixed raw upgrade route,
  exact upgrade checks, immutable upstream mapping, secret redaction, bounded relay, and liveness.

## 4. Player Session State Machine

- [x] 4.1 Write failing state-transition tests for connect, authenticate, submit, accepted/refused,
  waiting, pre-submit latest-terminal watermark, terminal delivery, malformed message, disconnect,
  reconnect, exact-ID retrieval, evidence-only latest/delivery, and user-authorized exact replay.
- [x] 4.2 Add RED cases proving an uncertain action is never automatically resubmitted, a second
  action is disabled while one is pending, and recovered live/retrieved terminal results coalesce.
  Include: a previous terminal exists; a new send disconnects before acceptance; `latest` returns
  the previous identity while the new turn runs; the old result is not rendered or associated; and
  only accepted replay IDs permit the later intended terminal to converge exactly once.
- [x] 4.3 Add a RED multi-connection case: another device for the same player acts after the
  watermark; this surface loses its send response or receives an active-turn refusal; the other
  terminal changes `latest` or arrives by player-scoped delivery; neither the result nor
  `active_turn_id` binds to this intent; only explicit replay acceptance supplies correlating IDs.
- [x] 4.4 Add RED recovery-replay cases proving the UI explains idempotent convergence versus
  submit-now authorization, sends the retained text/key only after a user action, never edits them,
  and keeps a typed replay refusal unresolved.
- [x] 4.5 Implement the pure client state machine and same-origin WebSocket client using one
  idempotency key per user intent, a pre-submit watermark used only as activity evidence, exact-ID
  recovery when known, and user-authorized replay when intended IDs are unknown.

## 5. Resolution and Orientation Projections

- [x] 5.1 Write failing resolution tests for rolled, no-roll, failed, malformed, duplicate, and
  conflicting terminal results, including exact delivered plausibility/risk/consequence/band,
  modifier, roll, and narration evidence.
- [x] 5.2 Implement a lossless resolution/narration view-model projection with no client arithmetic,
  band lookup, adjudication, object-store fetch, or prose inference.
- [x] 5.3 Write failing layout tests for query-order independence, directed edges, authored-only,
  topology-only, mixed anchored/unpositioned, and disconnected location graphs.
- [x] 5.4 Implement the pure deterministic labelled schematic layout while preserving every authored
  coordinate and performing no graph or map-artifact write.
- [x] 5.5 Write failing clock tests for configured, `not_configured`, missing, ambiguous, malformed,
  out-of-scope, and unchanged-across-wall-time facts.
- [x] 5.6 Implement the typed clock projection with no timer, interpolation, vocabulary, policy,
  deadline, or mutation behavior.
  - Verification passed 201 focused projection/parser tests and all 407 web unit tests; strict
    check, lint, build, and diff gates were green.
  - Formal architect, Svelte, and backend reviews APPROVED the projection boundaries and
    implementation.

## 6. Accessible Svelte Components

- [ ] 6.1 Write failing component tests for labelled action input, keyboard submission, busy/disabled
  state, typed refusal focus, reconnect status, and non-repeating live-region announcements.
- [ ] 6.2 Implement the action/session shell and narration/resolution card using Svelte 5 runes and
  semantic headings/definition lists, with no transition logic inside presentation components.
- [ ] 6.3 Write failing accessibility tests that require equivalent labelled topology for the visual
  map, non-color-only errors, clock status text, and usable keyboard/focus order.
- [ ] 6.4 Implement the map and clock widgets, including an explicit schematic label whenever fallback
  positions are shown and no world-switching control.

## 7. Acceptance

- [ ] 7.1 Add a deterministic mock acceptance journey: authenticate through the same-origin bridge,
  submit one action, observe typed progress, receive narration/resolution, project authored and
  schematic places, and display `not_configured` clock state.
- [ ] 7.2 Add disconnect acceptance that proves no automatic resubmission, no correlation from
  player-scoped latest/delivery without intended IDs, explicit replay authorization when needed,
  and exactly one terminal card after exact-ID delivery/retrieval convergence.
- [ ] 7.3 Run the real starter-stack journey through the bounded bridge, existing upstream WebSocket,
  and SemStreams beta.159 GraphQL surface, with no browser-visible Bearer or direct graph/NATS access.

## 8. Reviews, Documentation, and Quality Gates

- [ ] 8.1 Obtain architecture review for projection-only ownership, byte-transparent bounded bridge,
  unchanged engine transport, one-world scope, graph allowlist, clock boundary, and upstream asks.
- [ ] 8.2 Obtain backend/security review for opaque sessions, exact upgrade checks, credential
  confinement/redaction, immutable deployment scope, bridge bounds/liveness, graph prefix
  derivation, operation allowlist, and fail-closed errors.
- [ ] 8.3 Obtain Svelte/TypeScript review for strict requests, additive-compatible server parsing,
  state-machine correctness, accessibility, Svelte 5 usage, reconnect, and terminal-result dedupe.
- [ ] 8.4 Document local surface startup, TLS/proxy and same-origin session/bridge configuration,
  instance/GraphQL configuration, authored-versus-schematic map behavior, clock states, one-world
  deployment, and secret-safe troubleshooting.
- [ ] 8.5 Run TypeScript unit/component/browser tests, Node bridge security/resource tests, Go
  unit/integration/race tests, mock and real-stack acceptance, lint, build/cross-compile, strict
  OpenSpec, and diff/line-length gates.
- [ ] 8.6 Archive only after every retained normative scenario and review gate passes, with any
  unresolved SemStreams graph blocker linked rather than bypassed.
