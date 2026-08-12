# SemStreams beta.160 Migration

## Why

SemStreams `v1.0.0-beta.160` at commit
`8403a2218000e45a31c5132fbfe01af42ed04f14` is an intentional clean break. It removes legacy graph mutation
lanes, referential stubs, permissive ports, identity-free component admission, the component-status
plane, and beta.159 GraphQL response shapes. SemMachina depends on each of those contracts today.

Adoption also requires newly provisioned NATS storage. Treating this as an ordinary dependency bump
would mix incompatible persisted state and conceal ownership, revision, readiness, and query errors
behind local compatibility code.

## What Changes

- Adopt beta.160 on a fresh-state blue/green stack, retaining the beta.159 stack and its storage as
  the rollback unit.
- Compose registered components through one upstream `ComponentManager` per immutable activation
  barrier, coordinated by the SemMachina composition root.
- Replace permissive port declarations and component-status polling with canonical typed ports,
  `GRAPH_STATUS`, and rule-domain readiness.
- Register the agentic object-store provider under logical instance `objectstore`, map it to the
  configured physical content bucket, and use it for every trajectory and content reference.
- Replace raw graph mutation subjects with typed create and reconcile operations, explicit
  projection contracts/groups, exact reads, and revision-aware outcomes.
- Remove referential-stub semantics. A reference to an entity without a birth record remains
  observable as a missing target.
- Adopt beta.160 GraphQL `ExactEntity` and paginated `EntityPage` envelopes, canonical relationship
  fields, bounded traversal, and explicit accessible retry for `index_not_ready`; remove beta.159
  response aliases.
- Prove the migration with failing tests first, fresh-stack integration, restart, token-free E2E,
  strict OpenSpec, and the full repository gate.

## Capabilities

### Added Capabilities

- `runtime-composition`: define fresh-state adoption, activation barriers, typed ports, readiness,
  and exact storage registration.
- `graph-projection-writes`: define projection ownership, typed mutation behavior, revisions, and
  missing-target semantics across every production writer.

## Non-goals

- Migrate, wipe, reseed, or reinterpret retained beta.159 NATS state.
- Add compatibility shims, legacy readers, subject aliases, fallback ports, or dual GraphQL shapes.
- Reimplement SemStreams component lifecycle, graph CAS, projection ownership, or storage registry
  behavior in SemMachina.
- Expand the one-world-per-process/broker MVP boundary.
- Run a paid provider smoke before deterministic fresh-state acceptance is green and separately
  reviewed.

## Classification

This is application integration work over released SemStreams substrate contracts. SemMachina owns
its projection groups, composition order, product readiness gates, query adapter, tests, and
operator evidence. SemStreams continues to own component lifecycle, mutation CAS, graph storage,
typed ports, trajectory storage, and GraphQL transport contracts.

## Impact

- Every production graph writer and every graph-backed product read is in scope.
- Boot composition, rule readiness, trajectory evidence, the creator surface, world packages, and
  token-free acceptance change together.
- Rollback is operationally safe only while beta.159 and beta.160 use separate NATS storage.
- The open `mystery-companion-hardening` change may consume beta.160 revision support only after this
  migration establishes the canonical mutation boundary.
