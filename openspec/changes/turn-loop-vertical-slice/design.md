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

### F10 — Rule conditions need predicate *registration*, not just canonical form (amends F1)

F1 established that rules can match arbitrary triple predicates. It missed a step: rule
config validation calls `vocabulary.RequireDeclaredPredicate` on every condition field and
action predicate (`processor/rule/config_validation.go:293,357`), which rejects a predicate
that is canonical but absent from the upstream vocabulary registry. So `turn.phase.current`
parses fine and still fails rule load. **The turn-sequencing rule pack will not load until
every SemMachina predicate is registered via the exported `vocabulary.RegisterPredicate` at
bootstrap.** This is not an upstream ask — the API is public and registration is ours to do
— but it is a hard prerequisite for group 8, and it means the predicate constants in
`internal/vocabulary` need a registration pass alongside them.

The same `PredicateMetadata` carries a `RuleOpaque` flag, and rule validation rejects any
rule whose condition field is flagged (`config_validation.go:214,298`). That is **M1
enforced by the substrate rather than by discipline**: registering narration refs, entity
descriptions, and verdict rationale as rule-opaque makes "no rule branches on fiction" a
load-time failure instead of a code-review convention. Adopt it in group 8 with the
registration pass — it is the cheapest structural win available to the fiction boundary.

### F11 — Referenced entities exist as stubs before their own data lands

graph-ingest creates a referenced entity as a **stub** — queryable, carrying only
`core.identity.*` markers and none of its own facts — as soon as another entity references
it. Discovered by a real-infrastructure re-import test, not by reasoning; a mocked
graph-ingest would never have shown it. `EntityState.IsStub()` is the envelope-based
discriminator (the marker triple persists after birth, so triple-counting is not a valid
test). Consequence for the slice: **any readiness check that treats "the ID resolves" as
"the entity is loaded" will read half-entities** — relevant to boot/world-ready checks in
task 10.1 and to the context assembler in 7.1, which must not hand a persona a stub.

**Two refinements found while building the context assembler.** First, **only the create and
fact-arrival lanes mint referential stubs** — `ensureRelationshipTargetsExist` runs on
`entity.create`/`create_with_triples` and the fact path, and upstream states explicitly that
the merge lane does not. So merging a *new* reference onto an existing entity leaves the
target **genuinely absent**, not stubbed: anything reasoning about "referenced but unborn"
must know which lane created the reference. Second, `IsStub()` is
`MessageType.Equal(StubMessageType)` — it keys on the stub envelope, not on a zero or
missing one, so an entity with a malformed envelope is *not* a stub (upstream pins this with
`TestEntityState_IsStub_KeysOnEnvelopeNotTriple`). The envelope check is still the right
discriminator; the reason it works is narrower than "any invalid envelope".

Related, and a genuine sharp edge: re-import convergence and destructiveness are the same
mechanism. `graph.MergeTriples` replaces by (subject, predicate), so re-importing a template
into a *living* campaign resets every predicate the template declares — including
relationships play has since changed. The no-op guarantee holds only for an unchanged world.
A template-update policy is a separate decision, deliberately out of this slice.

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

### F23 — A single-turn replay test cannot see call-order dependence

The dice purity scan bans a **PCG-typed field** on the roller, not *state in general* — so a
plain `int` call counter mixed into the seed passes it. And a replay test that plays **one**
turn cannot catch that either: producer and replayer are each making their first call, so
they agree. Golden vectors do not help when the perturbation leaves call one untouched.

The fix is cheap and belongs in any determinism test: play **several** turns through one
producer and replay them in a **different order**. That turns call-order dependence from
invisible into a failing diff, and it is the only shape that distinguishes "this function is
deterministic" from "this function agrees with itself when asked the same way twice".

The general lesson, third of its kind here: a structural scan proves the absence of the
mechanisms it names, never the absence of the property you want. It is a good tripwire and a
bad proof. The behavioral test has to carry the claim.

### F22 — `on_recovery` rescues a crashed *stage*, never a stranded *phase*

The bootstrap replay fires `on_recovery` only for a rule that is **currently matching** at
boot — upstream requires `Bootstrap && hadPrevState && currentlyMatching` together. A turn
parked in `adjudicating` with no verdict matches **no rule in the pack**, because every
mid-chain rule is a conjunction of a phase and an artifact (F21) and the artifact is exactly
what is missing. Proven by probe, not inference: a turn parked mid-stage, a second rule
processor booted against the same broker, twenty seconds, no re-trigger.

So the real resume mechanism is **JetStream redelivery of an unacked stage trigger**, which
is durable and correct — but it only covers a stage that crashed *while holding its
trigger*. The persona stages ack after publishing the task, so once the task is out there is
no unacked trigger and no matching rule, and nothing at boot reconciles the turn.

Consequences worth stating plainly, because two decisions rested on the false claim:

- A persona loop that fails for **any reason other than its cap** — a model error, far more
  common than cap exhaustion — strands the turn permanently with a player waiting. The
  bounded-execution requirement's "never a silent stall" is **not met** for that class today.
- A missed loop-failure notification has the same outcome for cap exhaustion, which
  undercuts the argument that a best-effort core-NATS notification is safe because recovery
  replay would catch what it drops.

The fix is a reconciliation pass, not more comments: something must find turns whose phase
is non-terminal and whose stage is not running. Anything that "resumes on the next boot"
should be assumed absent until a probe says otherwise — this is the second time a recovery
story has been believed on the strength of a plausible mechanism name.

### F21 — A mid-chain rule gated on the phase alone races the stage it follows

Every phase except `accepted` is written on stage **entry**, so a rule matching
`phase == resolving` fires as the dice stage *starts*, not when it finishes. The next stage
then races the previous one for the artifact it needs. The race is almost always won — the
previous stage writes in microseconds while the trigger travels a KV watch, a rule
evaluation, a publish and a consumer — so **the defect passes every test and appears under
load**, which is the worst possible signature.

The fix is a load-time gate: a mid-chain rule must match on the **artifact** the previous
stage produces, not merely on the phase it entered. Two exemptions are principled rather
than convenient — the first hop, because `accepted` is written by intake's atomic create and
is therefore a finished fact rather than an entry marker; and `transition` conditions, which
fire on the phase *move* rather than on its presence.

This is the same shape as the resume guard (a phase cannot distinguish "entered" from
"finished"; the artifact ref can), arriving at the rule layer instead of the persona layer.
Worth stating as a general rule for any future pack: **phases sequence, artifacts gate.**

### F20 — An undeclared model capability silently resolves to the default model

`model.Registry.Resolve` returns `Defaults.Model` for any capability absent from its map, so
`ResolveEndpoint` **succeeds** for a capability nobody configured. A deployment that forgets
to declare `fiction_adjudication` therefore runs the adjudicator on whatever the default
happens to be, reports nothing, and looks healthy. That is precisely F5's failure — the
schema-bearing persona on the small slot — arriving through a fallback rather than a
decision, which is worse, because a decision leaves a trace.

Boot must therefore assert the capability is **declared**, not merely that it resolves.
Every persona's model slot is a deliberate configuration choice; silently inheriting the
default is not a degraded mode, it is an unnoticed one.

**Related, and it strengthens F4 with a structural argument:** the banded verdict cannot be
strict-mode-conformant regardless of provider support. Strict mode requires every property
listed in `required`, while the band set is a discriminated union — `miss`/`partial`/`full`
XOR `auto` — so a conformant schema would compel the model to author bands D12 requires the
engine to refuse. Executor-side validation is the enforcement of record for two independent
reasons now: providers may ignore strict mode, and this schema could not use it anyway. The
narrator's flat schema *does* satisfy the subset and uses it, deliberately confined to a
conservative keyword set — a strict schema carrying a keyword some runtime rejects returns a
400 that no token-free test can see and a live run discovers on turn one.

### F18 — A tolerant client hides exactly the defects a mock exists to produce

The framework's model client normalizes: it recomputes `total_tokens`, infers a tool call
from the presence of `tool_calls` regardless of `finish_reason`, and substitutes `{}` for
both empty and malformed tool arguments. Every one of those is correct client behavior and
every one of them makes a **wrong response indistinguishable from a right one when asserted
through the client**. Three mock mutations survived on exactly that basis — a zeroed token
total, a `stop` finish reason on a tool call, and truncated argument bytes — and were only
caught once the assertions moved to the wire.

The rule that follows, and it applies to 10.3's E2E as much as to the mock: **assert
provider shape at the wire, not client-observable behavior through the client.** A mock
whose fidelity is only ever checked through a tolerant consumer will drift into emitting
responses no real provider would send, and the first live-model run is where that surfaces.

### F19 — The mock cannot choose an outcome band; the seed does

A verdict declares intents for *all three* bands and the seeded dice select one (D3), and
modifier sums are bounded to `[-2, +4]` precisely so a verdict cannot pre-determine the
result. So `miss` / `partial` / `full` are not scriptable at the model boundary — a scripted
verdict differs across bands only in the narrator's voice.

Consequence for 10.3: the per-band E2E scenarios are selected by supplying the
**(campaign_seed, turn_id) pair whose derived roll lands in the wanted band**, not by
scripting the model. That is seed search, done once and pinned as fixture constants. It is
also a small proof of the determinism claim in its own right — if the pairs stop producing
their bands, seeded replay has broken.

### F17 — Flat per-turn cost is true of tokens and not yet of bytes on the wire

Context assembly is where the flat-per-turn-cost claim is actually enforced: a fixed
scene-scoped query, capped before each batch read, refusing rather than truncating (a
persona handed part of a room narrates a room that is not there). That bound holds — the
assembled context is scene-bounded regardless of world size.

But `turn.action.scene` accumulates on the scene's incoming index: one edge per turn ever
taken there. The assembler filters them out, so *context* stays bounded, while the incoming
*query* returns O(campaign history) edges every turn. Per-turn token cost is flat; per-turn
retrieval transport is not. "Bounded context" and "bounded retrieval" are different claims,
and only the first is currently true.

**And the curve is a cliff, not a slope.** The index reply is a single NATS message, so at
roughly 100 bytes per entry the default 1 MB `max_payload` is reached near ten thousand
accumulated edges — after which the membership read fails outright and **the scene becomes
unassemblable**. Not "slower": unplayable, in the scene the campaign has used most.

Stage 2's place ontology reduces it rather than dissolving it: if scenes are durable
entities carrying a place reference, the location's incoming index accumulates one edge per
scene ever played there — the same shape at coarser granularity, per-scene instead of
per-turn. Note also that dissolving it requires **the assembler to re-key its membership
read onto the location**; adding a membership predicate alone changes nothing while the
reverse lookup is still keyed on the scene.

The structural fix is available now and is an engine ask (M6), not a local workaround: the
incoming index key is a fixed-position layout and the underlying key filter supports
positional wildcards, so a **predicate filter on the incoming query** is expressible — which
would bound the wire payload by membership instead of by campaign history.

### F16 — A runtime gate over proposals is not an authoring gate over data

`vocabulary.AllowsObjectKind` was registered so two consumers could share one rule, and for
a while had exactly one caller: the effect applier. The importer never consulted it, so a
template could author `world.location.current: local:crowbar` — a character located *inside
an item* — and import cleanly. The applier validates *proposed intents*; it has no opinion
about facts that arrived as authored data.

The failure mode is worse than "unenforced". For a multi-valued predicate the applier seeds
each write from the entity's current objects, so an invalid sibling is republished as part
of the complete set the merge lane demands — a write the applier issues and is told
succeeded, carrying a fact it would have refused as an intent. For a single-valued
predicate it is never republished at all: it simply sits in `ENTITY_STATES` as an invalid
fact that retrieval, the map view (stage 3), and the continuity checker (stage 5) all read
as truth. Neither shape is caught by the runtime gate, and only the import gate fixes both.

The general rule, which matters increasingly as user-authored content lands: **every
constraint on world data needs an enforcement point at authoring time, and a runtime
validator over persona output is not it.** Our own comment on the rule said it — "a rule
that important, re-derived per consumer, is a rule that ends up enforced on one path and
not the other" — while describing itself.

### F15 — KV revision is unreachable from the query surface, so the multi-valued fold has no CAS

Writing a multi-valued predicate requires read-modify-write (F14's third face), and that
fold is unprotected against a concurrent writer of the same predicate. The mutation request
supports `ExpectedRevision` for single-pass CAS-on-condition, but the entity query surface
never returns a revision to supply: `handleQueryEntityNATS` reads `entry.Revision` for its
own validation and discards it, and no query response type carries it
(`processor/graph-ingest/query.go`).

Not a problem in this slice — one player, one turn at a time, stage-guarded by the turn
phase — so nothing here works around it. It becomes real the moment two writers touch one
entity's relation set (NPC cognition, stage 9, is the obvious arrival). **This is an engine
ask, not a local retry loop** (M6): the fix is exposing the read revision on the query
response so an RMW caller can close the loop with the CAS the mutation side already offers.

The asymmetry is the whole argument for the ask: **`MutationResponse.KVRevision` is
returned after every write, while the read path discards the revision it already loaded.**
The write side exposes exactly what the read side withholds, so CAS is unreachable only
because of where the value stops.

The same gap makes the turn's phase guard **convergent rather than mutually exclusive** —
`Advance` reads a phase then writes one, and two writers reading the same phase both pass.
Nothing here works around that either; upstream has the identical shape in
`processor/gated-dag/claim.go`, which documents "mutual exclusion comes from single-flight
execution, not from this write" and marks a CAS-UPGRADE POINT. We follow that precedent and
mark ours the same way. It holds while the slice is single-player and single-flight, and it
is the second thing that closes when the read revision is exposed.

### F14 — The triple-add lane APPENDS; only the entity lane replaces (corrects F7)

F7 established that `graph.mutation.triple.add_batch` is atomic per entity and surfaces
`FailedSubjects`. It missed the more dangerous property: **that lane appends.**
`Component.AddTriples` does `entity.Triples = append(entity.Triples, group...)`
(`processor/graph-ingest/component.go`), while `graph.MergeTriples` — newer-wins replacement
by (subject, predicate) — is used only on the entity merge path. Proven against a live
broker, not inferred: writing the same roll triples twice through the add lane leaves **two
`turn.roll.band` values on one turn**, with a success response, empty `FailedSubjects`, and
no error anywhere.

This is a correctness trap for every single-valued predicate in the slice, and two specs
were written assuming the wrong lane:

- **D1 / turn-sequencing** — "phase writes SHALL replace, never append". Through the add
  lane a duplicate stage trigger yields a turn holding two phases and no error, which
  defeats the phase predicate as an idempotency guard.
- **D5 / effect-application** — "single-valued predicates replacing prior values". Through
  the add lane a character accumulates health values instead of changing health.

So: **single-valued writes go through the entity merge lane
(`graph.mutation.entity.update_with_triples`), never `triple.add`/`triple.add_batch`.** The
add lane remains correct for genuinely multi-valued accumulation, of which this slice has
none. Note the merge lane replaces a multi-valued predicate as a whole set (gh#466), so a
writer owning a multi-valued predicate must publish the complete set per write.

**F14's third face is silent data loss, and the starter world reaches it.** The merge lane
replaces a predicate's **whole value set**: upstream states it outright — *"For a
MULTI-valued predicate, send the FULL desired set — a partial set drops the omitted
siblings"* (`graph/mutation_requests.go`). Rook carries a crowbar and a lantern; an
`add_relationship` that merges only the new object leaves him carrying only that object,
with a success response and no error. So a multi-valued write reads the current set and
publishes the complete result. The mirror case: *"Predicates absent from AddTriples are
untouched"*, so emptying a predicate needs an explicit removal — "put down the last thing
you were carrying" committed as an add-list omission would commit as "still carrying it".
Proven by mutation against the live broker: the naive merge left `[rations]` where
`[crowbar lantern rations]` was intended, deleting two items.

**The merge lane is per-entity, and its multi-entity behavior is the same trap wearing a
different hat.** `UpdateEntityWithTriplesResponse` carries no `FailedSubjects` — that field
exists only on the batch add response — and graph-ingest splits any triple whose subject is
not the request's entity off to `appendForeignEdges`, which calls `AddTriples` (**the
appending lane**) and logs failure as a WARN without returning it. So a single merge request
spanning two entities replaces on the first and appends on the second, silently. A
multi-target write is therefore N merge calls, one per target, each with its own classified
error — which also means a multi-entity effect batch is still not atomic, so F7's
conclusion survives F14 by a different mechanism. `internal/graphio` refuses a foreign
subject locally rather than trusting graph-ingest to route it.

### F13 — The instantiation gate is one atomic create, borrowed from KV config seeding

semstreams already solves "seed declared state without clobbering live state":
`ConfigManager.SeedFromRuntime` writes file-loaded rules into KV **using Create, not Put, so
operator edits already in KV are never overwritten — key-already-exists is a no-op**
(`processor/rule/kv_config_integration.go`); `flowstore.Manager.Create` uses the same idiom.
That maps onto world instantiation exactly: template entities are the file-loaded rules,
play-created state is the operator edits.

Granularity is where it gets interesting. **Per-entity create-not-put is wrong here**:
referential stubs occupy keys (F11), so importing an entity that references another creates
a stub at the referenced key, and that entity's own create then returns `ErrKVKeyExists` —
which a no-op policy would swallow, leaving it a permanent factless stub. The stream path
works today precisely because it merges.

So the gate is **a single sentinel**: one atomic `graph.mutation.entity.create` of the
campaign entity decides the whole question — created means fresh world, proceed with the
full import; `ErrorCodeEntityExists` means already instantiated, skip. This is genuinely
atomic (`CreateEntityStrict` exists specifically to close the exists-check-then-Put TOCTOU,
per its own doc comment), costs one round trip rather than N, and is immune to the stub
problem because no template entity references the campaign. It is also free: D4 needs a
campaign entity to hold `campaign_seed` regardless, so the boot sentinel and the seed holder
are the same entity.

### F12 — Verify against the pinned module cache, not the working checkout

The semstreams working checkout at `~/Code/c360/semstreams` runs ahead of the pin — measured
at 56 commits ahead of `v1.0.0-beta.158`, with uncommitted changes and a branch mid-archive.
Line numbers already differ between the two for the same mechanism. Findings F1–F9 were read
from the checkout and later spot-confirmed at the pinned tag in the module cache (predicate
arity, batch partial-success, rule condition gating all identical), so they stand — but the
method was wrong and would eventually produce a finding about code we do not compile. Verify
behavior against `$(go env GOMODCACHE)/github.com/c360studio/semstreams@<pinned tag>`; read
the checkout for ADRs, design intent, and upstream direction. `CLAUDE.md` corrected.

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
- ~~`world_ns` allocation format~~ **CLOSED:** operator-assigned, validated as an entity-ID
  segment, no collision detection. Instance-per-world makes collision an operator concern,
  not an engine one; revisit if hosted multi-world lands.
