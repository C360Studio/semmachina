package payload_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testCompanionContext  = "c360.semmachina.world1.starter.scene.gatehouse"
	testCompanionPlayerID = "c360.semmachina.world1.starter.player.p1"
	testCompanionID       = "c360.semmachina.world1.starter.character.wren"
	testCompanionEvidence = "c360.semmachina.world1.starter.evidence.scrap"
)

func validCompanionDecision() *payload.CompanionDecision {
	d := &payload.CompanionDecision{
		TurnID: testTurnID, ContextRef: testCompanionContext,
		PlayerID: testCompanionPlayerID, CompanionID: testCompanionID,
		Kind: payload.CompanionDecisionHint, HintLevel: vocabulary.HintLevelNudge,
		EvidenceRefs: []string{testCompanionEvidence}, TargetRef: testTargetRef,
	}
	d.DecisionID = payload.CompanionDecisionID(d.TurnID, d.ContextRef, d.PlayerID, d.CompanionID)
	return d
}

func TestCompanionDecision_StrictStructuralContract(t *testing.T) {
	d := validCompanionDecision()
	if got, want := d.Schema(), (message.Type{Domain: payload.Domain, Category: "companion_decision", Version: payload.SchemaVersion}); got != want {
		t.Fatalf("Schema() = %v, want %v", got, want)
	}
	if len(d.DecisionID) != 64 || strings.ToLower(d.DecisionID) != d.DecisionID {
		t.Fatalf("decision_id = %q, want lowercase SHA-256", d.DecisionID)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
	if payload.CompanionDecisionID("ab", "c", testCompanionPlayerID, testCompanionID) ==
		payload.CompanionDecisionID("a", "bc", testCompanionPlayerID, testCompanionID) {
		t.Fatal("identity tuple is not length framed")
	}

	for _, kind := range payload.CompanionDecisionKinds() {
		t.Run(string(kind), func(t *testing.T) {
			candidate := validCompanionDecision()
			candidate.Kind = kind
			candidate.HintLevel = ""
			candidate.EvidenceRefs = nil
			if kind == payload.CompanionDecisionHint {
				candidate.HintLevel = vocabulary.HintLevelNudge
				candidate.EvidenceRefs = []string{testCompanionEvidence}
			}
			if kind == payload.CompanionDecisionWarning || kind == payload.CompanionDecisionRecall {
				candidate.EvidenceRefs = []string{testCompanionEvidence}
			}
			if err := candidate.Validate(); err != nil {
				t.Fatalf("registered kind rejected: %v", err)
			}
		})
	}
}

func TestCompanionDecision_RejectsInvalidConditionalAndReferenceShapes(t *testing.T) {
	tests := map[string]func(*payload.CompanionDecision){
		"unknown kind":       func(d *payload.CompanionDecision) { d.Kind = "aside" },
		"hint missing level": func(d *payload.CompanionDecision) { d.HintLevel = "" },
		"quip with level":    func(d *payload.CompanionDecision) { d.Kind = payload.CompanionDecisionQuip },
		"recall without evidence": func(d *payload.CompanionDecision) {
			d.Kind = payload.CompanionDecisionRecall
			d.HintLevel = ""
			d.EvidenceRefs = nil
		},
		"nine evidence refs": func(d *payload.CompanionDecision) { d.EvidenceRefs = entityRefs("evidence", 9) },
		"duplicate evidence": func(d *payload.CompanionDecision) {
			d.EvidenceRefs = []string{testCompanionEvidence, testCompanionEvidence}
		},
		"malformed evidence": func(d *payload.CompanionDecision) { d.EvidenceRefs = []string{"scrap"} },
		"malformed context":  func(d *payload.CompanionDecision) { d.ContextRef = "case-ish" },
		"wrong id":           func(d *payload.CompanionDecision) { d.DecisionID = strings.Repeat("0", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			d := validCompanionDecision()
			mutate(d)
			if err := d.Validate(); err == nil {
				t.Fatal("invalid decision accepted")
			}
		})
	}
}

func TestCompanionDecision_AliasJSONRejectsUnknownFieldsAndProductionDecoderRoundTrips(t *testing.T) {
	d := validCompanionDecision()
	body, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := object["payload"]; wrapped {
		t.Fatal("payload wrapped its own envelope")
	}

	withUnknown := append(body[:len(body)-1], []byte(`,"dialogue":"secret prose"}`)...)
	var refused payload.CompanionDecision
	if err := json.Unmarshal(withUnknown, &refused); err == nil {
		t.Fatal("unknown prose field was silently ignored")
	}
	if err := refused.UnmarshalJSON(append(body, []byte(` {}`)...)); err == nil {
		t.Fatal("direct alias decoder accepted a trailing JSON value")
	}

	decoded := decode(t, publish(t, d))
	got, ok := decoded.(*payload.CompanionDecision)
	if !ok {
		t.Fatalf("production decoder produced %T", decoded)
	}
	if !reflect.DeepEqual(got, d) {
		t.Fatalf("round trip changed fields: got %#v want %#v", got, d)
	}
}
