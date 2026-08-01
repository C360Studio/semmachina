package vocabulary_test

import (
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestInterpretingPhase_SitsBetweenAcceptedAndAdjudicating(t *testing.T) {
	phases := vocabulary.TurnPhases()
	accepted := slices.Index(phases, vocabulary.PhaseAccepted)
	interpreting := slices.Index(phases, vocabulary.PhaseInterpreting)
	adjudicating := slices.Index(phases, vocabulary.PhaseAdjudicating)
	if accepted < 0 || interpreting != accepted+1 || adjudicating != interpreting+1 {
		t.Fatalf("turn phases = %v, want accepted → interpreting → adjudicating", phases)
	}
	if !vocabulary.PhaseFollows(vocabulary.PhaseAccepted, vocabulary.PhaseInterpreting) ||
		!vocabulary.PhaseFollows(vocabulary.PhaseInterpreting, vocabulary.PhaseAdjudicating) {
		t.Fatal("phase predecessor graph does not contain accepted → interpreting → adjudicating")
	}
	if vocabulary.PhaseFollows(vocabulary.PhaseAccepted, vocabulary.PhaseAdjudicating) {
		t.Fatal("accepted can still skip directly to adjudicating")
	}
	artifacts, known := vocabulary.StageArtifacts(vocabulary.PhaseInterpreting)
	if !known || !slices.Equal(artifacts, []vocabulary.Predicate{vocabulary.TurnCaseDecisionRef}) {
		t.Fatalf("interpreting artifacts = %v, known=%v", artifacts, known)
	}
	witness, ok := vocabulary.StageWitness(vocabulary.PhaseInterpreting)
	if !ok || witness != vocabulary.TurnCaseDecisionRef {
		t.Fatalf("interpreting witness = %s, ok=%v", witness, ok)
	}
}
