## ADDED Requirements

### Requirement: One closed experience selection is resolved per instance
Instance configuration MAY select one named persona pack and one named mechanics pack from the
loaded package catalog. Omitted names SHALL resolve to catalog defaults. Unknown names, multiple
names for one class, or a selection that cannot supply the required persona roles SHALL fail before
boot touches the broker. Resolution SHALL seal the exact sorted selected file lists into the plan.

#### Scenario: Two worlds select differently without editing the template
- **WHEN** two world namespaces resolve the same package with different valid experience selections
- **THEN** both plans contain complete disjoint entity identities and each carries only its selected
  persona and mechanics files

#### Scenario: An unknown selection fails before boot
- **WHEN** instance configuration names a persona or mechanics pack absent from the package catalog
- **THEN** construction fails with the class and unknown name before campaign claim or publication

### Requirement: Experience selection is durable campaign provenance
Fresh campaign claim SHALL atomically record the selected persona-pack and mechanics-pack IDs as
engine-owned, single-valued campaign facts. Before persona seeding or rule startup, every restart
SHALL read those facts and require an exact match with the resolved selection. Missing, partial,
ambiguous, or mismatched provenance SHALL be a boot refusal. A campaign created before these facts
exist SHALL require explicit migration; boot SHALL NOT infer or backfill its experience selection.

#### Scenario: Restart uses the recorded experience
- **WHEN** a campaign restarts with the same persona and mechanics selection recorded at creation
- **THEN** boot accepts the match without re-importing or changing the durable provenance

#### Scenario: Changed selection is refused before seeding
- **WHEN** a restart resolves either pack ID differently from the campaign's recorded provenance
- **THEN** boot fails before persona seeding or rule startup and names the recorded and requested IDs

#### Scenario: A pre-provenance campaign requires migration
- **WHEN** an existing campaign carries no complete experience provenance
- **THEN** boot refuses with an explicit migration diagnosis rather than guessing from current files,
  defaults, or process-local state

### Requirement: Selected world reactions cannot own engine orchestration
Boot SHALL seed only the selected persona records and SHALL combine only the selected, preflighted
world-rule definitions with the fixed engine-owned turn-sequencing definitions in the existing rule
processor. Rule IDs SHALL be unique across the combined set. Package rules SHALL remain unable to
read or write engine turn state, publish to stage, persona, model, approval, or tool-result subjects,
write protected engine buckets, select models, exceed the package action iteration ceiling, or
select tool authority.

#### Scenario: Unselected package files have no runtime effect
- **WHEN** a package contains multiple valid persona and mechanics packs and one pair is selected
- **THEN** only that pair is seeded or loaded and no record or rule from another pack affects play

#### Scenario: A selected reaction cannot cross the package boundary
- **WHEN** a selected mechanics pack contains a rule that reaches a reserved engine lane or collides
  with a fixed engine rule ID
- **THEN** boot fails before the processor starts, naming the offending rule and boundary

### Requirement: Pack selection produces a material deterministic difference
The starter template SHALL provide at least two persona packs and two bounded world-reaction packs.
Running identical deterministic player/model input in two disjoint instances with different
selections SHALL preserve the same fixed turn-stage sequence while producing different selected
voice evidence and at least one different committed world fact attributable to the mechanics pack.

#### Scenario: Identical input demonstrates the configuration dial
- **WHEN** two instances of the unchanged starter template run the same deterministic turn with
  different persona and mechanics selections
- **THEN** both turns complete through the engine-owned sequence, their selected narrator evidence
  differs, and their authoritative world states differ exactly where the selected bounded reactions
  declare

#### Scenario: Restart cannot switch a living world implicitly
- **WHEN** an already materialized instance restarts with a different experience selection
- **THEN** boot refuses the mismatch rather than re-importing, reseeding, or silently changing the
  experience of the living world
