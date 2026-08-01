package companion

import (
	"context"
	"errors"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	semerrs "github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	turnID       = "turn-act-1"
	turnEntityID = "c360.semmachina.world1.starter.turn.turn-act-1"
)

type decisionStore struct {
	puts     int
	decision *payload.CompanionDecision
	record   *payload.CompanionStageRecord
	err      error
}

func (s *decisionStore) PutCompanionStageRecord(_ context.Context, _ string, r *payload.CompanionStageRecord) (content.Ref, error) {
	stored := *r
	s.record = &stored
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/companion-stage"}, nil
}

func (s *decisionStore) PutCompanionDecision(_ context.Context, _ string, d *payload.CompanionDecision) (content.Ref, error) {
	s.puts++
	if s.err != nil {
		return content.Ref{}, s.err
	}
	stored := *d
	s.decision = &stored
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/companion-decision"}, nil
}

type decisionWriter struct{ merges int }

func (w *decisionWriter) MergeTriples(_ context.Context, _ string, _ []message.Triple, _ ...graphio.MergeOption) (*graph.EntityState, error) {
	w.merges++
	return &graph.EntityState{}, nil
}

type decisionProjector struct {
	evidence string
	err      error
}

func (p decisionProjector) Project(context.Context, epistemic.AuthenticatedAudience) (*epistemic.Projection, error) {
	if p.err != nil {
		return nil, p.err
	}
	projection := &epistemic.Projection{Purpose: epistemic.PurposeCompanion}
	if p.evidence != "" {
		projection.Neighbours = []epistemic.Entity{{ID: p.evidence, Facts: []epistemic.Fact{{Predicate: vocabulary.WorldEntityKind, Object: string(vocabulary.EntityKindEvidence)}}}}
	}
	return projection, nil
}

func companionMetadata(t *testing.T, bondID string) map[string]any {
	t.Helper()
	task, err := persona.Companion().Task(persona.TaskRequest{
		Identity: persona.Identity{
			TurnID: turnID, TurnEntityID: turnEntityID, ActionID: "act-1",
			SceneID: locationID, ContextRef: locationID, PlayerID: playerID,
			CompanionID: companionID, BondID: bondID,
		}, Prompt: "authorized companion projection",
	})
	if err != nil {
		t.Fatal(err)
	}
	return task.Metadata
}

func newDecisionExecutor(t *testing.T, projection decisionProjector) (*Executor, *decisionStore, *decisionWriter, string) {
	t.Helper()
	authority, graphStore, bondID := validAuthority(t)
	graphStore.states[bondID].Triples[3].Object = string(vocabulary.CompanionPolicyBoundedInitiative)
	graphStore.states[turnEntityID] = &graph.EntityState{ID: turnEntityID, Triples: []message.Triple{
		{Subject: turnEntityID, Predicate: vocabulary.TurnRollBand.String(), Object: string(vocabulary.BandMiss)},
		{Subject: turnEntityID, Predicate: vocabulary.TurnVerdictRisk.String(), Object: string(vocabulary.RiskHigh)},
		{Subject: turnEntityID, Predicate: vocabulary.TurnVerdictConsequence.String(), Object: string(vocabulary.ConsequenceHarm)},
		{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionTriggerKind.String(), Object: string(vocabulary.CompanionTriggerWarning)},
		{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionTriggerSource.String(), Object: string(vocabulary.CompanionTriggerSourceResolvedRisk)},
	}}
	store := &decisionStore{}
	writer := &decisionWriter{}
	executor, err := NewExecutor(store, writer, authority, projection)
	if err != nil {
		t.Fatal(err)
	}
	return executor, store, writer, bondID
}

func TestExecutor_UnauthorizedEvidenceIsCorrectableAndWritesNothing(t *testing.T) {
	executor, store, writer, bondID := newDecisionExecutor(t, decisionProjector{})
	result, err := executor.Execute(t.Context(), agentic.ToolCall{
		ID: "call-1", Name: persona.CompanionDecisionToolName, Metadata: companionMetadata(t, bondID),
		Arguments: map[string]any{"kind": "recall", "hint_level": "", "evidence_refs": []any{evidenceID}, "target_ref": ""},
	})
	if err != nil {
		t.Fatalf("correctable refusal returned error: %v", err)
	}
	if result.ErrorKind != agentic.ToolErrorInvalidArgs || result.StopLoop {
		t.Fatalf("result = %+v", result)
	}
	if store.puts != 0 || writer.merges != 0 {
		t.Fatalf("unauthorized evidence wrote store=%d graph=%d", store.puts, writer.merges)
	}
}

func TestExecutor_InjectsIdentityStoresThenWritesReference(t *testing.T) {
	executor, store, writer, bondID := newDecisionExecutor(t, decisionProjector{evidence: evidenceID})
	result, err := executor.Execute(t.Context(), agentic.ToolCall{
		ID: "call-1", Name: persona.CompanionDecisionToolName, Metadata: companionMetadata(t, bondID),
		Arguments: map[string]any{"kind": "warning", "hint_level": "", "evidence_refs": []any{evidenceID}, "target_ref": locationID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.StopLoop || result.ErrorKind != "" || store.puts != 1 || writer.merges != 1 {
		t.Fatalf("result=%+v puts=%d merges=%d", result, store.puts, writer.merges)
	}
	if store.decision.PlayerID != playerID || store.decision.CompanionID != companionID || store.decision.ContextRef != locationID {
		t.Fatalf("runtime identity not injected: %+v", store.decision)
	}
}

func TestExecutor_CorruptBondIsPermanentAndProjectionOrStorageFaultsAreTransient(t *testing.T) {
	executor, store, writer, bondID := newDecisionExecutor(t, decisionProjector{
		err: semerrs.WrapTransient(errors.New("projection unavailable"), "test", "Project", "read graph"),
	})
	result, err := executor.Execute(t.Context(), agentic.ToolCall{
		ID: "call-1", Name: persona.CompanionDecisionToolName, Metadata: companionMetadata(t, bondID),
		Arguments: map[string]any{"kind": "silent", "hint_level": "", "evidence_refs": []any{}, "target_ref": ""},
	})
	if err == nil || result.ErrorKind != agentic.ToolErrorNetwork {
		t.Fatalf("projection fault result=%+v err=%v", result, err)
	}
	if store.puts != 0 || writer.merges != 0 {
		t.Fatal("projection fault wrote state")
	}
}

func TestExecutor_ProjectionIntegrityFailureIsPermanent(t *testing.T) {
	executor, store, writer, bondID := newDecisionExecutor(t,
		decisionProjector{err: errors.New("projection carries ambiguous companion bond")})
	result, err := executor.Execute(t.Context(), agentic.ToolCall{
		ID: "call-1", Name: persona.CompanionDecisionToolName, Metadata: companionMetadata(t, bondID),
		Arguments: map[string]any{"kind": "silent", "hint_level": "", "evidence_refs": []any{}, "target_ref": ""},
	})
	if err == nil || result.ErrorKind != agentic.ToolErrorInternal {
		t.Fatalf("projection integrity result=%+v err=%v", result, err)
	}
	if store.puts != 0 || writer.merges != 0 {
		t.Fatal("projection integrity failure wrote state")
	}
}

func TestExecutor_ResidentDecisionCorruptionIsPermanent(t *testing.T) {
	executor, store, writer, bondID := newDecisionExecutor(t, decisionProjector{})
	store.err = content.ErrArtifactCorrupt
	result, err := executor.Execute(t.Context(), agentic.ToolCall{
		ID: "call-1", Name: persona.CompanionDecisionToolName, Metadata: companionMetadata(t, bondID),
		Arguments: map[string]any{"kind": "silent", "hint_level": "", "evidence_refs": []any{}, "target_ref": ""},
	})
	if err == nil || result.ErrorKind != agentic.ToolErrorInternal || writer.merges != 0 {
		t.Fatalf("resident corruption result=%+v err=%v merges=%d", result, err, writer.merges)
	}
}

func TestExecutor_ExhaustionCommitsSilentStageResultInsteadOfFailingTurn(t *testing.T) {
	executor, store, writer, bondID := newDecisionExecutor(t, decisionProjector{})
	identity, err := persona.CompanionIdentityFrom(companionMetadata(t, bondID))
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Exhaust(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if store.decision == nil || store.decision.Kind != payload.CompanionDecisionSilent ||
		store.record == nil || store.record.Status != payload.CompanionStageExhausted || writer.merges != 1 {
		t.Fatalf("decision=%+v record=%+v merges=%d", store.decision, store.record, writer.merges)
	}
}
