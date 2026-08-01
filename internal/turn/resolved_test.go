package turn_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The retrieval surface's whole durable index, from the writing side. The
// reading side lives in internal/egress; these tests prove the fact is there,
// singular, pointing at the turn that actually ended, and that writing it never
// moves the admission gate's pointer.

func resolvedPointers(t *testing.T, store *fakeStore, playerID string) []any {
	t.Helper()
	stored, err := store.GetEntity(context.Background(), playerID)
	if err != nil {
		t.Fatalf("read the player entity: %v", err)
	}
	return objectsFor(stored, vocabulary.PlayerTurnResolved)
}

// resolvedTurn drives one accepted turn all the way to a terminal phase through
// the production transitions, so the pointer under test is written by the same
// path a real turn takes.
func resolvedTurn(t *testing.T, store *fakeStore) *turn.Recorder {
	t.Helper()
	recorder := newRecorder(t, store)
	if _, err := recorder.Accept(context.Background(), testAction()); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseInterpreting,
		vocabulary.PhaseAdjudicating,
		vocabulary.PhaseResolving,
		vocabulary.PhaseApplying,
		vocabulary.PhaseCompanion,
		vocabulary.PhaseNarrating,
		vocabulary.PhaseComplete,
	} {
		if _, err := recorder.Advance(context.Background(), testTurnID, testTurnEntityID, phase); err != nil {
			t.Fatalf("Advance to %s: %v", phase, err)
		}
	}
	return recorder
}

func TestAdvance_ToCompletePointsThePlayerAtTheTurnThatJustResolved(t *testing.T) {
	store := newFakeStore()
	resolvedTurn(t, store)

	pointers := resolvedPointers(t, store, testPlayerID)
	if len(pointers) != 1 {
		t.Fatalf("the player holds %d resolved-turn pointers %v, want exactly one; a retrieval that had to "+
			"choose among them would be answering a coin flip about what happened last", len(pointers), pointers)
	}
	if pointers[0] != testTurnEntityID {
		t.Fatalf("the resolved pointer names %v, want the turn that just ended (%s)", pointers[0], testTurnEntityID)
	}
}

func TestFail_PointsThePlayerAtTheTurnThatJustFailed(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)
	if _, err := recorder.Accept(context.Background(), testAction()); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	transition, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{})
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if transition.Outcome != turn.OutcomeAdvanced {
		t.Fatalf("Fail reported %q, want the turn to have ended", transition.Outcome)
	}

	pointers := resolvedPointers(t, store, testPlayerID)
	if len(pointers) != 1 || pointers[0] != testTurnEntityID {
		t.Fatalf("a failed turn left the player pointing at %v; the player who most needs an answer is the "+
			"one whose turn failed, and a surface keyed on success cannot give them one", pointers)
	}
}

// The negative control that makes the positive ones mean something: an ordinary
// mid-turn hop writes NO resolved pointer. Without this, a recorder that wrote
// the pointer on every transition would pass every test above while telling a
// player their live turn had already ended.
func TestAdvance_ANonTerminalHopWritesNoResolvedPointer(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)
	if _, err := recorder.Accept(context.Background(), testAction()); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseInterpreting,
		vocabulary.PhaseAdjudicating,
		vocabulary.PhaseResolving,
		vocabulary.PhaseApplying,
		vocabulary.PhaseCompanion,
		vocabulary.PhaseNarrating,
	} {
		if _, err := recorder.Advance(context.Background(), testTurnID, testTurnEntityID, phase); err != nil {
			t.Fatalf("Advance to %s: %v", phase, err)
		}
		if pointers := resolvedPointers(t, store, testPlayerID); len(pointers) != 0 {
			t.Fatalf("entering %s left the player pointing at a resolved turn %v; the turn is still running",
				phase, pointers)
		}
	}
}

// The two pointers answer different questions and must not disturb each other,
// and the case that proves it is the one where they DISAGREE: an older turn
// ending while a newer one runs.
//
// If the resolution-time write touched player.turn.current at all, it would drag
// the admission gate's pointer back onto a turn that has ended — the gate would
// then read a terminal turn, admit a third action, and the documented
// one-extra-turn window would become a cascade, one per turn that ends late. A
// single-turn version of this test cannot see that: there, both pointers name the
// same turn and a write to either looks correct.
func TestAdvance_ResolvingAnOlderTurnDoesNotDragTheAdmissionGateBackwards(t *testing.T) {
	const (
		secondActionID = "act-2"
		secondTurnID   = "turn-act-2"
		secondEntityID = "c360.semmachina.world1.starter.turn.turn-act-2"
	)

	// The state is reached by the path pointPlayerAtTurn documents: the first
	// turn's pointer write fails, so the gate sees no live turn, admits a second
	// action, and the newer turn becomes the one the gate is watching while the
	// older one is still running.
	store := &playerMergeFailingStore{fakeStore: newFakeStore(), fail: true}
	recorder := newRecorder(t, store)
	if _, err := recorder.Accept(context.Background(), testAction()); err == nil {
		t.Fatal("the first accept's pointer write was supposed to fail")
	}

	store.fail = false
	second := testAction()
	second.ActionID = secondActionID
	if _, err := recorder.Accept(context.Background(), second); err != nil {
		t.Fatalf("Accept the second action: %v", err)
	}
	if pointers := playerPointers(t, store.fakeStore); len(pointers) != 1 || pointers[0] != secondEntityID {
		t.Fatalf("player.turn.current holds %v, want the newer live turn (%s)", pointers, secondEntityID)
	}

	// The OLDER turn — still running, and not the one the gate is watching —
	// ends first.
	if _, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{},
	); err != nil {
		t.Fatalf("Fail the older turn: %v", err)
	}

	pointers := playerPointers(t, store.fakeStore)
	if len(pointers) != 1 || pointers[0] != secondEntityID {
		t.Fatalf("player.turn.current holds %v after an OLDER turn resolved, want the live turn (%s); the "+
			"admission gate reading a turn that has ended would admit an action while one is still running",
			pointers, secondEntityID)
	}
	resolved := resolvedPointers(t, store.fakeStore, testPlayerID)
	if len(resolved) != 1 || resolved[0] != testTurnEntityID {
		t.Fatalf("the resolved pointer holds %v, want the turn that just ended (%s)", resolved, testTurnEntityID)
	}
}

// The convergence property. The phase is written first and the pointer second,
// so a transport failure in the gap leaves the phase terminal and the pointer
// stale — and the redelivery that follows finds the transition already made.
// Nothing would ever write the pointer if the declined path did not repair it.
func TestTransition_ARedeliveryRepairsAResolvedPointerTheFirstAttemptLost(t *testing.T) {
	// Accept writes the CURRENT pointer onto the same entity, so the lane only
	// starts failing after the turn exists and the player points at it.
	store := &playerMergeFailingStore{fakeStore: newFakeStore()}
	recorder := newRecorder(t, store)
	if _, err := recorder.Accept(context.Background(), testAction()); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	store.fail = true

	_, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{})
	if err == nil {
		t.Fatal("a lost resolved-pointer write was reported as success; the caller would acknowledge and " +
			"nothing would ever retry it")
	}
	if pointers := resolvedPointers(t, store.fakeStore, testPlayerID); len(pointers) != 0 {
		t.Fatalf("the pointer landed despite the failing lane: %v", pointers)
	}

	// The redelivery. The turn is already failed, so the transition declines —
	// and the repair is the only thing that can still land the pointer.
	store.fail = false
	transition, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{})
	if err != nil {
		t.Fatalf("the redelivery failed: %v", err)
	}
	if transition.Outcome != turn.OutcomeDeclined {
		t.Fatalf("the redelivery reported %q; a turn that already ended declines", transition.Outcome)
	}
	pointers := resolvedPointers(t, store.fakeStore, testPlayerID)
	if len(pointers) != 1 || pointers[0] != testTurnEntityID {
		t.Fatalf("the redelivery left the player pointing at %v, want the turn that ended (%s); a pointer "+
			"nothing repairs is a player permanently answered with their previous turn", pointers, testTurnEntityID)
	}
}

// The other half of the repair, and the reason it is guarded rather than
// unconditional: a stale trigger for an OLD turn must not drag the pointer
// backwards over a newer turn's answer. player.turn.current names the most
// recently accepted turn, so a repair for a turn it does not name is a repair
// arriving after the player moved on.
func TestTransition_AStaleRedeliveryDoesNotRunTheResolvedPointerBackwards(t *testing.T) {
	const (
		secondActionID = "act-2"
		secondTurnID   = "turn-act-2"
		secondEntityID = "c360.semmachina.world1.starter.turn.turn-act-2"
	)

	store := newFakeStore()
	recorder := newRecorder(t, store)

	// The first turn runs and ends.
	if _, err := recorder.Accept(context.Background(), testAction()); err != nil {
		t.Fatalf("Accept the first action: %v", err)
	}
	if _, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{},
	); err != nil {
		t.Fatalf("Fail the first turn: %v", err)
	}

	// The player takes another action, which moves the current pointer.
	second := testAction()
	second.ActionID = secondActionID
	if _, err := recorder.Accept(context.Background(), second); err != nil {
		t.Fatalf("Accept the second action: %v", err)
	}
	if _, err := recorder.Fail(
		context.Background(), secondTurnID, secondEntityID, vocabulary.FailureTurnStranded, content.Ref{},
	); err != nil {
		t.Fatalf("Fail the second turn: %v", err)
	}
	if pointers := resolvedPointers(t, store, testPlayerID); len(pointers) != 1 || pointers[0] != secondEntityID {
		t.Fatalf("after two turns the resolved pointer is %v, want the second (%s)", pointers, secondEntityID)
	}

	// A stale trigger for the FIRST turn redelivers long after.
	if _, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{},
	); err != nil {
		t.Fatalf("the stale redelivery errored: %v", err)
	}

	pointers := resolvedPointers(t, store, testPlayerID)
	if len(pointers) != 1 || pointers[0] != secondEntityID {
		t.Fatalf("a stale redelivery moved the resolved pointer to %v; it must still name the turn that "+
			"resolved most recently (%s)", pointers, secondEntityID)
	}
}

// A turn whose birth record does not name its player cannot be delivered to
// anyone and cannot be archived either. It is reported rather than skipped: the
// alternative is a turn that quietly resolves for nobody.
func TestTransition_ATurnWithNoPlayerIsReportedRatherThanResolvedForNobody(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)
	if _, err := recorder.Accept(context.Background(), testAction()); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	stripPredicate(store, testTurnEntityID, vocabulary.TurnActionPlayer)

	_, err := recorder.Fail(
		context.Background(), testTurnID, testTurnEntityID, vocabulary.FailureTurnStranded, content.Ref{})
	if err == nil {
		t.Fatal("a turn carrying no player resolved silently")
	}
	if !strings.Contains(err.Error(), vocabulary.TurnActionPlayer.String()) {
		t.Errorf("the failure %q does not name the missing fact", err)
	}
}

func TestResolvedTurns_ReadsTheAnomalyRatherThanAVoteOnIt(t *testing.T) {
	store := newFakeStore()
	resolvedTurn(t, store)
	state, err := store.GetEntity(context.Background(), testPlayerID)
	if err != nil {
		t.Fatalf("read the player: %v", err)
	}

	resolved, unreadable := turn.ResolvedTurns(state)
	if len(resolved) != 1 || resolved[0] != testTurnEntityID || unreadable != 0 {
		t.Fatalf("ResolvedTurns = %v, %d; want the one turn that resolved and nothing unreadable",
			resolved, unreadable)
	}

	// The append anomaly: two values for a single-valued predicate. A reader
	// that took the first would answer a coin flip about what happened last.
	state.Triples = append(state.Triples, message.Triple{
		Subject:   testPlayerID,
		Predicate: vocabulary.PlayerTurnResolved.String(),
		Object:    "c360.semmachina.world1.starter.turn.turn-act-9",
		Source:    turn.Source,
		Timestamp: testTime,
	}, message.Triple{
		Subject:   testPlayerID,
		Predicate: vocabulary.PlayerTurnResolved.String(),
		Object:    42,
		Source:    turn.Source,
		Timestamp: testTime,
	})

	resolved, unreadable = turn.ResolvedTurns(state)
	if len(resolved) != 2 {
		t.Fatalf("ResolvedTurns reported %v, want BOTH values so a reader can see the anomaly", resolved)
	}
	if unreadable != 1 {
		t.Fatalf("ResolvedTurns counted %d unreadable pointers, want 1; a pointer nothing can read is a "+
			"pointer nothing checks", unreadable)
	}
	if got, _ := turn.ResolvedTurns(nil); got != nil {
		t.Errorf("ResolvedTurns(nil) = %v, want nothing", got)
	}
}

// ------------------------------------------------------------ test doubles

// playerMergeFailingStore fails merges onto the PLAYER entity only.
//
// A store that failed every merge could not reach the case under test at all:
// the phase write happens first and would fail, so the turn would never become
// terminal and the pointer write would never be attempted.
type playerMergeFailingStore struct {
	*fakeStore
	fail bool
}

func (s *playerMergeFailingStore) MergeTriples(
	ctx context.Context,
	entityID string,
	triples []message.Triple,
	opts ...graphio.MergeOption,
) (*graph.EntityState, error) {
	if s.fail && entityID == testPlayerID {
		return nil, errors.New("the player lane is unreachable")
	}
	return s.fakeStore.MergeTriples(ctx, entityID, triples, opts...)
}

// stripPredicate removes a fact from a stored entity, which is how a corrupt
// record is produced without a lane that can produce one.
func stripPredicate(store *fakeStore, entityID string, predicate vocabulary.Predicate) {
	stored := store.entities[entityID]
	kept := stored.Triples[:0]
	for _, triple := range stored.Triples {
		if triple.Predicate != predicate.String() {
			kept = append(kept, triple)
		}
	}
	stored.Triples = kept
}
