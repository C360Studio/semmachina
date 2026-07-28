# dice-resolution — Delta Spec

## ADDED Requirements

### Requirement: Versioned deterministic mechanic
The dice component SHALL implement mechanic `2d6-pbta/v1`: two six-sided dice plus the sum
of the verdict's typed modifiers, banded as ≤6 `miss`, 7–9 `partial`, 10+ `full`. The
roll-result triple SHALL record mechanic version, RNG version, dice values, modifier list,
total, and selected band.

#### Scenario: Band thresholds
- **WHEN** dice plus modifiers total 7
- **THEN** the recorded band is `partial`; totals of 6 and 10 yield `miss` and `full`
  respectively

### Requirement: Seed derivation from turn identity
The per-roll seed SHALL be derived deterministically as a hash of the campaign seed
(recorded on the campaign entity at world instantiation) and the `turn_id`. The component
SHALL use no other entropy source: no wall-clock, no global RNG state, no unseeded
randomness.

#### Scenario: Same turn identity reproduces the roll
- **WHEN** the roll for a given campaign seed and `turn_id` is re-executed
- **THEN** the dice values, total, and band are byte-identical to the original roll-result
  triple

#### Scenario: Different turns roll independently
- **WHEN** two turns in the same campaign roll with identical verdicts and modifiers
- **THEN** their seeds differ (derived from distinct `turn_id`s) and their dice values are
  independent

### Requirement: Rolls occur only when required
The dice component SHALL execute only for verdicts whose class requires a roll, and SHALL
produce at most one roll-result triple per turn.

#### Scenario: One roll per turn
- **WHEN** a turn's roll trigger is delivered more than once
- **THEN** exactly one roll-result triple exists for the turn
