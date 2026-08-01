package vocabulary_test

import (
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestEvidenceRevealEligibility_IsClosedAndRuleOpaque(t *testing.T) {
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.EvidenceRevealPhase,
		vocabulary.EvidenceRevealKindPredicate,
		vocabulary.EvidenceRevealTarget,
	} {
		if !slices.Contains(vocabulary.AllPredicates(), predicate) {
			t.Errorf("reveal-eligibility predicate %q is absent from the closed vocabulary", predicate)
		}
		if !slices.Contains(vocabulary.RuleOpaquePredicates(), predicate) {
			t.Errorf("reveal-eligibility predicate %q is rule-visible private authoring", predicate)
		}
	}

	for _, kind := range []vocabulary.EvidenceRevealKind{
		vocabulary.EvidenceRevealObserve,
		vocabulary.EvidenceRevealInvestigate,
	} {
		if !kind.Valid() {
			t.Errorf("authored reveal kind %q is not registered", kind)
		}
		if parsed, err := vocabulary.ParseEvidenceRevealKind(string(kind)); err != nil || parsed != kind {
			t.Errorf("ParseEvidenceRevealKind(%q) = %q, %v", kind, parsed, err)
		}
	}
	if _, err := vocabulary.ParseEvidenceRevealKind("whatever-the-model-said"); err == nil {
		t.Fatal("open-ended reveal kind was accepted into authored authorization state")
	}
	if _, err := vocabulary.ParseEvidenceRevealKind("question"); err == nil {
		t.Fatal("question was accepted as evidence eligibility; testimony authority comes only from authored beliefs")
	}
}
