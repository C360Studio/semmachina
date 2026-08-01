package epistemic_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	turnID           = "turn-act-1"
	turnEntity       = "acme.semmachina.keep.starter.turn.turn-act-1"
	sceneID          = "acme.semmachina.keep.starter.scene.library"
	playerID         = "acme.semmachina.keep.starter.player.alice"
	actorID          = "acme.semmachina.keep.starter.character.rook"
	otherID          = "acme.semmachina.keep.starter.character.wren"
	culpritID        = "acme.semmachina.keep.starter.character.culprit"
	methodID         = "acme.semmachina.keep.starter.item.method"
	motiveID         = "acme.semmachina.keep.starter.item.motive"
	ownEvidence      = "acme.semmachina.keep.starter.evidence.own"
	otherEvidence    = "acme.semmachina.keep.starter.evidence.other"
	revealedEvidence = "acme.semmachina.keep.starter.evidence.revealed"
	hiddenEvidence   = "acme.semmachina.keep.starter.evidence.unrevealed"
	caseID           = "acme.semmachina.keep.starter.case.bellweather"
	beliefID         = "acme.semmachina.keep.starter.belief.culprit"
)

type fakeScenes struct {
	view    *scene.View
	err     error
	journal *[]string
}

func (f *fakeScenes) Assemble(_ context.Context, _, _ string) (*scene.View, error) {
	if f.journal != nil {
		*f.journal = append(*f.journal, "scene")
	}
	return f.view, f.err
}

type predicateCall struct {
	predicate string
	value     string
	limit     int
}

type fakeProjectionGraph struct {
	states  map[string]graph.EntityState
	queries map[string][]string
	calls   []predicateCall
	journal *[]string
}

func (f *fakeProjectionGraph) EntitiesByPredicateValue(
	_ context.Context, predicate, value string, limit int,
) ([]string, error) {
	if f.journal != nil {
		*f.journal = append(*f.journal, "query")
	}
	f.calls = append(f.calls, predicateCall{predicate: predicate, value: value, limit: limit})
	return slices.Clone(f.queries[predicate+"="+value]), nil
}

func (f *fakeProjectionGraph) GetEntities(
	_ context.Context, ids []string,
) (graphio.BatchResult, error) {
	if f.journal != nil {
		*f.journal = append(*f.journal, "hydrate")
	}
	var result graphio.BatchResult
	for _, id := range ids {
		state, ok := f.states[id]
		if !ok {
			result.Missing = append(result.Missing, graph.MissingEntity{ID: id})
			continue
		}
		result.Entities = append(result.Entities, state)
	}
	return result, nil
}

func fixture() (*fakeScenes, *fakeProjectionGraph) {
	view := &scene.View{
		TurnID: turnID, TurnEntityID: turnEntity, SceneID: sceneID,
		Actor: scene.Actor{PlayerID: playerID, CharacterID: actorID},
		Turn: sceneEntity(turnEntity,
			fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
			fact(vocabulary.TurnActionPlayer, playerID),
			fact(vocabulary.TurnActionScene, sceneID),
		),
		Scene: sceneEntity(sceneID,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindScene)),
			fact(vocabulary.WorldEntityName, "Bellweather Library"),
		),
		Members: []scene.Entity{
			sceneEntity(actorID,
				fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
				fact(vocabulary.WorldEntityName, "Rook"),
			),
			sceneEntity(playerID,
				fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindPlayer)),
				fact(vocabulary.PlayerCharacterCurrent, actorID),
			),
		},
	}

	states := map[string]graph.EntityState{
		"knowledge-own": state("knowledge-own",
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindKnowledge)),
			fact(vocabulary.KnowledgeActorHolder, actorID),
			fact(vocabulary.KnowledgeEvidenceRef, revealedEvidence),
		),
		"knowledge-other": state("knowledge-other",
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindKnowledge)),
			fact(vocabulary.KnowledgeActorHolder, otherID),
			fact(vocabulary.KnowledgeEvidenceRef, otherEvidence),
		),
		ownEvidence: state(ownEvidence,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
			fact(vocabulary.WorldEntityName, "OWN-KNOWLEDGE-CANARY"),
			fact(vocabulary.EvidenceTruthStatusCurrent, "TRUTH-CANARY"),
		),
		otherEvidence: state(otherEvidence,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
			fact(vocabulary.WorldEntityName, "COMPANION-KNOWLEDGE-CANARY"),
		),
		revealedEvidence: state(revealedEvidence,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
			fact(vocabulary.WorldEntityName, "CURRENT-REVELATION-CANARY"),
		),
		"revelation-own": state("revelation-own",
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindRevelation)),
			fact(vocabulary.RevelationActorHolder, actorID),
			fact(vocabulary.RevelationTurnID, turnID),
			fact(vocabulary.RevelationEvidenceRef, revealedEvidence),
		),
		"revelation-other": state("revelation-other",
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindRevelation)),
			fact(vocabulary.RevelationActorHolder, otherID),
			fact(vocabulary.RevelationTurnID, turnID),
			fact(vocabulary.RevelationEvidenceRef, otherEvidence),
		),
		"bond-other": state("bond-other",
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCompanionBond)),
			fact(vocabulary.CompanionBondPlayer, playerID),
			fact(vocabulary.CompanionBondCharacter, otherID),
		),
		caseID: state(caseID,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCase)),
			fact(vocabulary.CaseSolutionCulprit, culpritID),
			fact(vocabulary.CaseSolutionMethod, methodID),
			fact(vocabulary.CaseSolutionMotive, motiveID),
			fact(vocabulary.CaseMemberSuspect, culpritID),
			fact(vocabulary.CaseMemberEvidence, ownEvidence),
			fact(vocabulary.CaseMemberEvidence, revealedEvidence),
			fact(vocabulary.CaseMemberEvidence, hiddenEvidence),
			fact(vocabulary.CaseLifecyclePhase, string(vocabulary.CasePhaseDenouement)),
		),
		hiddenEvidence: state(hiddenEvidence,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
			fact(vocabulary.WorldEntityName, "UNREVEALED-CLUE-CANARY"),
			fact(vocabulary.EvidenceTruthStatusCurrent, "hidden"),
			fact(vocabulary.EvidenceRevealPhase, string(vocabulary.CasePhaseInvestigation)),
			fact(vocabulary.EvidenceRevealKindPredicate, string(vocabulary.EvidenceRevealInvestigate)),
			fact(vocabulary.EvidenceRevealTarget, culpritID),
		),
		culpritID: state(culpritID,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
			fact(vocabulary.WorldEntityName, "CULPRIT-DESCRIPTION-CANARY"),
		),
		methodID: state(methodID,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindItem)),
			fact(vocabulary.WorldEntityName, "METHOD-DESCRIPTION-CANARY"),
		),
		motiveID: state(motiveID,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindItem)),
			fact(vocabulary.WorldEntityName, "MOTIVE-DESCRIPTION-CANARY"),
		),
		beliefID: state(beliefID,
			fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindBelief)),
			fact(vocabulary.WorldEntityDescription, "BELIEF-CANARY"),
			fact(vocabulary.BeliefActorHolder, culpritID),
			fact(vocabulary.BeliefEvidenceRef, ownEvidence),
			fact(vocabulary.BeliefStanceCurrent, string(vocabulary.BeliefAffirms)),
		),
	}
	queries := map[string][]string{
		vocabulary.KnowledgeActorHolder.String() + "=" + actorID: {"knowledge-own"},
		vocabulary.KnowledgeActorHolder.String() + "=" + otherID: {"knowledge-other"},
		vocabulary.RevelationTurnID.String() + "=" + turnID: {
			"revelation-other", "revelation-own",
		},
		vocabulary.CompanionBondCharacter.String() + "=" + otherID:                      {"bond-other"},
		vocabulary.WorldEntityKind.String() + "=" + string(vocabulary.EntityKindCase):   {caseID},
		vocabulary.WorldEntityKind.String() + "=" + string(vocabulary.EntityKindBelief): {beliefID},
	}
	return &fakeScenes{view: view}, &fakeProjectionGraph{states: states, queries: queries}
}

func TestClosedPurposeCanaryMatrix(t *testing.T) {
	type expectation struct {
		audience     epistemic.AuthenticatedAudience
		culpritID    bool
		culpritText  bool
		hiddenID     bool
		hiddenText   bool
		revealedID   bool
		revealedText bool
		reject       bool
	}
	tests := map[string]expectation{
		"casekeeper": {
			audience: casekeeperAudience(), culpritID: true, culpritText: true,
			hiddenID: true, hiddenText: true, revealedID: true, revealedText: true,
		},
		"player": {
			audience: playerAudience(), revealedID: true, revealedText: true,
		},
		"companion": {audience: companionAudience()},
		"public-adjudicator": {
			audience: publicAudience(), revealedID: true, revealedText: true,
		},
		"narrator": {
			audience: narratorAudience(), revealedID: true, revealedText: true,
		},
		"denouement": {
			audience: denouementAudience(), culpritID: true,
			revealedID: true, revealedText: true,
		},
		"verifier": {audience: verifierAudience(), culpritID: true},
		"operator": {audience: epistemic.OperatorAudience(), reject: true},
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			scenes, graphReader := fixture()
			raw := fmt.Sprint(graphReader.states)
			for _, canary := range []string{
				culpritID, "CULPRIT-DESCRIPTION-CANARY",
				hiddenEvidence, "UNREVEALED-CLUE-CANARY",
				revealedEvidence, "CURRENT-REVELATION-CANARY",
			} {
				if !strings.Contains(raw, canary) {
					t.Fatalf("fixture does not contain canary %q; absence checks would be vacuous", canary)
				}
			}
			projector, err := epistemic.NewProjector(
				scenes, graphReader, scopeForFixture(t),
				epistemic.WithDenouementAuthorizer(allowDenouement{allowed: true}),
			)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := projector.Project(t.Context(), want.audience)
			if want.reject {
				if err == nil {
					t.Fatal("purpose was expected to be rejected")
				}
				return
			}
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			body := string(mustBytes(t, projection))
			assertPresence(t, body, culpritID, want.culpritID)
			assertPresence(t, body, "CULPRIT-DESCRIPTION-CANARY", want.culpritText)
			assertPresence(t, body, hiddenEvidence, want.hiddenID)
			assertPresence(t, body, "UNREVEALED-CLUE-CANARY", want.hiddenText)
			assertPresence(t, body, revealedEvidence, want.revealedID)
			assertPresence(t, body, "CURRENT-REVELATION-CANARY", want.revealedText)
		})
	}
}

func TestScopedCaseAndBeliefsIgnoreASecondWorld(t *testing.T) {
	const (
		foreignCase     = "other.semmachina.world.pack.case.foreign"
		foreignBelief   = "other.semmachina.world.pack.belief.foreign"
		foreignActor    = "other.semmachina.world.pack.character.foreign"
		foreignEvidence = "other.semmachina.world.pack.evidence.foreign"
		foreignCanary   = "FOREIGN-WORLD-SECRET-CANARY"
	)
	scenes, graphReader := fixture()
	graphReader.states[foreignCase] = state(foreignCase,
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCase)),
		fact(vocabulary.CaseSolutionCulprit, foreignActor),
		fact(vocabulary.CaseSolutionMethod, methodID),
		fact(vocabulary.CaseSolutionMotive, foreignEvidence),
	)
	graphReader.states[foreignBelief] = state(foreignBelief,
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindBelief)),
		fact(vocabulary.BeliefActorHolder, foreignActor),
		fact(vocabulary.BeliefEvidenceRef, foreignEvidence),
		fact(vocabulary.BeliefStanceCurrent, foreignCanary),
	)
	graphReader.states[foreignEvidence] = state(foreignEvidence,
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)),
		fact(vocabulary.WorldEntityName, foreignCanary),
	)
	graphReader.queries[vocabulary.WorldEntityKind.String()+"="+string(vocabulary.EntityKindCase)] =
		[]string{caseID, foreignCase}
	graphReader.queries[vocabulary.WorldEntityKind.String()+"="+string(vocabulary.EntityKindBelief)] =
		[]string{beliefID, foreignBelief}

	projector := mustProjector(t, scenes, graphReader)
	for _, audience := range []epistemic.AuthenticatedAudience{
		verifierAudience(), casekeeperAudience(),
	} {
		projection, err := projector.Project(t.Context(), audience)
		if err != nil {
			t.Fatalf("Project(%s): %v", audience.Purpose(), err)
		}
		body := string(mustBytes(t, projection))
		for _, forbidden := range []string{foreignCase, foreignBelief, foreignActor, foreignEvidence, foreignCanary} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s projection crossed world scope with %q: %s", audience.Purpose(), forbidden, body)
			}
		}
	}
	for _, call := range graphReader.calls {
		if call.predicate == vocabulary.WorldEntityKind.String() {
			t.Fatalf("scoped projection issued a global kind lookup: %+v", graphReader.calls)
		}
	}
}

func TestAuthorizationRecordsFailClosedOnAmbiguityAndWrongKinds(t *testing.T) {
	tests := map[string]struct {
		audience epistemic.AuthenticatedAudience
		mutate   func(map[string]graph.EntityState)
	}{
		"knowledge duplicate holder": {
			audience: publicAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["knowledge-own"] = withFact(states["knowledge-own"],
					fact(vocabulary.KnowledgeActorHolder, otherID))
			},
		},
		"knowledge duplicate evidence target": {
			audience: publicAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["knowledge-own"] = withFact(states["knowledge-own"],
					fact(vocabulary.KnowledgeEvidenceRef, ownEvidence))
			},
		},
		"knowledge wrong record kind": {
			audience: publicAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["knowledge-own"] = replaceFacts(states["knowledge-own"], vocabulary.WorldEntityKind,
					fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)))
			},
		},
		"knowledge wrong evidence kind": {
			audience: publicAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states[revealedEvidence] = replaceFacts(states[revealedEvidence], vocabulary.WorldEntityKind,
					fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)))
			},
		},
		"knowledge duplicate evidence kind": {
			audience: publicAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states[revealedEvidence] = withFact(states[revealedEvidence],
					fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindEvidence)))
			},
		},
		"revelation duplicate actor": {
			audience: playerAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["revelation-own"] = withFact(states["revelation-own"],
					fact(vocabulary.RevelationActorHolder, otherID))
			},
		},
		"revelation duplicate turn": {
			audience: playerAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["revelation-own"] = withFact(states["revelation-own"],
					fact(vocabulary.RevelationTurnID, "turn-other"))
			},
		},
		"revelation duplicate evidence target": {
			audience: playerAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["revelation-own"] = withFact(states["revelation-own"],
					fact(vocabulary.RevelationEvidenceRef, ownEvidence))
			},
		},
		"bond duplicate player": {
			audience: companionAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["bond-other"] = withFact(states["bond-other"],
					fact(vocabulary.CompanionBondPlayer, "other-player"))
			},
		},
		"bond duplicate character": {
			audience: companionAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["bond-other"] = withFact(states["bond-other"],
					fact(vocabulary.CompanionBondCharacter, actorID))
			},
		},
		"bond wrong record kind": {
			audience: companionAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states["bond-other"] = replaceFacts(states["bond-other"], vocabulary.WorldEntityKind,
					fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)))
			},
		},
		"belief duplicate holder": {
			audience: casekeeperAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states[beliefID] = withFact(states[beliefID],
					fact(vocabulary.BeliefActorHolder, otherID))
			},
		},
		"belief duplicate evidence target": {
			audience: casekeeperAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states[beliefID] = withFact(states[beliefID],
					fact(vocabulary.BeliefEvidenceRef, hiddenEvidence))
			},
		},
		"belief wrong record kind": {
			audience: casekeeperAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states[beliefID] = replaceFacts(states[beliefID], vocabulary.WorldEntityKind,
					fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)))
			},
		},
		"case wrong record kind": {
			audience: verifierAudience(),
			mutate: func(states map[string]graph.EntityState) {
				states[caseID] = replaceFacts(states[caseID], vocabulary.WorldEntityKind,
					fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)))
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			scenes, graphReader := fixture()
			testCase.mutate(graphReader.states)
			_, err := mustProjector(t, scenes, graphReader).Project(t.Context(), testCase.audience)
			if err == nil {
				t.Fatal("malformed authorization record was accepted")
			}
		})
	}
}

func TestInvalidCasekeeperTargetsFailBeforeAnyGraphOrSceneRead(t *testing.T) {
	tests := map[string][]string{
		"blank":     {""},
		"duplicate": {culpritID, culpritID},
		"over cap":  {"a", "b", "c", "d", "e", "f", "g", "h", "i"},
	}
	for name, targets := range tests {
		t.Run(name, func(t *testing.T) {
			scenes, graphReader := fixture()
			journal := []string{}
			scenes.journal = &journal
			graphReader.journal = &journal
			audience := epistemic.CasekeeperAudience(caseID, turnID, turnEntity, targets...)
			if _, err := mustProjector(t, scenes, graphReader).Project(t.Context(), audience); err == nil {
				t.Fatal("invalid casekeeper targets were accepted")
			}
			if len(journal) != 0 {
				t.Fatalf("invalid audience caused reads before refusal: %v", journal)
			}
		})
	}
}

func TestPublicAdjudicatorGetsOwnKnowledgeWithoutMysteryTruth(t *testing.T) {
	scenes, graphReader := fixture()
	projector := mustProjector(t, scenes, graphReader)

	projection, err := projector.Project(
		t.Context(), publicAudience(),
	)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	body := string(mustBytes(t, projection))
	if !strings.Contains(body, "CURRENT-REVELATION-CANARY") {
		t.Fatalf("authorized actor knowledge is absent: %s", body)
	}
	for _, forbidden := range []string{
		culpritID, "CULPRIT-DESCRIPTION-CANARY", hiddenEvidence, "UNREVEALED-CLUE-CANARY",
		"TRUTH-CANARY", "BELIEF-CANARY", caseID,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public adjudicator projection leaks %q: %s", forbidden, body)
		}
	}
	assertNoDanglingReferences(t, projection)
}

func TestPlayerAndNarratorGetOnlyTheActingActorsCurrentRevelation(t *testing.T) {
	for name, audience := range map[string]epistemic.AuthenticatedAudience{
		"player":   playerAudience(),
		"narrator": narratorAudience(),
	} {
		t.Run(name, func(t *testing.T) {
			scenes, graphReader := fixture()
			projection := mustProject(t, scenes, graphReader, audience)
			body := string(mustBytes(t, projection))
			if !strings.Contains(body, "CURRENT-REVELATION-CANARY") {
				t.Fatalf("current committed revelation is absent: %s", body)
			}
			if strings.Contains(body, "COMPANION-KNOWLEDGE-CANARY") {
				t.Fatalf("another actor's revelation leaked: %s", body)
			}
			if strings.Contains(body, hiddenEvidence) || strings.Contains(body, "UNREVEALED-CLUE-CANARY") {
				t.Fatalf("unrevealed clue leaked: %s", body)
			}
		})
	}
}

func TestCompanionGetsOwnKnowledgeOnlyAfterBondVerification(t *testing.T) {
	scenes, graphReader := fixture()
	projection := mustProject(t, scenes, graphReader, companionAudience())
	body := string(mustBytes(t, projection))
	if !strings.Contains(body, "COMPANION-KNOWLEDGE-CANARY") {
		t.Fatalf("bonded companion's knowledge is absent: %s", body)
	}
	if strings.Contains(body, "CURRENT-REVELATION-CANARY") {
		t.Fatalf("the acting player's private knowledge leaked to the companion: %s", body)
	}
	if strings.Contains(body, hiddenEvidence) || strings.Contains(body, "UNREVEALED-CLUE-CANARY") {
		t.Fatalf("unrevealed clue leaked to companion: %s", body)
	}

	delete(graphReader.states, "bond-other")
	if _, err := mustProjector(t, scenes, graphReader).Project(
		t.Context(), companionAudience(),
	); err == nil {
		t.Fatal("unbonded companion received a projection")
	}
}

func TestPlayerAudienceCannotSubstituteAnotherTurnForTheGraphPinnedActor(t *testing.T) {
	scenes, graphReader := fixture()
	if _, err := mustProjector(t, scenes, graphReader).Project(
		t.Context(), epistemic.PlayerAudience("other-turn", "other-turn-entity"),
	); err == nil {
		t.Fatal("caller-supplied player character overrode the graph-pinned actor")
	}
}

func TestCasekeeperGetsSolutionEvidenceTruthAndBeliefs(t *testing.T) {
	scenes, graphReader := fixture()
	projector := mustProjector(t, scenes, graphReader)

	projection, err := projector.Project(t.Context(), casekeeperAudience())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	body := string(mustBytes(t, projection))
	for _, canary := range []string{
		culpritID, methodID, motiveID, hiddenEvidence, "UNREVEALED-CLUE-CANARY",
		"TRUTH-CANARY", "BELIEF-CANARY",
		string(vocabulary.CasePhaseInvestigation), string(vocabulary.EvidenceRevealInvestigate),
	} {
		if !strings.Contains(body, canary) {
			t.Fatalf("casekeeper projection lacks %q: %s", canary, body)
		}
	}
	assertNoDanglingReferences(t, projection)
}

func TestRevealEligibilityPredicatesStayInsideTheCasekeeperBoundary(t *testing.T) {
	for name, audience := range map[string]epistemic.AuthenticatedAudience{
		"player":             playerAudience(),
		"public-adjudicator": publicAudience(),
		"companion":          companionAudience(),
		"narrator":           narratorAudience(),
	} {
		t.Run(name, func(t *testing.T) {
			scenes, graphReader := fixture()
			body := string(mustBytes(t, mustProject(t, scenes, graphReader, audience)))
			for _, predicate := range []vocabulary.Predicate{
				vocabulary.EvidenceRevealPhase,
				vocabulary.EvidenceRevealKindPredicate,
				vocabulary.EvidenceRevealTarget,
			} {
				if strings.Contains(body, predicate.String()) {
					t.Fatalf("%s projection leaked private eligibility predicate %s: %s", name, predicate, body)
				}
			}
		})
	}
}

func TestVerifierGetsExactSolutionIDsAndNoDescriptiveEntities(t *testing.T) {
	scenes, graphReader := fixture()
	projector := mustProjector(t, scenes, graphReader)

	projection, err := projector.Project(t.Context(), verifierAudience())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	body := string(mustBytes(t, projection))
	for _, id := range []string{culpritID, methodID, motiveID} {
		if !strings.Contains(body, id) {
			t.Fatalf("verifier projection lacks exact solution id %q: %s", id, body)
		}
	}
	for _, forbidden := range []string{
		turnID, turnEntity, caseID, "CULPRIT-DESCRIPTION-CANARY", hiddenEvidence, "UNREVEALED-CLUE-CANARY",
		"TRUTH-CANARY", "BELIEF-CANARY",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("verifier projection leaks %q: %s", forbidden, body)
		}
	}
}

func TestDenouementNeedsPhaseAndAuthorizationBeforeSolutionDisclosure(t *testing.T) {
	scenes, graphReader := fixture()
	withoutAuthorizer := mustProjector(t, scenes, graphReader)
	if _, err := withoutAuthorizer.Project(
		t.Context(), denouementAudience(),
	); err == nil {
		t.Fatal("denouement solution was disclosed without an accusation authorizer")
	}

	denied, err := epistemic.NewProjector(
		scenes, graphReader, scopeForFixture(t), epistemic.WithDenouementAuthorizer(allowDenouement{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Project(
		t.Context(), denouementAudience(),
	); err == nil {
		t.Fatal("denouement solution was disclosed after a denied accusation")
	}

	allowed, err := epistemic.NewProjector(
		scenes, graphReader, scopeForFixture(t),
		epistemic.WithDenouementAuthorizer(allowDenouement{allowed: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := allowed.Project(
		t.Context(), denouementAudience(),
	)
	if err != nil {
		t.Fatalf("authorized denouement: %v", err)
	}
	body := string(mustBytes(t, projection))
	for _, id := range []string{culpritID, methodID, motiveID} {
		if !strings.Contains(body, id) {
			t.Fatalf("authorized denouement lacks solution id %q: %s", id, body)
		}
	}
	for _, forbidden := range []string{
		hiddenEvidence, "UNREVEALED-CLUE-CANARY", "CULPRIT-DESCRIPTION-CANARY",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("denouement leaks non-solution private state %q: %s", forbidden, body)
		}
	}

	caseState := graphReader.states[caseID]
	for index := range caseState.Triples {
		if caseState.Triples[index].Predicate == vocabulary.CaseLifecyclePhase.String() {
			caseState.Triples[index].Object = string(vocabulary.CasePhaseInvestigation)
		}
	}
	graphReader.states[caseID] = caseState
	if _, err := allowed.Project(
		t.Context(), denouementAudience(),
	); err == nil {
		t.Fatal("denouement solution was disclosed before the denouement lifecycle phase")
	}
}

func TestProjectorRefusesLimitPlusOneAndReadsSceneBeforePrivateState(t *testing.T) {
	scenes, graphReader := fixture()
	journal := []string{}
	scenes.journal = &journal
	graphReader.journal = &journal
	graphReader.queries[vocabulary.KnowledgeActorHolder.String()+"="+actorID] = []string{"a", "b", "c"}
	projector, err := epistemic.NewProjector(
		scenes, graphReader, scopeForFixture(t), epistemic.WithMaxSupplementalEntities(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = projector.Project(
		t.Context(), publicAudience(),
	)
	if err == nil || !strings.Contains(err.Error(), "limit is 2") {
		t.Fatalf("limit+1 result was not refused: %v", err)
	}
	if len(graphReader.calls) != 1 || graphReader.calls[0].limit != 3 {
		t.Fatalf("query calls = %+v, want one hard limit+1 probe", graphReader.calls)
	}
	if !slices.Equal(journal, []string{"scene", "query"}) {
		t.Fatalf("read order = %v, want bounded scene before private query and no hydration after overflow", journal)
	}
}

func TestProjectorEnforcesOneCumulativeSupplementalEntityBudgetAcrossSources(t *testing.T) {
	scenes, graphReader := fixture()
	graphReader.states["knowledge-extra"] = state("knowledge-extra",
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindKnowledge)),
		fact(vocabulary.KnowledgeActorHolder, actorID),
		fact(vocabulary.KnowledgeEvidenceRef, ownEvidence),
	)
	graphReader.queries[vocabulary.KnowledgeActorHolder.String()+"="+actorID] =
		[]string{"knowledge-own", "knowledge-extra"}
	graphReader.states["revelation-own"] = replaceFacts(
		graphReader.states["revelation-own"], vocabulary.RevelationEvidenceRef,
		fact(vocabulary.RevelationEvidenceRef, otherEvidence),
	)
	projector, err := epistemic.NewProjector(
		scenes, graphReader, scopeForFixture(t), epistemic.WithMaxSupplementalEntities(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := projector.Project(
		t.Context(), playerAudience(),
	); err == nil || !strings.Contains(err.Error(), "supplemental") {
		t.Fatalf("knowledge+revelation exceeded cumulative supplemental budget without refusal: %v", err)
	}
}

func TestProjectorEnforcesOutputEntityTripleAndByteCaps(t *testing.T) {
	tests := map[string]epistemic.Option{
		"entities": epistemic.WithMaxProjectionEntities(3),
		"triples":  epistemic.WithMaxProjectionTriples(2),
		"bytes":    epistemic.WithMaxProjectionBytes(64),
	}
	for name, option := range tests {
		t.Run(name, func(t *testing.T) {
			scenes, graphReader := fixture()
			projector, err := epistemic.NewProjector(
				scenes, graphReader, scopeForFixture(t), option,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := projector.Project(
				t.Context(), publicAudience(),
			); err == nil || !strings.Contains(err.Error(), "projection") {
				t.Fatalf("%s cap was not enforced: %v", name, err)
			}
		})
	}
}

func TestProjectionCapsRejectManyEntitiesAndOneLargeEntity(t *testing.T) {
	t.Run("many entities", func(t *testing.T) {
		scenes, graphReader := fixture()
		for index := 0; index < 12; index++ {
			id := fmt.Sprintf("acme.semmachina.keep.starter.item.extra-%02d", index)
			scenes.view.Neighbours = append(scenes.view.Neighbours, sceneEntity(id,
				fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindItem)),
				fact(vocabulary.WorldEntityName, id),
			))
		}
		projector, err := epistemic.NewProjector(
			scenes, graphReader, scopeForFixture(t), epistemic.WithMaxProjectionEntities(8),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projector.Project(t.Context(), publicAudience()); err == nil {
			t.Fatal("many-entity projection exceeded the whole-output cap without refusal")
		}
	})

	t.Run("many triples on one entity", func(t *testing.T) {
		scenes, graphReader := fixture()
		for index := 0; index < 40; index++ {
			scenes.view.Scene.Triples = append(scenes.view.Scene.Triples,
				fact(vocabulary.WorldEntityDescription, fmt.Sprintf("fact-%02d", index)))
		}
		projector, err := epistemic.NewProjector(
			scenes, graphReader, scopeForFixture(t), epistemic.WithMaxProjectionTriples(20),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projector.Project(t.Context(), publicAudience()); err == nil {
			t.Fatal("one entity exceeded the whole-output triple cap without refusal")
		}
	})

	t.Run("many bytes on one entity", func(t *testing.T) {
		scenes, graphReader := fixture()
		scenes.view.Scene.Triples = append(scenes.view.Scene.Triples,
			fact(vocabulary.WorldEntityDescription, strings.Repeat("x", 2048)))
		projector, err := epistemic.NewProjector(
			scenes, graphReader, scopeForFixture(t), epistemic.WithMaxProjectionBytes(1024),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projector.Project(t.Context(), publicAudience()); err == nil {
			t.Fatal("one entity exceeded the whole-output byte cap without refusal")
		}
	})
}

func TestProjectorRefusesOperatorAndUnverifiedActor(t *testing.T) {
	scenes, graphReader := fixture()
	projector := mustProjector(t, scenes, graphReader)
	if _, err := projector.Project(t.Context(), epistemic.OperatorAudience()); err == nil {
		t.Fatal("operator was admitted to the persona projection surface")
	}
	scenes.view.Actor = scene.Actor{PlayerID: playerID, Doubt: scene.ActorBindingAbsent}
	if _, err := projector.Project(
		t.Context(), publicAudience(),
	); err == nil {
		t.Fatal("an unverified turn actor received a projection")
	}
}

func TestProjectionSerializationIsIndependentOfGraphOrdering(t *testing.T) {
	firstScenes, firstGraph := fixture()
	first := mustProject(t, firstScenes, firstGraph, publicAudience())

	secondScenes, secondGraph := fixture()
	slices.Reverse(secondScenes.view.Members)
	for index := range secondScenes.view.Members {
		slices.Reverse(secondScenes.view.Members[index].Triples)
	}
	state := secondGraph.states[ownEvidence]
	slices.Reverse(state.Triples)
	secondGraph.states[ownEvidence] = state
	second := mustProject(t, secondScenes, secondGraph, publicAudience())

	if string(mustBytes(t, first)) != string(mustBytes(t, second)) {
		t.Fatalf("projection bytes changed with graph order:\nfirst: %s\nsecond: %s",
			mustBytes(t, first), mustBytes(t, second))
	}
}

func TestProjectionSerializationUsesTypeAwareOrderingForCollidingObjects(t *testing.T) {
	firstScenes, firstGraph := fixture()
	firstScenes.view.Scene.Triples = append(firstScenes.view.Scene.Triples,
		fact(vocabulary.WorldEntityDescription, "1"),
		fact(vocabulary.WorldEntityDescription, 1),
	)
	first := mustProject(t, firstScenes, firstGraph, publicAudience())

	secondScenes, secondGraph := fixture()
	secondScenes.view.Scene.Triples = append(secondScenes.view.Scene.Triples,
		fact(vocabulary.WorldEntityDescription, 1),
		fact(vocabulary.WorldEntityDescription, "1"),
	)
	second := mustProject(t, secondScenes, secondGraph, publicAudience())

	if string(mustBytes(t, first)) != string(mustBytes(t, second)) {
		t.Fatalf("mixed-type facts with identical fmt.Sprint values changed projection bytes:\nfirst: %s\nsecond: %s",
			mustBytes(t, first), mustBytes(t, second))
	}
}

func TestProjectionRefusesAnObjectThatCannotBeCanonicallySerialized(t *testing.T) {
	scenes, graphReader := fixture()
	scenes.view.Scene.Triples = append(scenes.view.Scene.Triples,
		fact(vocabulary.WorldEntityDescription, func() {}),
	)
	if _, err := mustProjector(t, scenes, graphReader).Project(
		t.Context(), publicAudience(),
	); err == nil {
		t.Fatal("projection accepted a fact object that cannot be serialized deterministically")
	}
}

func mustProjector(
	t *testing.T, scenes *fakeScenes, graphReader *fakeProjectionGraph,
) *epistemic.Projector {
	t.Helper()
	projector, err := epistemic.NewProjector(scenes, graphReader, scopeForFixture(t))
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	return projector
}

func scopeForFixture(t *testing.T) epistemic.Scope {
	t.Helper()
	scope, err := epistemic.NewScope(caseID, map[string][]string{culpritID: {beliefID}})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return scope
}

func publicAudience() epistemic.AuthenticatedAudience {
	return epistemic.PublicAdjudicatorAudience(turnID, turnEntity)
}

func playerAudience() epistemic.AuthenticatedAudience {
	return epistemic.PlayerAudience(turnID, turnEntity)
}

func narratorAudience() epistemic.AuthenticatedAudience {
	return epistemic.NarratorAudience(turnID, turnEntity)
}

func casekeeperAudience() epistemic.AuthenticatedAudience {
	return epistemic.CasekeeperAudience(caseID, turnID, turnEntity, culpritID)
}

func companionAudience() epistemic.AuthenticatedAudience {
	return epistemic.CompanionAudience(turnID, turnEntity, caseID, otherID, "bond-other")
}

func verifierAudience() epistemic.AuthenticatedAudience {
	return epistemic.VerifierAudience(caseID)
}

func denouementAudience() epistemic.AuthenticatedAudience {
	return epistemic.DenouementAudience(turnID, turnEntity, caseID, "accusation-result-1")
}

func mustProject(
	t *testing.T,
	scenes *fakeScenes,
	graphReader *fakeProjectionGraph,
	audience epistemic.AuthenticatedAudience,
) *epistemic.Projection {
	t.Helper()
	projection, err := mustProjector(t, scenes, graphReader).Project(t.Context(), audience)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	return projection
}

func mustBytes(t *testing.T, projection *epistemic.Projection) []byte {
	t.Helper()
	body, err := projection.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return body
}

func assertPresence(t *testing.T, body, canary string, want bool) {
	t.Helper()
	if got := strings.Contains(body, canary); got != want {
		t.Fatalf("projection contains %q = %t, want %t: %s", canary, got, want, body)
	}
}

func assertNoDanglingReferences(t *testing.T, projection *epistemic.Projection) {
	t.Helper()
	ids := make(map[string]bool)
	for _, entity := range projection.Entities() {
		ids[entity.ID] = true
	}
	for _, entity := range projection.Entities() {
		for _, fact := range entity.Facts {
			if !vocabulary.IsEntityReference(fact.Predicate) {
				continue
			}
			target, ok := fact.Object.(string)
			if !ok || !ids[target] {
				t.Fatalf("%s carries dangling %s -> %v", entity.ID, fact.Predicate, fact.Object)
			}
		}
	}
}

func sceneEntity(id string, facts ...message.Triple) scene.Entity {
	for index := range facts {
		facts[index].Subject = id
	}
	return scene.Entity{ID: id, Triples: facts}
}

func state(id string, facts ...message.Triple) graph.EntityState {
	for index := range facts {
		facts[index].Subject = id
	}
	return graph.EntityState{ID: id, Triples: facts}
}

func withFact(state graph.EntityState, added message.Triple) graph.EntityState {
	added.Subject = state.ID
	state.Triples = append(state.Triples, added)
	return state
}

func replaceFacts(
	state graph.EntityState, predicate vocabulary.Predicate, replacements ...message.Triple,
) graph.EntityState {
	state.Triples = slices.DeleteFunc(state.Triples, func(triple message.Triple) bool {
		return triple.Predicate == predicate.String()
	})
	for _, replacement := range replacements {
		state = withFact(state, replacement)
	}
	return state
}

func fact(predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{Predicate: predicate.String(), Object: object}
}

type allowDenouement struct{ allowed bool }

func (a allowDenouement) Authorized(context.Context, string, string, string) (bool, error) {
	return a.allowed, nil
}

var _ epistemic.DenouementAuthorizer = allowDenouement{}
