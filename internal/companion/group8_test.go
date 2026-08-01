package companion

import (
	"encoding/json"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestNextHintLevel_EmitsCurrentLevelThenSaturates(t *testing.T) {
	want := []vocabulary.HintLevel{
		vocabulary.HintLevelNudge,
		vocabulary.HintLevelConnect,
		vocabulary.HintLevelNextStep,
		vocabulary.HintLevelNextStep,
	}
	level := vocabulary.HintLevelNudge
	for index, expected := range want {
		emit, next, err := NextHintLevel(level)
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		if emit != expected {
			t.Fatalf("step %d emitted %q, want %q", index, emit, expected)
		}
		level = next
	}
}

func TestCompanionTask_IsGenerationZeroAndCappedAtOne(t *testing.T) {
	spec := persona.Companion()
	if spec.MaxIterations != 1 {
		t.Fatalf("companion cap = %d", spec.MaxIterations)
	}
	task, err := spec.Task(persona.TaskRequest{Identity: persona.Identity{
		TurnID: "turn-act-1", TurnEntityID: "c360.semmachina.world1.starter.turn.turn-act-1",
		ActionID: "act-1", SceneID: locationID, ContextRef: locationID,
		PlayerID: playerID, CompanionID: companionID,
		BondID: "c360.semmachina.world1.starter.companion-bond.a",
	}, Prompt: "authorized warning"})
	if err != nil {
		t.Fatal(err)
	}
	if task.TaskID != "companion-turn-act-1" || task.MaxIterations == nil || *task.MaxIterations != 1 {
		t.Fatalf("task id/cap = %q/%v", task.TaskID, task.MaxIterations)
	}
}

func TestSelectHintEvidence_IsStableClosedAndProjectionBound(t *testing.T) {
	const (
		companion = "c360.semmachina.world1.starter.character.wren"
		e1        = "c360.semmachina.world1.starter.evidence.a"
		e2        = "c360.semmachina.world1.starter.evidence.b"
		e3        = "c360.semmachina.world1.starter.evidence.c"
		e4        = "c360.semmachina.world1.starter.evidence.outside"
	)
	projection := &epistemic.Projection{
		Purpose:    epistemic.PurposeCompanion,
		Neighbours: []epistemic.Entity{{ID: e3}, {ID: e1}, {ID: e2}},
	}
	record := func(id, holder, evidence string) graph.EntityState {
		return graph.EntityState{ID: id, MessageType: message.Type{Domain: payload.Domain, Category: "knowledge_grant_entity", Version: payload.SchemaVersion}, Version: 1,
			Triples: []message.Triple{
				{Subject: id, Predicate: vocabulary.WorldEntityKind.String(), Object: string(vocabulary.EntityKindKnowledge)},
				{Subject: id, Predicate: vocabulary.KnowledgeActorHolder.String(), Object: holder},
				{Subject: id, Predicate: vocabulary.KnowledgeEvidenceRef.String(), Object: evidence},
			}}
	}
	records := []graph.EntityState{
		record("c360.semmachina.world1.starter.knowledge.c", companion, e3),
		record("c360.semmachina.world1.starter.knowledge.a", companion, e1),
		record("c360.semmachina.world1.starter.knowledge.b", companion, e2),
		record("c360.semmachina.world1.starter.knowledge.duplicate", companion, e2),
		record("c360.semmachina.world1.starter.knowledge.outside", companion, e4),
		record("c360.semmachina.world1.starter.knowledge.other", "c360.semmachina.world1.starter.character.other", e1),
	}

	for seed := int64(0); seed < 16; seed++ {
		rand.New(rand.NewSource(seed)).Shuffle(len(records), func(i, j int) { records[i], records[j] = records[j], records[i] })
		got, err := SelectHintEvidence(projection, companion, records, vocabulary.HintLevelNextStep)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if want := []string{e1, e2, e3}; !slices.Equal(got, want) {
			t.Fatalf("seed %d refs = %v, want %v", seed, got, want)
		}
	}
	projection.HasSolution = true
	if _, err := SelectHintEvidence(projection, companion, records, vocabulary.HintLevelNudge); err == nil {
		t.Fatal("solution-bearing projection was accepted")
	}
}

func TestCompanionStageRecord_ClosedStatusesAndStructuralTriples(t *testing.T) {
	turnID := "turn-act-1"
	turnEntityID := "c360.semmachina.world1.starter.turn." + turnID
	ref := "obj://TEST_CONTENT/turn/turn-act-1/companion-stage"
	record := &payload.CompanionStageRecord{
		TurnID: turnID, PlayerID: playerID, CompanionID: companionID,
		BondID:        "c360.semmachina.world1.starter.companion-bond.a",
		Status:        payload.CompanionStageDecision,
		TriggerKind:   vocabulary.CompanionTriggerPlayerHint,
		TriggerSource: vocabulary.CompanionTriggerSourceCaseDecision,
		DecisionRef:   "obj://TEST_CONTENT/turn/turn-act-1/companion-decision",
	}
	triples, err := record.Triples(turnEntityID, ref, "stage-companion", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]any)
	for _, triple := range triples {
		got[triple.Predicate] = triple.Object
	}
	if got[vocabulary.TurnCompanionStageRef.String()] != ref ||
		got[vocabulary.TurnCompanionTriggerKind.String()] != string(vocabulary.CompanionTriggerPlayerHint) ||
		got[vocabulary.TurnCompanionTriggerSource.String()] != string(vocabulary.CompanionTriggerSourceCaseDecision) {
		t.Fatalf("triples = %#v", got)
	}
	record.Status = "prose"
	if err := record.Validate(); err == nil {
		t.Fatal("open companion stage status passed validation")
	}
	data, err := json.Marshal(map[string]any{
		"turn_id": turnID, "player_id": playerID, "status": "no-active-bond", "prose": "never graph this",
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded payload.CompanionStageRecord
	if err := json.Unmarshal(data, &decoded); err == nil {
		t.Fatal("companion stage record accepted undeclared prose")
	}
}
