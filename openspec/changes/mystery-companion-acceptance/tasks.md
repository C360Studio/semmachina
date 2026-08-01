## 1. Vocabulary and Bellweather Package

- [x] 1.1 Add failing tests for case, evidence, belief, knowledge, revelation, and
  companion-bond vocabulary, including immutable and rule-opaque classifications
- [x] 1.2 Add failing package-validation tests for one complete case, six suspects,
  exactly twelve clues/red herrings, ordered timeline, and complete solution references
- [x] 1.3 Implement the registered vocabulary and typed authoring records required by
  those tests
- [x] 1.4 Author `fixtures/worlds/bellweather-maze/` with Kit Finch as a character-backed
  companion candidate and no engine-hardcoded world content
- [ ] 1.5 Reject import, effect, world-rule, and operator-write attempts that mutate or
  branch on canonical solution and truth-status predicates
  - Local package, rule, and effect gates are complete; full graph-ingest/operator
    enforcement is blocked by `C360Studio/semstreams#818`.

## 2. Case Lifecycle

- [x] 2.1 Add failing tests for the
  `cold_open → discovery → investigation → accusation → denouement` phase graph
- [x] 2.2 Implement and register `CaseState` as a SemStreams lifecycle participant with
  lifecycle-manager-exclusive phase writes
- [ ] 2.3 Add recovery and duplicate-event tests proving one legal transition per
  structured case event
  - Local duplicate/stale receipt no-op and built-in rule recovery parity are covered;
    production duplicate delivery awaits the event producers and durable delivery path in
    groups 4 and 6.

## 3. Epistemic Projection Safety

- [x] 3.1 Add table-driven failing tests with unique entity IDs and unique text values for
  culprit, unrevealed-clue, and revealed-clue canaries across casekeeper, player,
  companion, public adjudicator, narrator, denouement, verifier, and operator purposes
- [x] 3.2 Implement the centralized closed-purpose `EpistemicProjector` with bounded reads
  and complete omission of unauthorized entities and identifiers
- [x] 3.3 Route adjudicator and narrator prompt assembly exclusively through the projector,
  pin public-adjudicator acting-actor knowledge and exclusions, and remove the
  audience-agnostic scene view as a direct persona input
- [ ] 3.4 Assert canaries at projector, serialized prompt, mock-model request, and player
  egress boundaries before player-facing or full Bellweather E2E acceptance
  - Projector, serialized-prompt, and actual mock-model `Call.Body` canary boundaries are
    complete. Raw player-egress byte assertions remain and must close before player-facing
    or full Bellweather E2E acceptance.

## 4. Case Decisions and Interpretation Stage

- [x] 4.1 Add failing schema, validation, closed-vocabulary, deterministic-ID, eight-target,
  twelve-reveal, duplicate-reference, accuse-field, JSON, and production-decoder tests for
  `CaseDecision`
- [x] 4.2 Implement `CaseDecision` with `Schema()`, strict `Validate()`, alias-only JSON
  methods, explicit payload registration, and all production/test bootstrap wiring
- [x] 4.3 Add the casekeeper role and terminal tool with private purpose-scoped context and
  no rule-visible prose fields
- [x] 4.4 Add the durable `interpreting` turn stage, package-driven applicability, and a
  deterministic no-model-call artifact for non-mystery turns
- [x] 4.5 Add restart and duplicate-delivery tests for one logical case decision per turn

## 5. Knowledge and Testimony

- [x] 5.1 Add failing authorization tests for eligible reveals, premature solution facts,
  wrong actors, wrong questioned targets, and attributed false testimony
- [x] 5.2 Implement `KnowledgeGranter` with actor-scoped grants and committed turn
  revelation receipts
- [x] 5.3 Add witnessed-discovery and explicit-share flows proving player and companion
  knowledge remain independent
  - Production composes the durable graph-backed companion authority as both
    `ShareAuthorizer` and `WitnessAuthorizer`. Witnessed discovery plans separate player
    and companion grants before persistence; explicit sharing still proves the source
    actor knows each cited item and authorizes the bonded recipient independently.
- [x] 5.4 Keep testimony and narration prose in ObjectStore and graph/rule paths limited to
  structural references

## 6. Deterministic Accusation

- [x] 6.1 Add failing exact-ID tests for wrong culprit, method, motive, mixed failures, and
  a complete correct accusation
- [x] 6.2 Add schema, strict validation, deterministic-ID, closed-vocabulary, alias-only
  JSON, registry-bootstrap, and production-decoder tests for `AccusationResult`
- [x] 6.3 Implement `AccusationVerifier` without model calls or fuzzy comparison and emit
  the registered closed `AccusationResult` payload
- [x] 6.4 Add rules that enter `accusation`, retain that phase after a wrong result, and
  request `denouement` only after a correct result
- [x] 6.5 Prove wrong results disclose no failed dimension and correct results alone unlock
  canonical solution context for denouement narration
  - Every turn crosses the durable accusation artifact barrier so narration cannot race
    verification; only an identity-bound correct result authorizes denouement context and
    transition.

## 7. Companion Decisions and Bond

- [x] 7.1 Add failing schema, validation, closed-vocabulary, generic-context,
  deterministic-ID, eight-evidence, duplicate-reference, JSON, and production-decoder
  round-trip tests for `CompanionDecision`
- [x] 7.2 Implement `CompanionDecision` with `Schema()`, strict `Validate()`, alias-only
  JSON methods, explicit payload registration, and all production/test bootstrap wiring
  - The registered payload is prose-free and generic-context. The terminal executor
    injects authoritative identity, stores the decision through an immutable exact-resident
    claim, and writes the graph reference only after the resident artifact is verified.
- [x] 7.3 Implement the durable player-companion bond and package-independent companion
  role using generic `context_ref` and only the companion actor's epistemic projection
  - Instance resolution imports the deterministic bond as world state. The generic
    companion persona, strict tool, actor-scoped projector, executor, and boot/model
    capability configuration are package-independent.
- [x] 7.4 Add runtime authorization that refuses any evidence reference outside the
  companion's knowledge even when the payload is schema-valid
  - The executor revalidates the imported bond and rejects evidence absent from the
    companion projection before any ObjectStore or graph write.
- [x] 7.5 Prove a second arbitrary player-character bond works outside Bellweather and
  outside any mystery case without an engine branch
  - The Starter-world Rook/Wren integration proof exercises ordinary instance import,
    generic scene `context_ref`, companion projection, prompt/tool assembly, exact-resident
    decision commit, and turn reference without mystery state. Independent backend review
    approved Group 7 after the authorization and resident-claim fixes.

## 8. Hint Ladder and Bounded Initiative

- [ ] 8.1 Add failing tests for `nudge → connect → next-step`, final-level bounding, exact
  reset on a newly committed companion knowledge grant, and no reset on duplicate or
  rejected grants
  - Ladder progression, final-level saturation, deterministic projection-bounded evidence
    selection, and serialized advances are implemented. The knowledge-driven reset remains
    blocked: it requires a revision-bearing authoritative entity read plus
    expected-revision/conditional graph mutation. This reset gap belongs to 8.1, not 8.5,
    and no external SemStreams issue has been filed for it.
  - The current keyed bond lock is process-local and assumes one process per world;
    active-active ladder compare-and-swap is unsupported.
- [x] 8.2 Implement deterministic `HintLadder` selection using companion-known evidence
  - Selection intersects exact companion knowledge records with the authorized companion
    projection, sorts and deduplicates evidence, and applies the fixed per-level bound.
- [x] 8.3 Add structured automatic-intervention triggers whose rule-owned nonzero cap is
  the hard ceiling, with bond/component policy allowed only to tighten admission and a
  silent exhaust path
  - Closed player-hint and resolved-risk triggers are implemented; the automatic rule and
    companion persona are capped at one, and cap exhaustion commits a terminal silent
    outcome.
- [x] 8.4 Add the durable `companion` turn stage and deterministic no-model-call artifact
  when no active bond or trigger applies
  - Applying now crosses an artifact-gated companion stage before narration. No-bond and
    no-trigger paths commit exact-resident structural no-op records without a model task.
- [ ] 8.5 Add restart and duplicate-delivery tests proving one companion call and one
  resolution at most per triggering turn
  - One logical task publication is identified deterministically per turn, and one
    exact-resident companion stage outcome plus graph reference is proven across retries.
    Literal at-most-one provider call across a process crash remains blocked: it requires
    a durable atomic `TaskID → LoopID → initial RequestID` claim and idempotent request
    publication. No external SemStreams issue has been filed for this gap.

## 9. Narration and Player Protocol

- [ ] 9.1 Extend narration to voice only committed case revelations and companion
  decisions from the player-authorized projection
- [ ] 9.2 Add denouement-purpose narration and prove canonical solution canaries remain
  unavailable before a correct accusation
- [ ] 9.3 Add `CompanionResolution` with closed kind and conditional hint validation; pin
  no active bond as absent and an active `silent` decision as a present silent summary
- [ ] 9.4 Pin the new player/v1 fields, closed sets, delivery identity, and retrieval
  round-trip in protocol tests

## 10. Acceptance and Live Smoke

- [ ] 10.1 Script the deterministic mock-model Bellweather path through discovery,
  investigation, testimony, sharing, all hint levels, wrong accusation, correct
  accusation, and denouement
- [ ] 10.2 Add one full-stack mock E2E asserting case progression, companion behavior,
  idempotency, and secret canaries with unique IDs and text values at every boundary
- [ ] 10.3 Add an opt-in Taskfile Gemini smoke for one short investigation and Kit
  exchange, loading `.env` without printing the key and excluding it from CI
- [ ] 10.4 Document active polling and fast-abort checks for the paid Gemini smoke

## 11. Quality Gates and Acceptance

- [ ] 11.1 Run backend architecture review for lifecycle ownership, rule/component/persona
  boundaries, and absence of SemStreams substrate workarounds
- [ ] 11.2 Run backend code and security review for secret authorization, idempotency,
  payload registry coverage, and error handling
- [ ] 11.3 Run unit tests, race tests, mock E2E, lint, build, and strict OpenSpec validation
- [ ] 11.4 Run the opt-in Gemini smoke only with operator authorization and record its
  result separately from deterministic acceptance
- [ ] 11.5 Archive this change only after every normative scenario and quality gate passes
