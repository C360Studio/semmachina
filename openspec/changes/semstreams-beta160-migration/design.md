# Design — SemStreams beta.160 Migration

## Context

SemMachina was pinned to beta.159 and centralized many graph writes in a raw subject adapter. The
application also treats relationship targets as queryable referential stubs, manually starts
framework components, waits on the retired component-status bucket, and consumes unpaged GraphQL
entity arrays. Beta.160 removes those contracts without compatibility aliases.

The release adoption contract is stronger than a source migration: the beta.160 process must start
against newly provisioned NATS storage. Discovery of retained deployed state stops this change and
opens a separate owner-reviewed migration or recovery design.

## Goals / Non-Goals

**Goals:**

- Make graph ownership explicit at every write call.
- Preserve product idempotency without blindly retrying uncertain mutations.
- Use ordinary upstream lifecycle and storage providers through strict registered composition.
- Keep product startup ordered, fail closed, and reversibly stoppable.
- Preserve the browser-facing closed DTO while adopting canonical beta.160 GraphQL envelopes.
- Produce auditable fresh-state, restart, and token-free acceptance evidence.

**Non-Goals:**

- Preserve beta.159 NATS state in place.
- Admit both beta.159 and beta.160 contracts in one binary.
- Build another component manager, graph writer, query gateway, or storage registry.
- Treat missing relationship targets as materialized entities.

## Decisions

### D1 — Adoption is a fresh-state blue/green cutover

The beta.159 deployment and its NATS storage remain unchanged. Beta.160 receives a newly
provisioned broker or storage volume with a distinct recorded identity. Provisioning, boot, world
import, restart, and deterministic acceptance occur on that green stack before traffic or paid
work moves.

Rollback stops the green application and restarts the beta.159 application against only its
original storage. Neither binary may open the other release's storage. Once retained state is found
on the intended green storage, adoption stops; deletion, reseeding, or conversion is outside this
change.

### D2 — Activation barriers each use a normal upstream ComponentManager

The composition root owns three immutable upstream component barriers: graph ingestion/index,
agentic storage/tools/model/loop, and rule processing. Each uses one ordinary upstream
`ComponentManager`. SemMachina's existing boot sequence remains the composition root for those
managers and product-owned ingress, stages, resume, ledger, and egress.

The coordinator admits all three managers and validates their union manifest before starting the
first one. It starts managers at their dependency-safe sequence positions and transfers cleanup
ownership before each start attempt, so a partially started barrier is also stopped. On failure or
shutdown it stops started managers exactly once in reverse activation order. It does not call
component lifecycle methods, admit identity-free instances, or reproduce manager internals.

Barrier membership is immutable for one boot. A configuration change requires a service restart,
which is the beta.160 operational contract.

### D3 — Ports, readiness, and storage are strict

Every port uses the canonical beta.160 definition with a declared direction and typed `config`.
JetStream inputs name `stream_name` and accepted subjects; outputs name their subjects. Request/reply
paths use `nats-request`. Graph-query outputs use the required named `graph.query/v1` family. A graph
writer declares the graph mutation requester, and a flow has exactly one matching mutation provider.

There are no side-lane aliases, custom kinds, inferred directions, legacy subjects, or optional
fallbacks. Cross-barrier validation fails before start when a required port has no exact provider.

Graph foundation readiness comes from `GRAPH_STATUS`. Rule readiness comes from the rule domain's
supported lifecycle/readiness evidence. The removed component-status bucket is neither read nor
created.

The agentic barrier owns the sole registered object-store provider. Its logical `StoreRegistry`
instance is always `objectstore`; configuration's `content_bucket` selects the physical NATS object
store bucket and never changes the logical reference. Cache is disabled. The agentic loop's
trajectory evidence and every SemMachina content reference use `objectstore`.

The content adapter resolves `objectstore` from the current `ComponentManager` generation for every
operation and neither caches nor closes the manager-owned provider. Only the exact-claim KV sidecar
remains locally owned. Startup fails before agentic work when the logical instance is absent,
duplicated, or mismatched. `AGENT_TRAJECTORIES` remains the bounded index; full evidence lives in
the registered store.

### D4 — Every graph writer has one projection contract and group

Projection contracts are application authority, not generic transport metadata. The graph adapter
accepts an explicit contract and group; it does not infer either from predicates or call stacks.
There is no catch-all SemMachina contract.

- Turn recorder and stages use contract `turn`, with groups `phase`, `case-decision`,
  `case-progress`, `companion-trigger`, `companion-decision`, `verdict`, `roll`, `effects-marker`,
  `narration`, `knowledge`, `accusation`, and `resume`. Birth creates the turn; each later group
  reconciles only its complete owned values.
- Player admission and egress use `player-turn` groups `current-pointer` and `resolved-pointer`.
  Each is a complete single-valued group.
- Campaign gate/importer uses `campaign/import-marker`. Campaign birth is created once, and the
  marker reconciles only after queryable import.
- Caseflow uses `case-lifecycle/receipt` for complete committed case-progression evidence.
- Companion runtime uses `companion-bond/hint`; hint level and reset evidence reconcile together.
- Knowledge granter uses `knowledge` and `revelation` births with complete top-level triples.
- Effect applier uses `world-effects`, with one group per registered mutable predicate family. Each
  target receives the complete desired set for every effect-owned group touched by a batch.
- A selected mechanics pack receives one contract and one group per declared rule-owned predicate
  set. Package preflight derives or validates the contracts before materialization.

The inventory is exhaustive for production writers. Adding a writer, mutable predicate family, or
package rule group requires a failing ownership test and an update to this inventory before code.
Protected truth predicates are absent from every mutable contract.

### D5 — Reconcile semantics are complete, revision-aware, and uncertainty-safe

Typed create sends an entity without embedded triples plus the complete top-level birth triples.
Reconcile begins from an exact entity read carrying the authoritative KV revision. Desired values
are complete for the named group: omitted predicates remain outside that group's authority, while an
explicit empty desired group clears every predicate owned by that group.

`revision_mismatch` is not hidden. The domain writer rereads the exact entity, recomputes desired
state from current authoritative facts, and attempts only a still-valid transition within its
bounded delivery policy.

`commit_unknown` is never blindly retried. The writer exact-reads the target and proves whether the
desired group converged. It records success only when the read proves the intended state. If the
read proves it did not converge, normal domain recovery may recompute a new conditional mutation;
if the read cannot classify the outcome, the delivery remains unresolved and no fictional success
or rejection is emitted.

`entity_not_found` means absent. A relationship to a missing target does not create a stub, occupy a
key, or turn a missing read into success. Domain boundaries decide whether absence is a permanent
integrity error, a safe omission, or an eventually consistent dependency.

### D6 — GraphQL clients consume only beta.160 shapes

The server adapter consumes exact entity reads as `ExactEntity { entity, kvRevision }` and prefix
enumeration as `EntityPage { entities, next_cursor }`. It follows opaque cursors to completeness
within configured page, entity, relationship, and byte caps. A repeated cursor, malformed page,
out-of-scope entity on any page, or aggregate-cap breach fails the whole projection without a
partial browser DTO.

Relationship responses use only the canonical beta.160 fields `from`, `to`, and `predicate`.
Beta.159 snake-case aliases are rejected as unknown response fields. The browser API remains a
closed SemMachina DTO and receives neither upstream revision values nor cursors.

Derived graph views may report `index_not_ready`. The server classifies that as retryable, and the
creator surface exposes an accessible explicit retry rather than permanently latching the first
projection error.

### D7 — Migration follows dependency and TDD order

1. Record tag `v1.0.0-beta.160`, commit
   `8403a2218000e45a31c5132fbfe01af42ed04f14`, and provision isolated green storage.
2. Add failing composition, typed-port, readiness, and storage-registration tests.
3. Pin beta.160 and make every binary compile without compatibility code.
4. Add failing graph create, reconcile, exact-read, revision, uncertainty, and missing-target tests.
5. Implement projection contracts and migrate writers in inventory order.
6. Migrate rules and world-package preflight.
7. Add failing beta.160 GraphQL adapter and accessible-retry tests, then migrate the surface.
8. Run fresh-stack integration, restart, deterministic E2E, strict spec, race, build, and isolation.
9. Run paid acceptance only with new authorization after every token-free gate is green.

Each backend slice requires Go reviewer sign-off before dependent frontend work. Frontend work
requires Svelte reviewer accessibility and UX sign-off. Documentation and task closure follow both.

## Risks / Trade-offs

- Projection ownership is deliberately verbose. That verbosity makes accidental cross-domain
  replacement fail at compile or admission time instead of corrupting graph state.
- Multiple managers add a coordinator, but preserve the upstream lifecycle implementation and make
  cross-subsystem activation order explicit.
- Full prefix pagination increases query work. Aggregate caps bound it and fail closed instead of
  returning a plausible partial world.
- Fresh-state adoption does not preserve active campaigns. Preserving one is a separate migration
  project with different authorization and rollback requirements.

## Rollback

Before traffic moves, any failure tears down the green stack in reverse barrier order and leaves
beta.159 serving from its untouched storage. After traffic moves, rollback still selects the whole
beta.159 application-plus-storage unit; beta.160-created state is retained for diagnosis and is not
opened by beta.159. A later attempt provisions another reviewed green target or explicitly reuses
the failed green target only after proving its state satisfies the fresh-adoption contract.

## Open Questions

None. The logical store instance is `objectstore`; the physical content bucket and fixed aggregate
query bounds are recorded configuration/implementation contracts.
