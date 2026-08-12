# runtime-composition Specification

## Purpose
TBD - created by archiving change semstreams-beta160-migration. Update Purpose after archive.
## Requirements
### Requirement: Beta.160 adoption uses isolated fresh storage
SemMachina SHALL start beta.160 only against newly provisioned NATS storage whose identity is
recorded independently from the beta.159 deployment. The beta.159 application and storage SHALL
remain one untouched rollback unit. Discovery of retained state on the intended beta.160 storage
SHALL stop adoption without deleting, reseeding, or interpreting that state.

#### Scenario: Retained state blocks adoption
- **WHEN** the beta.160 preflight discovers retained deployed state on its intended NATS storage
- **THEN** startup stops before materialization, and the operator opens a separate owner-reviewed
  migration or recovery design instead of wiping or converting the state

#### Scenario: Rollback preserves release boundaries
- **WHEN** the green beta.160 stack fails before or after traffic moves
- **THEN** rollback stops the green stack and selects the complete beta.159 application and its
  original storage without opening beta.160-created state

### Requirement: Activation barriers use registered upstream component managers
The composition root SHALL validate every component manifest and cross-barrier dependency before
starting one ordinary upstream `ComponentManager` for each immutable activation barrier. Barriers
SHALL start in dependency order and stop in reverse order on failure or shutdown. The coordinator
SHALL NOT call component lifecycle methods directly or reproduce manager admission behavior.

#### Scenario: Cross-barrier validation fails before activation
- **WHEN** a required cross-barrier port has no exact provider or a component manifest is invalid
- **THEN** the composition root refuses startup before any barrier starts

#### Scenario: A later barrier fails to start
- **WHEN** a barrier fails after earlier barriers have started successfully
- **THEN** the coordinator stops every started barrier exactly once in reverse dependency order

### Requirement: Runtime ports, readiness, and storage resolution are exact
Every component SHALL declare beta.160 canonical typed ports with explicit direction and config.
Graph foundation readiness SHALL come from `GRAPH_STATUS`, and rule readiness SHALL come from the
supported rule-domain lifecycle evidence. The rule gate SHALL require the manager's started state
and a newer `GRAPH_STATUS/rule` KV generation whose state is ready, `Ready` is true, and
`BootstrapComplete` is true. The agentic barrier SHALL register its object-store provider under the
logical `StoreRegistry` instance `objectstore`, mapped to the configured physical content bucket.
Trajectory evidence and SemMachina content references SHALL both use that logical instance. Legacy
port shapes, component-status reads, aliases, and storage-name fallbacks SHALL be rejected.

#### Scenario: A strict runtime manifest starts
- **WHEN** every required port has one matching provider, graph and rule readiness are healthy, and
  logical `objectstore` is registered exactly once for the configured physical content bucket
- **THEN** the dependent barriers start without consulting legacy ports, component status, or a
  fallback store

#### Scenario: Storage registration does not match a reference
- **WHEN** the logical `objectstore` provider is missing or duplicated, or trajectory/content
  configuration names a different logical instance
- **THEN** startup fails before agentic work or content publication begins
