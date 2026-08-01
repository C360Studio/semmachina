package caseflow_test

import (
	"reflect"
	"testing"

	"github.com/c360studio/semstreams/pkg/lifecycle"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestCaseStateImplementsParticipantAndWorkflowIsExact(t *testing.T) {
	var _ lifecycle.Participant = (*caseflow.CaseState)(nil)

	state := &caseflow.CaseState{ID: "acme.semmachina.bellweather.campaign.case.case-bellweather"}
	if got, want := state.Workflow(), caseflow.WorkflowName; got != want {
		t.Fatalf("Workflow() = %q, want %q", got, want)
	}

	workflow := caseflow.Workflow()
	if got, want := workflow.EntityIDPattern, "*.semmachina.*.*.case.*"; got != want {
		t.Fatalf("EntityIDPattern = %q, want %q", got, want)
	}
	if len(workflow.OperatorWritablePredicates) != 0 {
		t.Fatalf("operator writable predicates = %v, want none", workflow.OperatorWritablePredicates)
	}
	wantTransitions := lifecycle.Transitions{
		string(vocabulary.CasePhaseColdOpen):      {string(vocabulary.CasePhaseDiscovery)},
		string(vocabulary.CasePhaseDiscovery):     {string(vocabulary.CasePhaseInvestigation)},
		string(vocabulary.CasePhaseInvestigation): {string(vocabulary.CasePhaseAccusation)},
		string(vocabulary.CasePhaseAccusation):    {string(vocabulary.CasePhaseDenouement)},
		string(vocabulary.CasePhaseDenouement):    {},
	}
	if got := workflow.Transitions; !reflect.DeepEqual(got, wantTransitions) {
		t.Fatalf("transitions = %#v, want %#v", got, wantTransitions)
	}
}
