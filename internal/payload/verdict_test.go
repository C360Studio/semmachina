package payload_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestVerdict_ValidFixtureIsAccepted(t *testing.T) {
	if err := validVerdict().Validate(); err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}
}

// The rejection table is the closed-vocabulary contract in behavior form.
// Each case arrives over the wire (decoded by the production decoder) so the
// rejection is proven on a payload the engine could actually receive.
func TestVerdict_RejectsOutOfContractExits(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*payload.Verdict)
		wantErr string
	}{
		{
			name:    "unregistered plausibility",
			mutate:  func(v *payload.Verdict) { v.Scalars.Plausibility = "likely" },
			wantErr: "plausibility",
		},
		{
			name:    "unregistered risk",
			mutate:  func(v *payload.Verdict) { v.Scalars.Risk = "severe" },
			wantErr: "risk",
		},
		{
			name:    "unregistered consequence",
			mutate:  func(v *payload.Verdict) { v.Scalars.Consequence = "damage" },
			wantErr: "consequence",
		},
		{
			name: "rolling verdict declares the auto band",
			mutate: func(v *payload.Verdict) {
				v.Bands = payload.EffectBands{vocabulary.BandAuto: nil}
			},
			wantErr: "band",
		},
		{
			name: "rolling verdict omits a band",
			mutate: func(v *payload.Verdict) {
				delete(v.Bands, vocabulary.BandPartial)
			},
			wantErr: "band",
		},
		{
			name: "rolling verdict declares an extra band",
			mutate: func(v *payload.Verdict) {
				v.Bands[vocabulary.BandAuto] = nil
			},
			wantErr: "band",
		},
		{
			// An unregistered key can only exist by displacing a required
			// band or inflating the count, so the exact-set check catches it.
			name: "band key outside the vocabulary",
			mutate: func(v *payload.Verdict) {
				delete(v.Bands, vocabulary.BandFull)
				v.Bands["critical"] = nil
			},
			wantErr: `missing the "full" band`,
		},
		{
			name: "intent outside the effect vocabulary",
			mutate: func(v *payload.Verdict) {
				v.Bands[vocabulary.BandMiss] = []payload.EffectIntent{
					{Type: "delete_entity", Target: testCharacter},
				}
			},
			wantErr: "effect_type",
		},
		{
			name: "intent value outside registered bounds",
			mutate: func(v *payload.Verdict) {
				v.Bands[vocabulary.BandFull] = []payload.EffectIntent{{
					Type:      vocabulary.EffectSetAttribute,
					Target:    testCharacter,
					Attribute: vocabulary.AttributeHealth,
					Value:     intPtr(900),
				}}
			},
			wantErr: "bounds",
		},
		{
			name:    "modifier source outside the vocabulary",
			mutate:  func(v *payload.Verdict) { v.Modifiers[0].Source = "luck" },
			wantErr: "modifier_source",
		},
		{
			name:    "modifier value beyond the cap",
			mutate:  func(v *payload.Verdict) { v.Modifiers[0].Value = 40 },
			wantErr: "outside",
		},
		{
			name: "more modifiers than the cap allows",
			mutate: func(v *payload.Verdict) {
				v.Modifiers = nil
				for range payload.MaxModifiers + 1 {
					v.Modifiers = append(v.Modifiers,
						payload.Modifier{Source: vocabulary.ModifierTrait, Value: 1})
				}
			},
			wantErr: "cap",
		},
		{
			name:    "scene is not a canonical entity id",
			mutate:  func(v *payload.Verdict) { v.SceneID = "gatehouse" },
			wantErr: "scene_id",
		},
		{
			name:    "turn id missing",
			mutate:  func(v *payload.Verdict) { v.TurnID = "" },
			wantErr: "turn_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := validVerdict()
			tc.mutate(verdict)

			decoded := decode(t, publishUnvalidated(t, verdict))
			err := decoded.Validate()
			if err == nil {
				t.Fatal("Validate accepted a verdict that violates the exit contract")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// bandsFor builds the band set a verdict reporting requiresRoll must declare,
// each band carrying one intent so the shape is never vacuously empty.
func bandsFor(requiresRoll bool) payload.EffectBands {
	bands := payload.EffectBands{}
	for _, band := range vocabulary.BandsForVerdict(requiresRoll) {
		bands[band] = []payload.EffectIntent{
			{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusWounded},
		}
	}
	return bands
}

// Fiction decides, the mapping advises. The adjudicator's reported
// requires_roll is authoritative — narrative positioning dictates mechanics —
// so a verdict whose reported gate disagrees with vocabulary.RequiresRoll is
// valid and PROCEEDS. It is published through BaseMessage here, which
// validates before serializing: a disagreeing verdict must be able to reach
// the wire, not merely survive a direct Validate call.
func TestVerdict_ReportedRollGateIsAuthoritativeAndDisagreementIsRecorded(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*payload.Verdict)
		wantReported bool
		wantAdvised  bool
	}{
		{
			name: "the persona declines the dice for an uncertain, risky action",
			mutate: func(v *payload.Verdict) {
				v.Scalars.RequiresRoll = false
				v.Bands = bandsFor(false)
			},
			wantReported: false,
			wantAdvised:  true,
		},
		{
			name: "the persona calls for the dice on an action the mapping calls impossible",
			mutate: func(v *payload.Verdict) {
				v.Scalars.Plausibility = vocabulary.PlausibilityImpossible
			},
			wantReported: true,
			wantAdvised:  false,
		},
		{
			name: "the persona calls for the dice when nothing is at stake",
			mutate: func(v *payload.Verdict) {
				v.Scalars.Risk = vocabulary.RiskNone
			},
			wantReported: true,
			wantAdvised:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := validVerdict()
			tc.mutate(verdict)

			decoded, ok := decode(t, publish(t, verdict)).(*payload.Verdict)
			if !ok {
				t.Fatal("decoder produced the wrong type")
			}
			if err := decoded.Validate(); err != nil {
				t.Fatalf("a verdict disagreeing with the roll-gate mapping was rejected: %v", err)
			}

			gate, err := decoded.Scalars.RollGate()
			if err != nil {
				t.Fatalf("RollGate on a valid verdict: %v", err)
			}
			if gate.Reported != tc.wantReported {
				t.Fatalf("gate.Reported = %v, want %v", gate.Reported, tc.wantReported)
			}
			if gate.Advised != tc.wantAdvised {
				t.Fatalf("gate.Advised = %v, want %v", gate.Advised, tc.wantAdvised)
			}
			if gate.Agrees() {
				t.Fatal("a disagreement reported agreement; the divergence signal would read as zero")
			}
			if gate.Reported != decoded.Scalars.RequiresRoll {
				t.Fatal("the recorded gate does not report what the engine will act on")
			}
			if gate.Plausibility != decoded.Scalars.Plausibility || gate.Risk != decoded.Scalars.Risk {
				t.Fatalf("the recorded expectation does not carry its own inputs: %+v", gate)
			}
		})
	}
}

// Agreement is recorded too — a signal that only fires on divergence cannot
// produce a rate.
func TestVerdictRollGate_RecordsAgreementAsWellAsDivergence(t *testing.T) {
	gate, err := validVerdict().Scalars.RollGate()
	if err != nil {
		t.Fatalf("RollGate: %v", err)
	}
	if !gate.Agrees() {
		t.Fatalf("the fixture agrees with the mapping but was recorded as divergent: %+v", gate)
	}
	if !gate.Reported || !gate.Advised {
		t.Fatalf("the fixture is a rolling verdict; got %+v", gate)
	}
}

// The gate comparison must be total over the closed product set for BOTH
// reported values: every combination is a verdict the engine accepts and
// records, never one it rejects.
func TestVerdictRollGate_IsTotalAndNeverRejectsAVerdict(t *testing.T) {
	pairs := 0
	for _, p := range vocabulary.Plausibilities() {
		for _, r := range vocabulary.Risks() {
			advised, err := vocabulary.RequiresRoll(p, r)
			if err != nil {
				t.Fatalf("RequiresRoll(%s, %s): %v", p, r, err)
			}
			for _, reported := range []bool{true, false} {
				verdict := validVerdict()
				verdict.Scalars.Plausibility = p
				verdict.Scalars.Risk = r
				verdict.Scalars.RequiresRoll = reported
				verdict.Bands = bandsFor(reported)

				if err := verdict.Validate(); err != nil {
					t.Fatalf("(%s, %s, reported=%v) was rejected: %v", p, r, reported, err)
				}
				gate, err := verdict.Scalars.RollGate()
				if err != nil {
					t.Fatalf("(%s, %s) RollGate: %v", p, r, err)
				}
				if gate.Advised != advised {
					t.Fatalf("(%s, %s) advised %v, mapping says %v", p, r, gate.Advised, advised)
				}
				if gate.Agrees() != (reported == advised) {
					t.Fatalf("(%s, %s, reported=%v) Agrees()=%v", p, r, reported, gate.Agrees())
				}
				pairs++
			}
		}
	}
	if want := len(vocabulary.Plausibilities()) * len(vocabulary.Risks()) * 2; pairs != want {
		t.Fatalf("covered %d combinations, want the full product set of %d", pairs, want)
	}
}

func TestVerdictRollGate_FailsClosedOutsideTheVocabulary(t *testing.T) {
	scalars := validVerdict().Scalars
	scalars.Risk = "severe"

	gate, err := scalars.RollGate()
	if err == nil {
		t.Fatal("an out-of-vocabulary risk produced a roll-gate expectation")
	}
	var unknown *vocabulary.UnknownValueError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
	}
	if gate != (payload.RollGateExpectation{}) {
		t.Fatalf("a refused comparison returned data: %+v", gate)
	}
}

// The expectation is consumed by the ledger writer and by cost/quality
// metrics, so it has to survive serialization carrying its own inputs.
func TestRollGateExpectation_SerializesAsSelfContainedStructuredData(t *testing.T) {
	verdict := validVerdict()
	verdict.Scalars.RequiresRoll = false
	verdict.Bands = bandsFor(false)

	gate, err := verdict.Scalars.RollGate()
	if err != nil {
		t.Fatalf("RollGate: %v", err)
	}

	data, err := json.Marshal(gate)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"plausibility", "risk", "reported", "advised"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("serialized expectation is missing %q: %s", key, data)
		}
	}

	var restored payload.RollGateExpectation
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal into the type: %v", err)
	}
	if restored != gate {
		t.Fatalf("round-trip altered the expectation: got %+v want %+v", restored, gate)
	}
	if restored.Agrees() {
		t.Fatal("a divergent expectation round-tripped as agreement")
	}
}

// Band shape follows the REPORTED gate, so the engine never demands bands the
// persona did not author and never accepts a verdict whose bands contradict
// its own gate.
func TestVerdict_BandShapeFollowsTheReportedGateNotTheMapping(t *testing.T) {
	t.Run("reported no-roll must not declare roll bands", func(t *testing.T) {
		verdict := validVerdict()
		verdict.Scalars.RequiresRoll = false
		// Bands left as miss/partial/full — what the mapping would advise.
		if err := verdict.Validate(); err == nil {
			t.Fatal("a no-roll verdict declaring three roll bands was accepted")
		}
	})

	t.Run("reported roll must not declare the auto band", func(t *testing.T) {
		verdict := validVerdict()
		verdict.Scalars.Plausibility = vocabulary.PlausibilityImpossible
		verdict.Bands = bandsFor(false)
		// Reported gate is still true, so the auto band contradicts it even
		// though the mapping advises exactly this shape.
		if err := verdict.Validate(); err == nil {
			t.Fatal("a rolling verdict declaring the auto band was accepted")
		}
	})
}

// Two invalid bands must always name the same one first. Ranging the map
// would make the rejection reason depend on Go's randomized iteration order,
// so an operator chasing a rejection would see a different cause per run.
func TestEffectBands_RejectionMessageIsStable(t *testing.T) {
	const attempts = 20
	messages := make(map[string]bool)
	for range attempts {
		verdict := validVerdict()
		bad := []payload.EffectIntent{{Type: "delete_entity", Target: testCharacter}}
		verdict.Bands[vocabulary.BandMiss] = bad
		verdict.Bands[vocabulary.BandFull] = bad

		err := verdict.Validate()
		if err == nil {
			t.Fatal("two out-of-vocabulary bands were accepted")
		}
		messages[err.Error()] = true
	}
	if len(messages) != 1 {
		t.Fatalf("%d attempts produced %d different rejection reasons: %v", attempts, len(messages), messages)
	}
}

// A no-roll verdict is a different contract, not a degenerate one: exactly
// one auto band, and nothing that implies the dice ran.
func TestVerdict_NoRollVerdictDeclaresExactlyTheAutoBand(t *testing.T) {
	verdict := &payload.Verdict{
		TurnID:   testTurnID,
		ActionID: testActionID,
		SceneID:  testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityImpossible,
			Risk:         vocabulary.RiskHigh,
			Consequence:  vocabulary.ConsequenceSetback,
			RequiresRoll: false,
		},
		Bands: payload.EffectBands{
			vocabulary.BandAuto: {
				{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusFrightened},
			},
		},
	}

	if err := verdict.Validate(); err != nil {
		t.Fatalf("a well-formed no-roll verdict was rejected: %v", err)
	}

	intents, err := verdict.Bands.For(vocabulary.BandAuto)
	if err != nil {
		t.Fatalf("auto band lookup: %v", err)
	}
	if len(intents) != 1 {
		t.Fatalf("auto band holds %d intents, want 1", len(intents))
	}

	for _, band := range vocabulary.RollBands() {
		if _, err := verdict.Bands.For(band); err == nil {
			t.Fatalf("a no-roll verdict must not resolve the %q band", band)
		}
	}
}

// "Declared but empty" and "not declared" must not collapse into the same
// silent no-op in the applier.
func TestEffectBands_EmptyBandIsNotAMissingBand(t *testing.T) {
	verdict := validVerdict()
	verdict.Bands[vocabulary.BandMiss] = []payload.EffectIntent{}

	if err := verdict.Validate(); err != nil {
		t.Fatalf("an empty but declared band is legal: %v", err)
	}
	intents, err := verdict.Bands.For(vocabulary.BandMiss)
	if err != nil {
		t.Fatalf("declared empty band must resolve: %v", err)
	}
	if len(intents) != 0 {
		t.Fatalf("expected no intents, got %d", len(intents))
	}

	if _, err := verdict.Bands.For("critical"); err == nil {
		t.Fatal("an undeclared band must be an error, not an empty result")
	}
}

// The distinction is load-bearing for the applier, and the applier reads
// verdicts off the wire — so it has to hold across serialization in BOTH
// shapes JSON can express an empty list. `[]` and `null` must resolve as a
// declared band with no intents; an absent key must not.
func TestEffectBands_EmptyBandSurvivesTheWireAsNullAndAsEmptyArray(t *testing.T) {
	declared := []struct {
		name     string
		intents  []payload.EffectIntent
		wantWire string
	}{
		{name: "explicit empty array", intents: []payload.EffectIntent{}, wantWire: `"miss":[]`},
		{name: "json null", intents: nil, wantWire: `"miss":null`},
	}

	for _, tc := range declared {
		t.Run(tc.name, func(t *testing.T) {
			verdict := validVerdict()
			verdict.Bands[vocabulary.BandMiss] = tc.intents

			body, err := json.Marshal(verdict)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(body), tc.wantWire) {
				t.Fatalf("the fixture did not serialize %s, so this case proves nothing: %s", tc.wantWire, body)
			}

			decoded, ok := decode(t, wireWithBody(t, verdict.Schema(), body)).(*payload.Verdict)
			if !ok {
				t.Fatal("decoder produced the wrong type")
			}
			if err := decoded.Validate(); err != nil {
				t.Fatalf("a declared but empty band was rejected after the wire: %v", err)
			}
			intents, err := decoded.Bands.For(vocabulary.BandMiss)
			if err != nil {
				t.Fatalf("a declared empty band did not resolve after the wire: %v", err)
			}
			if len(intents) != 0 {
				t.Fatalf("expected no intents, got %d", len(intents))
			}
		})
	}

	t.Run("an absent key is a missing band, not an empty one", func(t *testing.T) {
		verdict := validVerdict()
		delete(verdict.Bands, vocabulary.BandMiss)

		body, err := json.Marshal(verdict)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(body), `"miss"`) {
			t.Fatalf("the deleted band still serialized, so this case proves nothing: %s", body)
		}

		decoded, ok := decode(t, wireWithBody(t, verdict.Schema(), body)).(*payload.Verdict)
		if !ok {
			t.Fatal("decoder produced the wrong type")
		}
		if err := decoded.Validate(); err == nil {
			t.Fatal("a verdict missing a required band was accepted after the wire")
		}
		if _, err := decoded.Bands.For(vocabulary.BandMiss); err == nil {
			t.Fatal("a band absent from the wire resolved as an empty one; the applier would silently no-op")
		}
	})
}

// The F6 split: only the four scalars plus one reference ever become triples.
func TestVerdictTriples_EmitOnlyTheRuleMatchedScalarsPlusOneReference(t *testing.T) {
	verdict := validVerdict()
	const verdictRef = "obj://verdicts/turn-act-1"

	triples, err := verdict.Triples(testTurnEntity, verdictRef, "adjudicator", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	want := map[string]any{
		vocabulary.TurnVerdictPlausibility.String(): string(verdict.Scalars.Plausibility),
		vocabulary.TurnVerdictRisk.String():         string(verdict.Scalars.Risk),
		vocabulary.TurnVerdictConsequence.String():  string(verdict.Scalars.Consequence),
		vocabulary.TurnVerdictRequiresRoll.String(): verdict.Scalars.RequiresRoll,
		vocabulary.TurnVerdictRef.String():          verdictRef,
	}
	if len(triples) != len(want) {
		t.Fatalf("emitted %d triples, want exactly %d", len(triples), len(want))
	}

	seen := make(map[string]bool, len(triples))
	for _, triple := range triples {
		expected, registered := want[triple.Predicate]
		if !registered {
			t.Fatalf("verdict emitted unexpected predicate %q; bulky fields must ride by reference", triple.Predicate)
		}
		if seen[triple.Predicate] {
			t.Fatalf("predicate %q emitted twice; single-valued predicates must replace, not accumulate", triple.Predicate)
		}
		seen[triple.Predicate] = true

		if triple.Object != expected {
			t.Fatalf("predicate %q object = %#v, want %#v", triple.Predicate, triple.Object, expected)
		}
		if triple.Subject != testTurnEntity {
			t.Fatalf("predicate %q lands on %q, want the turn entity %q", triple.Predicate, triple.Subject, testTurnEntity)
		}
		if triple.Context != verdict.TurnID {
			t.Fatalf("predicate %q carries context %q, want the turn id %q", triple.Predicate, triple.Context, verdict.TurnID)
		}
		if _, parseErr := ssvocab.ParsePredicate(triple.Predicate); parseErr != nil {
			t.Fatalf("the write gate would reject emitted predicate %q: %v", triple.Predicate, parseErr)
		}
	}
}

// requires-roll must reach the graph as a boolean so the rule engine's
// bool eq/ne operator can branch on it. Emitting "true" as a string would
// still write, and the dice branch would quietly never fire.
func TestVerdictTriples_RequiresRollIsABooleanObject(t *testing.T) {
	for _, requiresRoll := range []bool{true, false} {
		verdict := validVerdict()
		if !requiresRoll {
			verdict.Scalars.Risk = vocabulary.RiskNone
			verdict.Scalars.RequiresRoll = false
			verdict.Bands = payload.EffectBands{vocabulary.BandAuto: nil}
		}

		triples, err := verdict.Triples(testTurnEntity, "obj://verdicts/x", "adjudicator", testTime)
		if err != nil {
			t.Fatalf("Triples: %v", err)
		}

		found := false
		for _, triple := range triples {
			if triple.Predicate != vocabulary.TurnVerdictRequiresRoll.String() {
				continue
			}
			found = true
			value, ok := triple.Object.(bool)
			if !ok {
				t.Fatalf("requires-roll object is %T, want bool", triple.Object)
			}
			if value != requiresRoll {
				t.Fatalf("requires-roll object = %v, want %v", value, requiresRoll)
			}
		}
		if !found {
			t.Fatal("no requires-roll triple emitted")
		}
	}
}

// The bulky half must not leak into the graph's rule-matching surface. This
// searches the serialized triples for content that only lives in the payload.
func TestVerdictTriples_CarryNoBulkyOrRuleOpaqueContent(t *testing.T) {
	verdict := validVerdict()
	triples, err := verdict.Triples(testTurnEntity, "obj://verdicts/turn-act-1", "adjudicator", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	serialized, err := json.Marshal(triples)
	if err != nil {
		t.Fatalf("marshal triples: %v", err)
	}
	rendered := string(serialized)

	forbidden := map[string]string{
		"the adjudicator's free-text rationale": verdict.Rationale,
		"a modifier note":                       verdict.Modifiers[0].Note,
		"a banded intent target":                testAlly,
		"a banded intent type":                  string(vocabulary.EffectMoveEntity),
		"a band name":                           string(vocabulary.BandPartial),
	}
	for what, needle := range forbidden {
		if needle == "" {
			t.Fatalf("the fixture does not populate %s; this check would be vacuous", what)
		}
		if strings.Contains(rendered, needle) {
			t.Fatalf("verdict triples leak %s (%q) into the graph: %s", what, needle, rendered)
		}
	}
}

func TestVerdictTriples_RefuseToEmitAnInvalidVerdict(t *testing.T) {
	verdict := validVerdict()
	verdict.Scalars.Risk = "severe"

	if _, err := verdict.Triples(testTurnEntity, "obj://verdicts/x", "adjudicator", testTime); err == nil {
		t.Fatal("an invalid verdict must not produce triples")
	}
}

func TestVerdictTriples_RequireATurnEntityAndAReference(t *testing.T) {
	verdict := validVerdict()

	if _, err := verdict.Triples("not-an-entity-id", "obj://verdicts/x", "adjudicator", testTime); err == nil {
		t.Fatal("triples must not be emitted onto a non-canonical subject")
	}
	if _, err := verdict.Triples(testTurnEntity, "", "adjudicator", testTime); err == nil {
		t.Fatal("the bulky verdict must always be reachable; an empty reference loses it")
	}
}
