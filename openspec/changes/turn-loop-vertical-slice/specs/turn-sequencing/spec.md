# turn-sequencing — Delta Spec

## ADDED Requirements

### Requirement: Turn entity with durable phase
Each accepted `PlayerAction` SHALL create exactly one turn entity in `ENTITY_STATES`,
identified 1:1 with `action_id`, carrying a single-valued `turn.phase.current` predicate
that moves through `accepted → adjudicating → resolving → applying → narrating → complete`
(or `failed`). Phase writes SHALL replace, never append. Verdict, roll, effect-batch, and
narration references SHALL land as triples on the turn entity as they are produced. Every
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

#### Scenario: Duplicate stage trigger
- **WHEN** a stage trigger fires twice for the same turn (redelivery or rule re-match)
- **THEN** the stage executes at most once and the turn's artifacts are identical to a
  single-delivery run

### Requirement: Crash recovery resumes from recorded phase
After a process crash at any point in a turn, restart SHALL resume the turn from its
recorded phase: completed stages SHALL NOT re-run, the in-flight stage SHALL run to
completion (or the turn SHALL fail explicitly), and the final world state, roll count, and
ledger content SHALL be identical to an uninterrupted run.

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
