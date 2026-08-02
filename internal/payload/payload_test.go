package payload_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Six-part IDs in the documented position order,
// org.platform.domain.system.type.instance: the WORLD NAMESPACE (world1) is the
// domain and the TEMPLATE (starter) is the system. These are opaque strings to
// every payload, so the order costs nothing here and is copied by the next
// author — which is the only reason it has to be right.
const (
	testPlayerID   = "c360.semmachina.world1.starter.player.p1"
	testCharacter  = "c360.semmachina.world1.starter.character.rook"
	testAlly       = "c360.semmachina.world1.starter.character.wren"
	testCampaignID = "c360.semmachina.world1.starter.campaign.main"
	testSceneID    = "c360.semmachina.world1.starter.scene.gatehouse"
	testTurnEntity = "c360.semmachina.world1.starter.turn.turn-act-1"
	testLocation   = "c360.semmachina.world1.starter.location.courtyard"
	testItem       = "c360.semmachina.world1.starter.item.rations"

	testActionID = "act-1"
	testTurnID   = "turn-act-1"
)

var testTime = time.Date(2026, 7, 28, 9, 15, 30, 123456000, time.UTC)

// testRegistry is the production registration path: a real
// payloadregistry.Registry populated by the same RegisterPayloads the binary
// bootstrap calls. Tests that decode go through message.NewDecoder bound to
// it, so a registration bug fails the test rather than hiding behind a
// hand-rolled decode.
func testRegistry(t *testing.T) *payloadregistry.Registry {
	t.Helper()
	reg := payloadregistry.New()
	if err := payload.RegisterPayloads(reg); err != nil {
		t.Fatalf("RegisterPayloads: %v", err)
	}
	return reg
}

func testDecoder(t *testing.T) *message.Decoder {
	t.Helper()
	return message.NewDecoder(testRegistry(t))
}

// publish marshals a payload the way production does: wrapped in a
// BaseMessage, which validates before serializing. A payload that cannot pass
// Validate cannot reach the wire at all.
func publish(t *testing.T, p message.Payload) []byte {
	t.Helper()
	msg := message.NewBaseMessage(p.Schema(), p, "payload-test", message.WithTime(testTime))
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal BaseMessage: %v", err)
	}
	return data
}

// publishUnvalidated builds the same wire shape while bypassing
// BaseMessage's validation gate, so tests can prove that a decoded payload
// carrying an out-of-vocabulary value is rejected by Validate. Decoding does
// not validate; skipping Validate after decode is skipping the vocabulary.
func publishUnvalidated(t *testing.T, p message.Payload) []byte {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload body: %v", err)
	}
	return wireWithBody(t, p.Schema(), body)
}

func wireWithBody(t *testing.T, msgType message.Type, body json.RawMessage) []byte {
	t.Helper()
	envelope := map[string]any{
		"id":      "payload-test-message",
		"type":    msgType,
		"payload": body,
		"meta": map[string]any{
			"created_at":  testTime.UnixMilli(),
			"received_at": testTime.UnixMilli(),
			"source":      "payload-test",
		},
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return data
}

func decode(t *testing.T, data []byte) message.Payload {
	t.Helper()
	msg, err := testDecoder(t).Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return msg.Payload()
}

// assertFixtureFullyPopulated fails if any field of a round-trip fixture is
// left at its zero value. Without it, a DeepEqual round-trip assertion is
// mostly comparing zeros and would pass even if half the fields never
// serialized.
func assertFixtureFullyPopulated(t *testing.T, value any, exempt ...string) {
	t.Helper()
	skip := make(map[string]bool, len(exempt))
	for _, name := range exempt {
		skip[name] = true
	}
	walkFixture(t, reflect.ValueOf(value), "", skip)
}

func walkFixture(t *testing.T, v reflect.Value, path string, skip map[string]bool) {
	t.Helper()
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			t.Fatalf("fixture field %s is a nil pointer", path)
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}

	typ := v.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if path != "" {
			name = path + "." + field.Name
		}
		if skip[field.Name] || skip[name] {
			continue
		}

		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Slice, reflect.Map:
			if fv.Len() == 0 {
				t.Fatalf("fixture field %s is empty; the round-trip assertion would not cover it", name)
			}
		case reflect.Struct:
			if fv.Type() == reflect.TypeOf(time.Time{}) {
				if fv.Interface().(time.Time).IsZero() {
					t.Fatalf("fixture field %s is the zero time", name)
				}
				continue
			}
			walkFixture(t, fv, name, skip)
		case reflect.Bool:
			if !fv.Bool() {
				t.Fatalf("fixture field %s is false; set it true so the round-trip covers it", name)
			}
		case reflect.Pointer:
			if fv.IsNil() {
				t.Fatalf("fixture field %s is nil", name)
			}
		default:
			if fv.IsZero() {
				t.Fatalf("fixture field %s is the zero value; the round-trip assertion would not cover it", name)
			}
		}
	}
}

func intPtr(v int) *int { return &v }

func validPlayerAction() *payload.PlayerAction {
	return &payload.PlayerAction{
		ActionID:   testActionID,
		PlayerID:   testPlayerID,
		CampaignID: testCampaignID,
		SceneID:    testSceneID,
		Text:       "I wedge the gate open with the crowbar and slip through.",
		ArrivedAt:  testTime,
		Channel: payload.ChannelBinding{
			Adapter: vocabulary.AdapterWebSocket,
			ReplyTo: "ws-conn-7",
		},
	}
}

func validVerdict() *payload.Verdict {
	return &payload.Verdict{
		TurnID:   testTurnID,
		ActionID: testActionID,
		SceneID:  testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskHigh,
			Consequence:  vocabulary.ConsequenceHarm,
			RequiresRoll: true,
		},
		Modifiers: []payload.Modifier{
			{Source: vocabulary.ModifierEquipment, Value: 1, Note: "crowbar"},
			{Source: vocabulary.ModifierCondition, Value: -1, Note: "exhausted"},
		},
		Bands: payload.EffectBands{
			vocabulary.BandMiss: {
				{
					Type:      vocabulary.EffectSetAttribute,
					Target:    testCharacter,
					Attribute: vocabulary.AttributeHealth,
					Value:     intPtr(3),
				},
				{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusWounded},
			},
			vocabulary.BandPartial: {
				{Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: testLocation},
			},
			vocabulary.BandFull: {
				{Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: testLocation},
				{
					Type:     vocabulary.EffectAddRelationship,
					Target:   testCharacter,
					Relation: vocabulary.RelationAlliedWith,
					Object:   testAlly,
				},
			},
		},
		Rationale: "The gate is rusted; the crowbar helps, but the guard is watching.",
	}
}

func validRollResult() *payload.RollResult {
	return &payload.RollResult{
		TurnID:     testTurnID,
		Mechanic:   vocabulary.Mechanic2d6PbtaV1,
		RNGVersion: vocabulary.RNGPCGV1,
		Seed:       payload.SeedSource{CampaignID: testCampaignID, TurnID: testTurnID},
		Dice:       []int{4, 4},
		Modifiers: []payload.Modifier{
			{Source: vocabulary.ModifierEquipment, Value: 1, Note: "crowbar"},
		},
		ModifierTotal: 1,
		Total:         9,
		Band:          vocabulary.BandPartial,
	}
}

func validEffectBatch() *payload.EffectBatch {
	return payload.NewEffectBatch(testTurnID, vocabulary.BandPartial, []payload.EffectIntent{
		{
			Type:      vocabulary.EffectSetAttribute,
			Target:    testItem,
			Attribute: vocabulary.AttributeQuantity,
			Value:     intPtr(2),
		},
		{Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: testLocation},
	})
}

func validTurnManifest() *payload.TurnManifest {
	return &payload.TurnManifest{
		TurnID:         testTurnID,
		ActionID:       testActionID,
		CampaignID:     testCampaignID,
		SceneID:        testSceneID,
		PlayerID:       testPlayerID,
		Phase:          vocabulary.PhaseComplete,
		ActionRef:      "obj://actions/act-1",
		VerdictRef:     "obj://verdicts/turn-act-1",
		RollRef:        "obj://rolls/turn-act-1",
		EffectBatchRef: "obj://effects/batch-turn-act-1",
		NarrationRef:   "obj://prose/turn-act-1",
		RollGate:       validRollGate(),
		RecordedAt:     testTime,
		WorldTime:      1720000000,
	}
}

func validSubmitAction() *payload.SubmitAction {
	return &payload.SubmitAction{
		Protocol:       payload.PlayerProtocolV1,
		Text:           "I wedge the gate open with the crowbar and slip through.",
		IdempotencyKey: "0f9c2b1e-6a4d-4c7f-9b21-8e5d3a7c1f04",
	}
}

// validTurnResult is a COMPLETED turn's result. It is the shape a client sees
// most, and the one with the most required structure; the failed shape is
// exercised where its own rules are, in turnresult_test.go.
//
// Its resolution is derived from the same fixtures the verdict and roll use, so
// the three describe one turn rather than three unrelated ones — which is what
// makes the band agreement in TurnResolution.Validate a real assertion here.
func validTurnResult() *payload.TurnResult {
	return &payload.TurnResult{
		Protocol:   payload.PlayerProtocolV1,
		TurnID:     testTurnID,
		ActionID:   testActionID,
		PlayerID:   testPlayerID,
		Phase:      vocabulary.PhaseComplete,
		Resolution: validTurnResolution(),
		CompanionResolution: &payload.CompanionResolution{
			CompanionID: testAlly,
			Kind:        payload.CompanionDecisionHint,
			HintLevel:   vocabulary.HintLevelConnect,
		},
		NarrationRef: testNarrationRef,
		ResolvedAt:   testTime,
	}
}

func validTurnResolution() *payload.TurnResolution {
	roll := validRollResult()
	return &payload.TurnResolution{
		Verdict: validVerdict().Scalars,
		Band:    roll.Band,
		Roll: &payload.TurnRoll{
			Mechanic:      roll.Mechanic,
			Dice:          roll.Dice,
			Modifiers:     roll.Modifiers,
			ModifierTotal: roll.ModifierTotal,
			Total:         roll.Total,
		},
	}
}

// validRollGate is the fixture's roll-gate agreement, derived from the same
// scalars validVerdict reports so the two fixtures describe one turn.
//
// (plausible, high) advises a roll and the verdict reports one, so Reported and
// Advised agree here; the disagreement cases are exercised where they belong,
// in the manifest's own tests.
func validRollGate() *payload.RollGateExpectation {
	gate, err := validVerdict().Scalars.RollGate()
	if err != nil {
		panic("the verdict fixture's scalars are outside the vocabulary: " + err.Error())
	}
	return &gate
}
