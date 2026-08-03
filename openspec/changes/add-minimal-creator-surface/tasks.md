## 1. Surface and Protocol Test Harness

- [ ] 1.1 Scaffold the strict TypeScript/SvelteKit surface with unit, component, and browser-test
  commands integrated into Taskfile and CI without changing the Go quality gates.
- [ ] 1.2 Write failing parser fixtures for every accepted/refused, retrieval, delivery, terminal
  failure, malformed discriminator, invalid closed value, and narration/resolution document used by
  the existing `player/v1` contract.
- [ ] 1.3 Implement closed TypeScript wire parsers that return typed protocol failures and never
  admit partial documents into application state.

## 2. Secure Server Projection Adapter

- [ ] 2.1 Write failing route/adapter tests proving active organization/world/template prefixes are
  server-derived and browser-supplied prefixes, entity IDs, credentials, and GraphQL documents are
  refused before any upstream request.
- [ ] 2.2 Write failing upstream-response tests for out-of-prefix entities, relationship endpoints,
  malformed typed facts, query errors, and over-broad returned data.
- [ ] 2.3 Implement fixed server-only adapters for beta.159 `entity`, `entitiesByPrefix`,
  `relationships`, and `spatialSearch`, returning only closed place and clock view models.
- [ ] 2.4 If upstream authentication or query behavior cannot enforce the specified boundary, file a
  focused SemStreams issue with a reproducer and record the blocker; do not add direct NATS access,
  a local query service, or an unrestricted proxy.

## 3. Player Session State Machine

- [ ] 3.1 Write failing state-transition tests for connect, authenticate, submit, accepted/refused,
  waiting, pre-submit latest-terminal watermark, terminal delivery, malformed message, disconnect,
  reconnect, exact-ID retrieval, evidence-only latest/delivery, and user-authorized exact replay.
- [ ] 3.2 Add RED cases proving an uncertain action is never automatically resubmitted, a second
  action is disabled while one is pending, and recovered live/retrieved terminal results coalesce.
  Include: a previous terminal exists; a new send disconnects before acceptance; `latest` returns
  the previous identity while the new turn runs; the old result is not rendered or associated; and
  only accepted replay IDs permit the later intended terminal to converge exactly once.
- [ ] 3.3 Add a RED multi-connection case: another device for the same player acts after the
  watermark; this surface loses its send response or receives an active-turn refusal; the other
  terminal changes `latest` or arrives by player-scoped delivery; neither the result nor
  `active_turn_id` binds to this intent; only explicit replay acceptance supplies correlating IDs.
- [ ] 3.4 Add RED recovery-replay cases proving the UI explains idempotent convergence versus
  submit-now authorization, sends the retained text/key only after a user action, never edits them,
  and keeps a typed replay refusal unresolved.
- [ ] 3.5 Implement the pure client state machine and WebSocket adapter using one idempotency key per
  user intent, a mandatory pre-submit latest-terminal watermark used only as activity evidence,
  exact-ID recovery when known, and explicit user-authorized replay when intended IDs are unknown.

## 4. Resolution and Orientation Projections

- [ ] 4.1 Write failing resolution tests for rolled, no-roll, failed, malformed, duplicate, and
  conflicting terminal results, including exact delivered plausibility/risk/consequence/band,
  modifier, roll, and narration evidence.
- [ ] 4.2 Implement a lossless resolution/narration view-model projection with no client arithmetic,
  band lookup, adjudication, object-store fetch, or prose inference.
- [ ] 4.3 Write failing layout tests for query-order independence, directed edges, authored-only,
  topology-only, mixed anchored/unpositioned, and disconnected location graphs.
- [ ] 4.4 Implement the pure deterministic labelled schematic layout while preserving every authored
  coordinate and performing no graph or map-artifact write.
- [ ] 4.5 Write failing clock tests for configured, `not_configured`, missing, ambiguous, malformed,
  out-of-scope, and unchanged-across-wall-time facts.
- [ ] 4.6 Implement the typed clock projection with no timer, interpolation, vocabulary, policy,
  deadline, or mutation behavior.

## 5. Accessible Svelte Components

- [ ] 5.1 Write failing component tests for labelled action input, keyboard submission, busy/disabled
  state, typed refusal focus, reconnect status, and non-repeating live-region announcements.
- [ ] 5.2 Implement the action/session shell and narration/resolution card using Svelte 5 runes and
  semantic headings/definition lists, with no transition logic inside presentation components.
- [ ] 5.3 Write failing accessibility tests that require equivalent labelled topology for the visual
  map, non-color-only errors, clock status text, and usable keyboard/focus order.
- [ ] 5.4 Implement the map and clock widgets, including an explicit schematic label whenever fallback
  positions are shown and no world-switching control.

## 6. Acceptance

- [ ] 6.1 Add a deterministic mock acceptance journey: authenticate, submit one action, observe typed
  progress, receive narration/resolution, project authored and schematic places, and display
  `not_configured` clock state.
- [ ] 6.2 Add disconnect acceptance that proves no automatic resubmission, no correlation from
  player-scoped latest/delivery without intended IDs, explicit replay authorization when needed,
  and exactly one terminal card after exact-ID delivery/retrieval convergence.
- [ ] 6.3 Run the real starter-stack journey through the existing WebSocket and SemStreams beta.159
  GraphQL surface, with server-derived scope and no direct browser graph/NATS access.

## 7. Reviews, Documentation, and Quality Gates

- [ ] 7.1 Obtain architecture review for projection-only ownership, unchanged engine transport,
  one-world scope, graph allowlist, clock boundary, and upstream-ask handling.
- [ ] 7.2 Obtain backend/security review for credential confinement, prefix derivation, request and
  response validation, operation allowlist, bounded inputs, and fail-closed errors.
- [ ] 7.3 Obtain Svelte/TypeScript review for closed wire parsing, state-machine correctness,
  accessibility, Svelte 5 usage, reconnect behavior, and terminal-result dedupe.
- [ ] 7.4 Document local surface startup, instance/GraphQL configuration, authored-versus-schematic
  map behavior, clock states, one-world deployment, and troubleshooting without exposing secrets.
- [ ] 7.5 Run TypeScript unit/component/browser tests, Go unit/integration/race tests, mock and
  real-stack acceptance, lint, build/cross-compile, strict OpenSpec, and diff/line-length gates.
- [ ] 7.6 Archive only after every retained normative scenario and review gate passes, with any
  unresolved SemStreams blocker linked rather than bypassed.
