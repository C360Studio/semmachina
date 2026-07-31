# fiction-adjudication Specification

## Purpose
TBD - created by archiving change turn-loop-vertical-slice. Update Purpose after archive.
## Requirements
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

### Requirement: The adjudicator holds the roll gate
The adjudicator's reported `requires_roll` SHALL be authoritative, and a verdict SHALL NOT
be rejected for disagreeing with the engine's advisory (plausibility, risk) mapping. The
disagreement SHALL be recorded on the resolved turn's **ledger manifest**, together with the
mapping version that produced it — the disagreement is derivable from the stored verdict
today, but the mapping is advisory and expected to be tuned with play, and a derived value
would flip retroactively for every historical turn when it changes. The archive records what
was true at the time. The declared band
set SHALL follow the reported value, so the engine never synthesizes a band the adjudicator
did not author.

#### Scenario: Verdict disagreeing with the advisory mapping proceeds
- **WHEN** the adjudicator reports `requires_roll` false for an action whose plausibility
  and risk classes would map to true
- **THEN** the verdict is accepted, the turn resolves through its single `auto` band, and
  the gate disagreement is recorded on the turn

#### Scenario: Engine never fabricates a band
- **WHEN** a verdict's reported roll gate disagrees with the mapping
- **THEN** the bands validated and applied are exactly those the adjudicator declared, and
  no empty band is synthesized to satisfy the mapping

### Requirement: Fiction boundary
Only the adjudicator (and other personas) SHALL read the action's free text. No rule
condition or non-persona component SHALL parse, branch on, or transform it.

#### Scenario: Prose is opaque downstream
- **WHEN** a turn proceeds past adjudication
- **THEN** every downstream trigger and component decision is a function of structured
  triples and payload fields, and the action's free text appears downstream only as an
  opaque stored reference

#### Scenario: The boundary is enforced at rule load, not by convention
- **WHEN** a rule declares a condition on a fiction-bearing predicate (narration reference,
  entity description, verdict rationale), all of which are registered rule-opaque
- **THEN** the rule fails validation and the pack does not load, so a rule that branches on
  prose cannot reach production regardless of review
