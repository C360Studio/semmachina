# SemMachina Project Context

A fiction-first AI RPG (PbtA-style: narrative positioning dictates mechanics) built
**framework-native on SemStreams**. SemStreams is the connective tissue between the
LLM (storyteller) and authoritative game state (facts): the world has a truth
independent of any story told about it, and drift becomes detectable because
narration can be diffed against authoritative state.

**Founding document:** `docs/proposals/fiction-first-rpg.md` — read it before
scoping anything non-trivial. **Project boundary:** `openspec/project.md`.

## Semantic Agent Routing

- Nontrivial SemMachina implementation uses `semmachina-developer`.
- Every nontrivial change is reviewed by `semmachina-reviewer` (read-only) before integration.
- Generic Go agents are only an isolated idiom, concurrency, or runtime second pass; they do not
  replace either role.
- Canonical role contracts live in `.agents/contracts/`; the `.claude/agents/` entries are thin
  adapters to them.
- Canonical shared decision skills live in `.agents/skills/` — kv-or-stream (KV Watch vs JetStream
  Stream, 4-test heuristic), orchestration-check (rule vs component vs lifecycle vs persona
  boundary), new-payload (payload-registry checklist), query-pattern (GraphQL vs MCP vs NATS
  Direct). The `.claude/skills/` entries of the same names are thin adapters to them.
- The framework source of truth is the semstreams checkout at `~/Code/c360/semstreams` — read it to
  confirm a mechanism rather than asserting from memory.

## Tech Stack

- Go 1.26+ on SemStreams (`github.com/c360studio/semstreams`) — NATS JetStream
  (KV, ObjectStore), rule engine, lifecycle harness, agentic substrate
- Dependency pinned to the latest `v1.0.0-beta.*` tag; retarget v1 on release.
  Beta gaps are filed upstream, not worked around.

## The One Design Rule

**Agentic judges fiction, rules match structure, components execute work.**

The LLM is the only layer allowed to read fiction, and it must exit through
structured triples (closed vocabulary, deliberately rule-matchable). Everything
else stays rule-opaque. If a rule condition needs to branch on narrative content,
that's adjudicator work exiting as a structured verdict triple — never a rule
parsing prose.

## Engine Decomposition

| Layer | SemMachina pieces |
|---|---|
| Agentic personas | Narrator/GM, fiction adjudicator (ADR-028 coordinator pattern), NPC agents, continuity checker (ops role reskinned), chronicler, writer loop (offline replay) |
| Rule packs (JSON data) | Turn sequencing chains, consequence propagation, world reactions (OnEnter/OnExit), bounded loops (`MaxIterations`), fan-out/join (`for_each` + counter join), salience markers |
| Components | Dice/resolution (seeded-deterministic), context assembler (GraphRAG retrieval), inventory/economy, chronicle egress, WebSocket player I/O |
| Lifecycle (ADR-049; supersedes 047) | Campaign, scene/encounter, story-arc as `Participant`s; phases land as graph triples in `ENTITY_STATES` (rule-matchable); operator patch contract = human-GM override; restart recovery = "resume game" |

## State Ownership (facts vs requests)

| Data | Home | Why |
|---|---|---|
| World facts (character/item/location) | KV / `ENTITY_STATES` | Restart re-delivers the world — correct recovery |
| Player actions | JetStream stream | A request; resume from last ack — a restart must not replay the dragon eating you |
| Prose (narration, vignettes) | ObjectStore + ref-triples | Bulky content addressable as state — enables lore re-entry and the writer loop |

`graph-ingest` remains the sole `ENTITY_STATES` writer; mutations go through the
`graph.mutation.*` API. Every graph write carries a semantic envelope.

## Standing Invariants

- **Seeded-deterministic dice** — replay (and the writer loop) reproduces exactly.
- **NPC decision/voice split** — NPC agents emit structured decisions only
  (classification-shaped, small local models); the narrator voices NPCs in prose.
  NPC models never need prose quality; this is the economics unlock.
- **Capped cascades from day one** — NPC-to-NPC reactions are structurally bounded
  (rule iteration caps + phase graphs); two NPCs must not chat each other into
  unbounded token burn.
- **Cost is a policy, not a forecast** — per-session budget via admission gate +
  spend ledger; LOD degradation absorbs variance (ambient NPCs → state machines
  first, chronicler → batch, narrator context depth last). Per-turn cost is flat
  because context is graph-retrieval-bounded, never append-everything.
- **The fiction↔crunch dial is rule-pack selection, not architecture.** Tone and
  hardness are data (JSON rule packs, persona config, `model_registry` tiers).
- **Worlds are data; the engine is world-agnostic.** Starting state + rules +
  personas ship as a package-shaped fixture (template-local IDs, manifest v0)
  loaded via an importer through graph-ingest. Template = immutable versioned
  product; world instance = mutable campaign. Never hardcode a world in Go.
- **Player I/O is adapter-shaped.** Canonical action payload in, canonical
  committed result out; WebSocket is one adapter of N (Slack/email/SMS later).
  Player identity is a graph entity, never a connection ID. No turn-loop step
  assumes interactive pacing — email-cadence play is valid.
- **Do not compete on simulation depth.** The adjudicator is compressed
  simulation — LLM judgment substituting for hand-built systems.

## Boundary Discipline

- **Substrate gaps are engine asks filed upstream in semstreams — never
  hand-rolled here.** Known asks: per-session LLM cost governance, realtime
  fanout hardening, user-authored rule sandboxing, (post-MVP) multi-tenant
  campaign scoping.
- **semdragon is a pattern donor, not a fork target** — lift by re-derivation
  only (quest lifecycle FSMs, bossbattle evaluator, promptmanager assembly,
  tokenbudget, mockllm + trajectory capture). Its hand-rolled-first migration
  arc is the cautionary tale; SemMachina is framework-native from day one.
- **User-content boundary:** players/GMs author entities (data) and rules (JSON,
  validated, caps mandatory); only developers author components (code).

## MVP Target

Standalone 32GB Apple Silicon, **instance-per-world** (one world = one stack;
hosted MVP = same image on a rented box). Instance-per-world deletes multi-tenant
scoping from the MVP at the process boundary. Federation via 6-part entity IDs +
NATS leaf nodes is post-MVP, pre-adapted by design.

## Spec-driven development (OpenSpec)

OpenSpec CLI + `.claude/` skills are installed — `/opsx:new`, `/opsx:continue`,
`/opsx:apply`, `/opsx:archive`; `openspec list`, `openspec validate`. Three homes,
three jobs:

| Home | Holds | Drifts? |
|------|-------|---------|
| `openspec/specs/<capability>/spec.md` | **Current truth** — what a capability does *today* (`Requirement` + `GIVEN/WHEN/THEN`) | No — every change edits it via a delta |
| `openspec/changes/<id>/` | **Proposed target state** — `proposal.md` + `tasks.md` + spec deltas; archived on completion | Resolves on archive |
| `docs/adr/` | **Genuine decisions only** — irreversible choices + cross-repo contracts (the *why*) | No — history |

Rules of the road:

- Non-trivial or cross-cutting work starts with a change (`/opsx:new`): proposal +
  tasks + spec deltas *before* code. Small mechanical fixes don't need one.
- Specs are seeded lazily — written when a change first touches a capability,
  verified against code. Do NOT backfill.
- Read `openspec/project.md` first when scoping anything.

## Conventions

- 6-part entity IDs: `org.platform.domain.system.type.instance`
- Facts vs requests: KV Watch for facts, JetStream for requests
- Rules trigger, components execute — no separate workflow engine; rules carry
  references (loop IDs, entity IDs, storage refs), never content
- No NATS TTL/MaxBytes/MaxAge for live-graph lifecycle (ADR-068 upstream)
- CI green before push: lint, `-race` tests, cross-compile
- Conventional commits: `<type>(scope): subject`

## Provenance Hygiene

The original external pitch contained market-landscape claims that could not be
verified; unverifiable comparables were dropped during review and must not be
reintroduced into this repo. Verified comparables: AI Dungeon, AI Roguelite (and
Dwarf Fortress as design lineage). Independently re-verify any market claim
before relying on it.
