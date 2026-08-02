# epistemic-projection Specification

## Purpose
Define purpose-scoped projections that separate canonical truth, belief, actor knowledge, and
committed revelation before state reaches personas, model requests, or players.
## Requirements
### Requirement: Truth, belief, knowledge, and revelation are distinct
The graph SHALL represent canonical case truth, a named character's belief, an actor's
granted knowledge, and a turn's committed narration revelation with distinct predicates.
Granting or narrating a claim SHALL NOT change its canonical truth status.

#### Scenario: False testimony remains attributed belief
- **WHEN** questioning a suspect reveals that suspect's partial or false account
- **THEN** the player receives attributed testimony without its truth status, and canonical
  truth remains unchanged

#### Scenario: Revelation is not global knowledge
- **WHEN** a turn reveals a clue to one player
- **THEN** a revelation receipt and that player's knowledge grant are recorded without
  granting the clue to every actor

### Requirement: One audience selector controls persona context
Every persona request SHALL obtain graph context through one centralized projector using
an authenticated audience and closed purpose. The projector SHALL expose only:

- casekeeper: case truth, eligible hidden evidence, targeted beliefs, scene, and action;
- public adjudicator: public scene facts and the acting actor's granted knowledge, excluding
  canonical private truth, targeted private beliefs, and every other actor's knowledge;
- player narration: public facts, that player's knowledge, and committed revelation;
- companion: public facts and that companion actor's knowledge;
- accusation verifier: canonical solution IDs only;
- denouement narrator: canonical solution only after correct verification; and
- operator reads: the full graph through authorized operator surfaces.

Unauthorized entities and their identifiers SHALL be omitted rather than rendered as
excluded or stub records.

#### Scenario: Hidden canaries do not cross a persona boundary
- **WHEN** culprit and unrevealed-clue canaries whose entity IDs and text values are all
  unique exist in case state before denouement
- **THEN** they are absent from player, companion, public adjudicator, and narrator
  projection bytes, prompt bytes, and model requests

#### Scenario: Public adjudicator receives only acting-actor knowledge
- **WHEN** the acting actor, another actor, and a targeted suspect each hold distinct
  private records
- **THEN** the public-adjudicator projection contains the acting actor's granted knowledge
  but excludes the other actor's knowledge, the suspect's private belief, and canonical
  private truth

#### Scenario: Revealed clue proves filtering is not vacuous
- **WHEN** a clue is granted to the player but not the companion
- **THEN** it appears in the player's projection and remains absent from the companion's
  projection

### Requirement: Knowledge grants are authorized per actor
`KnowledgeGranter` SHALL treat every `CaseDecision.reveal_refs` entry as an untrusted
proposal. It SHALL reject solution or denouement-only material before denouement, evidence
outside the decision's eligible casekeeper projection, a grant to the wrong actor, and
belief testimony when the named believer was not the questioned target.

#### Scenario: Illegal reveal proposal is rejected
- **WHEN** a casekeeper proposes an unrevealed solution fact that is not eligible for the
  current decision
- **THEN** no knowledge or revelation record is written and the rejection names a closed
  authorization reason

#### Scenario: Shared discovery can grant two witnesses
- **WHEN** the player and companion both witness an eligible discovery
- **THEN** separate actor-scoped grants may be committed for both witnesses

#### Scenario: Explicit sharing grants companion knowledge
- **WHEN** a valid `share` decision names a player-known clue and the bonded companion
- **THEN** the clue is granted to that companion exactly once without changing truth or
  other actors' knowledge

### Requirement: Secret safety is enforced through model and egress boundaries
Acceptance tests SHALL inspect serialized persona prompts, actual mock-model request bytes,
and player egress in addition to projector return values. Unauthorized canonical truth or
unrevealed evidence SHALL NOT appear at any boundary before its purpose is authorized.

#### Scenario: Full E2E preserves secret canaries
- **WHEN** the Bellweather mock-model E2E runs with unique secret entity IDs and text values
  from cold open through a correct accusation
- **THEN** every unauthorized pre-denouement boundary excludes the secret canaries, while
  authorized discoveries and the final denouement include their intended facts
