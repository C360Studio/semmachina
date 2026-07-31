# campaign-ledger — Delta Spec

## ADDED Requirements

### Requirement: One immutable manifest per resolved turn
Every turn reaching phase `complete` or `failed` SHALL append exactly one manifest to the
campaign ledger stream, keyed by `turn_id`, carrying references only: action payload ref,
verdict ref, roll-result ref (if rolled), applied effect-batch ref (if applied), narration
prose ref (if narrated), failure reason (if failed), a real-time stamp, and a world-time
field (zero until the world clock exists). Manifests SHALL never carry prose or bulky
content inline.

#### Scenario: Completed turn is ledgered
- **WHEN** a turn enters phase `complete`
- **THEN** the ledger contains exactly one manifest for its `turn_id` whose references
  resolve to the turn's actual artifacts

#### Scenario: Failed turn is ledgered
- **WHEN** a turn enters phase `failed`
- **THEN** the ledger contains exactly one manifest for its `turn_id` carrying the
  failure reason

#### Scenario: Duplicate ledger append is dropped
- **WHEN** the ledger writer is triggered twice for the same turn
- **THEN** the ledger contains exactly one manifest for that `turn_id`

### Requirement: The ledger is the archive, not the graph
The ledger stream SHALL be configured with no age- or size-based eviction of manifests.
`ENTITY_STATES` SHALL remain current truth; reconstruction of campaign history SHALL be
possible from the ledger alone plus the stores its references point into.

#### Scenario: History outlives graph churn
- **WHEN** an entity's attribute has been overwritten by later turns
- **THEN** the earlier value's turn context remains reconstructable from the ledger
  manifest chain and referenced artifacts

### Requirement: Replay honesty
Replay tooling SHALL re-execute only deterministic stages (dice, effect validation) and
SHALL reproduce persona output exclusively by reading preserved artifacts. Re-running a
persona SHALL be treated as producing a new rendition, never a replay.

#### Scenario: Deterministic replay of a rolled turn
- **WHEN** a ledgered turn's roll is re-executed from its manifest references
- **THEN** the reproduced dice values and band match the original roll-result artifact
  byte-for-byte, and the narration is read from the preserved prose ref, not regenerated
