## 1. Canonical Truth Write Enforcement

- [ ] 1.1 Finish acceptance task 1.5: reject import, effect, world-rule, and operator-write
  attempts that mutate or branch on canonical solution and truth-status predicates
  - Package validation, import classification, world-rule, and effect gates are complete.
    Graph-ingest and operator enforcement remain blocked by
    [SemStreams issue #818](https://github.com/C360Studio/semstreams/issues/818), verified open on
    2026-08-02.
- [ ] 1.2 After #818 ships, add failing graph-ingest and operator-write tests and wire the
  registered protected predicates through the released enforcement surface

## 2. Knowledge-Driven Hint Reset

- [ ] 2.1 Finish acceptance task 8.1: prove exact `nudge → connect → next-step` reset on a newly
  committed companion knowledge grant, with no reset on duplicate or rejected grants
  - Progression, saturation, companion-known selection, and serialized advance are complete.
    Revision-aware reset remains blocked by
    [SemStreams issue #851](https://github.com/C360Studio/semstreams/issues/851), verified open on
    2026-08-02.
- [ ] 2.2 After #851 ships, add failing new, duplicate, rejected, stale, wrong-actor, and
  concurrent-revision tests before implementing the conditional bond update

## 3. Crash-Safe Companion Dispatch

- [ ] 3.1 Finish acceptance task 8.5: prove one companion provider call and one resolution at
  most per triggering turn across restart and duplicate delivery
  - Deterministic task identity and one exact-resident outcome/ref are complete for sequential
    redelivery. Crash-safe provider dispatch remains blocked by
    [SemStreams issue #807](https://github.com/C360Studio/semstreams/issues/807), verified open on
    2026-08-02.
- [ ] 3.2 After #807 ships, test every claim/publication crash boundary before wiring the durable
  `TaskID → LoopID → initial RequestID` binding and idempotent request publication

## 4. Quality Gates

- [ ] 4.1 Re-run architecture review for lifecycle, rule, component, persona, KV, and stream
  boundaries without a substrate workaround
- [ ] 4.2 Re-run code and security review for protected truth, revision checks, request identity,
  and provider-call idempotency
- [ ] 4.3 Run unit, integration, race, deterministic Bellweather E2E, lint, build, strict OpenSpec,
  and diff checks before archive
