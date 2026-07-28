# SemMachina Project Context

## Purpose

SemMachina is a **fiction-first AI RPG** (PbtA-style: narrative positioning dictates
mechanics) built **framework-native on SemStreams** from day one. SemStreams is the
connective tissue between the LLM (storyteller) and authoritative game state (facts).
The genre's known failure mode — narrative drift when one LLM holds world state and
fiction simultaneously — is exactly the problem shape SemStreams' authoritative-state
design center solves: the world has a truth independent of any story told about it.

Secondary product: the **writer loop / chronicler** — a running prose collection
during play, replayable into long-form manuscript from the event log + trajectories.
Lead wedge is **creator/authoring** (play-to-draft story development), not
mass-market entertainment. The primary persona is a **solo story creator**: they
complete a scene, understand why consequences occurred (legible resolution, not
raw triples), and leave with reusable prose.

Founding document: `docs/proposals/fiction-first-rpg.md` (imported from the
SemStreams repo review; read it before scoping anything).

## Product Boundary

- **SemMachina owns** game domain semantics composed from SemStreams primitives:
  - **Agentic personas** (judgment over unstructured narrative): narrator/GM,
    fiction adjudicator, NPC agents, continuity checker, chronicler, writer loop.
  - **Rule packs** (deterministic triggers, JSON data): turn sequencing chains,
    consequence propagation, world reactions, bounded loops, salience markers,
    fiction↔crunch dial, tone/hardness packs.
  - **Game components** (caller-agnostic work): dice/resolution
    (seeded-deterministic), context assembler (GraphRAG retrieval path),
    inventory/economy, chronicle egress.
  - **Lifecycle workflows**: campaign, scene/encounter, story-arc as
    `Participant`s (ADR-049, which supersedes ADR-047 — phases land as graph
    triples in `ENTITY_STATES`); operator patch contract = human-GM override.
- **SemStreams owns the substrate** — graph, KV-twofer, rule engine, lifecycle
  harness, agentic loop/tool/model primitives, payload registry, governance.
  **Substrate gaps are filed upstream as engine asks, never hand-rolled here.**
  Known asks from the founding review: per-session LLM cost governance
  (admission, budgets, degradation tiers), consumer-grade realtime fanout
  hardening, user-authored rule sandboxing, and (post-MVP) multi-tenant campaign
  scoping.
- **semdragon is a pattern donor, not a fork target.** Lift by re-derivation only:
  quest lifecycle FSMs, bossbattle evaluator shape (→ adjudicator), promptmanager
  fragment assembly (→ persona composition), tokenbudget (→ cost governance seed),
  mockllm + trajectory capture (→ token-free game E2E).

## Core Design Rules

- **Agentic judges fiction, rules match structure, components execute work.**
  The LLM is the only layer allowed to read fiction, and it must exit through
  structured triples (closed vocabulary, deliberately rule-matchable; everything
  else stays rule-opaque).
- **State ownership (facts vs requests):** world facts (character/item/location)
  → KV/`ENTITY_STATES` (restart re-delivers the world — correct recovery). A
  **player action is a request** → JetStream (resume from last ack — a restart
  must not replay the dragon eating you). Prose → ObjectStore with ref-triples.
- **Drift is detectable, not prevented.** Schema-constrained terminal tools
  prevent state corruption; the continuity checker diffs narration against
  authoritative state (ops-role pattern, ADR-028, genre skin).
- **NPC cognition is event-driven and split.** NPC agents emit STRUCTURED
  decisions only (small local models); the narrator voices NPCs in prose. NPC-to-
  NPC cascades are structurally capped (iteration caps + phase graphs) from day
  one. Tiered cognition (rules → routine ticks → on-screen planning) with the
  admission gate as the LOD dial.
- **Determinism where affordable:** seeded dice + structured verdicts so replay
  (and the writer loop) reproduces exactly.
- **Cost is a policy, not a forecast:** per-session budget enforced by admission
  gate + spend ledger; per-turn cost is flat because context is
  graph-retrieval-bounded, never append-everything.
- **Player I/O is adapter-shaped.** The engine's contract is a canonical action
  payload in (JetStream) and a canonical committed result out (ledger, graph,
  ObjectStore); channels — WebSocket now; Slack, email, SMS later — are thin
  ingress/egress components that normalize to it. Player identity is a graph
  entity bound to channel addresses, never a connection ID. No turn-loop step may
  assume interactive pacing — play at email cadence is valid.
- **Worlds are data; the engine is world-agnostic.** Starting state, rule packs,
  and persona configs ship as package-shaped data (template-local logical IDs,
  versioned manifest) loaded through an importer. A **template** is an immutable
  versioned product; a **world instance** is a mutable campaign materialized from
  it into a world namespace — an update never silently rewrites a living
  campaign. Engine code never hardcodes a world.

## MVP Scope

Standalone 32GB Apple Silicon, **instance-per-world** (one world = one stack).
This deletes multi-tenant campaign scoping from the MVP by resolving isolation at
the process boundary. Federation (cross-world entity references via 6-part IDs,
NATS leaf nodes) is post-MVP, pre-adapted by design.

## Sequencing (working roadmap)

Ordered; each stage is one or more OpenSpec changes. Later stages stay in the
vision but are NOT on the active MVP path.

1. **Turn-loop engine spike** (active: `turn-loop-vertical-slice`) — durable turn
   state, idempotent effects, validated closed-vocabulary mutations, crash tests;
   loads the starter world from a package-shaped fixture via an importer.
2. **Template proof** — instantiate the same starter world twice; swap the
   narrator/tone pack; swap fiction-heavy vs mechanics-heavy rule packs; two
   materially different experiences on identical engine code. The first
   falsifiable test of the tunability claim ("the dial is rule-pack selection,
   not architecture").
3. **Creator surface** — minimal Svelte client: action in, narration out, and a
   **resolution card** (plausibility, risk, modifiers, roll, consequence) so
   outcomes are legible, never arbitrary; typed progress/error/reconnect.
4. **Scene completion + chronicler** — salience marking, vignette emission,
   Markdown preview/export (play-to-draft becomes demonstrable).
5. **Continuity checking + retrieval evaluation** — the ops-role diff persona and
   an RPG-shaped retrieval-eval corpus (theme-spanning ground-truth queries).
6. **Campaign/scene lifecycle** — `Participant` workflows, resume, curated
   tone/crunch rule-pack presets.
7. **Epistemic model** — canonical truth vs character belief vs player knowledge
   vs narration-revealed; required BEFORE NPC cognition, else retrieval produces
   spoilers and omniscient NPCs.
8. **NPC cognition + cost governance** — tiered NPCs, decision/voice split,
   admission-gate LOD (needs the upstream governance engine ask).
9. **Deferred**: human-GM editing surface, sandboxed user-authored rules, hosting,
   federation, multi-tenancy, detailed inventory/economy, **multi-channel play**
   (Slack/email/SMS ingress-egress adapters over the same action/result contract —
   allowed-for by design, built later), and the **world-template marketplace**. Marketplace principles, recorded now so the package shape stays
   compatible: templates are immutable/content-addressed and composable from typed
   packs (world, mechanics, persona, media — components stay first-party);
   community packages are hostile input with capability *requests* resolved as
   engine ∩ host ∩ user policy (a package never grants itself authority); rollout
   ladder is first-party worlds → import/export/fork + private sharing → curated
   free gallery → verified publishers → paid packages; template purchase stays
   separate from runtime inference cost. Foundry VTT manifests are the precedent.
   The hosted cost model in the founding doc is an assumptions register, not
   validated economics.

## Standing Technical Conventions

- Go 1.26+; depends on `github.com/c360studio/semstreams` pinned to the latest
  `v1.0.0-beta.*` tag; retarget v1 on release. File upstream issues rather than
  working around beta gaps.
- All SemStreams standing conventions apply: 6-part entity IDs
  (`org.platform.domain.system.type.instance`), semantic envelopes on every graph
  write, `graph-ingest` as sole `ENTITY_STATES` writer, facts-vs-requests
  (KV Watch vs JetStream), rules trigger / components execute (no separate
  workflow engine), no NATS TTL/MaxBytes/MaxAge for live-graph lifecycle.
- Large or cross-cutting changes go through OpenSpec (proposal + tasks + spec
  deltas) before code. Specs are seeded lazily and verified against code.
- Genuine decisions (irreversible choices, cross-repo contracts) are recorded as
  ADRs in `docs/adr/`; mechanics live in specs.
- CI green before push: lint, `-race` tests, cross-compile.
- Conventional commits: `<type>(scope): subject`.

## Provenance Hygiene

The original external pitch contained market-landscape claims that could not be
verified; unverifiable comparables were dropped during review and must not be
reintroduced. Verified comparables: AI Dungeon, AI Roguelite (and Dwarf Fortress
as design lineage). Independently re-verify any market claim before relying on it.
