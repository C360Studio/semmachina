package vocabulary_test

import (
	"slices"
	"testing"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestMysteryVocabulary_IsRegisteredAndTyped(t *testing.T) {
	want := []vocabulary.Predicate{
		vocabulary.CaseSolutionCulprit,
		vocabulary.CaseSolutionMethod,
		vocabulary.CaseSolutionMotive,
		vocabulary.CaseMemberSuspect,
		vocabulary.CaseMemberEvidence,
		vocabulary.CaseMemberTimeline,
		vocabulary.CaseTimelineOrder,
		vocabulary.EvidenceTruthStatusCurrent,
		vocabulary.BeliefActorHolder,
		vocabulary.BeliefEvidenceRef,
		vocabulary.BeliefStanceCurrent,
		vocabulary.KnowledgeActorHolder,
		vocabulary.KnowledgeEvidenceRef,
		vocabulary.RevelationEvidenceRef,
		vocabulary.CompanionCandidatePolicy,
		vocabulary.CompanionBondPlayer,
		vocabulary.CompanionBondCharacter,
		vocabulary.CompanionBondPolicy,
		vocabulary.CompanionBondHintLevel,
	}
	all := vocabulary.AllPredicates()
	for _, predicate := range want {
		if !slices.Contains(all, predicate) {
			t.Errorf("mystery predicate %q is not registered", predicate)
		}
		if err := vocabulary.ValidatePredicate(predicate); err != nil {
			t.Errorf("mystery predicate %q is not canonical: %v", predicate, err)
		}
	}

	for _, kind := range []vocabulary.EntityKind{
		vocabulary.EntityKindCase,
		vocabulary.EntityKindEvidence,
		vocabulary.EntityKindEvent,
		vocabulary.EntityKindBelief,
		vocabulary.EntityKindKnowledge,
		vocabulary.EntityKindRevelation,
		vocabulary.EntityKindCompanionBond,
	} {
		if !kind.Valid() {
			t.Errorf("mystery entity kind %q is not registered", kind)
		}
	}
}

func TestProtectedMysteryPredicates_AreImmutableAndRuleOpaque(t *testing.T) {
	protected := []vocabulary.Predicate{
		vocabulary.CaseSolutionCulprit,
		vocabulary.CaseSolutionMethod,
		vocabulary.CaseSolutionMotive,
		vocabulary.EvidenceTruthStatusCurrent,
	}
	if got := vocabulary.ImmutablePredicates(); !slices.Equal(got, protected) {
		t.Fatalf("ImmutablePredicates() = %v, want %v", got, protected)
	}
	for _, predicate := range protected {
		if !vocabulary.IsProtectedPredicate(predicate) {
			t.Errorf("%q is not protected", predicate)
		}
		if !slices.Contains(vocabulary.RuleOpaquePredicates(), predicate) {
			t.Errorf("%q is not rule-opaque", predicate)
		}
	}

	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}
	for _, predicate := range protected {
		if !ssvocab.IsRuleOpaque(predicate.String()) {
			t.Errorf("registered %q is rule-matchable", predicate)
		}
	}
}

func TestMysteryClosedValues_RejectUnknownValues(t *testing.T) {
	if _, err := vocabulary.ParseEvidenceTruthStatus("probably-true"); err == nil {
		t.Fatal("accepted an open evidence truth status")
	}
	if _, err := vocabulary.ParseBeliefStance("secretly-guilty"); err == nil {
		t.Fatal("accepted an open belief stance")
	}
	if _, err := vocabulary.ParseCompanionPolicy("do-whatever-the-model-wants"); err == nil {
		t.Fatal("accepted an open companion policy")
	}
	if _, err := vocabulary.ParseHintLevel("reveal-the-culprit"); err == nil {
		t.Fatal("accepted an open hint level")
	}
}
