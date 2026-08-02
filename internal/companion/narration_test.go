package companion

import (
	"context"
	"slices"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const narrationTurnID = "turn-narration-1"
const narrationTurnEntityID = "c360.semmachina.world1.starter.turn." + narrationTurnID

type narrationArtifacts struct {
	stage    *payload.CompanionStageRecord
	decision *payload.CompanionDecision
}

func (n *narrationArtifacts) InstanceName() string { return "TEST_CONTENT" }
func (n *narrationArtifacts) GetCompanionStageRecord(context.Context, content.Ref) (*payload.CompanionStageRecord, error) {
	return n.stage, nil
}
func (n *narrationArtifacts) GetCompanionDecision(context.Context, content.Ref) (*payload.CompanionDecision, error) {
	return n.decision, nil
}

func narrationFixture(t *testing.T) (*NarrationResolver, *authorityGraph, *narrationArtifacts, epistemic.NarrationEvidenceRequest) {
	t.Helper()
	authority, g, bondID := validAuthority(t)
	stageKey, err := content.KeyFor(vocabulary.TurnCompanionStageRef, content.SubjectTurn, narrationTurnID)
	if err != nil {
		t.Fatal(err)
	}
	decisionKey, err := content.KeyFor(vocabulary.TurnCompanionDecisionRef, content.SubjectTurn, narrationTurnID)
	if err != nil {
		t.Fatal(err)
	}
	stageRef := content.Ref{Instance: "TEST_CONTENT", Key: stageKey}
	decisionRef := content.Ref{Instance: "TEST_CONTENT", Key: decisionKey}
	g.states[narrationTurnEntityID] = &graph.EntityState{ID: narrationTurnEntityID, Triples: []message.Triple{
		fact(narrationTurnEntityID, vocabulary.TurnActionPlayer, playerID),
		fact(narrationTurnEntityID, vocabulary.TurnActionScene, locationID),
		fact(narrationTurnEntityID, vocabulary.TurnCompanionStageRef, stageRef.String()),
		fact(narrationTurnEntityID, vocabulary.TurnCompanionDecisionRef, decisionRef.String()),
	}}
	knowledgeID := "c360.semmachina.world1.starter.knowledge.wren-scrap"
	g.states[knowledgeID] = state(knowledgeID, vocabulary.EntityKindKnowledge,
		fact(knowledgeID, vocabulary.KnowledgeActorHolder, companionID),
		fact(knowledgeID, vocabulary.KnowledgeEvidenceRef, evidenceID))
	uncitedEvidence := "c360.semmachina.world1.starter.evidence.uncited"
	uncitedKnowledge := "c360.semmachina.world1.starter.knowledge.wren-uncited"
	g.states[uncitedKnowledge] = state(uncitedKnowledge, vocabulary.EntityKindKnowledge,
		fact(uncitedKnowledge, vocabulary.KnowledgeActorHolder, companionID),
		fact(uncitedKnowledge, vocabulary.KnowledgeEvidenceRef, uncitedEvidence))
	decision := &payload.CompanionDecision{
		TurnID: narrationTurnID, ContextRef: locationID, PlayerID: playerID, CompanionID: companionID,
		Kind: payload.CompanionDecisionHint, HintLevel: vocabulary.HintLevelNudge,
		EvidenceRefs: []string{evidenceID},
	}
	decision.DecisionID = payload.CompanionDecisionID(
		decision.TurnID, decision.ContextRef, decision.PlayerID, decision.CompanionID)
	artifacts := &narrationArtifacts{stage: &payload.CompanionStageRecord{
		TurnID: narrationTurnID, PlayerID: playerID, CompanionID: companionID, BondID: bondID,
		Status: payload.CompanionStageDecision, TriggerKind: vocabulary.CompanionTriggerPlayerHint,
		TriggerSource: vocabulary.CompanionTriggerSourceCaseDecision, DecisionRef: decisionRef.String(),
	}, decision: decision}
	resolver, err := NewNarrationResolver(g, artifacts, authority)
	if err != nil {
		t.Fatal(err)
	}
	return resolver, g, artifacts, epistemic.NarrationEvidenceRequest{
		TurnID: narrationTurnID, TurnEntityID: narrationTurnEntityID, PlayerID: playerID,
		ContextRef: locationID, Purpose: epistemic.PurposeNarrator,
	}
}

func TestNarrationResolverReturnsOnlyIdentityBoundCitedCompanionKnowledge(t *testing.T) {
	resolver, _, _, request := narrationFixture(t)
	got, err := resolver.AuthorizedNarrationEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{evidenceID}) {
		t.Fatalf("authorized narration evidence = %v, want only cited %s", got, evidenceID)
	}
}

func TestNarrationResolverFailsClosedAcrossIdentityAndArtifactBoundaries(t *testing.T) {
	for name, mutate := range map[string]func(*authorityGraph, *narrationArtifacts, *epistemic.NarrationEvidenceRequest){
		"wrong purpose": func(_ *authorityGraph, _ *narrationArtifacts, r *epistemic.NarrationEvidenceRequest) {
			r.Purpose = epistemic.PurposePublicAdjudicator
		},
		"foreign turn": func(_ *authorityGraph, _ *narrationArtifacts, r *epistemic.NarrationEvidenceRequest) {
			r.TurnID = "turn-foreign"
		},
		"foreign player": func(_ *authorityGraph, a *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			a.stage.PlayerID = "c360.semmachina.world1.starter.player.other"
		},
		"wrong context": func(_ *authorityGraph, a *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			a.decision.ContextRef = "c360.semmachina.world1.starter.scene.road"
			a.decision.DecisionID = payload.CompanionDecisionID(a.decision.TurnID, a.decision.ContextRef,
				a.decision.PlayerID, a.decision.CompanionID)
		},
		"uncited knowledge": func(g *authorityGraph, a *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			a.decision.EvidenceRefs = []string{"c360.semmachina.world1.starter.evidence.unknown"}
			a.decision.DecisionID = payload.CompanionDecisionID(a.decision.TurnID, a.decision.ContextRef,
				a.decision.PlayerID, a.decision.CompanionID)
			_ = g
		},
		"ambiguous stage ref": func(g *authorityGraph, _ *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			g.states[narrationTurnEntityID].Triples = append(g.states[narrationTurnEntityID].Triples,
				fact(narrationTurnEntityID, vocabulary.TurnCompanionStageRef,
					"obj://TEST_CONTENT/turn/turn-foreign/companion-stage"))
		},
		"foreign stage ref": func(g *authorityGraph, _ *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			for index := range g.states[narrationTurnEntityID].Triples {
				if g.states[narrationTurnEntityID].Triples[index].Predicate == vocabulary.TurnCompanionStageRef.String() {
					g.states[narrationTurnEntityID].Triples[index].Object =
						"obj://TEST_CONTENT/turn/turn-foreign/companion-stage"
				}
			}
		},
		"stage decision ref mismatch": func(_ *authorityGraph, a *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			a.stage.DecisionRef = "obj://TEST_CONTENT/turn/turn-foreign/companion-decision"
		},
		"wrong bond": func(_ *authorityGraph, a *narrationArtifacts, _ *epistemic.NarrationEvidenceRequest) {
			a.stage.BondID = "c360.semmachina.world1.starter.companion-bond.wrong"
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolver, g, artifacts, request := narrationFixture(t)
			mutate(g, artifacts, &request)
			if _, err := resolver.AuthorizedNarrationEvidence(t.Context(), request); err == nil {
				t.Fatal("invalid narration authorization state was accepted")
			}
		})
	}
}

func TestNarrationResolverNoDecisionStatusesReturnNoEvidenceAndForbidDecisionRef(t *testing.T) {
	for _, status := range []payload.CompanionStageStatus{
		payload.CompanionStageNoActiveBond, payload.CompanionStageNoTrigger,
	} {
		t.Run(string(status), func(t *testing.T) {
			resolver, g, artifacts, request := narrationFixture(t)
			turn := g.states[narrationTurnEntityID]
			kept := turn.Triples[:0]
			for _, triple := range turn.Triples {
				if triple.Predicate != vocabulary.TurnCompanionDecisionRef.String() {
					kept = append(kept, triple)
				}
			}
			turn.Triples = kept
			artifacts.stage.Status = status
			artifacts.stage.DecisionRef = ""
			if status == payload.CompanionStageNoActiveBond {
				delete(g.states, artifacts.stage.BondID)
				artifacts.stage.CompanionID, artifacts.stage.BondID = "", ""
				artifacts.stage.TriggerKind, artifacts.stage.TriggerSource = "", ""
			} else {
				artifacts.stage.TriggerKind = vocabulary.CompanionTriggerNone
				artifacts.stage.TriggerSource = vocabulary.CompanionTriggerSourceNone
			}
			got, err := resolver.AuthorizedNarrationEvidence(t.Context(), request)
			if err != nil || len(got) != 0 {
				t.Fatalf("no-decision status %s = %v, %v", status, got, err)
			}
			turn.Triples = append(turn.Triples, fact(narrationTurnEntityID,
				vocabulary.TurnCompanionDecisionRef, "obj://TEST_CONTENT/turn/turn-narration-1/companion-decision"))
			if _, err := resolver.AuthorizedNarrationEvidence(t.Context(), request); err == nil {
				t.Fatal("no-decision status accepted a companion decision reference")
			}
		})
	}
}

var _ Graph = (*authorityGraph)(nil)
