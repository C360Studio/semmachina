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
- [x] 2.2 Add deployment acceptance that proves the browser-facing network and an untrusted sibling
  workload cannot directly connect to or read the graph endpoint; document that startup checks
  support but cannot replace firewall/network-policy/proxy enforcement.
  - Formal architecture and backend/security reviews APPROVED the exclusive runtime-namespace
    topology and live-read-first proof. The runbook distinguishes enforced isolation from startup,
    CORS, CSP, and browser no-request checks.
  - The real Docker gate passed a per-run nonce from live GraphQL through the actual surface, then
    proved edge and sibling HTTPS positive controls before DNS, TCP, and exact GraphQL POST denial.
    Topology inspection proved private namespaces, zero probe mounts, and no port publication;
    interruption-safe cleanup left no remnants. All four focused contract tests passed.
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

- [x] 6.1 Write failing component tests for labelled action input, keyboard submission, busy/disabled
  state, typed refusal focus, reconnect status, and non-repeating live-region announcements.
- [x] 6.2 Implement the action/session shell and narration/resolution card using Svelte 5 runes and
  semantic headings/definition lists, with no transition logic inside presentation components.
- [x] 6.3 Write failing accessibility tests that require equivalent labelled topology for the visual
  map, non-color-only errors, clock status text, and usable keyboard/focus order.
- [x] 6.4 Implement the map and clock widgets, including an explicit schematic label whenever fallback
  positions are shown and no world-switching control.
  - Verification passed all 19 browser component tests and all 407 unit tests; strict check, lint,
    build, and diff gates were green.
  - Formal architect and Svelte/accessibility reviews APPROVED after three map RED→GREEN
    review-fix cycles.

## 7. Acceptance

- [x] 7.1 Add a deterministic mock acceptance journey: authenticate through the same-origin bridge,
  submit one action, observe typed progress, receive narration/resolution, project authored and
  schematic places, and display `not_configured` clock state.
- [x] 7.2 Add disconnect acceptance that proves no automatic resubmission, no correlation from
  player-scoped latest/delivery without intended IDs, explicit replay authorization when needed,
  and exactly one terminal card after exact-ID delivery/retrieval convergence.
  - The six Playwright journeys passed through the test HTTPS/WSS proxy, including a companion-aware
    terminal, mixed map plus `not_configured` clock, and explicit exact replay with no automatic
    replay and one terminal card.
  - Verification passed 6/6 Playwright, 24/24 component, and 424/424 unit tests; `check` reported
    0 errors/0 warnings, and lint, build, and diff gates were green.
  - Formal architect, backend/security, and Svelte/accessibility reviews APPROVED after review
    fixes.
- [x] 7.3 Run the real starter-stack journey through the bounded bridge, existing upstream WebSocket,
  and SemStreams beta.159 GraphQL surface, with no browser-visible Bearer or direct graph/NATS access.
  - The authorized 2026-08-03 Gemini 3.5 Flash-Lite run passed one Playwright test in 1.0 minute.
    Its two distinct browser-submitted turns proved `discovery`, terminal-card replacement, Kit's
    exact entity and visible text, and the persisted `request_hint` / `player-hint` / `case-decision`
    route. Browser audit proved same-origin HTTPS/WSS only, no browser authorization header or direct
    GraphQL/NATS request, and no upstream Bearer in storage, DOM, or resource URLs.
  - Both independent diagnostic readiness polls returned HTTP 200. Managed teardown left no surface,
    player, GraphQL, diagnostic, or TLS-proxy listener and no generated binary. See the
    [dated acceptance record](../../../../docs/smoke-results/2026-08-03-bellweather-flash-lite-surface.md).

## 8. Reviews, Documentation, and Quality Gates

- [x] 8.1 Obtain architecture review for projection-only ownership, byte-transparent bounded bridge,
  unchanged engine transport, one-world scope, graph allowlist, clock boundary, and upstream asks.
  - Holistic architecture review APPROVED the projection-only GraphQL BFF, exact one-world scope,
    closed operations and DTOs, byte-transparent bounded bridge, and factual clock boundary.
    SemStreams upstream issues #882–#886 were verified as the retained public/scale follow-ups.
- [x] 8.2 Obtain backend/security review for opaque sessions, exact upgrade checks, credential
  confinement/redaction, immutable deployment scope, bridge bounds/liveness, graph prefix
  derivation, operation allowlist, and fail-closed errors.
  - Final backend/security review APPROVED the closed fixed protocol-failure categories,
    fixed-message early paid poll, and canary nonleak guarantees. The focused runner suite passed
    68/68 tests.
- [x] 8.3 Obtain Svelte/TypeScript review for strict requests, additive-compatible server parsing,
  state-machine correctness, accessibility, Svelte 5 usage, reconnect, and terminal-result dedupe.
  - Svelte/TypeScript review APPROVED strict own-property parser semantics, including null and empty
    values; pre-intent UTF-8 validation and accessibility; reconnect and terminal dedupe; non-dumping
    browser audit; and cleanup-owned build behavior.
  - Verification passed check with 0 errors and 0 warnings, 503 unit tests, 26 component tests,
    six deterministic browser journeys, and lint.
- [x] 8.4 Document local surface startup, TLS/proxy and same-origin session/bridge configuration,
  instance/GraphQL configuration, authored-versus-schematic map behavior, clock states, one-world
  deployment, and secret-safe troubleshooting.
  - The [creator-surface operations runbook](../../../../docs/runbooks/creator-surface.md) covers the
    action-free preflight, paid Flash-Lite acceptance, immutable deployment scope, configuration
    contracts, map/clock truth boundaries, failure handling, and secret-safe evidence retention.
- [x] 8.5 Run TypeScript unit/component/browser tests, Node bridge security/resource tests, Go
  unit/integration/race tests, mock and real-stack acceptance, lint, build/cross-compile, strict
  OpenSpec, and diff/line-length gates.
  - Full `task default` passed lint, revive, frontend check, build and Linux cross-compile, the real
    Docker surface-isolation gate, and strict OpenSpec validation (15/15). Go `-race` passed 3,200
    tests across 32 packages with zero skips; frontend passed 504 unit, 26 component, and six
    deterministic browser tests.
  - Real-stack evidence combines the action-free preflight with the already recorded
    [2026-08-03 paid acceptance](../../../../docs/smoke-results/2026-08-03-bellweather-flash-lite-surface.md).
    No new paid rerun was performed for this gate.
- [x] 8.6 Archive only after every retained normative scenario and review gate passes, with any
  unresolved SemStreams graph blocker linked rather than bypassed.
  - Archive readiness confirmed: all retained scenarios, formal reviews, and full quality gates
    pass; real acceptance evidence is linked; and upstream SemStreams blockers remain explicitly
    linked as follow-ups with no local bypass.
