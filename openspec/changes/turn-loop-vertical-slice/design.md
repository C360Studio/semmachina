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
`turn.phase.current` predicate (`accepted → adjudicating → resolving → applying → narrating →
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

Two agentic-loop configurations (upstream substrate): `adjudicator` (**mid** model slot per
F5 — it carries the slice's only schema-bearing exit; demoting it to the small/fast slot is
a later optimization, not the starting configuration) and `narrator` (larger slot, prose
only — no schema burden), both with `MaxIterations` caps and terminal tools
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

### D12 — Roll-gate authority: the adjudicator decides, the mapping advises

`requires_roll` is a closed-vocabulary mapping over (plausibility, risk) — `roll = plausibility ∈
{unlikely, plausible} AND risk ≠ none` — but that mapping is **advisory, not authoritative**. The
adjudicator's reported `requires_roll` is trusted; a verdict whose reported gate disagrees with the
mapping is valid and proceeds, with the disagreement recorded as structured data for the ledger and
for quality metrics.

*Why:* the project's founding claim is that narrative positioning dictates mechanics. A deterministic
two-axis lookup deciding when the dice come out is rules-first — it is precisely the thing a
fiction-first engine exists not to do. A PbtA GM decides that a move triggers by reading the fiction,
not by consulting a table.

*What this costs:* a persona can decline the dice, so the roll gate is no longer a structural
guarantee. That is accepted deliberately. The compensating controls are elsewhere and remain
structural: effect intents are still drawn from the closed vocabulary and validated by the applier
(D5), modifiers are bounded so a verdict cannot pre-determine a band, and the dice remain
seeded-deterministic (D4). The persona chooses *whether* the dice are consulted; it cannot choose
what they say, nor what the world does afterward.

*Band shape follows the reported value*, so the engine never fabricates bands the adjudicator did not
author — a verdict reporting no roll declares exactly one `auto` band, and the recorded disagreement
tells us whether the persona's fiction judgment tracks the mapping over time. If it diverges wildly
in play, that is data about the persona or the vocabulary, not a reason to re-take the authority.

## Risks / Trade-offs

- [Beta API drift until semstreams v1] → pin exact beta tag; file upstream issues instead
  of workarounds; the spike touches broad substrate surface early, which is the point.
- ~~[Rule engine's typed `ENTITY_STATES` evaluator may not match game predicates
  (turn.phase, verdict class) without upstream support]~~ **RESOLVED — see F1.** Arbitrary
  triple predicates are matchable on `ENTITY_STATES` with an in-tree precedent; no upstream
  ask. Residual constraint is naming only (F2, three-segment lower-kebab).
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
  **sharpened by F5**: upstream (ADR-026) already hit schema-adherence collapse on small
  models, so the adjudicator starts on the mid slot and the tool-boundary validator — not
  provider strict-mode (F4) — is the enforcement of record. Mock-first development plus
  per-tool retry policy absorbs the residual; dropping to the 4B slot is an experiment run
  after the loop is green, never the default.

## Migration Plan

Greenfield; nothing to migrate. Local run: NATS (Docker) + `cmd/semmachina` with the
starter world fixture; CI runs unit + mock-LLM e2e. Rollback = git revert; no persistent
state contract to preserve until a campaign is worth keeping (pre-v1 clean-break policy
applies to any NATS state).

## Upstream Verification Findings (task group 1, semstreams v1.0.0-beta.158)

Verified against the semstreams checkout at the pinned tag. No upstream issues filed —
every gap resolved into a local design refinement rather than an engine ask.

### F1 — Game predicates are rule-matchable (design risk #2 closes GREEN)

`conditions[].field` is an arbitrary triple predicate path; the evaluator looks up the
predicate in the entity's triples and compares the object value
(`processor/rule/docs/conditions.md`). Rule entity-watching supports `ENTITY_STATES` and
only `ENTITY_STATES` (`processor/rule/entity_pattern_contract.go:39`) — exactly where D1
puts turn state. In-tree precedent: `configs/rules/lifecycle/01-mission-launch.json`
matches `mission.command.requested == "launch"` on a graph-ingest-written entity and fires
a lifecycle action — structurally identical to `turn.phase.current == "accepted"` → spawn
adjudicator. Operators cover eq/ne/lt/lte/gt/gte/between, string contains/prefix/suffix/
regex, bool eq/ne, in/not_in/array_contains/length_*, plus a `transition` operator with
`from` sets (a declarative FSM guard). Bootstrap replay with `on_recovery` supplies
restart resume. **No engine ask; the spike's biggest unknown is retired.**

### F2 — Predicates are exactly three segments (corrects D1/D7 naming)

`vocabulary/predicate_contract.go` rejects any predicate without exactly three segments
(`PredicateReasonArity`); each segment is lower-kebab ASCII `[a-z][a-z0-9]*(-[a-z0-9]+)*`,
≤64 bytes. This is enforced fail-closed at the ENTITY_STATES write gate
(`graph.ValidateEntityPredicates`, and `validateTriplePredicates` on the batch path), so a
two-segment predicate is rejected at write, not silently stored. Consequences: `turn.phase`
becomes **`turn.phase.current`** throughout, and **underscores are illegal in predicates** —
`requires_roll` stays a payload/tool field but its triple form is
`turn.verdict.requires-roll`. All rule-matched predicates in the rule pack must be
three-segment lower-kebab.

### F3 — `transition` cannot fire on an entity's first write (affects D2 guards)

A `transition` condition returns false on first evaluation because it needs a recorded
previous value. A turn entity created directly in `accepted` therefore never fires a
`transition`-guarded rule on its creating write. The accepted → adjudicating trigger uses
`eq` + `on_enter` (edge-triggered by rule match state); `transition` is available only for
later phase hops where a prior value exists.

### F4 — Terminal-tool nesting is expressible but schema enforcement is not guaranteed

`agentic.ToolDefinition.Parameters` is `map[string]any` — arbitrary JSON Schema, so D3's
banded shape is expressible and the flattened three-parameter fallback is not needed on
expressiveness grounds. But `Strict` enforcement is provider-dependent
(`agentic/tools.go:28`): honored on OpenAI/vLLM/OpenRouter, **silently ignored on Anthropic
and Gemini**, best-effort and model-dependent on Ollama. Strict additionally requires
`additionalProperties:false` at every level, every property listed in `required`, and max
nesting 5. **Therefore tool-boundary validation in our executor is load-bearing, not
decorative** — it is the only enforcement that holds on the MVP's local runtime.
`agentic.MetadataKeyDecideActionAllowlist` is the ready-made closed-vocabulary seam: a
rule-supplied allowlist validated in the executor, rejecting non-members with
`ToolErrorInvalidArgs` so the model can self-correct on retry (M2).

### F5 — Upstream has already lived D3's small-model risk (sharpens D10)

ADR-026: *"requiring every agent to submit via a schema-enforced terminal tool breaks on
small models. Schema adherence fails; retries eat iteration budget; flows stall. Semspec
lived this."* Their resolution — concentrate schema discipline in one role and run that
role on a stronger model — is what D3 already does (one adjudicator call is our only
schema-bearing exit; the narrator returns prose). But **D10's placement of the adjudicator
on the small/fast slot is the exact configuration ADR-026 warns about.** Design change: the
adjudicator starts on the **mid** model slot; moving it down is the optimization, not the
default. `agentic-loop`'s `SynthesizeTerminalOnCompletion` and per-tool retry policy
(`agentic-tools.tool_retries`) are the cheap-substrate safety nets.

### F6 — Verdict triples carry scalars; banded intents ride as a reference

The `decide` executor (`processor/agentic-tools/decide.go:123`) publishes only a small set
of rule-matchable metadata triples onto the loop entity and keeps bulky fields in the
result Content for `read_loop_result`. D3's banded effect intents are bulky and are never
rule-matched (only the applier consumes them), so they follow that precedent: the rule-
matched scalars (`turn.verdict.plausibility`, `turn.verdict.risk`,
`turn.verdict.requires-roll`) land as triples; the banded intent set lands as a reference.
This is the rules-carry-references discipline applied to the verdict.

### F7 — Batch mutations are per-entity atomic, not per-batch (refines D5)

`graph.mutation.triple.add_batch` groups triples by Subject and issues **one CAS per
entity**; cross-entity partial success is a first-class documented outcome returning
`FailedSubjects` with a **nil Go error** (`processor/graph-ingest/mutations.go:373`).
Triple-level adds are also must-exist (ADR-055): a triple targeting an absent entity is
rejected into `FailedSubjects`. D5's no-partial-batches guarantee survives for the case it
was written for — *validation* rejection — because the applier validates every intent
before issuing any write. It does not survive an infrastructure-level partial commit. Two
binding requirements follow: (a) the applier MUST inspect `FailedSubjects` on every batch
response and treat non-empty as failure — a nil error is not success, and this is the
sharpest bug magnet in the slice; (b) recovery is idempotent re-application keyed on
`turn_id` under replace semantics, which D2 already specifies. Substrate-level multi-entity
atomicity would be an upstream ask; D5 does not need it and none is filed.

### F9 — Entity IDs and predicates have different alphabets (trap for D8's importer)

The two identity contracts are not the same shape, and the difference is invisible until a
write fails. Entity ID segments (`pkg/types/entity_id.go:200`) require an alphanumeric
first byte and then allow alphanumerics, `_`, and `-` — **uppercase and underscores are
legal** — bounded at six parts and 256 total bytes. Predicate segments (F2) are strictly
lower-kebab with **no underscore**. So a world author's `local_id: "rusty_sword"` maps to a
perfectly valid entity ID, while a predicate `item.condition.rust_level` in the same
package is rejected at the write gate. The importer's validation therefore applies two
distinct rules — `types.ValidateEntityID` for mapped IDs, `vocabulary.ParsePredicate` for
every predicate in `entities.jsonl` — and must reject bad predicates at import time with a
reason naming the offending line, rather than letting them fail later at materialization.

### F8 — Environment notes

`input/websocket` and `output/websocket` both exist as separate components — bidirectional
play is an ingress/egress pair (matching D9's split), not one duplex component.
`ENTITY_STATES` is created with no TTL and an explicit ADR-068 guardrail against age/size
eviction (`processor/graph-ingest/component.go:1128`), matching the project invariant;
`processor/rule/docs/entity-watching.md` still documents a 7-day TTL and is stale doc drift
(candidate upstream doc issue, not a blocker). Pinned `v1.0.0-beta.158`; `go build` and
`go vet` clean against the full import surface the slice needs.

## Open Questions

- Whether the intake component and effect applier are one component or two (both are
  thin; decide at implementation by port topology, not architecture).
- Are `risk: none` and `consequence: none` independent, or the same claim? Both are
  rule-matched scalars, so an incoherent pair is a value a future rule pack can match on.
  Either constrain them (`RiskNone ⟺ ConsequenceNone`) or document why they differ.
- Does `consequence` describe the miss shape, the partial shape, or the worst case? In PbtA
  those differ (`cost` is a 7–9 shape; `harm`/`setback`/`escalation` are 6− shapes). No
  consumer reads it in this slice; the meaning must be stated before a rule pack matches it.
- Plausibility and risk carry four values each but only two bits of behavior today, and
  `ModifierPosition` lets the adjudicator express positioning a second time as a number —
  a verdict can double-penalize with nothing noticing. Mapping plausibility to a
  deterministic modifier would give the middle values real work and remove the double-count.
- `world_ns` allocation format (slug rules, collision policy) — trivial single-instance,
  worth one paragraph in the `world-loading` spec.
