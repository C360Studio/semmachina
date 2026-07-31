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
Valid batches SHALL commit through the entity merge lane
(`graph.mutation.entity.update_with_triples`), never through `graph.mutation.triple.add`
or `.add_batch`, because those lanes **append**: committing a single-valued effect through
them accumulates values instead of replacing them, silently and without error. The merge
lane is **per-entity**, so a batch touching N target entities SHALL issue N merge calls —
one per target — and SHALL NOT send foreign subjects in a single request: graph-ingest
splits foreign subjects off and routes them through the appending lane, logging any failure
without returning it, which would reintroduce exactly the accumulation this requirement
forbids. Writes SHALL NOT touch buckets directly. Validation SHALL complete
for every intent before any write is issued, so a batch rejected on validation grounds
applies nothing at all; a rejected batch SHALL move the turn to phase `failed` with a
recorded reason.

#### Scenario: Committed effects are graph-visible
- **WHEN** a valid batch containing a `set_attribute` and a `move_entity` intent commits
- **THEN** both changes are queryable from `ENTITY_STATES` and each replaced prior values
  rather than appending competing ones

#### Scenario: Rejection is a normal path
- **WHEN** a batch is rejected
- **THEN** the world state is unchanged, the turn is in phase `failed`, and the rejection
  reason is retrievable from the turn entity

### Requirement: Partial commits are detected, never mistaken for success
The applier SHALL treat a batch as applied only when every per-entity merge succeeded. When
a merge returns a transient or unclassified `CommitError`, the turn SHALL remain in phase
`applying` and the stage SHALL return the error so its durable delivery is retried. The
error names the target whose response failed and the preceding targets whose responses
confirmed success. These fields are response bookkeeping, not a proof that the named target
did not mutate: its write may have landed before the response failed. Recovery is idempotent
re-application under the `turn_id` batch identity, never a partial-batch repair. A transient
partial commit is not a fictional rejection and SHALL NOT terminally fail the player's turn;
an invalid or fatal classified cause SHALL end it once with `effect-commit-incomplete`
rather than retry forever.

#### Scenario: A transiently failing merge leaves the applying stage retryable
- **WHEN** a batch's Nth per-entity merge returns a transient failure after N−1 confirmed
- **THEN** the stage returns a `CommitError` naming the unconfirmed target and confirmed
  response prefix, the turn remains in `applying`, and the durable trigger is not acknowledged

#### Scenario: A response failure after mutation still converges
- **WHEN** a target mutation lands but its response fails and does not enter `Committed`
- **THEN** the retry re-applies that target with replace semantics and converges with one
  intended value and one applied-batch marker

#### Scenario: Re-application after a partial commit converges
- **WHEN** a batch partially committed and the apply stage runs again for the same turn
- **THEN** re-application under the same `turn_id` batch identity converges the world to
  the full intended batch state, with already-committed subjects unchanged by replacement,
  one applied-batch marker, and no terminal failure recorded

### Requirement: Multi-valued predicates are written as complete sets
A write to a multi-valued predicate SHALL publish the predicate's full intended value set,
because the merge lane replaces a predicate's whole set and silently drops any sibling
omitted from the write. Adding one relationship therefore SHALL read the current set and
publish the result, never the new value alone. Emptying a multi-valued predicate SHALL use
an explicit predicate removal, because a write that simply omits the predicate leaves it
untouched.

#### Scenario: Adding a relationship keeps its siblings
- **WHEN** a character already carrying two items gains a third through an
  `add_relationship` effect
- **THEN** the character carries all three afterwards, not only the newly added one

#### Scenario: Removing the last value clears the predicate
- **WHEN** a `remove_relationship` effect removes a character's only remaining carried item
- **THEN** the predicate is empty afterwards, not left holding the removed value

### Requirement: Idempotent application
The applier SHALL derive its batch identity from `turn_id` and SHALL NOT re-apply a batch
already recorded on the turn entity, regardless of duplicate triggers or crash-restart.

#### Scenario: Duplicate apply trigger
- **WHEN** the apply stage is triggered twice for the same turn
- **THEN** the world state after the second trigger is identical to the state after the
  first, and only one applied-batch reference exists on the turn entity
