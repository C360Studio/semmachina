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
- **Scale is a budget dial, not an architecture decision — and the bill is stated
  honestly.** World size, spatial-index precision, and how many NPCs get real
  cognition are things an operator buys; the engine's job is to report the
  envelope, not to pick a ceiling. That promise is only keepable because nothing
  is unbounded — iteration caps, capped NPC cascades, and retrieval-bounded
  context are STRUCTURAL, and a budget dial on a runaway path is a fuse, not a
  dial. Published capacity envelopes are **measured from spend-ledger data on real
  runs**, never estimated. **Three cost axes that scale differently — never
  quote one as if it answered another:** *idle* cost follows the clock policy
  (reactive burns nothing while nobody plays, however large the world); *per-turn*
  cost follows the **cognitive population** — how many entities THINK per turn —
  not the map size, because a thousand passive locations barely move a
  scene-scoped retrieval while a thousand reasoning NPCs multiply calls directly;
  and *capacity* is a wall rather than a bill — concurrent inference on one box
  runs out long before the budget does, and that, not map size, is what forces a
  cluster. Note the retrieval-bounded-context pin bounds **context depth per
  call**, which is why a big map is cheap; bounding **calls per turn** is a
  different mechanism (capped cascades + LOD tiers, stage 10). A 1,000-NPC
  cognitive world is a different beast from a 10-NPC one even under identical
  pacing and an identical map. Distinguish cheap dials from baked ones —
  index precision is re-derivable from `ENTITY_STATES` at any time, whereas a
  world's spatial scale is authored into an immutable content-addressed template.
- **World time is a world fact, governed by policy.** The campaign clock is a
  graph entity advanced under a per-campaign clock policy — **reactive**
  (advances only on player action; the world waits), **scheduled** (in-game time
  per real time; the world lives alongside you), or **fiction-driven**
  (rounds / scene / travel pacing). The clock policy is also a COST policy: only
  reactive mode preserves zero-idle-cost; ticking worlds spend under the
  admission gate/LOD tiers and are priced accordingly. Deadlines are world facts
  with threshold rules — player inaction is an input, and deadline evaluation
  uses the action's arrival time (JetStream ingest timestamp), never processing
  time. Personas always judge against current authoritative state at execution
  time, never a submission-time snapshot.
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

1. **Turn-loop engine spike** (complete; archived as
   `2026-07-31-turn-loop-vertical-slice`) — durable turn state, idempotent effects,
   validated closed-vocabulary mutations, crash tests; loads the starter world
   from a package-shaped fixture via an importer.
2. **Minimum epistemic + companion safety proof** (complete; archived as
   `2026-08-02-mystery-companion-acceptance`) — use the original *Death in the Bellweather
   Maze* case to prove secret-safe mystery play and first-class companion support.
   Pull forward only the epistemic primitives required to keep canonical truth,
   character belief, actor knowledge, and narration revelation separate, with a
   centralized audience selector on every persona request. Kit Finch is a
   character-backed reusable companion with scoped knowledge, rule-capped initiative,
   deterministic hint progression, and narrator-owned voice; a second non-mystery binding
   proves the capability is not case-specific. The case lifecycle and exact-ID accusation
   verifier complete one mock-model E2E; Gemini is an opt-in smoke, never a CI dependency.
   This stage does not complete the full epistemic or NPC-cognition roadmap stages. The focused
   `mystery-companion-hardening` follow-up is active but blocked by open SemStreams issues #818,
   #851, and #807. It owns post-import truth-write enforcement, revision-safe hint reset, and
   crash-safe provider dispatch without reopening the completed acceptance proof.
3. **Template proof + place ontology** (complete; archived as
   `2026-08-02-template-place-proof`) — instantiate the same starter world twice;
   swap the narrator/tone pack; swap fiction-heavy vs mechanics-heavy rule packs;
   two materially different experiences on identical engine code. The first
   falsifiable test of the tunability claim ("the dial is rule-pack selection,
   not architecture"). Also promotes **place to first-class**: a `location` kind
   distinct from `scene` (a location persists; a scene is a unit of play *at* one,
   and many scenes may share a place) plus a connective relation. Today the
   ontology can say "Rook is in the gatehouse" but cannot say "the gatehouse
   connects to the courtyard" — there is no edge between places, so no map is
   expressible. Vocabulary is cheapest to change before more templates bind to it.
4. **Creator surface + sense of place** (active; `add-minimal-creator-surface`) — minimal Svelte
   client: action in,
   narration out, and a **resolution card** (plausibility, risk, modifiers, roll,
   consequence) so outcomes are legible, never arbitrary; typed
   progress/error/reconnect. Plus the two orientation widgets a player needs to
   know where and when they are: a **map view** and a **clock readout**.
   - The world exists whole at import — nothing about the map accretes through
     play. The **base map is authored world data**, disclosed at session zero the
     way a GM hands over a regional map: geography is public, contents and current
     state are not. That baseline needs no epistemic model and ships here.
   - The map is a **projection over `ENTITY_STATES`, never a stored artifact**. A
     saved map can disagree with the world, and detecting exactly that kind of
     divergence is the product thesis; a map that can lie is the failure mode.
   - Templates may carry authored **geometry**, not just topology — an authored
     world deserves author-controlled arrangement. Auto-layout from the connection
     graph is the fallback when a package supplies no coordinates, not the target.
     Drawn regional art is a later media pack (see stage 11's typed packs).
   - The clock readout renders the campaign clock entity as a **fact, not an
     animation**: under reactive pacing time advances only on player action, so a
     visibly ticking clock would be a lie. Policy-aware from the first version.
   - **Reuse the upstream indexes; do not hand-roll spatial or temporal search**
     (M6). `graph-index-spatial` is a geohash index over `geo.location.latitude`/
     `.longitude` with radius, bounding-box, and GeoJSON polygon queries;
     `graph-index-temporal` buckets by minute/hour/day off a canonical event-time
     predicate. Both are projections over `ENTITY_STATES`, so they reward the
     stage-3 ontology rather than substituting for it — with no coordinates and no
     connective edge, neither index has anything to index. Match the mechanism to
     the question: **adjacency is a graph edge** ("the gatehouse connects to the
     courtyard" — geohash cannot express connectivity, and two rooms a metre apart
     through a locked wall are metrically adjacent and unreachable in play);
     regional travel and "what is within a day's ride" is the spatial index;
     deadline windows are the temporal index. Two decisions to take deliberately:
     fictional coordinates must be expressed as literal lat/lon (workable — geohash
     is a space-filling curve over any 2D range — but cells are not equal-area, so
     keep a large map in a modest band), and there is a **single** canonical
     event-time predicate, so indexing world time means real time falls back to
     ingestion time. Needing both indexed is an upstream ask, not a local index.
5. **Scene completion + chronicler** — salience marking, vignette emission,
   Markdown preview/export (play-to-draft becomes demonstrable).
6. **Continuity checking + retrieval evaluation** — the ops-role diff persona and
   an RPG-shaped retrieval-eval corpus (theme-spanning ground-truth queries).
7. **Campaign/scene lifecycle** — `Participant` workflows, resume, curated
   tone/crunch rule-pack presets.
8. **Full epistemic model** — extend the minimum stage-2 safety proof with belief
   revision, contradiction reconciliation, stale beliefs, progressive discovery, and
   multiplayer knowledge sharing. Canonical truth vs character belief vs player knowledge
   vs narration-revealed remain required BEFORE NPC cognition, else retrieval produces
   spoilers and omniscient NPCs. It governs the **delta** on top of stage 4's
   authored baseline — what a character has since discovered, been told, or been
   told wrongly. The base map does not wait on this; progressive discovery,
   rumored-but-unverified places, and stale player beliefs do.
9. **World clock + pacing policies** — campaign clock entity, clock-policy modes
   (reactive / scheduled / fiction-driven), deadline entities + threshold rules
   ("the caravan leaves at dawn"), deadline-warning notification egress over
   channel bindings, world-time stamps in the ledger. Prerequisite for NPC life
   ticks; useful long before them (time-sensitive choices work solo).
10. **NPC cognition + cost governance** — tiered NPCs, decision/voice split,
   admission-gate LOD (needs the upstream governance engine ask).
11. **Deferred**: procedural world generation (as a **template author**, not an
   engine feature — a generator emits a package that imports through the existing
   path, adding no engine surface and keeping worlds-as-data; an LLM naming and
   describing places at *generation* time bakes into the template and stays
   replay-deterministic, whereas embellishing at render time would break replay
   honesty and per-turn cost flatness), human-GM editing surface, sandboxed
   user-authored rules, hosting,
   federation, multi-tenancy, detailed inventory/economy, **multi-channel play**
   (Slack/email/SMS ingress-egress adapters over the same action/result contract —
   allowed-for by design, built later), and the **world-template marketplace**.
   Marketplace principles, recorded now so the package shape stays
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
