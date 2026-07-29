package persona_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testActionText = "Rook levers the winch housing with the crowbar, watching Hollis out of the corner of his eye."

// promptFixture is one assembled turn: the view a persona is shown and the
// artifacts its references resolve to.
type promptFixture struct {
	view      *scene.View
	artifacts *fakeArtifacts
	builder   *persona.Builder
}

func newPromptFixture(t *testing.T, turnTriples ...message.Triple) *promptFixture {
	t.Helper()
	artifacts := newFakeArtifacts(&journal{})

	actionRef := refFor(t, vocabulary.TurnActionRef)
	artifacts.actions[actionRef.Key] = &payload.PlayerAction{
		ActionID:   testActionID,
		PlayerID:   testPlayerID,
		CampaignID: "c360.semmachina.world1.starter.campaign.instance",
		SceneID:    testSceneID,
		Text:       testActionText,
		ArrivedAt:  testTime,
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-1"},
	}

	turn := scene.Entity{ID: testTurnEntityID, Triples: append([]message.Triple{
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
		fact(vocabulary.TurnActionRef, actionRef.String()),
		fact(vocabulary.TurnActionPlayer, testPlayerID),
		fact(vocabulary.TurnActionScene, testSceneID),
	}, turnTriples...)}

	builder, err := persona.NewBuilder(artifacts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return &promptFixture{
		artifacts: artifacts,
		builder:   builder,
		view: &scene.View{
			TurnID:       testTurnID,
			TurnEntityID: testTurnEntityID,
			SceneID:      testSceneID,
			AssembledAt:  testTime,
			Turn:         turn,
			Scene: entityWith(testSceneID,
				triple(testSceneID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindScene)),
				triple(testSceneID, vocabulary.WorldEntityName, "The Gatehouse at Dusk"),
				triple(testSceneID, vocabulary.WorldEntityDescription, "Cold iron and a squealing winch."),
			),
			Members: []scene.Entity{
				entityWith(testCharacterID,
					triple(testCharacterID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
					triple(testCharacterID, vocabulary.WorldEntityName, "Rook"),
					triple(testCharacterID, vocabulary.WorldRelationCarries, testCrowbarID),
					triple(testCharacterID, vocabulary.CharacterAttributeStamina, 5),
				),
				entityWith(testSentryID,
					triple(testSentryID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
					triple(testSentryID, vocabulary.WorldEntityName, "Hollis"),
				),
			},
			Neighbours: []scene.Entity{
				entityWith(testCrowbarID,
					triple(testCrowbarID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindItem)),
					triple(testCrowbarID, vocabulary.WorldEntityName, "a bent crowbar"),
				),
				entityWith(testPlayerID,
					triple(testPlayerID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindPlayer)),
					triple(testPlayerID, vocabulary.PlayerCharacterCurrent, testCharacterID),
				),
			},
			Actor: scene.Actor{PlayerID: testPlayerID, CharacterID: testCharacterID},
		},
	}
}

func entityWith(id string, triples ...message.Triple) scene.Entity {
	return scene.Entity{ID: id, Triples: triples}
}

func triple(subject string, predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{
		Subject:   subject,
		Predicate: predicate.String(),
		Object:    object,
		Source:    "test",
		Timestamp: testTime,
	}
}

// storeVerdict puts a verdict where the turn's reference will find it.
func (f *promptFixture) storeVerdict(t *testing.T, verdict *payload.Verdict) content.Ref {
	t.Helper()
	ref, err := f.artifacts.PutVerdict(t.Context(), testTurnEntityID, verdict)
	if err != nil {
		t.Fatalf("PutVerdict: %v", err)
	}
	f.view.Turn.Triples = append(f.view.Turn.Triples, fact(vocabulary.TurnVerdictRef, ref.String()))
	return ref
}

func rollingVerdict() *payload.Verdict {
	stamina := 4
	return &payload.Verdict{
		TurnID: testTurnID, ActionID: testActionID, SceneID: testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskModerate,
			Consequence:  vocabulary.ConsequenceSetback,
			RequiresRoll: true,
		},
		Modifiers: []payload.Modifier{{Source: vocabulary.ModifierEquipment, Value: 1, Note: "the crowbar bites"}},
		Bands: payload.EffectBands{
			vocabulary.BandMiss: {{
				Type: vocabulary.EffectSetStatus, Target: testCharacterID, Status: vocabulary.StatusRestrained,
			}},
			vocabulary.BandPartial: {{
				Type: vocabulary.EffectSetAttribute, Target: testCharacterID,
				Attribute: vocabulary.AttributeStamina, Value: &stamina,
			}},
			vocabulary.BandFull: {},
		},
		Rationale: "The winch will move for a crowbar, but not quietly.",
	}
}

// ------------------------------------------------------------- adjudicator

// The context assembler deliberately leaves turn.action.ref unresolved — it is a
// graph query, and coupling it to the content store would put fiction-fetching
// in a component with no business reading fiction. So the following of that
// reference is the prompt builder's job, and missing it hands the adjudicator a
// perfectly assembled room and no idea what the player did in it.
func TestAdjudicatePrompt_FollowsTheActionReferenceTheAssemblerLeavesAlone(t *testing.T) {
	fixture := newPromptFixture(t)

	// The assembler's own output carries the reference and not the text; if that
	// ever changes this test is measuring the wrong thing.
	if strings.Contains(renderTurn(fixture.view.Turn), testActionText) {
		t.Fatal("the assembled view already contains the action text, so this test proves nothing")
	}

	request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	if !strings.Contains(request.Prompt, testActionText) {
		t.Fatalf("the adjudicator's prompt does not contain the player's action:\n%s", request.Prompt)
	}
	if request.Identity != testIdentity() {
		t.Fatalf("the spawn identity is %+v, want the one read off the view and the resolved action", request.Identity)
	}
	if request.Band != "" {
		t.Fatalf("the adjudicator's spawn carries band %q; it runs before any band exists", request.Band)
	}
}

func TestAdjudicatePrompt_RefusesATurnWithNoActionToJudge(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Turn.Triples = slices.DeleteFunc(fixture.view.Turn.Triples, func(t message.Triple) bool {
		return t.Predicate == vocabulary.TurnActionRef.String()
	})

	if _, err := fixture.builder.Adjudicate(t.Context(), fixture.view); err == nil {
		t.Fatal("a prompt was built for a turn carrying no action reference; the adjudicator would be asked " +
			"to judge a room and told nothing about what happened in it")
	}
}

func TestAdjudicatePrompt_RefusesAReferenceNothingCanResolve(t *testing.T) {
	fixture := newPromptFixture(t)
	clear(fixture.artifacts.actions)

	if _, err := fixture.builder.Adjudicate(t.Context(), fixture.view); err == nil {
		t.Fatal("a prompt was built from a reference that resolved to nothing")
	}
}

// The adjudicator has to be able to name a target in its exit, and it cannot
// name what it was never shown.
func TestAdjudicatePrompt_ShowsTheEntitiesTheVerdictWillHaveToTarget(t *testing.T) {
	fixture := newPromptFixture(t)
	request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	for _, want := range []string{testSceneID, testCharacterID, testSentryID, testCrowbarID} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("the prompt never names %s, so a verdict targeting it would be a hallucinated "+
				"identifier:\n%s", want, request.Prompt)
		}
	}
	// Without the player-to-character binding a persona is shown three people in
	// a room and cannot tell which one is acting.
	if !strings.Contains(request.Prompt, "playing: "+testCharacterID) {
		t.Fatalf("the prompt does not say which character the player controls:\n%s", request.Prompt)
	}
}

// Nothing about the turn's later stages may leak backwards into the judgment.
func TestAdjudicatePrompt_ShowsNoOutcomeBecauseNoneExistsYet(t *testing.T) {
	fixture := newPromptFixture(t,
		fact(vocabulary.TurnRollBand, string(vocabulary.BandFull)),
		fact(vocabulary.TurnRollTotal, 11),
	)
	request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	for _, leaked := range []string{string(vocabulary.BandFull), vocabulary.TurnRollTotal.String()} {
		if strings.Contains(request.Prompt, leaked) {
			t.Fatalf("the adjudicator's prompt contains %q; a judgment made knowing the roll is not a "+
				"judgment:\n%s", leaked, request.Prompt)
		}
	}
}

// A prompt whose bytes depend on storage layout makes two identical worlds
// produce two different prompts — and makes a token-free replay's output depend
// on something no fixture controls.
func TestPrompt_IsDeterministicAndIndependentOfGraphOrder(t *testing.T) {
	first := newPromptFixture(t)
	firstRequest, err := first.builder.Adjudicate(t.Context(), first.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}

	second := newPromptFixture(t)
	for i := range second.view.Members {
		slices.Reverse(second.view.Members[i].Triples)
	}
	slices.Reverse(second.view.Scene.Triples)
	secondRequest, err := second.builder.Adjudicate(t.Context(), second.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}

	if firstRequest.Prompt != secondRequest.Prompt {
		t.Fatalf("reversing the order the graph returned an entity's facts changed the prompt:\n--- got\n%s\n"+
			"--- want\n%s", secondRequest.Prompt, firstRequest.Prompt)
	}
}

// A persona told the room is incomplete can write around the gap; one silently
// handed three of seven people narrates a room that is not there.
func TestPrompt_NamesWhatTheAssemblerCouldNotShow(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Excluded = []scene.Exclusion{
		{ID: "c360.semmachina.world1.starter.character.wren", Reason: scene.ExcludedStub},
	}
	request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	if !strings.Contains(request.Prompt, "character.wren") ||
		!strings.Contains(request.Prompt, string(scene.ExcludedStub)) {
		t.Fatalf("the prompt does not report what was left out:\n%s", request.Prompt)
	}
}

func TestPrompt_SaysSoWhenTheCampaignCannotNameTheActingCharacter(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Actor = scene.Actor{PlayerID: testPlayerID, Doubt: scene.ActorBindingAbsent}

	request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	if !strings.Contains(request.Prompt, string(scene.ActorBindingAbsent)) {
		t.Fatalf("the prompt does not report that the actor is unknown:\n%s", request.Prompt)
	}
}

// ---------------------------------------------------------------- narrator

func TestNarratePrompt_CarriesTheCommittedOutcomeAndTheChangeItMade(t *testing.T) {
	fixture := newPromptFixture(t,
		fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
		fact(vocabulary.TurnRollTotal, 8),
		fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)),
	)
	fixture.storeVerdict(t, rollingVerdict())

	request, err := fixture.builder.Narrate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Narrate: %v", err)
	}
	if request.Band != vocabulary.BandPartial {
		t.Fatalf("the narrator's spawn carries band %q, want the one the dice chose", request.Band)
	}
	for _, want := range []string{
		testActionText,                           // what the player declared
		string(vocabulary.PlausibilityPlausible), // what was judged
		string(vocabulary.BandPartial),           // what the dice said
		"8",                                      // the total behind it
		"The winch will move for a crowbar",      // why it was judged that way
		"stamina is now 4",                       // what actually changed
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("the narrator's prompt is missing %q:\n%s", want, request.Prompt)
		}
	}
	// The miss band's change belongs to an outcome that did not happen.
	if strings.Contains(request.Prompt, string(vocabulary.StatusRestrained)) {
		t.Fatalf("the narrator's prompt describes a band the dice did not select:\n%s", request.Prompt)
	}
}

func TestNarratePrompt_SaysNothingChangedWhenNothingWasCommitted(t *testing.T) {
	cases := map[string]struct {
		turn []message.Triple
		want string
	}{
		"the applier refused the batch": {
			turn: []message.Triple{
				fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				fact(vocabulary.TurnRollTotal, 8),
				fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseFailed)),
				fact(vocabulary.TurnFailureReason, string(vocabulary.FailureEffectEntityKind)),
			},
			want: string(vocabulary.FailureEffectEntityKind),
		},
		"the effects have not landed": {
			turn: []message.Triple{
				fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				fact(vocabulary.TurnRollTotal, 8),
			},
			want: "Nothing was committed",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newPromptFixture(t, testCase.turn...)
			fixture.storeVerdict(t, rollingVerdict())

			request, err := fixture.builder.Narrate(t.Context(), fixture.view)
			if err != nil {
				t.Fatalf("Narrate: %v", err)
			}
			if !strings.Contains(request.Prompt, testCase.want) {
				t.Fatalf("the narrator's prompt does not say %q:\n%s", testCase.want, request.Prompt)
			}
			if strings.Contains(request.Prompt, "stamina is now 4") {
				t.Fatalf("the narrator was told to voice a change that never committed:\n%s", request.Prompt)
			}
		})
	}
}

func TestNarratePrompt_VoicesTheAutoBandOfAVerdictThatDeclinedTheDice(t *testing.T) {
	fixture := newPromptFixture(t, fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)))
	fixture.storeVerdict(t, &payload.Verdict{
		TurnID: testTurnID, ActionID: testActionID, SceneID: testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityCertain,
			Risk:         vocabulary.RiskNone,
			Consequence:  vocabulary.ConsequenceNone,
		},
		Bands: payload.EffectBands{vocabulary.BandAuto: {{
			Type: vocabulary.EffectAddRelationship, Target: testCharacterID,
			Relation: vocabulary.RelationCarries, Object: testCrowbarID,
		}}},
		Rationale: "Nothing at stake.",
	})

	request, err := fixture.builder.Narrate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Narrate: %v", err)
	}
	if request.Band != vocabulary.BandAuto {
		t.Fatalf("the spawn carries band %q, want %q", request.Band, vocabulary.BandAuto)
	}
	if !strings.Contains(request.Prompt, "roll: none") {
		t.Fatalf("the narrator is not told the dice were declined:\n%s", request.Prompt)
	}
	if !strings.Contains(request.Prompt, "now carries "+testCrowbarID) {
		t.Fatalf("the narrator is not told what changed:\n%s", request.Prompt)
	}
}

// A narrator handed the wrong band writes prose that disagrees with the world —
// the exact drift this engine exists to make detectable, manufactured by the
// engine itself. Both incoherences are refused rather than guessed at.
func TestNarratePrompt_RefusesAnOutcomeItCannotEstablish(t *testing.T) {
	t.Run("a rolling verdict with no recorded band", func(t *testing.T) {
		fixture := newPromptFixture(t)
		fixture.storeVerdict(t, rollingVerdict())
		if _, err := fixture.builder.Narrate(t.Context(), fixture.view); err == nil {
			t.Fatal("a narrator was prompted for a turn the dice have not resolved")
		}
	})

	t.Run("a no-roll verdict carrying a band", func(t *testing.T) {
		fixture := newPromptFixture(t, fact(vocabulary.TurnRollBand, string(vocabulary.BandFull)))
		fixture.storeVerdict(t, &payload.Verdict{
			TurnID: testTurnID, ActionID: testActionID, SceneID: testSceneID,
			Scalars: payload.VerdictScalars{
				Plausibility: vocabulary.PlausibilityCertain,
				Risk:         vocabulary.RiskNone,
				Consequence:  vocabulary.ConsequenceNone,
			},
			Bands:     payload.EffectBands{vocabulary.BandAuto: {}},
			Rationale: "Declined the dice.",
		})
		if _, err := fixture.builder.Narrate(t.Context(), fixture.view); err == nil {
			t.Fatal("a turn recorded a roll for a verdict that never went to the dice")
		}
	})

	t.Run("a turn with no verdict at all", func(t *testing.T) {
		fixture := newPromptFixture(t, fact(vocabulary.TurnRollBand, string(vocabulary.BandFull)))
		if _, err := fixture.builder.Narrate(t.Context(), fixture.view); err == nil {
			t.Fatal("a narrator was prompted for a turn that was never judged")
		}
	})

	t.Run("a roll band the dice cannot select", func(t *testing.T) {
		fixture := newPromptFixture(t,
			fact(vocabulary.TurnRollBand, string(vocabulary.BandAuto)),
			fact(vocabulary.TurnRollTotal, 8),
		)
		fixture.storeVerdict(t, rollingVerdict())
		if _, err := fixture.builder.Narrate(t.Context(), fixture.view); err == nil {
			t.Fatal("a rolled turn recorded the auto band, which belongs to a verdict that never reached the dice")
		}
	})
}

// A single-valued predicate holding two values is the signature of a write that
// took an appending lane, and this reader must not resolve it by guessing.
//
// The effects mark is the dangerous one. Read as ABSENT — which is what any
// "exactly one" test degrades to at two — the narrator is told "Nothing was
// committed. The world is exactly as described above" while the effects are on
// the graph: narration disagreeing with authoritative state, manufactured by the
// engine, which is the exact drift this product exists to detect.
func TestNarratePrompt_RefusesADuplicatedSingleValuedFact(t *testing.T) {
	rolled := []message.Triple{
		fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
		fact(vocabulary.TurnRollTotal, 8),
	}
	batch := payload.BatchIDForTurn(testTurnID)

	cases := map[string]struct {
		turn      []message.Triple
		predicate vocabulary.Predicate
	}{
		"two effects marks": {
			turn: append(slices.Clone(rolled),
				fact(vocabulary.TurnEffectsBatch, batch),
				fact(vocabulary.TurnEffectsBatch, batch),
			),
			predicate: vocabulary.TurnEffectsBatch,
		},
		"two roll bands": {
			turn: []message.Triple{
				fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				fact(vocabulary.TurnRollBand, string(vocabulary.BandFull)),
				fact(vocabulary.TurnRollTotal, 8),
			},
			predicate: vocabulary.TurnRollBand,
		},
		"two failure reasons": {
			turn: append(slices.Clone(rolled),
				fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseFailed)),
				fact(vocabulary.TurnFailureReason, string(vocabulary.FailureEffectEntityKind)),
				fact(vocabulary.TurnFailureReason, string(vocabulary.FailurePersonaCapExhausted)),
			),
			predicate: vocabulary.TurnFailureReason,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newPromptFixture(t, testCase.turn...)
			fixture.storeVerdict(t, rollingVerdict())

			request, err := fixture.builder.Narrate(t.Context(), fixture.view)
			if err == nil {
				t.Fatalf("a turn holding two values for the single-valued %s was rendered into a prompt, so the "+
					"narrator was told something about the world nobody checked:\n%s",
					testCase.predicate, request.Prompt)
			}
			if !strings.Contains(err.Error(), testCase.predicate.String()) {
				t.Fatalf("the refusal is %q; it must name the predicate that was written twice", err)
			}
		})
	}
}

// A persona judging one room about an action taken in another is judging a
// premise the world does not hold.
func TestPrompt_RefusesAViewAssembledForAnotherScene(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.SceneID = "c360.semmachina.world1.starter.scene.courtyard"

	if _, err := fixture.builder.Adjudicate(t.Context(), fixture.view); err == nil {
		t.Fatal("a prompt was built for a scene the action was not taken in")
	}
}

func TestNewBuilder_RequiresAnArtifactReader(t *testing.T) {
	if _, err := persona.NewBuilder(nil); err == nil {
		t.Fatal("a prompt builder was built with no way to resolve the action reference")
	}
}

// renderTurn is the assembled turn's own facts, for the anti-vacuity check on
// the reference-following test.
func renderTurn(turn scene.Entity) string {
	var out strings.Builder
	for _, t := range turn.Triples {
		out.WriteString(t.Predicate)
		out.WriteString(": ")
		out.WriteString(strings.TrimSpace(timeless(t.Object)))
		out.WriteString("\n")
	}
	return out.String()
}

func timeless(object any) string {
	if at, ok := object.(time.Time); ok {
		return at.Format(time.RFC3339)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(strings.TrimSpace(sprint(object))), " "))
}

func sprint(object any) string {
	switch value := object.(type) {
	case string:
		return value
	default:
		return ""
	}
}
