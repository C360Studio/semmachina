## ADDED Requirements

### Requirement: Every production graph writer declares projection ownership
Every SemMachina graph write SHALL use a typed mutation with an explicit projection contract and
group. The admitted inventory SHALL cover turn groups (`phase`, `case-decision`, `case-progress`,
`companion-trigger`, `companion-decision`, `verdict`, `roll`, `effects-marker`, `narration`,
`knowledge`, `accusation`, and `resume`); player `current-pointer` and `resolved-pointer`; campaign
`import-marker`; case lifecycle `receipt`; companion-bond `hint`; knowledge and revelation births;
effect-owned mutable world state; and one contract with declared groups per selected mechanics
pack. No generic catch-all contract SHALL infer authority from predicates or call stacks.

#### Scenario: An inventoried writer mutates its group
- **WHEN** a production writer submits complete desired values for a group admitted by its contract
- **THEN** the typed mutation may change only predicates owned by that group

#### Scenario: A writer has no admitted group
- **WHEN** a new writer, predicate family, or package rule attempts a graph mutation without an
  explicit admitted contract and group
- **THEN** validation fails before the write and requires the projection inventory to be reviewed

### Requirement: Reconciliation is complete and revision-aware
A typed birth SHALL create an entity with complete top-level birth triples and no triples embedded
in the entity envelope. The entity ID SHALL be the stable create `RequestID`, and every request-owned
copy of a birth triple SHALL carry that same value as `Context`; producer-owned input SHALL remain
unchanged. A reconcile SHALL start from an exact entity read carrying the authoritative KV revision.
Desired values SHALL be complete for the named group, and an explicit empty desired group SHALL
clear every predicate owned by that group.

On `revision_mismatch`, the domain writer SHALL reread authoritative state, recompute desired values,
and attempt only a still-valid transition within its bounded delivery policy. On `commit_unknown`,
the writer SHALL exact-read the group to prove convergence and SHALL NOT blindly retry or report
success. A missing relationship target SHALL remain absent and SHALL NOT materialize a stub entity.

#### Scenario: An empty desired group clears owned state
- **WHEN** a writer reconciles an admitted group with an explicitly empty complete desired value set
- **THEN** every predicate owned by that group is absent afterward and predicates owned by other
  groups are unchanged

#### Scenario: Create canonicalizes mutation identity without changing producer input
- **WHEN** birth triples arrive with mixed domain-provenance contexts
- **THEN** the mutation sends the entity ID as both `RequestID` and every copied triple `Context`,
  while the caller's entity and triples retain their original contexts

#### Scenario: A concurrent write changes the revision
- **WHEN** reconcile returns `revision_mismatch`
- **THEN** the writer rereads the exact entity and recomputes the transition instead of replaying a
  stale mutation unchanged

#### Scenario: Mutation outcome is unknown
- **WHEN** reconcile returns `commit_unknown`
- **THEN** an exact read must prove the desired group converged before success is recorded, and an
  unclassified outcome remains unresolved rather than being blindly retried

#### Scenario: A relationship names an entity without a birth record
- **WHEN** a graph read follows that relationship target
- **THEN** the target is reported missing and no queryable referential stub occupies its entity key
