# Mystery Companion Hardening

## Why

The archived mystery-companion acceptance proves the complete Bellweather path, secret-safe
projection, reusable companion behavior, deterministic hint progression, and logical redelivery
convergence. Three stronger guarantees depend on SemStreams primitives that remain unavailable.
Keeping them in one focused follow-up preserves the accepted product slice without weakening or
locally reimplementing substrate contracts.

## What Changes

- Reject graph-ingest and operator writes to canonical solution and truth-status predicates after
  import once [SemStreams #818](https://github.com/C360Studio/semstreams/issues/818) is available.
- Reset a companion hint ladder exactly once for each newly committed companion knowledge grant
  using revision-aware conditional mutation from
  [SemStreams #851](https://github.com/C360Studio/semstreams/issues/851).
- Guarantee at most one initial companion provider call across process crashes using durable
  request identity and idempotent publication from
  [SemStreams #807](https://github.com/C360Studio/semstreams/issues/807).

All three upstream issues were verified open on 2026-08-02.

## Capabilities

### Modified Capabilities

- `mystery-case`: extend package/import/rule/effect truth protection to graph-ingest and operator
  write surfaces.
- `companion-support`: add revision-safe knowledge-driven hint reset and crash-safe provider
  dispatch idempotency.

## Non-goals

- Reopen or rerun the completed Bellweather acceptance proof or its paid Gemini evidence.
- Add a SemMachina-local graph CAS, request ledger, or other SemStreams substrate workaround.
- Change hint selection, companion voice, initiative caps, or one-world-per-broker deployment.
- Claim these guarantees before the linked upstream capabilities ship and integration tests pass.

## Classification

This is blocked game-repo integration work over three SemStreams engine asks. SemMachina will
consume released upstream primitives and add domain tests and wiring; it will not implement
competing substrate behavior locally.

## Impact

- Strengthens canonical mystery truth against post-import substrate writes.
- Makes knowledge-driven hint reset safe under redelivery and concurrent graph mutation.
- Closes the provider-dispatch crash window while preserving one logical task and resolution.
- Does not change the already archived acceptance behavior until each upstream dependency lands.
