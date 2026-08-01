package content_test

import (
	"reflect"
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestStore_KnowledgeReceiptUsesTheTurnKnowledgeSlot(t *testing.T) {
	store, backend := newTestStore(t)
	receipt := &content.KnowledgeReceipt{
		TurnID: "turn-act-1", DecisionID: "decision-1", Status: content.KnowledgeCommitted,
		Entries: []content.KnowledgeReceiptEntry{{
			RecipientID:  compose(t, "character", "rowan"),
			EvidenceID:   compose(t, "evidence", "wire"),
			KnowledgeID:  compose(t, "knowledge", "grant-1"),
			RevelationID: compose(t, "revelation", "receipt-1"),
		}},
	}
	ref, err := store.PutKnowledgeReceipt(t.Context(), compose(t, "turn", receipt.TurnID), receipt)
	if err != nil {
		t.Fatalf("PutKnowledgeReceipt: %v", err)
	}
	if ref.Key != "turn/turn-act-1/knowledge" {
		t.Fatalf("knowledge key = %q", ref.Key)
	}
	got, err := store.GetKnowledgeReceipt(t.Context(), ref)
	if err != nil || !reflect.DeepEqual(got, receipt) {
		t.Fatalf("knowledge round trip = %#v, %v", got, err)
	}
	again, err := store.PutKnowledgeReceipt(t.Context(), compose(t, "turn", receipt.TurnID), receipt)
	if err != nil || again != ref || len(backend.objects) != 1 {
		t.Fatalf("duplicate receipt did not converge: ref=%v again=%v objects=%d err=%v",
			ref, again, len(backend.objects), err)
	}
}

func TestStore_TestimonyUsesDeterministicRevelationIdentityAndCarriesNoTruth(t *testing.T) {
	store, _ := newTestStore(t)
	testimony := &content.Testimony{
		TurnID: "turn-act-1", DecisionID: "decision-1",
		BeliefID:      compose(t, "belief", "judith-wire"),
		SourceActorID: compose(t, "character", "judith"), RecipientID: compose(t, "character", "rowan"),
		EvidenceID: compose(t, "evidence", "wire"), Stance: vocabulary.BeliefDenies,
		Prose: "I never touched that wire.",
	}
	instance := content.TestimonyID(testimony.TurnID, testimony.DecisionID, testimony.BeliefID,
		testimony.RecipientID, testimony.EvidenceID)
	ref, err := store.PutTestimony(t.Context(), instance, testimony)
	if err != nil {
		t.Fatalf("PutTestimony: %v", err)
	}
	if ref.Key != "revelation/"+instance+"/testimony" {
		t.Fatalf("testimony key = %q", ref.Key)
	}
	got, err := store.GetTestimony(t.Context(), ref)
	if err != nil || !reflect.DeepEqual(got, testimony) {
		t.Fatalf("testimony round trip = %#v, %v", got, err)
	}
}

func TestKnowledgeReceipt_RejectsUnsortedDuplicateOrOversizedEntriesBeforeWrite(t *testing.T) {
	store, backend := newTestStore(t)
	entry := content.KnowledgeReceiptEntry{
		RecipientID: compose(t, "character", "rowan"), EvidenceID: compose(t, "evidence", "wire"),
		KnowledgeID: compose(t, "knowledge", "grant-1"), RevelationID: compose(t, "revelation", "receipt-1"),
	}
	for name, entries := range map[string][]content.KnowledgeReceiptEntry{
		"duplicate": {entry, entry},
		"over cap":  make([]content.KnowledgeReceiptEntry, content.MaxKnowledgeReceiptEntries+1),
	} {
		t.Run(name, func(t *testing.T) {
			receipt := &content.KnowledgeReceipt{
				TurnID: "turn-act-1", DecisionID: "decision-1", Status: content.KnowledgeCommitted, Entries: entries,
			}
			if _, err := store.PutKnowledgeReceipt(t.Context(), compose(t, "turn", receipt.TurnID), receipt); err == nil {
				t.Fatal("invalid knowledge receipt was stored")
			}
		})
	}
	if len(backend.objects) != 0 {
		t.Fatalf("invalid receipts wrote %d objects", len(backend.objects))
	}
}
