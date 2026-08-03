# world-loading Specification

## Purpose
TBD - created by archiving change turn-loop-vertical-slice. Update Purpose after archive.
## Requirements
### Requirement: Package-shaped world fixture
A world SHALL be defined by a package directory containing `manifest.yaml` (v0 fields:
`id`, `name`, `version`, `engine_compat`, `description` — nothing else), `entities.jsonl`
(one entity per line: `local_id`, `type`, triples using registered vocabulary predicates
and `local:`-prefixed references), `personas/`, and optionally `rules/`. Engine code SHALL
contain no hardcoded world content.

#### Scenario: Manifest validation
- **WHEN** the importer loads a package whose manifest is missing a required v0 field or
  whose `engine_compat` constraint excludes the running engine version
- **THEN** the import fails before any entity is materialized, with a reason naming the
  violation

#### Scenario: A world that reacts to nothing is still a world
- **WHEN** a package carries no `rules/` directory
- **THEN** the package loads with no rule files, because world reactions are optional
  content and requiring one produces placeholder rules rather than real ones

### Requirement: World rules may not author the turn loop, dispatch personas, or write engine state
Every world-rule definition SHALL be preflighted before entity materialization. Conditions,
per-action `when` guards, and graph-action predicates SHALL NOT read or write reserved engine state.
Every action in `on_enter`, `on_exit`, `while_true`, `on_recovery`, and `actions` SHALL be checked.

Downloaded rules SHALL be limited categorically to `add_triple`, `remove_triple`, `update_triple`,
`replace_owned`, and `deny`. The loader SHALL refuse `publish`, `publish_agent`, `approve`,
`update_kv`, `lifecycle_transition`, `lifecycle_complete`, `lifecycle_fail`, and every unclassified
action regardless of its subject or bucket.

Every admitted action SHALL be bounded by action-level `max_iterations`. Omission SHALL use the
upstream default of three only while that default remains within the package ceiling. Explicit
values SHALL be from 1 through 4; zero and values above 4 SHALL be refused. Boot SHALL narrow primary
and related entity patterns to the selected organization, world, and template. A graph-action
predicate SHALL be literal and outside reserved namespaces. Its subject SHALL be omitted or exactly
`$entity.id`. An entity-reference object SHALL be `$entity.id`, or `$related.id` when the rule
declares a related pattern. Foreign entity IDs and unprovable substitutions SHALL be refused.
Scalar literals SHALL remain valid; `remove_triple` SHALL ignore its object, and an empty
`replace_owned` object SHALL clear the owned group.

#### Scenario: Reserved engine state is refused
- **WHEN** a package rule condition, per-action guard, or graph mutation reads or writes a reserved
  turn, campaign provenance, player gate, protected truth, or lifecycle predicate
- **THEN** package loading fails before materialization and names the rule, position, and boundary

#### Scenario: A world rule that drives the turn loop is refused
- **WHEN** a package rule condition, per-action guard, or graph-action predicate reads or writes
  protected turn state
- **THEN** package loading fails before entity materialization and names the rule and boundary

#### Scenario: Unassigned action capabilities are refused categorically
- **WHEN** a package rule uses publish, agent dispatch, approval, arbitrary KV, lifecycle, or an
  unclassified action capability
- **THEN** package loading fails regardless of the action's subject or bucket

#### Scenario: A world rule that reaches a persona is refused
- **WHEN** a package rule uses `publish` or `publish_agent`
- **THEN** package loading fails categorically with no exception for an author-named subject

#### Scenario: A world rule that writes the rule engine's own state is refused
- **WHEN** a package rule uses `update_kv`
- **THEN** package loading fails categorically with no exception for an author-named bucket

#### Scenario: Every admitted action has a package iteration ceiling
- **WHEN** an admitted action omits `max_iterations` while the upstream default is three, or declares
  a value from 1 through 4
- **THEN** it passes the action-bound check, while explicit zero, a value above 4, or an upstream
  default outside the package ceiling is refused

#### Scenario: Graph mutations remain in the selected instance
- **WHEN** a graph action targets the narrowed entity through an omitted subject or `$entity.id`, or
  uses `$related.id` with a declared related pattern
- **THEN** the action passes the identity boundary, while a foreign ID or unprovable substitution is
  refused before startup

#### Scenario: The campaign clock stays world content
- **WHEN** a package rule reads a campaign clock predicate outside the reserved campaign namespaces
- **THEN** it passes the engine-state boundary because world time and deadline reactions are world
  content

#### Scenario: A world reaction loads
- **WHEN** a package rule matches an authorable world fact and uses bounded graph actions with safe
  same-instance references or scalar literals, or returns a bounded deny verdict
- **THEN** the package loads because the reaction remains inside the world's assigned authority

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

### Requirement: Instantiation happens once, never implicitly on restart
A world instance already materialized from a template SHALL NOT be re-imported implicitly,
and no startup path SHALL import into an existing world instance. Convergence and
destruction are the same mechanism: triples replace by (subject, predicate), and a
multi-valued predicate replaces as a whole set, so re-importing a template into a living
campaign resets every fact the template declares and drops relationships play has since
created. Applying a template update to a live world is a separate, explicitly invoked path.

#### Scenario: Restart does not re-import
- **WHEN** the engine restarts against a world instance that was already materialized
- **THEN** no import is performed, and every fact created by play since instantiation
  survives intact

#### Scenario: Re-import would not silently discard play
- **WHEN** an import is invoked against a world namespace that already carries a
  materialized template
- **THEN** the import is refused rather than converging the world back to template state

### Requirement: Player binding is instance configuration
The player entity and its played-character binding SHALL come from instance
configuration, not from the template package. A template SHALL be instantiable into
multiple worlds without modification.

#### Scenario: Same template, two worlds
- **WHEN** one package is imported into two different world namespaces
- **THEN** both worlds contain complete, independently addressable entity sets with no
  shared entity IDs

### Requirement: Mystery package records are validated before materialization
A package that declares a mystery case SHALL provide registered, structurally typed
entities and predicates for its case, solution, timeline, suspects, evidence, beliefs,
knowledge seeds, and character-backed companions. Validation SHALL confirm referential
integrity, cardinality, ordering, and immutable-truth classification before any entity is
materialized.

#### Scenario: Incomplete case package fails atomically
- **WHEN** a mystery package omits a solution dimension, references a missing timeline
  event, or violates a declared cardinality
- **THEN** import fails before materialization and names the invalid case field

#### Scenario: Bellweather imports through the ordinary package path
- **WHEN** the Bellweather package passes validation
- **THEN** its case, suspects, evidence, beliefs, Kit character, and companion-ready data
  materialize through graph-ingest with no hard-coded engine content

### Requirement: Protected truth cannot be authored by package rules
World rule scope validation SHALL reserve canonical solution and truth-status predicates
from all rule condition and mutation positions that could disclose or change authored
truth. A package MAY contain those predicates only as validated import data.

#### Scenario: World rule cannot branch on the culprit
- **WHEN** a package rule matches or publishes a canonical culprit or truth-status value
- **THEN** package loading fails before materialization, naming the rule and protected
  predicate

#### Scenario: World rule cannot rewrite a solution
- **WHEN** a package rule attempts to add, remove, or replace a canonical solution
  predicate
- **THEN** package loading fails before materialization

### Requirement: Experience catalogs are preflighted package data
A world package MAY carry a closed `packs.yaml` v1 catalog containing named persona packs,
named mechanics packs, and one default name for each class. Every entry SHALL enumerate clean,
package-relative JSON paths confined to its matching `personas/` or `rules/` directory. The loader
SHALL validate every declared file with the same persona or world-rule gate used for legacy files
before any instance is resolved or entity is materialized. `manifest.yaml` SHALL remain the closed
v0 contract.

#### Scenario: A catalog is validated before materialization
- **WHEN** `packs.yaml` has an unknown field or version, a missing default, an empty persona pack,
  a duplicate within one pack, a missing file, path traversal, or a file outside its matching
  directory
- **THEN** package load fails before resolution or graph publication and names the invalid catalog
  field or path

#### Scenario: A legacy package receives implicit defaults
- **WHEN** a valid package has no `packs.yaml`
- **THEN** it exposes an implicit `default` persona pack over its sorted legacy persona files and
  an empty implicit `default` mechanics pack without changing manifest v0 or activating legacy
  `rules/*.json` files that were previously inert

#### Scenario: Persona fragments may be shared across packs
- **WHEN** two named persona packs reference the same valid adjudicator or companion file
- **THEN** the catalog loads because reuse across packs is allowed while duplicate paths within one
  pack remain invalid
