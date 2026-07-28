// Package vocabulary is SemMachina's closed exit vocabulary: the single
// source of every value a persona is allowed to emit, a rule is allowed to
// match, and the effect applier is allowed to commit.
//
// The one design rule is "agentic judges fiction, rules match structure,
// components execute work". The LLM is the only layer allowed to read fiction
// and it must exit through structured triples drawn from a closed,
// deliberately rule-matchable vocabulary. This package IS that closed
// vocabulary. Three consumers share it without duplication:
//
//   - the adjudicator's terminal-tool schema (enumerations come from Sets())
//   - the effect applier's validation (membership, bounds, predicate mapping)
//   - the turn-sequencing rule pack (predicate identities and the scalar
//     values rules compare against)
//
// Two naming regimes coexist and must not be confused:
//
//   - Predicate identities are canonical three-segment lower-kebab
//     domain.category.property strings. semstreams enforces this fail-closed
//     at the ENTITY_STATES write gate (vocabulary.ParsePredicate), so an
//     underscore-bearing or two-segment predicate is REJECTED AT WRITE, never
//     silently stored. Every Predicate constant here is proven against the
//     upstream parser in TestPredicates_AcceptedByUpstreamParser.
//   - Payload/tool field values (effect types, phases, classes) are ordinary
//     JSON values where snake_case is legal and used where it reads best
//     (`set_attribute`), and kebab-case is used where the value also names a
//     predicate segment (`allied-with`).
package vocabulary
