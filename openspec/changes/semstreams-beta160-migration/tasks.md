## 1. Adoption Contract and Architecture

- [x] 1.1 Record the exact beta.160 tag and commit, prove the intended green NATS storage is newly
  provisioned, and stop for separate review if retained state is found
  - The 2026-08-12 workstation operator record resolves beta.160 to upstream commit
    `8403a2218000e45a31c5132fbfe01af42ed04f14` and records the newly created green volume,
    exact NATS `2.14.4` server/container/account identities, and zero preboot streams, KV buckets,
    object stores, consumers, and storage bytes. This closes the approved workstation scope; a
    production deployment requires its own preboot evidence.
- [x] 1.2 Add failing tests for cross-barrier manifest validation, ordered start, reverse stop, and
  immutable barrier membership before implementing the composition-root coordinator
- [x] 1.3 Define one registered upstream `ComponentManager` per activation barrier and obtain
  architect sign-off on the complete component and port manifest
- [x] 1.4 Record the beta.159 application-plus-storage rollback unit and the distinct beta.160 green
  storage identity in the operator evidence template

## 2. Dependency, Ports, Readiness, and Storage

- [x] 2.1 Pin `v1.0.0-beta.160`, update the engine version and world compatibility evidence, and make
  every binary compile without compatibility aliases
- [x] 2.2 Add failing strict-port tests for canonical input/output direction, typed config, stream
  names, subjects, graph query ports, graph mutation requester/provider matching, and rejection of
  legacy shapes
- [x] 2.3 Implement registered factories and managers, then remove direct component lifecycle calls
  and identity-free admission
- [x] 2.4 Add failing readiness tests, replace component-status polling with `GRAPH_STATUS` and
  rule-domain readiness, and prove restart convergence
- [x] 2.5 Add failing exact-storage tests, register the agentic object-store provider under one stable
  `StoreRegistry` instance, and use it for trajectory evidence and content references
- [x] 2.6 Obtain Go reviewer sign-off for architecture compliance, context handling, shutdown,
  error classification, and race safety

## 3. Typed Graph Mutation Foundation

- [x] 3.1 Add table-driven failing tests for typed create, exact reads with KV revision, complete-group
  reconcile, empty-group clearing, `entity_not_found`, and revision mismatch
- [x] 3.2 Add failing uncertainty tests proving `commit_unknown` exact-reads for convergence and is
  never blindly retried or reported as fictional rejection
- [x] 3.3 Replace the raw subject adapter with typed mutation and exact-read clients that require an
  explicit projection contract and group
- [x] 3.4 Encode the reviewed projection inventory and add a gate that fails when a production writer
  has no admitted contract/group
- [x] 3.5 Remove degraded-create and referential-stub behavior; test missing relationship targets as
  absent state at every affected domain boundary
- [x] 3.6 Obtain Go reviewer sign-off for ownership isolation, CAS behavior, uncertainty handling,
  error wrapping, and critical-path coverage

## 4. Production Writer Migration

- [x] 4.1 Migrate turn birth and the `phase`, `case-decision`, `case-progress`, `companion-trigger`,
  `companion-decision`, `verdict`, `roll`, `effects-marker`, `narration`, `knowledge`, `accusation`,
  and `resume` groups with failing behavior tests first
- [x] 4.2 Migrate player `current-pointer` and `resolved-pointer`, campaign `import-marker`, and case
  lifecycle `receipt` groups with duplicate-delivery and restart tests
- [x] 4.3 Migrate companion-bond `hint`, knowledge/revelation births, and effect-owned mutable world
  groups with revision-conflict and uncertain-commit tests
- [x] 4.4 Replace package `replace_owned` with `reconcile_predicates`, generate or validate one
  per-pack contract and declared group set, and reject undeclared/protected predicates
- [x] 4.5 Prove every desired group is complete, an empty group clears, and no writer can mutate a
  predicate outside its contract
- [x] 4.6 Obtain Go reviewer sign-off for every production writer and the complete integration suite
  - Formal Go approval and all six zero-skip lanes are complete; their union records 4,722 outcomes
    across 32 packages.

## 5. GraphQL and Creator Surface

- [x] 5.1 Add failing adapter tests for `ExactEntity`, `EntityPage`, empty and multi-page results,
  opaque cursor forwarding, repeated cursors, malformed pages, aggregate caps, and later-page scope
  violations
- [x] 5.2 Migrate world and clock queries to beta.160 envelopes, reject beta.159 relationship aliases,
  and preserve the closed browser DTO
- [x] 5.3 Add failing component/browser tests for retryable `index_not_ready`, explicit retry,
  focus management, `aria-live` status, and restored controls
- [x] 5.4 Update surface fixtures, the real-stack probe, and deployment-isolation proof to canonical
  beta.160 GraphQL shapes
- [x] 5.5 Obtain Svelte reviewer sign-off for strict TypeScript, fail-closed parsing, accessibility,
  retry UX, and no browser authority expansion

## 6. Fresh-State Acceptance and Documentation

- [x] 6.1 Run strict OpenSpec, lint, builds, unit tests, race tests, and the zero-skip full Go suite
  - Green: strict OpenSpec 19/19, lint, build, unit/race 3,025 outcomes, integration/race 1,394,
    pipeline/race 82, recovery/race 59, acceptance 134, and E2E 28. The six-lane union contains
    4,722 outcomes across 32 packages with zero skips.
- [x] 6.2 Run fresh-NATS integration and restart proofs for world import, graph readiness, rule
  readiness, agentic trajectory evidence, storage resolution, turn completion, resume, and ledger
  - The complete E2E lane passed against fresh exact NATS `2.14.4` in 508.657 seconds, including
    crash/resume and digest/size proof that logical `objectstore` resolved to the configured physical
    `ContentBucket` for trajectory evidence.
- [x] 6.3 Run frontend unit/component/browser/build gates, deployment isolation, and the action-free
  real-stack preflight against canonical beta.160 GraphQL
  - Green: 532 server-unit tests, 30 browser-component tests, 6 deterministic UI journeys, strict
    check, lint, build, and deployment-isolation contract checks.
  - The action-free real-stack Playwright preflight passed 1 test in 1.2 seconds and recorded all six
    expected checkpoints; temporary NATS and Ryuk resources were cleaned up.
- [x] 6.4 Search active code and docs for retired subjects, ports, component status, stub semantics,
  `replace_owned`, and beta.159 GraphQL aliases; require zero unjustified hits
  - The deep semantic re-audit is clean. Remaining hits are explicit retired-alias rejection
    tests/code/spec, dated beta.159 smoke evidence, rollback/migration guidance, a compatibility
    negative fixture, and explicit beta.160 no-stub statements.
- [x] 6.5 Update active specs, world-authoring guidance, configuration examples, manifests, README,
  runbooks, and the now-partially-unblocked mystery companion change
- [x] 6.6 Record exact tag/commit, green storage identity, config version, test results, reviewer
  approvals, reverse-stop/teardown proof, and rollback posture
  - The dated workstation evidence records immutable blue/green application, broker, account,
    volume, configuration, and content-bucket identities; preboot freshness; first boot and restart
    convergence; graceful application-before-broker teardown; whole-unit beta.159 rollback; retained
    volumes; test results; and Go/Svelte reviewer approvals. It does not authorize or evidence a
    future production cutover.
- [ ] 6.7 After separate operator authorization, run paid acceptance with 30–60 second authoritative
  state polling and abort immediately when a wedge is proved
- [ ] 6.8 Obtain technical-writer sign-off and archive this change only after all implementation and
  reviewer gates pass
