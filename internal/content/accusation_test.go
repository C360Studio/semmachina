package content_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
)

func contentAccusationResult(t *testing.T) *payload.AccusationResult {
	t.Helper()
	result := &payload.AccusationResult{
		TurnID: "turn-act-1", CaseID: compose(t, "case", "bellweather"),
		DecisionID: payload.CaseDecisionID("turn-act-1", "act-1",
			compose(t, "case", "bellweather"), compose(t, "character", "rook")),
		Outcome: payload.AccusationIncorrect,
	}
	result.ResultID = payload.AccusationResultID(result.TurnID, result.CaseID, result.DecisionID)
	return result
}

func TestStore_AccusationReadClassifiesWrongSlotMissingAndCorrupt(t *testing.T) {
	store, backend := newTestStore(t)
	wrong := content.Ref{Instance: backend.instance, Key: "turn/turn-act-1/case-decision"}
	if _, err := store.GetAccusationRecord(t.Context(), wrong); !errors.Is(err, content.ErrArtifactReference) {
		t.Fatalf("wrong-slot error = %v, want ErrArtifactReference", err)
	}
	missing := content.Ref{Instance: backend.instance, Key: "turn/turn-act-1/accusation"}
	if _, err := store.GetAccusationRecord(t.Context(), missing); !errors.Is(err, content.ErrArtifactNotFound) {
		t.Fatalf("missing error = %v, want ErrArtifactNotFound", err)
	}
	backend.objects[missing.Key] = []byte("not-json")
	if _, err := store.GetAccusationRecord(t.Context(), missing); !errors.Is(err, content.ErrArtifactCorrupt) {
		t.Fatalf("corrupt error = %v, want ErrArtifactCorrupt", err)
	}
}

func TestStore_AccusationRecordRoundTripUsesDeterministicTurnKey(t *testing.T) {
	store, backend := newTestStore(t)
	result := contentAccusationResult(t)
	record := &content.AccusationRecord{TurnID: result.TurnID, Status: content.AccusationResultRecorded, Result: result}
	turnEntityID := compose(t, "turn", result.TurnID)
	ref, err := store.PutAccusationRecord(t.Context(), turnEntityID, record)
	if err != nil {
		t.Fatalf("PutAccusationRecord: %v", err)
	}
	if ref.Key != "turn/turn-act-1/accusation" {
		t.Fatalf("key = %q", ref.Key)
	}
	got, err := store.GetAccusationRecord(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetAccusationRecord: %v", err)
	}
	if !reflect.DeepEqual(got, record) {
		t.Fatalf("round trip changed record: got %#v want %#v", got, record)
	}
	if _, err := store.PutAccusationRecord(t.Context(), turnEntityID, record); err != nil {
		t.Fatalf("orphan retry: %v", err)
	}
	if backend.puts[ref.Key] != 2 || len(backend.objects) != 1 {
		t.Fatalf("retry did not overwrite one deterministic slot: puts=%d objects=%d",
			backend.puts[ref.Key], len(backend.objects))
	}
}

func TestAccusationRecordNotApplicableIsACompleteBarrierArtifact(t *testing.T) {
	store, _ := newTestStore(t)
	record := &content.AccusationRecord{TurnID: "turn-act-1", Status: content.AccusationNotApplicable}
	ref, err := store.PutAccusationRecord(t.Context(), compose(t, "turn", record.TurnID), record)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAccusationRecord(t.Context(), ref)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("not-applicable round trip = %#v, %v", got, err)
	}
}
