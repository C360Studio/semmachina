package vocabulary

import (
	"fmt"
	"slices"

	ssvocab "github.com/c360studio/semstreams/vocabulary"
)

// ruleOpaquePredicates are predicates rules must never use as conditions.
//
// There are two reasons for membership. WorldEntityName and
// WorldEntityDescription carry author-written fiction, which rules may not
// parse. The mystery solution, evidence classification, and targeted belief
// predicates are structurally private authored facts: their objects are IDs or
// closed values, but branching on them would bypass epistemic projection and
// reveal private truth. RuleOpaque is therefore an authorization boundary as
// well as the fiction boundary.
//
// Storage-reference predicates are deliberately not opaque merely because
// they end in `*.ref`. A storage pointer tells a rule that a stage finished and
// nothing about the content behind it. Turn sequencing closes a turn by
// matching turn.narration.ref, so classifying all references as opaque would
// fail the engine's own rule pack. Mystery solution and belief references are
// opaque for their private meaning, not their wire shape.
// TestRegisterPredicates_LeavesEveryStorageReferenceRuleMatchable holds that
// line from the other side.
//
// What the flag buys is M1 enforced by the SUBSTRATE. Upstream's rule-config
// validator rejects any rule whose condition field names a rule-opaque
// predicate, on both the file-load and the hot-reload path, so neither prose
// nor structurally private authored facts can become rule conditions.
var ruleOpaquePredicates = []Predicate{
	// The display name a persona speaks. Author-written text, and a rule
	// branching on a character's name would be a rule reading the fiction.
	WorldEntityName,
	// Author-written flavor text, fed to personas verbatim.
	WorldEntityDescription,
	// Authored canonical truth and private belief state are never legitimate
	// rule conditions. Structural revelation and knowledge references remain
	// matchable for the authorization workflows that commit them.
	CaseSolutionCulprit,
	CaseSolutionMethod,
	CaseSolutionMotive,
	EvidenceTruthStatusCurrent,
	EvidenceRevealPhase,
	EvidenceRevealKindPredicate,
	EvidenceRevealTarget,
	BeliefActorHolder,
	BeliefEvidenceRef,
	BeliefStanceCurrent,
}

var immutablePredicates = []Predicate{
	CaseSolutionCulprit,
	CaseSolutionMethod,
	CaseSolutionMotive,
	EvidenceTruthStatusCurrent,
}

// ImmutablePredicates returns canonical mystery truth predicates that only a
// fully validated package seed may author.
func ImmutablePredicates() []Predicate { return slices.Clone(immutablePredicates) }

// IsProtectedPredicate reports whether p is immutable authored truth.
func IsProtectedPredicate(p Predicate) bool { return slices.Contains(immutablePredicates, p) }

// RuleOpaquePredicates returns prose-bearing and structurally private authored
// predicates that may never appear in a rule condition.
func RuleOpaquePredicates() []Predicate { return slices.Clone(ruleOpaquePredicates) }

// RegisterPredicates declares every predicate this engine writes in the
// semstreams vocabulary registry.
//
// It is a HARD PREREQUISITE for the turn-sequencing rule pack, not a nicety.
// Rule-config validation calls vocabulary.RequireDeclaredPredicate on every
// condition field and on every triple-mutating action's predicate, and that
// check is a registry lookup on top of the syntax parse: `turn.phase.current`
// parses perfectly and still fails rule load until it has been declared here.
// Nothing warns; the pack simply does not start.
//
// It must be called ONCE, EARLY, in every binary and every test that loads rule
// configuration — the same bootstrap position payload.RegisterPayloads occupies.
// The registry it writes is PROCESS-GLOBAL, which has two consequences worth
// stating where they can be seen: there is exactly one exported entry point so
// no caller has to know the per-predicate calls, and a test that touches the
// registry cannot be t.Parallel.
//
// RegisterPredicate rather than Register, deliberately. Register AMENDS — it
// starts from whatever is already there and keeps any field the new call omits
// — so a predicate somebody else had declared rule-matchable would stay
// rule-matchable. RegisterPredicate OVERWRITES, which makes this function's
// output total: after it returns, the metadata for every engine predicate is
// exactly what this file says, regardless of what ran before it.
//
// Only Name and RuleOpaque are populated. The rest of PredicateMetadata is
// upstream's semantic-web and ranking surface (IRIs, alias roles, weights,
// units) and none of it has a consumer here; the authoritative description of
// what each predicate MEANS is the doc comment on its constant, and copying
// those into registry strings would create a second place for them to disagree.
//
// Calling it repeatedly is safe and converges: every call writes the same
// metadata over whatever it finds.
func RegisterPredicates() error {
	opaque := make(map[Predicate]bool, len(ruleOpaquePredicates))
	for _, predicate := range ruleOpaquePredicates {
		if !slices.Contains(allPredicates, predicate) {
			return fmt.Errorf(
				"predicate %q is marked rule-opaque but is not one this engine writes; a flag on a predicate "+
					"nothing produces protects nothing", predicate)
		}
		opaque[predicate] = true
	}

	for _, predicate := range allPredicates {
		// Validated before the call rather than after, because
		// RegisterPredicate PANICS on a predicate the write gate would refuse.
		// A returned error names the offending constant; a panic names a
		// framework function and a stack.
		if err := ValidatePredicate(predicate); err != nil {
			return fmt.Errorf("cannot declare predicate %q: %w", predicate, err)
		}
		ssvocab.RegisterPredicate(ssvocab.PredicateMetadata{
			Name:       predicate.String(),
			RuleOpaque: opaque[predicate],
		})
	}
	return nil
}
