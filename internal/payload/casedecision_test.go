package payload_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
)

const (
	testCaseID     = "c360.semmachina.bellweather.maze.case.bellweather"
	testActorID    = "c360.semmachina.bellweather.maze.character.kit-finch"
	testTargetRef  = "c360.semmachina.bellweather.maze.character.suspect-one"
	testRevealRef  = "c360.semmachina.bellweather.maze.evidence.silver-thread"
	testCulpritRef = "c360.semmachina.bellweather.maze.character.culprit"
	testMethodRef  = "c360.semmachina.bellweather.maze.method.clockwork-wire"
	testMotiveRef  = "c360.semmachina.bellweather.maze.motive.inheritance"
)

func validCaseDecision() *payload.CaseDecision {
	d := &payload.CaseDecision{
		TurnID:     testTurnID,
		ActionID:   testActionID,
		CaseID:     testCaseID,
		ActorID:    testActorID,
		Kind:       payload.CaseDecisionQuestion,
		TargetRefs: []string{testTargetRef},
		RevealRefs: []string{testRevealRef},
	}
	d.DecisionID = payload.CaseDecisionID(d.TurnID, d.ActionID, d.CaseID, d.ActorID)
	return d
}

func validAccusationDecision() *payload.CaseDecision {
	d := validCaseDecision()
	d.Kind = payload.CaseDecisionAccuse
	d.CulpritRef = testCulpritRef
	d.MethodRef = testMethodRef
	d.MotiveRef = testMotiveRef
	return d
}

func TestCaseDecision_SchemaAndDeterministicIdentity(t *testing.T) {
	d := validCaseDecision()
	wantSchema := message.Type{Domain: payload.Domain, Category: "case_decision", Version: payload.SchemaVersion}
	if d.Schema() != wantSchema {
		t.Fatalf("Schema() = %v, want %v", d.Schema(), wantSchema)
	}
	if len(d.DecisionID) != 64 || strings.ToLower(d.DecisionID) != d.DecisionID {
		t.Fatalf("decision_id = %q, want lowercase 64-byte hex", d.DecisionID)
	}
	const fixedVector = "b17ebf125b73ac704a41051b9f562aeb5f001a5f3900baf54de447561c0a200a"
	if d.DecisionID != fixedVector {
		t.Fatalf("decision_id = %q, want fixed length-prefixed SHA-256 vector %q", d.DecisionID, fixedVector)
	}
	if payload.CaseDecisionID("ab", "c", "d", "e") == payload.CaseDecisionID("a", "bc", "d", "e") {
		t.Fatal("tuple member boundaries are not length framed")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}

	mutated := *d
	mutated.DecisionID = strings.Repeat("0", 64)
	if err := mutated.Validate(); err == nil || !strings.Contains(err.Error(), "decision_id") {
		t.Fatalf("mismatched deterministic decision_id accepted or unnamed: %v", err)
	}

	wrongPair := *d
	wrongPair.ActionID = "act-2"
	wrongPair.DecisionID = payload.CaseDecisionID(wrongPair.TurnID, wrongPair.ActionID, wrongPair.CaseID, wrongPair.ActorID)
	if err := wrongPair.Validate(); err == nil || !strings.Contains(err.Error(), "turn_id") {
		t.Fatalf("turn/action 1:1 mismatch accepted or unnamed: %v", err)
	}
}

func TestCaseDecision_ClosedKindAndReferenceBounds(t *testing.T) {
	for _, kind := range []payload.CaseDecisionKind{
		payload.CaseDecisionObserve,
		payload.CaseDecisionInvestigate,
		payload.CaseDecisionQuestion,
		payload.CaseDecisionShare,
		payload.CaseDecisionRequestHint,
		payload.CaseDecisionOther,
	} {
		t.Run(string(kind), func(t *testing.T) {
			d := validCaseDecision()
			d.Kind = kind
			if err := d.Validate(); err != nil {
				t.Fatalf("registered kind rejected: %v", err)
			}
		})
	}

	d := validCaseDecision()
	d.Kind = payload.CaseDecisionKind("search-ish")
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("open kind accepted or unnamed: %v", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*payload.CaseDecision)
		field  string
	}{
		{name: "nine targets", field: "target_refs", mutate: func(d *payload.CaseDecision) {
			d.TargetRefs = entityRefs("character", 9)
		}},
		{name: "thirteen reveals", field: "reveal_refs", mutate: func(d *payload.CaseDecision) {
			d.RevealRefs = entityRefs("evidence", 13)
		}},
		{name: "duplicate target", field: "target_refs", mutate: func(d *payload.CaseDecision) {
			d.TargetRefs = []string{testTargetRef, testTargetRef}
		}},
		{name: "duplicate reveal", field: "reveal_refs", mutate: func(d *payload.CaseDecision) {
			d.RevealRefs = []string{testRevealRef, testRevealRef}
		}},
		{name: "malformed target", field: "target_refs", mutate: func(d *payload.CaseDecision) {
			d.TargetRefs = []string{"not-an-entity-id"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := validCaseDecision()
			tc.mutate(decision)
			before := *decision
			before.TargetRefs = append([]string(nil), decision.TargetRefs...)
			before.RevealRefs = append([]string(nil), decision.RevealRefs...)
			err := decision.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("invalid %s accepted or unnamed: %v", tc.field, err)
			}
			if !reflect.DeepEqual(*decision, before) {
				t.Fatal("Validate mutated its receiver")
			}
		})
	}

	// A reference may occur once in each list: the lists have distinct meanings.
	crossList := validCaseDecision()
	crossList.RevealRefs = append(crossList.RevealRefs, testTargetRef)
	if err := crossList.Validate(); err != nil {
		t.Fatalf("cross-list duplicate rejected: %v", err)
	}
}

func TestCaseDecision_AccusationFieldsAreConditional(t *testing.T) {
	accuse := validAccusationDecision()
	if err := accuse.Validate(); err != nil {
		t.Fatalf("complete accusation rejected: %v", err)
	}

	for _, field := range []string{"culprit_ref", "method_ref", "motive_ref"} {
		t.Run("accuse requires "+field, func(t *testing.T) {
			d := *accuse
			switch field {
			case "culprit_ref":
				d.CulpritRef = ""
			case "method_ref":
				d.MethodRef = ""
			case "motive_ref":
				d.MotiveRef = ""
			}
			if err := d.Validate(); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("missing %s accepted or unnamed: %v", field, err)
			}
		})

		t.Run("non-accuse forbids "+field, func(t *testing.T) {
			d := validCaseDecision()
			switch field {
			case "culprit_ref":
				d.CulpritRef = testCulpritRef
			case "method_ref":
				d.MethodRef = testMethodRef
			case "motive_ref":
				d.MotiveRef = testMotiveRef
			}
			if err := d.Validate(); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("forbidden %s accepted or unnamed: %v", field, err)
			}
		})
	}
}

func TestCaseDecision_JSONIsAliasOnlyAndProductionDecoderRoundTripsEveryField(t *testing.T) {
	d := validAccusationDecision()

	body, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, wrapped := object["payload"]; wrapped {
		t.Fatal("CaseDecision.MarshalJSON wrapped a BaseMessage envelope")
	}
	if _, wrapped := object["type"]; wrapped {
		t.Fatal("CaseDecision.MarshalJSON wrote a message discriminator")
	}

	decoded := decode(t, publish(t, d))
	got, ok := decoded.(*payload.CaseDecision)
	if !ok {
		t.Fatalf("production decoder produced %T", decoded)
	}
	if !reflect.DeepEqual(got, d) {
		t.Fatalf("round trip changed fields:\n got %#v\nwant %#v", got, d)
	}
}

func entityRefs(kind string, count int) []string {
	refs := make([]string, count)
	for i := range count {
		refs[i] = "c360.semmachina.bellweather.maze." + kind + ".ref-" + string(rune('a'+i))
	}
	return refs
}
