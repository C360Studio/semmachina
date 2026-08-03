package vocabulary_test

import (
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestAuthorizationReasonsAreClosedAndParseable(t *testing.T) {
	want := []vocabulary.AuthorizationReason{
		vocabulary.AuthorizationWrongTurn,
		vocabulary.AuthorizationWrongCase,
		vocabulary.AuthorizationWrongActor,
		vocabulary.AuthorizationInvalidTarget,
		vocabulary.AuthorizationIneligibleReveal,
		vocabulary.AuthorizationIneligiblePhase,
		vocabulary.AuthorizationSolutionLocked,
		vocabulary.AuthorizationQuestionTargetMismatch,
		vocabulary.AuthorizationShareSourceUnknown,
		vocabulary.AuthorizationShareTargetUnauthorized,
		vocabulary.AuthorizationWitnessUnauthorized,
		vocabulary.AuthorizationUnsupportedKind,
	}
	if got := vocabulary.AuthorizationReasons(); !slices.Equal(got, want) {
		t.Fatalf("AuthorizationReasons() = %v, want %v", got, want)
	}
	for _, reason := range want {
		parsed, err := vocabulary.ParseAuthorizationReason(string(reason))
		if err != nil || parsed != reason {
			t.Fatalf("ParseAuthorizationReason(%q) = %q, %v", reason, parsed, err)
		}
	}
	if _, err := vocabulary.ParseAuthorizationReason("actor API key invalid"); err == nil {
		t.Fatal("ParseAuthorizationReason accepted open error text")
	}
}
