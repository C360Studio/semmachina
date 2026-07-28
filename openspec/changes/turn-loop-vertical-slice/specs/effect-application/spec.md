# effect-application — Delta Spec

## ADDED Requirements

### Requirement: Deterministic validation against the closed effect vocabulary
The effect applier SHALL validate every intent in the selected outcome band against the
registered effect vocabulary (v1 types: `set_attribute`, `move_entity`,
`add_relationship`, `remove_relationship`, `set_status`), per-type bounds, and
target-entity existence. Validation SHALL be deterministic — no LLM involvement.

#### Scenario: Out-of-vocabulary intent is rejected
- **WHEN** the selected band contains an intent whose type is not in the registered
  vocabulary
- **THEN** the batch is rejected and no intent from the batch is applied

#### Scenario: Out-of-bounds value is rejected
- **WHEN** a `set_attribute` intent exceeds the attribute's registered bounds
- **THEN** the batch is rejected with a recorded reason identifying the offending intent

### Requirement: Whole-batch commit through the mutation API
Valid batches SHALL commit through the `graph.mutation.*` API (never direct bucket
writes), with single-valued predicates replacing prior values. A batch SHALL apply
entirely or not at all; a rejected batch SHALL move the turn to phase `failed` with a
recorded reason.

#### Scenario: Committed effects are graph-visible
- **WHEN** a valid batch containing a `set_attribute` and a `move_entity` intent commits
- **THEN** both changes are queryable from `ENTITY_STATES` and each replaced prior values
  rather than appending competing ones

#### Scenario: Rejection is a normal path
- **WHEN** a batch is rejected
- **THEN** the world state is unchanged, the turn is in phase `failed`, and the rejection
  reason is retrievable from the turn entity

### Requirement: Idempotent application
The applier SHALL derive its batch identity from `turn_id` and SHALL NOT re-apply a batch
already recorded on the turn entity, regardless of duplicate triggers or crash-restart.

#### Scenario: Duplicate apply trigger
- **WHEN** the apply stage is triggered twice for the same turn
- **THEN** the world state after the second trigger is identical to the state after the
  first, and only one applied-batch reference exists on the turn entity
