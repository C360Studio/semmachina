package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testCase     = "c360.semmachina.test.bellweather.case.main"
	testActor    = "c360.semmachina.test.bellweather.character.rowan"
	testTarget   = "c360.semmachina.test.bellweather.scene.green"
	testEvidence = "c360.semmachina.test.bellweather.evidence.wire"
	testSuspect  = "c360.semmachina.test.bellweather.character.judith"
)

type fakeShareAuthorizer struct {
	allowed bool
	err     error
}

type fakeWitnessAuthorizer struct {
	witness string
	allowed bool
	err     error
}

func (f fakeWitnessAuthorizer) Witness(context.Context, string, []string) (string, bool, error) {
	return f.witness, f.allowed, f.err
}

func (f fakeShareAuthorizer) Authorized(context.Context, string, string, string) (bool, error) {
	return f.allowed, f.err
}

func validPreflight() Preflight {
	decision := &payload.CaseDecision{
		TurnID: "turn-act-1", ActionID: "act-1", CaseID: testCase, ActorID: testActor,
		Kind: payload.CaseDecisionInvestigate, TargetRefs: []string{testTarget},
		RevealRefs: []string{testEvidence},
	}
	decision.DecisionID = payload.CaseDecisionID(
		decision.TurnID, decision.ActionID, decision.CaseID, decision.ActorID)
	return Preflight{
		Decision: decision, ActingActorID: testActor, CaseID: testCase,
		CasePhase:      vocabulary.CasePhaseInvestigation,
		CaseEvidence:   map[string]bool{testEvidence: true},
		CaseCharacters: map[string]bool{testSuspect: true},
		Eligibility: map[string]eligibility{testEvidence: {
			MinimumPhase: vocabulary.CasePhaseDiscovery,
			Kinds:        []vocabulary.EvidenceRevealKind{vocabulary.EvidenceRevealInvestigate},
			Targets:      []string{testTarget},
		}},
		Beliefs: map[beliefKey]AuthoredBelief{}, Known: map[knowledgeKey]bool{},
	}
}

func TestAuthorize_EligibleInvestigationGrantsOnlyTheActingActor(t *testing.T) {
	plan, err := Authorize(t.Context(), validPreflight(), fakeShareAuthorizer{})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].RecipientID != testActor ||
		plan.Entries[0].EvidenceID != testEvidence || plan.Entries[0].Testimony != nil {
		t.Fatalf("grant plan = %#v", plan)
	}
}

func TestAuthorize_WitnessedDiscoveryPlansIndependentPlayerAndCompanionGrants(t *testing.T) {
	plan, err := AuthorizeWithWitnesses(t.Context(), validPreflight(), fakeShareAuthorizer{},
		fakeWitnessAuthorizer{witness: testSuspect, allowed: true})
	if err != nil {
		t.Fatalf("AuthorizeWithWitnesses: %v", err)
	}
	if len(plan.Entries) != 2 || plan.Entries[0].RecipientID != testActor ||
		plan.Entries[1].RecipientID != testSuspect || plan.Entries[1].EvidenceID != testEvidence {
		t.Fatalf("witness plan = %#v", plan)
	}
	if _, err := AuthorizeWithWitnesses(t.Context(), validPreflight(), fakeShareAuthorizer{},
		fakeWitnessAuthorizer{err: errors.New("graph unavailable")}); err == nil {
		t.Fatal("transient witness authorization error was converted into a player-only grant")
	}
}

func TestAuthorize_RejectsWholeBatchBeforeWritesWithClosedReasons(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Preflight)
		want   AuthorizationReason
	}{
		"wrong case":      {func(p *Preflight) { p.Decision.CaseID = testSuspect }, ReasonWrongCase},
		"wrong actor":     {func(p *Preflight) { p.Decision.ActorID = testSuspect }, ReasonWrongActor},
		"invalid target":  {func(p *Preflight) { p.Decision.TargetRefs = []string{testSuspect} }, ReasonInvalidTarget},
		"outside case":    {func(p *Preflight) { p.CaseEvidence = map[string]bool{} }, ReasonIneligibleReveal},
		"premature phase": {func(p *Preflight) { p.CasePhase = vocabulary.CasePhaseColdOpen }, ReasonIneligiblePhase},
		"solution locked": {func(p *Preflight) { p.SolutionLocked = map[string]bool{testEvidence: true} }, ReasonSolutionLocked},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			input := validPreflight()
			tc.mutate(&input)
			_, err := Authorize(t.Context(), input, fakeShareAuthorizer{})
			var rejection *AuthorizationError
			if !errors.As(err, &rejection) || rejection.Reason != tc.want {
				t.Fatalf("Authorize error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAuthorize_QuestionStoresAttributedFalseTestimonyWithoutTruth(t *testing.T) {
	input := validPreflight()
	input.Decision.Kind = payload.CaseDecisionQuestion
	input.Decision.TargetRefs = []string{testSuspect}
	input.Beliefs[beliefKey{ActorID: testSuspect, EvidenceID: testEvidence}] = AuthoredBelief{
		ID: "c360.semmachina.test.bellweather.belief.judith-wire", HolderID: testSuspect,
		EvidenceID: testEvidence, Stance: vocabulary.BeliefDenies, Prose: "I never touched that wire.",
	}

	plan, err := Authorize(t.Context(), input, fakeShareAuthorizer{})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Testimony == nil {
		t.Fatalf("question plan = %#v", plan)
	}
	got := plan.Entries[0].Testimony
	if got.SourceActorID != testSuspect || got.Stance != vocabulary.BeliefDenies || got.Prose == "" {
		t.Fatalf("testimony attribution = %#v", got)
	}
}

func TestAuthorize_QuestionAndShareRefuseClosedTargetFailures(t *testing.T) {
	question := validPreflight()
	question.Decision.Kind = payload.CaseDecisionQuestion
	question.Decision.TargetRefs = []string{testSuspect}
	if _, err := Authorize(t.Context(), question, fakeShareAuthorizer{}); !IsReason(err, ReasonQuestionTargetMismatch) {
		t.Fatalf("question without matching belief = %v", err)
	}

	share := validPreflight()
	share.Decision.Kind = payload.CaseDecisionShare
	share.Decision.TargetRefs = []string{testSuspect}
	if _, err := Authorize(t.Context(), share, fakeShareAuthorizer{}); !IsReason(err, ReasonShareSourceUnknown) {
		t.Fatalf("unknown share source = %v", err)
	}
	share.Known[knowledgeKey{ActorID: testActor, EvidenceID: testEvidence}] = true
	if _, err := Authorize(t.Context(), share, fakeShareAuthorizer{}); !IsReason(err, ReasonShareTargetUnauthorized) {
		t.Fatalf("production-denied share target = %v", err)
	}
	if _, err := Authorize(t.Context(), share, fakeShareAuthorizer{allowed: true}); err != nil {
		t.Fatalf("fake-authorized share seam: %v", err)
	}
	if _, err := Authorize(t.Context(), share, fakeShareAuthorizer{err: errors.New("lookup")}); err == nil {
		t.Fatal("transient share-authorizer error was converted into a permanent refusal")
	}
}

func TestAuthorize_NoRevealKindsCommitAnEmptyPlan(t *testing.T) {
	for _, kind := range []payload.CaseDecisionKind{
		payload.CaseDecisionRequestHint, payload.CaseDecisionAccuse, payload.CaseDecisionOther,
	} {
		input := validPreflight()
		input.Decision.Kind = kind
		input.Decision.RevealRefs = nil
		input.Decision.TargetRefs = nil
		plan, err := Authorize(t.Context(), input, fakeShareAuthorizer{})
		if err != nil || len(plan.Entries) != 0 {
			t.Fatalf("kind %s: plan=%#v err=%v", kind, plan, err)
		}
	}
}
