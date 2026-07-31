# narration Specification

## Purpose
TBD - created by archiving change turn-loop-vertical-slice. Update Purpose after archive.
## Requirements
### Requirement: Narrator voices committed outcomes only
The narrator persona SHALL run only after effect application resolves, SHALL read the
verdict, roll result, applied (or rejected) effects, and current scene facts assembled at
execution time, and SHALL narrate the outcome that actually occurred. The narrator SHALL
have no tool capable of world mutation.

#### Scenario: Narration reflects the committed band
- **WHEN** a turn's roll selected the `partial` band and its effects committed
- **THEN** the narration voices a partial success consistent with the applied effects,
  and no world state differs before and after the narration stage

#### Scenario: Narrator cannot mutate
- **WHEN** the narrator persona executes
- **THEN** its available tools contain no mutation-capable tool, and the turn's applied
  effects are identical before and after narration

### Requirement: Prose to ObjectStore with reference triple
Narration prose SHALL be written to ObjectStore first; a reference triple on the turn
entity SHALL be committed only after the prose write succeeds. Prose SHALL never be
inlined into triples, rule payloads, or KV values.

#### Scenario: Write ordering
- **WHEN** the process crashes between the prose write and the reference commit
- **THEN** after restart the turn resumes narration, and a reference triple never points
  at a missing object (an unreferenced orphan object is acceptable)

### Requirement: Closed exit contract
The narrator's terminal tool SHALL emit only: the prose ObjectStore reference and
rule-opaque metadata. Any rule-matched field in the narrator's exit SHALL take values
only from the registered vocabulary.

#### Scenario: Narration completes the turn
- **WHEN** the narrator's terminal tool exits successfully
- **THEN** the turn entity carries the prose reference and enters phase `complete`
