## MODIFIED Requirements

### Requirement: Narrator voices committed outcomes only
The narrator persona SHALL run only after effect application, case knowledge grants, and
companion resolution have committed. It SHALL read the verdict, roll result, applied or
rejected effects, committed case and companion decisions, and current scene facts from the
centralized player-narration projection assembled at execution time. The narrator SHALL
narrate only the outcome and revelations that actually committed, SHALL have no tool
capable of world mutation, and SHALL NOT decide what a companion knows, whether the
companion intervenes, or which hint level applies.

Canonical solution facts SHALL remain absent from narrator context until a correct
accusation enters `denouement`; the denouement narrator may then receive the solution
through its dedicated purpose.

#### Scenario: Narration reflects the committed band
- **WHEN** a turn's roll selected the `partial` band and its effects committed
- **THEN** the narration voices a partial success consistent with the applied effects,
  and no world state differs before and after the narration stage

#### Scenario: Narrator cannot mutate
- **WHEN** the narrator persona executes
- **THEN** its available tools contain no mutation-capable tool, and the turn's applied
  effects are identical before and after narration

#### Scenario: Narrator voices a committed companion decision
- **WHEN** the companion commits a `quip`, `recall`, or `hint` decision
- **THEN** narration may give it the companion's configured voice without adding evidence
  absent from the committed decision and player-authorized projection

#### Scenario: Solution is withheld until denouement
- **WHEN** the case has not entered `denouement`
- **THEN** canonical solution canaries are absent from narrator prompt and model bytes
