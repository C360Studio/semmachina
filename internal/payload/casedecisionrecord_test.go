package payload_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestCaseDecisionRecord_StatusControlsDecisionPresence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record *payload.CaseDecisionRecord
		valid  bool
	}{
		{
			name: "real decision",
			record: &payload.CaseDecisionRecord{
				TurnID: testTurnID, ActionID: testActionID,
				Status:   payload.CaseDecisionStatusDecision,
				Decision: validCaseDecision(),
			},
			valid: true,
		},
		{
			name: "not applicable",
			record: &payload.CaseDecisionRecord{
				TurnID: testTurnID, ActionID: testActionID,
				Status: payload.CaseDecisionStatusNotApplicable,
			},
			valid: true,
		},
		{
			name: "decision missing",
			record: &payload.CaseDecisionRecord{
				TurnID: testTurnID, ActionID: testActionID,
				Status: payload.CaseDecisionStatusDecision,
			},
		},
		{
			name: "not applicable carries decision",
			record: &payload.CaseDecisionRecord{
				TurnID: testTurnID, ActionID: testActionID,
				Status:   payload.CaseDecisionStatusNotApplicable,
				Decision: validCaseDecision(),
			},
		},
		{
			name: "open status",
			record: &payload.CaseDecisionRecord{
				TurnID: testTurnID, ActionID: testActionID,
				Status: payload.CaseDecisionStatus("skipped"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := *tc.record
			err := tc.record.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() error = %v, valid=%v", err, tc.valid)
			}
			if !reflect.DeepEqual(*tc.record, before) {
				t.Fatal("Validate mutated its receiver")
			}
		})
	}
}

func TestCaseDecisionRecord_RequiresOneToOneRecordAndDecisionIdentity(t *testing.T) {
	for _, mutate := range []func(*payload.CaseDecisionRecord){
		func(r *payload.CaseDecisionRecord) { r.ActionID = "act-2" },
		func(r *payload.CaseDecisionRecord) { r.TurnID = payload.TurnIDForAction("act-2") },
		func(r *payload.CaseDecisionRecord) { r.Decision.ActionID = "act-2" },
	} {
		record := &payload.CaseDecisionRecord{
			TurnID: testTurnID, ActionID: testActionID,
			Status:   payload.CaseDecisionStatusDecision,
			Decision: validCaseDecision(),
		}
		mutate(record)
		if err := record.Validate(); err == nil {
			t.Fatal("mismatched record identity was accepted")
		}
	}
}

func TestCaseDecisionRecord_TriplesExposeOnlyReferenceAndRealDecisionKind(t *testing.T) {
	at := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	ref := "obj://TEST_CONTENT/turn/turn-act-1/case-decision"
	record := &payload.CaseDecisionRecord{
		TurnID: testTurnID, ActionID: testActionID,
		Status:   payload.CaseDecisionStatusDecision,
		Decision: validCaseDecision(),
	}
	triples, err := record.Triples(testTurnEntity, ref, "casekeeper-test", at)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != 2 {
		t.Fatalf("real record emitted %d triples, want ref and kind", len(triples))
	}
	got := map[string]any{}
	for _, triple := range triples {
		got[triple.Predicate] = triple.Object
		encoded := triple.Predicate + "=" + strings.TrimSpace(reflect.ValueOf(triple.Object).String())
		for _, private := range []string{testCaseID, testActorID, testTargetRef, testRevealRef} {
			if strings.Contains(encoded, private) {
				t.Fatalf("private identifier %q reached graph triple %s", private, encoded)
			}
		}
	}
	if got[vocabulary.TurnCaseDecisionRef.String()] != ref {
		t.Fatalf("case-decision ref = %v, want %q", got[vocabulary.TurnCaseDecisionRef.String()], ref)
	}
	if got[vocabulary.TurnCaseDecisionKind.String()] != string(payload.CaseDecisionQuestion) {
		t.Fatalf("case-decision kind = %v", got[vocabulary.TurnCaseDecisionKind.String()])
	}

	noOp := &payload.CaseDecisionRecord{
		TurnID: testTurnID, ActionID: testActionID,
		Status: payload.CaseDecisionStatusNotApplicable,
	}
	triples, err = noOp.Triples(testTurnEntity, ref, "casekeeper-test", at)
	if err != nil {
		t.Fatalf("no-op Triples: %v", err)
	}
	if len(triples) != 1 || triples[0].Predicate != vocabulary.TurnCaseDecisionRef.String() {
		t.Fatalf("no-op triples = %#v, want only case-decision ref", triples)
	}
}
