package persona_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testActionText = "Rook levers the winch housing with the crowbar, watching Hollis out of the corner of his eye."

// promptFixture is one assembled turn: the view a persona is shown and the
// artifacts its references resolve to.
type promptFixture struct {
	view      *epistemic.Projection
	artifacts *fakeArtifacts
	builder   *persona.Builder
}

func newPromptFixture(t *testing.T, turnTriples ...message.Triple) *promptFixture {
	t.Helper()
	artifacts := newFakeArtifacts(&journal{})

	actionRef := refFor(t, vocabulary.TurnActionRef)
	knowledgeRef := refFor(t, vocabulary.TurnKnowledgeRef)
	companionStageRef := refFor(t, vocabulary.TurnCompanionStageRef)
	artifacts.actions[actionRef.Key] = &payload.PlayerAction{
		ActionID:   testActionID,
		PlayerID:   testPlayerID,
		CampaignID: "c360.semmachina.world1.starter.campaign.instance",
		SceneID:    testSceneID,
		Text:       testActionText,
		ArrivedAt:  testTime,
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-1"},
	}
	artifacts.knowledge[knowledgeRef.Key] = &content.KnowledgeReceipt{
		TurnID: testTurnID, Status: content.KnowledgeNotApplicable, Entries: []content.KnowledgeReceiptEntry{},
	}
	artifacts.companionStages[companionStageRef.Key] = &payload.CompanionStageRecord{
		TurnID: testTurnID, PlayerID: testPlayerID, Status: payload.CompanionStageNoActiveBond,
	}

	turn := projectedEntity(testTurnEntityID, append([]message.Triple{
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
		fact(vocabulary.TurnActionRef, actionRef.String()),
		fact(vocabulary.TurnActionPlayer, testPlayerID),
		fact(vocabulary.TurnActionScene, testSceneID),
		fact(vocabulary.TurnKnowledgeRef, knowledgeRef.String()),
		fact(vocabulary.TurnCompanionStageRef, companionStageRef.String()),
	}, turnTriples...)...)

	builder, err := persona.NewBuilder(artifacts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return &promptFixture{
		artifacts: artifacts,
		builder:   builder,
		view: &epistemic.Projection{
			Purpose:      epistemic.PurposePublicAdjudicator,
			TurnID:       testTurnID,
			TurnEntityID: testTurnEntityID,
			SceneID:      testSceneID,
			Turn:         turn,
			Scene: entityWith(testSceneID,
				triple(testSceneID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindScene)),
				triple(testSceneID, vocabulary.WorldEntityName, "The Gatehouse at Dusk"),
				triple(testSceneID, vocabulary.WorldEntityDescription, "Cold iron and a squealing winch."),
			),
			Members: []epistemic.Entity{
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
			Neighbours: []epistemic.Entity{
				entityWith(testCrowbarID,
					triple(testCrowbarID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindItem)),
					triple(testCrowbarID, vocabulary.WorldEntityName, "a bent crowbar"),
				),
				entityWith(testPlayerID,
					triple(testPlayerID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindPlayer)),
					triple(testPlayerID, vocabulary.PlayerCharacterCurrent, testCharacterID),
				),
			},
			Actor: epistemic.Actor{PlayerID: testPlayerID, CharacterID: testCharacterID},
		},
	}
}

func entityWith(id string, triples ...message.Triple) epistemic.Entity {
	return projectedEntity(id, triples...)
}

func projectedEntity(id string, triples ...message.Triple) epistemic.Entity {
	entity := epistemic.Entity{ID: id, Facts: make([]epistemic.Fact, 0, len(triples))}
	for _, triple := range triples {
		entity.Facts = append(entity.Facts, epistemic.Fact{
			Predicate: vocabulary.Predicate(triple.Predicate), Object: triple.Object,
		})
	}
	return entity
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
	f.view.Purpose = epistemic.PurposeNarrator
	ref, err := f.artifacts.PutVerdict(t.Context(), testTurnEntityID, verdict)
	if err != nil {
		t.Fatalf("PutVerdict: %v", err)
	}
	f.view.Turn.Facts = append(f.view.Turn.Facts, epistemic.Fact{
		Predicate: vocabulary.TurnVerdictRef, Object: ref.String(),
	})
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

const narratorEvidenceID = "c360.semmachina.world1.starter.evidence.scrap"

func addNarratorArtifacts(t *testing.T, fixture *promptFixture) {
	t.Helper()
	knowledgeRef := refFor(t, vocabulary.TurnKnowledgeRef)
	testimonyRef := refFor(t, vocabulary.TurnNarrationRef)
	decisionID := "case-decision-1"
	fixture.artifacts.knowledge[knowledgeRef.Key] = &content.KnowledgeReceipt{
		TurnID: testTurnID, DecisionID: decisionID, Status: content.KnowledgeCommitted,
		Entries: content.SortedKnowledgeEntries([]content.KnowledgeReceiptEntry{
			{
				RecipientID: testCharacterID, EvidenceID: narratorEvidenceID,
				KnowledgeID:  "c360.semmachina.world1.starter.knowledge.rook-scrap",
				RevelationID: "c360.semmachina.world1.starter.revelation.rook-scrap",
				TestimonyRef: testimonyRef.String(),
			},
			{
				RecipientID: testSentryID, EvidenceID: "c360.semmachina.world1.starter.evidence.foreign-canary",
				KnowledgeID:  "c360.semmachina.world1.starter.knowledge.hollis-foreign",
				RevelationID: "c360.semmachina.world1.starter.revelation.hollis-foreign",
			},
		}),
	}
	fixture.artifacts.testimony[testimonyRef.Key] = &content.Testimony{
		TurnID: testTurnID, DecisionID: decisionID,
		BeliefID: "c360.semmachina.world1.starter.belief.hollis-scrap", SourceActorID: testSentryID,
		RecipientID: testCharacterID, EvidenceID: narratorEvidenceID,
		Stance: vocabulary.BeliefAffirms, Prose: "Hollis admits the brass scrap came from the winch.",
	}
	fixture.view.Neighbours = append(fixture.view.Neighbours, entityWith(narratorEvidenceID,
		triple(narratorEvidenceID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
		triple(narratorEvidenceID, vocabulary.WorldEntityName, "a stamped brass scrap"),
	))

	stageRef := refFor(t, vocabulary.TurnCompanionStageRef)
	decisionRef := refFor(t, vocabulary.TurnCompanionDecisionRef)
	decision := &payload.CompanionDecision{
		TurnID: testTurnID, ContextRef: testSceneID, PlayerID: testPlayerID,
		CompanionID: testSentryID, Kind: payload.CompanionDecisionHint,
		HintLevel: vocabulary.HintLevelConnect, EvidenceRefs: []string{narratorEvidenceID},
	}
	decision.DecisionID = payload.CompanionDecisionID(
		decision.TurnID, decision.ContextRef, decision.PlayerID, decision.CompanionID)
	fixture.artifacts.companionDecisions[decisionRef.Key] = decision
	fixture.artifacts.companionStages[stageRef.Key] = &payload.CompanionStageRecord{
		TurnID: testTurnID, PlayerID: testPlayerID, CompanionID: testSentryID,
		BondID: "c360.semmachina.world1.starter.companion-bond.solo-hollis",
		Status: payload.CompanionStageDecision, TriggerKind: vocabulary.CompanionTriggerPlayerHint,
		TriggerSource: vocabulary.CompanionTriggerSourceCaseDecision, DecisionRef: decisionRef.String(),
	}
	fixture.view.Turn.Facts = append(fixture.view.Turn.Facts, epistemic.Fact{
		Predicate: vocabulary.TurnCompanionDecisionRef, Object: decisionRef.String(),
	})
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

func TestPromptBuilder_CarriesThePersistedResumeAttemptForBothRoles(t *testing.T) {
	t.Run("adjudicator", func(t *testing.T) {
		fixture := newPromptFixture(t, fact(vocabulary.TurnResumeAttempts, float64(1)))
		request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
		if err != nil {
			t.Fatalf("Adjudicate: %v", err)
		}
		if request.ResumeAttempt != 1 {
			t.Fatalf("resume attempt = %d, want 1", request.ResumeAttempt)
		}
	})

	t.Run("narrator", func(t *testing.T) {
		fixture := newPromptFixture(t,
			fact(vocabulary.TurnResumeAttempts, float64(2)),
			fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
			fact(vocabulary.TurnRollTotal, 8),
			fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)),
		)
		fixture.storeVerdict(t, rollingVerdict())
		request, err := fixture.builder.Narrate(t.Context(), fixture.view)
		if err != nil {
			t.Fatalf("Narrate: %v", err)
		}
		if request.ResumeAttempt != 2 {
			t.Fatalf("resume attempt = %d, want 2", request.ResumeAttempt)
		}
	})
}

func TestPromptBuilder_RefusesInvalidResumeAttemptsForBothRoles(t *testing.T) {
	cases := map[string][]message.Triple{
		"duplicate": {
			fact(vocabulary.TurnResumeAttempts, 1),
			fact(vocabulary.TurnResumeAttempts, 2),
		},
		"fractional": {fact(vocabulary.TurnResumeAttempts, 1.5)},
		"zero":       {fact(vocabulary.TurnResumeAttempts, 0)},
		"negative":   {fact(vocabulary.TurnResumeAttempts, -1)},
	}
	roles := map[string]func(*promptFixture) error{
		"adjudicator": func(f *promptFixture) error {
			_, err := f.builder.Adjudicate(t.Context(), f.view)
			return err
		},
		"narrator": func(f *promptFixture) error {
			_, err := f.builder.Narrate(t.Context(), f.view)
			return err
		},
	}
	for role, build := range roles {
		for name, triples := range cases {
			t.Run(role+"/"+name, func(t *testing.T) {
				fixture := newPromptFixture(t, triples...)
				if err := build(fixture); err == nil {
					t.Fatal("invalid persisted resume attempt was accepted")
				}
			})
		}
	}
}

func TestAdjudicatePrompt_RefusesATurnWithNoActionToJudge(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Turn.Facts = slices.DeleteFunc(fixture.view.Turn.Facts, func(f epistemic.Fact) bool {
		return f.Predicate == vocabulary.TurnActionRef
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
		slices.Reverse(second.view.Members[i].Facts)
	}
	slices.Reverse(second.view.Scene.Facts)
	secondRequest, err := second.builder.Adjudicate(t.Context(), second.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}

	if firstRequest.Prompt != secondRequest.Prompt {
		t.Fatalf("reversing the order the graph returned an entity's facts changed the prompt:\n--- got\n%s\n"+
			"--- want\n%s", secondRequest.Prompt, firstRequest.Prompt)
	}
}

func TestSerializedPromptContainsOnlyTheAuthorizedProjectionCanary(t *testing.T) {
	fixture := newPromptFixture(t)
	const (
		revealedID   = "c360.semmachina.world1.starter.evidence.revealed-canary"
		revealedText = "REVEALED-PROMPT-CANARY"
		hiddenID     = "c360.semmachina.world1.starter.evidence.unrevealed-canary"
		hiddenText   = "UNREVEALED-PROMPT-CANARY"
	)
	fixture.view.Neighbours = append(fixture.view.Neighbours, epistemic.Entity{
		ID: revealedID,
		Facts: []epistemic.Fact{
			{Predicate: vocabulary.WorldEntityKind, Object: string(vocabulary.EntityKindEvidence)},
			{Predicate: vocabulary.WorldEntityName, Object: revealedText},
		},
	})

	request, err := fixture.builder.Adjudicate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	for _, want := range []string{revealedID, revealedText} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("serialized prompt lacks authorized canary %q:\n%s", want, request.Prompt)
		}
	}
	for _, forbidden := range []string{hiddenID, hiddenText, "# Not available"} {
		if strings.Contains(request.Prompt, forbidden) {
			t.Fatalf("serialized prompt contains omitted/legacy value %q:\n%s", forbidden, request.Prompt)
		}
	}
}

func TestSerializedCasekeeperPromptCarriesPrivateCanariesAndInjectedIdentity(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Purpose = epistemic.PurposeCasekeeper
	const (
		caseID      = "c360.semmachina.world1.starter.case.bellweather"
		culpritID   = "c360.semmachina.world1.starter.character.culprit-canary"
		culpritText = "CULPRIT-CASEKEEPER-CANARY"
		hiddenID    = "c360.semmachina.world1.starter.evidence.hidden-canary"
		hiddenText  = "HIDDEN-CASEKEEPER-CANARY"
	)
	fixture.view.Neighbours = append(fixture.view.Neighbours,
		entityWith(caseID,
			triple(caseID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindCase)),
			triple(caseID, vocabulary.CaseSolutionCulprit, culpritID),
		),
		entityWith(culpritID,
			triple(culpritID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
			triple(culpritID, vocabulary.WorldEntityName, culpritText),
		),
		entityWith(hiddenID,
			triple(hiddenID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
			triple(hiddenID, vocabulary.WorldEntityName, hiddenText),
		),
	)
	request, err := fixture.builder.Interpret(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	for _, want := range []string{caseID, culpritID, culpritText, hiddenID, hiddenText, testActionText} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("casekeeper prompt lacks authorized private canary %q:\n%s", want, request.Prompt)
		}
	}
	if request.Identity.CaseID != caseID || request.Identity.ActorID != testCharacterID {
		t.Fatalf("casekeeper identity = %+v", request.Identity)
	}
}

func TestSerializedNarrationPromptPreservesRevealedCanaryAndOmitsSecrets(t *testing.T) {
	fixture := newPromptFixture(t,
		fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
		fact(vocabulary.TurnRollTotal, 8),
		fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)),
	)
	fixture.storeVerdict(t, rollingVerdict())
	const (
		revealedID   = "c360.semmachina.world1.starter.evidence.revealed-narration"
		revealedText = "REVEALED-NARRATION-CANARY"
		culpritID    = "c360.semmachina.world1.starter.character.culprit-canary"
		culpritText  = "CULPRIT-NARRATION-CANARY"
		hiddenID     = "c360.semmachina.world1.starter.evidence.unrevealed-narration"
		hiddenText   = "UNREVEALED-NARRATION-CANARY"
	)
	fixture.view.Neighbours = append(fixture.view.Neighbours, epistemic.Entity{
		ID: revealedID,
		Facts: []epistemic.Fact{
			{Predicate: vocabulary.WorldEntityKind, Object: string(vocabulary.EntityKindEvidence)},
			{Predicate: vocabulary.WorldEntityName, Object: revealedText},
		},
	})

	request, err := fixture.builder.Narrate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Narrate: %v", err)
	}
	for _, want := range []string{revealedID, revealedText} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("narration prompt lacks revealed anti-vacuity canary %q:\n%s", want, request.Prompt)
		}
	}
	for _, forbidden := range []string{culpritID, culpritText, hiddenID, hiddenText} {
		if strings.Contains(request.Prompt, forbidden) {
			t.Fatalf("narration prompt leaks pre-denouement secret %q:\n%s", forbidden, request.Prompt)
		}
	}
}

func TestPromptBuilderRejectsWrongPurposeBeforeRendering(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Purpose = epistemic.PurposeCasekeeper
	if _, err := fixture.builder.Adjudicate(t.Context(), fixture.view); err == nil {
		t.Fatal("Adjudicate accepted a casekeeper projection")
	}
	fixture.view.Purpose = epistemic.PurposeVerifier
	if _, err := fixture.builder.Narrate(t.Context(), fixture.view); err == nil {
		t.Fatal("Narrate accepted a verifier projection")
	}
}

func TestPromptBuilderRejectsAnOversizedSerializedProjection(t *testing.T) {
	fixture := newPromptFixture(t)
	fixture.view.Scene.Facts = append(fixture.view.Scene.Facts, epistemic.Fact{
		Predicate: vocabulary.WorldEntityDescription,
		Object:    strings.Repeat("x", epistemic.DefaultMaxProjectionBytes+1),
	})
	if _, err := fixture.builder.Adjudicate(t.Context(), fixture.view); err == nil {
		t.Fatal("prompt builder accepted a projection beyond its serialized-byte ceiling")
	}
}

// ---------------------------------------------------------------- narrator

func TestNarratePrompt_UsesOnlyCommittedAuthorizedRevelationAndCompanionArtifacts(t *testing.T) {
	fixture := newPromptFixture(t,
		fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
		fact(vocabulary.TurnRollTotal, 8),
		fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)),
	)
	fixture.storeVerdict(t, rollingVerdict())
	addNarratorArtifacts(t, fixture)

	request, err := fixture.builder.Narrate(t.Context(), fixture.view)
	if err != nil {
		t.Fatalf("Narrate: %v", err)
	}
	for _, want := range []string{
		narratorEvidenceID, "a stamped brass scrap",
		"Hollis admits the brass scrap came from the winch.",
		testSentryID, string(payload.CompanionDecisionHint), string(vocabulary.HintLevelConnect),
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("narrator prompt lacks committed authorized context %q:\n%s", want, request.Prompt)
		}
	}
	if strings.Contains(request.Prompt, "foreign-canary") {
		t.Fatalf("narrator prompt included another recipient's receipt entry:\n%s", request.Prompt)
	}
}

func TestNarratePrompt_FailsClosedOnMissingOrMismatchedCommittedArtifacts(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *promptFixture){
		"missing knowledge receipt": func(t *testing.T, f *promptFixture) {
			delete(f.artifacts.knowledge, refFor(t, vocabulary.TurnKnowledgeRef).Key)
		},
		"receipt belongs to another turn": func(t *testing.T, f *promptFixture) {
			f.artifacts.knowledge[refFor(t, vocabulary.TurnKnowledgeRef).Key].TurnID = "turn-act-2"
		},
		"stage belongs to another player": func(t *testing.T, f *promptFixture) {
			f.artifacts.companionStages[refFor(t, vocabulary.TurnCompanionStageRef).Key].PlayerID =
				"c360.semmachina.world1.starter.player.foreign"
		},
		"decision reference disagrees": func(t *testing.T, f *promptFixture) {
			f.artifacts.companionStages[refFor(t, vocabulary.TurnCompanionStageRef).Key].DecisionRef =
				"obj://TEST_CONTENT/turn/turn-act-2/companion-decision"
		},
		"decision cites unauthorized evidence": func(t *testing.T, f *promptFixture) {
			f.artifacts.companionDecisions[refFor(t, vocabulary.TurnCompanionDecisionRef).Key].EvidenceRefs =
				[]string{"c360.semmachina.world1.starter.evidence.secret-canary"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newPromptFixture(t,
				fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				fact(vocabulary.TurnRollTotal, 8),
				fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)),
			)
			fixture.storeVerdict(t, rollingVerdict())
			addNarratorArtifacts(t, fixture)
			mutate(t, fixture)
			if _, err := fixture.builder.Narrate(t.Context(), fixture.view); err == nil {
				t.Fatal("mismatched committed artifact chain produced a narration prompt")
			}
		})
	}
}

func TestNarratePrompt_DisclosesSolutionOnlyForDenouementPurpose(t *testing.T) {
	const solutionCanary = "c360.semmachina.world1.starter.character.solution-canary"
	for name, tc := range map[string]struct {
		purpose      epistemic.Purpose
		wantSolution bool
	}{
		"ordinary narrator":     {purpose: epistemic.PurposeNarrator},
		"authorized denouement": {purpose: epistemic.PurposeDenouement, wantSolution: true},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newPromptFixture(t,
				fact(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				fact(vocabulary.TurnRollTotal, 8),
				fact(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID)),
			)
			fixture.storeVerdict(t, rollingVerdict())
			fixture.view.Purpose = tc.purpose
			fixture.view.HasSolution = tc.wantSolution
			if tc.wantSolution {
				fixture.view.Solution = epistemic.Solution{
					Culprit: solutionCanary,
					Method:  "c360.semmachina.world1.starter.item.solution-method-canary",
					Motive:  "c360.semmachina.world1.starter.item.solution-motive-canary",
				}
			}
			request, err := fixture.builder.Narrate(t.Context(), fixture.view)
			if err != nil {
				t.Fatalf("Narrate: %v", err)
			}
			if strings.Contains(request.Prompt, solutionCanary) != tc.wantSolution {
				t.Fatalf("solution canary presence = %v, want %v:\n%s",
					strings.Contains(request.Prompt, solutionCanary), tc.wantSolution, request.Prompt)
			}
		})
	}
}

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

func TestPromptBuilderHasNoRawSceneViewInput(t *testing.T) {
	for _, methodName := range []string{"Adjudicate", "Narrate"} {
		method, ok := reflect.TypeOf((*persona.Builder)(nil)).MethodByName(methodName)
		if !ok {
			t.Fatalf("Builder.%s is missing", methodName)
		}
		projectionType := reflect.TypeOf((*epistemic.Projection)(nil))
		if method.Type.NumIn() != 3 || method.Type.In(2) != projectionType {
			t.Fatalf("Builder.%s context input = %v, want only *epistemic.Projection",
				methodName, method.Type.In(2))
		}
	}
}

// renderTurn is the assembled turn's own facts, for the anti-vacuity check on
// the reference-following test.
func renderTurn(turn epistemic.Entity) string {
	var out strings.Builder
	for _, fact := range turn.Facts {
		out.WriteString(fact.Predicate.String())
		out.WriteString(": ")
		out.WriteString(strings.TrimSpace(timeless(fact.Object)))
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
