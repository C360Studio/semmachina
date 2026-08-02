# companion-support Specification

## Purpose
Define reusable character-backed companions with actor-scoped knowledge, structural decisions,
bounded hint progression and initiative, and narrator-owned voice.
## Requirements
### Requirement: Companion identity and relationship are durable world state
A companion SHALL be a character-backed actor connected to a player by a durable
companion-bond entity. The bond SHALL record the active policy and hint level. Companion
knowledge SHALL be actor-scoped and SHALL NOT be inferred from the player's knowledge.

#### Scenario: Kit is data, not a hard-coded role
- **WHEN** the Bellweather package and player instance configuration are loaded
- **THEN** Kit Finch participates through a character entity and companion bond accepted by
  the reusable companion runtime, with no Bellweather-specific engine branch

#### Scenario: An arbitrary companion uses the same runtime
- **WHEN** another world binds an arbitrary player and character as companions using the
  same bond predicates and policy fields
- **THEN** the runtime accepts the bond and processes that character without a mystery,
  Bellweather, or Kit-specific branch

#### Scenario: Player knowledge does not silently become Kit's
- **WHEN** the player discovers a clue without Kit witnessing it or receiving a share
- **THEN** the clue remains absent from Kit's knowledge and companion projection

### Requirement: Companion decisions use a strict structural payload
`CompanionDecision` SHALL carry `decision_id`, `turn_id`, generic `context_ref`,
`player_id`, `companion_id`, no more than eight `evidence_refs`, an optional structural
`target_ref`, and a kind from the closed set `silent`, `quip`, `question`, `warning`,
`recall`, or `hint`. Bellweather SHALL use its case ID as `context_ref`; the payload SHALL
also accept a scene, encounter, quest, or other activity reference from another world.
The payload SHALL carry a hint level from `nudge`, `connect`, or `next-step` exactly when
the kind is `hint` and SHALL contain no generated dialogue, rationale, or other prose.

Validation SHALL require evidence for `warning`, `recall`, and `hint`, reject duplicate or
malformed references, and enforce the eight-reference ceiling. `decision_id` SHALL be the
lowercase hexadecimal SHA-256 of the length-prefixed tuple (`companion-decision/v1`,
`turn_id`, `context_ref`, `player_id`, `companion_id`); validation SHALL reject a supplied
ID that differs. Runtime authorization SHALL separately prove every evidence reference
belongs to the companion's knowledge projection.

#### Scenario: Structurally invalid companion decision is refused
- **WHEN** a `hint` omits its hint level, a `quip` supplies one, or a recall cites no
  evidence
- **THEN** payload validation rejects the decision before it reaches a rule

#### Scenario: Companion evidence bounds are enforced
- **WHEN** a decision supplies more than eight evidence references, duplicates a reference,
  or supplies an ID that differs from the deterministic decision ID
- **THEN** payload validation rejects the decision before it reaches a rule

#### Scenario: Unknown evidence is refused at runtime
- **WHEN** a schema-valid companion decision cites evidence the companion does not know
- **THEN** runtime authorization rejects it and no companion resolution is committed

### Requirement: Hint ladder is deterministic and bounded
Repeated valid hint requests SHALL advance the bond's hint level through
`nudge → connect → next-step`. Requests after `next-step` SHALL remain at that final level.
Every hint SHALL cite only companion-known evidence.

#### Scenario: Three requests climb the ladder
- **WHEN** the player makes three valid hint requests without a reset
- **THEN** the committed companion resolutions use `nudge`, then `connect`, then
  `next-step`

#### Scenario: Additional requests do not reveal the solution
- **WHEN** the player asks again after `next-step`
- **THEN** the ladder stays bounded, and the response cites no evidence outside the
  companion's knowledge projection

### Requirement: Companion initiative is capped and idempotent
Automatic companion work SHALL start only from a structured trigger, at most once for the
triggering turn. Every triggering rule SHALL declare a nonzero iteration cap as the hard
initiative ceiling and a silent exhaust path. Any bond-policy or component admission bound
SHALL be less than or equal to that rule cap and SHALL NOT replace or bypass it. Duplicate
decisions, triggers, or sequential deliveries SHALL converge on one logical task and one
committed resolution. This logical idempotency does not claim at-most-one provider call across
a process crash.

#### Scenario: Kit intervenes once
- **WHEN** a structured warning trigger lands twice for one turn
- **THEN** at most one companion task and one companion resolution are committed for that
  turn

#### Scenario: Exhaustion is silent and terminal for the stage
- **WHEN** the companion reaches its iteration cap without a valid terminal decision
- **THEN** the companion stage commits a silent resolution and does not stall or retry
  without bound

### Requirement: Narrator owns companion voice
The narrator SHALL voice only a committed companion decision and SHALL NOT decide what the
companion knows, whether the companion intervenes, or which hint level applies.

#### Scenario: Structured recall becomes voiced prose
- **WHEN** Kit commits a `recall` decision citing authorized evidence
- **THEN** the narrator may render Kit's established voice in prose without changing the
  decision or adding unrevealed evidence
