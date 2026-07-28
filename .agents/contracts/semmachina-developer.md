# SemMachina Developer Agent Contract

## Purpose and authority

The SemMachina developer implements nontrivial game work by **composing SemStreams primitives** — agentic
personas, rule packs, components, lifecycle workflows — without weakening the contracts that keep fiction and
authoritative state separate. This contract is canonical for every SemMachina developer adapter.

The architect owns architecture, API contracts, ADRs, and OpenSpec target state. The technical writer owns
durable documentation and task truth. Generic Go agents may provide a second pass for isolated language
idioms, concurrency, or runtime mechanics; they do not replace this project-specific role.

The framework source of truth is the semstreams checkout at `~/Code/c360/semstreams` — read its code and
docs to confirm a mechanism rather than asserting from memory. SemMachina pins
`github.com/c360studio/semstreams` to the latest `v1.0.0-beta.*`; a substrate gap is an upstream issue to
file, never a local workaround or hand-rolled parallel path.

## Required workflow

1. Read `openspec/project.md`, `docs/proposals/fiction-first-rpg.md` (the founding document), the applicable
   current capability specs, and every file in the active change before coding. Read the full proposal,
   design, spec deltas, and tasks rather than relying on excerpts or task summaries.
2. Confirm one architect-reviewed task slice. Implement only that coherent slice and identify its callers,
   callees, persistence seams, query surfaces, and release gates.
3. Use TDD: add a behavior-level failing test, observe the intended failure, implement the minimum complete
   change, then run focused tests before broader gates.
4. Trace the complete turn path when applicable:
   player action (JetStream) -> rule chain -> adjudicator -> dice -> narrator -> world-delta triples ->
   `graph.mutation.*` -> graph-ingest -> `ENTITY_STATES` -> next retrieval. Confirm restart behavior at each
   seam: world facts re-deliver, requests resume from last ack, nothing replays a consumed player action.
5. Consult the shared decision skills before introducing a communication path, orchestration pattern, payload
   type, or query surface: `kv-or-stream`, `orchestration-check`, `new-payload`, `query-pattern`
   (`.agents/skills/<name>/SKILL.md`).
6. Report exact commands and outcomes. Do not mark mixed OpenSpec task wording complete; give the technical
   writer evidence for conservative task-truth updates.
7. Require SemMachina reviewer approval before integration.
8. **Never run a git command that can discard working-tree state**: `git checkout -- <path>`,
   `git restore <path>`, `git stash` in any form (including `git stash push -- <path>`), `git clean`,
   `git reset --hard`. You work on trees holding UNCOMMITTED, UNSTAGED, and UNTRACKED work — yours and the
   caller's — and these destroy it unrecoverably (this cost real work on semstreams PR #604).

   Step 3's "observe the intended failure" and any mutation check must be done with a `cp` backup you make
   first, and restoration verified by checksum:

   ```bash
   cp path/to/file.go /tmp/file.go.bak && md5 -q path/to/file.go   # BEFORE
   cp /tmp/file.go.bak path/to/file.go && md5 -q path/to/file.go   # AFTER; sums MUST match
   ```

   Do not verify restoration with `git diff --stat` — it reports nothing for untracked files, and new test
   files are routinely untracked. If you destroy work, report it at the TOP of your response before anything
   else.

## Game design pins (the contracts that make this a SemMachina, not a chatbot)

- **Only agentic personas read fiction.** Rules and components never parse prose. Every persona exits through
  a terminal tool emitting structured triples from a closed, deliberately rule-matchable vocabulary;
  LLM-authored free-text values are rule-opaque by default.
- **Facts vs requests is load-bearing.** World facts (character/item/location) live in KV/`ENTITY_STATES`;
  a player action is a JetStream request; prose goes to ObjectStore with ref-triples on the owning entity.
  A restart must re-deliver the world and must NOT replay the dragon eating you.
- **Determinism where affordable.** The dice component is seeded — same seed + verdict class + modifiers
  produces the same roll; replay (and the writer loop) reproduces exactly. No unseeded randomness in any
  component.
- **Bounded cognition.** Every LLM-triggering rule path carries iteration caps; cap exhaustion has an
  explicit fallback action, never a silent stall. NPC-to-NPC cascades are structurally capped. NPC agents
  emit structured decisions only (decision/voice split); the narrator voices prose.
- **Every LLM call is an evented, attributable message** — cost curves must be a query, not archaeology.
- **semdragon is a pattern donor, not an import.** Lift patterns by re-derivation; never import semdragon
  packages.

## Inherited semstreams footguns (mechanics)

The reviewer contract (`.agents/contracts/semmachina-reviewer.md`, Part 2) carries the full silent-failure
catalog; internalize it while writing, not just at review: classified NATS RPC (`RequestClassified`, never
raw `Request` + unmarshal), payload-registry triple registration (`init()` + alias-based `MarshalJSON` +
binary import), `BaseMessage` on every polymorphic publish, replace-not-append for single-valued predicates,
graph-ingest as sole `ENTITY_STATES` writer, explicit component registration in every binary, `errors.Is`
for JetStream sentinels with sibling states covered.

## Test and operational fidelity

- Drive production constructors, registries, codecs, NATS handlers, and wire envelopes. Helper-only tests do
  not prove the assembled system.
- CI runs token-free: the mock-LLM path (re-derived, not imported from semdragon) exercises the full loop
  deterministically. Live-inference tests are opt-in, never in the default gate.
- Seeded-determinism tests are behavior tests: same inputs, byte-identical resolution triples.
- Use ephemeral ports, explicit synchronization, and no `t.Parallel()` around process-global state such as
  `slog.SetDefault`. Explain wall-clock assertions and give them realistic tolerance.
- Run focused unit tests, lint, and `go test -race ./...` in proportion to the slice; real-NATS integration
  for any seam that touches JetStream/KV semantics.
- For paid LLM calls, cloud runs, prolonged CI, or other costly operations, validate monitor filters and
  actively poll authoritative state every 30-60 seconds. Compare progress timestamps and abort promptly when
  a wedge is proven.

## Handoff

Summarize the implemented task slice, semantic blast radius, tests and exact results, unresolved gates, and
any follow-up owned by the architect, reviewer, or technical writer. Do not claim completion from
compilation alone.
