# Tasks — Turn Loop Vertical Slice

Implementer notes: read `openspec/project.md`, `proposal.md`, `design.md` (D1–D11), and all
eight delta specs before starting. Follow `.agents/contracts/semmachina-developer.md` (TDD,
git safety, evidence-based completion). The semstreams checkout at `~/Code/c360/semstreams`
is the framework source of truth — read it, don't assume. Spec scenarios are the test list.

## 1. Upstream verification (design Open Questions — do FIRST)

- [x] 1.1 Verified GREEN (design.md F1): `conditions[].field` matches arbitrary triple
      predicates on `ENTITY_STATES`; in-tree precedent
      `configs/rules/lifecycle/01-mission-launch.json`. No upstream issue needed — design
      risk #2 retired. Constraint found: predicates are exactly three lower-kebab segments
      (F2), so `turn.phase` → `turn.phase.current` and no underscores; `transition` cannot
      fire on an entity's first write (F3)
- [x] 1.2 Verified AMBER (design.md F4/F5): `ToolDefinition.Parameters` is arbitrary JSON
      Schema so the nested banded shape holds — flattened fallback NOT needed. But `Strict`
      is provider-dependent and unreliable on the MVP runtime, so executor-side validation
      is the enforcement of record; ADR-026 documents small-model schema collapse, so the
      adjudicator starts on the mid slot
- [x] 1.3 Verified (design.md F7/F8): `input/websocket` + `output/websocket` both exist as
      an ingress/egress pair. `graph.mutation.triple.add_batch` is atomic PER ENTITY only —
      cross-entity partial success returns `FailedSubjects` with a nil error. D5 survives
      via validate-then-commit + idempotent retry; no upstream issue filed
- [x] 1.4 Pinned `github.com/c360studio/semstreams v1.0.0-beta.158`; `go build ./...` and
      `go vet ./...` clean against the full import surface the slice needs

## 2. Vocabulary and payloads

- [x] 2.1 `internal/vocabulary`: 13 closed sets, 29 predicate constants, attribute-bounds
      registry (overridable per D-note, engine numbers as defaults), relation→predicate map,
      `BandForTotal`, and the now-advisory `RequiresRoll` mapping (D12). Closure proven by
      an AST scan, not convention; every predicate constant is proven acceptable by
      semstreams' own `vocabulary.ParsePredicate`, with an anti-vacuity test asserting that
      parser rejects the nine shapes F2 names
- [x] 2.2 `internal/payload`: all five types with explicit `RegisterPayloads(reg)` (the
      `init()` singleton was retired upstream in beta.18 — skill and reviewer contract
      corrected), alias-based `MarshalJSON` that does not wrap `BaseMessage`, and
      round-trip tests through `message.NewDecoder` over fully-populated fixtures. F6 is
      structural: `VerdictScalars` is its own type, so `Triples()` cannot grow to include a
      band. Identity fields gated as entity-ID segments (review HIGH-1: `action_id` becomes
      the turn entity's instance segment, so a bad one was a poison-message loop)

## 3. World loading

- [x] 3.1 `internal/world`: manifest v0 with strict YAML (`KnownFields(true)`), all five
      fields required, `engine_compat` as an explicit comparator grammar over semver
      (no caret/tilde — the engine ships `v1.0.0-beta.N`, which sorts *below* `v1.0.0`, and
      an author must be able to see that). F9 enforced as two ordered gates: upstream
      `ParsePredicate` first, then vocabulary membership; every failure a `*LineError`
      naming file and line
- [x] 3.2 Two-pass `local_id` → six-part mapping so forward references resolve, `local:`
      rewriting, dangling and self references rejected by name, every composed ID through
      `types.ValidateEntityID`. Determinism proven by whole-`Plan` value equality, not
      spot-checked IDs; two-worlds disjointness covered
- [x] 3.3 Importer materializes Graphable → graph-ingest with no direct `ENTITY_STATES`
      write anywhere (verified by review). Six integration tests against a **real** NATS
      container and a real graph-ingest started through its production lifecycle, reading
      back over the query surface. Missing Docker **fails** these tests — a skip-by-default
      was built first and rejected because `go test` hides a passing package's output, so
      "covered" and "never ran" were indistinguishable; `SEMMACHINA_SKIP_INTEGRATION=1` is
      the loud opt-out
- [x] 3.4 `fixtures/worlds/starter/`: 7 entities, 2 persona records, 1 rule stub, embedded
      so a broken package fails the build rather than booting an empty world. Authoring it
      exposed the gap it was meant to expose — the vocabulary could describe everything that
      *happens* to a character and nothing about who they *are*, hence `world.entity.name`,
      `.description`, `.kind`, `player.character.current`, and the `EntityKind` set

## 4. Dice component

- [x] 4.0 `internal/campaign`: fixed-width 32-byte seed from `crypto/rand` (fixed width is
      load-bearing — it makes `campaign_seed ‖ turn_id` unambiguous, so no two pairs collide
      by re-partitioning the same bytes), all-zero refused on both generate and parse since
      that is what a *forgotten* generation leaves behind and rolls deterministically wrong.
      `Gate.Claim` is F13's gate: one atomic create, `Fresh` on success, stored seed read
      back on `ErrorCodeEntityExists`. Instance segment is constant so two boots cannot
      claim different campaign IDs in one namespace. Proven against real infrastructure,
      including 8 concurrent claims yielding exactly one `Fresh`, and a claim refusing a
      campaign key occupied by a referential stub rather than reporting "already
      instantiated" over a factless one (F11)
- [x] 4.1 `internal/dice`: `MechanicSpec` registry keyed by mechanic version — the
      package-level `BandForTotal`/thresholds and `DiceCount`/`DieFaces` are gone, and
      `MechanicSpecFor` returns an error rather than a zero spec, because a zero spec bands
      every total as a full success. Seed = SHA-256(seed ‖ turn_id) → fresh PCG per roll,
      pinned against digests computed outside Go so a self-consistent-but-different
      derivation is caught. **Determinism is proven structurally**: a source-parsing test
      fails on any import of `time`/`math/rand`/`crypto/rand`/`os`, any package-level
      `math/rand/v2` call, or a PCG-typed field — a construction-time-seeded generator is
      caught by rolling a turn, rolling ten others, and rolling the first again
- [x] 4.2 Shared triple projection extracted (`internal/payload/triples.go`): validate →
      project only registered predicates → exactly one ref, rejecting unregistered
      predicates, missing registered ones, duplicates, ref/scalar collisions, write-gate-
      invalid predicates, and structurally any non-scalar object. `Verdict` and `RollResult`
      both use it, and `EffectBatch` (5.2) inherits it — but **`turn.phase.current` (6.2)
      cannot**, since the projection requires exactly one ref predicate and a phase write has
      none; extend it there rather than hand-rolling a second, weaker path. `WorldEntity` is
      the other documented exception (authored data with its own closure gates). The
      projection and merge client are production; the roll *write* is still a test helper —
      it is wired for real in 6.2 — proven against real infrastructure by a duplicate
      trigger leaving exactly one band, with a deliberate negative control showing the
      append lane leaving two

## 5. Effect applier

- [x] 5.1 `internal/effect/plan.go`: per intent, `EffectIntent.Mutation()` (vocabulary,
      field shape, bounds — one call, no parallel switch), then target existence via
      `EntityState.IsStub()` so a referenced-but-unborn entity cannot satisfy it (F11), then
      target-kind compatibility, then the same two checks on every referenced *object*.
      Rejection tests per failure class; no agentic or model package in the dependency tree
      (`go list -deps` verified)
- [x] 5.2 `internal/effect/applier.go`: idempotency guard on `turn.effects.batch`, full plan
      built before the first write, N merges (one per target, first-touch order), turn marker
      last. `EffectBatch` fits the shared projection unchanged — one scalar, one ref, no
      weakening. Multi-valued writes publish the complete set and removals use explicit
      predicate deletion (F14's third face). Seven integration tests against a real graph,
      including single-valued-applied-twice-leaves-one-value, add-relationship-keeps-
      siblings, remove-last-clears, and partial-commit-then-converge.
      **Carried to 6.2/6.3:** the applier *classifies* failures into closed
      `vocabulary.FailureReason` codes but does not write `turn.phase.current = failed` —
      the projection extension for phase writes is reserved for 6.2, so the
      effect-application spec's "rejected batch moves the turn to failed" is satisfied there,
      not here. Original text: implement whole-batch commit via `graph.mutation.*` with
      batch identity derived from `turn_id`; validate every intent before issuing any
      write. Commit through the entity merge lane, **never `triple.add_batch`, which
      appends (F14)**. The merge lane is per-entity and has no `FailedSubjects` field: a
      multi-target batch is **N merge calls, one per target entity**, each returning its own
      classified error. Never send a foreign subject in a merge request — graph-ingest
      splits it off onto the appending lane and swallows the failure (F14). A multi-entity
      batch is therefore still not atomic (F7 stands, by a different mechanism), and
      recovery is idempotent re-application keyed on `turn_id` under replace semantics (D2).
      `graphio.Store.MergeTriples` already refuses a foreign subject locally — rely on that,
      never on graph-ingest to route one for you;
      tests: validation rejection leaves the world unchanged, a failing per-entity merge
      fails the turn with the target named, re-application converges, committed effects
      graph-visible, and a single-valued effect applied twice leaves one value (the F14
      regression guard, against a real graph)

## 6. Turn intake and phase management

- [x] 6.1 `internal/turn`: durable consumer on `PLAYER_ACTIONS`, `Handle`'s return value
      *is* the ack decision — nil only after the turn entity exists. Accept uses the atomic
      `entity.create`, so the winning path has no read and therefore no read-then-write race
      to lose; `ErrEntityExists` reads back the phase and no-ops without resetting a turn
      already in flight. `MaxDeliver: 0` so a transport blip never drops a player's move,
      which is safe because a structurally-invalid action is terminated rather than nak'd
      forever — other deterministic-permanent classes deliberately nak-forever instead,
      preserving a recoverable action where terminating would destroy it. Proven against
      real infrastructure, including that a restart **rebinding the same durable consumer**
      does not redeliver an acknowledged action
- [x] 6.2 Phase FSM is **data** in `internal/vocabulary/phases.go` (predecessor table +
      rank) so 8.1's rule pack can author matching `transition` `from` sets from the same
      source. `Advance` gives three answers rather than two: advanced, resumed (already in
      the target phase — no write), declined (moved past — no write); a hop that *skips* a
      stage is an error, since a stale trigger and a wiring bug are otherwise
      indistinguishable. Merge-lane writes, with a negative control proving the append lane
      leaves two phases. The shared projection was **extended** with an explicit ref-less
      mode rather than relaxed — "may have a ref" would make a forgotten ref
      indistinguishable from a deliberate absence — and now asserts
      `RequireTurnEntityID` at the shared gate, so all four payloads inherit the pairing
- [x] 6.3 `Fail` writes phase and closed reason in **one merge** — two writes would leave a
      failed turn nobody can explain — with detail riding as a ref. `FailureReasonFor`'s
      unclassified branch is pinned by an integration test asserting a turn-record anomaly
      does not burn the player's turn. Original requirement: the reason on the graph MUST
      be a **closed reason code** from `internal/vocabulary` plus a ref to any detail — the
      shared triple projection gates object *shape* (scalar, bounded length), not closure,
      so an applier- or persona-authored sentence would pass every gate and land free text
      on the rule-matching surface. `RuleOpaque` (8.0) stops a rule branching on it; nothing
      stops it being written

## 7. Personas and context assembly

- [x] 7.0 `internal/content` wraps upstream `storage/objectstore` (no hand-rolled store).
      The whole canonical `PlayerAction` is stored, with `ReplyTo` and `ArrivedAt` asserted
      by name; the ref rides **inside** the atomic create, so the birth record is one
      `entity.create` carrying phase, player, scene and `turn.action.ref`; the key is
      `turn/<turn_id>/<slot>` where slot is the ref predicate's own category segment, so
      predicate and object location cannot disagree; and the object is written before the
      create, proven by a shared write journal because ordering is invisible in any
      end-state assertion. Stored bytes are canonical payload JSON rather than a
      `BaseMessage` envelope — `NewBaseMessage` mints a UUID with no override, so an
      envelope-wrapped re-put writes different bytes to the same key and "identical re-put"
      would be true of the location and false of the content. `Fail` now takes a typed ref,
      so "detail reaches the graph as a reference or not at all" is a type rather than a
      comment. Original: land the ObjectStore seam and `turn.action.ref` — **before the ack,
      not after**. The text is fiction (M1) and exceeds the triple object budget, so it can
      only reach the graph as a ref, and group 6 had no store. Until this lands, a crash in
      `accepted` leaves a turn the rule pack can re-trigger and the adjudicator cannot
      re-prompt: "ack only after the turn durably exists" is true of the paperwork and false
      of the player's words. Four requirements: (a) store the **whole canonical
      `PlayerAction`**, not just the text — `Channel.ReplyTo` is what 9.2's egress resolves
      the delivery target from and `ArrivedAt` is what deadline evaluation must use per
      project policy, and both currently die at the ack; (b) the ref rides **in the atomic
      create**, not a follow-up write, or it reopens the exact crash window this task
      closes; (c) derive the object key from `turn_id` so a redelivery re-puts identically;
      (d) write the object **before** the create (prose-first-ref-last — a ref to missing
      prose is a correctness bug, an orphan is only garbage). Same store backs the failure
      detail ref: until it exists, effect-application's "record the failed target" is not
      satisfiable
- [x] 7.1 `internal/scene`: fixed shape, **five reads always** — turn → scene (read off the
      turn, never supplied) → scene's incoming edges → one batch for members → one batch for
      their 1-hop neighbours. No recursion, so depth-1 is structural rather than a
      parameter. The bound is enforced *before* each batch is issued and **refuses rather
      than truncates**, because a persona handed part of a room narrates a room that is not
      there; every view carries its own size, tested to move with the context. Stubs are
      excluded with a reported reason, via the envelope discriminator — a test builds a
      fully-born entity still carrying the stub marker triple and asserts it is *included*,
      which a marker-based check would fail. The turn's own 1-hop is included too: without
      `turn.action.player` the adjudicator sees three people in a room and cannot tell which
      one is acting
- [x] 7.2 Before re-running a persona on a resumed stage, check whether that stage's
      artifact ref triple is already on the turn — present means the interrupted attempt
      actually finished, so advance instead of re-executing. The phase is written on stage
      *entry*, so it cannot distinguish "entered" from "finished"; the artifact ref can.
      This turns the re-billed call the resume path currently costs into a no-op, with no
      CAS required. **The assembler deliberately does not resolve `turn.action.ref`** — it
      is a graph query, and coupling it to the content store would put fiction-fetching in a
      component with no business reading fiction — so the prompt builder here must follow
      that reference itself, or the adjudicator receives a scene and no action. Then:
      configure the adjudicator loop (mid model slot per F5) + terminal tool enforcing
      the banded verdict schema and closed classes **in the executor**, not via provider
      strict-mode (F4); emit only rule-matchable scalar triples and carry banded intents by
      reference (F6); rejection test for out-of-vocabulary exit; `MaxIterations` cap with
      explicit failure. Two constraints from 10.2: **the terminal tool must not ask the
      model for identifiers the engine already knows** (turn, action, scene) — they cannot
      be scripted deterministically and a live model would hallucinate them, so the executor
      injects identity and the model supplies judgment only. And decide deliberately whether
      the adjudicator exits through upstream's existing `decide` tool — which F4 names as
      the ready-made closed-vocabulary seam, with a rule-supplied allowlist enforced in the
      executor — or a custom terminal tool.
      **Resolved: custom terminal tools.** `decide`'s schema is
      `{action, reason, subtopics, retry_hint}`, so a banded verdict would ride as JSON
      smuggled through a string — moving the schema out of the tool definition where a
      mid-tier model can see it, and into a parser, which is backwards for the slice's one
      schema-bearing exit (F5). It also publishes onto the *loop* entity via the
      **appending** lane (F14), so our single-valued turn scalars would need a second write
      regardless, and its allowlist closes one string field where we close four scalars, the
      effect vocabulary inside every band, modifier bounds, and gate/band coherence. The
      correction-message shape and stop-with-payload-in-content pattern were re-derived
- [x] 7.3 Narrator loop + terminal tool (prose ref + rule-opaque metadata
      only, no mutation-capable tools); prose-first-ref-last write ordering per narration
      spec

## 8. Rule pack and ledger

- [x] 8.0 Register every SemMachina predicate upstream via `vocabulary.RegisterPredicate`
      at bootstrap — **rule conditions reject canonical-but-unregistered predicates
      (F10), so the rule pack cannot load without this**. In the same pass mark `RuleOpaque`
      exactly those predicates **whose object is fiction** — entity description, verdict
      rationale, any predicate carrying prose inline. **`*.ref` predicates stay
      rule-matchable**: a ref is a structural pointer, and turn sequencing requires a rule to
      match on the narration ref landing to close the turn, so flagging it rule-opaque would
      fail the pack at load. This makes "no rule branches on prose" a load-time failure
      instead of a review convention (M1 enforced structurally). Note `RegisterPredicate`
      writes a process-global map: one exported entry point, called before any rule config
      loads in every binary and test, and registry-touching tests cannot be `t.Parallel()`.
      Test: a rule branching on a rule-opaque predicate fails validation, and one matching a
      `*.ref` predicate loads
- [x] 8.1 Author the turn-sequencing rule pack: accepted → adjudicator; roll-requiring
      verdict → dice; roll or no-roll verdict → applier; applied/rejected → narrator;
      narrated → complete + ledger. References only; caps on every LLM-triggering path.
      **Wire the two group-7 components that currently have no production caller** — if this
      lands without them, 7.2's cost saving silently evaporates and cap exhaustion silently
      stalls, and no test notices, because there is nothing to regress:
      (a) the spawner consults `persona.Guard.Check` **before** publishing the task, so a
      stage whose artifact ref already exists advances instead of re-running a billed call;
      (b) a cap-exhausted loop routes to `persona.RecordCapExhausted` rather than ending in
      silence. **And reconcile the guard with the recorder deliberately, not by discovery**:
      the guard answers "did this stage finish", the recorder answers "is this transition
      legal", and a skip decision taken while the phase is still `accepted` would have the
      guard say advance while the recorder refuses it as an illegal stage skip
- [x] 8.1a **Stranded-turn reconciliation (F22).** The `on_recovery` backstop the stage
      comments claim does not exist: bootstrap replay fires only for rules *currently
      matching*, and a turn parked mid-stage matches none, because every mid-chain rule is
      phase AND artifact and the artifact is what is missing. Unacked JetStream triggers
      resume a **crashed stage**; nothing resumes a **stranded phase** — and the persona
      stages ack after publishing, so a loop that dies leaves neither. Today a persona
      failing for any non-cap reason (a model error, far commoner than cap exhaustion)
      strands the turn permanently with a player waiting, so bounded execution's "never a
      silent stall" is unmet for that class. Build the reconciliation that finds turns whose
      phase is non-terminal with no stage running, correct the three comments and this file's
      claim, and move the loop-failure notification onto a durable consumer — with one
      consumer and a billed consequence, the fan-out argument for core NATS is weak.
      **Landed as `internal/resume`.** The probe went further than F22 did: bootstrap replay
      rescues **none** of the three parked shapes, including the one whose artifact is
      present, because upstream's durable stale-replay guard skips any evaluation whose
      source revision it already recorded — and a parked turn is exactly a turn nobody has
      written to since. `TestBootstrapReplay_RescuesNoParkedTurn` keeps the measurement.
      **Built twice and rescoped once, and the reason is worth more than the code was.** The
      first two versions asked the rule pack (`rulepack.Matcher`, since deleted) whether a
      rule matched the turn and inferred a MESSAGE from the answer — first that the hop had
      gone missing, so republish it (two billed adjudicator spawns on the first hop, where
      no artifact exists for the resume guard to skip on); then that the hop was durably
      pending, so leave it (strands every turn whose rule fired and whose publish did not).
      Both are underivable: `natsclient` publishes fail on an open circuit breaker, a
      disconnected client, or a refused PubAck, the breaker short-circuits a whole batch
      after it opens, the rule engine persists match state whether or not its action
      succeeded, and a rule action has its own `max_iterations`. Each patch to the inference
      surfaced another route, which is the signal that the inference was the defect.
      **So the pass now MEASURES**, across every durable queue that can deliver work for a
      turn rather than one of them — `TURN_STAGES` and `AGENT`, because a stage acks its
      trigger when it PUBLISHES the persona task and the real work then sits unacked where
      upstream redelivers it. It reads each subject from its consumer's acknowledgement
      floor (finding the loop's consumer by FILTER SUBJECT, since its name is an unexported
      sanitisation plus an operator suffix — semstreams#733), and disposes on that: queued means the substrate owns it, counted and
      untouched; nothing queued with the artifact absent is stranded and gets a stage
      re-triggered under `resume.MaxAttempts`, then `vocabulary.FailureTurnStranded` — its
      own stage, or for a turn still in `accepted` the FIRST stage, which is the single
      unconditional derivation the pass makes (index 0 only; any other index is a route
      table);
      nothing queued with the artifact present has the sighting counted and ends once the
      budget is gone, because re-running that stage would resume a phase the turn is already
      in and skip on the artifact — and ending on a single reading could fail a turn whose
      work landed between the queue measurement and the turn read. Nothing
      derives a hop from the pack — `PhaseRank`/`PhasePredecessors` are deliberately unused.
      Ordering settled by probe, not argument: bootstrap replay's timing relative to the
      processor's `Start` is a race (measured 1.02s after, and measured already-landed), so
      composition starts the rule processor first and the pass waits for the stage stream to
      go quiet before reading. Upstream has no bootstrap-replay completion signal — an
      engine ask
- [x] 8.1b Two cleanups 8.1 surfaced: ~~(a) the starter world's `rules/00-turn-sequencing.json`
      stub is now misleading — turn sequencing lives in `internal/rulepack` because it is the
      *engine's* state machine and a downloaded world must not be able to author or break the
      turn loop, so replace the stub with a genuinely world-scoped rule (a world reaction) or
      relax the loader's "at least one rule file" requirement~~ **DONE**, and the rationale
      for the cleanup was itself aspirational: `checkRuleFile` validated one thing — predicate
      SYNTAX on condition fields — so a downloaded world could ship a rule matching
      `turn.phase.current`, publishing to `semmachina.turn.narrating`, and carrying no cap, and
      nothing refused it. `internal/world/rulescope.go` now refuses at LOAD, naming the rule id,
      the position and the reason, across the FOUR positions a rule can carry a name the engine
      answers to. **Predicates**, in any condition, any per-action `when` guard (which upstream's
      own `validateConditionFields` does not walk), or any triple-mutating action — reserved at
      the width each justification supports: `turn.*` whole, because every predicate in that
      domain is loop state written by a guarded stage, and `campaign.seed.*` ONLY, because the
      harm there is replay determinism and the rest of that domain is world content — the
      campaign CLOCK lands in it, and CLAUDE.md makes clock deadlines the canonical world
      reaction, so a domain-wide reservation would have refused the exemplar world rule of the
      whole project before any world shipped to argue back. **Subjects**, for any publishing
      action, against three roots: the engine's own (derived from `rulepack.StageSubjectPrefix`
      rather than listed, so the ledger and task 9's intake are covered without a list nobody
      remembers to extend), the whole AGENTIC root (from `persona.AgentSubjectFilter` — not just
      the task lane, because the loop declares five inputs and `agent.signal.*` alone carries
      `retry`, which spends, and `feedback`, which injects author text into a running persona's
      context), and `tool.result.` (the fifth input, which sits outside `agent.`; a forged tool
      result is fabricated evidence handed to a live adjudicator). Reserved on the SUBJECT rather
      than by refusing `publish_agent`, because refusing the action type leaves the identical
      spend available to a plain `publish`. **Buckets**, for `update_kv`: upstream's
      `FrameworkOwnedBuckets()` (taken from upstream so the set cannot fall behind it) plus
      `RULE_STATE`, which is NOT on that list and holds the rule engine's `MatchState` — a world
      rule merging an empty `action_iterations` map into it switches off the per-action caps on
      the engine's own hops, at fire time, silently. In every reserved position the name must be
      a LITERAL, because the rule engine resolves `Action.Predicate`, `Action.Subject` and
      `Action.Bucket` at fire time and a literal-only gate is evaded by a template while
      reporting the package clean; and any action type the boundary has not classified is refused
      by name, so upstream's next one cannot be admitted unchecked. The stub is replaced by a
      genuine world reaction (`00-spent-supplies.json` — a spent stack loses its location: REMOVE
      not add per F14, capped ON THE ACTION, naming nothing the engine owns), and the "at least
      one rule file" requirement is relaxed to match, since a world that reacts to nothing is
      legitimate and that requirement is exactly what forced a placeholder rule into existence.
      **The other half of the standing invariant is DEFERRED, deliberately, and the remaining
      claim is now small**: caps are NOT mandatory on world rules that reach an LLM-triggering
      path. Every subject that reaches a model today is reserved — all five agentic-loop inputs,
      pinned by a test that reads the loop's own port declarations and fails when upstream adds a
      sixth — so what stays open is a world spending through a path nobody has enumerated: a
      future component consuming a subject a world rule may legitimately name. Whoever builds the
      cap check inherits one PRECONDITION that already cost this task a defect: **which field is
      the cap**. `Definition.MaxIterations` is not enforced for expression rules (the evaluator
      records it into `MatchState` and republishes it as `$state.max_iterations` for a `when`
      guard to read; cron rules reject the field outright), while the enforced cap is per-ACTION
      `Action.MaxIterations`, defaulting to `rule.DefaultActionMaxIterations` = 3 when omitted. A
      check written against the definition-level field would validate a field that does nothing
      and pass every uncapped world; ~~(b) add the closed
      `vocabulary.FailureReason` for a persona loop that fails for a reason **other** than
      its cap~~ **DONE in 8.1a** — `FailurePersonaLoopFailed` and `FailureTurnStranded` both
      landed, and `LoopFailureWatcher` records the first rather than logging it. The claim
      this line used to make — that the turn waits "until the next boot's recovery replay" —
      was false in both halves: no replay fires for a parked turn, and there was no code to
      record; ~~(c) tighten `checkArtifactGate`~~ **DONE** — it now asks the specific
      question: for each phase a rule pins with `eq`, does another condition name a predicate
      in `vocabulary.StageArtifacts(that phase)`? The old "gated on something besides the
      phase" test is gone, so a *birth-record* fact like `turn.action.player` — present the
      whole time — can no longer look like a gate while gating nothing and reintroducing
      F21's race. Two refusals were needed to make the gate TOTAL rather than skippable, and
      both are load-bearing: a rule must PIN a phase (one with only artifact conditions gave
      the gate nothing to read, so it would have passed in silence — a gate that answers
      "fine" to a question it could not read is the exact shape (c) exists to close), and
      `ne` on `turn.phase.current` is refused (only `eq` and `transition` pin a phase;
      without this, rekeying the gate on `eq` would have been WEAKER than the gate it
      replaced, in a case the old one caught — a tightening that is secretly a regression).
      Terminal phases matched with `eq` get their own refusal: `complete`/`failed` own no
      stage, so no artifact could ever satisfy the gate; ~~(d) check FSM-edge legality~~
      **DONE** — `checkPhaseEdges` uses `vocabulary.PhasePredecessors` for legality and
      `PhaseRank` to classify the diagnosis ("runs the turn backwards" vs "skips a stage"),
      so a pack can no longer express `accepted → narrating`. Note this is the LEGITIMATE use
      of the phase-succession vocabulary that 8.1a forbade: 8.1a's warning was about
      *executing* a derived hop at runtime, which is a second FSM; this compares two
      declarations before the engine starts and executes nothing. `rulepack.PhaseForSubject`
      is the derived inverse of `SubjectForPhase`, so there is still one pairing. Applied to
      `transition`-pinned hops as well as `eq`, and order is load-bearing — edges are checked
      AFTER `checkActions`, so a typo'd subject reads as a typo rather than an illegal edge;
      ~~(e) three pack gates have no regression test~~ **DONE** — `logic != "and"`, the
      on_enter/on_recovery subject agreement, and `TerminateDelivery` on a poison trigger.
      The third was a proven green lie, not a suspected one: the OLD test body was run against
      a runner mutated to return a plain error instead of terminating, and it PASSED. The
      replacement observes the poison message itself — publish with ack, then poll the stage
      consumer until its `AckFloor.Stream` passes that sequence, inside a window deliberately
      under the framework's 30s nak delay. **Supervision note:** (d)'s extension to
      transition-pinned hops was initially unreachable — both of the pack's transition-pinned
      rules publish `SubjectResolved`, which `PhaseForSubject` declines, so the branch was
      real code no rule could exercise and a mutation disabling it survived the whole suite.
      Closed with a case that retargets a transition hop at a real stage across an illegal
      edge; nothing else in the pack can refuse that pack, which is what makes the case prove
      the branch rather than merely reach it
- [x] 8.2 Record the roll-gate agreement on `TurnManifest` — reported gate, advised gate,
      and the **mapping version**, which does not exist yet: `RollGateExpectation` carries
      no version and `vocabulary.RequiresRoll` has no version constant, so add one here
      rather than discovering it mid-ledger. Resolves a question 7.2 raised rather than
      guessed.
      It is derivable from the stored verdict today, so a field looks redundant; it is not,
      because the mapping is advisory and expected to be tuned with play, and a derived
      value would silently flip for every historical turn when it changes. Payloads are
      cheapest to change before the ledger holds records. Then: implement the ledger writer:
      `CAMPAIGN_LEDGER` stream (no age/size eviction),
      one manifest per resolved turn keyed by `turn_id` (including failed turns),
      duplicate-append dropped; world-time field present (zero)
- [x] 8.3 Implement a minimal replay reader proving replay honesty: re-execute the roll
      from a manifest byte-identically; read narration from the preserved ref

## 9. Player I/O adapters

**Scope correction (recorded 2026-07-29, before implementation).** These two tasks were
authored assuming SemStreams' `input/websocket` + `output/websocket` pair (verified to exist
in 1.3). Verification since says the EGRESS half cannot be used: `output/websocket`
broadcasts every message to every connected client — `output/websocket/doc.go:229` lists
"No per-client subject filtering" as a documented limitation, and `broadcastToClients`
iterates the whole client map. Composing it would satisfy "the result was delivered" while
showing one player's narration to every player in the campaign. Nothing in upstream's open
changes proposes selective routing, so a version bump does not fix it. The session gateway
below is SemMachina's, and per-recipient delivery is filed upstream as a generic ask rather
than pretended to be a game concern.

Still an ENGINE SPIKE with a raw client — the proposal's "No creator UI" non-goal stands and
the Svelte creator surface remains project.md Sequencing stage 3. What changed is that the
PROTOCOL is specified rather than assumed, because the protocol is what the eventual client
speaks and it is expensive to retrofit.

- [x] 9.0 Define the public player protocol before writing an adapter, as `internal/payload`
      types with the same registry discipline as every other payload (`new-payload` skill):
      a versioned **`SubmitAction`** carrying only client-owned fields (action text, an
      idempotency key) and a **`TurnResult`** covering successful AND failed terminal turns.
      `PlayerAction` stays the ENGINE's contract and is never the wire format — every one of
      its identity fields is server-owned, and `action_id` is the sharpest: it is derived
      deterministically so a redelivered channel message makes one turn rather than two, so
      a client that could choose it could pre-claim another player's action id and have that
      player's turn silently absorbed as a duplicate. A request carrying a server-owned field
      is REFUSED, not sanitized, so a confused client learns rather than being quietly
      corrected. Design `TurnResult` against the resolution card project.md stage 3 names
      (plausibility, risk, modifiers, roll, consequence) — free now, retrofit later
- [x] 9.1 Implement the **player-session gateway** (SemMachina's, not SemStreams'):
      authenticate a session to a `player_id` graph entity, map that identity to the current
      connection, validate `SubmitAction`, derive `action_id`, stamp arrival time and the
      channel binding, publish the canonical `PlayerAction`. Test that `player_id` survives
      reconnect (identity ≠ connection). **Admission control is part of this task, not a
      follow-up**: refuse an action while that player holds a non-terminal turn, answering a
      structured `turn_in_progress` naming the active `turn_id`. It belongs at ingress
      because that is the only place a player is still connected to be told — `turn/intake.go`
      already argues this from the other side, in the comment explaining why a TERM there is
      the quietest failure in the component. Queueing is a later change; refusing is the MVP
- [x] 9.2 Implement **targeted** egress: deliver a turn's result to the player it belongs to
      and to no other connected player, resolving the connection from `player_id` at
      DELIVERY time rather than from an identifier captured at submission.
      `ChannelBinding.ReplyTo` is a durable address where the adapter has one (email, a chat
      channel) and is not one for a WebSocket, so the per-adapter contract must say which it
      is rather than leaving `content.Store`'s "the egress adapter resolves the player's
      delivery target from it" to mean both. Retrieval from durable state, never adapter
      memory: by `action_id` or `turn_id`, plus **last TERMINAL result** — not "last
      completed". A failed turn's narration is not guaranteed (one that died before the
      narrator ran has none; one abandoned after its prose landed has some — see
      `internal/resume`), so a surface keyed on narration cannot answer for it at all, and one
      keyed on completion leaves the player who most needs an answer with silence
      indistinguishable from a turn still running. The ledger already archives failed turns.
      **Two seams 9.0 and 9.1 left open, both to be closed here rather than discovered:**
      (a) `player.turn.current` names the most recent turn, which answers "last terminal" only
      while that turn IS terminal — when a turn is live, nothing names the previous terminal
      one; (b) `TurnResult.NarrationRef` is a storage reference and no client can resolve an
      `obj://`, so who dereferences it, and whether the delivered document differs from the
      published one, is undecided
- [ ] 9.3 Local-only security posture, stated and tested rather than assumed: what
      authenticates a session on an instance-per-world box, what happens to an unauthenticated
      socket, and what a second connection claiming the same `player_id` does. MVP may answer
      these narrowly — it may NOT leave them undefined, because every one of them is a
      disclosure decision and the egress path above is where a wrong answer shows up as one
      player reading another's fiction.
      **Three obligations 9.1 and 9.2 deliberately left here rather than solving in the wrong
      layer**, recorded so the transport author inherits them instead of inferring them from a
      comment:
      (a) `gateway.MaxRequestBytes` is a limit **the transport must apply to its own reader**
      (`websocket.Conn.SetReadLimit`, `http.MaxBytesReader`, or the equivalent). `Gateway.Submit`
      takes a `[]byte`, so the frame is already wholly in memory by the time its own check runs —
      that check prevents the second allocation `json.Unmarshal` would make and is a second line
      of defence, NOT the bound. An adapter that reads "the gateway bounds the frame" and skips
      its reader limit has moved the bound one allocation too late; (b) the session table has
      **no bound, no expiry, and no way to notice a connection that left** — an entry is created
      by `Authenticate` and removed only by `Disconnect`, so a transport that drops a socket
      without calling `Disconnect` leaks it permanently. Not solved in 9.1 on purpose: a TTL, a
      cap, and an eviction policy are all decisions about what happens to a player who goes
      quiet, and this project's pacing rule makes an idle-session timeout the exact
      interactive-pacing assumption email-cadence play forbids. Connection lifecycle belongs
      with the rest of the posture here; (c) **a connection id must never be reused without
      going through `Authenticate`** — the sharpest of the three, and NEW with 9.2. The session
      table is now indexed by player as well as by connection, so a stale session is no longer
      a leaked map entry: it is a live DELIVERY TARGET. A transport that recycles `c1` onto a
      fresh socket without re-authenticating leaves the previous session bound, and that socket
      can both submit actions as the old player and receive that player's fiction. Nothing in
      the gateway can detect it — `Authenticate` is the only place a connection's identity is
      established, so a transport that skips it presents an impersonation as an ordinary
      session. The table's own handling is correct and reviewed: `put` unbinds a connection
      from its previous player before rebinding, and `unbind` deletes empty player entries, so
      the index adds no growth the connection index did not already have. The obligation is on
      the transport, and it is stated here because a comment in `session.go` is not where the
      transport author will look

## 10. Composition, mock-LLM E2E, and gates

- [ ] 10.1 Compose `cmd/semmachina`. **BOOT ORDER IS PART OF THE TASK, and violating it is
      silent and destructive**: ensure the stage-trigger stream → start the rule
      processor → run `resume.Reconcile` → bind the stage runners → bind intake. The
      stranded-turn pass reads which turns the substrate still holds work for and acts on
      what is missing from that set, so (a) the stream must exist or a re-trigger publish is
      refused, (b) the rule processor must have STARTED, because its bootstrap replay
      publishes into the very set the pass reads and *an unstarted processor is
      indistinguishable from a quiet one* — the pass cannot detect the violation, it can
      only be handed the wrong order — and (c) no player action may be accepted during the
      pass, or a turn born mid-scan reads as stranded a millisecond after its creation.
      Getting (b) wrong ends live turns: the pass fails a turn it believes nothing is coming
      for, `failed` is terminal, and the replayed trigger is then declined. The gap between
      "started" and "finished replaying" is the one part the pass enforces itself
      (`WorkQueues.Settle` waits for the queues to stop moving); the ordering is the
      composition's. See `internal/resume`'s package doc.
      Flow config wiring importer, intake, rules, personas,
      dice, applier, ledger, adapters, **plus `graph-index`** — scene membership is asserted
      by the member (`world.location.current` → scene), so "who is here" is a reverse lookup
      the assembler answers via the incoming index rather than by scanning the world. Its
      readiness gate matters: a mid-build index returns a partial keyset, which reads as a
      smaller scene rather than an error. **Gate on a positive readback — poll the incoming
      query until the imported membership edges actually appear — never on the absence of
      `ErrIndexNotReady`**, which is inert in exactly that window: the index latches ready
      when the target count is zero and enumeration completes, so on a fresh
      instance-per-world boot it reports ready *before the import writes anything*; `RegisterPayloads` called at every binary's bootstrap
      (grep-check across `cmd/`). **Guard instantiation: import only when the world instance
      does not already exist** — a boot-time import into a live campaign resets every
      template-declared fact and drops play-created relationships, which is the exact
      inverse of "a restart must not replay the dragon eating you". Gate it the way
      semstreams seeds rule configs into KV (`ConfigManager.SeedFromRuntime`: Create-not-Put,
      key-exists treated as a no-op, so operator edits are never overwritten): **one atomic
      `graph.mutation.entity.create` of the campaign entity is the whole gate** — success
      means a fresh world, `ErrorCodeEntityExists` means already instantiated, skip the
      import. Do NOT apply create-not-put per template entity: referential stubs occupy
      keys, so a referenced entity's own create would return key-exists and it would stay a
      permanent stub (F11). Boot-readiness must also exclude stubs via `EntityState.IsStub()`.
      **Keep the two persona terminal tools out of the approval/governance path**: upstream's
      approval re-dispatch rebuilds a bare tool call and propagates only a closed framework
      metadata list, so a persona tool routed through it loses the engine-injected identity
      and every exit fails as an internal error. Governance is disabled by default and
      nothing enables it, so this is fail-closed rather than a present defect — but enabling
      it without this in mind makes the identity contract silently unwireable.
      **Boot ordering**: the spawner publishes to a stream the agentic-loop component owns,
      so the loop must be started (or the stream ensured) *before* ingress opens — otherwise
      every persona stage nak-loops. Note a JetStream publish is a core publish underneath,
      so a missing stream can look like it worked while leaving deliveries unacknowledged;
      assert `NumAckPending == 0` on stage consumers rather than trusting the turn completed.
      **The claim alone is not enough**: it answers "was this campaign created", not "did
      the import that followed it finish", so a crash mid-import leaves a claimed campaign
      and a partial world that boot would then skip. Write an import-completion marker after
      the import and gate on it — absent campaign → import; campaign without marker →
      interrupted; campaign with marker → skip. Three things the marker must actually
      promise: (a) it gates **ingress**, not just the importer — no `PLAYER_ACTIONS` message
      is consumed and no persona runs until it is observed, otherwise a partial-world boot
      accepts an action and the re-import clobbers play-created state; (b) "import finished"
      means every planned entity is **queryable and non-stub**, not merely durably queued —
      the importer acks on publish, and those are different instants; (c) only the claimant
      that observed `Fresh` imports — a claimant seeing campaign-without-marker waits a
      bounded time and then fails loudly, since otherwise a late loser could write the
      marker while the real importer is mid-flight, manufacturing the marked-but-partial
      world the marker exists to prevent. Write the marker through the merge lane (F14) so
      it replaces its own predicate and leaves `campaign.seed.value` intact. Serving play
      from a half-imported world is the failure this prevents — and the context assembler
      (7.1) is the silent reader: stub-filtering catches referenced-but-unborn entities, but
      never-published ones just make a scene quietly smaller
- [x] 10.2 **Pulled forward ahead of 7.2/7.3** so the persona loops are testable end-to-end
      as written. `internal/mockmodel`: HTTP stub on the OpenAI-compatible chat-completions
      contract the pinned client actually speaks, responses built from the framework's own
      wire types. Selection is **structural** (model + declared tool names, never prompt
      text — `AgentRequest.Role` is not forwarded over HTTP); subsuming matches are rejected
      at load, and unmatched/unscripted/exhausted/streaming are all loud 400s with stable
      codes. **There is no default response.** Script exhaustion is the duplicate-delivery
      assertion: a re-invoked persona gets a refusal naming the role rather than quietly
      buying a second billed call. Bad output is first-class — out-of-vocabulary arguments,
      raw bytes no encoder would produce, prose with no exit, truncation, provider errors,
      and `repeat` for a persona that never terminates (so 7.2's `MaxIterations` has
      something real to stop). Determinism proven behaviourally *and* by an AST scan banning
      clocks and randomness. Re-derived from the semstreams contract; semdragon not read
      (M6). 31 mutations, 4 survivors fixed — see F18
- [ ] 10.3 E2E scenarios (token-free, full production wire). **Band scenarios are selected
      by seed, not by script** (F19): a verdict declares all three bands and the seeded dice
      choose, so supply the (campaign_seed, turn_id) pair whose derived roll lands in each
      band and pin them as fixture constants — if they stop producing their bands, seeded
      replay has broken. **Assert provider shape at the wire** where fidelity matters (F18):
      the client normalizes token totals, finish reasons, and malformed arguments, so
      asserting through it hides the defects the mock exists to produce. Scenarios: no-roll
      turn; miss / partial / full band turns; invalid-effect rejection turn — **note the
      mock's current `invalid-effect` script no longer reaches the applier**: it uses an
      out-of-vocabulary effect *type*, which executor-side validation now refuses at the
      tool boundary, so an applier-rejection scenario needs a **well-formed intent naming a
      wrong-*kind* target** (moving a character into an item, F16's own example), and that
      step's `rejected` entry must be flipped; duplicate action delivery;
      crash-resume at each phase (kill between roll and apply at minimum); reconnect
      retrieval; email-cadence gap (clock-independent processing)
- [ ] 10.4 CI workflow: lint (revive-clean), `go test -race ./...`, mock-LLM e2e; no live
      inference anywhere in the gate. Assert **zero skips** across the module — that count
      is load-bearing now that missing infrastructure fails rather than skips, so a skip
      creeping back in is a coverage regression the suite would otherwise report as `ok`.
      Note `internal/vocabulary` trips revive's `max-public-structs` (a closed vocabulary is
      exported types by construction — raise or disable it for that package rather than
      contorting the vocabulary), and a few `t.Fatalf` calls with constant strings will want
      `t.Fatal` once a linter lands
- [ ] 10.5 Run `semmachina-reviewer` on the full slice pre-merge; resolve findings;
      update task truth conservatively with evidence
