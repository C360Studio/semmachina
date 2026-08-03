## Purpose

Defines a least-authority, read-only projection of the active world's public place graph into an
honest map with authored geometry and deterministic topology fallback.

## ADDED Requirements

### Requirement: Graph reads are server-scoped and allowlisted
The graph adapter SHALL run only on the server and SHALL expose purpose-built view models to the
browser. It SHALL derive the active organization, world, template, and entity prefix from trusted
deployment/session state; validate every requested and returned entity against that scope; and fail
closed when scope cannot be established. Its upstream GraphQL operation allowlist SHALL contain
only `entity`, `entitiesByPrefix`, `relationships`, and `spatialSearch`. It SHALL accept neither an
arbitrary GraphQL document nor a browser-selected entity prefix.

#### Scenario: A valid map request stays inside the active world
- **WHEN** an authenticated session requests its map projection
- **THEN** the server queries only allowlisted graph operations under the derived active-world scope
  and returns a typed map view model

#### Scenario: A forged prefix is not a query parameter
- **WHEN** a browser supplies a world namespace, entity prefix, raw GraphQL document, or out-of-scope
  entity identifier
- **THEN** the adapter refuses the input without forwarding it upstream or disclosing graph data

#### Scenario: An out-of-scope result fails closed
- **WHEN** an upstream response contains an entity or relationship endpoint outside the derived
  active-world prefix
- **THEN** the adapter returns a typed projection error and excludes the response from the map

#### Scenario: An upstream authorization or query gap remains visible
- **WHEN** the projection cannot be implemented safely with the allowlisted beta.159 operations and
  available authentication boundary
- **THEN** implementation stops at a filed SemStreams issue rather than adding a local query service,
  direct NATS access, or an unrestricted proxy

### Requirement: The map is a projection of authoritative place facts
The map SHALL contain typed location nodes, player-safe labels, directed
`location.relation.connects-to` edges, and optional canonical authored coordinates from the active
world. It SHALL not expose arbitrary entity triples or treat nearby coordinates as connectivity.
Refreshing the map SHALL re-project authoritative graph state; neither browser nor server SHALL
write map positions or derived connectivity back into the world.

#### Scenario: Authored geometry controls placement
- **WHEN** a location carries a valid authored latitude/longitude pair
- **THEN** its map position comes from that pair and automatic layout does not move or replace it

#### Scenario: Connectivity remains directed graph data
- **WHEN** location A connects to location B and no reverse edge is authored
- **THEN** the map shows the A-to-B connection and does not infer B-to-A from distance or appearance

#### Scenario: Rendering does not create world facts
- **WHEN** the map lays out, pans, zooms, or refreshes
- **THEN** it performs no graph mutation and persists no derived map artifact as authoritative state

### Requirement: Topology-only worlds receive a deterministic schematic
Locations without authored coordinates SHALL receive deterministic labelled schematic positions
computed from the directed topology and stable location identity. The same scoped graph SHALL yield
the same positions regardless of query return order. When authored and topology-only locations are
mixed, authored positions SHALL remain fixed and fallback placement SHALL apply only to unpositioned
nodes. A disconnected location SHALL remain visible and labelled.

#### Scenario: A topology-only map is stable
- **WHEN** the same unmodified place graph is projected from differently ordered query responses
- **THEN** every location receives the same label and schematic position in both projections

#### Scenario: Partial geometry preserves authored intent
- **WHEN** some locations have authored coordinates and others have only topology
- **THEN** authored locations keep their exact positions and only the remaining locations use the
  deterministic fallback

#### Scenario: A disconnected location is not discarded
- **WHEN** a scoped location has neither coordinates nor a connective edge
- **THEN** the schematic still displays it with a deterministic position and label
