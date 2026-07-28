# fiction-adjudication — Delta Spec

## ADDED Requirements

### Requirement: Single-exit verdict with closed vocabulary
The adjudicator persona SHALL read the player's action text and current scene facts
(assembled at execution time) and SHALL exit exactly once per turn through a terminal tool
emitting a structured verdict: plausibility class, risk class, `requires_roll`, and typed
modifiers. All rule-matched fields SHALL take values only from the registered closed
vocabulary; free-text fields in the verdict SHALL be rule-opaque.

#### Scenario: Verdict uses only registered classes
- **WHEN** the adjudicator exits with a verdict
- **THEN** its plausibility, risk, and consequence-class values each belong to the
  registered vocabulary, and the exit is rejected at the tool boundary if any do not

#### Scenario: Adjudicator reads current state
- **WHEN** the world state changed after the action was submitted but before adjudication
  runs
- **THEN** the adjudicator's context reflects the state at execution time, not a snapshot
  from submission time

### Requirement: Effect intents declared per outcome band
The verdict SHALL include proposed effect intents grouped by outcome band: `miss`,
`partial`, and `full` for roll-requiring verdicts, or a single `auto` band when
`requires_roll` is false. After the verdict lands, no further creative judgment SHALL
occur before effect application — band selection is fully determined by the roll result.

#### Scenario: Roll-requiring verdict carries three bands
- **WHEN** the adjudicator emits a verdict with `requires_roll` true
- **THEN** the verdict contains effect-intent lists for `miss`, `partial`, and `full`
  bands (any of which may be empty), each drawn from the closed effect vocabulary

#### Scenario: No-roll verdict carries auto band
- **WHEN** the adjudicator emits a verdict with `requires_roll` false
- **THEN** the verdict contains exactly one `auto` effect-intent band

### Requirement: Fiction boundary
Only the adjudicator (and other personas) SHALL read the action's free text. No rule
condition or non-persona component SHALL parse, branch on, or transform it.

#### Scenario: Prose is opaque downstream
- **WHEN** a turn proceeds past adjudication
- **THEN** every downstream trigger and component decision is a function of structured
  triples and payload fields, and the action's free text appears downstream only as an
  opaque stored reference
