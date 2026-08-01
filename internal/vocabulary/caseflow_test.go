package vocabulary_test

import (
	"reflect"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestCaseLifecycleClosedSets(t *testing.T) {
	if got, want := vocabulary.CasePhases(), []vocabulary.CasePhase{
		vocabulary.CasePhaseColdOpen,
		vocabulary.CasePhaseDiscovery,
		vocabulary.CasePhaseInvestigation,
		vocabulary.CasePhaseAccusation,
		vocabulary.CasePhaseDenouement,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CasePhases() = %v, want %v", got, want)
	}
	if got, want := vocabulary.CaseLifecycleEventKinds(), []vocabulary.CaseLifecycleEventKind{
		vocabulary.CaseEventBodyObserved,
		vocabulary.CaseEventInvestigationStarted,
		vocabulary.CaseEventAccusationSubmitted,
		vocabulary.CaseEventAccusationCorrect,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CaseLifecycleEventKinds() = %v, want %v", got, want)
	}
}

func TestCaseLifecyclePredicatesAreDeclared(t *testing.T) {
	want := []vocabulary.Predicate{
		vocabulary.CaseLifecyclePhase,
		vocabulary.CaseLifecycleEventID,
		vocabulary.CaseLifecycleEventKindPredicate,
		vocabulary.CaseLifecycleFromPhase,
		vocabulary.CaseLifecycleToPhase,
		vocabulary.CaseTransitionSource,
		vocabulary.CaseTransitionAt,
		vocabulary.CaseTransitionFrom,
		vocabulary.CaseMemberVictim,
	}
	all := vocabulary.AllPredicates()
	for _, predicate := range want {
		if !contains(all, predicate) {
			t.Errorf("AllPredicates() omits %q", predicate)
		}
	}
}

func contains(values []vocabulary.Predicate, want vocabulary.Predicate) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
