## ADDED Requirements

### Requirement: Terminal result carries a structural companion summary
The canonical terminal result SHALL carry a `CompanionResolution` when an active companion
bond produced a committed decision for the turn, including `silent`. A turn with no active
bond SHALL omit `CompanionResolution`. The summary SHALL contain `companion_id`, a decision
kind from the closed companion vocabulary, and a hint level exactly when the kind is
`hint`. Companion dialogue SHALL remain in ordinary narration prose rather than in the
structural result.

The player/v1 protocol tests SHALL pin the summary's fields, optionality, and closed sets.
Delivery and retrieval SHALL return the same canonical result without adapter-specific
rewriting.

#### Scenario: Hint result is machine-readable and voiced once
- **WHEN** a turn commits a companion `hint` at level `connect` and narration voices it
- **THEN** the canonical result contains the companion ID, `hint`, and `connect`, while the
  delivered prose contains the voice

#### Scenario: No active bond omits companion resolution
- **WHEN** the turn has no active companion bond
- **THEN** the canonical result omits `CompanionResolution`

#### Scenario: Active silent companion remains visible
- **WHEN** an active companion commits a `silent` decision
- **THEN** the canonical result contains `CompanionResolution` with that companion ID and
  kind `silent`, with no hint level or fabricated dialogue

#### Scenario: Retrieval preserves companion resolution
- **WHEN** a player retrieves a terminal turn that carried a companion resolution
- **THEN** the retrieved canonical result encodes the same companion summary as the
  originally published result
