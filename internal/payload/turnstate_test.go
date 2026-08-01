package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// testActionRef is the shape a reference actually has: a pointer, not a
// sentence. The turn-state contract only requires "present and bounded" — the
// grammar belongs to the store that mints it — but a test that used a bare word
// would let a later reader think anything goes here.
const testActionRef = "obj://SEMMACHINA_CONTENT/turn/" + testTurnID + "/action"

func acceptedState() *payload.TurnState {
	return &payload.TurnState{
		TurnID:    testTurnID,
		Phase:     vocabulary.PhaseAccepted,
		PlayerID:  testPlayerID,
		SceneID:   testSceneID,
		ActionRef: testActionRef,
	}
}

func phaseState(phase vocabulary.TurnPhase) *payload.TurnState {
	return &payload.TurnState{TurnID: testTurnID, Phase: phase}
}

func failedState(reason vocabulary.FailureReason) *payload.TurnState {
	return &payload.TurnState{TurnID: testTurnID, Phase: vocabulary.PhaseFailed, Reason: reason}
}

// The birth record is the only write that establishes who is playing, where,
// and what they said. Every later stage reads the scene off the turn and the
// action off the reference, so a birth record missing either is a turn nothing
// downstream can assemble context for or re-prompt.
func TestTurnState_AcceptedRecordCarriesThePhasePlayerSceneAndActionReference(t *testing.T) {
	triples, err := acceptedState().Triples(testTurnEntity, "turn-recorder", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	want := map[string]any{
		vocabulary.TurnPhaseCurrent.String(): string(vocabulary.PhaseAccepted),
		vocabulary.TurnActionPlayer.String(): testPlayerID,
		vocabulary.TurnActionScene.String():  testSceneID,
		vocabulary.TurnActionRef.String():    testActionRef,
	}
	if len(triples) != len(want) {
		t.Fatalf("emitted %d triples, want %d: %+v", len(triples), len(want), triples)
	}
	for _, triple := range triples {
		expected, registered := want[triple.Predicate]
		if !registered {
			t.Fatalf("the accepted record wrote %q, which is not part of a turn's birth", triple.Predicate)
		}
		if triple.Object != expected {
			t.Fatalf("%q carries %v, want %v", triple.Predicate, triple.Object, expected)
		}
		if triple.Subject != testTurnEntity {
			t.Fatalf("%q lands on %q, want the turn entity", triple.Predicate, triple.Subject)
		}
		if triple.Context != testTurnID {
			t.Fatalf("%q carries context %q, want the turn id", triple.Predicate, triple.Context)
		}
	}
}

// The reference rides in the SAME projection as the phase, so the caller has
// one write to issue and no window between them. A projection that emitted the
// reference separately would look identical at the call site and reopen the
// crash window the atomic create exists to close.
func TestTurnState_AnAcceptedRecordWithNoActionReferenceIsRefused(t *testing.T) {
	state := acceptedState()
	state.ActionRef = ""

	if _, err := state.Triples(testTurnEntity, "turn-recorder", testTime); err == nil {
		t.Fatal("a turn was born with no pointer to the player's words; the rule pack can re-trigger it and " +
			"the adjudicator cannot re-prompt it")
	}
}

func TestTurnState_OrdinaryTransitionWritesOnlyThePhase(t *testing.T) {
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseCompanion, vocabulary.PhaseNarrating, vocabulary.PhaseComplete,
	} {
		t.Run(string(phase), func(t *testing.T) {
			triples, err := phaseState(phase).Triples(testTurnEntity, "turn-recorder", testTime)
			if err != nil {
				t.Fatalf("Triples: %v", err)
			}
			if len(triples) != 1 {
				t.Fatalf("emitted %d triples, want only the phase: %+v", len(triples), triples)
			}
			if triples[0].Predicate != vocabulary.TurnPhaseCurrent.String() {
				t.Fatalf("emitted %q, want %q", triples[0].Predicate, vocabulary.TurnPhaseCurrent)
			}
			if triples[0].Object != string(phase) {
				t.Fatalf("emitted object %v, want %q", triples[0].Object, phase)
			}
		})
	}
}

// The phase and the reason land in ONE write. Two writes would leave a window in
// which the turn is failed with nothing to explain it, or carries a reason while
// still reporting itself as running.
func TestTurnState_FailureWritesThePhaseAndTheReasonTogether(t *testing.T) {
	triples, err := failedState(vocabulary.FailureEffectInvalid).
		Triples(testTurnEntity, "turn-recorder", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != 2 {
		t.Fatalf("emitted %d triples, want the phase and the reason: %+v", len(triples), triples)
	}

	got := map[string]any{}
	for _, triple := range triples {
		got[triple.Predicate] = triple.Object
	}
	if got[vocabulary.TurnPhaseCurrent.String()] != string(vocabulary.PhaseFailed) {
		t.Fatalf("phase is %v", got[vocabulary.TurnPhaseCurrent.String()])
	}
	if got[vocabulary.TurnFailureReason.String()] != string(vocabulary.FailureEffectInvalid) {
		t.Fatalf("reason is %v", got[vocabulary.TurnFailureReason.String()])
	}
}

func TestTurnState_FailureDetailRidesAsAReference(t *testing.T) {
	const detail = "obj://SEMMACHINA_CONTENT/turn/" + testTurnID + "/failure"

	state := failedState(vocabulary.FailureEffectInvalid)
	state.DetailRef = detail

	triples, err := state.Triples(testTurnEntity, "turn-recorder", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != 3 {
		t.Fatalf("emitted %d triples, want phase, reason, and the detail reference: %+v", len(triples), triples)
	}
	last := triples[len(triples)-1]
	if last.Predicate != vocabulary.TurnFailureRef.String() || last.Object != detail {
		t.Fatalf("the last triple is %q=%v, want the detail reference", last.Predicate, last.Object)
	}
}

// The whole point of the closed reason code is that free text cannot reach the
// rule-matching surface. The reason field is the obvious place a sentence would
// be put, so it is closed at the payload boundary — before the projection, which
// gates shape and would happily carry a short sentence.
func TestTurnState_RejectsAReasonOutsideTheClosedSet(t *testing.T) {
	state := failedState("intent 3 exceeded the health bound")

	if _, err := state.Triples(testTurnEntity, "turn-recorder", testTime); err == nil {
		t.Fatal("a hand-written failure sentence was projected onto the turn entity")
	}

	// Anti-vacuity: the same sentence is inside the projection's SHAPE budget,
	// so nothing but closure would have stopped it.
	if len(state.Reason) > payload.MaxTripleObjectBytes {
		t.Fatal("the sentence is too long to pass the shape gate, so this proves nothing about closure")
	}
}

// TurnState is a DECODABLE wire payload, so "the store validates the references
// it mints" is not an enforcement point (F16): a decoded record can carry
// anything in a ref field. The projection is where a reference reaches the
// graph, so that is where the grammar has to hold — for the action reference the
// birth record requires, and for the failure detail reference, which is the one
// most likely to be handed an explanation instead of a pointer.
func TestTurnState_RefusesASentenceWhereAReferenceGoes(t *testing.T) {
	const explanation = "the applier refused the batch because health would go to 43"

	accepted := acceptedState()
	accepted.ActionRef = explanation
	if _, err := accepted.Triples(testTurnEntity, "turn-recorder", testTime); err == nil {
		t.Fatal("a sentence was projected onto turn.action.ref; free text reached the surface rules match")
	}

	failed := failedState(vocabulary.FailureEffectInvalid)
	failed.DetailRef = explanation
	if _, err := failed.Triples(testTurnEntity, "turn-recorder", testTime); err == nil {
		t.Fatal("a sentence was projected onto turn.failure.ref, which is exactly the closed reason code's " +
			"hole relocated one predicate over")
	}

	// Anti-vacuity: the record's own Validate accepts it — it asks only that a
	// reference be present and bounded — so nothing but the projection's grammar
	// check would have stopped it.
	if err := accepted.Validate(); err != nil {
		t.Fatalf("the payload contract already refused the sentence (%v); this proves nothing about the "+
			"projection", err)
	}
	if len(explanation) > payload.MaxTripleObjectBytes {
		t.Fatal("the sentence is past the shape budget, so the shape gate would have caught it anyway")
	}
}

func TestTurnState_RejectsEveryMisshapedRecord(t *testing.T) {
	cases := []struct {
		name    string
		state   *payload.TurnState
		wantErr string
	}{
		{
			name: "an accepted record with no player",
			state: &payload.TurnState{
				TurnID: testTurnID, Phase: vocabulary.PhaseAccepted,
				SceneID: testSceneID, ActionRef: testActionRef,
			},
			wantErr: "player_id",
		},
		{
			name: "an accepted record with no scene",
			state: &payload.TurnState{
				TurnID: testTurnID, Phase: vocabulary.PhaseAccepted,
				PlayerID: testPlayerID, ActionRef: testActionRef,
			},
			wantErr: "scene_id",
		},
		{
			name: "an accepted record with no action reference",
			state: &payload.TurnState{
				TurnID: testTurnID, Phase: vocabulary.PhaseAccepted,
				PlayerID: testPlayerID, SceneID: testSceneID,
			},
			wantErr: "action_ref",
		},
		{
			name: "a transition that resends the player",
			state: &payload.TurnState{
				TurnID: testTurnID, Phase: vocabulary.PhaseApplying, PlayerID: testPlayerID,
			},
			wantErr: "rewrite who is playing",
		},
		{
			name: "a transition that resends the action reference",
			state: &payload.TurnState{
				TurnID: testTurnID, Phase: vocabulary.PhaseApplying, ActionRef: testActionRef,
			},
			wantErr: "another turn's words",
		},
		{
			name:    "a failed record with no reason",
			state:   failedState(""),
			wantErr: "failure_reason",
		},
		{
			name:    "a reason on a turn that did not fail",
			state:   &payload.TurnState{TurnID: testTurnID, Phase: vocabulary.PhaseComplete, Reason: vocabulary.FailureEffectInvalid},
			wantErr: "only a failed turn has a reason",
		},
		{
			name:    "a phase outside the closed set",
			state:   phaseState("adjudicatin"),
			wantErr: "turn_phase",
		},
		{
			name: "a detail reference on a turn that did not fail",
			state: &payload.TurnState{
				TurnID: testTurnID, Phase: vocabulary.PhaseComplete,
				DetailRef: "obj://SEMMACHINA_CONTENT/turn/" + testTurnID + "/failure",
			},
			wantErr: "a turn that did not fail has none",
		},
		{
			name:    "a player id that is not a canonical entity id",
			state:   &payload.TurnState{TurnID: testTurnID, Phase: vocabulary.PhaseAccepted, PlayerID: "p1", SceneID: testSceneID, ActionRef: testActionRef},
			wantErr: "canonical six-part entity ID",
		},
		{
			name:    "no turn id",
			state:   &payload.TurnState{Phase: vocabulary.PhaseApplying},
			wantErr: "turn_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			triples, err := tc.state.Triples(testTurnEntity, "turn-recorder", testTime)
			if err == nil {
				t.Fatalf("the record was accepted and emitted %d triples", len(triples))
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// turn_id and the turn entity ID are one fact wearing two shapes, and a stage
// handed one turn's entity with another turn's record would write a phase onto
// the wrong turn — advancing a turn nobody is running and stranding the one that
// is.
func TestTurnState_RefusesATurnEntityAddressingADifferentTurn(t *testing.T) {
	_, err := phaseState(vocabulary.PhaseApplying).
		Triples("c360.semmachina.world1.starter.turn.turn-act-9", "turn-recorder", testTime)
	if err == nil {
		t.Fatal("a phase was projected onto a turn entity addressing a different turn")
	}
	if !strings.Contains(err.Error(), "one turn or neither does") {
		t.Fatalf("rejection reason %q does not name the mismatch", err.Error())
	}
}
