# turn-sequencing — Delta Spec

## ADDED Requirements

### Requirement: Turn entity with durable phase
Each accepted `PlayerAction` SHALL create exactly one turn entity in `ENTITY_STATES`,
identified 1:1 with `action_id`, carrying a single-valued `turn.phase.current` predicate
that moves through `accepted → adjudicating → resolving → applying → narrating → complete`
(or `failed`). Phase writes SHALL replace, never append, which requires the entity merge
lane (`graph.mutation.entity.update_with_triples`) — the triple-add lanes append, so a
duplicate stage trigger through them leaves a turn holding two phases with no error, and
the phase predicate stops being an idempotency guard. Verdict, roll, effect-batch, and
narration references SHALL land as triples on the turn entity as they are produced. The
action payload SHALL be durably stored and referenced from the turn's birth record, written
before the action is acknowledged, so a turn that survives a crash can still be adjudicated:
the action's free text is fiction and exceeds the triple object budget, so it reaches the
graph only as a reference. Every
predicate the engine writes SHALL be a canonical three-segment lower-kebab identity
(`domain.category.property`) — the graph write gate rejects any other shape.

#### Scenario: Engine predicates satisfy the canonical contract
- **WHEN** any turn-loop predicate is written to `ENTITY_STATES`
- **THEN** it has exactly three lower-kebab segments and is accepted by the graph write
  gate, and no two-segment or underscore-bearing predicate appears anywhere in the engine

#### Scenario: One action, one turn
- **WHEN** a `PlayerAction` is consumed and accepted
- **THEN** exactly one turn entity exists for its `action_id`, in phase `accepted`,
  before the action message is acknowledged

#### Scenario: Phase is single-valued
- **WHEN** a turn transitions from `resolving` to `applying`
- **THEN** a query of the turn entity returns exactly one phase value, `applying`

### Requirement: Rule-chain sequencing with conditional roll
Turn progression SHALL be driven by rules matching structured triples (never prose): an
accepted action triggers adjudication; a verdict whose class requires a roll triggers the
dice component; a roll result — or a no-roll verdict — triggers effect application;
committed (or rejected) effects trigger narration; narration closes the turn. Rule
payloads SHALL carry references only (entity IDs, `turn_id`, storage refs), never content.

#### Scenario: Roll-requiring verdict routes through dice
- **WHEN** a verdict lands with a class that requires a roll
- **THEN** the dice component is triggered, and effect application is triggered only
  after the roll result lands

#### Scenario: No-roll verdict skips dice
- **WHEN** a verdict lands with a class that does not require a roll
- **THEN** effect application is triggered directly with the verdict's `auto` band and no
  roll-result triple is ever created for the turn

### Requirement: Idempotency under duplicate delivery
Duplicate delivery of any message in the turn pipeline SHALL NOT create a second turn,
re-run a completed stage, re-apply effects, or duplicate a ledger entry. Stage triggers
SHALL be guarded by the turn phase such that a duplicate trigger observing an
already-advanced phase is a no-op.

#### Scenario: Duplicate action delivery
- **WHEN** the same `PlayerAction` message is delivered twice
- **THEN** exactly one turn entity exists for its `action_id` and the second delivery is
  acknowledged without side effects

#### Scenario: Duplicate stage trigger past the stage
- **WHEN** a stage trigger fires for a turn whose phase has already moved past that stage
- **THEN** the trigger is a no-op: no write occurs and no artifact changes

#### Scenario: Duplicate stage trigger inside the stage
- **WHEN** a stage trigger fires for a turn still in that stage's own phase, because the
  previous attempt was interrupted
- **THEN** the stage resumes and the turn ends carrying exactly one of each artifact, with
  effects applied exactly once — this comes from idempotency (seeded dice, `turn_id`-derived
  batch identity, replace-writes), not from suppressing the second execution. A
  deterministic stage re-executes byte-identically; a **persona** stage re-executes and its
  interrupted attempt is replaced by a single completed one, at the cost of one re-billed
  call. Replace-writes converge on *one* value, not on the *same* value, so a resumed
  persona stage does not promise the output the interrupted attempt would have produced

#### Scenario: A skipped stage is an error, not a decline
- **WHEN** a trigger would advance a turn past a stage that never ran
- **THEN** the transition is refused as illegal rather than silently declined, because a
  stale trigger and a wiring bug are otherwise indistinguishable — while the turn is still
  running; a terminal turn declines every trigger, including a skipping one

### Requirement: Crash recovery resumes from recorded phase
After a process crash at any point in a turn, restart SHALL resume the turn from its
recorded phase: completed stages SHALL NOT re-run, the in-flight stage SHALL run to
completion (or the turn SHALL fail explicitly), and the turn SHALL resolve exactly once —
one roll at most, effects applied exactly once, one ledger manifest. Where the interrupted
stage was deterministic, the result SHALL be identical to an uninterrupted run; where it was
a persona, the result SHALL be a single coherent outcome rather than the outcome the
interrupted attempt would have produced, since a re-executed persona supplies different
modifiers and can therefore shift the total and the band even on identical dice.

#### Scenario: Crash between roll and effect application
- **WHEN** the process crashes after the roll-result triple lands but before effects are
  applied, and then restarts
- **THEN** the turn resumes at effect application using the recorded roll, no second roll
  occurs, and effects are applied exactly once

### Requirement: Bounded execution
Every LLM-triggering rule path in the turn chain SHALL carry an iteration cap, and cap
exhaustion SHALL produce an explicit `failed` turn phase with a recorded reason — never a
silent stall.

#### Scenario: Persona exceeds its iteration cap
- **WHEN** a persona loop reaches its `MaxIterations` cap without a terminal exit
- **THEN** the turn enters phase `failed` with a cap-exhaustion reason and the failure is
  visible on the turn entity
