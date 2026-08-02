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
  - Durable campaign selection provenance and restart matching remain open under task 4.4.
- [ ] 4.4 Add failing campaign-provenance tests for atomic fresh selection records, exact restart
  match, changed selection, missing/partial provenance, and the explicit existing-world migration
  refusal; then implement the read gate before persona seeding or rule startup

## 5. Template and Tunability Acceptance

- [ ] 5.1 Author two persona packs and two bounded mechanics overlays in the unchanged starter
  template, including one topology-only location as an optional-geometry control
- [ ] 5.2 Add one deterministic integration/E2E proof that two namespaces instantiate the same
  template with disjoint IDs, different selected voice, and materially different world-state
  consequences from identical input while the fixed turn-stage path remains unchanged
- [ ] 5.3 Prove restart preserves the selected experience and never re-imports or switches a living
  world implicitly

## 6. Quality Gates

- [ ] 6.1 Run architecture review for world/package, rule/component/persona, graph, and ownership
  boundaries
- [ ] 6.2 Run backend code and security review for path confinement, rule authority, identity
  isolation, bounds, error handling, and regression coverage
- [ ] 6.3 Run unit, integration, race, deterministic E2E, lint, build, strict OpenSpec, and diff gates
- [ ] 6.4 Update authoring, instance configuration, ontology, existing-world migration, and roadmap
  documentation, then archive only after every retained normative scenario passes
