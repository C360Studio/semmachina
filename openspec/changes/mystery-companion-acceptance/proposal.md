# Mystery Companion Acceptance

## Why

The turn-loop spike can move fiction through the engine, but its audience-agnostic
context would expose authored mystery truth and it has no reusable way to model a
companion. The Bellweather case is the smallest product-shaped acceptance proof that
the engine can preserve a mystery while a bounded, knowledgeable companion participates.

## What Changes

- Add an immutable authored mystery case with a canonical solution, ordered timeline,
  six suspects, and twelve clues or red herrings.
- Separate canonical truth, character belief, actor knowledge, and narration revelation,
  and project only audience-authorized state into every persona request.
- Add a casekeeper persona that interprets natural-language investigation into a closed
  `CaseDecision`; rules and deterministic components grant knowledge, verify accusations,
  and advance the case lifecycle.
- Add a durable player-companion bond, actor-scoped companion knowledge, bounded
  initiative, a three-step hint ladder, and a closed `CompanionDecision` contract. Kit
  Finch is the first fixture, not a hard-coded role.
- Extend turn sequencing with durable `interpreting` and `companion` phases while
  preserving deterministic no-model-call behavior for turns that need neither stage.
- Extend terminal player results with a structural companion summary while keeping
  companion dialogue in ordinary narrator prose.
- Prove the entire case with the existing mock-model E2E; keep Gemini as an opt-in live
  smoke outside CI.

## Capabilities

### New Capabilities

- `mystery-case`: authored case truth, structured investigation decisions, lifecycle,
  clue disclosure, and deterministic accusation verification.
- `epistemic-projection`: canonical truth, belief, actor knowledge, revelation receipts,
  and a centralized audience/purpose selector for persona-safe context.
- `companion-support`: durable companion bonds, scoped knowledge, bounded initiative,
  structured decisions, hint progression, and public resolution summaries.

### Modified Capabilities

- `world-loading`: validate and import immutable mystery, knowledge, and companion data.
- `turn-sequencing`: add durable interpretation and companion phases with recovery and
  deterministic no-op behavior.
- `fiction-adjudication`: keep ordinary adjudication on a public, audience-safe projection.
- `narration`: restrict narration context by purpose and voice committed companion and
  case outcomes without deciding them.
- `player-io`: expose the closed companion-resolution summary in the canonical result.

## Non-goals

- Full epistemic simulation: no belief revision, contradiction reconciliation, stale or
  probabilistic beliefs, multiplayer knowledge sharing, or universal guarantee that a
  model cannot infer a secret from public clues.
- Full NPC cognition: no autonomous goals, schedules, inventories, off-screen life,
  NPC-to-NPC cascades, or independent NPC prose generation.
- Multiple active companions, companion switching, or trust and affinity systems.
- Creator UI, place ontology or maps, world clock, chronicler, continuity checker, and
  general campaign lifecycle work.
- Live-model inference in CI; Gemini remains an explicit, paid smoke run.

## Classification

This is **game-repo work**: personas judge fiction, rules match closed structured
decisions, components execute bounded projection, grants, hints, and exact-ID
verification, and a case lifecycle owns phase. It composes existing SemStreams
primitives; any substrate gap found during implementation becomes an upstream issue
rather than a local engine workaround.

## Impact

- Adds mystery and companion vocabulary, payloads, lifecycle registration, rules,
  deterministic components, audience-safe persona assembly, and player/v1 fields.
- Adds `fixtures/worlds/bellweather-maze/` as the acceptance world package.
- Replaces the current audience-agnostic scene view as a direct persona input; operator
  graph access remains complete.
- Pulls a minimum epistemic and companion safety proof ahead of template/place work
  without declaring the roadmap's full epistemic or NPC-cognition stages complete.
