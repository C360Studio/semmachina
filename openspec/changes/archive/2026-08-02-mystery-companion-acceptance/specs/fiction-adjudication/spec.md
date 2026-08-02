## MODIFIED Requirements

### Requirement: Single-exit verdict with closed vocabulary
The adjudicator persona SHALL read the player's action text, public current-scene facts,
and the acting actor's granted knowledge from the centralized public-adjudicator projection
assembled at execution time. It SHALL exit exactly once per turn through a terminal tool
emitting a structured verdict: plausibility class, risk class, `requires_roll`, and typed
modifiers. All rule-matched fields SHALL take values only from the registered closed
vocabulary; free-text fields in the verdict SHALL be rule-opaque. Canonical private truth,
targeted private beliefs, unrevealed evidence, and every other actor's knowledge SHALL NOT
appear in its projection or model request.

#### Scenario: Verdict uses only registered classes
- **WHEN** the adjudicator exits with a verdict
- **THEN** its plausibility, risk, and consequence-class values each belong to the
  registered vocabulary, and the exit is rejected at the tool boundary if any do not

#### Scenario: Adjudicator reads current state
- **WHEN** authorized world state changed after the action was submitted but before
  adjudication runs
- **THEN** the adjudicator's context reflects that state at execution time, not a snapshot
  from submission time

#### Scenario: Adjudicator receives no mystery truth
- **WHEN** an active case holds culprit and unrevealed-clue canaries with unique entity IDs
  and unique text values
- **THEN** neither canary appears in the adjudicator's projection, serialized prompt, or
  model request
