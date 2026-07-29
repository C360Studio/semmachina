package ledger

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/stage"
)

// resolvedNotification is the bytes the rule engine's `publish` action puts on
// the wire for a rule that fired.
func resolvedNotification(t *testing.T, entityID string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"entity_id": entityID,
		"subject":   "semmachina.turn.resolved",
		"source":    "semmachina-turn-sequencing",
	})
	if err != nil {
		t.Fatalf("encode notification: %v", err)
	}
	return data
}

func TestParseResolved_ReadsTheTurnTheRuleFiredFor(t *testing.T) {
	event, err := parseResolved(resolvedNotification(t, "c360.semmachina.world1.starter.turn.turn-act-1"))
	if err != nil {
		t.Fatalf("parseResolved: %v", err)
	}
	if event.TurnEntityID != "c360.semmachina.world1.starter.turn.turn-act-1" {
		t.Fatalf("entity id = %q", event.TurnEntityID)
	}
	if event.TurnID != "turn-act-1" {
		t.Fatalf("turn id = %q; it is the instance segment of the entity id", event.TurnID)
	}
}

func TestParseResolved_RefusesANotificationThatCannotArchiveAnything(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{name: "not JSON", data: []byte("{"), wantErr: "decode"},
		{name: "names no entity", data: resolvedNotification(t, ""), wantErr: "names no entity"},
		{
			name:    "not a canonical entity id",
			data:    resolvedNotification(t, "turn-act-1"),
			wantErr: "canonical six-part entity ID",
		},
		{
			// The interesting refusal: a rule whose pattern was widened to
			// characters or scenes would hand the archive a perfectly valid
			// entity ID, and a manifest would be composed for something that has
			// no phase.
			name:    "a scene, not a turn",
			data:    resolvedNotification(t, "c360.semmachina.world1.starter.scene.gatehouse"),
			wantErr: `type segment "scene"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, err := parseResolved(tc.data)
			if err == nil {
				t.Fatalf("parseResolved accepted %+v", event)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// The ledger decodes the rule engine's publication shape itself rather than
// importing internal/stage, so that the replay reader cannot reach a persona
// through the turn loop. That is a deliberate duplication, and this is what
// stops it drifting: both decoders are handed the same bytes and must agree on
// every one, accept and reject alike.
//
// The dependency lives here, in the test binary, where it costs nothing.
func TestResolvedEvent_ReadsTheSameWireShapeAsTheStageTriggerParser(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"a turn", resolvedNotification(t, "c360.semmachina.world1.starter.turn.turn-act-1")},
		{"another world's turn", resolvedNotification(t, "acme.semmachina.w9.other.turn.turn-x")},
		{"a scene", resolvedNotification(t, "c360.semmachina.world1.starter.scene.gatehouse")},
		{"a character", resolvedNotification(t, "c360.semmachina.world1.starter.character.rook")},
		{"no entity", resolvedNotification(t, "")},
		{"not an entity id", resolvedNotification(t, "turn-act-1")},
		{"too few segments", resolvedNotification(t, "c360.semmachina.world1.turn.turn-act-1")},
		{"a dotted turn id", resolvedNotification(t, "c360.semmachina.world1.starter.turn.turn.act")},
		{"not JSON", []byte("{")},
		{"an empty object", []byte("{}")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, ledgerErr := parseResolved(tc.data)
			trigger, stageErr := stage.ParseTrigger(tc.data)

			if (ledgerErr == nil) != (stageErr == nil) {
				t.Fatalf("the two decoders disagree on whether this is a turn notification:\n"+
					"  ledger: %v\n  stage:  %v", ledgerErr, stageErr)
			}
			if ledgerErr != nil {
				return
			}
			if event.TurnEntityID != trigger.TurnEntityID || event.TurnID != trigger.TurnID {
				t.Fatalf("the two decoders read different turns: ledger %+v, stage %+v", event, trigger)
			}
		})
	}
}
