# Template and Place Proof

## Why

The engine can already resolve one template into disjoint world namespaces, but a package has
only one undifferentiated persona directory and its validated world rules are not started. Scenes
also double as places, so the graph cannot say that several scenes occur at one persistent
location or that two locations connect. Stage 3 turns both design claims into executable proofs.

## What Changes

- Add a closed, versioned optional `packs.yaml` catalog. An instance selects one persona pack and
  one mechanics/world-reaction pack; packages without a catalog retain implicit legacy defaults.
- Seed only the selected persona files and combine only the selected, scope-validated world rules
  with the fixed engine-owned turn-sequencing rules.
- Prove the same template and engine code produce materially different voice and deterministic
  world-state consequences under two instance selections.
- Add first-class `location` entities, a scene-to-location edge, directed location connectivity,
  and optional paired latitude/longitude using SemStreams' canonical spatial predicates.
- Migrate the starter and Bellweather fixtures and make persona context resolve scene membership
  through the scene's location.

## Capabilities

### New Capabilities

- `place-ontology`: persistent locations, scene placement, occupancy, topology, and optional
  authored point geometry.
- `experience-packs`: closed package catalogs and instance-scoped persona/mechanics selection.

### Modified Capabilities

- `world-loading`: load and preflight the optional experience catalog before resolving or
  materializing a selected instance.

## Non-goals

- Map UI, auto-layout implementation, route finding, spatial query APIs, or starting the
  SemStreams spatial-index component.
- Scene, campaign, or encounter lifecycle work.
- Downloadable control of turn stages, model routing, tool schemas, spend, or iteration caps.
- The final pure-fiction sequencing preset that bypasses dice; trusted sequencing presets remain
  engine-owned future work.
- Multiple active persona or mechanics packs, runtime pack switching, or live template upgrades.

## Classification

This is game-repo work. Locations, connections, tone fragments, and bounded world reactions are
authored domain data. The engine retains turn sequencing and execution authority, and the change
composes existing SemStreams graph, rule, and canonical geo vocabulary without a substrate fork.

## Impact

- Extends world vocabulary and authoring validation, package loading/resolution, boot persona and
  rule composition, scene context assembly, fixtures, and deterministic integration acceptance.
- Existing packages remain readable through an implicit `default` persona selection over their
  legacy persona files and an empty mechanics selection, preserving today's inert legacy rules.
- Existing world instances require fixture migration because `world.location.current` will point
  only to `location` entities rather than scenes.
- New campaigns durably record both selected pack IDs. Existing campaigns without that provenance
  fail closed until explicitly migrated; boot never guesses which experience a living world used.
