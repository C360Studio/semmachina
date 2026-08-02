package content_test

import (
	"reflect"
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestStore_CaseProgressUsesDeterministicTurnSlot(t *testing.T) {
	store, backend := newTestStore(t)
	record := &content.CaseProgressRecord{
		TurnID: "turn-act-1", DecisionID: content.KnowledgeReceiptID("turn-act-1", "decision-1"),
		CaseID: compose(t, "case", "bellweather"), Status: content.CaseProgressEventRecorded,
		EventID: content.CaseProgressEventID("turn-act-1", content.KnowledgeReceiptID("turn-act-1", "decision-1"),
			vocabulary.CaseEventBodyObserved),
		EventKind: vocabulary.CaseEventBodyObserved,
	}
	turnEntityID := compose(t, "turn", record.TurnID)
	ref, err := store.PutCaseProgressRecord(t.Context(), turnEntityID, record)
	if err != nil {
		t.Fatalf("PutCaseProgressRecord: %v", err)
	}
	if ref.Key != "turn/turn-act-1/case-progress" {
		t.Fatalf("case progress key = %q", ref.Key)
	}
	got, err := store.GetCaseProgressRecord(t.Context(), ref)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("case progress round trip = %#v, %v", got, err)
	}
	again, err := store.PutCaseProgressRecord(t.Context(), turnEntityID, record)
	if err != nil || again != ref || len(backend.objects) != 1 {
		t.Fatalf("duplicate progress did not converge: ref=%v again=%v objects=%d err=%v",
			ref, again, len(backend.objects), err)
	}
}

func TestCaseProgressRecord_NotApplicableForbidsLifecycleEvent(t *testing.T) {
	record := &content.CaseProgressRecord{
		TurnID: "turn-act-1", Status: content.CaseProgressNotApplicable,
		EventKind: vocabulary.CaseEventBodyObserved,
	}
	if err := record.Validate(); err == nil {
		t.Fatal("not-applicable progress accepted a lifecycle event")
	}
}

func TestCaseProgressEventIDFramesItsTuple(t *testing.T) {
	one := content.CaseProgressEventID("ab", "c", vocabulary.CaseEventBodyObserved)
	two := content.CaseProgressEventID("a", "bc", vocabulary.CaseEventBodyObserved)
	if one == two || len(one) != 64 {
		t.Fatalf("event identities are not framed SHA-256 values: %q %q", one, two)
	}
}
