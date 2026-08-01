package content_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func storedCompanionDecision(t *testing.T) *payload.CompanionDecision {
	t.Helper()
	d := &payload.CompanionDecision{
		TurnID: "turn-act-1", ContextRef: compose(t, "scene", "gatehouse"),
		PlayerID: compose(t, "player", "p1"), CompanionID: compose(t, "character", "wren"),
		Kind: payload.CompanionDecisionSilent, EvidenceRefs: []string{},
	}
	d.DecisionID = payload.CompanionDecisionID(d.TurnID, d.ContextRef, d.PlayerID, d.CompanionID)
	return d
}

func TestStore_ConcurrentDifferentCompanionDecisionsChooseOneExactResident(t *testing.T) {
	store, backend := newTestStore(t)
	otherStore, err := content.NewStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	first := storedCompanionDecision(t)
	second := *first
	second.Kind = payload.CompanionDecisionQuip
	turnEntityID := compose(t, "turn", first.TurnID)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	contenders := []struct {
		store    *content.Store
		decision *payload.CompanionDecision
	}{{store: store, decision: first}, {store: otherStore, decision: &second}}
	for _, contender := range contenders {
		workers.Add(1)
		go func(candidateStore *content.Store, candidate *payload.CompanionDecision) {
			defer workers.Done()
			<-start
			_, err := candidateStore.PutCompanionDecision(t.Context(), turnEntityID, candidate)
			errs <- err
		}(contender.store, contender.decision)
	}
	close(start)
	workers.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, content.ErrArtifactConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PutCompanionDecision error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results successes=%d conflicts=%d, want one atomic winner", successes, conflicts)
	}
	key, err := content.KeyFor(vocabulary.TurnCompanionDecisionRef, content.SubjectTurn, first.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	ref := content.Ref{Instance: backend.InstanceName(), Key: key}
	resident, err := store.GetCompanionDecision(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if resident.Kind != first.Kind && resident.Kind != second.Kind {
		t.Fatalf("resident kind = %q, want one complete contender", resident.Kind)
	}
	if backend.puts[ref.Key] != 1 {
		t.Fatalf("immutable slot was physically written %d times", backend.puts[ref.Key])
	}
}

func TestStore_CompanionDecisionExactResidentIdempotencyAndConflict(t *testing.T) {
	store, backend := newTestStore(t)
	decision := storedCompanionDecision(t)
	turnEntityID := compose(t, "turn", decision.TurnID)
	ref, err := store.PutCompanionDecision(t.Context(), turnEntityID, decision)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.PutCompanionDecision(t.Context(), turnEntityID, decision)
	if err != nil || again != ref {
		t.Fatalf("equal retry ref=%+v err=%v", again, err)
	}
	if backend.puts[ref.Key] != 1 {
		t.Fatalf("equal retry rewrote resident object %d times", backend.puts[ref.Key])
	}
	got, err := store.GetCompanionDecision(t.Context(), ref)
	if err != nil || !reflect.DeepEqual(got, decision) {
		t.Fatalf("round trip got=%#v err=%v", got, err)
	}

	collision := *decision
	collision.Kind = payload.CompanionDecisionQuip
	if _, err := store.PutCompanionDecision(t.Context(), turnEntityID, &collision); !errors.Is(err, content.ErrArtifactConflict) {
		t.Fatalf("semantic collision error = %v", err)
	}
	if backend.puts[ref.Key] != 1 {
		t.Fatal("semantic collision overwrote resident decision")
	}
}
