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
	m.FailureReason = string(vocabulary.FailureEffectInvalid)
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
			// The archive is where a campaign's history is read back, so a
			// free-text reason here would hand the chronicler, the writer loop
			// and any failure-rate metric a value they cannot group on.
			name: "failed turn whose reason is a sentence rather than a code",
			mutate: func(m *payload.TurnManifest) {
				m.Phase = vocabulary.PhaseFailed
				m.NarrationRef = ""
				m.FailureReason = "effect batch rejected: health value 900 is outside registered bounds"
			},
			wantErr: "not in the closed vocabulary",
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
		{
			name:    "complete turn with no recorded roll gate",
			mutate:  func(m *payload.TurnManifest) { m.RollGate = nil },
			wantErr: "roll_gate",
		},
		{
			name: "roll gate naming a mapping version this engine never had",
			mutate: func(m *payload.TurnManifest) {
				gate := *m.RollGate
				gate.Mapping = "roll-gate/v2"
				m.RollGate = &gate
			},
			wantErr: "roll_gate_mapping",
		},
		{
			name: "roll gate carrying no mapping version at all",
			mutate: func(m *payload.TurnManifest) {
				gate := *m.RollGate
				gate.Mapping = ""
				m.RollGate = &gate
			},
			wantErr: "roll_gate_mapping",
		},
		{
			// The advice must be the one its own named mapping gives. A record
			// claiming otherwise is a comparison that never happened.
			name: "roll gate recording advice its own mapping does not give",
			mutate: func(m *payload.TurnManifest) {
				gate := *m.RollGate
				gate.Advised = !gate.Advised
				m.RollGate = &gate
			},
			wantErr: "advises",
		},
		{
			name: "roll gate carrying an out-of-vocabulary class",
			mutate: func(m *payload.TurnManifest) {
				gate := *m.RollGate
				gate.Plausibility = "likely"
				m.RollGate = &gate
			},
			wantErr: "plausibility",
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
		// roll_gate is the one non-reference, non-identifier field here, and it
		// earns the exemption by being the opposite of bulky content: five
		// closed-vocabulary scalars totalling a few dozen bytes, none of them
		// authored by a model. What it is NOT is derivable-on-read — see
		// RollGateExpectation.Mapping.
		"roll_gate":   true,
		"recorded_at": true,
		"world_time":  true,
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

// A verdict whose reported gate DISAGREES with the mapping is valid and
// proceeds (D12), so the manifest that archives it must be valid too.
//
// This is the case the whole field exists for. Rejecting a disagreement here
// would take the roll gate back from the fiction by the back door — the turn
// would run and then be unarchivable — so the archive records the divergence
// and says nothing about whether it was right.
func TestTurnManifest_ArchivesAGateDisagreementRatherThanRefusingIt(t *testing.T) {
	// (certain, none) advises no roll; this adjudicator called for the dice
	// anyway, which is exactly the judgment D12 leaves to it.
	scalars := payload.VerdictScalars{
		Plausibility: vocabulary.PlausibilityCertain,
		Risk:         vocabulary.RiskNone,
		Consequence:  vocabulary.ConsequenceNone,
		RequiresRoll: true,
	}
	gate, err := scalars.RollGate()
	if err != nil {
		t.Fatalf("RollGate: %v", err)
	}
	if gate.Agrees() {
		t.Fatal("this fixture is supposed to DISAGREE with the mapping; the premise is stale")
	}

	manifest := validTurnManifest()
	manifest.RollGate = &gate
	if err := manifest.Validate(); err != nil {
		t.Fatalf("a manifest archiving a gate disagreement was rejected: %v", err)
	}

	decoded, ok := decode(t, publish(t, manifest)).(*payload.TurnManifest)
	if !ok {
		t.Fatal("decoder produced the wrong type")
	}
	if decoded.RollGate == nil {
		t.Fatal("the roll gate did not survive the wire")
	}
	if decoded.RollGate.Agrees() {
		t.Fatalf("the archived disagreement decoded as agreement: %+v", decoded.RollGate)
	}
	if decoded.RollGate.Mapping != vocabulary.RollGateMappingVersion() {
		t.Fatalf("archived mapping version %q, want %q",
			decoded.RollGate.Mapping, vocabulary.RollGateMappingVersion())
	}
}

// The mapping version must reach the wire under its own name.
//
// Without it on the wire, a reader can only recompute the advice — which is
// the derived value that silently flips for every historical turn the day the
// mapping is tuned. A field that serialized as anything but a version would be
// no better than not having it.
func TestTurnManifest_RollGateCarriesItsMappingVersionOnTheWire(t *testing.T) {
	body, err := json.Marshal(validTurnManifest())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields struct {
		RollGate map[string]json.RawMessage `json:"roll_gate"`
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"plausibility", "risk", "reported", "advised", "mapping"} {
		if _, ok := fields.RollGate[key]; !ok {
			t.Fatalf("roll_gate is missing %q on the wire; it carries %v", key, fields.RollGate)
		}
	}
	var mapping string
	if err := json.Unmarshal(fields.RollGate["mapping"], &mapping); err != nil {
		t.Fatalf("decode mapping: %v", err)
	}
	if mapping != string(vocabulary.RollGateMappingVersion()) {
		t.Fatalf("wire mapping %q, want %q", mapping, vocabulary.RollGateMappingVersion())
	}
}

// A turn that failed BEFORE adjudication has no verdict and therefore no gate,
// and that absence must be spelled differently from "reported no roll".
func TestTurnManifest_AFailedTurnWithNoVerdictCarriesNoRollGateAtAll(t *testing.T) {
	manifest := failedTurnManifest()
	manifest.VerdictRef = ""
	manifest.RollRef = ""
	manifest.RollGate = nil

	if err := manifest.Validate(); err != nil {
		t.Fatalf("a turn that failed before adjudication was rejected: %v", err)
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw, present := fields["roll_gate"]; present {
		t.Fatalf("an unadjudicated turn serialized roll_gate as %s; absence and a zero-valued gate must not "+
			"be the same record", raw)
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
