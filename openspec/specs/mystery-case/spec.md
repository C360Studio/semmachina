# mystery-case Specification

## Purpose
Define authored mystery structure, closed investigation decisions, lifecycle progression, and
deterministic non-revealing accusation verification.
## Requirements
### Requirement: Authored mystery case is complete and protected
Each mystery package SHALL declare one case with a canonical culprit, method, motive,
ordered timeline, suspects, and evidence. The Bellweather acceptance package SHALL contain
exactly six suspects and twelve clues or red herrings. Package validation and import SHALL
classify canonical solution and truth-status predicates as immutable, and package rules and
effect intents SHALL NOT branch on or mutate them.

#### Scenario: Bellweather package is structurally complete
- **WHEN** the Bellweather package is validated
- **THEN** it contains one case, six suspects, twelve evidence entities including multiple
  red herrings, and complete culprit, method, motive, and ordered timeline references

#### Scenario: Package-local truth mutation is rejected
- **WHEN** package validation, import, a world rule, or an effect intent encounters a protected
  canonical solution or truth-status mutation
- **THEN** the domain gate rejects it before the authored value can change

### Requirement: Natural-language investigation exits through CaseDecision
The casekeeper SHALL interpret a player's natural-language case action into one
`CaseDecision` carrying `decision_id`, `turn_id`, `action_id`, `case_id`, `actor_id`, no
more than eight `target_refs`, no more than twelve `reveal_refs`, and a kind from the
closed set `observe`, `investigate`, `question`, `share`, `request_hint`, `accuse`, or
`other`. A decision whose kind is `accuse` SHALL carry all three structural
`culprit_ref`, `method_ref`, and `motive_ref` fields; every other kind SHALL carry none of
them. Both reference lists SHALL reject duplicates. Rule-visible fields SHALL contain no
prose.

The decision ID SHALL be the lowercase hexadecimal SHA-256 of the length-prefixed tuple
(`case-decision/v1`, `turn_id`, `action_id`, `case_id`, `actor_id`) and validation SHALL
reject any mismatched supplied ID.

#### Scenario: Observation becomes a closed decision
- **WHEN** the player describes examining the body in natural language
- **THEN** the casekeeper emits an `observe` or `investigate` decision whose IDs bind it to
  that action, turn, case, and actor

#### Scenario: Case decision reference bounds are enforced
- **WHEN** a decision supplies more than eight targets, more than twelve reveal proposals,
  a duplicate within either list, or a decision ID that differs from the deterministic ID
- **THEN** payload validation rejects the decision before it reaches a rule

#### Scenario: Accusation fields match the decision kind
- **WHEN** an `accuse` decision omits any solution reference or a non-accusation decision
  supplies one
- **THEN** payload validation rejects the decision before it reaches the verifier or a rule

#### Scenario: Rules never parse investigation prose
- **WHEN** a case decision lands
- **THEN** every rule branches only on its closed kind and structural references, and no
  rule or non-persona component reads the original action text

### Requirement: Case lifecycle owns visible progression
`CaseState` SHALL participate in the lifecycle
`cold_open → discovery → investigation → accusation → denouement`. The lifecycle manager
SHALL exclusively write `case.lifecycle.phase`; rules SHALL only request legal transitions
after structured case facts land. A wrong accusation SHALL remain in `accusation`, and a
correct accusation SHALL enter terminal `denouement`.

#### Scenario: First observation discovers the case
- **WHEN** the first structured observation reveals the body while the case is in
  `cold_open`
- **THEN** a lifecycle request advances the case exactly once to `discovery`

#### Scenario: Investigation follows structured work
- **WHEN** the first valid structured investigation lands while the case is in `discovery`
- **THEN** the lifecycle advances exactly once to `investigation`

#### Scenario: Duplicate transition input is idempotent
- **WHEN** the same case decision or lifecycle event is delivered more than once
- **THEN** the case records one legal transition and no phase is skipped or duplicated

### Requirement: Accusation verification is deterministic and non-revealing
The accusation verifier SHALL compare the submitted culprit, method, and motive IDs
exactly with the immutable authored solution without an LLM. It SHALL emit a closed
`AccusationResult` carrying `result_id`, `turn_id`, `case_id`, `decision_id`, and an
`outcome` from `correct` or `incorrect`, with no dimension-level result or rule-visible
prose. The result ID SHALL be the lowercase hexadecimal SHA-256 of the length-prefixed
tuple (`accusation-result/v1`, `turn_id`, `case_id`, `decision_id`).

`AccusationResult` SHALL be a polymorphic, message-decoded payload implementing `Schema()`,
strict `Validate()`, alias-only `MarshalJSON` and `UnmarshalJSON`, explicit
`RegisterPayloads` registration at every production and test bootstrap, a fully populated
round trip through `message.NewDecoder(registry)`, and closed-vocabulary tests. An
incorrect result SHALL state only that verification failed. A correct result SHALL
authorize denouement context and request the terminal lifecycle transition.

#### Scenario: Wrong accusation reveals no partial answer
- **WHEN** one or more submitted solution IDs differ from the authored solution
- **THEN** verification returns false without identifying which dimension failed, and the
  case remains in `accusation`

#### Scenario: Correct accusation unlocks denouement
- **WHEN** all three submitted solution IDs exactly match the authored solution
- **THEN** verification returns true, the case advances to `denouement`, and only then may
  denouement narration receive canonical solution facts
