# Design — Mystery Companion Acceptance

## Context

The existing turn loop assembles one audience-agnostic scene view and renders it for both
adjudicator and narrator. It retains every registered predicate, follows visible
relationships, and renders excluded entity IDs. That is sufficient for the starter
fixture but unsafe for a mystery: canonical solution facts, private motives, false
beliefs, and unrevealed clues can enter model context and player-facing prose.

The world package also has no first-class case, clue, knowledge, or companion contracts.
This design uses an original cozy-village case, *Death in the Bellweather Maze*, as an
acceptance fixture for the minimum reusable epistemic and companion primitives. It
preserves the project boundary: personas judge fiction, rules match structure, components
execute work, and lifecycle owns phase.

## Goals / Non-Goals

**Goals:**

- Complete one authored mystery without supplying unrevealed truth to an unauthorized
  persona, model request, or player result.
- Interpret natural-language investigation into closed decisions, then resolve disclosure
  and accusations mechanically.
- Make Kit Finch the first instance of a reusable companion capability with durable
  identity, scoped knowledge, bounded initiative, and deterministic hints.
- Preserve crash recovery, idempotency, bounded calls, and token-free CI acceptance.

**Non-Goals:**

- Full belief evolution, probabilistic knowledge, multiplayer sharing, or proof that an
  LLM cannot infer a secret from authorized public clues.
- Full autonomous NPC life, multiple companions, affinity mechanics, UI, map/place work,
  world clock, chronicler, or continuity checking.
- A live-model CI gate. Gemini remains an operator-invoked smoke test.

## Decisions

### D1 — The case is immutable authored graph state with a dedicated lifecycle

The Bellweather package declares one case entity, six suspect characters, exactly twelve
clue/red-herring entities, culprit/method/motive references, and an ordered timeline.
`CaseState` implements SemStreams `lifecycle.Participant` with the declared graph:
`cold_open → discovery → investigation → accusation → denouement`. The lifecycle manager
exclusively owns `case.lifecycle.phase`; rules request transitions after structured facts
land. A wrong accusation remains in `accusation`; a correct accusation enters terminal
`denouement`.

Canonical solution and truth-status predicates are immutable after import and unavailable
to effects, world rules, and operator writes. They remain readable through authorized
operator surfaces and purpose-scoped engine reads.

Alternatives rejected: encoding the solution in persona prose leaks it; using the turn
phase for case progression confuses one request with a multi-turn workflow; letting a
component advance phase violates lifecycle ownership.

### D2 — One centralized epistemic selector owns every persona projection

An `EpistemicProjector` accepts an authenticated audience and a closed purpose, performs a
bounded graph read, and returns only the allowed state. Every persona prompt is assembled
from this projection; the current audience-agnostic scene view is no longer a persona
input.

| Audience/purpose | Allowed state |
|---|---|
| casekeeper | Case truth, eligible hidden evidence, targeted beliefs, scene and action |
| public adjudicator | Public scene facts and the acting actor's granted knowledge |
| player narration | Public facts, player-granted knowledge, committed revelation |
| companion | Public facts and that companion actor's granted knowledge |
| accusation verifier | Canonical solution IDs only |
| denouement narrator | Canonical solution after a correct verified accusation only |
| operator | Full graph through operator read surfaces |

The public-adjudicator projection excludes canonical private truth, targeted private
beliefs, and every other actor's knowledge. The selector omits unauthorized entities
entirely, including excluded or stub IDs, so an identifier cannot become a semantic leak.
Prompt, model-request, and egress tests use secret canaries whose entity IDs and text
values are both unique, plus a revealed-clue anti-vacuity control.

Alternatives rejected: predicate filtering scattered across persona builders is difficult
to audit; prompt instructions asking a model to ignore secrets still disclose them;
separate copied graphs create synchronization and authority problems.

### D3 — Truth, belief, knowledge, and revelation are distinct records

Case truth, a named character's belief, an actor's granted knowledge, and a turn's
committed narration revelation use separate predicates. `KnowledgeGranter` validates and
commits actor-specific grants. A player discovery does not automatically become companion
knowledge: both may receive a grant when both witnessed it, otherwise the player must
share it explicitly.

Questioning may grant attributed testimony from the named target without revealing the
belief's truth status or changing canonical truth. Bulky testimony and narration remain in
ObjectStore; graph state and rules carry references.

Alternatives rejected: one `revealed` flag cannot represent different audiences; copying
belief into truth creates false authority; placing prose in the graph makes rules and
retrieval depend on unbounded content.

### D4 — The casekeeper interprets fiction; components authorize effects

The casekeeper receives private purpose-scoped context and emits `CaseDecision` with
`decision_id`, `turn_id`, `action_id`, `case_id`, `actor_id`, a closed kind (`observe`,
`investigate`, `question`, `share`, `request_hint`, `accuse`, `other`), at most eight
unique target references, at most twelve unique reveal references, and optional structured
culprit/method/motive accusation IDs. The three accusation IDs are required together for
`accuse` and forbidden for every other kind. `decision_id` is the lowercase hexadecimal SHA-256
of the length-prefixed tuple (`case-decision/v1`, `turn_id`, `action_id`, `case_id`,
`actor_id`). Validation rejects duplicate references and any value above either ceiling.
Rule-visible fields contain no prose.

A reveal reference is a proposal, not authority. `KnowledgeGranter` rejects solution or
denouement-only material before denouement, evidence outside the eligible private
projection, grants to the wrong actor, and belief testimony whose believer was not the
questioned target.

Alternatives rejected: rules parsing player text violates the fiction boundary; allowing
the casekeeper to write knowledge directly merges narrative judgment with authorization.

### D5 — Accusations are verified by exact authored IDs

`AccusationVerifier` compares culprit, method, and motive IDs against the canonical
solution without an LLM. It emits `AccusationResult` as a closed, polymorphic,
message-decoded payload containing deterministic `result_id`, `turn_id`, `case_id`,
`decision_id`, and `outcome` (`correct` or `incorrect`), with no dimension-level result or
rule-visible prose. The result ID is the lowercase hexadecimal SHA-256 of the
length-prefixed tuple (`accusation-result/v1`, `turn_id`, `case_id`, `decision_id`). The
payload follows the complete registry contract in D9. A wrong result reveals only that
verification failed; a correct result authorizes denouement context and requests the
lifecycle transition.

Alternatives rejected: asking a model to judge correctness makes the authored solution
advisory and can reveal partial answers; fuzzy string comparison makes aliases and prose
part of game authority.

### D6 — Companion state is reusable and decisions are structural

A durable player-companion bond identifies player, character-backed companion, policy,
and hint level. The companion persona reads only its actor-scoped projection and emits
`CompanionDecision`: `decision_id`, `turn_id`, generic `context_ref`, `player_id`,
`companion_id`, a closed kind (`silent`, `quip`, `question`, `warning`, `recall`, `hint`),
optional closed hint level (`nudge`, `connect`, `next-step`), at most eight unique evidence
references, and an optional structural target. In Bellweather, `context_ref` identifies
the case; another world may bind it to a scene, encounter, quest, or other activity. The
payload contains no dialogue or rationale.

Validation requires a hint level exactly for `hint`, evidence for `warning`, `recall`, and
`hint`, well-formed references without duplicates, and no more than eight evidence
references. `decision_id` is the lowercase hexadecimal SHA-256 of the length-prefixed tuple
(`companion-decision/v1`, `turn_id`, `context_ref`, `player_id`, `companion_id`), binding
the decision to the triggering turn and companion. Runtime authorization separately proves
every evidence reference belongs to that companion's projection. The narrator gives Kit
his voice only after the decision commits.

Alternatives rejected: prompt-only sidekick flavor has no durable identity or knowledge
boundary; generated dialogue inside the decision makes prose rule-visible and bypasses
the narrator's voice ownership.

### D7 — Hint progression and initiative are deterministic and bounded

`HintLadder` chooses and persists `nudge → connect → next-step` on the companion bond.
Further requests stay at the bounded final level. A newly committed knowledge grant for
that companion is the only reset condition and makes the next hint a `nudge`; duplicate or
rejected grants do not reset it. Every cited evidence reference must already be
companion-known.

Rules trigger automatic companion work only from closed decision or resolution facts, at
most once per triggering turn, with an explicit nonzero `max_iterations` that is the hard
initiative ceiling and a silent exhaust path. Bond policy and components may tighten
admission beneath that ceiling but can never raise, replace, or bypass it. Components
select the required hint level; the persona decides only the structural companion
response.

Alternatives rejected: model-selected hint escalation is nondeterministic and can jump to
a spoiler; uncapped ambient interventions turn one player action into an unbounded call
cascade.

### D8 — Interpretation and companion work are durable turn stages

The turn phase graph gains `interpreting` before ordinary adjudication and `companion`
after effects and knowledge grants but before narration. Non-mystery turns produce a
deterministic interpretation no-op; turns with no active companion produce a deterministic
companion no-op. Neither no-op calls a model. Existing stage identity, restart recovery,
and duplicate-delivery guards apply to both stages.

Alternatives rejected: conditional calls hidden inside another component cannot be
recovered or diagnosed independently; parallel companion and narrator calls can voice an
uncommitted or duplicated intervention.

### D9 — Payload and public protocol contracts stay explicit

Every new polymorphic payload implements `Schema()`, strict `Validate()`, alias-only JSON
marshal/unmarshal, explicit registry registration at every production and test bootstrap,
and a fully populated round trip through `message.NewDecoder(registry)`. Closed-vocabulary
tests pin all rule-matched values.

The player/v1 terminal result adds `CompanionResolution` containing `companion_id`, the
decision kind, and optional hint level. A turn with no active bond omits the summary; an
active companion that decides `silent` produces a present summary whose kind is `silent`.
Companion voice remains in ordinary narration prose. Protocol surface tests pin the added
fields, optionality, and closed sets.

### D10 — Acceptance uses mock inference; Gemini is an opt-in smoke

One deterministic mock-model E2E traverses discovery, investigation, knowledge sharing,
the hint ladder, wrong accusation, correct accusation, and denouement. It checks secret
canaries with unique IDs and unique text values in projector output, serialized prompt
bytes, model requests, and egress. The Bellweather script includes idempotent redelivery
assertions.

Gemini Flash may run one short investigation and Kit exchange through a Taskfile target
that loads `.env`. It is never required by CI and must be actively polled when invoked
because it is a paid operation.

## Risks / Trade-offs

- [A public clue may let a capable model infer the culprit] → the guarantee is structural
  non-disclosure, not prevention of inference; author and evaluate clue fairness separately.
- [The casekeeper sees private truth and could propose an illegal reveal] → treat every
  reveal as untrusted and authorize it in `KnowledgeGranter`.
- [Audience filtering can become a second authorization system] → centralize the matrix,
  use closed purposes, and test all prompt/model/egress boundaries with canaries.
- [Two added stages increase latency and paid calls] → deterministic no-op artifacts skip
  irrelevant calls; mock inference remains the CI gate.
- [Package vocabulary may expose a SemStreams substrate gap] → file the gap upstream and
  block the affected task instead of adding a local substrate workaround.

## Migration Plan

1. Add failing package and vocabulary tests, then import Bellweather without connecting it
   to any persona.
2. Register the case lifecycle and centralized selector; land canary tests before enabling
   the casekeeper or companion.
3. Add payloads, decisions, deterministic components, and rule paths behind package-driven
   applicability checks.
4. Add durable stages and narrator/player protocol integration, preserving no-op behavior
   for the existing starter fixture.
5. Run mock E2E, race tests, lint, build, and strict OpenSpec validation. Rollback removes
   the Bellweather package and new stage wiring; existing worlds remain readable because
   the additions are package-driven and player/v1 fields are optional when absent.

## Open Questions

No blocking design questions remain. Exact predicate names and package record shapes are
implementation details to settle under the closed contracts above; any required substrate
change must be raised upstream before proceeding.
