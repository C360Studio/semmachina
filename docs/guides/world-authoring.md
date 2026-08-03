# World Authoring

A world package is an immutable template. Instance configuration supplies the namespace, player,
and selected experience that turn that template into one mutable campaign. Use
`fixtures/worlds/starter/` as the executable example.

## Package layout

```text
my-world/
├── manifest.yaml
├── entities.jsonl
├── packs.yaml                 # optional experience catalog
├── personas/
│   ├── adjudicator.json
│   └── narrator.json
└── rules/                     # optional world reactions
    └── wounded-reaction.json
```

`manifest.yaml` is the closed v0 manifest. It requires `id`, `name`, canonical semantic `version`,
`engine_compat`, and `description`; unknown fields fail package load. `entities.jsonl` contains one
strict JSON entity per non-empty line. Template references use `local:<local-id>` and resolve to
instance-specific six-part entity IDs only after an operator supplies `org` and `world_ns`.

Persona JSON is required for a runnable package. Rules are optional: a world with no automatic
reactions is valid. Package loading validates every manifest field, entity, persona, declared rule,
and reference before graph materialization.

Production opens a package with a rooted filesystem handle. Catalog paths must be clean,
package-relative `.json` paths below `personas/` or `rules/`, and symlinks cannot escape the package
root.

## Experience packs

`packs.yaml` is optional. When present, it is a closed v1 document with one default persona pack and
one default mechanics pack. Unknown fields or versions, missing defaults, undeclared default names,
missing files, path traversal, and duplicate paths within one pack fail load. A file may be shared
by different packs. Each persona pack must be non-empty, and its files must collectively provide
the required adjudicator and narrator roles; mechanics packs may be empty.

```yaml
version: 1
defaults:
  persona_pack: default
  mechanics_pack: quiet
persona_packs:
  default:
    files:
      - personas/adjudicator.json
      - personas/narrator.json
  alternate:
    files:
      - personas/adjudicator.json
      - personas/narrator-alternate.json
mechanics_packs:
  quiet:
    files: []
  reactive:
    files:
      - rules/wounded-reaction.json
```

Without `packs.yaml`, the loader preserves legacy behavior: sorted `personas/*.json` become the
implicit `default` persona pack, and the implicit `default` mechanics pack is empty. Legacy
`rules/*.json` are still validated but remain inert. Adding a catalog is the explicit opt-in to
selectable runtime mechanics; it does not change manifest v0.

## Select an experience for an instance

The operator schema uses `experience.persona_pack` and `experience.mechanics_pack`. Add the block to
a complete instance file such as `configs/instance.example.json`:

```json
{
  "nats_url": "nats://127.0.0.1:4222",
  "org": "c360",
  "world_ns": "gatehouse-alternate",
  "player": {
    "local_id": "one",
    "name": "The Player",
    "character": "local:rook",
    "credential": "CHANGE-ME-local-only-bearer-credential"
  },
  "experience": {
    "persona_pack": "alternate",
    "mechanics_pack": "reactive"
  }
}
```

Keep the socket and `model_registry` sections from the complete example. Either pack name may be
omitted to use that class's catalog default. Unknown names fail before broker access. Resolution
copies the exact sorted selected file lists into the world plan; boot seeds only those persona
records and composes only those mechanics rules with the fixed engine-owned turn rules. Unselected
files have no runtime effect.

Persona content can set voice, tone, and judging stance. It cannot select a model, tool schema, or
iteration budget. Mechanics can react to world facts, but cannot replace or reorder the engine's
turn stages.

## Package rule authority

Downloaded mechanics have a categorical capability boundary. The only admitted actions are:

- `add_triple`, `remove_triple`, `update_triple`, and `replace_owned` for bounded graph changes;
- `deny` for a bounded refusal returned to the rule caller.

Every executable action is bounded. Omitting `max_iterations` uses the upstream default of 3; an
explicit value must be from 1 through 4. Zero is unlimited upstream and is refused. The bound is on
the action, not the definition.

Graph mutation stays in the selected instance:

- `predicate` must be literal and outside reserved engine namespaces;
- `subject` is omitted for the triggering entity or is exactly `$entity.id`;
- an entity-reference `object` is `$entity.id`, or `$related.id` when a related pattern is declared;
- scalar predicates may use literal values, while foreign IDs and unprovable substitutions are
  refused; `remove_triple` ignores `object`, and an empty `replace_owned` clears its owned group.

Boot narrows primary and related entity patterns to the selected `org`, `world_ns`, and template.
Conditions may inspect world facts, but not turn, sealed campaign, protected player, secret truth,
or lifecycle-managed engine state.

Packages cannot `publish`, dispatch an agent/persona, `approve`, write arbitrary KV, or transition,
complete, or fail a lifecycle. Unknown action types fail closed. Graph integration is enabled only
when the selected mechanics actually contain a graph-mutating action; non-graph selections,
including bounded `deny`-only packs, retain the baseline processor configuration.

## Locations, scenes, and geometry

A `location` is a persistent place. A `scene` is a unit of play that occurs at exactly one place.
Every scene therefore declares one `scene.location.current` reference:

```text
{
  "local_id": "gatehouse",
  "type": "scene",
  "triples": [
    {"predicate": "scene.location.current", "object": "local:gatehouse-place"}
  ]
}
{
  "local_id": "gatehouse-place",
  "type": "location",
  "triples": [
    {"predicate": "location.relation.connects-to", "object": "local:north-road"},
    {"predicate": "geo.location.latitude", "object": 41.8819},
    {"predicate": "geo.location.longitude", "object": -87.6278}
  ]
}
{
  "local_id": "north-road",
  "type": "location",
  "triples": [
    {"predicate": "world.entity.name", "object": "North Road"}
  ]
}
```

The excerpt is expanded for readability; `entities.jsonl` stores each complete entity object on one
line.

Characters and items may carry one `world.location.current`, and its object must be a location, not
a scene. `location.relation.connects-to` is a directed, multi-valued location-to-location edge. One
authored edge grants one direction; write the reverse edge explicitly for bidirectional topology.
The engine does not infer symmetry or routes.

Coordinates are optional. When used, canonical `geo.location.latitude` and
`geo.location.longitude` must appear together as finite numbers. Latitude is in `[-90, 90]` and
longitude in `[-180, 180]`. A topology-only location such as the starter's `north-road` is valid.

## Existing packages and living worlds

To migrate an uninstantiated fixture from scene-as-place authoring:

1. Add `location` entities.
2. Give every scene exactly one `scene.location.current` reference.
3. Change character and item `world.location.current` references to locations.
4. Add directed topology and optional paired coordinates.
5. Add `packs.yaml` only when runtime selection is intended; otherwise retain the legacy defaults.

Living campaigns are protected from implicit migration. A fresh campaign atomically records its
persona and mechanics pack IDs, and the import-completion marker is written only after the world is
queryable. A restart with the same selection skips import, preserving play-created state. A changed
selection is refused before persona seeding or rule startup. A campaign created before experience
provenance exists is also refused: boot does not guess or backfill from current files or defaults.

There is no automatic in-place template or experience migration in this stage. Use a new
`world_ns` for a new campaign, or perform an explicit, separately reviewed migration that preserves
and records the living world's provenance. Normal boot never reimports a template over a living
world and never switches its experience.

## Deployment boundary

`world_ns` isolates entity IDs, but selected persona records are written to the broker-global
`PERSONAS` bucket under stable persona IDs. Consequently the supported MVP deployment is one active
experience/world per broker and process. Run separate brokers/stacks for concurrently active worlds
that need different voices. Sequential boots may replace the global records, which is why the
two-experience acceptance proof is deliberately serial and does not claim concurrent voice
isolation.
