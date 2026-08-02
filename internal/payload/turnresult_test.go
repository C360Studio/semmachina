package payload_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// validFailedTurnResult is the FULLEST failed shape: a turn stranded in
// `narrating` after its re-trigger budget ran out, with the narration already
// landed and the whole resolution behind it.
//
// That combination is not hypothetical — internal/resume ends exactly this turn
// with vocabulary.FailureTurnStranded — and it is the fixture because it is the
// one that would break if failed results were modelled as "a reason and nothing
// else".
func validFailedTurnResult() *payload.TurnResult {
	result := validTurnResult()
	result.Phase = vocabulary.PhaseFailed
	result.FailureReason = vocabulary.FailureTurnStranded
	return result
}

func TestTurnResult_ValidFixturesAreAccepted(t *testing.T) {
	for name, result := range map[string]*payload.TurnResult{
		"complete": validTurnResult(),
		"failed":   validFailedTurnResult(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := result.Validate(); err != nil {
				t.Fatalf("the valid %s fixture was rejected: %v", name, err)
			}
		})
	}
}

// The failed shape gets its own round trip through the production decoder. The
// registry test covers the complete one; this is the half where every optional
// field is populated at once, so a marshalling bug in the failure branch cannot
// hide behind the complete fixture's empty failure_reason.
func TestTurnResult_FailedShapeRoundTripsThroughTheProductionDecoder(t *testing.T) {
	original := validFailedTurnResult()
	assertFixtureFullyPopulated(t, original)

	decoded := decode(t, publish(t, original))
	result, ok := decoded.(*payload.TurnResult)
	if !ok {
		t.Fatalf("the decoder produced %T", decoded)
	}
	if !reflect.DeepEqual(result, original) {
		t.Fatalf("round-trip lost or altered fields:\n got %#v\nwant %#v", result, original)
	}
}

func TestCompanionResolution_ValidatesTheClosedPublicShape(t *testing.T) {
	validHint := func() *payload.CompanionResolution {
		return &payload.CompanionResolution{
			CompanionID: testAlly,
			Kind:        payload.CompanionDecisionHint,
			HintLevel:   vocabulary.HintLevelConnect,
		}
	}

	for name, mutate := range map[string]func(*payload.CompanionResolution){
		"missing companion": func(r *payload.CompanionResolution) { r.CompanionID = "" },
		"unknown kind":      func(r *payload.CompanionResolution) { r.Kind = "aside" },
		"hint missing level": func(r *payload.CompanionResolution) {
			r.HintLevel = ""
		},
		"hint unknown level": func(r *payload.CompanionResolution) {
			r.HintLevel = "obvious"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validHint()
			mutate(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid companion resolution was accepted")
			}
		})
	}

	for _, kind := range payload.CompanionDecisionKinds() {
		t.Run(string(kind), func(t *testing.T) {
			candidate := validHint()
			candidate.Kind = kind
			if kind != payload.CompanionDecisionHint {
				candidate.HintLevel = ""
			}
			if err := candidate.Validate(); err != nil {
				t.Fatalf("registered kind %q was rejected: %v", kind, err)
			}
		})
	}
}

func TestCompanionResolution_HintLevelIsPresentExactlyForHint(t *testing.T) {
	for _, kind := range payload.CompanionDecisionKinds() {
		if kind == payload.CompanionDecisionHint {
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			resolution := &payload.CompanionResolution{
				CompanionID: testAlly,
				Kind:        kind,
				HintLevel:   vocabulary.HintLevelNudge,
			}
			if err := resolution.Validate(); err == nil {
				t.Fatal("a non-hint companion resolution carrying hint_level was accepted")
			}
		})
	}
}

func TestTurnResult_CompanionResolutionJSONOptionality(t *testing.T) {
	for name, tc := range map[string]struct {
		companion   *payload.CompanionResolution
		wantPresent bool
	}{
		"no companion stage summary": {companion: nil, wantPresent: false},
		"active silent companion": {
			companion:   &payload.CompanionResolution{CompanionID: testAlly, Kind: payload.CompanionDecisionSilent},
			wantPresent: true,
		},
		"active hinting companion": {
			companion: &payload.CompanionResolution{
				CompanionID: testAlly, Kind: payload.CompanionDecisionHint, HintLevel: vocabulary.HintLevelConnect,
			},
			wantPresent: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := validTurnResult()
			result.CompanionResolution = tc.companion
			body, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatalf("decode result JSON: %v", err)
			}
			_, present := wire["companion_resolution"]
			if present != tc.wantPresent {
				t.Fatalf("companion_resolution presence = %v, want %v: %s", present, tc.wantPresent, body)
			}
			if _, shortened := wire["companion"]; shortened {
				t.Fatalf("public result used the uncontracted shortened key companion: %s", body)
			}
		})
	}
}

// One type, both endings, and the phase is what says which arrived. A result
// carrying a phase the turn is still PASSING THROUGH would tell a player their
// turn is over while it runs.
func TestTurnResult_RefusesAPhaseThatIsNotAnEnding(t *testing.T) {
	for _, phase := range vocabulary.TurnPhases() {
		if phase.IsTerminal() {
			continue
		}
		t.Run(string(phase), func(t *testing.T) {
			result := validTurnResult()
			result.Phase = phase
			err := result.Validate()
			if err == nil {
				t.Fatalf("a result claiming the non-terminal phase %q was accepted", phase)
			}
			if !strings.Contains(err.Error(), "terminal") {
				t.Fatalf("the refusal is %q and does not say why a mid-flight phase is not a result", err)
			}
		})
	}

	t.Run("outside the vocabulary", func(t *testing.T) {
		result := validTurnResult()
		result.Phase = "finished"
		if err := result.Validate(); err == nil {
			t.Fatal("a result claiming an out-of-vocabulary phase was accepted")
		}
	})
}

func TestTurnResult_CompleteRequiresWhatACompletedTurnAlwaysHas(t *testing.T) {
	t.Run("narration", func(t *testing.T) {
		result := validTurnResult()
		result.NarrationRef = ""
		err := result.Validate()
		if err == nil {
			t.Fatal("a completed turn with no prose to show was accepted")
		}
		if !strings.Contains(err.Error(), "narration_ref") {
			t.Fatalf("the refusal is %q and does not name the missing field", err)
		}
	})

	t.Run("resolution", func(t *testing.T) {
		result := validTurnResult()
		result.Resolution = nil
		err := result.Validate()
		if err == nil {
			t.Fatal("a completed turn with no resolution was accepted; the outcome would read as arbitrary")
		}
		if !strings.Contains(err.Error(), "resolution") {
			t.Fatalf("the refusal is %q and does not name the missing field", err)
		}
	})

	t.Run("and refuses a failure reason", func(t *testing.T) {
		result := validTurnResult()
		result.FailureReason = vocabulary.FailureEffectInvalid
		if err := result.Validate(); err == nil {
			t.Fatal("a completed turn carrying a failure reason was accepted")
		}
	})
}

// Bounded execution promises a turn never ends in silence. An unexplained failed
// turn is that promise unmet at the one surface the player reads.
func TestTurnResult_FailedRequiresAClosedReasonCode(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		result := validFailedTurnResult()
		result.FailureReason = ""
		if err := result.Validate(); err == nil {
			t.Fatal("a failed turn with no reason was accepted")
		}
	})

	// A CODE, never a sentence. Free text here would put engine- or
	// persona-authored prose in front of a player as if it were the fiction, and
	// would give every consumer downstream a value it cannot group on.
	for name, reason := range map[string]vocabulary.FailureReason{
		"a sentence":           "the applier could not reach the graph",
		"a near-miss code":     "effect_invalid",
		"an upstream-ish code": "internal_error",
	} {
		t.Run(name, func(t *testing.T) {
			result := validFailedTurnResult()
			result.FailureReason = reason
			if err := result.Validate(); err == nil {
				t.Fatalf("a failed turn reported %q, which is outside the closed set", reason)
			}
		})
	}

	// Anti-vacuity: every registered reason must be accepted, or the refusals
	// above could be passing for a reason unrelated to closure.
	for _, reason := range vocabulary.FailureReasons() {
		t.Run(string(reason), func(t *testing.T) {
			result := validFailedTurnResult()
			result.FailureReason = reason
			if err := result.Validate(); err != nil {
				t.Fatalf("the registered reason %q was rejected: %v", reason, err)
			}
		})
	}
}

// How far the turn got is what decides which optional halves a failed result
// carries, and every one of these shapes is a turn the engine really produces.
func TestTurnResult_FailedCarriesAsMuchAsTheTurnGotTo(t *testing.T) {
	t.Run("stranded before adjudication: no resolution, no narration", func(t *testing.T) {
		result := validFailedTurnResult()
		result.Resolution = nil
		result.NarrationRef = ""
		result.FailureReason = vocabulary.FailurePersonaLoopFailed
		if err := result.Validate(); err != nil {
			t.Fatalf("a turn that died before it was judged could not be reported: %v", err)
		}
	})

	t.Run("stranded after adjudication, before the dice: no band, no roll", func(t *testing.T) {
		result := validFailedTurnResult()
		result.NarrationRef = ""
		result.Resolution = &payload.TurnResolution{Verdict: validVerdict().Scalars}
		if err := result.Validate(); err != nil {
			t.Fatalf("a turn stranded between the verdict and the dice could not be reported: %v", err)
		}
	})

	t.Run("failed during effect commit: banded, rolled, unnarrated", func(t *testing.T) {
		result := validFailedTurnResult()
		result.NarrationRef = ""
		result.FailureReason = vocabulary.FailureEffectCommitIncomplete
		if err := result.Validate(); err != nil {
			t.Fatalf("a turn that failed after the dice could not be reported: %v", err)
		}
	})
}

// A completed turn selected a band by definition: the applier commits the
// intents of exactly one. The empty band is a mid-flight state and must not be
// reachable through a result that says the turn finished.
func TestTurnResult_CompleteRefusesAResolutionWithNoOutcome(t *testing.T) {
	result := validTurnResult()
	result.Resolution = &payload.TurnResolution{Verdict: validVerdict().Scalars}

	err := result.Validate()
	if err == nil {
		t.Fatal("a completed turn recorded no outcome band")
	}
	if !strings.Contains(err.Error(), "band") {
		t.Fatalf("the refusal is %q and does not name the missing outcome", err)
	}
}

// Turn and action are 1:1 by a derivation the engine owns. A result naming turn
// A and action B is retrievable by a key that answers with the other turn's
// outcome.
func TestTurnResult_RefusesAnUnpairedTurnAndAction(t *testing.T) {
	t.Run("a different action", func(t *testing.T) {
		result := validTurnResult()
		result.ActionID = "act-9"
		if err := result.Validate(); err == nil {
			t.Fatal("a result naming turn-act-1 and action act-9 was accepted")
		}
	})

	t.Run("a turn id no action derives", func(t *testing.T) {
		result := validTurnResult()
		result.TurnID = "act-1"
		if err := result.Validate(); err == nil {
			t.Fatal("a result whose turn id carries no turn- prefix was accepted")
		}
	})

	// The derivation is also where turn_id is checked as an entity-ID segment,
	// so these must be refused by it rather than by a check in front of it.
	for name, turnID := range map[string]string{
		"absent":        "",
		"two segments":  "turn.act-1",
		"not a segment": "-leading",
	} {
		t.Run(name, func(t *testing.T) {
			result := validTurnResult()
			result.TurnID = turnID
			if err := result.Validate(); err == nil {
				t.Fatalf("a result filed under the unusable turn id %q was accepted", turnID)
			}
		})
	}
}

func TestTurnResult_RefusesAPlayerThatIsNotAGraphEntity(t *testing.T) {
	for name, playerID := range map[string]string{
		"a connection id": "ws-conn-7",
		"a bare name":     "rook",
		"absent":          "",
	} {
		t.Run(name, func(t *testing.T) {
			result := validTurnResult()
			result.PlayerID = playerID
			if err := result.Validate(); err == nil {
				t.Fatalf("a result addressed to %q was accepted; delivery resolves a connection FROM this "+
					"field, so anything that is not durable identity breaks reconnect", playerID)
			}
		})
	}
}

// The narration is carried as a POINTER, and the *.ref grammar is what stops a
// sentence being handed to the field. A result is the one place prose is
// expected, which is exactly why the pointer must still be a pointer.
func TestTurnResult_RefusesProseWhereAPointerBelongs(t *testing.T) {
	for name, ref := range map[string]string{
		"a sentence":       "The gate lifts a hand's width and stops.",
		"a bare key":       "turn/turn-act-1/narration",
		"no key":           "obj://SEMMACHINA_CONTENT",
		"no instance":      "obj:///turn/turn-act-1/narration",
		"a different verb": "https://example.com/prose",
	} {
		t.Run(name, func(t *testing.T) {
			result := validTurnResult()
			result.NarrationRef = ref
			if err := result.Validate(); err == nil {
				t.Fatalf("%q was accepted as a narration reference", ref)
			}

			// The same grammar holds on a failed turn, where narration is
			// optional — optional must not mean unchecked.
			failed := validFailedTurnResult()
			failed.NarrationRef = ref
			if err := failed.Validate(); err == nil {
				t.Fatalf("%q was accepted as a failed turn's narration reference", ref)
			}
		})
	}
}

func TestTurnResult_RefusesAProtocolVersionThisEngineDoesNotSpeak(t *testing.T) {
	for _, version := range []payload.PlayerProtocolVersion{"", "player/v2", "v1"} {
		t.Run(string(version), func(t *testing.T) {
			result := validTurnResult()
			result.Protocol = version
			if err := result.Validate(); !errors.Is(err, payload.ErrUnsupportedProtocol) {
				t.Fatalf("protocol %q was accepted or misclassified: %v", version, err)
			}
		})
	}
}

// ResolvedAt is when the turn ENDED, not when the result was composed or
// delivered. A result that cannot date itself leaves an email-cadence player
// unable to tell which of their turns they are reading.
func TestTurnResult_RefusesAResultItCannotDate(t *testing.T) {
	result := validTurnResult()
	result.ResolvedAt = time.Time{}
	if err := result.Validate(); err == nil {
		t.Fatal("a result with no resolution time was accepted")
	}
}

// The protocol's stated asymmetry: a request is strict and a result is additive.
// A client or adapter built against v1 must keep working when a later engine
// adds a result field, which is what makes adding one cheap.
func TestTurnResult_ToleratesAFieldFromALaterVersion(t *testing.T) {
	wire, err := json.Marshal(validTurnResult())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	extended := strings.Replace(string(wire), "{", `{"world_time":1720000000,`, 1)

	var result payload.TurnResult
	if err := json.Unmarshal([]byte(extended), &result); err != nil {
		t.Fatalf("a v1 reader refused a result carrying a later version's field: %v", err)
	}
	if !reflect.DeepEqual(&result, validTurnResult()) {
		t.Fatalf("the unknown field disturbed the decode: %#v", result)
	}

	// Anti-vacuity in the other direction: the SUBMISSION side must refuse the
	// same shape, or "the asymmetry is deliberate" would be describing one rule.
	var submission payload.SubmitAction
	if err := json.Unmarshal([]byte(`{"protocol":"player/v1","text":"x","idempotency_key":"k",`+
		`"world_time":1}`), &submission); err == nil {
		t.Fatal("a submission carrying an undefined field was accepted; the asymmetry is not implemented")
	}
}

// ----------------------------------------------------------- the resolution

// The band is carried rather than derived, so the two can disagree — and this is
// the disagreement that matters: a card showing a band whose effects are NOT the
// ones on the world.
func TestTurnResolution_RefusesABandItsOwnDiceDoNotProduce(t *testing.T) {
	resolution := validTurnResolution()
	resolution.Band = vocabulary.BandFull // the fixture rolls 4+4+1 = 9, a partial

	err := resolution.Validate(true)
	if err == nil {
		t.Fatal("a resolution banded full on a total of 9 was accepted")
	}
	if !strings.Contains(err.Error(), "9") {
		t.Fatalf("the refusal is %q and does not name the total that contradicts the band", err)
	}
}

func TestTurnResolution_RefusesADisagreementBetweenTheGateAndTheDice(t *testing.T) {
	t.Run("dice nobody called for", func(t *testing.T) {
		resolution := validTurnResolution()
		resolution.Verdict.RequiresRoll = false
		if err := resolution.Validate(true); err == nil {
			t.Fatal("a resolution carrying a roll under a verdict that declined the dice was accepted")
		}
	})

	t.Run("a roll band with no roll", func(t *testing.T) {
		resolution := validTurnResolution()
		resolution.Roll = nil
		if err := resolution.Validate(true); err == nil {
			t.Fatal("a resolution banded partial with no dice was accepted")
		}
	})

	// The case above is also refused because its verdict called for dice it does
	// not have; this isolates the band rule itself. A verdict that declined the
	// dice, banded partial, with no roll to contradict, is internally consistent
	// everywhere EXCEPT that only auto resolves without dice.
	t.Run("a roll band from a verdict that threw no dice", func(t *testing.T) {
		resolution := validTurnResolution()
		resolution.Roll = nil
		resolution.Verdict.RequiresRoll = false
		if err := resolution.Validate(true); err == nil {
			t.Fatal("a no-roll verdict resolved in the partial band; only auto resolves without dice")
		}
	})

	t.Run("auto under a gate that called for dice", func(t *testing.T) {
		resolution := validTurnResolution()
		resolution.Roll = nil
		resolution.Band = vocabulary.BandAuto
		if err := resolution.Validate(true); err == nil {
			t.Fatal("a roll-requiring verdict resolved in the auto band")
		}
	})

	// Auto with dice is caught twice over — no total ever bands as auto — so the
	// MESSAGE is the assertion. The check earns its place by diagnosing the
	// contradiction rather than reporting the downstream symptom ("auto does not
	// match total 9, expected partial"), which reads as an arithmetic fault in a
	// resolution whose arithmetic is fine.
	t.Run("auto with dice", func(t *testing.T) {
		resolution := validTurnResolution()
		resolution.Band = vocabulary.BandAuto

		err := resolution.Validate(true)
		if err == nil {
			t.Fatal("a resolution banded auto carried a roll; auto is the band of a verdict that threw none")
		}
		if !strings.Contains(err.Error(), "not selectable by a roll") {
			t.Fatalf("the refusal is %q, which diagnoses the total rather than the contradiction", err)
		}
	})

	t.Run("dice with no band at all", func(t *testing.T) {
		resolution := validTurnResolution()
		resolution.Band = ""
		if err := resolution.Validate(false); err == nil {
			t.Fatal("a resolution threw dice and recorded no band")
		}
	})
}

// The no-roll shape is a first-class outcome, not a degenerate one: a verdict
// may decline the dice entirely and the applier commits the auto band.
func TestTurnResolution_AcceptsANoRollOutcome(t *testing.T) {
	scalars := validVerdict().Scalars
	scalars.RequiresRoll = false

	resolution := &payload.TurnResolution{Verdict: scalars, Band: vocabulary.BandAuto}
	if err := resolution.Validate(true); err != nil {
		t.Fatalf("a no-roll resolution was rejected: %v", err)
	}
}

func TestTurnResolution_RefusesAVerdictOutsideTheClosedVocabulary(t *testing.T) {
	for name, mutate := range map[string]func(*payload.VerdictScalars){
		"plausibility": func(s *payload.VerdictScalars) { s.Plausibility = "likely" },
		"risk":         func(s *payload.VerdictScalars) { s.Risk = "extreme" },
		"consequence":  func(s *payload.VerdictScalars) { s.Consequence = "doom" },
	} {
		t.Run(name, func(t *testing.T) {
			resolution := validTurnResolution()
			mutate(&resolution.Verdict)
			if err := resolution.Validate(true); err == nil {
				t.Fatalf("an out-of-vocabulary %s reached a player-facing result", name)
			}
		})
	}
}

// The card explains a total; a card whose arithmetic does not add up explains
// nothing. This is the shared validateRollArithmetic the ledger's RollResult
// uses, reached through the public protocol.
func TestTurnRoll_RefusesArithmeticThatDoesNotAddUp(t *testing.T) {
	for name, mutate := range map[string]func(*payload.TurnRoll){
		"a total that is not the sum": func(r *payload.TurnRoll) { r.Total = 12 },
		"a modifier total that is not the modifiers": func(r *payload.TurnRoll) {
			r.ModifierTotal = 2
		},
		// Three legal dice summing to the recorded total: every other check in
		// the chain passes, so only the dice COUNT can refuse it. A record with
		// too few dice that also broke the sum would be caught by the sum, and
		// the count check would never have to be right.
		"a die too many":          func(r *payload.TurnRoll) { r.Dice = []int{3, 3, 2} },
		"too few dice":            func(r *payload.TurnRoll) { r.Dice = []int{4} },
		"a die outside its faces": func(r *payload.TurnRoll) { r.Dice = []int{4, 7} },
		"an unregistered mechanic": func(r *payload.TurnRoll) {
			r.Mechanic = "2d6-pbta/v9"
		},
		"a modifier outside its bound": func(r *payload.TurnRoll) {
			r.Modifiers = []payload.Modifier{{Source: vocabulary.ModifierEquipment, Value: 9}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolution := validTurnResolution()
			mutate(resolution.Roll)
			if err := resolution.Validate(true); err == nil {
				t.Fatalf("a roll with %s was accepted", name)
			}
			if err := resolution.Roll.Validate(); err == nil {
				t.Fatalf("TurnRoll.Validate accepted %s", name)
			}
		})
	}
}
