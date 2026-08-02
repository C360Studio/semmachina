## MODIFIED Requirements

### Requirement: Hint ladder is deterministic and bounded
Repeated valid hint requests SHALL advance the bond's hint level through
`nudge → connect → next-step`. Requests after `next-step` SHALL remain at that final level.
A newly committed actor-knowledge grant for that companion SHALL be the only hint-ladder reset
and SHALL make the next hint a `nudge`; a duplicate, rejected, stale, or other actor's grant SHALL
NOT reset it. Every hint SHALL cite only companion-known evidence.

#### Scenario: Three requests climb the ladder
- **WHEN** the player makes three valid hint requests without a reset
- **THEN** the committed companion resolutions use `nudge`, then `connect`, then
  `next-step`

#### Scenario: Additional requests do not reveal the solution
- **WHEN** the player asks again after `next-step`
- **THEN** the ladder stays bounded, and the response cites no evidence outside the
  companion's knowledge projection

#### Scenario: New companion knowledge resets the ladder exactly once
- **WHEN** a new knowledge grant commits for the companion after the ladder reached `next-step`
- **THEN** an expected-revision update makes the next hint `nudge`, and duplicate or stale delivery
  of that grant cannot reset an already advanced ladder again

### Requirement: Companion initiative is capped and idempotent
Automatic companion work SHALL start only from a structured trigger, at most once for the
triggering turn. Every triggering rule SHALL declare a nonzero iteration cap as the hard
initiative ceiling and a silent exhaust path. Any bond-policy or component admission bound
SHALL be less than or equal to that rule cap and SHALL NOT replace or bypass it. Duplicate
decisions, triggers, or deliveries, including recovery after a process crash, SHALL cause at most
one initial provider call and SHALL converge on one logical task and one committed resolution.

#### Scenario: Kit intervenes once
- **WHEN** a structured warning trigger lands twice for one turn
- **THEN** at most one companion task and one companion resolution are committed for that
  turn

#### Scenario: Exhaustion is silent and terminal for the stage
- **WHEN** the companion reaches its iteration cap without a valid terminal decision
- **THEN** the companion stage commits a silent resolution and does not stall or retry
  without bound

#### Scenario: Crash recovery reuses the initial provider request
- **WHEN** the process crashes between companion task delivery, durable request-identity claim,
  initial request publication, response processing, or resolution commit
- **THEN** recovery reuses the claimed loop and request identities, publishes idempotently, and
  produces at most one initial provider call and one committed resolution
