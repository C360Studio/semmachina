# Design — Turn Loop Vertical Slice

## Context

Greenfield repo; first code. The proposal commits to an engine spike proving the turn loop
(action → adjudicate → conditional roll → validated effects → narration) on SemStreams
primitives, with turn correctness under at-least-once delivery as the center of gravity.
Constraints inherited from `openspec/project.md`: only personas read fiction (M1), closed
exit vocabulary (M2), facts-vs-requests (M3), seeded determinism (M4), bounded cognition
(M5), no hand-rolled substrate (M6), attributable spend (M7), worlds are data, player I/O
is transport-neutral, personas read current state at execution time. SemStreams is pinned
at the latest `v1.0.0-beta.*`; gaps become upstream issues.

## Goals / Non-Goals

**Goals:**

- One player completes turns in one scene with provably exactly-once world effects under
  duplicate delivery and crash-at-any-phase.
- Every contract the product thesis rests on exists in v1 form: closed verdict vocabulary,
  seeded versioned dice, effect validation boundary, append-only ledger, package-shaped
  world, transport-neutral action payload.
- The full loop runs token-free in CI via a mock model endpoint.

**Non-Goals:** everything in the proposal's Non-goals list. Additionally: no auth on the
WebSocket adapter (single local player), no scene transitions (one scene, played until the
process stops), no operator UI.

## Decisions

### D1 — Turn state: plain graph entity, not a lifecycle Participant (yet)

Each accepted action creates a **turn entity** in `ENTITY_STATES` with a single-valued
`turn.phase` predicate (`accepted → adjudicating → resolving → applying → narrating →
complete | failed`), written via `graph.mutation.*` (replace semantics) by the component
that owns each transition. Verdict, roll, effect-batch, and narration refs land as triples
on the turn entity as they are produced.

*Why not the lifecycle harness:* the harness's value (operator API, transitions table,
resume) is campaign/scene-scoped and deferred to Sequencing stage 6. A turn is short-lived
and single-owner-per-phase; a phase predicate suffices. The phase vocabulary is chosen to
be ADR-049-compatible so promoting scenes/campaigns to `Participant`s later does not
rename turn phases. *Why an entity at all:* rules chain on artifact-landing triples, but
the phase predicate is the idempotency guard, the crash-diagnosis record, and the explicit
`failed` state.

### D2 — Turn identity and idempotency

- `action_id` is assigned by the ingress adapter, deterministically derived from the
  channel-native message identity (WebSocket: client-supplied token; later channels:
  message-ID). `turn_id` is 1:1 with `action_id`. Both ride every downstream payload as
  references (correlation/causation).
- The intake consumer acks a `PLAYER_ACTIONS` message only after the turn entity exists in
  `accepted`. On redelivery, an existing turn entity for `action_id` → ack and no-op.
- Each rule-triggered stage is guarded by the phase predicate: a duplicate trigger
  observes the phase already advanced and no-ops. Single-valued predicates replace, so
  re-writing the same fact is convergent.
- The effect applier derives its batch ID from `turn_id`; a batch already recorded on the
  turn entity is not re-applied. The ledger manifest is keyed by `turn_id`; duplicate
  appends of an identical manifest are detectable and dropped.

Crash-at-any-phase resolves as: unacked action → redelivered → guard checks resume the
turn from its recorded phase, never re-running a completed stage.

### D3 — One adjudicator call: verdict declares effect intents per outcome band

The adjudicator's terminal tool emits, in a single exit: plausibility class, risk class,
`requires_roll`, modifier list (typed sources), and **proposed effect intents grouped by
outcome band** (`miss` / `partial` / `full`; a no-roll verdict uses a single `auto` band).
The dice component selects the band; the effect applier validates and commits only the
selected band's intents.

*Why:* one LLM call per turn for judgment (cost, M7), fully deterministic after the roll
(M4), and faithful to PbtA — a GM move declares outcomes before the dice hit the table.
*Alternative rejected:* a second post-roll persona call to propose effects (doubles
cost/latency, reintroduces judgment after determinism should have taken over). The
narrator never proposes effects — it voices the committed outcome (proposal's effect
split).

### D4 — Dice: `2d6-pbta/v1`, seed = H(campaign_seed, turn_id)

2d6 + summed modifiers; thresholds ≤6 miss / 7–9 partial / 10+ full. The campaign entity
records a `campaign_seed` generated at world instantiation. Per-roll seed is
SHA-256(campaign_seed ‖ turn_id) feeding a PCG PRNG (`math/rand/v2`). The roll-result
triple records mechanic version, RNG version, seed derivation inputs, dice values,
modifiers, total, and band — sufficient for byte-identical re-execution (M4) without
storing the raw seed material anywhere else.

### D5 — Effect vocabulary v1 and the applier

Effect intents are typed payloads (see D7) drawn from a closed v1 vocabulary, sized to the
starter scene: `set_attribute` (numeric/enum attribute on an entity, bounded range),
`move_entity` (location relationship), `add_relationship` / `remove_relationship` (from a
registered predicate list), `set_status` (condition enum). The applier validates intents
against the vocabulary, per-type bounds, and target-entity existence; valid intents commit
through `graph.mutation.*`; any invalid intent fails the whole batch → turn phase
`failed` with a recorded reason (no partial batches). Rejection is a normal, tested path,
not an exception.

### D6 — Campaign ledger: JetStream stream of turn manifests

`CAMPAIGN_LEDGER` is a file-backed JetStream stream (limits-based retention, no age/size
eviction — it is the archive). One message per completed (or failed) turn: a manifest
keyed by `turn_id` carrying refs — action payload, verdict, roll result, applied effect
batch, narration ObjectStore key — plus real-time stamp and a world-time field (zero in
this slice; the clock is stage 8). Prose lives in ObjectStore; the ledger carries only
references (rules-carry-references discipline applied to the archive). Replay honesty per
the proposal: deterministic parts re-execute; persona outputs replay by reading the refs.

### D7 — Payloads and vocabulary registration

New payload types (all `BaseMessage`-wrapped, registry-registered, imported in every
binary; see `new-payload` skill): `PlayerAction`, `Verdict` (with effect-intent bands),
`RollResult`, `EffectBatch`, `TurnManifest`. Closed vocabularies (plausibility, risk,
bands, effect types, status enums) are Go constants in one `vocabulary` package — the
single source the adjudicator tool schema, the applier validation, and the rule pack all
reference. Rule-matched values come only from this package (M2).

### D8 — World package and ID mapping

`fixtures/worlds/starter/` per the proposal. `manifest.yaml` v0: `id`, `name`, `version`,
`engine_compat` (semver constraint on semstreams), `description`. `entities.jsonl`: one
entity per line — `local_id`, `type`, triples using vocabulary predicates and
`local:`-prefixed references. The importer maps `local_id` →
`{org}.semmachina.{world_ns}.{template_id}.{type}.{local_id}` (org and `world_ns` from
instance config), rewrites `local:` references, validates six-part/256-byte limits, and
materializes via Graphable → graph-ingest. Same template + same namespace → identical IDs
(deterministic re-import is a no-op by replace semantics). Player binding (player entity +
played-character ref) comes from instance config, not the template.

### D9 — Ingress/egress adapters and the canonical action payload

`PlayerAction` carries: `action_id`, `player_id` (entity ID), `campaign_id`, `scene_id`,
free-text action, arrival timestamp, and a `channel` metadata block (adapter type,
reply-to address). The WebSocket input component only authenticates-by-configuration
(single player), normalizes, and publishes; the output component watches for narration
refs addressed to the player's channel binding and delivers prose + a minimal resolution
summary (verdict class, roll, band — the stage-3 resolution card's data, available early).
Reconnect replays the last completed turn's result by reading the turn entity + ObjectStore,
not from adapter memory.

### D10 — Personas and context assembly

Two agentic-loop configurations (upstream substrate): `adjudicator` (small/fast model
slot) and `narrator` (larger slot), both with `MaxIterations` caps and terminal tools
enforcing D3/D7 schemas (M5, M2). The context assembler is a component performing a fixed
scene-scoped graph query at persona execution time — scene entity, its member entities,
1-hop relationships, current turn artifacts — explicitly not thematic retrieval. No
snapshot travels with the action (execution-time reads discipline).

### D11 — Mock-LLM harness at the model-endpoint boundary

CI runs the production loop, tools, registry, and wire paths against a local HTTP stub
implementing the model-endpoint contract, returning scripted responses keyed by persona
role + scenario fixture (re-derived pattern, no semdragon imports — M6). Scenarios cover:
no-roll turn, each outcome band, invalid effect intent (rejection path), duplicate
delivery, crash-resume at each phase. Live-model runs are a manual flow config swap
(`model_registry` retarget), never required by CI.

## Risks / Trade-offs

- [Beta API drift until semstreams v1] → pin exact beta tag; file upstream issues instead
  of workarounds; the spike touches broad substrate surface early, which is the point.
- [Rule engine's typed `ENTITY_STATES` evaluator may not match game predicates
  (turn.phase, verdict class) without upstream support] → verify against semstreams
  source FIRST (developer contract); if game triples can't be rule-matched, that is an
  engine ask filed upstream, not a local rule-engine workaround. This is the spike's
  biggest unknown.
- [Terminal-tool schema expressiveness for banded effect intents] → verify agentic tool
  schema support early; fallback is flattening bands into three tool parameters, not
  loosening the closed vocabulary.
- [Prose write + ref-triple commit are two writes; crash between them orphans prose] →
  write ObjectStore first, ref last; orphans are harmless and collectable; never the
  reverse order (a ref to missing prose is a correctness bug, an orphan is garbage).
- [Ledger stream as long-term archive has no retention ceiling] → acceptable
  instance-per-world at slice scale; durable archive/export policy is a stage-4+ concern
  and possibly an upstream ask.
- [One adjudicator call must anticipate outcome bands, which strains small models] →
  mitigated by schema-constrained tool exit + mock-first development; if quality is
  insufficient at the 4B slot, move adjudicator to the mid slot before changing the
  architecture.

## Migration Plan

Greenfield; nothing to migrate. Local run: NATS (Docker) + `cmd/semmachina` with the
starter world fixture; CI runs unit + mock-LLM e2e. Rollback = git revert; no persistent
state contract to preserve until a campaign is worth keeping (pre-v1 clean-break policy
applies to any NATS state).

## Open Questions

- Exact upstream surfaces to confirm at implementation start (read semstreams source, per
  developer contract): typed rule-evaluator predicate coverage; agentic terminal-tool
  schema nesting; WebSocket input/output component current state (engine gap #3 is about
  hardening, but confirm basic bidirectional support); graph.mutation batch semantics
  (atomicity of a multi-triple commit — affects D5's no-partial-batches guarantee).
- Whether the intake component and effect applier are one component or two (both are
  thin; decide at implementation by port topology, not architecture).
- `world_ns` allocation format (slug rules, collision policy) — trivial single-instance,
  worth one paragraph in the `world-loading` spec.
