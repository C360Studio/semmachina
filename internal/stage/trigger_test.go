package stage_test

import (
	"encoding/json"
	"testing"

	"github.com/c360studio/semmachina/internal/stage"
)

func publication(entityID string) []byte {
	data, _ := json.Marshal(map[string]any{
		"entity_id": entityID,
		"subject":   "semmachina.turn.adjudicating",
		"timestamp": "2026-07-29T00:00:00Z",
		"source":    "rule_engine",
	})
	return data
}

func TestParseTrigger_ReadsTheTurnOffTheRuleEnginesOwnPublication(t *testing.T) {
	trigger, err := stage.ParseTrigger(publication(testTurnEntityID))
	if err != nil {
		t.Fatalf("ParseTrigger: %v", err)
	}
	if trigger.TurnEntityID != testTurnEntityID {
		t.Errorf("turn entity = %q, want %q", trigger.TurnEntityID, testTurnEntityID)
	}
	if trigger.TurnID != testTurnID {
		t.Errorf("turn id = %q, want %q; the turn id is the entity's instance segment",
			trigger.TurnID, testTurnID)
	}
}

func TestParseTrigger_RefusesEverythingThatIsNotATurn(t *testing.T) {
	cases := map[string][]byte{
		"not JSON at all":       []byte("{"),
		"no entity reference":   publication(""),
		"not a six-part id":     publication("gatehouse"),
		"a scene, not a turn":   publication(testSceneID),
		"a character, not turn": publication("acme.semmachina.keep.starter.character.rook"),
	}
	for name, data := range cases {
		if trigger, err := stage.ParseTrigger(data); err == nil {
			t.Errorf("%s parsed into %+v", name, trigger)
		}
	}
}
