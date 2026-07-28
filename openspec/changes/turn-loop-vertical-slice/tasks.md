# Tasks — Turn Loop Vertical Slice

Implementer notes: read `openspec/project.md`, `proposal.md`, `design.md` (D1–D11), and all
eight delta specs before starting. Follow `.agents/contracts/semmachina-developer.md` (TDD,
git safety, evidence-based completion). The semstreams checkout at `~/Code/c360/semstreams`
is the framework source of truth — read it, don't assume. Spec scenarios are the test list.

## 1. Upstream verification (design Open Questions — do FIRST)

- [x] 1.1 Verified GREEN (design.md F1): `conditions[].field` matches arbitrary triple
      predicates on `ENTITY_STATES`; in-tree precedent
      `configs/rules/lifecycle/01-mission-launch.json`. No upstream issue needed — design
      risk #2 retired. Constraint found: predicates are exactly three lower-kebab segments
      (F2), so `turn.phase` → `turn.phase.current` and no underscores; `transition` cannot
      fire on an entity's first write (F3)
- [x] 1.2 Verified AMBER (design.md F4/F5): `ToolDefinition.Parameters` is arbitrary JSON
      Schema so the nested banded shape holds — flattened fallback NOT needed. But `Strict`
      is provider-dependent and unreliable on the MVP runtime, so executor-side validation
      is the enforcement of record; ADR-026 documents small-model schema collapse, so the
      adjudicator starts on the mid slot
- [x] 1.3 Verified (design.md F7/F8): `input/websocket` + `output/websocket` both exist as
      an ingress/egress pair. `graph.mutation.triple.add_batch` is atomic PER ENTITY only —
      cross-entity partial success returns `FailedSubjects` with a nil error. D5 survives
      via validate-then-commit + idempotent retry; no upstream issue filed
- [x] 1.4 Pinned `github.com/c360studio/semstreams v1.0.0-beta.158`; `go build ./...` and
      `go vet ./...` clean against the full import surface the slice needs

## 2. Vocabulary and payloads

- [ ] 2.1 Create the `vocabulary` package: plausibility, risk, consequence classes,
      outcome bands, effect types, status enums, `requires_roll` mapping (D7); table-driven
      tests that every rule-matched constant set is closed and enumerable
- [ ] 2.2 Implement the five payload types (`PlayerAction`, `Verdict` with banded effect
      intents, `RollResult`, `EffectBatch`, `TurnManifest`) per the `new-payload` skill:
      registry `init()`, alias-based `MarshalJSON`, production-decoder round-trip tests

## 3. World loading

- [ ] 3.1 Implement manifest v0 parse + validation (required fields, `engine_compat`
      check) — failing tests first per world-loading spec "Manifest validation"; validate
      every predicate in `entities.jsonl` with `vocabulary.ParsePredicate` at import time
      (F9 — IDs allow `_`/uppercase, predicates do not; catch it here, not at write)
- [ ] 3.2 Implement template-local → six-part ID mapping with `local:` reference rewrite,
      dangling-reference detection, and ID limit validation; tests for deterministic
      mapping and the two-worlds scenario
- [ ] 3.3 Implement the importer materializing via Graphable → graph-ingest (never direct
      writes); integration tests: import visible via graph query, re-import is a no-op
- [ ] 3.4 Author the `fixtures/worlds/starter/` package: manifest, ~6–10 entities
      (player character, one scene/location, 2–3 items, 1–2 NPC-shaped set pieces),
      persona configs, rule pack stub — all vocabulary-valid

## 4. Dice component

- [ ] 4.1 Implement `2d6-pbta/v1` with seed = SHA-256(campaign_seed ‖ turn_id) → PCG;
      tests: band boundaries (6/7/9/10), byte-identical re-execution, distinct turns roll
      independently, no wall-clock/global-RNG usage
- [ ] 4.2 Emit the roll-result triple with mechanic/RNG versions, dice, modifiers, total,
      band; at-most-one-roll-per-turn guard test

## 5. Effect applier

- [ ] 5.1 Implement deterministic validation of the five effect types (vocabulary
      membership, per-type bounds, target existence); rejection tests for each failure
      class per effect-application spec
- [ ] 5.2 Implement whole-batch commit via `graph.mutation.*` with replace semantics and
      batch identity derived from `turn_id`; validate every intent before issuing any
      write, and treat a response naming `FailedSubjects` as failure even though the
      transport returns nil error (F7 — batches are atomic per entity, not per batch);
      tests: validation rejection leaves world unchanged, failed-subjects response fails
      the turn, idempotent re-application converges, committed effects graph-visible

## 6. Turn intake and phase management

- [ ] 6.1 Implement the intake consumer: durable consumer on the player-action stream,
      turn entity creation in `accepted`, ack-after-durable-accept; duplicate-delivery
      test (one turn, second delivery no-op)
- [ ] 6.2 Implement phase transitions as replace-writes on `turn.phase.current` with stage
      guards; tests: single-valued phase, duplicate stage trigger no-op
- [ ] 6.3 Implement explicit `failed` transitions (validation rejection, cap exhaustion)
      with recorded reasons retrievable from the turn entity

## 7. Personas and context assembly

- [ ] 7.1 Implement the context assembler component: fixed scene-scoped query (scene +
      members + 1-hop) executed at persona run time; test that post-submission state
      changes are reflected (execution-time reads)
- [ ] 7.2 Configure the adjudicator loop (mid model slot per F5) + terminal tool enforcing
      the banded verdict schema and closed classes **in the executor**, not via provider
      strict-mode (F4); emit only rule-matchable scalar triples and carry banded intents by
      reference (F6); rejection test for out-of-vocabulary exit; `MaxIterations` cap with
      explicit failure
- [ ] 7.3 Configure the narrator loop + terminal tool (prose ref + rule-opaque metadata
      only, no mutation-capable tools); prose-first-ref-last write ordering per narration
      spec

## 8. Rule pack and ledger

- [ ] 8.1 Author the turn-sequencing rule pack: accepted → adjudicator; roll-requiring
      verdict → dice; roll or no-roll verdict → applier; applied/rejected → narrator;
      narrated → complete + ledger. References only; caps on every LLM-triggering path
- [ ] 8.2 Implement the ledger writer: `CAMPAIGN_LEDGER` stream (no age/size eviction),
      one manifest per resolved turn keyed by `turn_id` (including failed turns),
      duplicate-append dropped; world-time field present (zero)
- [ ] 8.3 Implement a minimal replay reader proving replay honesty: re-execute the roll
      from a manifest byte-identically; read narration from the preserved ref

## 9. Player I/O adapters

- [ ] 9.1 Implement the WebSocket ingress adapter: normalize to `PlayerAction` (identity
      fields, arrival timestamp, channel block), publish to the stream; test that
      `player_id` survives reconnect (identity ≠ connection)
- [ ] 9.2 Implement the egress adapter: deliver narration + resolution summary to the
      channel binding; reconnect retrieval of the last completed turn from durable state
      (not adapter memory)

## 10. Composition, mock-LLM E2E, and gates

- [ ] 10.1 Compose `cmd/semmachina`: flow config wiring importer, intake, rules, personas,
      dice, applier, ledger, adapters; every payload package imported (registration
      grep-check across `cmd/`)
- [ ] 10.2 Implement the mock model endpoint (HTTP stub honoring the model-endpoint
      contract, scripted by persona role + scenario fixture; re-derived, no semdragon
      imports)
- [ ] 10.3 E2E scenarios (token-free, full production wire): no-roll turn; miss / partial /
      full band turns; invalid-effect rejection turn; duplicate action delivery;
      crash-resume at each phase (kill between roll and apply at minimum); reconnect
      retrieval; email-cadence gap (clock-independent processing)
- [ ] 10.4 CI workflow: lint (revive-clean), `go test -race ./...`, mock-LLM e2e; no live
      inference anywhere in the gate
- [ ] 10.5 Run `semmachina-reviewer` on the full slice pre-merge; resolve findings;
      update task truth conservatively with evidence
