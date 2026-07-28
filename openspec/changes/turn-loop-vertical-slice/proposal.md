# Turn Loop Vertical Slice

## Why

SemMachina has no code yet; the founding review (`docs/proposals/fiction-first-rpg.md`) is
decision-complete and the turn loop is the product's spine — every other feature (NPCs,
chronicler, continuity checking) hangs off it. A minimal end-to-end slice proves the core
architectural bet — **agentic judges fiction, rules match structure, components execute
work** — on real SemStreams primitives before anything else is layered on, and surfaces
engine gaps early while SemStreams approaches v1.

## What Changes

- First code in the repo: a `cmd/semmachina` binary composing SemStreams components into a
  one-player, single-scene world (instance-per-world from day one).
- **Player I/O**: WebSocket ingress lands each player action as a request on JetStream
  (a restart must not replay the dragon eating you); narration is pushed back out over
  WebSocket.
- **Turn-sequencing rule pack** (JSON, no code): action lands → adjudicator; verdict lands
  → dice **only if the verdict class requires a roll**; roll (or roll-skipping verdict)
  lands → narrator. Rules pass references only, never content.
- **Fiction adjudicator persona**: agentic loop config whose terminal tool emits a
  structured verdict triple — plausibility, risk, consequence class — from a closed,
  rule-matchable vocabulary (ADR-028 coordinator pattern).
- **Dice/resolution component**: seeded-deterministic; verdict class + modifiers →
  roll-result triple. Replay reproduces exactly.
- **Narrator persona**: verdict/roll + scene facts → prose to ObjectStore (ref-triple on
  the scene) + world-delta triples through the `graph.mutation.*` API.
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
- `turn-sequencing`: the rule chain driving a turn — trigger order, conditional dice
  branch, reference-only payloads, iteration bounds.
- `fiction-adjudication`: the adjudicator persona's contract — what it reads, the closed
  verdict vocabulary it must exit through, and which verdict classes require a roll.
- `dice-resolution`: seeded-deterministic resolution — inputs (verdict class, modifiers),
  the roll-result triple, and the replay-reproducibility guarantee.
- `narration`: the narrator persona's contract — inputs, prose-to-ObjectStore with
  ref-triples, world-delta triples via the mutation API, closed exit vocabulary.

### Modified Capabilities

None — no specs exist yet (first change in the repo).

## Non-goals

Boundary discipline for the slice; each of these is a future change, not scope creep here:

- **No NPC agents, chronicler, continuity checker, or writer loop** — the personas beyond
  narrator + adjudicator come after the spine works.
- **No campaign/scene lifecycle harness** — the slice runs one hardcoded scene; ADR-047
  `Participant` workflows (campaign/scene/arc, "resume game") are the natural next change.
- **No thematic GraphRAG context assembly** — the hard retrieval problem (Epic B upstream)
  is deliberately deferred; the slice uses a fixed scene-scoped query and says so.
- **No cost governance / admission gate** — single local session; governance is an
  upstream engine ask, never built here.
- **No tunability dials** (fiction↔crunch rule-pack selection, tone packs, model tiers) —
  one rule pack, one configuration.
- **No multi-tenancy, federation, or UI** — instance-per-world at the process boundary; a
  raw WebSocket client (e.g. `wscat`) is the player interface.

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
- New: `cmd/semmachina`, flow/persona/rule-pack configs, dice component package, seed
  data, mock-LLM E2E harness. All greenfield — no existing code or consumers affected.
- Requires a local NATS JetStream (and, for live play, an LLM endpoint via
  `model_registry`); CI uses the mock-LLM path only.
