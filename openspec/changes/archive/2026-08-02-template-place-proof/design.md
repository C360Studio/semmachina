# Design — Template and Place Proof

## Context

Before this change, `Package` loaded every file under `personas/` and `rules/`. Boot seeded every
persona record but started only the fixed inline turn-sequencing pack; package rules were validated
and then remained inert. Separately, `scene` was both a unit of play and the object of
`world.location.current`. The scene assembler's reverse-edge lookup was the migration seam for
making place first-class.

## Goals / Non-Goals

**Goals:**

- Select voice and bounded mechanics as instance configuration over one unchanged template.
- Execute selected world reactions without granting packages turn-loop or model authority.
- Represent persistent place independently from a scene and preserve bounded context assembly.
- Accept topology-only worlds and optional author-controlled point geometry.

**Non-Goals:**

- Build a map renderer, layout engine, route planner, spatial query facade, or new workflow.
- Let a downloaded package replace engine sequencing or disable a mandatory safety stage.
- Infer missing coordinates from topology or missing reverse connections from one directed edge.

## Decisions

### D1 — `packs.yaml` is optional, closed, and versioned

Manifest v0 remains closed. A separate optional `packs.yaml` v1 catalog declares named
`persona_packs`, named `mechanics_packs`, and one default name for each class. Each entry contains
an explicit, package-relative list of JSON files. Paths must be clean, remain below `personas/` or
`rules/` respectively, exist, and pass the same persona/rule checks as legacy directories. Unknown
fields, versions, names, duplicate files within one pack, path traversal, empty persona packs, and
missing defaults fail package load before materialization. Files may be reused across packs so tone
variants can share adjudicator and companion fragments without duplicating package content.
Production package reads use `os.OpenRoot`, so symlinks cannot escape the package root after a path
passes lexical validation.

A legacy package with no catalog exposes an implicit `default` persona pack containing its sorted
`personas/*.json` files and an implicit empty `default` mechanics pack. Legacy `rules/*.json` are
currently validated but inert, so activating them during an upgrade would be a breaking behavior
change. A catalog opts into runnable mechanics files explicitly. Omitted instance selection
resolves to catalog defaults.

### D2 — Selection is instance state sealed into the resolved plan

Instance configuration carries an `experience` object with `persona_pack` and `mechanics_pack`.
Resolution validates both names and copies their exact sorted file lists into the resolved plan.
Boot consumes only that sealed selection; it does not reselect by directory scan. On fresh campaign
claim it also records both pack IDs as engine-owned, single-valued campaign provenance. Every
restart reads those durable IDs before persona seeding or rule startup and refuses a mismatch.
An existing campaign with neither provenance predicate is refused with an explicit migration
diagnosis; process-local state cannot prove what experience created a living world, so boot does
not backfill by guess. Two namespaces may therefore resolve the same immutable template with
different experiences while all entity IDs and local references remain namespace-disjoint.

### D3 — Engine sequencing and package reactions share a processor, not authority

The engine's checked turn-sequencing definitions remain fixed binary data. Selected mechanics
files are decoded only after package scope validation, checked for duplicate IDs against each
other and the engine pack, and appended as inline definitions in the same rule processor config.
Package rules remain unable to read or write `turn.*`, publish to stage/persona/model subjects,
write protected engine buckets, or use unclassified actions. They may only react to world facts
with bounded actions.

The capability allowlist is categorical: `add_triple`, `remove_triple`, `update_triple`,
`replace_owned`, and `deny`. Every action list is checked. Omitted `max_iterations` uses the bounded
upstream default of three; explicit values are 1 through 4, and zero is refused as unlimited.
Graph-action predicates are literal and outside reserved engine namespaces. Subject is the narrowed
trigger entity (omitted or exactly `$entity.id`); reference objects are `$entity.id`, or
`$related.id` with a narrowed related pattern. Scalar literal values remain valid, while foreign
IDs and unprovable substitutions fail closed. Packages cannot publish, dispatch an agent, approve,
write KV, or transition/complete/fail a lifecycle.

The base processor does not need graph integration for fixed turn sequencing. Boot enables it only
when a selected mechanic contains one of the four graph-mutating capabilities. Empty and
non-graph selections, including bounded `deny`-only packs, preserve the baseline processor config.

This stage's tunability proof uses two deterministic world-reaction packs with observably different
world-state consequences. It does not treat downloadable rules as trusted sequencing presets. A
future pure-fiction dice-bypass preset must be engine-owned and separately specified.

### D4 — A scene occurs at one persistent location

Add `location` to the closed entity-kind set. Every scene carries exactly one
`scene.location.current` reference to a location. Characters and items retain the single-valued
`world.location.current` predicate, but its object kind becomes location-only. A scene is no
longer a legal occupancy target.

Context assembly reads the turn's scene, resolves that scene's location, and performs the bounded
incoming membership lookup at the location. The view retains turn, scene, and location as three
fixed entities. Its structural ceiling becomes six graph reads: turn, scene, location, incoming
membership, one member batch, and one neighbour batch. The configured entity cap and one-hop bound
remain unchanged. Missing, duplicate, stub, or wrong-kind scene locations fail closed rather than
producing a smaller room.

### D5 — Topology is a directed authored edge

`location.relation.connects-to` is a multi-valued location-to-location reference. One edge means
one traversable direction. Authors declare two edges when movement is bidirectional. Self edges,
dangling references, duplicate objects, and wrong subject/object kinds fail existing package
preflight rules. No route search or inferred symmetry is introduced.

### D6 — Geometry is an optional paired point using canonical predicates

Locations may declare both `geo.location.latitude` and `geo.location.longitude` as finite numeric
literals. Latitude is within [-90, 90] and longitude within [-180, 180]. Supplying only one member
of the pair, an out-of-range value, or either predicate on a non-location fails package load.
Topology-only locations remain valid. SemMachina registers the upstream canonical predicate names
rather than inventing aliases; starting or querying the spatial index is deferred to the map stage.

### D7 — Living-world provenance and persona deployment stay explicit

Fresh campaign claim atomically records the selected persona and mechanics pack IDs. The
import-completion marker is written only after every planned entity is queryable and indexed. A
same-selection restart reads that provenance and skips template import. A changed selection, partial
provenance, or a pre-provenance campaign is refused before Persona or Rules; boot never infers a
selection from current defaults and never reimports a template over a living world.

This change does not add an automatic in-place migration. Existing uninstantiated fixtures migrate
by adding locations, scene placement, location occupancy, and optional `packs.yaml`; living worlds
need a separately reviewed provenance migration or a new namespace.

`world_ns` isolates graph identities, not persona prompts. Persona records live in the broker-global
`PERSONAS` bucket under stable IDs, so the supported MVP is one active experience/world per broker
and process. Concurrent worlds with different voices require separate brokers/stacks.

## TDD and Integration Contract

Implementation proceeds vocabulary/authoring first, then fixture and assembler migration, then
catalog selection and boot composition. Unit tests pin every closed set, subject/object kind,
shape, range, legacy fallback, selection refusal, and authority boundary before production wiring.
Integration tests import two namespaces from one package and run the same deterministic input with
different pack selections, asserting different selected persona records and different committed
world facts while engine turn stages remain identical. The runs are serial because `PERSONAS` is
global; the proof makes no concurrent voice-isolation claim.

## Risks / Trade-offs

- Adding location touches every fixture and scene lookup; fail-closed migration tests prevent a
  compatibility shim from silently preserving the old scene-as-place ontology.
- Combining world and engine rules increases collision surface; duplicate rule IDs are a boot
  refusal, never last-writer-wins.
- Coordinates use literal latitude/longitude for compatibility with SemStreams. Fictional maps
  should remain in a modest coordinate band; equal-area interpretation is not promised.
- Namespace-disjoint graph state can coexist on a broker, but voice cannot safely differ while
  `PERSONAS` remains global. Separate stacks are the deliberate MVP isolation boundary.

## Migration Plan

1. Add and register the ontology with failing vocabulary and authoring tests.
2. Add locations and scene-placement edges to shipped fixtures; move occupancy references.
3. Re-key context assembly and boot readiness from scene to location.
4. Add catalog parsing, implicit defaults, selection, and sealed plan fields.
5. Persist selection on fresh campaign claim and refuse missing or mismatched provenance.
6. Seed selected personas and compose selected world rules with fixed engine rules.
7. Add serial dual-instance/material-difference acceptance, reviews, docs, and full quality gates.

## Open Questions

No blocking architecture question remains. The exact future engine-owned pure-fiction sequencing
preset and any instance-scoped replacement for broker-global `PERSONAS` are deliberately outside
this change.
