package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type journalArtifacts struct {
	journal       *[]string
	failTestimony bool
}

func (s *journalArtifacts) PutTestimony(context.Context, string, *content.Testimony) (content.Ref, error) {
	*s.journal = append(*s.journal, "testimony")
	if s.failTestimony {
		s.failTestimony = false
		return content.Ref{}, errors.New("injected testimony failure")
	}
	return content.Ref{Instance: "TEST", Key: "revelation/test/testimony"}, nil
}
func (s *journalArtifacts) PutKnowledgeReceipt(_ context.Context, _ string, _ *content.KnowledgeReceipt) (content.Ref, error) {
	*s.journal = append(*s.journal, "aggregate-receipt")
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/knowledge"}, nil
}

type journalGraph struct {
	journal        *[]string
	entities       map[string]*graph.EntityState
	failRevelation bool
}

func (s *journalGraph) CreateEntity(_ context.Context, entity *graph.EntityState) (graphio.CreateResult, error) {
	kind := fmt.Sprint(entity.Triples[0].Object)
	*s.journal = append(*s.journal, "create-"+kind)
	if kind == string(vocabulary.EntityKindRevelation) && s.failRevelation {
		s.failRevelation = false
		return graphio.CreateResult{}, errors.New("injected revelation crash")
	}
	if _, ok := s.entities[entity.ID]; ok {
		return graphio.CreateResult{}, graphio.ErrEntityExists
	}
	cloned := *entity
	s.entities[entity.ID] = &cloned
	return graphio.CreateResult{Entity: &cloned}, nil
}
func (s *journalGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	*s.journal = append(*s.journal, "verify-existing")
	return s.entities[id], nil
}
func (s *journalGraph) MergeTriples(_ context.Context, _ string, triples []message.Triple, _ ...graphio.MergeOption) (*graph.EntityState, error) {
	*s.journal = append(*s.journal, "turn-ref-last")
	if len(triples) != 1 || triples[0].Predicate != vocabulary.TurnKnowledgeRef.String() {
		return nil, errors.New("wrong final witness")
	}
	return &graph.EntityState{}, nil
}

func newJournalGranter(t *testing.T, graphStore *journalGraph, journal *[]string) *Granter {
	t.Helper()
	granter, err := NewGranter(graphStore, &journalArtifacts{journal: journal})
	if err != nil {
		t.Fatalf("NewGranter: %v", err)
	}
	return granter
}

func TestGranter_WritesEntitiesReceiptThenTurnWitnessLast(t *testing.T) {
	journal := []string{}
	graphStore := &journalGraph{journal: &journal, entities: map[string]*graph.EntityState{}}
	granter := newJournalGranter(t, graphStore, &journal)
	if _, err := granter.Grant(t.Context(), "c360.semmachina.test.bellweather.turn.turn-act-1",
		validPreflight(), fakeShareAuthorizer{}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	want := []string{"create-knowledge", "create-revelation", "aggregate-receipt", "turn-ref-last"}
	if fmt.Sprint(journal) != fmt.Sprint(want) {
		t.Fatalf("write journal = %v, want %v", journal, want)
	}
}

func TestGranter_DuplicateAndPartialCrashConvergeByExactExistingVerification(t *testing.T) {
	journal := []string{}
	graphStore := &journalGraph{journal: &journal, entities: map[string]*graph.EntityState{}, failRevelation: true}
	granter := newJournalGranter(t, graphStore, &journal)
	turn := "c360.semmachina.test.bellweather.turn.turn-act-1"
	if _, err := granter.Grant(t.Context(), turn, validPreflight(), fakeShareAuthorizer{}); err == nil {
		t.Fatal("injected partial crash did not interrupt the commit")
	}
	journal = nil
	if _, err := granter.Grant(t.Context(), turn, validPreflight(), fakeShareAuthorizer{}); err != nil {
		t.Fatalf("retry after partial crash: %v", err)
	}
	if !strings.Contains(fmt.Sprint(journal), "verify-existing") || journal[len(journal)-1] != "turn-ref-last" {
		t.Fatalf("retry journal = %v", journal)
	}
	journal = nil
	if _, err := granter.Grant(t.Context(), turn, validPreflight(), fakeShareAuthorizer{}); err != nil {
		t.Fatalf("duplicate completed delivery: %v", err)
	}
	if journal[len(journal)-1] != "turn-ref-last" {
		t.Fatalf("duplicate did not converge through final witness: %v", journal)
	}
}

func TestGranter_ExistingSemanticCollisionIsIntegrityFailure(t *testing.T) {
	journal := []string{}
	graphStore := &journalGraph{journal: &journal, entities: map[string]*graph.EntityState{}}
	granter := newJournalGranter(t, graphStore, &journal)
	turn := "c360.semmachina.test.bellweather.turn.turn-act-1"
	if _, err := granter.Grant(t.Context(), turn, validPreflight(), fakeShareAuthorizer{}); err != nil {
		t.Fatalf("initial Grant: %v", err)
	}
	for _, state := range graphStore.entities {
		if fmt.Sprint(state.Triples[0].Object) == string(vocabulary.EntityKindKnowledge) {
			state.Triples[1].Object = testSuspect
		}
	}
	if _, err := granter.Grant(t.Context(), turn, validPreflight(), fakeShareAuthorizer{}); err == nil ||
		!strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("semantic collision = %v", err)
	}
}

func TestExactEntitySemantics_AllowsOnlyEnumeratedFrameworkIdentityFacts(t *testing.T) {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	id := "c360.semmachina.test.bellweather.knowledge.grant-1"
	want := entity(id, knowledgeEntityType, at,
		fact(id, vocabulary.WorldEntityKind, string(vocabulary.EntityKindKnowledge), at),
		fact(id, vocabulary.KnowledgeActorHolder, testActor, at),
		fact(id, vocabulary.KnowledgeEvidenceRef, testEvidence, at))
	got := want.Clone()
	got.Triples = append(got.Triples, message.Triple{
		Subject: id, Predicate: graph.PredStubMarker, Object: true,
	})
	if err := exactEntitySemantics(got, want); err != nil {
		t.Fatalf("framework-injected identity changed owned semantics: %v", err)
	}
	got = want.Clone()
	got.Triples = append(got.Triples, message.Triple{
		Subject: id, Predicate: ssvocab.EntityIndexingProfile, Object: ssvocab.IndexingProfileControl,
	})
	if err := exactEntitySemantics(got, want); err != nil {
		t.Fatalf("graph-ingest indexing-profile stamp changed owned semantics: %v", err)
	}
	for _, predicate := range []string{
		"foreign.audit.marker", "core.identity.unapproved", "provenance.audit.unapproved",
	} {
		got = want.Clone()
		got.Triples = append(got.Triples, message.Triple{
			Subject: id, Predicate: predicate, Object: "unexpected",
		})
		if err := exactEntitySemantics(got, want); err == nil {
			t.Fatalf("foreign extra predicate %s passed exact semantic verification", predicate)
		}
	}
	got = want.Clone()
	got.Triples[1].Object = testSuspect
	if err := exactEntitySemantics(got, want); err == nil {
		t.Fatal("changed owned knowledge holder passed semantic verification")
	}
}

func TestExactEntitySemantics_RejectsWrongEnvelopeTypeAndVersion(t *testing.T) {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	id := "c360.semmachina.test.bellweather.knowledge.grant-1"
	want := entity(id, knowledgeEntityType, at,
		fact(id, vocabulary.WorldEntityKind, string(vocabulary.EntityKindKnowledge), at),
		fact(id, vocabulary.KnowledgeActorHolder, testActor, at),
		fact(id, vocabulary.KnowledgeEvidenceRef, testEvidence, at))

	wrongType := want.Clone()
	wrongType.MessageType = revelationEntityType
	if err := exactEntitySemantics(wrongType, want); err == nil {
		t.Fatal("wrong message type passed exact semantic verification")
	}
	wrongVersion := want.Clone()
	wrongVersion.Version++
	if err := exactEntitySemantics(wrongVersion, want); err == nil {
		t.Fatal("wrong entity version passed exact semantic verification")
	}
}

func TestGranter_PermanentAuthorizationFailureWritesNothing(t *testing.T) {
	journal := []string{}
	graphStore := &journalGraph{journal: &journal, entities: map[string]*graph.EntityState{}}
	granter := newJournalGranter(t, graphStore, &journal)
	input := validPreflight()
	input.Decision.ActorID = testSuspect
	if _, err := granter.Grant(t.Context(), "c360.semmachina.test.bellweather.turn.turn-act-1",
		input, fakeShareAuthorizer{}); !IsReason(err, ReasonWrongActor) {
		t.Fatalf("Grant error = %v", err)
	}
	if len(journal) != 0 || len(graphStore.entities) != 0 {
		t.Fatalf("authorization refusal wrote state: journal=%v entities=%d", journal, len(graphStore.entities))
	}
}

func TestGranter_QuestionWritesTestimonyFirstAndTestimonyFailureIsRetryable(t *testing.T) {
	journal := []string{}
	graphStore := &journalGraph{journal: &journal, entities: map[string]*graph.EntityState{}}
	artifacts := &journalArtifacts{journal: &journal, failTestimony: true}
	granter, err := NewGranter(graphStore, artifacts)
	if err != nil {
		t.Fatalf("NewGranter: %v", err)
	}
	input := validPreflight()
	input.Decision.Kind = "question"
	input.Decision.TargetRefs = []string{testSuspect}
	input.Beliefs[beliefKey{ActorID: testSuspect, EvidenceID: testEvidence}] = AuthoredBelief{
		ID: "c360.semmachina.test.bellweather.belief.judith-wire", HolderID: testSuspect,
		EvidenceID: testEvidence, Stance: vocabulary.BeliefDenies, Prose: "I never touched that wire.",
	}
	turn := "c360.semmachina.test.bellweather.turn.turn-act-1"
	if _, err := granter.Grant(t.Context(), turn, input, fakeShareAuthorizer{}); err == nil {
		t.Fatal("testimony storage failure was treated as committed")
	}
	if fmt.Sprint(journal) != "[testimony]" || len(graphStore.entities) != 0 {
		t.Fatalf("testimony failure wrote beyond the first object: journal=%v entities=%d", journal, len(graphStore.entities))
	}
	journal = nil
	if _, err := granter.Grant(t.Context(), turn, input, fakeShareAuthorizer{}); err != nil {
		t.Fatalf("retry testimony commit: %v", err)
	}
	want := []string{"testimony", "create-knowledge", "create-revelation", "aggregate-receipt", "turn-ref-last"}
	if fmt.Sprint(journal) != fmt.Sprint(want) {
		t.Fatalf("question write journal = %v, want %v", journal, want)
	}
}
