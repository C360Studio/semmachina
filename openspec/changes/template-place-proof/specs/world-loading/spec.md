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
