## 1. Vocabulary and Bellweather Package

- [x] 1.1 Add failing tests for case, evidence, belief, knowledge, revelation, and
  companion-bond vocabulary, including immutable and rule-opaque classifications
- [x] 1.2 Add failing package-validation tests for one complete case, six suspects,
  exactly twelve clues/red herrings, ordered timeline, and complete solution references
- [x] 1.3 Implement the registered vocabulary and typed authoring records required by
  those tests
- [x] 1.4 Author `fixtures/worlds/bellweather-maze/` with Kit Finch as a character-backed
  companion candidate and no engine-hardcoded world content

## 2. Case Lifecycle

- [x] 2.1 Add failing tests for the
  `cold_open → discovery → investigation → accusation → denouement` phase graph
- [x] 2.2 Implement and register `CaseState` as a SemStreams lifecycle participant with
  lifecycle-manager-exclusive phase writes
- [x] 2.3 Add recovery and duplicate-event tests proving one legal transition per
  structured case event
  - The production case-progress consumer maps identity-validated decisions and knowledge
    receipts to typed lifecycle events or an exact no-op barrier. Typed poison fails the turn,
    the auxiliary durable participates in queue-clean recovery checks, and sequential redelivery
    converges on the resident progress record and receipt.
  - Lifecycle receipt creation is a read followed by a merge, not a conditional graph write;
    this proves sequential redelivery convergence and makes no concurrent exactly-once or CAS
    claim.

## 3. Epistemic Projection Safety

- [x] 3.1 Add table-driven failing tests with unique entity IDs and unique text values for
  culprit, unrevealed-clue, and revealed-clue canaries across casekeeper, player,
  companion, public adjudicator, narrator, denouement, verifier, and operator purposes
- [x] 3.2 Implement the centralized closed-purpose `EpistemicProjector` with bounded reads
  and complete omission of unauthorized entities and identifiers
  - Companion-aware narration resolves only committed companion and knowledge artifacts, then
    renders them through the same centralized purpose-scoped projection boundary.
- [x] 3.3 Route adjudicator and narrator prompt assembly exclusively through the projector,
  pin public-adjudicator acting-actor knowledge and exclusions, and remove the
  audience-agnostic scene view as a direct persona input
- [x] 3.4 Assert canaries at projector, serialized prompt, mock-model request, and player
  egress boundaries before player-facing or full Bellweather E2E acceptance
  - Purpose-scoped projector output, serialized request bodies, mock-provider response bodies,
    and raw WebSocket delivery and retrieval frames carry unique positive and negative canaries.
    Authorized revealed evidence appears after commitment; culprit, hidden motive, solution
    predicates, and their unique text remain absent until the authorized denouement.

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

## 9. Narration and Player Protocol

- [x] 9.1 Extend narration to voice only committed case revelations and companion
  decisions from the player-authorized projection
  - Narrator projections alone carry the exact committed refs; prompt assembly resolves
    and identity-validates their resident `KnowledgeReceipt`, `Testimony`,
    `CompanionStageRecord`, and `CompanionDecision`, actor-filters revelations, requires
    projected evidence, and fails closed on missing or mismatched artifacts.
- [x] 9.2 Add denouement-purpose narration and prove canonical solution canaries remain
  unavailable before a correct accusation
  - Ordinary narration excludes solution canaries. Denouement narration accepts and
    renders them only through the existing exact correct-accusation router plus lifecycle
    and accusation-authorizer gates.
- [x] 9.3 Add `CompanionResolution` with closed kind and conditional hint validation; pin
  no active bond as absent and an active `silent` decision as a present silent summary
  - `CompanionResolution` reuses the closed decision kinds and requires `hint_level` if
    and only if kind is `hint`; no-active-bond and no-trigger stages omit it, while
    committed `silent` and exhausted decisions remain present.
- [x] 9.4 Pin the new player/v1 fields, closed sets, delivery identity, and retrieval
  round-trip in protocol tests
  - Player/v1 pins the exact `companion_resolution` field and nested field set. Production
    `TurnResult` decoding round-trips it, delivery and retrieval compose the same summary,
    and a completed turn missing its companion-stage reference fails closed. Architecture
    and backend review approved the result after the barrier fix.

## 10. Acceptance and Live Smoke

- [x] 10.1 Script the deterministic mock-model Bellweather path through discovery,
  investigation, testimony, sharing, all hint levels, wrong accusation, correct
  accusation, and denouement
  - The nine-turn fixture observes the body, investigates the fete green, questions Beatrice,
    shares the learned sedative evidence with Kit, advances all three bounded hints, and resolves
    wrong then correct accusations through denouement.
- [x] 10.2 Add one full-stack mock E2E asserting case progression, companion behavior,
  idempotency, and secret canaries with unique IDs and text values at every boundary
  - The full-stack proof checks authoritative case phases, actor-scoped knowledge, testimony,
    companion hint levels, deterministic accusations, fixed model-call budgets, sequential
    redelivery, queue settlement, one logical delivery, and stable committed artifacts.
  - Unique authorized and secret controls are checked at purpose-scoped projector output,
    serialized model requests, mock responses, and raw WebSocket delivery/retrieval bytes.
- [x] 10.3 Add an opt-in Taskfile Gemini smoke for one short investigation and Kit
  exchange, loading `.env` without printing the key and excluding it from CI
  - `task smoke:gemini:bellweather` requires both `SEMMACHINA_PAID_SMOKE=1` and a non-empty
    `GEMINI_API_KEY`; it is absent from the default task and GitHub Actions.
- [x] 10.4 Document active polling and fast-abort checks for the paid Gemini smoke
  - `docs/runbooks/bellweather-gemini-smoke.md` covers prerequisites, the fresh namespace,
    two bounded provider turn chains, authoritative polling, wedge and egress aborts, the
    absolute timeout, terminal evidence, teardown, cost, CI exclusion, and secret-safe diagnosis.
  - The separately authorized run is recorded in
    `docs/smoke-results/2026-08-02-bellweather-gemini.md` under task 11.4.

## 11. Quality Gates and Acceptance

- [x] 11.1 Run backend architecture review for lifecycle ownership, rule/component/persona
  boundaries, and absence of SemStreams substrate workarounds
  - Architecture review approved lifecycle-manager-exclusive phase writes, structural rule
    triggers, deterministic component ownership, purpose-scoped personas, and the existing
    stream-versus-KV boundaries without a SemStreams workaround.
  - The accepted deployment boundary remains one world per broker; multi-world subject and
    durable isolation is not claimed by this change.
- [x] 11.2 Run backend code and security review for secret authorization, idempotency,
  payload registry coverage, and error handling
  - Independent code and security reviews approved the unique canary coverage and fail-closed
    identity, artifact-reference, bond, and knowledge authorization checks.
  - Reviews accepted the documented sequential-redelivery and provider-crash idempotency
    caveats. Provider secrets and the paid smoke remain isolated from deterministic tests and CI.
- [x] 11.3 Run unit tests, race tests, mock E2E, lint, build, and strict OpenSpec validation
  - `go test ./...`, `go test -race ./...`, the deterministic Bellweather E2E, lint, build,
    strict OpenSpec validation at 9/9, and the final diff check all passed.
- [x] 11.4 Run the opt-in Gemini smoke only with operator authorization and record its
  result separately from deterministic acceptance
  - The authorized 2026-08-02 run passed both bounded provider turn chains in approximately
    59 seconds with authoritative discovery, terminal `complete`, a Kit `hint`, and no wedge.
    Secret-safe evidence is recorded separately in
    `docs/smoke-results/2026-08-02-bellweather-gemini.md`; provider billing detail was not captured.
  - A preceding pre-ingress boot attempt exposed the 32-byte content-bucket reference limit and
    made no provider call. The exact corrected configuration and contract test passed before retry.
- [x] 11.5 Archive this change only after every normative scenario and quality gate passes
  - By explicit scope decision, the three upstream-dependent hardening tasks moved to the
    focused `mystery-companion-hardening` follow-up. Every normative task retained by this
    acceptance change is complete, and the fiction-adjudication delta retains the current
    `Adjudicator reads current state` scenario for archive reconciliation.
