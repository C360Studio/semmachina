## ADDED Requirements

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

## MODIFIED Requirements

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
