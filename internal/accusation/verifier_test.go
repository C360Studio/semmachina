package accusation_test

import (
	"testing"

	"github.com/c360studio/semmachina/internal/accusation"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
)

const (
	caseID    = "c360.semmachina.bellweather.campaign.case.murder"
	actorID   = "c360.semmachina.bellweather.campaign.character.player"
	culpritID = "c360.semmachina.bellweather.campaign.character.culprit"
	methodID  = "c360.semmachina.bellweather.campaign.item.method"
	motiveID  = "c360.semmachina.bellweather.campaign.evidence.motive"
)

func accusationDecision(culprit, method, motive string) *payload.CaseDecision {
	d := &payload.CaseDecision{
		TurnID: "turn-action-1", ActionID: "action-1", CaseID: caseID, ActorID: actorID,
		Kind: payload.CaseDecisionAccuse, CulpritRef: culprit, MethodRef: method, MotiveRef: motive,
		TargetRefs: []string{}, RevealRefs: []string{},
	}
	d.DecisionID = payload.CaseDecisionID(d.TurnID, d.ActionID, d.CaseID, d.ActorID)
	return d
}

func TestVerifier_ExactIdentityComparisonHasOneNonRevealingWrongResult(t *testing.T) {
	solution := epistemic.Solution{Culprit: culpritID, Method: methodID, Motive: motiveID}
	cases := []struct {
		name                    string
		culprit, method, motive string
		want                    payload.AccusationOutcome
	}{
		{"wrong culprit", actorID, methodID, motiveID, payload.AccusationIncorrect},
		{"wrong method", culpritID, culpritID, motiveID, payload.AccusationIncorrect},
		{"wrong motive", culpritID, methodID, methodID, payload.AccusationIncorrect},
		{"mixed wrong", actorID, culpritID, methodID, payload.AccusationIncorrect},
		{"all wrong", actorID, culpritID, methodID, payload.AccusationIncorrect},
		{"correct", culpritID, methodID, motiveID, payload.AccusationCorrect},
	}
	var incorrect *payload.AccusationResult
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := accusationDecision(tc.culprit, tc.method, tc.motive)
			result, err := accusation.Verify(decision, solution)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if result.Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q", result.Outcome, tc.want)
			}
			if result.ResultID != payload.AccusationResultID(decision.TurnID, decision.CaseID, decision.DecisionID) {
				t.Fatalf("result id = %q", result.ResultID)
			}
			if tc.want == payload.AccusationIncorrect {
				if incorrect == nil {
					incorrect = result
				} else if *result != *incorrect {
					t.Fatalf("wrong answer disclosed a dimension: got %+v, canonical %+v", result, incorrect)
				}
			}
		})
	}
}

func TestVerifier_RejectsMalformedOrNonAccusationDecision(t *testing.T) {
	solution := epistemic.Solution{Culprit: culpritID, Method: methodID, Motive: motiveID}
	for _, mutate := range []func(*payload.CaseDecision){
		func(d *payload.CaseDecision) { d.Kind = payload.CaseDecisionQuestion },
		func(d *payload.CaseDecision) { d.DecisionID = "foreign" },
	} {
		decision := accusationDecision(culpritID, methodID, motiveID)
		mutate(decision)
		if _, err := accusation.Verify(decision, solution); err == nil {
			t.Fatal("Verify accepted malformed or non-accusation decision")
		}
	}
}
