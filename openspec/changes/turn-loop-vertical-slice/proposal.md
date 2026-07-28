# Turn Loop Vertical Slice

## Why

SemMachina has no code yet; the founding review (`docs/proposals/fiction-first-rpg.md`) is
decision-complete and the turn loop is the product's spine — every other feature (NPCs,
chronicler, continuity checking) hangs off it. A minimal end-to-end slice proves the core
architectural bet — **agentic judges fiction, rules match structure, components execute
work** — on real SemStreams primitives before anything else is layered on, and surfaces
engine gaps early while SemStreams approaches v1.

Honest label: this is an **engine spike**, not a full product vertical slice — the player
interface is a raw WebSocket client. The creator-facing surface (Svelte resolution card)
is the named next change (see `openspec/project.md` Sequencing). The spike's center of
gravity is **turn correctness**: JetStream is at-least-once, so acknowledgement alone does
not prevent the dragon from eating the player twice — duplicate delivery or a crash at any
stage must still yield one logical turn, one set of world effects, one recorded roll, one
canonical narration, and a retrievable response after reconnect.

## What Changes

- First code in the repo: a `cmd/semmachina` binary composing SemStreams components into a
  one-player, single-scene world (instance-per-world from day one).
- **Player I/O**: WebSocket ingress lands each player action as a request on JetStream
  (a restart must not replay the dragon eating you); narration is pushed back out over
  WebSocket, and the last turn's result is retrievable after reconnect.
- **Turn identity and idempotency**: every action carries `action_id`, `turn_id`,
  `scene_id`, and correlation/causation IDs; turn state moves through explicit phases
  (accepted → adjudicating → resolving → applying → narrating → complete/failed) durable
  enough to prove crash-at-any-phase and duplicate-delivery scenarios.
- **Turn-sequencing rule pack** (JSON, no code): action lands → adjudicator; verdict lands
  → dice **only if the verdict class requires a roll**; roll (or roll-skipping verdict)
  lands → narrator; committed effects + narration close the turn. Rules pass references
  only, never content.
- **Fiction adjudicator persona**: agentic loop config whose terminal tool emits a
  structured verdict triple — plausibility, risk, consequence class — from a closed,
  rule-matchable vocabulary (ADR-028 coordinator pattern).
- **Dice/resolution component**: seeded-deterministic, **versioned mechanic v1: 2d6 +
  modifiers, PbtA thresholds (≤6 miss / 7–9 partial / 10+ full success)**; seed derived
  from turn identity; verdict classes declare whether they roll. Replay reproduces exactly.
- **Effect application split from narration**: the narrator (or adjudicator) *proposes*
  consequence intents; a deterministic **effect applier** validates them against the
  closed vocabulary and commits through the `graph.mutation.*` API (idempotent under
  redelivery). Prose quality, graph integrity, and delivery failures can never become one
  partial transaction.
- **Narrator persona**: voices the **committed** outcome — verdict/roll + applied effects
  + scene facts → prose to ObjectStore (ref-triple on the scene). Voice only; no direct
  world mutation.
- **Campaign ledger**: an append-only completed-turn manifest (immutable refs to action,
  verdict, roll, effects, narration) — `ENTITY_STATES` is current truth, the ledger is the
  archive the writer loop replays. Replay honesty: deterministic parts re-execute exactly;
  LLM output replays by preservation — re-running a narrator is a new rendition.
- **Seeded scene**: a hardcoded starting world (character, location, a few items) written
  as entities with 6-part IDs. Context for both personas is a fixed scene-scoped graph
  query — deliberately NOT thematic retrieval (see Non-goals).
- **Token-free E2E harness**: mock-LLM pattern (re-derived from semdragon, not imported)
  so the full loop runs deterministically in CI without inference.

Nothing breaking — greenfield.

## Capabilities

### New Capabilities

- `player-io`: player action ingress (WebSocket → JetStream request) and narration egress;
  the facts-vs-requests boundary for player input.
- `turn-sequencing`: the rule chain driving a turn — turn identity (`action_id`/`turn_id`/
  `scene_id`), explicit turn phases, trigger order, conditional dice branch, reference-only
  payloads, iteration bounds, and the idempotency scenarios (duplicate delivery, crash at
  every phase → one logical turn).
- `fiction-adjudication`: the adjudicator persona's contract — what it reads, the closed
  verdict vocabulary it must exit through, and which verdict classes require a roll.
- `dice-resolution`: seeded-deterministic resolution — versioned mechanic (2d6 v1,
  PbtA thresholds), seed derivation from turn identity, modifier sources, the roll-result
  triple, and the replay-reproducibility guarantee.
- `effect-application`: the deterministic commit boundary — consequence-intent validation
  against the closed effect vocabulary, idempotent application via `graph.mutation.*`,
  rejection semantics for out-of-vocabulary or out-of-bounds intents.
- `narration`: the narrator persona's contract — voices committed outcomes only; inputs,
  prose-to-ObjectStore with ref-triples, closed exit vocabulary, no direct mutation.
- `campaign-ledger`: the append-only completed-turn manifest — record shape, immutability,
  what replay may re-execute vs must preserve.

### Modified Capabilities

None — no specs exist yet (first change in the repo).

## Non-goals

Boundary discipline for the slice; each of these is a future change, not scope creep here:

- **No NPC agents, chronicler, continuity checker, or writer loop** — the personas beyond
  narrator + adjudicator come after the spine works. (The ledger the writer loop will
  replay IS in scope; the loop itself is not.)
- **No creator UI** — the raw WebSocket client marks this an engine spike; the Svelte
  creator surface (resolution card) is the named next change in project.md Sequencing.
- **No campaign/scene lifecycle harness** — the slice runs one hardcoded scene; ADR-049
  `Participant` workflows (campaign/scene/arc, "resume game") arrive at Sequencing
  stage 5.
- **No thematic GraphRAG context assembly** — retrieval/context quality is the product's
  hard dependency (upstream Epic B closed with the synthesis-context fix, but game-corpus
  quality is unproven); the slice uses a fixed scene-scoped query and says so.
- **No cost governance / admission gate** — single local session; governance is an
  upstream engine ask, never built here.
- **No tunability dials** (fiction↔crunch rule-pack selection, tone packs, model tiers) —
  one rule pack, one configuration.
- **No multi-tenancy or federation** — instance-per-world at the process boundary.

## Classification

**Game-repo work** throughout: two agentic personas, one JSON rule pack, one component
(dice), flow composition, and seed data — all composition of existing SemStreams
primitives (agentic loop/tools, rule engine, WebSocket components, graph mutation API,
ObjectStore). **No upstream engine asks are required to build this slice.** Any substrate
gap discovered while building it gets filed upstream in semstreams, not worked around
here.

## Impact

- `go.mod` gains `github.com/c360studio/semstreams` pinned to the latest `v1.0.0-beta.*`
  tag (retarget v1 on release). Beta API drift until v1 is the main schedule risk; the
  mitigation is the pin plus filing upstream issues instead of workarounds.
- New: `cmd/semmachina`, flow/persona/rule-pack configs, dice component package, effect
  applier component, campaign-ledger stream + manifest writer, seed data, mock-LLM E2E
  harness. All greenfield — no existing code or consumers affected.
- Requires a local NATS JetStream (and, for live play, an LLM endpoint via
  `model_registry`); CI uses the mock-LLM path only.
