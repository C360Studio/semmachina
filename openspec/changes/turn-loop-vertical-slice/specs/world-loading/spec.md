# world-loading — Delta Spec

## ADDED Requirements

### Requirement: Package-shaped world fixture
A world SHALL be defined by a package directory containing `manifest.yaml` (v0 fields:
`id`, `name`, `version`, `engine_compat`, `description` — nothing else), `entities.jsonl`
(one entity per line: `local_id`, `type`, triples using registered vocabulary predicates
and `local:`-prefixed references), `rules/`, and `personas/`. Engine code SHALL contain no
hardcoded world content.

#### Scenario: Manifest validation
- **WHEN** the importer loads a package whose manifest is missing a required v0 field or
  whose `engine_compat` constraint excludes the running engine version
- **THEN** the import fails before any entity is materialized, with a reason naming the
  violation

### Requirement: Deterministic template-to-instance ID mapping
The importer SHALL map each `local_id` to the six-part entity ID
`{org}.semmachina.{world_ns}.{template_id}.{type}.{local_id}` (org and world namespace
from instance configuration), rewrite all `local:` references to mapped IDs, and validate
every produced ID against six-part and length limits before materialization.

#### Scenario: Mapping is deterministic
- **WHEN** the same package is imported twice into the same org and world namespace
- **THEN** every entity receives the same six-part ID both times

#### Scenario: Dangling local reference fails the import
- **WHEN** an entity's triples reference a `local:` ID that no entity in the package
  declares
- **THEN** the import fails before materialization, naming the dangling reference

### Requirement: Materialization through graph-ingest only
The importer SHALL materialize entities by emitting them through the standard Graphable →
graph-ingest path. It SHALL NOT write `ENTITY_STATES` directly. Re-importing an unchanged
package into an existing world SHALL be convergent (replace semantics; no duplicate or
competing triples).

#### Scenario: Import is graph-visible and single-writer
- **WHEN** a package import completes
- **THEN** all template entities are queryable from `ENTITY_STATES` and every write
  traveled through graph-ingest

#### Scenario: Re-import is a no-op
- **WHEN** the same package version is imported again into the same world
- **THEN** the resulting graph state is identical to the state after the first import

### Requirement: Player binding is instance configuration
The player entity and its played-character binding SHALL come from instance
configuration, not from the template package. A template SHALL be instantiable into
multiple worlds without modification.

#### Scenario: Same template, two worlds
- **WHEN** one package is imported into two different world namespaces
- **THEN** both worlds contain complete, independently addressable entity sets with no
  shared entity IDs
