package projectioncontract_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/projectioncontract"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestContractsValidateAsACompleteRegisteredSet(t *testing.T) {
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatal(err)
	}
	if err := projection.ValidateContracts(projectioncontract.Contracts()); err != nil {
		t.Fatalf("projection contracts: %v", err)
	}
}

func TestEffectTargetGroupsPartitionTheClosedWritableUnionByMutableFamily(t *testing.T) {
	want := vocabulary.EffectWritablePredicateStrings()
	var got []string
	wantTargets := []projectioncontract.Target{
		projectioncontract.EffectTargetAttributes,
		projectioncontract.EffectTargetStatus,
		projectioncontract.EffectTargetLocation,
		projectioncontract.EffectTargetRelationships,
	}
	for _, contract := range projectioncontract.Contracts() {
		if contract.Name != projectioncontract.EffectTargetContract {
			continue
		}
		if len(contract.Groups) != len(wantTargets) {
			t.Fatalf("effect contract groups = %+v", contract.Groups)
		}
		for index, group := range contract.Groups {
			if group.Name != wantTargets[index].Group || group.Mode != projection.ModeReconcile {
				t.Fatalf("effect group[%d] = %+v, want reconcile target %+v", index, group, wantTargets[index])
			}
			for _, predicate := range group.Predicates {
				target, ok := projectioncontract.EffectTargetForPredicate(vocabulary.Predicate(predicate))
				if !ok || target != wantTargets[index] {
					t.Fatalf("predicate %q resolves to %+v, %t; want %+v", predicate, target, ok, wantTargets[index])
				}
			}
			got = append(got, group.Predicates...)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("effect group union = %v, want %v", got, want)
	}
}

func TestOverlappingCompanionGroupsAreSeparateContracts(t *testing.T) {
	targets := []projectioncontract.Target{
		projectioncontract.TurnCompanionTrigger,
		projectioncontract.TurnCompanionResult,
	}
	if targets[0].Contract == targets[1].Contract {
		t.Fatal("overlapping companion groups share a contract; validation must reject overlapping predicates")
	}
}

func TestKnowledgeBirthContractsDeclareEveryFactWrittenAtCreation(t *testing.T) {
	tests := []struct {
		name     string
		contract string
		want     []string
	}{
		{
			name:     "knowledge",
			contract: projectioncontract.KnowledgeBirthContract,
			want: []string{
				vocabulary.WorldEntityKind.String(),
				vocabulary.KnowledgeActorHolder.String(),
				vocabulary.KnowledgeEvidenceRef.String(),
			},
		},
		{
			name:     "revelation",
			contract: projectioncontract.RevelationBirthContract,
			want: []string{
				vocabulary.WorldEntityKind.String(),
				vocabulary.RevelationEvidenceRef.String(),
				vocabulary.RevelationActorHolder.String(),
				vocabulary.RevelationTurnID.String(),
				vocabulary.RevelationSourceActor.String(),
				vocabulary.RevelationTestimonyRef.String(),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectioncontract.BirthPredicates(tc.contract); !slices.Equal(got, tc.want) {
				t.Fatalf("birth predicates = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCanonicalMutationClientAcceptsKnowledgeBirthFacts(t *testing.T) {
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatal(err)
	}
	nc, err := natsclient.NewClient("nats://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client, err := projection.NewMutationClient(projection.MutationClientConfig{
		NATS: nc, Contracts: projectioncontract.Contracts(), Timeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMutationClient: %v", err)
	}
	at := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		contract   string
		id         string
		category   string
		kind       vocabulary.EntityKind
		predicates []vocabulary.Predicate
	}{
		{
			name: "knowledge", contract: projectioncontract.KnowledgeBirthContract,
			id: "c360.semmachina.test.bellweather.knowledge.grant-1", category: "knowledge_grant_entity",
			kind:       vocabulary.EntityKindKnowledge,
			predicates: []vocabulary.Predicate{vocabulary.KnowledgeActorHolder, vocabulary.KnowledgeEvidenceRef},
		},
		{
			name: "revelation", contract: projectioncontract.RevelationBirthContract,
			id: "c360.semmachina.test.bellweather.revelation.receipt-1", category: "revelation_receipt_entity",
			kind: vocabulary.EntityKindRevelation,
			predicates: []vocabulary.Predicate{
				vocabulary.RevelationEvidenceRef, vocabulary.RevelationActorHolder, vocabulary.RevelationTurnID,
				vocabulary.RevelationSourceActor, vocabulary.RevelationTestimonyRef,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			triples := []message.Triple{{Subject: tc.id, Predicate: vocabulary.WorldEntityKind.String(), Object: string(tc.kind)}}
			for _, predicate := range tc.predicates {
				triples = append(triples, message.Triple{Subject: tc.id, Predicate: predicate.String(), Object: "test-value"})
			}
			_, createErr := client.Create(context.Background(), projection.CreateMutation{
				Contract: tc.contract,
				Entity: &graph.EntityState{
					ID: tc.id, MessageType: message.Type{Domain: payload.Domain, Category: tc.category, Version: payload.SchemaVersion},
					Version: 1, UpdatedAt: at,
				},
				Triples: triples,
				Metadata: projection.MutationMetadata{
					RequestID: "birth-contract-proof", Source: "projection-contract-test", Timestamp: at,
				},
			})
			var mutationErr *projection.MutationError
			if !errors.As(createErr, &mutationErr) {
				t.Fatalf("Create reached disconnected transport with error %T, want MutationError: %v", createErr, createErr)
			}
			if mutationErr.Kind == projection.MutationInvalid {
				t.Fatalf("canonical mutation client rejected declared birth facts: %v", mutationErr)
			}
		})
	}
}
