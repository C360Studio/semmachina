package persona_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testCaseID = "c360.semmachina.world1.starter.case.bellweather"

func casekeeperIdentity() persona.Identity {
	i := testIdentity()
	i.CaseID = testCaseID
	i.ActorID = testCharacterID
	return i
}

func TestCasekeeperSpec_IsMidBoundedAndHasOneStrictTerminalTool(t *testing.T) {
	spec := persona.Casekeeper()
	if spec.Role != persona.RoleCasekeeper || spec.Slot != persona.SlotMid ||
		spec.Capability != persona.CapabilityCasekeeping {
		t.Fatalf("casekeeper spec = %+v", spec)
	}
	if spec.Artifact != vocabulary.TurnCaseDecisionRef || spec.MaxIterations < 1 {
		t.Fatalf("casekeeper artifact/budget = %s/%d", spec.Artifact, spec.MaxIterations)
	}
	if !spec.Tool.Strict || spec.Tool.Name != persona.CaseDecisionToolName {
		t.Fatalf("casekeeper tool = %+v", spec.Tool)
	}
	properties := spec.Tool.Parameters["properties"].(map[string]any)
	for _, forbidden := range []string{
		"decision_id", "turn_id", "action_id", "case_id", "actor_id", "prose", "rationale",
	} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("casekeeper asks model for engine/private field %q", forbidden)
		}
	}
	for _, required := range []string{
		"kind", "target_refs", "reveal_refs", "culprit_ref", "method_ref", "motive_ref",
	} {
		if _, exists := properties[required]; !exists {
			t.Fatalf("casekeeper schema omits %q", required)
		}
	}
}

func TestCasekeeperTask_InjectsCaseAndActorIdentity(t *testing.T) {
	task, err := persona.Casekeeper().Task(persona.TaskRequest{
		Identity: casekeeperIdentity(), Prompt: "private case projection",
	})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	identity, err := persona.CaseIdentityFrom(task.Metadata)
	if err != nil {
		t.Fatalf("CaseIdentityFrom: %v", err)
	}
	if identity != casekeeperIdentity() {
		t.Fatalf("case identity = %+v, want %+v", identity, casekeeperIdentity())
	}
	if len(task.Tools) != 1 || task.Tools[0].Name != persona.CaseDecisionToolName {
		t.Fatalf("casekeeper tools = %+v", task.Tools)
	}
}

func TestCaseDecisionExecutor_InjectsIdentityStoresBeforeGraphAndStops(t *testing.T) {
	j := &journal{}
	artifacts := newFakeArtifacts(j)
	graph := newFakeGraph(j)
	graph.seedTurn()
	executor, err := persona.NewCaseDecisionExecutor(
		artifacts, graph, persona.WithClock(func() time.Time { return testTime }),
	)
	if err != nil {
		t.Fatalf("NewCaseDecisionExecutor: %v", err)
	}
	task, err := persona.Casekeeper().Task(persona.TaskRequest{
		Identity: casekeeperIdentity(), Prompt: "private case projection",
	})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	result, err := executor.Execute(context.Background(), agentic.ToolCall{
		ID: "call-case", Name: persona.CaseDecisionToolName, Metadata: task.Metadata,
		Arguments: map[string]any{
			"kind":        "question",
			"target_refs": []any{testSentryID},
			"reveal_refs": []any{testCrowbarID},
			"culprit_ref": "", "method_ref": "", "motive_ref": "",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.StopLoop || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	if len(j.entries) != 2 || !strings.HasPrefix(j.entries[0], "put ") ||
		!strings.HasPrefix(j.entries[1], "merge ") {
		t.Fatalf("write order = %v, want store then graph", j.entries)
	}
	var record payload.CaseDecisionRecord
	if err := json.Unmarshal([]byte(result.Content), &record); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if record.Decision == nil || record.Decision.CaseID != testCaseID ||
		record.Decision.ActorID != testCharacterID || record.Decision.TurnID != testTurnID {
		t.Fatalf("engine identity not injected: %+v", record.Decision)
	}
	if record.Decision.DecisionID != payload.CaseDecisionID(
		testTurnID, testActionID, testCaseID, testCharacterID,
	) {
		t.Fatalf("decision id = %q", record.Decision.DecisionID)
	}
	if got := firstObject(t, graph.entities[testTurnEntityID], vocabulary.TurnCaseDecisionKind); got != "question" {
		t.Fatalf("kind triple = %v", got)
	}
}

func TestCaseDecisionExecutor_InvalidArgumentsWriteNothing(t *testing.T) {
	j := &journal{}
	artifacts := newFakeArtifacts(j)
	graph := newFakeGraph(j)
	graph.seedTurn()
	executor, err := persona.NewCaseDecisionExecutor(artifacts, graph)
	if err != nil {
		t.Fatalf("NewCaseDecisionExecutor: %v", err)
	}
	task, _ := persona.Casekeeper().Task(persona.TaskRequest{
		Identity: casekeeperIdentity(), Prompt: "private case projection",
	})
	for _, args := range []map[string]any{
		{"kind": "invented", "target_refs": []any{}, "reveal_refs": []any{},
			"culprit_ref": "", "method_ref": "", "motive_ref": ""},
		{"kind": "observe", "target_refs": []any{}, "reveal_refs": []any{},
			"culprit_ref": "", "method_ref": "", "motive_ref": "", "prose": "secret"},
	} {
		before := append([]string(nil), j.entries...)
		result, execErr := executor.Execute(context.Background(), agentic.ToolCall{
			ID: "bad", Name: persona.CaseDecisionToolName, Metadata: task.Metadata, Arguments: args,
		})
		if execErr != nil || result.ErrorKind != agentic.ToolErrorInvalidArgs || result.StopLoop {
			t.Fatalf("invalid result = %+v, err=%v", result, execErr)
		}
		if !reflect.DeepEqual(j.entries, before) || graph.merges != 0 {
			t.Fatalf("invalid call wrote state: journal=%v merges=%d", j.entries, graph.merges)
		}
	}
}

func TestCaseDecisionExecutor_DuplicateDeliveryConvergesOnOneLogicalDecision(t *testing.T) {
	j := &journal{}
	artifacts := newFakeArtifacts(j)
	graph := newFakeGraph(j)
	graph.seedTurn()
	executor, err := persona.NewCaseDecisionExecutor(artifacts, graph)
	if err != nil {
		t.Fatalf("NewCaseDecisionExecutor: %v", err)
	}
	task, _ := persona.Casekeeper().Task(persona.TaskRequest{
		Identity: casekeeperIdentity(), Prompt: "private case projection",
	})
	call := agentic.ToolCall{
		ID: "duplicate", Name: persona.CaseDecisionToolName, Metadata: task.Metadata,
		Arguments: map[string]any{
			"kind": "observe", "target_refs": []any{}, "reveal_refs": []any{},
			"culprit_ref": "", "method_ref": "", "motive_ref": "",
		},
	}
	var decisionIDs []string
	for range 2 {
		result, execErr := executor.Execute(context.Background(), call)
		if execErr != nil || !result.StopLoop {
			t.Fatalf("duplicate Execute result=%+v err=%v", result, execErr)
		}
		var record payload.CaseDecisionRecord
		if err := json.Unmarshal([]byte(result.Content), &record); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		decisionIDs = append(decisionIDs, record.Decision.DecisionID)
	}
	if decisionIDs[0] != decisionIDs[1] || len(artifacts.decisions) != 1 {
		t.Fatalf("duplicate decisions=%v stored artifacts=%d", decisionIDs, len(artifacts.decisions))
	}
	if got := objectsFor(graph.entities[testTurnEntityID], vocabulary.TurnCaseDecisionRef); len(got) != 1 {
		t.Fatalf("turn holds %d case-decision refs after duplicate delivery: %v", len(got), got)
	}
	if got := objectsFor(graph.entities[testTurnEntityID], vocabulary.TurnCaseDecisionKind); len(got) != 1 {
		t.Fatalf("turn holds %d case-decision kinds after duplicate delivery: %v", len(got), got)
	}
}
