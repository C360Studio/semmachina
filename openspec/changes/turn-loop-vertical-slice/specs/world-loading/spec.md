# world-loading — Delta Spec

## ADDED Requirements

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
A world package's rules SHALL be refused at load when they reach a reserved namespace, with the
refusal naming the rule id, the position, and the reason. Four positions are checked: condition
fields (including per-action `when` guards), triple-mutating action predicates, publishing
action subjects, and `update_kv` buckets. Reserved predicate namespaces SHALL be derived at the
granularity their justification supports — `turn.*` whole, because every predicate in it is
engine loop state, and `campaign.seed.*` only, because the rest of that domain (the world clock
and its deadline rules) is world content. Reserved subject roots SHALL cover the engine's own
root and every subject the agentic loop consumes. Reserved buckets SHALL cover the
framework-owned graph buckets and the rule engine's own state bucket. A name in any reserved
position SHALL be a literal, because a substitution-assembled name resolves at fire time and
cannot be checked at load. An action type the loader has not classified SHALL be refused rather
than admitted unchecked.

#### Scenario: A world rule that drives the turn loop is refused
- **WHEN** a package ships a rule that matches or writes a `turn.*` predicate, or publishes to
  a stage-trigger subject
- **THEN** the load fails before any entity is materialized, naming the rule, the offending
  field or subject, and why that namespace belongs to the engine

#### Scenario: A world rule that reaches a persona is refused
- **WHEN** a package ships a rule that publishes to any subject the agentic loop consumes — a
  task, a control signal, a model response, an approval, or a tool result — by `publish_agent`
  or by plain `publish`
- **THEN** the load fails, because the reservation is on the SUBJECT rather than the action
  type, and those lanes spend money and steer a running persona

#### Scenario: A world rule that writes the rule engine's own state is refused
- **WHEN** a package ships a rule whose `update_kv` names the rule engine's state bucket
- **THEN** the load fails, because that bucket holds the per-action firing counters and
  upstream's runtime bucket guard covers only graph buckets

#### Scenario: The campaign clock stays world content
- **WHEN** a package ships a threshold reaction on a campaign clock predicate outside the seed
  namespace
- **THEN** the package loads, because world time is a world fact and deadline rules are the
  canonical world reaction

#### Scenario: A world reaction loads
- **WHEN** a package ships a rule that matches a world fact and writes a world fact
- **THEN** the package loads, because reactions are what a world's rules directory is for

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
