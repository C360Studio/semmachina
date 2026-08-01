package accusation_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/accusation"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const turnEntityID = "c360.semmachina.bellweather.campaign.turn.turn-action-1"

type accusationGraph struct {
	entities map[string]*graph.EntityState
	writes   [][]message.Triple
	mergeErr error
	queryIDs []string
}

func (g *accusationGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	state := g.entities[id]
	if state == nil {
		return nil, errors.New("missing")
	}
	return state, nil
}
func (g *accusationGraph) MergeTriples(_ context.Context, id string, triples []message.Triple, _ ...graphio.MergeOption) (*graph.EntityState, error) {
	if g.mergeErr != nil {
		return nil, g.mergeErr
	}
	g.writes = append(g.writes, append([]message.Triple(nil), triples...))
	return g.entities[id], nil
}
func (g *accusationGraph) EntitiesByPredicateValue(context.Context, string, string, int) ([]string, error) {
	return append([]string(nil), g.queryIDs...), nil
}

type decisionStore struct{ record *payload.CaseDecisionRecord }

func (s decisionStore) GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error) {
	return s.record, nil
}

type projectorProbe struct {
	called bool
	value  *epistemic.Projection
}

func (p *projectorProbe) Project(context.Context, epistemic.AuthenticatedAudience) (*epistemic.Projection, error) {
	p.called = true
	return p.value, nil
}

func turnState(ref string) *graph.EntityState {
	return &graph.EntityState{ID: turnEntityID, Triples: []message.Triple{
		{Subject: turnEntityID, Predicate: vocabulary.TurnCaseDecisionRef.String(), Object: ref},
	}}
}

func TestLoaderRejectsForeignDecisionBeforeReadingCanonicalSolution(t *testing.T) {
	decision := accusationDecision(culpritID, methodID, motiveID)
	decision.TurnID = "turn-foreign"
	decision.ActionID = "foreign"
	decision.DecisionID = payload.CaseDecisionID(decision.TurnID, decision.ActionID, decision.CaseID, decision.ActorID)
	ref := "obj://TEST_CONTENT/turn/turn-action-1/case-decision"
	graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: turnState(ref)}}
	projection := &projectorProbe{value: &epistemic.Projection{HasSolution: true}}
	loader, err := accusation.NewLoader(graphStore, decisionStore{record: &payload.CaseDecisionRecord{
		TurnID: decision.TurnID, ActionID: decision.ActionID,
		Status: payload.CaseDecisionStatusDecision, Decision: decision,
	}}, projection, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(t.Context(), turnEntityID); err == nil {
		t.Fatal("foreign decision was accepted")
	}
	if projection.called {
		t.Fatal("loader disclosed the canonical solution before rejecting foreign identity")
	}
}

func TestLoaderMakesNoOpAndNonAccuseRecordsNonApplicableWithoutSolutionRead(t *testing.T) {
	ref := "obj://TEST_CONTENT/turn/turn-action-1/case-decision"
	for _, record := range []*payload.CaseDecisionRecord{
		{TurnID: "turn-action-1", ActionID: "action-1", Status: payload.CaseDecisionStatusNotApplicable},
		func() *payload.CaseDecisionRecord {
			decision := accusationDecision(culpritID, methodID, motiveID)
			decision.Kind = payload.CaseDecisionQuestion
			decision.CulpritRef, decision.MethodRef, decision.MotiveRef = "", "", ""
			return &payload.CaseDecisionRecord{TurnID: decision.TurnID, ActionID: decision.ActionID,
				Status: payload.CaseDecisionStatusDecision, Decision: decision}
		}(),
	} {
		graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: turnState(ref)}}
		projection := &projectorProbe{value: &epistemic.Projection{HasSolution: true}}
		loader, err := accusation.NewLoader(graphStore, decisionStore{record: record}, projection, caseID)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := loader.Load(t.Context(), turnEntityID)
		if err != nil || loaded.Applicable || loaded.TurnID != "turn-action-1" {
			t.Fatalf("Load = %+v, %v", loaded, err)
		}
		if projection.called {
			t.Fatal("non-applicable record read canonical solution")
		}
	}
}

type resultStore struct {
	puts   int
	record *content.AccusationRecord
	ref    content.Ref
	getErr error
}

func (s *resultStore) PutAccusationRecord(_ context.Context, _ string, record *content.AccusationRecord) (content.Ref, error) {
	s.puts++
	recordSnapshot := *record
	s.record = &recordSnapshot
	return s.ref, nil
}
func (s *resultStore) GetAccusationRecord(context.Context, content.Ref) (*content.AccusationRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.record == nil {
		return nil, errors.New("missing")
	}
	recordSnapshot := *s.record
	return &recordSnapshot, nil
}

func resultRecord(result *payload.AccusationResult) *content.AccusationRecord {
	return &content.AccusationRecord{TurnID: result.TurnID, Status: content.AccusationResultRecorded, Result: result}
}

func verifiedResult() *payload.AccusationResult {
	decision := accusationDecision(culpritID, methodID, motiveID)
	result, _ := accusation.Verify(decision, epistemic.Solution{Culprit: culpritID, Method: methodID, Motive: motiveID})
	return result
}

func TestCommitterStoresRecordFirstThenWritesOnlyBarrierRef(t *testing.T) {
	graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: turnState(
		"obj://TEST_CONTENT/turn/turn-action-1/case-decision")}}
	artifacts := &resultStore{ref: content.Ref{Instance: "TEST_CONTENT", Key: "turn/turn-action-1/accusation"}}
	committer, err := accusation.NewCommitter(graphStore, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := committer.CommitResult(t.Context(), turnEntityID, verifiedResult())
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if artifacts.puts != 1 || ref != artifacts.ref || len(graphStore.writes) != 1 {
		t.Fatalf("puts=%d ref=%v writes=%d", artifacts.puts, ref, len(graphStore.writes))
	}
	written := graphStore.writes[0]
	if len(written) != 1 || written[0].Predicate != vocabulary.TurnAccusationRef.String() {
		t.Fatalf("atomic result write = %#v", written)
	}
}

func TestCommitterNotApplicableCrashDuplicateAndMismatch(t *testing.T) {
	ref := content.Ref{Instance: "TEST_CONTENT", Key: "turn/turn-action-1/accusation"}
	graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: turnState(
		"obj://TEST_CONTENT/turn/turn-action-1/case-decision")}, mergeErr: errors.New("crash")}
	artifacts := &resultStore{ref: ref}
	committer, _ := accusation.NewCommitter(graphStore, artifacts)
	if _, err := committer.CommitNotApplicable(t.Context(), "turn-action-1", turnEntityID); err == nil {
		t.Fatal("not-applicable commit crossed a failed ref write")
	}
	if artifacts.puts != 1 || len(graphStore.writes) != 0 {
		t.Fatalf("crash ordering puts=%d writes=%d", artifacts.puts, len(graphStore.writes))
	}
	graphStore.mergeErr = nil
	graphStore.entities[turnEntityID].Triples = append(graphStore.entities[turnEntityID].Triples,
		message.Triple{Subject: turnEntityID, Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()})
	artifacts.record = &content.AccusationRecord{TurnID: "turn-action-1", Status: content.AccusationNotApplicable}
	if _, err := committer.CommitNotApplicable(t.Context(), "turn-action-1", turnEntityID); err != nil {
		t.Fatalf("duplicate not-applicable commit: %v", err)
	}
	if artifacts.puts != 1 {
		t.Fatalf("resident no-op was rewritten: puts=%d", artifacts.puts)
	}
	artifacts.record = resultRecord(verifiedResult())
	if _, err := committer.CommitNotApplicable(t.Context(), "turn-action-1", turnEntityID); err == nil {
		t.Fatal("resident result collision accepted as not-applicable")
	}
}

func TestCommitterCrashRetryDuplicateAndResidentMismatch(t *testing.T) {
	result := verifiedResult()
	ref := content.Ref{Instance: "TEST_CONTENT", Key: "turn/turn-action-1/accusation"}
	graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: turnState(
		"obj://TEST_CONTENT/turn/turn-action-1/case-decision")}, mergeErr: errors.New("crash")}
	artifacts := &resultStore{ref: ref}
	committer, _ := accusation.NewCommitter(graphStore, artifacts)
	if _, err := committer.CommitResult(t.Context(), turnEntityID, result); err == nil {
		t.Fatal("commit succeeded across failed graph write")
	}
	if artifacts.puts != 1 || len(graphStore.writes) != 0 {
		t.Fatalf("crash ordering puts=%d writes=%d", artifacts.puts, len(graphStore.writes))
	}

	graphStore.mergeErr = nil
	graphStore.entities[turnEntityID].Triples = append(graphStore.entities[turnEntityID].Triples,
		message.Triple{Subject: turnEntityID, Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()})
	artifacts.record = resultRecord(result)
	if _, err := committer.CommitResult(t.Context(), turnEntityID, result); err != nil {
		t.Fatalf("duplicate commit: %v", err)
	}
	if artifacts.puts != 1 {
		t.Fatalf("resident exact match was rewritten %d times", artifacts.puts)
	}

	mismatch := *result
	mismatch.Outcome = payload.AccusationIncorrect
	artifacts.record = resultRecord(&mismatch)
	if _, err := committer.CommitResult(t.Context(), turnEntityID, result); err == nil {
		t.Fatal("resident mismatch was accepted")
	}
}

func TestResultTriplesCarryNoDimensionOrProse(t *testing.T) {
	graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: turnState(
		"obj://TEST_CONTENT/turn/turn-action-1/case-decision")}}
	artifacts := &resultStore{ref: content.Ref{Instance: "TEST_CONTENT", Key: "turn/turn-action-1/accusation"}}
	committer, _ := accusation.NewCommitter(graphStore, artifacts,
		accusation.WithNow(func() time.Time { return time.Unix(1, 0).UTC() }))
	_, _ = committer.CommitResult(t.Context(), turnEntityID, verifiedResult())
	objects := []any{graphStore.writes[0][0].Object}
	want := []any{artifacts.ref.String()}
	if !reflect.DeepEqual(objects, want) {
		t.Fatalf("rule-visible accusation facts = %#v, want %#v", objects, want)
	}
}

func TestDenouementAuthorizerBindsTurnCommittedRefAndCorrectResultIdentity(t *testing.T) {
	result := verifiedResult()
	ref := content.Ref{Instance: "TEST_CONTENT", Key: "turn/turn-action-1/accusation"}
	state := &graph.EntityState{ID: turnEntityID, Triples: []message.Triple{
		{Subject: turnEntityID, Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()},
	}}
	graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: state}, queryIDs: []string{turnEntityID}}
	artifacts := &resultStore{ref: ref, record: resultRecord(result)}
	authorizer, err := accusation.NewDenouementAuthorizer(graphStore, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := authorizer.Authorized(t.Context(), result.TurnID, result.CaseID, ref.String())
	if err != nil || !allowed {
		t.Fatalf("Authorized = %v, %v", allowed, err)
	}
	for _, mutate := range []func(*payload.AccusationResult){
		func(r *payload.AccusationResult) { r.Outcome = payload.AccusationIncorrect },
		func(r *payload.AccusationResult) { r.CaseID = "c360.semmachina.bellweather.campaign.case.foreign" },
	} {
		candidate := *verifiedResult()
		mutate(&candidate)
		artifacts.record = resultRecord(&candidate)
		allowed, err = authorizer.Authorized(t.Context(), result.TurnID, result.CaseID, ref.String())
		if err == nil || allowed {
			t.Fatalf("foreign/incorrect result authorized: %v, %v", allowed, err)
		}
	}
}

type projectionCapture struct{ purposes []epistemic.Purpose }

func (p *projectionCapture) Project(_ context.Context, audience epistemic.AuthenticatedAudience) (*epistemic.Projection, error) {
	p.purposes = append(p.purposes, audience.Purpose())
	return &epistemic.Projection{Purpose: audience.Purpose()}, nil
}

func TestNarrationRouterUpgradesOnlyValidCorrectResult(t *testing.T) {
	ref := content.Ref{Instance: "TEST_CONTENT", Key: "turn/turn-action-1/accusation"}
	result := verifiedResult()
	for _, tc := range []struct {
		name    string
		triples []message.Triple
		stored  *content.AccusationRecord
		want    epistemic.Purpose
		wantErr bool
	}{
		{name: "missing", want: epistemic.PurposeNarrator},
		{name: "incorrect", triples: []message.Triple{
			{Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()},
		}, stored: resultRecord(func() *payload.AccusationResult {
			incorrectResult := *result
			incorrectResult.Outcome = payload.AccusationIncorrect
			return &incorrectResult
		}()),
			want: epistemic.PurposeNarrator},
		{name: "correct", triples: []message.Triple{
			{Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()},
		}, stored: resultRecord(result), want: epistemic.PurposeDenouement},
		{name: "not applicable", triples: []message.Triple{
			{Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()},
		}, stored: &content.AccusationRecord{TurnID: result.TurnID, Status: content.AccusationNotApplicable},
			want: epistemic.PurposeNarrator},
		{name: "ambiguous", triples: []message.Triple{
			{Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()},
			{Predicate: vocabulary.TurnAccusationRef.String(), Object: ref.String()},
		}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for index := range tc.triples {
				tc.triples[index].Subject = turnEntityID
			}
			state := &graph.EntityState{ID: turnEntityID, Triples: tc.triples}
			graphStore := &accusationGraph{entities: map[string]*graph.EntityState{turnEntityID: state}}
			artifacts := &resultStore{ref: ref, record: tc.stored}
			capture := &projectionCapture{}
			router, err := accusation.NewNarrationRouter(graphStore, artifacts, capture, caseID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = router.Project(t.Context(), epistemic.NarratorAudience(result.TurnID, turnEntityID))
			if tc.wantErr {
				if err == nil || len(capture.purposes) != 0 {
					t.Fatalf("malformed state: err=%v delegated=%v", err, capture.purposes)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(capture.purposes, []epistemic.Purpose{tc.want}) {
				t.Fatalf("Project: purposes=%v err=%v", capture.purposes, err)
			}
		})
	}
}
