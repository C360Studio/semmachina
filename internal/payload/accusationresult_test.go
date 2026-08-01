package payload_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
)

func validAccusationResult() *payload.AccusationResult {
	result := &payload.AccusationResult{
		TurnID: testTurnID,
		CaseID: "c360.semmachina.world1.starter.case.bellweather",
		DecisionID: payload.CaseDecisionID(testTurnID, testActionID,
			"c360.semmachina.world1.starter.case.bellweather", testCharacter),
		Outcome: payload.AccusationCorrect,
	}
	result.ResultID = payload.AccusationResultID(result.TurnID, result.CaseID, result.DecisionID)
	return result
}

func TestAccusationResult_SchemaIdentityAndClosedOutcome(t *testing.T) {
	result := validAccusationResult()
	if got := result.Schema().String(); got != "semmachina.accusation_result.v1" {
		t.Fatalf("schema = %q", got)
	}
	if len(result.ResultID) != 64 || result.ResultID != strings.ToLower(result.ResultID) {
		t.Fatalf("result_id = %q, want lowercase SHA-256", result.ResultID)
	}
	if payload.AccusationResultID("ab", "c", "d") == payload.AccusationResultID("a", "bc", "d") {
		t.Fatal("result identity does not frame tuple members")
	}
	if got := payload.AccusationOutcomes(); !reflect.DeepEqual(got,
		[]payload.AccusationOutcome{payload.AccusationCorrect, payload.AccusationIncorrect}) {
		t.Fatalf("outcomes = %v", got)
	}
	for _, outcome := range payload.AccusationOutcomes() {
		candidate := *result
		candidate.Outcome = outcome
		if err := candidate.Validate(); err != nil {
			t.Fatalf("Validate(%q): %v", outcome, err)
		}
	}
	bad := *result
	bad.Outcome = "partly-correct"
	if err := bad.Validate(); err == nil {
		t.Fatal("Validate accepted an open-vocabulary outcome")
	}
}

func TestAccusationResult_StrictValidationAndAliasJSON(t *testing.T) {
	valid := validAccusationResult()
	mutations := []struct {
		name string
		edit func(*payload.AccusationResult)
	}{
		{"result id", func(r *payload.AccusationResult) { r.ResultID = strings.Repeat("a", 64) }},
		{"turn id", func(r *payload.AccusationResult) { r.TurnID = "turn.bad" }},
		{"case id", func(r *payload.AccusationResult) { r.CaseID = "case" }},
		{"decision id", func(r *payload.AccusationResult) { r.DecisionID = "" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *valid
			tc.edit(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate accepted malformed result")
			}
		})
	}

	wire, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || fields["type"] != nil || fields["payload"] != nil {
		t.Fatalf("MarshalJSON emitted an envelope or extra fields: %s", wire)
	}
	decoded := decode(t, publish(t, valid))
	got, ok := decoded.(*payload.AccusationResult)
	if !ok || !reflect.DeepEqual(got, valid) {
		t.Fatalf("production decoder = %#v (%T), want %#v", decoded, decoded, valid)
	}
}
