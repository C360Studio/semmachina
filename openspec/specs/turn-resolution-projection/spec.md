# turn-resolution-projection Specification

## Purpose
Defines a faithful player-facing explanation of a terminal turn from the canonical delivered
resolution and narration, without recalculating or interpreting the engine's decision.
## Requirements
### Requirement: The resolution card renders delivered resolution evidence
For a terminal result carrying a resolution, the surface SHALL render the delivered plausibility,
risk, consequence class, outcome band, modifiers, and roll. When a roll is present, the card SHALL
show its mechanic, ordered dice, modifier total, and total. When the delivered verdict says no roll
was required, the card SHALL state that fact and SHALL NOT synthesize a roll. The client SHALL NOT
recompute a band, modifier total, consequence, or any other adjudication output.

#### Scenario: A rolled resolution is legible
- **WHEN** a completed delivery contains verdict scalars, a band, and a roll
- **THEN** the card shows those delivered values and their delivered modifier breakdown without
  deriving a replacement outcome

#### Scenario: An automatic resolution does not invent dice
- **WHEN** a completed delivery says no roll was required and carries no roll
- **THEN** the card shows the delivered band and a no-roll explanation without displaying zero or
  placeholder dice

#### Scenario: A partial resolution is not presented as complete
- **WHEN** a delivery violates the closed terminal-result contract or carries an invalid resolution
- **THEN** the surface reports a typed protocol error and renders no inferred or partially repaired
  resolution card

### Requirement: Narration remains the delivered fiction
The surface SHALL render narration only from the prose in the authorized delivery envelope. It
SHALL keep the canonical terminal result distinct from that prose and SHALL NOT resolve object-store
references, generate replacement prose, or infer narration from graph state in the browser.

#### Scenario: Delivered prose accompanies its resolution
- **WHEN** a valid terminal delivery contains narration for the same turn and outcome band
- **THEN** the surface renders that prose with the turn's resolution card

#### Scenario: A result without valid prose fails visibly
- **WHEN** a successful result references narration but the authorized delivery contains no valid
  prose
- **THEN** the surface reports a typed delivery error rather than showing an empty or generated story

### Requirement: Terminal results are idempotent in the view
The surface SHALL identify a terminal result by its canonical turn and action identities and SHALL
render one terminal entry for that identity. Delivery, reconnect recovery, and repeated retrieval
of the same result SHALL update or confirm the existing entry rather than append another result.
Additive unknown fields SHALL not create a conflict or become trusted view state. Conflicting known
canonical fields for one identity SHALL fail closed.

#### Scenario: Retrieval repeats a delivered result
- **WHEN** the same canonical result arrives once by delivery and again by retrieval
- **THEN** the session shows one narration and one resolution card for that turn

#### Scenario: One identity carries conflicting terminal data
- **WHEN** two terminal documents claim the same turn and action identity but differ in known
  canonical result content
- **THEN** the surface reports a protocol conflict and does not choose one by arrival order

