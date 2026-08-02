## MODIFIED Requirements

### Requirement: Turn entity with durable phase
Each accepted `PlayerAction` SHALL create exactly one turn entity in `ENTITY_STATES`,
identified 1:1 with `action_id`, carrying a single-valued `turn.phase.current` predicate
that moves through
`accepted → interpreting → adjudicating → resolving → applying → companion → narrating → complete`
(or `failed`). Phase writes SHALL replace, never append, which requires the entity merge
lane (`graph.mutation.entity.update_with_triples`) — the triple-add lanes append, so a
duplicate stage trigger through them leaves a turn holding two phases with no error, and
the phase predicate stops being an idempotency guard. Case decision, verdict, roll,
effect-batch, companion resolution, and narration references SHALL land as triples on the
turn entity as they are produced. The action payload SHALL be durably stored and
referenced from the turn's birth record, written before the action is acknowledged, so a
turn that survives a crash can still be interpreted and adjudicated: the action's free
text is fiction and exceeds the triple object budget, so it reaches the graph only as a
reference. Every predicate the engine writes SHALL be a canonical three-segment
lower-kebab identity (`domain.category.property`) — the graph write gate rejects any other
shape.

#### Scenario: Engine predicates satisfy the canonical contract
- **WHEN** any turn-loop predicate is written to `ENTITY_STATES`
- **THEN** it has exactly three lower-kebab segments and is accepted by the graph write
  gate, and no two-segment or underscore-bearing predicate appears anywhere in the engine

#### Scenario: One action, one turn
- **WHEN** a `PlayerAction` is consumed and accepted
- **THEN** exactly one turn entity exists for its `action_id`, in phase `accepted`, before
  the action message is acknowledged

#### Scenario: Phase is single-valued
- **WHEN** a turn transitions from `applying` to `companion`
- **THEN** a query of the turn entity returns exactly one phase value, `companion`

### Requirement: Rule-chain sequencing with conditional roll
Turn progression SHALL be driven by rules matching structured triples, never prose. An
accepted action triggers case interpretation; its structured result or deterministic
no-op triggers ordinary adjudication. A verdict whose class requires a roll triggers the
dice component; a roll result, or a no-roll verdict, triggers effect application.
Committed effects and knowledge grants trigger bounded companion work or its deterministic
no-op; the committed companion resolution then triggers narration, which closes the turn.

A **rejected** batch does not narrate: it moves the turn to `failed`, which is terminal,
and the player receives the resolution summary carrying the failure code rather than
prose. Effect rejection means a persona proposed an intent the closed vocabulary refuses
— an engine fault, not a fiction outcome — and narrating it would be fabricating story
about a bug. An action the *fiction* denies is different: it is a normal verdict with
failure-shaped intents in its `auto` band and narrates like any other turn. Rule payloads
SHALL carry references only, never content.

#### Scenario: Roll-requiring verdict routes through dice
- **WHEN** a verdict lands with a class that requires a roll
- **THEN** the dice component is triggered, and effect application is triggered only
  after the roll result lands

#### Scenario: No-roll verdict skips dice
- **WHEN** a verdict lands with a class that does not require a roll
- **THEN** effect application is triggered directly with the verdict's `auto` band and no
  roll-result triple is ever created for the turn

#### Scenario: Non-mystery interpretation is a deterministic no-op
- **WHEN** a turn does not belong to an active mystery case
- **THEN** the interpreting stage commits one no-op artifact without a casekeeper model
  call and advances normally

#### Scenario: Turn without a companion is a deterministic no-op
- **WHEN** the player has no active companion bond or no structured companion trigger
- **THEN** the companion stage commits one silent no-op artifact without a companion model
  call and advances to narration

## ADDED Requirements

### Requirement: New stages preserve recovery and idempotency
The `interpreting` and `companion` stages SHALL use the same deterministic task identity,
phase guards, durable artifacts, restart recovery, and duplicate-delivery protections as
the existing stages. Repeated inputs SHALL produce one logical case decision and one
logical companion resolution per turn.

#### Scenario: Restart resumes case interpretation
- **WHEN** the process restarts with a turn parked in `interpreting` and no completed case
  decision or queued work
- **THEN** recovery re-triggers that stage within its bounded attempt budget and does not
  skip directly to adjudication

#### Scenario: Duplicate companion delivery converges
- **WHEN** the same companion trigger or decision is delivered more than once
- **THEN** the turn carries one companion resolution and narration runs once
