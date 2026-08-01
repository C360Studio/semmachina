package stage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	companionpkg "github.com/c360studio/semmachina/internal/companion"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

const (
	companionTurnID     = "turn-act-1"
	companionTurnEntity = "c360.semmachina.world1.starter.turn.turn-act-1"
	companionPlayer     = "c360.semmachina.world1.starter.player.p1"
	companionActor      = "c360.semmachina.world1.starter.character.rook"
	companionCharacter  = "c360.semmachina.world1.starter.character.wren"
	companionScene      = "c360.semmachina.world1.starter.scene.gatehouse"
	companionEvidence   = "c360.semmachina.world1.starter.evidence.scrap"
)

type companionRecorder struct{}

func (companionRecorder) Advance(context.Context, string, string, vocabulary.TurnPhase) (turn.Transition, error) {
	return turn.Transition{Outcome: turn.OutcomeAdvanced}, nil
}

type companionGraph struct {
	mu          sync.Mutex
	states      map[string]*graph.EntityState
	mergeCalls  int
	failMergeAt int
}

func (g *companionGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.states[id]
	if state == nil {
		return nil, graphio.ErrEntityNotFound
	}
	return state.Clone(), nil
}
func (g *companionGraph) GetEntities(_ context.Context, ids []string) (graphio.BatchResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out graphio.BatchResult
	for _, id := range ids {
		if state := g.states[id]; state != nil {
			out.Entities = append(out.Entities, *state.Clone())
		} else {
			out.Missing = append(out.Missing, graph.MissingEntity{ID: id})
		}
	}
	return out, nil
}
func (g *companionGraph) EntitiesByPredicateValue(_ context.Context, predicate, value string, limit int) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var ids []string
	for id, state := range g.states {
		for _, triple := range state.Triples {
			if triple.Predicate == predicate && triple.Object == value {
				ids = append(ids, id)
				break
			}
		}
		if len(ids) == limit {
			break
		}
	}
	return ids, nil
}
func (g *companionGraph) MergeTriples(_ context.Context, id string, triples []message.Triple, _ ...graphio.MergeOption) (*graph.EntityState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mergeCalls++
	if g.mergeCalls == g.failMergeAt {
		return nil, errors.New("injected companion graph merge failure")
	}
	state := g.states[id]
	for _, incoming := range triples {
		kept := state.Triples[:0]
		for _, resident := range state.Triples {
			if resident.Predicate != incoming.Predicate {
				kept = append(kept, resident)
			}
		}
		state.Triples = append(kept, incoming)
	}
	return state.Clone(), nil
}

type companionArtifacts struct {
	decision *payload.CompanionDecision
	record   *payload.CompanionStageRecord
}

type keyedCompanionArtifacts struct {
	mu        sync.Mutex
	decisions map[string]*payload.CompanionDecision
	records   map[string]*payload.CompanionStageRecord
}

func (*keyedCompanionArtifacts) InstanceName() string { return "TEST" }
func (a *keyedCompanionArtifacts) PutCompanionDecision(
	_ context.Context, _ string, decision *payload.CompanionDecision,
) (content.Ref, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key, err := content.KeyFor(vocabulary.TurnCompanionDecisionRef, content.SubjectTurn, decision.TurnID)
	if err != nil {
		return content.Ref{}, err
	}
	a.decisions[key] = decision
	return content.Ref{Instance: "TEST", Key: key}, nil
}
func (a *keyedCompanionArtifacts) GetCompanionDecision(_ context.Context, ref content.Ref) (*payload.CompanionDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	decision := a.decisions[ref.Key]
	if decision == nil {
		return nil, content.ErrArtifactNotFound
	}
	return decision, nil
}
func (a *keyedCompanionArtifacts) PutCompanionStageRecord(
	_ context.Context, _ string, record *payload.CompanionStageRecord,
) (content.Ref, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key, err := content.KeyFor(vocabulary.TurnCompanionStageRef, content.SubjectTurn, record.TurnID)
	if err != nil {
		return content.Ref{}, err
	}
	a.records[key] = record
	return content.Ref{Instance: "TEST", Key: key}, nil
}
func (a *keyedCompanionArtifacts) GetCompanionStageRecord(_ context.Context, ref content.Ref) (*payload.CompanionStageRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record := a.records[ref.Key]
	if record == nil {
		return nil, content.ErrArtifactNotFound
	}
	return record, nil
}

func (*companionArtifacts) InstanceName() string { return "TEST" }
func (a *companionArtifacts) PutCompanionDecision(_ context.Context, _ string, d *payload.CompanionDecision) (content.Ref, error) {
	a.decision = d
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/companion-decision"}, nil
}
func (a *companionArtifacts) GetCompanionDecision(context.Context, content.Ref) (*payload.CompanionDecision, error) {
	if a.decision == nil {
		return nil, content.ErrArtifactNotFound
	}
	return a.decision, nil
}
func (a *companionArtifacts) PutCompanionStageRecord(_ context.Context, _ string, r *payload.CompanionStageRecord) (content.Ref, error) {
	a.record = r
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/companion-stage"}, nil
}
func (a *companionArtifacts) GetCompanionStageRecord(context.Context, content.Ref) (*payload.CompanionStageRecord, error) {
	if a.record == nil {
		return nil, content.ErrArtifactNotFound
	}
	return a.record, nil
}

type companionProjector struct {
	calls      int
	projection *epistemic.Projection
}

type gatedCompanionProjector struct {
	projection *epistemic.Projection
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
}

func (p *gatedCompanionProjector) Project(
	_ context.Context, _ epistemic.AuthenticatedAudience,
) (*epistemic.Projection, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return p.projection, nil
}

func (p *companionProjector) Project(_ context.Context, _ epistemic.AuthenticatedAudience) (*epistemic.Projection, error) {
	p.calls++
	return p.projection, nil
}

type companionPrompt struct{ calls int }

func (p *companionPrompt) Companion(_ context.Context, projection *epistemic.Projection) (persona.TaskRequest, error) {
	p.calls++
	return persona.TaskRequest{Identity: persona.Identity{TurnID: companionTurnID,
		TurnEntityID: companionTurnEntity, ActionID: "act-1", SceneID: companionScene,
		ContextRef: companionScene, PlayerID: companionPlayer, CompanionID: companionCharacter,
		BondID: projection.BondID}, Prompt: "warn structurally"}, nil
}

type companionTasks struct {
	calls   int
	subject string
	msgID   string
}

func (p *companionTasks) PublishToStreamWithMsgID(_ context.Context, subject string, _ []byte, msgID string) error {
	p.calls++
	p.subject, p.msgID = subject, msgID
	return nil
}

func importedState(id string, kind vocabulary.EntityKind, triples ...message.Triple) *graph.EntityState {
	at := time.Now().UTC()
	facts := []message.Triple{{Subject: id, Predicate: vocabulary.WorldEntityKind.String(), Object: string(kind),
		Source: payload.WorldImportSource, Context: "starter@1", Timestamp: at}}
	for index := range triples {
		triples[index].Subject, triples[index].Source, triples[index].Context = id, payload.WorldImportSource, "starter@1"
	}
	return &graph.EntityState{ID: id, MessageType: (&payload.WorldEntity{}).Schema(), Version: 1,
		Triples: append(facts, triples...)}
}

func companionFixture(t *testing.T, withBond bool, policy vocabulary.CompanionPolicy) (*CompanionStage, *companionGraph, *companionArtifacts, *companionProjector, *companionPrompt, *companionTasks, string) {
	t.Helper()
	g := &companionGraph{states: map[string]*graph.EntityState{}}
	g.states[companionTurnEntity] = &graph.EntityState{ID: companionTurnEntity, Triples: []message.Triple{
		{Subject: companionTurnEntity, Predicate: vocabulary.TurnActionPlayer.String(), Object: companionPlayer},
		{Subject: companionTurnEntity, Predicate: vocabulary.TurnActionScene.String(), Object: companionScene},
	}}
	g.states[companionPlayer] = importedState(companionPlayer, vocabulary.EntityKindPlayer,
		message.Triple{Predicate: vocabulary.PlayerCharacterCurrent.String(), Object: companionActor})
	g.states[companionActor] = importedState(companionActor, vocabulary.EntityKindCharacter,
		message.Triple{Predicate: vocabulary.WorldLocationCurrent.String(), Object: companionScene})
	g.states[companionCharacter] = importedState(companionCharacter, vocabulary.EntityKindCharacter,
		message.Triple{Predicate: vocabulary.CompanionCandidatePolicy.String(), Object: string(vocabulary.CompanionPolicyBoundedInitiative)},
		message.Triple{Predicate: vocabulary.WorldLocationCurrent.String(), Object: companionScene})
	bondID := ""
	if withBond {
		var err error
		bondID, err = world.CompanionBondID("c360", "world1", "starter", companionPlayer, companionCharacter)
		if err != nil {
			t.Fatal(err)
		}
		g.states[bondID] = importedState(bondID, vocabulary.EntityKindCompanionBond,
			message.Triple{Predicate: vocabulary.CompanionBondPlayer.String(), Object: companionPlayer},
			message.Triple{Predicate: vocabulary.CompanionBondCharacter.String(), Object: companionCharacter},
			message.Triple{Predicate: vocabulary.CompanionBondPolicy.String(), Object: string(policy)},
			message.Triple{Predicate: vocabulary.CompanionBondHintLevel.String(), Object: string(vocabulary.HintLevelNudge)})
	}
	authority, err := companionpkg.NewAuthority(g)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &companionArtifacts{}
	projector := &companionProjector{projection: &epistemic.Projection{Purpose: epistemic.PurposeCompanion,
		TurnID: companionTurnID, TurnEntityID: companionTurnEntity, SceneID: companionScene,
		ContextRef: companionScene, CompanionID: companionCharacter, BondID: bondID,
		Actor: epistemic.Actor{PlayerID: companionPlayer, CharacterID: companionCharacter}}}
	prompt, tasks := &companionPrompt{}, &companionTasks{}
	stage, err := NewCompanionStage(companionRecorder{}, g, artifacts, authority, projector, prompt, tasks)
	if err != nil {
		t.Fatal(err)
	}
	return stage, g, artifacts, projector, prompt, tasks, bondID
}

func companionTrigger() Trigger {
	return Trigger{TurnID: companionTurnID, TurnEntityID: companionTurnEntity, Subject: "semmachina.turn.companion"}
}

func TestCompanionStage_NoBondAndNoTriggerCommitExactZeroModelResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		bond bool
		want payload.CompanionStageStatus
	}{{"no bond", false, payload.CompanionStageNoActiveBond}, {"no trigger", true, payload.CompanionStageNoTrigger}} {
		t.Run(tc.name, func(t *testing.T) {
			stage, _, artifacts, projector, prompt, tasks, _ := companionFixture(t, tc.bond, vocabulary.CompanionPolicyReactive)
			if err := stage.Run(t.Context(), companionTrigger()); err != nil {
				t.Fatal(err)
			}
			if artifacts.record == nil || artifacts.record.Status != tc.want || tasks.calls != 0 || prompt.calls != 0 {
				t.Fatalf("record=%+v tasks=%d prompts=%d", artifacts.record, tasks.calls, prompt.calls)
			}
			if !tc.bond && projector.calls != 0 {
				t.Fatal("no-bond path projected model context")
			}
		})
	}
}

func TestCompanionStage_ResidentNoOpRecordHealsMissingTurnReference(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bond        bool
		failMergeAt int
		want        payload.CompanionStageStatus
	}{{"no active bond", false, 1, payload.CompanionStageNoActiveBond},
		{"no trigger", true, 2, payload.CompanionStageNoTrigger}} {
		t.Run(tc.name, func(t *testing.T) {
			stage, graphStore, artifacts, projector, prompt, tasks, _ := companionFixture(
				t, tc.bond, vocabulary.CompanionPolicyReactive)
			graphStore.failMergeAt = tc.failMergeAt
			if err := stage.Run(t.Context(), companionTrigger()); err == nil {
				t.Fatal("injected graph failure did not interrupt the first run")
			}
			if artifacts.record == nil || artifacts.record.Status != tc.want || artifacts.record.DecisionRef != "" {
				t.Fatalf("partial resident record = %+v", artifacts.record)
			}
			if err := stage.Run(t.Context(), companionTrigger()); err != nil {
				t.Fatalf("retry did not heal resident no-op record: %v", err)
			}
			state := graphStore.states[companionTurnEntity]
			if got := len(objectsForPredicate(state, vocabulary.TurnCompanionStageRef)); got != 1 {
				t.Fatalf("healed stage refs = %d, want 1", got)
			}
			if projector.calls != 0 || prompt.calls != 0 || tasks.calls != 0 {
				t.Fatalf("no-op retry crossed model boundary: project=%d prompt=%d tasks=%d",
					projector.calls, prompt.calls, tasks.calls)
			}
		})
	}
}

func objectsForPredicate(state *graph.EntityState, predicate vocabulary.Predicate) []any {
	var objects []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			objects = append(objects, triple.Object)
		}
	}
	return objects
}

func TestCompanionStage_PlayerHintIsDeterministicAndWarningPublishesOneBoundedTask(t *testing.T) {
	stage, g, artifacts, _, prompt, tasks, bondID := companionFixture(t, true, vocabulary.CompanionPolicyReactive)
	g.states[companionTurnEntity].Triples = append(g.states[companionTurnEntity].Triples,
		message.Triple{Predicate: vocabulary.TurnCaseDecisionKind.String(), Object: string(payload.CaseDecisionRequestHint)})
	knowledgeID := "c360.semmachina.world1.starter.knowledge.scrap"
	g.states[knowledgeID] = importedState(knowledgeID, vocabulary.EntityKindKnowledge,
		message.Triple{Predicate: vocabulary.KnowledgeActorHolder.String(), Object: companionCharacter},
		message.Triple{Predicate: vocabulary.KnowledgeEvidenceRef.String(), Object: companionEvidence})
	stage.projector.(*companionProjector).projection.Neighbours = []epistemic.Entity{{ID: companionEvidence}}
	if err := stage.Run(t.Context(), companionTrigger()); err != nil {
		t.Fatal(err)
	}
	if tasks.calls != 0 || prompt.calls != 0 || artifacts.decision == nil || artifacts.decision.Kind != payload.CompanionDecisionHint {
		t.Fatalf("tasks=%d prompts=%d decision=%+v", tasks.calls, prompt.calls, artifacts.decision)
	}
	bond, err := stage.authority.ValidateBond(t.Context(), bondID, companionPlayer, companionCharacter)
	if err != nil || bond.HintLevel != vocabulary.HintLevelConnect {
		t.Fatalf("advanced bond=%+v err=%v", bond, err)
	}

	warning, wg, _, _, wp, wt, _ := companionFixture(t, true, vocabulary.CompanionPolicyBoundedInitiative)
	wg.states[companionTurnEntity].Triples = append(wg.states[companionTurnEntity].Triples,
		message.Triple{Predicate: vocabulary.TurnRollBand.String(), Object: string(vocabulary.BandPartial)},
		message.Triple{Predicate: vocabulary.TurnVerdictRisk.String(), Object: string(vocabulary.RiskHigh)},
		message.Triple{Predicate: vocabulary.TurnVerdictConsequence.String(), Object: string(vocabulary.ConsequenceEscalation)})
	if err := warning.Run(t.Context(), companionTrigger()); err != nil {
		t.Fatal(err)
	}
	if wt.calls != 1 || wp.calls != 1 || wt.subject != "agent.task.companion" || wt.msgID != "companion-turn-act-1" {
		t.Fatalf("warning task=%d prompt=%d subject=%s msg-id=%s", wt.calls, wp.calls, wt.subject, wt.msgID)
	}
}

func TestCompanionStage_ConcurrentTurnsEmitNudgeThenConnect(t *testing.T) {
	first, graphStore, _, firstProjection, prompt, tasks, bondID := companionFixture(
		t, true, vocabulary.CompanionPolicyReactive)
	graphStore.states[companionTurnEntity].Triples = append(graphStore.states[companionTurnEntity].Triples,
		message.Triple{Predicate: vocabulary.TurnCaseDecisionKind.String(), Object: string(payload.CaseDecisionRequestHint)})
	knowledgeID := "c360.semmachina.world1.starter.knowledge.scrap"
	secondEvidence := "c360.semmachina.world1.starter.evidence.thread"
	secondKnowledgeID := "c360.semmachina.world1.starter.knowledge.thread"
	graphStore.states[knowledgeID] = importedState(knowledgeID, vocabulary.EntityKindKnowledge,
		message.Triple{Predicate: vocabulary.KnowledgeActorHolder.String(), Object: companionCharacter},
		message.Triple{Predicate: vocabulary.KnowledgeEvidenceRef.String(), Object: companionEvidence})
	graphStore.states[secondKnowledgeID] = importedState(secondKnowledgeID, vocabulary.EntityKindKnowledge,
		message.Triple{Predicate: vocabulary.KnowledgeActorHolder.String(), Object: companionCharacter},
		message.Triple{Predicate: vocabulary.KnowledgeEvidenceRef.String(), Object: secondEvidence})
	firstProjection.projection.Neighbours = []epistemic.Entity{{ID: companionEvidence}, {ID: secondEvidence}}

	const secondTurnID = "turn-act-2"
	const secondTurnEntity = "c360.semmachina.world1.starter.turn.turn-act-2"
	graphStore.states[secondTurnEntity] = &graph.EntityState{ID: secondTurnEntity, Triples: []message.Triple{
		{Subject: secondTurnEntity, Predicate: vocabulary.TurnActionPlayer.String(), Object: companionPlayer},
		{Subject: secondTurnEntity, Predicate: vocabulary.TurnActionScene.String(), Object: companionScene},
		{Subject: secondTurnEntity, Predicate: vocabulary.TurnCaseDecisionKind.String(), Object: string(payload.CaseDecisionRequestHint)},
	}}
	artifacts := &keyedCompanionArtifacts{decisions: map[string]*payload.CompanionDecision{},
		records: map[string]*payload.CompanionStageRecord{}}
	entered, release := make(chan struct{}), make(chan struct{})
	firstProjector := &gatedCompanionProjector{projection: firstProjection.projection, entered: entered, release: release}
	first.artifacts, first.projector = artifacts, firstProjector
	secondProjection := *firstProjection.projection
	secondProjection.TurnID, secondProjection.TurnEntityID = secondTurnID, secondTurnEntity
	secondProjector := &companionProjector{projection: &secondProjection}
	second, err := NewCompanionStage(companionRecorder{}, graphStore, artifacts, first.authority,
		secondProjector, prompt, tasks)
	if err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 2)
	go func() { errs <- first.Run(t.Context(), companionTrigger()) }()
	<-entered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		errs <- second.Run(t.Context(), Trigger{TurnID: secondTurnID, TurnEntityID: secondTurnEntity,
			Subject: "semmachina.turn.companion"})
	}()
	<-secondStarted
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	firstKey, _ := content.KeyFor(vocabulary.TurnCompanionDecisionRef, content.SubjectTurn, companionTurnID)
	secondKey, _ := content.KeyFor(vocabulary.TurnCompanionDecisionRef, content.SubjectTurn, secondTurnID)
	firstDecision, secondDecision := artifacts.decisions[firstKey], artifacts.decisions[secondKey]
	if firstDecision == nil || secondDecision == nil ||
		firstDecision.HintLevel != vocabulary.HintLevelNudge ||
		secondDecision.HintLevel != vocabulary.HintLevelConnect {
		t.Fatalf("concurrent decisions = %+v then %+v, want nudge then connect", firstDecision, secondDecision)
	}
	bond, err := first.authority.ValidateBond(t.Context(), bondID, companionPlayer, companionCharacter)
	if err != nil || bond.HintLevel != vocabulary.HintLevelNextStep {
		t.Fatalf("concurrent final bond = %+v err=%v", bond, err)
	}
}

var _ TaskPublisher = (*companionTasks)(nil)
