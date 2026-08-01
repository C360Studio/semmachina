package content_test

import (
	"reflect"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
)

func contentCaseDecision(t *testing.T) *payload.CaseDecision {
	t.Helper()
	d := &payload.CaseDecision{
		TurnID:   "turn-act-1",
		ActionID: "act-1",
		CaseID:   compose(t, "case", "bellweather"),
		ActorID:  compose(t, "character", "rook"),
		Kind:     payload.CaseDecisionInvestigate,
		TargetRefs: []string{
			compose(t, "evidence", "silver-thread"),
		},
		RevealRefs: []string{
			compose(t, "evidence", "silver-thread"),
		},
	}
	d.DecisionID = payload.CaseDecisionID(d.TurnID, d.ActionID, d.CaseID, d.ActorID)
	return d
}

func TestStore_CaseDecisionRecordRoundTripUsesDeterministicTurnKey(t *testing.T) {
	store, backend := newTestStore(t)
	turnID := "turn-act-1"
	turnEntityID := compose(t, "turn", turnID)
	record := &payload.CaseDecisionRecord{
		TurnID: turnID, ActionID: "act-1",
		Status:   payload.CaseDecisionStatusDecision,
		Decision: contentCaseDecision(t),
	}

	ref, err := store.PutCaseDecisionRecord(t.Context(), turnEntityID, record)
	if err != nil {
		t.Fatalf("PutCaseDecisionRecord: %v", err)
	}
	if ref.Key != "turn/turn-act-1/case-decision" {
		t.Fatalf("key = %q, want deterministic turn case-decision slot", ref.Key)
	}
	got, err := store.GetCaseDecisionRecord(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetCaseDecisionRecord: %v", err)
	}
	if !reflect.DeepEqual(got, record) {
		t.Fatalf("round trip changed record:\n got %#v\nwant %#v", got, record)
	}

	// Duplicate delivery replaces the same logical artifact at the same key.
	again, err := store.PutCaseDecisionRecord(t.Context(), turnEntityID, record)
	if err != nil {
		t.Fatalf("duplicate PutCaseDecisionRecord: %v", err)
	}
	if again != ref || len(backend.objects) != 1 || backend.puts[ref.Key] != 2 {
		t.Fatalf("duplicate put did not converge: ref=%+v again=%+v objects=%d puts=%d",
			ref, again, len(backend.objects), backend.puts[ref.Key])
	}
}

func TestStore_InvalidCaseDecisionRecordWritesNothing(t *testing.T) {
	store, backend := newTestStore(t)
	record := &payload.CaseDecisionRecord{
		TurnID: "turn-act-1", ActionID: "act-1",
		Status: payload.CaseDecisionStatusDecision,
	}
	if _, err := store.PutCaseDecisionRecord(
		t.Context(), compose(t, "turn", record.TurnID), record,
	); err == nil {
		t.Fatal("invalid case decision record was stored")
	}
	if len(backend.objects) != 0 || len(backend.puts) != 0 {
		t.Fatalf("invalid record caused writes: objects=%d puts=%d", len(backend.objects), len(backend.puts))
	}
}
