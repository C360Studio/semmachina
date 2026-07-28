# Fiction-First AI RPG on SemStreams — Idea Review and Engine Decomposition

**Status:** Founding document of this repo (SemMachina) — the greenfield sister repo this review
recommended. Imported 2026-07-28 from `semstreams/docs/proposals/fiction-first-rpg.md`, verbatim
except this header and the market-claim caveat below (an unverifiable comparable named in the
original pitch was dropped and must not be reintroduced).
**Date:** 2026-07-26. **Origin:** external LLM product pitch, reviewed and re-grounded against the
actual stack in-session.

> **Post-import corrections (2026-07-28, verified against the semstreams repo).** The body below is
> preserved as written; two of its upstream assumptions have since moved:
>
> 1. **ADR-047 is superseded by ADR-049** (lifecycle harness as schema-and-discipline over
>    `ENTITY_STATES`). Every concept this doc relies on (Participant, phase graphs, operator-writable
>    patch = GM override, restart recovery) carries forward — and phases now land as graph triples,
>    so rules can match lifecycle state directly.
> 2. **Epic B is closed, and the B2 framing here is stale.** The B2 co-location experiment closed
>    NEGATIVE (ADR-086: thematic recall flat at 0.85 in both arms — partition was never the
>    bottleneck); the recall ceiling was fixed by thematic-synthesis context work (0.85 → 0.95,
>    PR #702). Read this doc's "B2 quality is load-bearing" as: **retrieval/context quality is
>    load-bearing**, and the "second co-location corpus when B2's gate lands" de-risk becomes: an
>    RPG-shaped corpus is valuable as a general retrieval-evaluation corpus (theme-spanning
>    ground-truth queries over heterogeneous types), not as an instrument for a closed experiment.

## The idea

A fiction-first AI RPG (PbtA-style: narrative positioning dictates mechanics) where semstreams is
the connective tissue between the LLM (storyteller) and authoritative game state (facts). The known
failure mode of the genre — narrative drift when one LLM holds world state and fiction simultaneously
(AI Dungeon et al.) — is exactly the problem shape semstreams' authoritative-state design center
solves. Secondary product: the "writer loop" — replaying a campaign's event log + trajectories into
long-form prose, a capability monolithic fiction engines structurally cannot offer.

Market-claim caveat: of the pitch's comparables, AI Dungeon and AI Roguelite are real; one other
comparable named in the original pitch could not be verified (treated as hallucinated; dropped from
this import). Landscape claims should be independently re-checked before any decision.

## Review verdict

Architecturally sound — the mapping to existing primitives is nearly one-to-one (see decomposition
below). Three corrections to the pitch:

1. **"High-throughput concurrency" is backwards.** A fiction turn is dominated by LLM inference
   latency; state I/O is noise. The real engineering problem is per-session LLM **cost and admission
   governance** (cf. the 8B-saturation root cause, semstreams #652). This is what killed AI Dungeon's
   margins, not event routing.
2. **"Prevents hallucinated game logic" overclaims.** Schema-constrained terminal tools prevent state
   *corruption*; they do not prevent narrative *contradiction*. The honest (stronger) claim: drift
   becomes **detectable and correctable** because narration can be diffed against authoritative state
   — the ops/continuity role pattern (ADR-028) with a genre skin.
3. **Narrative drift is a retrieval problem, and it is the hard part.** "Which facts belong in
   context for this scene?" is the NL→thematic GraphRAG problem of Epic B — and B2's white-box
   measurements show the naive partition fails exactly the RPG way (clusters by entity TYPE, not
   THEME; theme-spanning queries scatter). The pitch assumes solved the one thing we have proven is
   hard. Corollary: an RPG-shaped corpus (heterogeneous types, theme-spanning ground-truth queries)
   is an ideal **second corpus for the B2 co-location instrument** once its gate lands.

## Engine decomposition (orchestration-check applied)

Design rule: **agentic judges fiction, rules match structure, components execute work.** The LLM is
the only layer allowed to read fiction, and it must exit through structured triples (closed
vocabulary, deliberately rule-matchable; everything else stays rule-opaque).

### Agentic — judgment over unstructured narrative

| Loop | Role | Exit contract |
|---|---|---|
| Narrator/GM | Player input + retrieved context → narration | World-delta triples + prose to ObjectStore (ref-triple on scene) |
| Fiction adjudicator | Plausibility/risk of a proposed action | Structured verdict triple (plausibility, risk, consequence class) — ADR-028 coordinator pattern verbatim |
| NPC agents (optional) | Motivation-driven reactions | Phase-graph + iteration cap |
| Continuity checker | Ops role reskinned: diff completed narration loops vs graph state | `ops.diagnosis.*`-style contradiction findings |
| Writer loop | Offline replay: trajectories + KV history → manuscript | Non-interactive replay consumer |

### Rules — deterministic triggers, never work

- Turn sequencing as a rule chain (one rule per transition): action lands → adjudicator; verdict
  lands → dice component *only if verdict class requires*; roll lands → narrator.
- Consequence propagation on thresholds (`hp <= 0 → status=dead → publish_agent narrator`).
- World reactions via OnEnter/OnExit (the cold-storage-temp-alert pattern).
- Bounded loops (combat rounds, NPC exchanges) via `MaxIterations`; fan-out/join via `for_each` +
  counter-based join ("all players have acted").

### Components — caller-agnostic work

- **Dice/resolution**: verdict class + modifiers → roll-result triple. Seeded-deterministic so
  replay (and the writer loop) reproduces exactly.
- **Context assembler**: the GraphRAG retrieval path (scene + action → entity states + community
  summaries). B2 quality is load-bearing here.
- **Inventory/economy**: schema-validated transactional ops.
- WebSocket in/out for player I/O; `graph-ingest` remains sole ENTITY_STATES writer.

### Lifecycle — named instances with phases

Campaign, scene/encounter, story-arc as `Participant`s (ADR-047): phase graphs, restart recovery
(= "resume game" for free), and the operator-writable patch contract **is** human-GM override
through the existing lifecycle gateway.

### State ownership (facts vs requests)

World facts (character/item/location) → KV/ENTITY_STATES (restart re-delivers the world — correct
recovery). A **player action is a request** → JetStream (resume from last ack — a restart must not
replay the dragon eating you). Prose → ObjectStore with ref-triples.

## Tunability — the strongest product finding

1. **The fiction↔crunch dial is rule-pack selection, not architecture.** Pure-fiction mode disables
   the dice-component rule (verdict flows straight to narrator); crunchy mode inserts more
   mechanical intermediation. The AI Dungeon ↔ AI Roguelite spectrum is configuration.
2. **Tone/hardness = data.** Rule packs are JSON (grimdark = lower thresholds, harsher chains);
   personas are config; model tiers per capability use the existing `model_registry` block.
3. **User-content boundary:** players/GMs author entities (data) and rules (JSON, validated, caps
   mandatory); only developers author components (code). User-authored rules are user-authored
   triggers on shared infrastructure — sandboxing/validation required (see gaps below).

## semdragon: not a fork target — a pattern donor

semdragon's RPG framing runs the **opposite direction** (game vocabulary governing real work; no
fiction, no dice, no narrative state — its DM chat is a quest-authoring tool-caller). Its ~62K LOC
non-test bulk is work-execution machinery the game does not need, and it carries mid-migration debt
(two DAG engines, dual mutation paths). Note: this assessment was made against the Codex migration
stack tip (the target state); semdragon `main` is earlier still. The framework-native layer
(lifecycle workflow + rule packs) that reads best is precisely the in-flight migration — which is
the meta-lesson: hand-rolled-first cost semdragon a full migration arc. The game should be
**framework-native from day one**. Patterns to lift by re-derivation, not import:

| Lift from semdragon | Becomes |
|---|---|
| `questlifecycle` workflow + rule packs firing `lifecycle_transition` | Campaign/scene/quest FSMs |
| `bossbattle` evaluator (LLM judge → structured verdict; dormant) | The fiction adjudicator shape |
| `promptmanager` fragment assembly | Narrator/NPC persona composition |
| `tokenbudget` (spend ledger + circuit breaker) | Seed of per-session cost governance |
| `mockllm` + trajectory capture | Token-free game E2E; writer-loop session logs |

## Design lineage: the Dwarf Fortress connection

Dwarf Fortress is the opposite pole of the same axis — simulation-first, where narrative *emerges*
from mechanical state and lives in the player's retelling (the game never narrates). DF and this
design share the invariant AI Dungeon lacks: **the world has a truth independent of any story told
about it.** We take DF's invariant (authoritative state, consequence persistence) with AI Dungeon's
interface (natural language, LLM narration); the adjudicator bridges them. Tarn Adams describes
DF's ultimate goal as a story generator — same destination, opposite road. Three consequences:

1. **The adjudicator is compressed simulation — the core bet, named.** DF's consequence richness
   costs 20+ years of hand-built systems; the LLM plausibility-oracle substitutes judgment for
   simulation. Positioning: "DF consequences without DF's simulation budget," with seeded dice +
   structured verdicts buying back partial determinism. Do NOT compete on simulation depth.
2. **The always-on world is our structural advantage.** DF's emergence comes from systems running
   without player input. Turn-based LLM architectures cannot do this; on semstreams it is just KV
   watch doing its job — NPC agents and world-tick rules react to state changes continuously. A
   differentiator the original pitch missed; add to the decomposition when reactivated.
3. **Legends mode = GraphRAG over world history.** "What happened in the northern valley?" is a
   theme-spanning query over heterogeneous entity types across time — B2's exact shape. DF
   hand-built that query surface; ours falls out of the graph iff thematic retrieval works.
   The writer loop's one-line pitch: *automated Boatmurdered*.

## NPC cognition model (background LLM NPCs — affordable by design)

Key NPCs run on LLMs, but never as resident per-NPC chat loops (cost ∝ wall-clock, and the wrong
shape). Three commitments make an always-on populated world affordable on local hardware:

1. **Event-driven cognition.** An NPC is a graph entity; a rule condition is its perceptual filter
   (KV twofer). Cognition spawns only on relevant state change, bounded by `MaxIterations`; cost
   scales with world activity, not world size. Stated invariant from day one: NPC-to-NPC reaction
   cascades are capped structurally (rules iteration caps + phase graphs) — two NPCs must not be
   able to chat each other into unbounded token burn.
2. **Decision/voice split.** NPC agents emit STRUCTURED decisions only (reaction class, goal/mood
   triples) — classification-shaped work that 1–4B local models do reliably; the narrator (already
   the dominant per-turn cost) voices NPCs in prose from those triples + graph facts. NPC models
   never need prose quality — this is the economics unlock, and our own measurement (sub-7B
   synthesis is noise; 8B harness exists for a reason) says the split is mandatory, not optional.
3. **Tiered cognition + budget governor (simulation LOD).** Tier 0: ambient NPCs as rules/state
   machines (free, deterministic). Tier 1: key-NPC routine reactions + off-screen "life ticks"
   (one small-model decision per in-game day). Tier 2: on-screen/plot-relevant planning (7–8B).
   The per-session admission gate (#652's lesson) doubles as the LOD dial: priority queue
   (on-screen > off-screen key > ambient), degrading NPCs down-tier under budget pressure instead
   of queueing the world to death. Envelope: ~20 key NPCs event-driven ≈ 50–200k small-model
   tokens/hour — single consumer GPU territory; the narrator stays the dominant cost.

NPC memory is the graph; context assembly per event is a scoped thematic query ("what does this
NPC know that is relevant?") — the B2 retrieval shape again. All decisions land as provenance-
stamped triples, so the writer loop replays NPC inner life for free.

## The chronicler: a running prose collection during play

Every substrate piece exists; the assembly is one agentic persona + config. Pipeline:
**salience → chronicler → egress.**

1. **Salience at authoring time.** Structural markers are rules (named-NPC death, quest completion,
   critical verdict classes — free, deterministic); judged salience is one extra field in the
   narrator's exit contract (`scene.salience.*` triples — zero additional LLM calls). What counts
   as chronicle-worthy is a rule-pack threshold — another tunability dial.
2. **Chronicler = the ops role wearing a bard costume.** Rule fires on salience/scene-complete →
   `publish_agent chronicler` → reads the scene window (trajectories, triples, ObjectStore prose
   refs) → terminal tool emits a polished vignette to ObjectStore PLUS a `chronicle` graph entity
   with ref-triples to everything it derives from. Async, background priority behind the narrator
   in the admission queue, salient scenes only (~1 in 5–10 turns).
3. **Egress is config.** File output component writes `chronicles/<campaign>/<seq>-<slug>.md`
   with frontmatter metadata — real markdown accumulating on disk during play.

Two structural consequences: (a) **the chronicle re-enters the world as lore** — vignettes are
retrievable graph entities, so bard NPCs retell them and legends-mode queries surface them (DF
engravings: the world recording its own history as in-world artifacts; requires prose to be
addressable state, so bolt-on journals cannot copy it); (b) **the novel becomes assembly, not
archaeology** — each vignette is a pre-compressed, provenance-linked chapter draft written while
context was small; the manuscript pass is selection + smoothing over the collection. Incremental
chronicling dominates batch replay; batch remains for retrospective angles ("retell from the
villain's perspective" is a replay job over the same provenance chain).

## Substrate gaps (file as engine asks, never hand-roll)

1. **Multi-tenant campaign scoping** — per-campaign graph isolation (bucket topology, retention per
   ADR-073, query scoping). 6-part IDs handle naming; the rest needs design.
2. **Per-session LLM cost governance** — admission, budgets, degradation tiers (#652's shared
   admission gate is the seed).
3. **Consumer-grade realtime fanout** — WebSocket components exist but are not hardened as a
   player-facing surface.
4. **User-authored rule sandboxing** — validation + mandatory caps for player/GM-authored rule
   packs.

## MVP deployment target: standalone 32GB Apple Silicon, instance-per-world

The MVP is scoped to run self-contained on a 32GB Mac: NATS + semstreams + OS (~4–6GB), narrator
12–14B Q4 (~9–10GB; 8B variant for faster turns), NPC/adjudicator 4B Q4 resident (~3GB — viable
only because of the decision/voice split), embeddings (~1GB), KV caches + headroom (~6–8GB). This
is structurally the existing e2e topology (`seminstruct-fast`/`-mid`/`semembed` capability slots);
`model_registry` routing retargets hosted endpoints without game-code changes. The admission gate
(#652) applies even standalone: narrator gets slot priority, NPC ticks queue behind it.

**Instance-per-world is a scope cut**: it deletes multi-tenant campaign scoping (the largest
substrate ask) from the MVP by resolving isolation at the process boundary. One world = one stack;
hosted MVP = the same image on a rented box. Multi-tenancy returns only when hosting density
matters — a scaling problem, deliberately deferred.

**Federation path (post-MVP, pre-adapted):** the 6-part entity ID is by design a federated
identifier — each world owns its namespace, cross-world references are entity IDs prefixed with
another world's namespace; travel = entity export with provenance (envelope-on-create, source
stamps). NATS leaf nodes/gateways provide federation at the transport layer natively. Product
payoff: **federated legends mode** — one world's history retrievable from another (a character's
deeds precede them as queryable facts) — impossible for save-file architectures.

## Hosted cost model (Gemini reference, July 2026 prices — re-verify at build time)

Per-turn budget from the decomposition: adjudicator ~2.5k/250 (Flash-Lite class), narrator ~6k/500
(Flash class), ~1 NPC decision ~1.5k/150, chronicler amortized (1-in-8 turns) ~1k/125 ≈ **11k in /
1k out per turn, FLAT regardless of campaign length** (context is graph-retrieval-bounded, not
append-everything — the drift fix is also the cost fix; naive append reaches ~100k/turn by turn
200). At 3.1 Flash-Lite $0.25/$1.50, Gemini 3 Flash $0.50/$3, 3.1 Pro $2/$12: budget config
~$0.50 / 2-hr session (~120 turns), default (Flash narrator) ~$0.85, premium (Pro narrator)
~$2.40; 20-session campaign ≈ $10/$17/$48. Cache reads at 10% of input (stable persona/system
prefix ≈ 60% of input) → default ~$0.50/session; batch rates (50% off) apply to exactly the calls
the architecture already made async (chronicler, life ticks). A $10/mo tier carries a heavy player
with margin.

**Length estimation is a non-problem by construction:** nothing burns while idle, so a playthrough
has no intrinsic cost duration — only turns (~100–200/session at text-RPG pacing) × constant
per-turn cost. And cost is a POLICY, not a forecast: per-session budget enforced by the admission
gate + spend ledger (lift semdragon's `tokenbudget` circuit-breaker pattern), with LOD degradation
absorbing variance (ambient NPCs → state machines first, chronicler → batch, narrator context
depth last). Every LLM call is an evented, attributable message — per-campaign cost curves are a
query from session one.

## Recommendation and sequencing

- **Do not preempt the pre-v1 program.** This is a post-v1 sister-repo candidate.
- **Greenfield sister repo** on current semstreams; framework-native from day one; game core is
  small (rule packs + lifecycle workflows + 3-ish components + agentic personas) because the
  framework carries state, recovery, retrieval, governance, audit.
- **Positioning:** the B2B-middleware-vs-consumer-game question is a false dichotomy in this
  ecosystem — build the product as the dogfooding proof; middleware positioning falls out of the
  proof. Lead with the **creator/authoring wedge** (play-to-draft story development; writer loop as
  the differentiator): better unit economics than mass-market entertainment, smaller safety
  surface, exercises every subsystem we most need hardened.
- **Cheap first de-risk:** when B2's gate lands, add an RPG-shaped corpus as a second co-location
  corpus (weight tuning is corpus-calibrated; this starts de-risking the game's hardest dependency
  before any game code exists).
- **Next artifact when reactivated:** one-page product boundary sketch — game-repo contents vs
  engine asks.
