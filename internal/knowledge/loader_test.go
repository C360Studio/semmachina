package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type loaderGraph struct {
	entities map[string]*graph.EntityState
	query    map[string][]string
}

func (g *loaderGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	state, ok := g.entities[id]
	if !ok {
		return nil, graphio.ErrEntityNotFound
	}
	return state, nil
}

func (g *loaderGraph) EntitiesByPredicateValue(
	_ context.Context, predicate, value string, _ int,
) ([]string, error) {
	return g.query[predicate+"\x00"+value], nil
}

type loaderDecisions struct {
	record *payload.CaseDecisionRecord
	err    error
}

func (s loaderDecisions) GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error) {
	return s.record, s.err
}

func loaderEntity(id string, facts ...struct {
	predicate vocabulary.Predicate
	object    string
}) *graph.EntityState {
	state := &graph.EntityState{ID: id}
	for _, item := range facts {
		state.Triples = append(state.Triples, message.Triple{
			Subject: id, Predicate: item.predicate.String(), Object: item.object,
		})
	}
	return state
}

func loaderFact(predicate vocabulary.Predicate, object string) struct {
	predicate vocabulary.Predicate
	object    string
} {
	return struct {
		predicate vocabulary.Predicate
		object    string
	}{predicate: predicate, object: object}
}

func TestLoader_ResolvesQuestionEligibilityAndAuthoredTestimonyBeforeWrites(t *testing.T) {
	turnEntityID := "c360.semmachina.test.bellweather.turn.turn-act-1"
	playerID := "c360.semmachina.test.bellweather.player.rowan"
	beliefID := "c360.semmachina.test.bellweather.belief.judith-wire"
	ref := content.Ref{Instance: "TEST", Key: "turn/turn-act-1/case-decision"}
	decision := validPreflight().Decision
	decision.Kind = payload.CaseDecisionQuestion
	decision.TargetRefs = []string{testSuspect}

	reader := &loaderGraph{entities: map[string]*graph.EntityState{
		turnEntityID: loaderEntity(turnEntityID,
			loaderFact(vocabulary.TurnCaseDecisionRef, ref.String()),
			loaderFact(vocabulary.TurnActionPlayer, playerID)),
		playerID: loaderEntity(playerID,
			loaderFact(vocabulary.PlayerCharacterCurrent, testActor)),
		testCase: loaderEntity(testCase,
			loaderFact(vocabulary.CaseLifecyclePhase, string(vocabulary.CasePhaseColdOpen)),
			loaderFact(vocabulary.CaseLifecyclePhase, string(vocabulary.CasePhaseInvestigation)),
			loaderFact(vocabulary.CaseMemberEvidence, testEvidence),
			loaderFact(vocabulary.CaseMemberSuspect, testSuspect)),
		testEvidence: loaderEntity(testEvidence,
			loaderFact(vocabulary.EvidenceRevealPhase, string(vocabulary.CasePhaseDiscovery)),
			loaderFact(vocabulary.EvidenceRevealKindPredicate, string(vocabulary.EvidenceRevealInvestigate)),
			loaderFact(vocabulary.EvidenceRevealTarget, testTarget)),
		beliefID: loaderEntity(beliefID,
			loaderFact(vocabulary.BeliefActorHolder, testSuspect),
			loaderFact(vocabulary.BeliefEvidenceRef, testEvidence),
			loaderFact(vocabulary.BeliefStanceCurrent, string(vocabulary.BeliefDenies)),
			loaderFact(vocabulary.WorldEntityDescription, "I never touched that wire.")),
	}, query: map[string][]string{
		vocabulary.BeliefActorHolder.String() + "\x00" + testSuspect: {beliefID},
	}}
	loader, err := NewLoader(reader, loaderDecisions{record: &payload.CaseDecisionRecord{
		TurnID: decision.TurnID, ActionID: decision.ActionID,
		Status: payload.CaseDecisionStatusDecision, Decision: decision,
	}}, testCase)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	loaded, err := loader.Load(t.Context(), turnEntityID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Applicable || loaded.Preflight.ActingActorID != testActor ||
		loaded.Preflight.CasePhase != vocabulary.CasePhaseInvestigation ||
		!loaded.Preflight.CaseEvidence[testEvidence] || !loaded.Preflight.CaseCharacters[testSuspect] {
		t.Fatalf("base preflight = %#v", loaded.Preflight)
	}
	gotEligibility := loaded.Preflight.Eligibility[testEvidence]
	if gotEligibility.MinimumPhase != vocabulary.CasePhaseDiscovery ||
		len(gotEligibility.Kinds) != 1 || len(gotEligibility.Targets) != 1 {
		t.Fatalf("eligibility = %#v", gotEligibility)
	}
	belief := loaded.Preflight.Beliefs[beliefKey{ActorID: testSuspect, EvidenceID: testEvidence}]
	if belief.ID != beliefID || belief.Stance != vocabulary.BeliefDenies || belief.Prose == "" {
		t.Fatalf("authored belief = %#v", belief)
	}
}

func TestLoader_NonMysteryNoOpStopsAfterDecisionArtifact(t *testing.T) {
	turnEntityID := "c360.semmachina.test.bellweather.turn.turn-act-1"
	ref := content.Ref{Instance: "TEST", Key: "turn/turn-act-1/case-decision"}
	reader := &loaderGraph{entities: map[string]*graph.EntityState{
		turnEntityID: loaderEntity(turnEntityID, loaderFact(vocabulary.TurnCaseDecisionRef, ref.String())),
	}}
	loader, err := NewLoader(reader, loaderDecisions{record: &payload.CaseDecisionRecord{
		TurnID: "turn-act-1", ActionID: "act-1", Status: payload.CaseDecisionStatusNotApplicable,
	}}, "")
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	loaded, err := loader.Load(t.Context(), turnEntityID)
	if err != nil || loaded.Applicable || loaded.TurnID != "turn-act-1" {
		t.Fatalf("non-mystery load = %#v, %v", loaded, err)
	}
	if _, err := loader.Load(t.Context(), "c360.semmachina.test.bellweather.turn.missing"); !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("missing turn error = %v", err)
	}
}

func TestLoader_RejectsValidForeignTurnDecisionBeforeAuthorization(t *testing.T) {
	turnEntityID := "c360.semmachina.test.bellweather.turn.turn-act-1"
	ref := content.Ref{Instance: "TEST", Key: "turn/turn-act-foreign/case-decision"}
	foreign := validPreflight().Decision
	foreign.ActionID = "act-foreign"
	foreign.TurnID = payload.TurnIDForAction(foreign.ActionID)
	foreign.DecisionID = payload.CaseDecisionID(
		foreign.TurnID, foreign.ActionID, foreign.CaseID, foreign.ActorID)
	record := &payload.CaseDecisionRecord{
		TurnID: foreign.TurnID, ActionID: foreign.ActionID,
		Status: payload.CaseDecisionStatusDecision, Decision: foreign,
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("foreign fixture must be independently valid: %v", err)
	}
	reader := &loaderGraph{entities: map[string]*graph.EntityState{
		turnEntityID: loaderEntity(turnEntityID,
			loaderFact(vocabulary.TurnCaseDecisionRef, ref.String())),
	}}
	loader, err := NewLoader(reader, loaderDecisions{record: record}, testCase)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if _, err := loader.Load(t.Context(), turnEntityID); !IsReason(err, ReasonWrongTurn) {
		t.Fatalf("foreign-turn load error = %v, want %q", err, ReasonWrongTurn)
	}
}
