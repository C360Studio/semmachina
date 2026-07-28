package payload_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func failedTurnManifest() *payload.TurnManifest {
	m := validTurnManifest()
	m.Phase = vocabulary.PhaseFailed
	m.FailureReason = "effect batch rejected: health value 900 is outside registered bounds [0, 10]"
	m.NarrationRef = ""
	m.EffectBatchRef = ""
	return m
}

func TestTurnManifest_CompleteAndFailedTurnsAreBothLedgerable(t *testing.T) {
	if err := validTurnManifest().Validate(); err != nil {
		t.Fatalf("a complete-turn manifest was rejected: %v", err)
	}
	if err := failedTurnManifest().Validate(); err != nil {
		t.Fatalf("a failed-turn manifest was rejected: %v", err)
	}
}

func TestTurnManifest_FailedManifestRoundTripsThroughTheProductionDecoder(t *testing.T) {
	original := failedTurnManifest()
	decoded, ok := decode(t, publish(t, original)).(*payload.TurnManifest)
	if !ok {
		t.Fatal("decoder produced the wrong type")
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round-trip lost or altered fields:\n got %#v\nwant %#v", decoded, original)
	}
	if decoded.FailureReason != original.FailureReason {
		t.Fatal("the failure reason did not survive the wire")
	}
}

func TestTurnManifest_RejectsIncoherentRecords(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*payload.TurnManifest)
		wantErr string
	}{
		{
			name:    "non-terminal phase",
			mutate:  func(m *payload.TurnManifest) { m.Phase = vocabulary.PhaseApplying },
			wantErr: "not terminal",
		},
		{
			name:    "phase outside the vocabulary",
			mutate:  func(m *payload.TurnManifest) { m.Phase = "done" },
			wantErr: "turn_phase",
		},
		{
			name: "failed turn without a reason",
			mutate: func(m *payload.TurnManifest) {
				m.Phase = vocabulary.PhaseFailed
				m.FailureReason = ""
			},
			wantErr: "failure_reason",
		},
		{
			name:    "complete turn carrying a failure reason",
			mutate:  func(m *payload.TurnManifest) { m.FailureReason = "something went wrong" },
			wantErr: "failure_reason",
		},
		{
			name:    "complete turn without narration",
			mutate:  func(m *payload.TurnManifest) { m.NarrationRef = "" },
			wantErr: "narration_ref",
		},
		{
			name:    "complete turn without a verdict",
			mutate:  func(m *payload.TurnManifest) { m.VerdictRef = "" },
			wantErr: "verdict_ref",
		},
		{
			name:    "no action reference",
			mutate:  func(m *payload.TurnManifest) { m.ActionRef = "" },
			wantErr: "action_ref",
		},
		{
			name:    "turn id missing",
			mutate:  func(m *payload.TurnManifest) { m.TurnID = "" },
			wantErr: "turn_id",
		},
		{
			name:    "player is not a canonical entity id",
			mutate:  func(m *payload.TurnManifest) { m.PlayerID = "conn-7" },
			wantErr: "player_id",
		},
		{
			name:    "no real-time stamp",
			mutate:  func(m *payload.TurnManifest) { m.RecordedAt = time0() },
			wantErr: "recorded_at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validTurnManifest()
			tc.mutate(manifest)

			decoded := decode(t, publishUnvalidated(t, manifest))
			err := decoded.Validate()
			if err == nil {
				t.Fatal("Validate accepted an incoherent ledger record")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// The ledger is references only. This is enforced by the field set itself:
// if someone adds a prose, verdict-body, or intent-list field, this fails.
// A manifest that inlines content stops being an archive index and starts
// being a second, divergent copy of the world.
func TestTurnManifest_CarriesReferencesOnly(t *testing.T) {
	allowed := map[string]bool{
		"turn_id":          true,
		"action_id":        true,
		"campaign_id":      true,
		"scene_id":         true,
		"player_id":        true,
		"phase":            true,
		"action_ref":       true,
		"verdict_ref":      true,
		"roll_ref":         true,
		"effect_batch_ref": true,
		"narration_ref":    true,
		"failure_reason":   true,
		"recorded_at":      true,
		"world_time":       true,
	}

	typ := reflect.TypeOf(payload.TurnManifest{})
	seen := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s has no JSON name", typ.Field(i).Name)
		}
		if !allowed[name] {
			t.Fatalf("TurnManifest gained field %q; the ledger carries references only, never content", name)
		}
		seen[name] = true
	}
	for name := range allowed {
		if !seen[name] {
			t.Fatalf("expected ledger field %q is missing from TurnManifest", name)
		}
	}
}

// The world-time field must exist and serialize even at zero, so ledger
// readers written now need no schema change when the world clock arrives.
func TestTurnManifest_WorldTimeIsAlwaysPresentEvenAtZero(t *testing.T) {
	manifest := validTurnManifest()
	manifest.WorldTime = 0

	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw, ok := fields["world_time"]
	if !ok {
		t.Fatal("world_time disappeared from the wire at zero; ledger readers would need a schema change later")
	}
	if string(raw) != "0" {
		t.Fatalf("world_time = %s, want 0", raw)
	}
}
