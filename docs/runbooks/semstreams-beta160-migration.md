# SemStreams beta.160 Migration Runbook

This runbook governs the breaking SemStreams cutover. It is a fresh-state blue/green deployment,
not an in-place data migration. Never point beta.160 at beta.159 storage, and never wipe, reseed, or
reinterpret retained state to make a preflight pass.

## Release contract

| Item | Required value |
| --- | --- |
| SemStreams tag | `v1.0.0-beta.160` |
| SemStreams commit | `8403a2218000e45a31c5132fbfe01af42ed04f14` |
| NATS server | `2.14.4` |
| Component config version | `1.1.0` |
| Logical content storage instance | `objectstore` |
| Physical content storage | The deployment's configured `content_bucket` |
| Adoption model | Fresh-state blue/green only |

`go.mod`, the engine compatibility constant, deployment harnesses, and the surface stack must agree
with these versions. The logical storage instance does not vary with the physical bucket name.

## Evidence record

Create an operator-owned record before deployment. Do not put credentials, NATS URLs containing
credentials, or provider keys in it.

| Evidence | Beta.159 blue | Beta.160 green |
| --- | --- | --- |
| Application image or commit | Record | Record |
| Broker/server identity | Record | Record |
| Storage volume/account identity | Record | Record |
| NATS server version | Record | Must be `2.14.4` |
| Physical `content_bucket` | Record | Record |
| First boot timestamp | Existing | Record |
| Traffic switch timestamp | Record if switched | Record if switched |
| Teardown/reverse-stop result | Not applicable | Record |

The blue application and its storage are one rollback unit. The green application and its distinct
storage are another. A different `world_ns` on the same retained broker is not sufficient isolation.

## Preflight

1. Verify the release pins:

   ```sh
   go list -m github.com/c360studio/semstreams
   rg 'EngineVersion|WithNATSVersion' internal cmd
   ```

2. Record the distinct blue and green broker, account, and volume identities.
3. Inspect the intended green NATS account without modifying it. Using an authenticated operator
   context, list streams, KV buckets, and object stores:

   ```sh
   nats stream ls
   nats kv ls
   nats object ls
   ```

4. Stop immediately if the green target contains retained deployed state. Do not purge it. Open a
   separate owner-reviewed migration or recovery design.
5. Verify the beta.159 application remains connected only to its original storage and can still be
   selected as a complete rollback unit.
6. Run all token-free gates before supplying a model-provider credential.

## Runtime contract

The composition root admits graph, agentic, and rule barriers before activating any of them. Each
barrier uses a normal upstream `ComponentManager`. The graph manager starts before agentic and rule
work, and every started or partially started manager is stopped exactly once in reverse activation
order on failure or shutdown.

The agentic manager owns the only production object-store provider. It registers logical instance
`objectstore`, maps it to `content_bucket`, disables the provider cache, and supplies both trajectory
evidence and SemMachina content references. The content adapter resolves the current managed store
on every operation; it does not cache or close the manager-owned provider.

Rule readiness is generation-aware. Startup requires both the rule manager's started state and a
`GRAPH_STATUS/rule` entry newer than the pre-activation revision with all of these values:

- state `ready`;
- `Ready` true; and
- `BootstrapComplete` true.

Missing, stale, building, degraded, or reset-required readiness fails closed. The removed
component-status plane is neither created nor consulted.

## Graph and surface contract

Every production writer uses an explicit projection contract and group. The inventory covers turn
birth and phase/evidence groups, player pointers, campaign import, case receipts, companion hints,
knowledge and revelation births, all registered effect-owned mutable families, and selected
mechanics groups. Typed create carries a shared canonical `Context` and `RequestID`; reconcile starts
from an exact revisioned read. `commit_unknown` is classified by an exact convergence read and is
never blindly retried.

A reference to an entity without a birth record remains missing. No stub or degraded-create lane is
available.

The creator surface consumes beta.160 `ExactEntity` and paginated `EntityPage` envelopes, forwards
opaque cursors, requires canonical relationship fields, and fails the entire projection on malformed
or out-of-scope later pages or bound violations. `index_not_ready` exposes an accessible explicit
retry; it does not permanently latch the first projection error.

## Token-free validation

Run from the repository root:

```sh
task spec
task lint
task build
task test:unit
task test:integration
task test:pipeline
task test:recovery
task test:acceptance
task test:e2e
task frontend:test
task smoke:surface:isolation
task smoke:surface:preflight
```

The five real-infrastructure Go lanes require Docker and must use exact NATS `2.14.4`. Each lane
writes checked `go test -json` evidence and rejects skips. CI accepts the split only after the union
gate receives all six non-empty artifacts and applies the repository-wide minimum-outcome and
zero-skip checks. See the [testing guide](../guides/testing.md) for the lane manifest, build tags,
package selectors, concurrency, and artifact contract.

The three frontend lanes are server unit, browser component, and deterministic UI journey. Static
checks and build remain separate required gates. Preflight and isolation make no paid model call.

Before cutover, search active code and docs for retired contracts. Hits in archived change records
or dated beta.159 smoke evidence are historical and must not be rewritten as beta.160 evidence.

```sh
rg 'COMPONENT_STATUS|component-status|replace_owned|from_entity|to_entity|beta\.159' \
  internal cmd web/src web/tests docs openspec/specs \
  --glob '!openspec/changes/archive/**'
```

Every active hit needs an explicit historical reason or blocks the release.

## Cutover

1. Start green against only the recorded fresh NATS target.
2. Confirm graph and generation-aware rule readiness.
3. Import the world and prove it is queryable before the campaign import marker is committed.
4. Restart green against its own new state and prove convergence for readiness, content resolution,
   turn completion, resume, and ledger behavior.
5. Run action-free surface preflight and deployment isolation.
6. Review the complete evidence record and both formal code-review approvals.
7. Move traffic as one deliberate switch. Do not share storage between releases.
8. Keep beta.159 application and storage unchanged for the approved rollback window.

Paid acceptance is a separate operator decision. If authorized, follow the paid smoke runbook and
poll authoritative state every 30–60 seconds. Abort when state proves the run is wedged rather than
waiting for a paid timeout.

## Rollback

On a green failure, stop the beta.160 application and verify reverse-order teardown. Switch traffic
to the entire beta.159 application-plus-storage unit. Never start beta.159 against green state or
beta.160 against blue state.

Retain failed green state for diagnosis. A later attempt uses another fresh target or reuses the
failed target only after a separate review proves that reuse satisfies the adoption contract. Any
request to delete, convert, or reseed retained state is outside this runbook.

## Current implementation evidence

The implementation has formal Go and Svelte reviewer approval. The evidence available for this
change is recorded below; it is implementation evidence, not proof that an operator provisioned an
isolated deployment target.

| Gate | Recorded result |
| --- | --- |
| Strict OpenSpec | Passed all 19 active specs and changes |
| Static/build | Passed `task lint` and `task build` |
| Go unit lane | Passed race-enabled, zero-skip; 3,025 outcomes |
| Go integration lane | Passed race-enabled, zero-skip; 1,394 outcomes |
| Go pipeline lane | Passed race-enabled, zero-skip; 82 outcomes |
| Go recovery lane | Passed race-enabled, zero-skip; 59 outcomes |
| Go acceptance lane | Passed zero-skip; 134 outcomes |
| Go E2E lane | Passed 28 outcomes in 508.657 seconds against exact NATS `2.14.4` |
| Six-lane union | Passed 4,722 outcomes across 32 packages, zero-skip |
| Frontend server unit | Passed 532 tests |
| Frontend browser component | Passed 30 tests |
| Frontend UI journey | Passed 6 Playwright tests |
| Frontend static/build | Passed strict check, lint, and build |
| Deployment-isolation contract | Passed |
| Action-free real-stack preflight | Passed 1 Playwright test in 1.2 seconds; NATS/Ryuk cleaned up |
| Deep retired-contract audit | Passed; all remaining hits are justified evidence or rejection guards |

A previous parallel all-package Go attempt exposed a genuine factless-player gateway fixture, then
timed out E2E while several container-heavy packages competed for the same Docker host. That run is
not acceptance evidence. The corrected checked lanes and their complete zero-skip union supersede
it.

The E2E lane includes fresh exact-NATS crash/resume coverage and digest/size proof that logical
`objectstore` trajectory references resolve through the manager-owned provider to the configured
physical `ContentBucket`. The action-free real-stack preflight recorded
`unauthorized_world_verified`, `login_http_verified`, `action_controls_visible`,
`schematic_world_visible`, `clock_visible`, and `retrieval_answered`, then cleaned up its temporary
NATS and Ryuk resources.

SemStreams issue #424 can emit benign invalid-subscription `ERROR` noise when the rule watcher is
stopped twice. This is a known upstream diagnostic artifact, not evidence of a local lifecycle
defect. SemMachina's ComponentManager shutdown ordering has formal review and test coverage.

The token-free workstation operator rehearsal is recorded in the migration evidence:
[2026-08-12 SemStreams beta.160 workstation](../migration-evidence/2026-08-12-semstreams-beta160-workstation.md).
It proves concrete blue/green identities, green-storage freshness, restart convergence,
application-before-broker teardown, and whole-unit beta.159 rollback without a model call. This
closes the migration's workstation acceptance scope.

The separately authorized paid acceptance is recorded in the
[2026-08-12 Gemini 3.5 Flash-Lite beta.160 acceptance][beta160-paid].
Exactly one invocation ran, both fixed provider chains passed, and no retry was authorized or run.
The paid result does not authorize a future production cutover. Such a deployment must create its
own preboot freshness, storage-identity, cutover, and rollback record.

## Release status

All implementation, reviewer, deterministic acceptance, workstation migration, and separately
authorized paid-acceptance gates are complete. The OpenSpec change was archived on 2026-08-12 as
[`2026-08-12-semstreams-beta160-migration`][beta160-archive], and the post-archive pinned OpenSpec
1.7.0 gate passed all 20 current specs and changes. Only the repository's normal pull-request
ready, green-check, and merge actions remain.

Archive does not convert this workstation evidence into production authorization or evidence.

[beta160-archive]: ../../openspec/changes/archive/2026-08-12-semstreams-beta160-migration/
[beta160-paid]: ../smoke-results/2026-08-12-bellweather-gemini35-flash-lite-beta160.md
