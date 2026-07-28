# Tasks — Turn Loop Vertical Slice

Implementer notes: read `openspec/project.md`, `proposal.md`, `design.md` (D1–D11), and all
eight delta specs before starting. Follow `.agents/contracts/semmachina-developer.md` (TDD,
git safety, evidence-based completion). The semstreams checkout at `~/Code/c360/semstreams`
is the framework source of truth — read it, don't assume. Spec scenarios are the test list.

## 1. Upstream verification (design Open Questions — do FIRST)

- [ ] 1.1 Verify the typed rule evaluator can watch/match game predicates on `ENTITY_STATES`
      (turn phase, verdict class triples); record findings in design.md; file an upstream
      semstreams issue if game triples cannot be rule-matched (design risk #2 — do NOT
      work around locally)
- [ ] 1.2 Verify agentic terminal-tool schema support for the banded verdict exit (D3);
      if nesting is unsupported, adopt the flattened three-parameter fallback and note it
      in design.md
- [ ] 1.3 Verify WebSocket input/output component bidirectional support and the
      graph.mutation batch atomicity semantics (affects D5 no-partial-batches); record
      findings and file upstream issues for gaps
- [ ] 1.4 Pin `github.com/c360studio/semstreams` to the latest `v1.0.0-beta.*` tag in
      go.mod and confirm the e2e-relevant packages compile against it

## 2. Vocabulary and payloads

- [ ] 2.1 Create the `vocabulary` package: plausibility, risk, consequence classes,
      outcome bands, effect types, status enums, `requires_roll` mapping (D7); table-driven
      tests that every rule-matched constant set is closed and enumerable
- [ ] 2.2 Implement the five payload types (`PlayerAction`, `Verdict` with banded effect
      intents, `RollResult`, `EffectBatch`, `TurnManifest`) per the `new-payload` skill:
      registry `init()`, alias-based `MarshalJSON`, production-decoder round-trip tests

## 3. World loading

- [ ] 3.1 Implement manifest v0 parse + validation (required fields, `engine_compat`
      check) — failing tests first per world-loading spec "Manifest validation"
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
      batch identity derived from `turn_id`; tests: atomicity (reject leaves world
      unchanged), idempotent re-application, committed effects graph-visible

## 6. Turn intake and phase management

- [ ] 6.1 Implement the intake consumer: durable consumer on the player-action stream,
      turn entity creation in `accepted`, ack-after-durable-accept; duplicate-delivery
      test (one turn, second delivery no-op)
- [ ] 6.2 Implement phase transitions as replace-writes on `turn.phase` with stage guards;
      tests: single-valued phase, duplicate stage trigger no-op
- [ ] 6.3 Implement explicit `failed` transitions (validation rejection, cap exhaustion)
      with recorded reasons retrievable from the turn entity

## 7. Personas and context assembly

- [ ] 7.1 Implement the context assembler component: fixed scene-scoped query (scene +
      members + 1-hop) executed at persona run time; test that post-submission state
      changes are reflected (execution-time reads)
- [ ] 7.2 Configure the adjudicator loop + terminal tool enforcing the banded verdict
      schema and closed classes at the tool boundary; rejection test for out-of-vocabulary
      exit; `MaxIterations` cap with explicit failure
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
