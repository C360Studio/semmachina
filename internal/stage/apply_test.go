package stage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	semerrs "github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	applyCharacterID = "acme.semmachina.keep.starter.character.rook"
	applyItemID      = "acme.semmachina.keep.starter.item.lantern"
)

type retryingEffectStore struct {
	entities          map[string]*graph.EntityState
	failNext          map[string]error
	failAfterMutation map[string]error
	writes            map[string]int
}

func (s *retryingEffectStore) GetEntity(_ context.Context, entityID string) (*graph.EntityState, error) {
	state, ok := s.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	return state, nil
}

func (s *retryingEffectStore) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	opts ...graphio.MergeOption,
) (*graph.EntityState, error) {
	s.writes[entityID]++
	if err := s.failNext[entityID]; err != nil {
		delete(s.failNext, entityID)
		return nil, err
	}

	state := s.entities[entityID]
	request := graph.UpdateEntityWithTriplesRequest{
		Entity:     &graph.EntityState{ID: entityID},
		AddTriples: triples,
	}
	for _, opt := range opts {
		opt(&request)
	}
	replaced := make(map[string]bool, len(request.AddTriples)+len(request.RemoveTriples))
	for _, triple := range request.AddTriples {
		replaced[triple.Predicate] = true
	}
	for _, predicate := range request.RemoveTriples {
		replaced[predicate] = true
	}
	kept := state.Triples[:0]
	for _, triple := range state.Triples {
		if !replaced[triple.Predicate] {
			kept = append(kept, triple)
		}
	}
	state.Triples = append(kept, request.AddTriples...)
	if err := s.failAfterMutation[entityID]; err != nil {
		delete(s.failAfterMutation, entityID)
		return nil, err
	}
	return state, nil
}

type effectBatchStore struct {
	ref     content.Ref
	batches []*payload.EffectBatch
}

func (s *effectBatchStore) PutEffectBatch(
	_ context.Context, _ string, batch *payload.EffectBatch,
) (content.Ref, error) {
	batchCopy := *batch
	batchCopy.Intents = append([]payload.EffectIntent(nil), batch.Intents...)
	s.batches = append(s.batches, &batchCopy)
	return s.ref, nil
}

type recordingDetailStore struct {
	puts   int
	detail *content.FailureDetail
}

func (s *recordingDetailStore) PutFailureDetail(
	_ context.Context, _ string, detail *content.FailureDetail,
) (content.Ref, error) {
	s.puts++
	detailCopy := *detail
	detailCopy.Committed = append([]string(nil), detail.Committed...)
	s.detail = &detailCopy
	return content.Ref{}, nil
}

type recordingTurnFailer struct {
	calls  int
	reason vocabulary.FailureReason
}

func (f *recordingTurnFailer) Fail(
	_ context.Context,
	_, _ string,
	reason vocabulary.FailureReason,
	_ content.Ref,
) (turn.Transition, error) {
	f.calls++
	f.reason = reason
	return turn.Transition{Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced}, nil
}

// A transient CommitError is an interrupted APPLYING stage, not a fictional
// rejection. Returning it keeps the durable delivery live; a permanent
// classified refusal instead ends the turn once with the closed commit code.
func TestEffectorStage_ClassifiesPartialCommitAndConvergesTransientFailures(t *testing.T) {
	cases := []struct {
		name              string
		failAfterMutation bool
		firstItemValue    int
		permanent         bool
	}{
		{name: "error before mutation response", firstItemValue: 1},
		{name: "error after mutation but before response", failAfterMutation: true, firstItemValue: 0},
		{name: "permanent refusal", firstItemValue: 1, permanent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdictRef := content.Ref{
				Instance: "SEMMACHINA_CONTENT",
				Key:      "turn/" + testTurnID + "/verdict",
			}
			batchRef := content.Ref{
				Instance: "SEMMACHINA_CONTENT",
				Key:      "turn/" + testTurnID + "/effects-ref",
			}
			fact := func(subject string, predicate vocabulary.Predicate, object any) message.Triple {
				return message.Triple{Subject: subject, Predicate: predicate.String(), Object: object, Source: "test"}
			}
			world := &retryingEffectStore{
				entities: map[string]*graph.EntityState{
					testTurnEntityID: {
						ID:          testTurnEntityID,
						MessageType: turn.EntityMessageType,
						Triples: []message.Triple{
							fact(testTurnEntityID, vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseApplying)),
							fact(testTurnEntityID, vocabulary.TurnVerdictRef, verdictRef.String()),
							fact(testTurnEntityID, vocabulary.TurnVerdictRequiresRoll, false),
						},
					},
					applyCharacterID: {
						ID:          applyCharacterID,
						MessageType: message.Type{Domain: "semmachina", Category: "world_entity", Version: "v1"},
						Triples: []message.Triple{
							fact(applyCharacterID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
							fact(applyCharacterID, vocabulary.CharacterAttributeHealth, 8),
						},
					},
					applyItemID: {
						ID:          applyItemID,
						MessageType: message.Type{Domain: "semmachina", Category: "world_entity", Version: "v1"},
						Triples: []message.Triple{
							fact(applyItemID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindItem)),
							fact(applyItemID, vocabulary.ItemAttributeQuantity, 1),
						},
					},
				},
				failNext:          make(map[string]error),
				failAfterMutation: make(map[string]error),
				writes:            make(map[string]int),
			}
			mergeErr := semerrs.WrapTransient(errors.New("merge refusal"), "test", "MergeTriples", "commit target")
			if tc.permanent {
				mergeErr = semerrs.WrapInvalid(errors.New("contract refusal"), "test", "MergeTriples", "commit target")
			}
			if tc.failAfterMutation {
				world.failAfterMutation[applyItemID] = mergeErr
			} else {
				world.failNext[applyItemID] = mergeErr
			}
			health, quantity := 4, 0
			verdict := &payload.Verdict{
				TurnID:   testTurnID,
				ActionID: testActionID,
				SceneID:  testSceneID,
				Scalars: payload.VerdictScalars{
					Plausibility: vocabulary.PlausibilityPlausible,
					Risk:         vocabulary.RiskNone,
					Consequence:  vocabulary.ConsequenceNone,
					RequiresRoll: false,
				},
				Bands: payload.EffectBands{
					vocabulary.BandAuto: {
						{Type: vocabulary.EffectSetAttribute, Target: applyCharacterID,
							Attribute: vocabulary.AttributeHealth, Value: &health},
						{Type: vocabulary.EffectSetAttribute, Target: applyItemID,
							Attribute: vocabulary.AttributeQuantity, Value: &quantity},
					},
				},
				Rationale: "two durable writes",
			}
			if err := verdict.Validate(); err != nil {
				t.Fatalf("test verdict is invalid: %v", err)
			}

			recorder := &fakeRecorder{
				journal: &journal{},
				transition: turn.Transition{
					Previous: vocabulary.PhaseAdjudicating,
					Phase:    vocabulary.PhaseApplying,
					Outcome:  turn.OutcomeAdvanced,
				},
			}
			failer := &recordingTurnFailer{}
			batches := &effectBatchStore{ref: batchRef}
			details := &recordingDetailStore{}
			applier, err := effect.NewApplier(world)
			if err != nil {
				t.Fatalf("NewApplier: %v", err)
			}
			effector, err := stage.NewEffector(
				recorder,
				failer,
				world,
				&fakeVerdicts{verdict: verdict},
				batches,
				details,
				applier,
			)
			if err != nil {
				t.Fatalf("NewEffector: %v", err)
			}

			err = effector.Run(t.Context(), testTrigger())
			if tc.permanent {
				if err != nil {
					t.Fatalf("permanent Run returned %v after recording its terminal outcome", err)
				}
				if failer.calls != 1 || failer.reason != vocabulary.FailureEffectCommitIncomplete {
					t.Fatalf("permanent commit failure calls=%d reason=%q, want one %q failure",
						failer.calls, failer.reason, vocabulary.FailureEffectCommitIncomplete)
				}
				if details.puts != 1 || details.detail == nil || details.detail.Target != applyItemID ||
					len(details.detail.Committed) != 1 || details.detail.Committed[0] != applyCharacterID {
					t.Fatalf("permanent commit detail = %+v after %d puts, want target %s after confirmed [%s]",
						details.detail, details.puts, applyItemID, applyCharacterID)
				}
				return
			}
			var incomplete *effect.CommitError
			if !errors.As(err, &incomplete) {
				t.Fatalf("first Run error = %v, want a retryable *effect.CommitError", err)
			}
			if incomplete.Target != applyItemID || len(incomplete.Committed) != 1 ||
				incomplete.Committed[0] != applyCharacterID {
				t.Fatalf("CommitError target=%q committed=%v, want the unconfirmed-response target %q after confirmed [%s]",
					incomplete.Target, incomplete.Committed, applyItemID, applyCharacterID)
			}
			if failer.calls != 0 || details.puts != 0 {
				t.Fatalf("partial commit terminally failed the turn: fail calls=%d detail puts=%d",
					failer.calls, details.puts)
			}
			if got := predicateObjects(world.entities[applyCharacterID], vocabulary.CharacterAttributeHealth); len(got) != 1 || got[0] != 4 {
				t.Fatalf("first target after partial commit = %v, want [4]", got)
			}
			if got := predicateObjects(world.entities[applyItemID], vocabulary.ItemAttributeQuantity); len(got) != 1 || got[0] != tc.firstItemValue {
				t.Fatalf("target whose response failed holds %v, want [%d] for this failure point", got, tc.firstItemValue)
			}

			recorder.transition = turn.Transition{
				Previous: vocabulary.PhaseApplying,
				Phase:    vocabulary.PhaseApplying,
				Outcome:  turn.OutcomeResumed,
			}
			if err := effector.Run(t.Context(), testTrigger()); err != nil {
				t.Fatalf("retry Run: %v", err)
			}
			if failer.calls != 0 || details.puts != 0 {
				t.Fatalf("converged retry terminally failed the turn: fail calls=%d detail puts=%d",
					failer.calls, details.puts)
			}
			if len(batches.batches) != 2 || batches.batches[0].BatchID != batches.batches[1].BatchID {
				t.Fatalf("retry stored different logical batches: %+v", batches.batches)
			}
			if got := predicateObjects(world.entities[applyCharacterID], vocabulary.CharacterAttributeHealth); len(got) != 1 || got[0] != 4 {
				t.Fatalf("retried first target accumulated %v, want exactly [4]", got)
			}
			if got := predicateObjects(world.entities[applyItemID], vocabulary.ItemAttributeQuantity); len(got) != 1 || got[0] != 0 {
				t.Fatalf("retried failed target = %v, want exactly [0]", got)
			}
			if got := predicateObjects(world.entities[testTurnEntityID], vocabulary.TurnEffectsBatch); len(got) != 1 || got[0] != batches.batches[0].BatchID {
				t.Fatalf("turn batch marker = %v, want one marker for %q", got, batches.batches[0].BatchID)
			}
			if world.writes[applyCharacterID] != 2 {
				t.Fatalf("retry did not re-apply the already committed target: writes=%d, want 2 convergent replacements",
					world.writes[applyCharacterID])
			}
		})
	}
}

func predicateObjects(state *graph.EntityState, predicate vocabulary.Predicate) []any {
	var objects []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			objects = append(objects, triple.Object)
		}
	}
	return objects
}
