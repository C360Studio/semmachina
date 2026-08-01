package epistemic

import (
	"reflect"
	"testing"
)

func TestScopeCasekeeperAudience_DerivesSortedBeliefHolders(t *testing.T) {
	scope, err := NewScope("case-1", map[string][]string{
		"actor-z": {"belief-z"},
		"actor-a": {"belief-a"},
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	audience, applicable, err := scope.CasekeeperAudience("turn-act-1", "turn-entity")
	if err != nil {
		t.Fatalf("CasekeeperAudience: %v", err)
	}
	if !applicable || audience.purpose != PurposeCasekeeper || audience.caseID != "case-1" {
		t.Fatalf("audience = %+v, applicable=%v", audience, applicable)
	}
	if want := []string{"actor-a", "actor-z"}; !reflect.DeepEqual(audience.targetActorIDs, want) {
		t.Fatalf("targets = %v, want %v", audience.targetActorIDs, want)
	}
}

func TestScopeCasekeeperAudience_NonMysteryIsNotApplicable(t *testing.T) {
	scope, err := NewScope("", nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	audience, applicable, err := scope.CasekeeperAudience("turn-act-1", "turn-entity")
	if err != nil || applicable || audience.purpose != "" {
		t.Fatalf("non-mystery audience = %+v, applicable=%v, err=%v", audience, applicable, err)
	}
}

func TestScopeCasekeeperAudience_RefusesMoreThanEightBeliefHolders(t *testing.T) {
	beliefs := make(map[string][]string)
	for _, actor := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		beliefs[actor] = []string{"belief-" + actor}
	}
	scope, err := NewScope("case-1", beliefs)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if _, applicable, err := scope.CasekeeperAudience("turn-act-1", "turn-entity"); err == nil || applicable {
		t.Fatalf("nine-holder scope returned applicable=%v, err=%v", applicable, err)
	}
}
