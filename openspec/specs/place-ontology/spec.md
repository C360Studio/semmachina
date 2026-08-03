# place-ontology Specification

## Purpose

Define persistent locations as distinct from scenes, directed topology, and optional canonical
point geometry for world placement and bounded persona-context assembly.

## Requirements

### Requirement: Locations and scenes are distinct world entities
The closed world entity-kind set SHALL include `location`. Every scene SHALL carry exactly one
single-valued `scene.location.current` reference to a location. Characters and items MAY carry
single-valued `world.location.current`, whose object SHALL be a location and never a scene. Package
validation SHALL reject missing, duplicate, dangling, self, or wrong-kind placement before import.

#### Scenario: Several scenes may occur at one place
- **WHEN** two scenes reference the same location
- **THEN** both remain distinct units of play while characters and items located there are members
  of the shared persistent place

#### Scenario: Scene-as-place compatibility is refused
- **WHEN** a character or item uses a scene as `world.location.current`, or a scene has no valid
  `scene.location.current`
- **THEN** package validation fails rather than silently treating the scene as a location

#### Scenario: Persona context is assembled through place
- **WHEN** a turn names a scene whose location contains the acting character and other entities
- **THEN** context assembly retains the turn, scene, and location as three fixed entities, discovers
  membership from incoming location edges, performs at most six graph reads, and preserves the
  configured entity cap and one-hop traversal bound

### Requirement: Location connectivity is explicit and directed
`location.relation.connects-to` SHALL be a registered multi-valued location-to-location reference.
One edge SHALL authorize only its declared direction; bidirectional connectivity SHALL require two
authored edges. The engine SHALL NOT infer symmetry or a route.

#### Scenario: A one-way connection stays one-way
- **WHEN** the gatehouse location connects to the road and the road declares no reverse edge
- **THEN** the graph exposes gatehouse-to-road connectivity and no road-to-gatehouse connection

#### Scenario: Bidirectionality is authored twice
- **WHEN** two locations each declare a connection to the other
- **THEN** both directed edges materialize without a second connection model or inferred fact

### Requirement: Authored location geometry is optional and canonical
A location MAY declare the canonical `geo.location.latitude` and `geo.location.longitude`
predicates as a pair of finite numeric literals. Latitude SHALL be within [-90, 90] and longitude
within [-180, 180]. Either predicate on a non-location, only half the pair, or a non-finite or
out-of-range value SHALL fail package validation. Locations without geometry SHALL remain valid.

#### Scenario: Authored point geometry survives import
- **WHEN** a location declares an in-range latitude/longitude pair
- **THEN** both canonical predicates materialize unchanged and are available to SemStreams spatial
  indexing without a SemMachina alias

#### Scenario: Topology does not require coordinates
- **WHEN** a location declares connections but no latitude or longitude
- **THEN** the package loads and the authored topology remains the complete place contract

#### Scenario: Partial or invalid geometry is refused
- **WHEN** a location declares one coordinate without the other, a non-finite value, or an
  out-of-range latitude or longitude
- **THEN** package validation fails before any entity is materialized
