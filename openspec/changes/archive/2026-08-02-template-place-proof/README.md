# template-place-proof

Stage 3 proof that one template can produce distinct configured experiences while locations,
scenes, topology, and optional authored geometry remain explicit world data.

The implemented contract adds an optional closed `packs.yaml` v1 catalog and instance-level
`experience.persona_pack` / `experience.mechanics_pack` selection. Boot seeds only the selected
persona records and composes only the selected bounded world reactions with fixed engine-owned turn
sequencing. Campaign provenance seals the selection: same-selection restart skips import, while a
missing or changed selection is refused before Persona or Rules start.

Downloaded rules are limited to bounded graph mutations and bounded `deny`; they have no publish,
agent, approval, KV, or lifecycle authority. Places are first-class `location` entities. Scenes use
`scene.location.current`, topology uses directed `location.relation.connects-to`, and optional point
geometry uses paired canonical latitude/longitude predicates.

The deterministic two-experience proof is serial. `PERSONAS` is broker-global, so this change does
not claim concurrent voice isolation: the supported deployment remains one active experience/world
per broker and process, with separate brokers/stacks for concurrent different voices.

See `docs/guides/world-authoring.md` for the user-facing authoring, configuration, rule-authority,
place, migration, and deployment contracts.
