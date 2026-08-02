package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type fakeHintProofObserver struct {
	state *graph.EntityState
	err   error
	gotID string
}

func (o *fakeHintProofObserver) turnState(_ context.Context, id string) (*graph.EntityState, error) {
	o.gotID = id
	return o.state, o.err
}

func TestHintTriggerProofRequiresTheExactPersistedPlayerHintRoute(t *testing.T) {
	valid := func() *graph.EntityState {
		return &graph.EntityState{ID: "c360.semmachina.smoke.bellweather-maze.turn.turn-act-hint", Triples: []message.Triple{
			{Predicate: vocabulary.TurnCaseDecisionKind.String(), Object: string(payload.CaseDecisionRequestHint)},
			{Predicate: vocabulary.TurnCompanionTriggerKind.String(), Object: string(vocabulary.CompanionTriggerPlayerHint)},
			{Predicate: vocabulary.TurnCompanionTriggerSource.String(), Object: string(vocabulary.CompanionTriggerSourceCaseDecision)},
		}}
	}

	if err := requireHintTriggerProof(valid()); err != nil {
		t.Fatalf("valid persisted player-hint route: %v", err)
	}
	observer := &fakeHintProofObserver{state: valid()}
	if err := proveHintTrigger(t.Context(), observer, "hint-turn-entity"); err != nil {
		t.Fatalf("authoritative hint proof: %v", err)
	}
	if observer.gotID != "hint-turn-entity" {
		t.Fatalf("authoritative observer read %q, want hint-turn-entity", observer.gotID)
	}
	observer.err = errors.New("graph unavailable")
	if err := proveHintTrigger(t.Context(), observer, "hint-turn-entity"); err == nil ||
		!strings.Contains(err.Error(), "graph unavailable") {
		t.Fatalf("authoritative read error = %v", err)
	}

	tests := map[string]struct {
		mutate  func(*graph.EntityState)
		wantErr string
	}{
		"warning is not a requested hint": {
			mutate: func(state *graph.EntityState) {
				state.Triples[1].Object = string(vocabulary.CompanionTriggerWarning)
			},
			wantErr: "player-hint",
		},
		"wrong decision kind": {
			mutate: func(state *graph.EntityState) {
				state.Triples[0].Object = string(payload.CaseDecisionObserve)
			},
			wantErr: "request_hint",
		},
		"wrong trigger source": {
			mutate: func(state *graph.EntityState) {
				state.Triples[2].Object = string(vocabulary.CompanionTriggerSourceResolvedRisk)
			},
			wantErr: "case-decision",
		},
		"missing persisted scalar": {
			mutate:  func(state *graph.EntityState) { state.Triples = state.Triples[:2] },
			wantErr: "exactly one",
		},
		"duplicate persisted scalar": {
			mutate: func(state *graph.EntityState) {
				state.Triples = append(state.Triples, state.Triples[1])
			},
			wantErr: "exactly one",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			state := valid()
			tc.mutate(state)
			err := requireHintTriggerProof(state)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("proof error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
