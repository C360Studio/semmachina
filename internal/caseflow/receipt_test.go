package caseflow_test

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testCaseID = "acme.semmachina.bellweather.campaign.case.bellweather-case"

type receiptStore struct {
	state  *graph.EntityState
	writes [][]message.Triple
}

func (s *receiptStore) GetEntity(context.Context, string) (*graph.EntityState, error) {
	return s.state, nil
}
func (s *receiptStore) MergeTriples(_ context.Context, _ string, triples []message.Triple, _ ...graphio.MergeOption) (*graph.EntityState, error) {
	s.writes = append(s.writes, triples)
	for _, incoming := range triples {
		kept := s.state.Triples[:0]
		for _, existing := range s.state.Triples {
			if existing.Predicate != incoming.Predicate {
				kept = append(kept, existing)
			}
		}
		s.state.Triples = append(kept, incoming)
	}
	return s.state, nil
}

func caseAt(phase vocabulary.CasePhase) *receiptStore {
	return &receiptStore{state: &graph.EntityState{ID: testCaseID, Triples: []message.Triple{{
		Subject: testCaseID, Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(phase),
	}}}}
}

func TestRecordWritesFourFieldReceiptInOneReplaceUpdateAndNeverWritesPhase(t *testing.T) {
	store := caseAt(vocabulary.CasePhaseColdOpen)
	recorder, _ := caseflow.NewRecorder(store)
	outcome, err := recorder.Record(t.Context(), caseflow.TransitionRequest{
		CaseEntityID: testCaseID, EventID: "body-1", Kind: vocabulary.CaseEventBodyObserved,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !outcome.Recorded || len(store.writes) != 1 || len(store.writes[0]) != 4 {
		t.Fatalf("outcome=%+v writes=%v", outcome, store.writes)
	}
	for _, triple := range store.writes[0] {
		if triple.Predicate == vocabulary.CaseLifecyclePhase.String() {
			t.Fatal("receipt seam wrote the manager-owned phase predicate")
		}
	}
}

func TestRecordDuplicateAndStaleReceiptsAreNoOps(t *testing.T) {
	store := caseAt(vocabulary.CasePhaseDiscovery)
	store.state.Triples = append(store.state.Triples, message.Triple{
		Subject: testCaseID, Predicate: vocabulary.CaseLifecycleEventID.String(), Object: "body-1",
	})
	recorder, _ := caseflow.NewRecorder(store)
	for _, request := range []caseflow.TransitionRequest{
		{CaseEntityID: testCaseID, EventID: "body-1", Kind: vocabulary.CaseEventBodyObserved},
		{CaseEntityID: testCaseID, EventID: "body-redelivery", Kind: vocabulary.CaseEventBodyObserved},
	} {
		outcome, err := recorder.Record(t.Context(), request)
		if err != nil || outcome.Recorded {
			t.Fatalf("Record(%+v) = %+v, %v", request, outcome, err)
		}
	}
	if len(store.writes) != 0 {
		t.Fatalf("no-op receipts wrote %d times", len(store.writes))
	}
}

func TestRecordRejectsOutOfOrderReceipt(t *testing.T) {
	store := caseAt(vocabulary.CasePhaseColdOpen)
	recorder, _ := caseflow.NewRecorder(store)
	_, err := recorder.Record(t.Context(), caseflow.TransitionRequest{
		CaseEntityID: testCaseID, EventID: "accuse-1", Kind: vocabulary.CaseEventAccusationSubmitted,
	})
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("Record error = %v, want out of order", err)
	}
	if len(store.writes) != 0 {
		t.Fatal("out-of-order receipt wrote graph state")
	}
}
