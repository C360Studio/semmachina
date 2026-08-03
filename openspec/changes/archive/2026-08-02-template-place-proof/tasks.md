## 1. Contract and Vocabulary

- [x] 1.1 Add failing closed-set, predicate-registration, subject/object-kind, multiplicity, and
  author-shape tests for `location`, `scene.location.current`,
  `location.relation.connects-to`, and canonical latitude/longitude
- [x] 1.2 Implement the registered place vocabulary and finite paired-coordinate validation
- [x] 1.3 Add failing package tests for missing/duplicate scene placement, wrong-kind occupancy,
  dangling or self connections, half coordinates, and coordinate ranges

## 2. Fixture and Context Migration

- [x] 2.1 Add location entities, scene placement, topology, and authored geometry to the starter
  fixture; migrate Bellweather without changing its accepted mystery facts
- [x] 2.2 Add failing assembler tests proving scene→location resolution, location-keyed membership,
  three fixed entities, at most six reads, old scene occupancy refusal, and unchanged configured
  entity and one-hop traversal bounds
- [x] 2.3 Implement the location-aware assembler and boot readiness gate
- [x] 2.4 Run starter, Bellweather, world-import, projector, companion, and turn-loop regression tests
  - Groups 1 and 2 received architecture APPROVE and independent Go review APPROVE.
  - Vocabulary, world, payload, scene, full boot, effect, companion, persona, mock-model, and
    epistemic gates were green. Strict OpenSpec validation also passed.
  - Focused starter and Bellweather E2E acceptances passed. The full combined E2E was not used as
    a gate for this slice; the focused acceptances were the E2E evidence.

## 3. Experience Catalog and Selection

- [x] 3.1 Add failing `packs.yaml` tests for closed/versioned parsing, clean existing paths, unique
  files within a pack, cross-pack file reuse, required defaults, empty persona packs, and legacy
  implicit persona plus empty-mechanics `default` packs
- [x] 3.2 Implement the catalog and strict package preflight without changing manifest v0
- [x] 3.3 Add failing instance tests for omitted defaults, exact named selection, unknown names, and
  selection sealed into otherwise disjoint resolved plans
- [x] 3.4 Implement `experience` instance configuration and selected plan records
  - Strict catalog, default, selection, and plan-seal tests passed, including the required
    persona-role gate and private validated-snapshot mutation safety.
  - Package path preflight uses `os.OpenRoot` and passed symlink-confinement coverage.
  - Independent Go review APPROVED the slice. The full world suite, targeted boot tests, CLI
    compilation, and diff checks passed.
  - Runtime boot composition and campaign provenance remain open under Group 4.

## 4. Boot Composition

- [x] 4.1 Add failing tests proving boot seeds only selected persona files and unselected files have
  no effect
- [x] 4.2 Add failing tests proving selected world rules combine with fixed engine definitions,
  duplicate IDs fail, and package rules still cannot reach turn/persona/model/engine state
- [x] 4.3 Implement selected persona seeding and selected rule composition in the existing boot
  sequence without a new lifecycle, component, stream, KV bucket, or rule processor
  - Named configuration selection resolves or refuses before broker access, and engine
    construction binds only the selected persona and mechanics content.
  - The aggregate persona-role gate runs before side effects. Selected rules compose
    deterministically with fixed engine rules in the same processor, with primary and related
    patterns narrowed to the selected instance.
  - Duplicate IDs and runtime-invalid definitions produce focused diagnostics. Independent Go
    review APPROVED the slice; focused boot/persona, full world, CLI compilation, and diff gates
    passed.
- [x] 4.4 Add failing campaign-provenance tests for atomic fresh selection records, exact restart
  match, changed selection, missing/partial provenance, and the explicit existing-world migration
  refusal; then implement the read gate before persona seeding or rule startup
  - Fresh campaign creation atomically records the seed plus persona and mechanics selections;
    restart reads one snapshot and requires an exact provenance match.
  - Migration, partial, ambiguous, malformed, and mismatched provenance are refused. Same-selection
    races converge, different-selection races refuse the loser, and the private full-claim proof
    covers the complete atomic value.
  - Boot orders World before Personas, Agentic, and Rules, while package-rule protections remain
    intact. Architecture and independent Go reviews both APPROVED the slice.
  - Bounded campaign, vocabulary, world, and boot gates passed. No broad boot aggregate or E2E was
    used yet; template and tunability acceptance remains Group 5.

## 5. Template and Tunability Acceptance

- [x] 5.1 Author two persona packs and two bounded mechanics overlays in the unchanged starter
  template, including one topology-only location as an optional-geometry control
- [x] 5.2 Add one deterministic integration/E2E proof that two namespaces instantiate the same
  template with disjoint IDs, different selected voice, and materially different world-state
  consequences from identical input while the fixed turn-stage path remains unchanged
- [x] 5.3 Prove restart preserves the selected experience and never re-imports or switches a living
  world implicitly
  - The unchanged starter fixture exposes two selectable persona/mechanics pairs and retains
    `north-road` as the topology-only optional-geometry control.
  - Acceptance runs the pairs serially because `PERSONAS` is global; it does not claim concurrent
    voice isolation. Both runs use the same fixture, player action, scripted model output, and
    wounded effect intent, while each narrator prompt contains only its selected voice marker.
  - The mechanics selections remove different graph relations. Both runs retain the identical fixed
    stage sequence after normalizing only `entity_id` and `timestamp`.
  - Same-selection restart preserves entity revisions, experience provenance, and the import marker.
    A changed selection is refused by World before Persona or Rules can mutate shared state.
  - Boot graph integration activates only for selected graph-mutating mechanics; empty and
    non-graph selections, including bounded `deny`-only packs, preserve the baseline processor
    configuration.
  - Architecture conditionally APPROVED the slice, and independent Go review APPROVED it. Focused
    world, mock-model, boot, deterministic E2E, and diff gates passed.

## 6. Quality Gates

- [x] 6.1 Run architecture review for world/package, rule/component/persona, graph, and ownership
  boundaries
- [x] 6.2 Run backend code and security review for path confinement, rule authority, identity
  isolation, bounds, error handling, and regression coverage
  - Architecture APPROVED the final world/package, place, selected-persona/mechanics, fixed
    sequencing, provenance, and instance-per-world ownership boundaries. The review records
    broker-global `PERSONAS` as a deployment limit rather than concurrent voice isolation.
  - Backend/security review APPROVED rooted package reads, closed catalog and strict decoding,
    same-instance graph targeting, bounded action capabilities, construction-bound selections,
    mismatch-before-side-effect ordering, and regression coverage.
- [x] 6.3 Run unit, integration, race, deterministic E2E, lint, build, strict OpenSpec, and diff gates
  - `go test -race -count=2 -timeout=20m ./internal/e2e` passed in 930.279 seconds. The clean-broker
    fixture rotates once at the start of each acceptance invocation, never between its two serial
    experience variants; it does not expand the broker-global `PERSONAS` deployment boundary or
    permit parallel E2E tests.
  - `task test` passed twice with the original `-p2` package parallelism. Each run recorded 3,043
    outcomes across 30 packages with zero skips; the E2E package completed in 467.216 seconds and
    475.403 seconds respectively.
  - Focused RED fixture regressions reproduced the action-authority, identity/datatype, and
    accumulated-broker failures before their fixes and passed afterward.
  - `task lint`, `task build` including the Linux cross-compile, strict `task spec` at 13/13 before
    archive, and `git diff --check` all passed. Final architecture and backend/security reviews
    found no P0-P3 issues.
- [x] 6.4 Update authoring, instance configuration, ontology, existing-world migration, and roadmap
  documentation, then archive only after every retained normative scenario passes
  - The world-authoring guide now covers package and catalog structure, instance selection, bounded
    rule authority, first-class places, living-world migration, and the one-active-world deployment
    boundary. The README and founding roadmap record the completed proof and next creator/UI slice.
    Archival follows the completed implementation, review, documentation, and quality gates.
