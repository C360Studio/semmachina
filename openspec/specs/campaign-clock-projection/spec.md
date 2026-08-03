# campaign-clock-projection Specification

## Purpose
Defines an honest orientation readout from an explicitly configured typed world fact, including a
closed not-configured state and no simulated passage of time.
## Requirements
### Requirement: Clock projection has configured and not-configured states
Server configuration SHALL either identify one typed clock fact inside the active world or omit the
clock projection. The browser-facing view model SHALL discriminate `configured` from
`not_configured`. A configured view SHALL carry the fact's server-supplied label, typed value, and
unit when declared. The browser SHALL not select the entity or predicate. An absent configuration
SHALL produce `not_configured`; a configured but missing, ambiguous, malformed, or out-of-scope fact
SHALL produce a typed projection error rather than the not-configured state.

#### Scenario: A configured fact is displayed as recorded
- **WHEN** server configuration identifies one valid typed fact in the active world
- **THEN** the readout displays its delivered label, value, and declared unit without conversion

#### Scenario: The world has no configured clock projection
- **WHEN** server configuration omits the clock fact
- **THEN** the readout reports `not_configured` rather than zero, the host's time, or a guessed world
  time

#### Scenario: A configured clock fact is invalid
- **WHEN** the configured fact is missing, multi-valued where one value is required, malformed, or
  outside the active-world scope
- **THEN** the adapter returns a typed projection error and the surface does not disguise it as
  `not_configured`

### Requirement: The clock readout never simulates time
The readout SHALL change only when a newly queried authoritative fact has a different value. It
SHALL NOT tick, interpolate, extrapolate, select a clock policy, advance a deadline, or write a world
fact. This capability SHALL introduce no clock vocabulary or pacing behavior.

#### Scenario: Wall time passes between graph reads
- **WHEN** real time passes while the authoritative configured clock fact remains unchanged
- **THEN** the displayed world time remains unchanged

#### Scenario: An authoritative clock fact advances
- **WHEN** a later projection returns a different valid value for the same configured fact
- **THEN** the readout displays that value without calculating the elapsed interval itself

