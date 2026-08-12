# Testing

SemMachina separates tests by the boundary they prove, not just by directory. The default Go lane is
Docker-free. Real-infrastructure files belong to exactly one checked execution lane, and frontend tests are split
by whether they need Node, a browser component runtime, or a complete HTTP and WebSocket journey.

The repository gate is intentionally stricter than a passing test process: it also proves that expensive tests
were not omitted and that no selected test skipped.

## Go test tiers

| Tier | Contract | Infrastructure | Local command |
|---|---|---|---|
| Unit | Deterministic behavior within a package or process | No Docker | `task test:unit` |
| Integration | Focused seam using real messaging or graph components | Docker | `task test:integration` |
| Pipeline | Rule, graph, and stage pipeline, including stage recovery | Docker | `task test:pipeline` |
| Recovery | JetStream work-queue inspection and resume decisions | Docker | `task test:recovery` |
| Acceptance | The production boot composition on a bare broker | Docker | `task test:acceptance` |
| E2E | A complete player action and turn path through the real local composition | Docker | `task test:e2e` |

“E2E” in this table means the Go engine lane. The Playwright UI-journey lane is a separate frontend boundary.

### Unit

Unit tests have no tier build tag and run through `go test -race -p 2 -timeout=10m -count=1 ./...`. The lane
must remain Docker-free. A test's directory does not assign its tier: for example, an untagged deterministic
fixture test may live in `internal/e2e` and still belong to the unit lane.

Use unit tests for pure business rules, state machines, parsers, contract validation, and process-local
collaborators. They should not start `natsclient.NewSharedTestClient`, testcontainers, or the shared
real-infrastructure harness.

### Execution lanes and build tags

The checked manifest has five real-infrastructure execution labels: `integration`, `pipeline`, `recovery`,
`acceptance`, and `e2e`. They map to three Go build tags:

| Execution lane | Required build tag |
|---|---|
| Integration, pipeline, recovery | `integration` |
| Acceptance | `acceptance` |
| E2E | `e2e` |

Pipeline and recovery are scheduling boundaries, not source tags. Their files must start with
`//go:build integration`; do not introduce `//go:build pipeline` or `//go:build recovery`. The manifest assigns
each file to one execution lane, and the runner derives that lane's package selector from those entries.

### Integration

Integration tests prove a focused production seam with real infrastructure. Each file begins with the exact
header below and is listed as `integration` in [`scripts/test-tiers.tsv`](../../scripts/test-tiers.tsv):

```go
//go:build integration

```

The runner derives packages from the manifest, then uses
`go test -tags=integration -race -p 2 -timeout=20m -count=1`. Package concurrency is capped at two so a CI host
starts at most two package-level NATS containers at once.

Most packages in the three integration-tagged lanes use
[`internal/testinfra`](../../internal/testinfra/harness.go). It starts one real NATS container and graph substrate
per package from `TestMain`; tests share that package harness and isolate world state with distinct entity IDs and
namespaces. This saves repeated container startup without replacing the production messaging and graph behavior
with fakes.

### Pipeline

The pipeline lane owns the integration-tagged tests in `internal/stage`. It proves the real graph, rule, and
stage pipeline; stage-runner acknowledgement behavior; persona-task spawning; and stage-level restart and
re-trigger decisions. Stage recovery remains here because it exercises the stage package's real pipeline harness,
not only the resume component.

The runner selects only manifest packages assigned to `pipeline`, then uses
`go test -tags=integration -race -p 1 -timeout=20m -count=1`. Serial package execution prevents this
container-heavy pipeline from competing with another package lifecycle on the same runner.

### Recovery

The recovery lane owns the integration-tagged tests in `internal/resume`. It proves work-queue inspection against
real JetStream streams and durable consumers, including stage triggers and persona tasks, acknowledgement-floor
semantics, and the decisions that distinguish queued work from a genuinely stranded turn.

The runner selects only manifest packages assigned to `recovery`, then uses
`go test -tags=integration -race -p 1 -timeout=20m -count=1`. This is the focused recovery-substrate lane. It
does not absorb every test whose scenario contains a restart: stage recovery belongs to pipeline, while a full
process crash and resumed player turn belongs to E2E.

### Acceptance

Acceptance tests prove that the production composition can create its own streams, buckets, consumers, graph
components, rule processor, agentic loop, and ingress on a bare broker. Files use `//go:build acceptance` and
the checked manifest. The lane runs serial package lifecycles with
`go test -tags=acceptance -p 1 -timeout=20m -count=1`.

The boot acceptance owns its bare `natsclient.TestClient` because the shared integration harness starts graph
components itself. Reusing that harness would create competing durable consumers and would no longer prove the
production boot order.

### E2E

Go E2E tests use `//go:build e2e` and drive a player action through the real broker-backed turn composition.
They use scripted [`internal/mockmodel`](../../internal/mockmodel) responses, so they prove orchestration without
calling a remote model. The measured lane is close to 20 minutes locally, so it uses
`go test -tags=e2e -p 1 -timeout=30m -count=1` to leave a cold hosted runner diagnostic headroom.

State-sensitive scenarios replace the package broker with a fresh SemStreams `natsclient.TestClient` at their
outer boundary. Subtests and process restarts share that broker so continuity is real within one scenario. Broker
rotation makes the package serial-only; a structural test rejects any `t.Parallel()` call in the E2E package.

Crash-and-resume coverage in [`internal/e2e/resume_test.go`](../../internal/e2e/resume_test.go) uses the package
harness in [`internal/e2e/harness_test.go`](../../internal/e2e/harness_test.go). It remains in `test:e2e` because
it proves a complete process crash, boot, and player-turn continuation. The focused `test:recovery` lane instead
owns the lower-level `internal/resume` work-queue contract.

## Real-infrastructure harness rules

SemStreams `natsclient.NewSharedTestClient` starts a real NATS test container and returns a connected
`TestClient`. Its lifecycle belongs at package or top-level acceptance scope:

- create it once in `TestMain` or at the explicit acceptance boundary;
- terminate it after all owners have stopped;
- give graph components a dedicated NATS connection when stopping test consumers could otherwise disturb their
  durable bindings;
- isolate shared-harness tests by namespace and clean up durable consumers or backlog that can affect the next
  test; and
- do not add `t.Parallel()` where package-global registries, brokers, consumer rotation, or cleanup make it
  unsafe.

Real infrastructure is required by default. `SEMMACHINA_SKIP_INTEGRATION` is only a developer escape hatch for a
machine without Docker. The harness turns the affected tests into explicit skips, and the repository gate rejects
those skips. CI must never set this variable.

## Anti-omission and zero-skip gates

[`scripts/test-tiers.tsv`](../../scripts/test-tiers.tsv) is the single source of truth for tagged Go test files.
Each entry assigns one file to exactly one of `integration`, `pipeline`, `recovery`, `acceptance`, or `e2e` and
therefore to one CI execution lane. The checker also enforces the build-tag mapping described above.
[`scripts/check-test-tiers.sh`](../../scripts/check-test-tiers.sh) rejects:

- duplicate, missing, unsafe, or incorrectly tagged entries;
- tagged test files absent from the manifest;
- `_integration_test.go` files absent from the manifest;
- files that start Docker through the known SemStreams or testinfra constructors but are absent from the
  manifest; and
- an untagged `TestMain` that can start Docker; or
- one package assigned to multiple execution lanes, which would make package-level `go test` selection overlap.

The validator has adversarial fixtures in
[`scripts/check-test-tiers_fixture_test.sh`](../../scripts/check-test-tiers_fixture_test.sh), and both local unit
and all-tag runs exercise them.

[`scripts/run-go-test-lane.sh`](../../scripts/run-go-test-lane.sh) validates membership, writes raw
`go test -json` output, renders a report, and invokes the zero-skip checker. Tagged lanes must produce at least one
test outcome. Unit and aggregate runs retain the module-level outcome floor, which catches selectors that silently
drop a large part of the suite.

CI runs the six Go lanes independently and uploads each raw JSON stream. The `test_union` job requires all six
exact, non-empty artifact names before concatenating them, validates the manifest again, and applies the zero-skip
and minimum-outcome gate to the union. This makes the parallel split accountable: a green lane is insufficient if
the combined run omitted coverage or skipped a test. Adversarial union fixtures prove a missing lane is rejected.

`task test:all-tags` remains the temporary local migration gate. It enables all three tags in one race-enabled
module run. `task test` runs that aggregate plus the full frontend suite.

## Frontend lanes

The frontend uses one test name per runtime boundary. Run the commands below from `web`, or add
`npm --prefix web run` in place of `npm run` when running from the repository root.

| Lane | Runner and selection | Command |
|---|---|---|
| Server unit | Vitest `server` in Node; excludes Svelte component tests | `npm run test:unit:server` |
| Browser component | Vitest `client` in headless Chromium; selects Svelte tests | `npm run test:component:browser` |
| UI journey | Playwright with built SvelteKit and a local upstream | `npm run test:journey:ui` |

The Vitest project definitions live in [`web/vite.config.ts`](../../web/vite.config.ts). Component tests render
Svelte in Chromium; they do not boot the complete application surface. Server unit tests run in Node and cover
server policy, runtime, transport, and projection behavior without a browser.

The default [`web/playwright.config.ts`](../../web/playwright.config.ts) starts two servers: a deterministic player
and GraphQL upstream on loopback, then the built SvelteKit server configured against it. It selects `*.e2e.ts`
while explicitly ignoring `*.real.e2e.ts`. These UI journeys exercise browser login, HTTP, WebSocket, rendering,
and reconnection behavior without Docker or a model provider. CI retains Playwright traces and screenshots only
for failures and uploads the report directories.

For a first local browser run, install dependencies and Chromium:

```sh
task frontend:install
task frontend:install:browsers
task frontend:test
```

## Paid and real-surface tests

Paid provider surfaces are deliberately outside `task test`, `npm test`, and CI. CI supplies no model API key,
Go acceptance and E2E use the loopback scripted model, and the default Playwright configuration excludes the real
surface file.

The real-stack browser runner uses [`web/playwright.real.config.ts`](../../web/playwright.real.config.ts) and is
operator-invoked:

- `task smoke:surface:preflight` boots the real local stack and performs an action-free browser check. It does not
  call a model provider.
- `task smoke:gemini:bellweather` and `task smoke:gemini:surface` are paid acceptance surfaces.
- `task demo:surface` is an interactive paid presenter path.

Every paid command requires both `SEMMACHINA_PAID_SMOKE=1` and a non-empty `GEMINI_API_KEY`. Do not fold these
commands into an aggregate test target or CI matrix: spend must remain an explicit operator decision.

## Common workflows

Run only the boundary changed by a small patch:

```sh
task test:unit
task test:integration
task test:pipeline
task test:recovery
task test:acceptance
task test:e2e
```

Run the current complete local test gate:

```sh
task test
```

Override a Go lane's JSON destination when preserving or comparing evidence:

```sh
task test:integration JSON=/tmp/semmachina-integration.json
```

When adding a real-infrastructure test, add the exact build tag and manifest entry in the same change, then run
`bash scripts/check-test-tiers.sh` before the lane. A new test that needs a different lifecycle boundary should
first extend the harness contract rather than silently starting another broker inside a test.
