# turn-sequencing Specification

## Purpose
TBD - created by archiving change turn-loop-vertical-slice. Update Purpose after archive.
## Requirements
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
committed effects trigger narration; narration closes the turn. A **rejected** batch does
not narrate: it moves the turn to `failed`, which is terminal, and the player receives the
resolution summary carrying the failure code rather than prose. Effect rejection means a
persona proposed an intent the closed vocabulary refuses — an engine fault, not a fiction
outcome — and narrating it would be fabricating story about a bug. An action the *fiction*
denies is a different thing entirely: that is a normal verdict with failure-shaped intents
in its `auto` band, and it narrates like any other turn. Rule
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

#### Scenario: Persona task was stored but its PubAck was lost
- **WHEN** the agent stream stores a persona task but the stage caller observes an error,
  and redelivery in the same persisted resume generation publishes the same deterministic
  `TaskID` again within the configured agent-stream retention horizon
- **THEN** both publishes carry that `TaskID` as `Nats-Msg-Id`, JetStream stores and
  delivers one task, and the redelivery does not buy a second model call. This is a bounded
  local crash-window guarantee, not universal at-most-once billing: `MaxBytes` plus
  `DiscardOld` followed by a NATS restart, or an operator purge, can remove both the old
  task and the server's recoverable duplicate evidence; durable TaskID claims/fencing remain
  an upstream requirement tracked by [SemStreams issue #807](https://github.com/C360Studio/semstreams/issues/807)

#### Scenario: Recovery replaces acknowledged persona work that produced no artifact
- **WHEN** a persona task was acknowledged without producing its stage artifact and the boot
  pass persists resume attempt N before re-triggering that stage
- **THEN** both persona roles derive their task ID from the assembled turn: generation zero
  retains the legacy `role-turn` ID, generation N uses `role/turn/resume/N` with a delimiter
  forbidden in validated turn IDs, and retries within generation N still deduplicate while
  the intentional recovered execution is accepted as new work without colliding with another
  valid turn's generation-zero ID

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

#### Scenario: A turn parked mid-stage with no queued work
- **WHEN** a turn sits in a non-terminal phase carrying none of that phase's artifacts, and
  no message is queued for it on any durable queue that can deliver work for a turn —
  neither an unacknowledged stage trigger nor an unacknowledged persona task — and the
  process restarts
- **THEN** a boot-time pass over every turn in the world finds it — by reading which turns
  the stage stream still holds an unacknowledged trigger for, not by asking which rules
  would match — persists the next resume attempt, re-triggers the stage its phase names,
  and the turn resolves

#### Scenario: A turn whose first hop never arrived
- **WHEN** a turn is created and the hop that would start it is lost or never published, so
  the turn sits in `accepted` with no stage trigger queued for it
- **THEN** the boot pass re-triggers the FIRST stage and the turn proceeds, spending one
  bounded attempt — the turn is owed that stage's work either way, so starting it costs
  nothing that was not already owed, and ending a player's action over one lost message
  would be the worse trade

#### Scenario: A turn already waiting on queued work
- **WHEN** a message is queued and unacknowledged for a turn on any durable queue that can
  deliver work for it — a stage trigger no stage has acknowledged, or a persona task the
  agentic loop has not finished — and the process restarts
- **THEN** the boot pass counts that turn and publishes nothing, because the substrate owns
  that delivery — and a second trigger would re-enter the stage, which for a persona stage
  with no artifact yet is a second billed call

#### Scenario: A persona still running when the process restarts
- **WHEN** a stage published a persona task and acknowledged its own trigger, and the task
  is still unacknowledged on the agentic loop's stream when the process restarts
- **THEN** the boot pass leaves the turn alone, because that task will be redelivered — the
  work is in flight inside the restarting process rather than lost, and re-triggering the
  stage would buy a second billed persona call racing the first to write the same artifact

#### Scenario: A queued loop failure wins over an older stage trigger on restart
- **WHEN** a loop-failure event and an older trigger for its persona stage are both queued
  while the engine is down
- **THEN** boot binds the durable loop-failure watcher and waits for its pending and
  acknowledgement-pending counts to reach zero before binding stage consumers, so the
  failure terminally records the turn and the old trigger is declined without another model
  call

#### Scenario: A sequencing rule fired and its publish did not
- **WHEN** a rule's publish action fails — an open circuit breaker, a disconnected client, a
  refused acknowledgement — so the rule's own state records that it fired while no stage
  trigger exists, and the turn is left in a non-terminal phase carrying its stage's artifact
- **THEN** the boot pass counts the sighting without publishing anything, and ends the turn
  explicitly with a stranded reason and a stored explanation once the sighting budget is
  gone — re-running the stage it is parked in would re-enter a phase the turn is already in
  and skip on the artifact already recorded, and ending on a single reading could fail a
  turn whose work landed between the queue measurement and the turn read

### Requirement: Bounded execution
Every LLM-triggering rule path in the turn chain SHALL carry an iteration cap, and a persona
loop that ends without a terminal exit SHALL produce an explicit `failed` turn phase with a
recorded closed reason — never a silent stall — whichever way it ended. Boot-time recovery
of a stranded turn SHALL likewise be bounded: a turn re-triggered a capped number of times
without producing its stage's artifact SHALL be failed explicitly rather than re-triggered
forever.

#### Scenario: Persona exceeds its iteration cap
- **WHEN** a persona loop reaches its `MaxIterations` cap without a terminal exit
- **THEN** the turn enters phase `failed` with a cap-exhaustion reason and the failure is
  visible on the turn entity

#### Scenario: Persona loop fails for a reason other than its cap
- **WHEN** a persona loop ends without a terminal exit because of a model error, a provider
  refusal, or a handler fault
- **THEN** the turn enters phase `failed` with its own closed reason, distinct from cap
  exhaustion, and the loop's own reason code rides behind the failure-detail reference
  rather than on the rule-matching surface

#### Scenario: A stranded turn exhausts its re-trigger budget
- **WHEN** the boot pass has re-triggered one turn's stage the permitted number of times and
  the stage has still produced no artifact
- **THEN** the turn enters phase `failed` with a stranded reason and a stored explanation,
  rather than being re-triggered on every subsequent boot
