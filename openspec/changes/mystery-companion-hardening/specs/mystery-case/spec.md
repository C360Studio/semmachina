## MODIFIED Requirements

### Requirement: Authored mystery case is complete and protected
Each mystery package SHALL declare one case with a canonical culprit, method, motive,
ordered timeline, suspects, and evidence. The Bellweather acceptance package SHALL contain
exactly six suspects and twelve clues or red herrings. Package validation and import SHALL
classify canonical solution and truth-status predicates as immutable, and package rules and
effect intents SHALL NOT branch on or mutate them. Graph-ingest and operator write surfaces
SHALL reject any later mutation of a resident canonical solution or truth-status value.

#### Scenario: Bellweather package is structurally complete
- **WHEN** the Bellweather package is validated
- **THEN** it contains one case, six suspects, twelve evidence entities including multiple
  red herrings, and complete culprit, method, motive, and ordered timeline references

#### Scenario: Package-local truth mutation is rejected
- **WHEN** package validation, import, a world rule, or an effect intent encounters a protected
  canonical solution or truth-status mutation
- **THEN** the domain gate rejects it before the authored value can change

#### Scenario: Substrate writes cannot rewrite canonical truth
- **WHEN** graph ingestion or an operator write attempts to change a resident canonical solution
  or truth-status predicate after import
- **THEN** the substrate write is rejected and the authored value remains unchanged
