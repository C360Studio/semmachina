package companion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

const (
	playerID    = "c360.semmachina.world1.starter.player.p1"
	actorID     = "c360.semmachina.world1.starter.character.rook"
	companionID = "c360.semmachina.world1.starter.character.wren"
	locationID  = "c360.semmachina.world1.starter.scene.gatehouse"
	evidenceID  = "c360.semmachina.world1.starter.evidence.scrap"
)

type authorityGraph struct {
	mu     sync.Mutex
	states map[string]*graph.EntityState
}

func (g *authorityGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.states[id]
	if state == nil {
		return nil, graphio.ErrEntityNotFound
	}
	return state.Clone(), nil
}
func (g *authorityGraph) EntitiesByPredicateValue(_ context.Context, predicate, value string, limit int) ([]string, error) {
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
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}
func (g *authorityGraph) MergeTriples(_ context.Context, id string, triples []message.Triple, _ ...graphio.MergeOption) (*graph.EntityState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.states[id]
	if state == nil {
		return nil, graphio.ErrEntityNotFound
	}
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

func state(id string, kind vocabulary.EntityKind, facts ...message.Triple) *graph.EntityState {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	triples := []message.Triple{{Subject: id, Predicate: vocabulary.WorldEntityKind.String(), Object: string(kind),
		Source: payload.WorldImportSource, Context: "starter@1.0.0", Timestamp: at}}
	triples = append(triples, facts...)
	return &graph.EntityState{ID: id, MessageType: message.Type{Domain: "semmachina", Category: "world_entity", Version: "v1"}, Version: 1, UpdatedAt: at, Triples: triples}
}
func fact(subject string, predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{Subject: subject, Predicate: predicate.String(), Object: object,
		Source: payload.WorldImportSource, Context: "starter@1.0.0",
		Timestamp: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
}

func validAuthority(t *testing.T) (*Authority, *authorityGraph, string) {
	t.Helper()
	bondID, err := world.CompanionBondID("c360", "world1", "starter", playerID, companionID)
	if err != nil {
		t.Fatal(err)
	}
	g := &authorityGraph{states: map[string]*graph.EntityState{}}
	g.states[playerID] = state(playerID, vocabulary.EntityKindPlayer, fact(playerID, vocabulary.PlayerCharacterCurrent, actorID))
	g.states[actorID] = state(actorID, vocabulary.EntityKindCharacter, fact(actorID, vocabulary.WorldLocationCurrent, locationID))
	g.states[companionID] = state(companionID, vocabulary.EntityKindCharacter,
		fact(companionID, vocabulary.CompanionCandidatePolicy, string(vocabulary.CompanionPolicyBoundedInitiative)),
		fact(companionID, vocabulary.WorldLocationCurrent, locationID))
	g.states[bondID] = state(bondID, vocabulary.EntityKindCompanionBond,
		fact(bondID, vocabulary.CompanionBondPlayer, playerID),
		fact(bondID, vocabulary.CompanionBondCharacter, companionID),
		fact(bondID, vocabulary.CompanionBondPolicy, string(vocabulary.CompanionPolicyReactive)),
		fact(bondID, vocabulary.CompanionBondHintLevel, string(vocabulary.HintLevelNudge)))
	authority, err := NewAuthority(g)
	if err != nil {
		t.Fatal(err)
	}
	return authority, g, bondID
}

func TestAuthority_ValidatesBondAndAuthorizesShareFromControlledCharacter(t *testing.T) {
	authority, _, bondID := validAuthority(t)
	bond, err := authority.ValidateBond(t.Context(), bondID, playerID, companionID)
	if err != nil {
		t.Fatalf("ValidateBond: %v", err)
	}
	if bond.Policy != vocabulary.CompanionPolicyReactive || bond.HintLevel != vocabulary.HintLevelNudge {
		t.Fatalf("bond = %+v", bond)
	}
	allowed, err := authority.Authorized(t.Context(), actorID, companionID, evidenceID)
	if err != nil || !allowed {
		t.Fatalf("share allowed=%v err=%v", allowed, err)
	}
	allowed, err = authority.Authorized(t.Context(), actorID, actorID, evidenceID)
	if err != nil || allowed {
		t.Fatalf("unbonded share allowed=%v err=%v", allowed, err)
	}
}

func TestAuthority_WitnessRequiresBondAndStructuralCoPresence(t *testing.T) {
	authority, graph, _ := validAuthority(t)
	witness, allowed, err := authority.Witness(t.Context(), actorID, []string{locationID})
	if err != nil || !allowed || witness != companionID {
		t.Fatalf("Witness = %s,%v,%v", witness, allowed, err)
	}
	graph.states[companionID].Triples[2].Object = "c360.semmachina.world1.starter.scene.road"
	if _, allowed, err := authority.Witness(t.Context(), actorID, []string{locationID}); err != nil || allowed {
		t.Fatalf("remote companion witnessed: allowed=%v err=%v", allowed, err)
	}
}

func TestAuthority_RejectsAmbiguousOrCorruptBondsAsIntegrityFailures(t *testing.T) {
	authority, graph, bondID := validAuthority(t)
	duplicate := graph.states[bondID].Clone()
	duplicate.ID = "c360.semmachina.world1.starter.companion-bond.other"
	for index := range duplicate.Triples {
		duplicate.Triples[index].Subject = duplicate.ID
	}
	graph.states[duplicate.ID] = duplicate
	if _, err := authority.ActiveBondForPlayer(t.Context(), playerID); !errors.Is(err, ErrBondIntegrity) {
		t.Fatalf("ambiguous bonds error = %v", err)
	}
	delete(graph.states, duplicate.ID)
	graph.states[bondID].Triples[3].Object = string(vocabulary.CompanionPolicyBoundedInitiative)
	graph.states[companionID].Triples[1].Object = string(vocabulary.CompanionPolicyReactive)
	if _, err := authority.ValidateBond(t.Context(), bondID, playerID, companionID); !errors.Is(err, ErrBondIntegrity) {
		t.Fatalf("widened policy error = %v", err)
	}
}

func TestAuthority_RejectsFabricatedEntityStateWithoutImportProvenance(t *testing.T) {
	authority, graph, bondID := validAuthority(t)
	graph.states[bondID].MessageType = message.Type{Domain: payload.Domain, Category: "fabricated", Version: payload.SchemaVersion}
	if _, err := authority.ValidateBond(t.Context(), bondID, playerID, companionID); !errors.Is(err, ErrBondIntegrity) {
		t.Fatalf("forged message type error = %v", err)
	}
	graph.states[bondID].MessageType = (&payload.WorldEntity{}).Schema()
	graph.states[bondID].Triples[1].Source = "direct-entity-state-write"
	if _, err := authority.ValidateBond(t.Context(), bondID, playerID, companionID); !errors.Is(err, ErrBondIntegrity) {
		t.Fatalf("forged structural provenance error = %v", err)
	}
}

func TestAuthority_BondTransactionSerializesTwoTurnsAcrossTheWholeLadderCommit(t *testing.T) {
	authority, _, bondID := validAuthority(t)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	levels := make(chan vocabulary.HintLevel, 2)
	errs := make(chan error, 2)

	go func() {
		err := authority.WithBondTransaction(t.Context(), bondID, playerID, companionID,
			func(transaction *LadderTransaction) error {
				bond := transaction.Bond()
				levels <- bond.HintLevel
				close(firstEntered)
				<-releaseFirst
				_, err := transaction.AdvanceHint(t.Context(), bond.HintLevel)
				return err
			})
		errs <- err
	}()
	<-firstEntered
	go func() {
		close(secondStarted)
		err := authority.WithBondTransaction(t.Context(), bondID, playerID, companionID,
			func(transaction *LadderTransaction) error {
				bond := transaction.Bond()
				levels <- bond.HintLevel
				_, err := transaction.AdvanceHint(t.Context(), bond.HintLevel)
				return err
			})
		errs <- err
	}()
	<-secondStarted
	close(releaseFirst)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	first, second := <-levels, <-levels
	if first != vocabulary.HintLevelNudge || second != vocabulary.HintLevelConnect {
		t.Fatalf("serialized emitted levels = %s then %s, want nudge then connect", first, second)
	}
	bond, err := authority.ValidateBond(t.Context(), bondID, playerID, companionID)
	if err != nil || bond.HintLevel != vocabulary.HintLevelNextStep {
		t.Fatalf("final bond = %+v err=%v", bond, err)
	}
}

func TestAuthority_ResetSharesTheBondTransactionBoundary(t *testing.T) {
	authority, _, bondID := validAuthority(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	transactionDone := make(chan error, 1)
	resetStarted := make(chan struct{})
	resetDone := make(chan error, 1)

	go func() {
		transactionDone <- authority.WithBondTransaction(t.Context(), bondID, playerID, companionID,
			func(transaction *LadderTransaction) error {
				bond := transaction.Bond()
				close(entered)
				<-release
				_, err := transaction.AdvanceHint(t.Context(), bond.HintLevel)
				return err
			})
	}()
	<-entered
	go func() {
		close(resetStarted)
		_, err := authority.ResetHint(t.Context(), bondID)
		resetDone <- err
	}()
	<-resetStarted
	close(release)
	if err := <-transactionDone; err != nil {
		t.Fatal(err)
	}
	if err := <-resetDone; err != nil {
		t.Fatal(err)
	}
	bond, err := authority.ValidateBond(t.Context(), bondID, playerID, companionID)
	if err != nil {
		t.Fatal(err)
	}
	if bond.HintLevel != vocabulary.HintLevelNudge {
		t.Fatal(fmt.Sprintf("reset interleaving left hint level %s, want nudge", bond.HintLevel))
	}
}
