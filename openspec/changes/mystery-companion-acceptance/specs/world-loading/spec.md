## ADDED Requirements

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
