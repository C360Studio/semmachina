package turn_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The ingress admission gate's whole durable state, from the writing side. The
// reading side lives in internal/gateway; these tests prove the fact is there,
// singular, and pointing where the gate expects.

func playerPointers(t *testing.T, store *fakeStore) []any {
	t.Helper()
	stored, err := store.GetEntity(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("read the player entity: %v", err)
	}
	return objectsFor(stored, vocabulary.PlayerTurnCurrent)
}

// endTurn rewrites a turn's phase to a terminal one directly in the fixture.
//
// Directly rather than through Advance, because the store under test in the
// negative control below APPENDS: an Advance through it would leave two phases,
// and a two-phase turn reads as unreadable rather than as ended — which would
// make the control pass through a branch it is not about.
func endTurn(t *testing.T, store *fakeStore, turnEntityID string) {
	t.Helper()
	stored, ok := store.entities[turnEntityID]
	if !ok {
		t.Fatalf("no turn %s to end", turnEntityID)
	}
	kept := stored.Triples[:0]
	for _, triple := range stored.Triples {
		if triple.Predicate != vocabulary.TurnPhaseCurrent.String() {
			kept = append(kept, triple)
		}
	}
	stored.Triples = append(kept, message.Triple{
		Subject:   turnEntityID,
		Predicate: vocabulary.TurnPhaseCurrent.String(),
		Object:    string(vocabulary.PhaseComplete),
		Source:    turn.Source,
		Timestamp: testTime,
	})
}

func TestAccept_PointsThePlayerAtTheTurnItJustCreated(t *testing.T) {
	store, _, acceptance := acceptedTurn(t)

	pointers := playerPointers(t, store)
	if len(pointers) != 1 {
		t.Fatalf("the player holds %d turn pointers: %v; the gate reads one or it reads a coin flip",
			len(pointers), pointers)
	}
	if pointers[0] != acceptance.TurnEntityID {
		t.Fatalf("the player points at %v, want the turn just accepted %q", pointers[0], acceptance.TurnEntityID)
	}
}

// The pointer replaces rather than appends, so a player's second turn leaves
// them pointing at exactly one. The negative control is the whole test: through
// an appending lane the same two accepts leave two pointers, with a success
// response and no error anywhere.
func TestAccept_TheSecondTurnReplacesThePointerRatherThanAddingOne(t *testing.T) {
	store, recorder, first := acceptedTurn(t)
	advanceTo(t, recorder, first,
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseCompanion, vocabulary.PhaseNarrating, vocabulary.PhaseComplete)

	second := testAction()
	second.ActionID = "act-2"
	accepted, err := recorder.Accept(t.Context(), second)
	if err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	pointers := playerPointers(t, store)
	if len(pointers) != 1 || pointers[0] != accepted.TurnEntityID {
		t.Fatalf("after two turns the player holds %v, want exactly [%s]", pointers, accepted.TurnEntityID)
	}

	// Negative control: the appending lane leaves the failure this predicate's
	// merge-lane discipline exists to prevent.
	//
	// The first turn is ENDED between the two accepts, exactly as it is above,
	// because the backwards guard would otherwise decline the second write and
	// the control would report one pointer for a reason that has nothing to do
	// with which lane the write took.
	appending := &appendingStore{newFakeStore()}
	appendingRecorder := newRecorderWithActions(t, appending, newFakeActions(appending.journal))
	firstAppended, err := appendingRecorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept through the appending lane: %v", err)
	}
	endTurn(t, appending.fakeStore, firstAppended.TurnEntityID)
	if _, err := appendingRecorder.Accept(t.Context(), second); err != nil {
		t.Fatalf("second Accept through the appending lane: %v", err)
	}
	stored, err := appending.GetEntity(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("read the player entity: %v", err)
	}
	if got := objectsFor(stored, vocabulary.PlayerTurnCurrent); len(got) != 2 {
		t.Fatalf("the appending lane left %d pointers: %v; if this is 1 the control proves nothing and the "+
			"merge-lane assertion above is vacuous", len(got), got)
	}
}

// The crash this heals: the turn create landed and the process died before the
// pointer write. The action was never acknowledged, so it is redelivered, and
// the redelivery must finish the job rather than skipping it as a duplicate.
func TestAccept_ARedeliveryLandsThePointerTheCrashLost(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)

	store.mergeErr = errors.New("nats: no responders available")
	if _, err := recorder.Accept(t.Context(), testAction()); err == nil {
		t.Fatal("a failed pointer write was reported as a clean accept")
	}
	if got := playerPointers(t, store); len(got) != 0 {
		t.Fatalf("the failed write left %v on the player", got)
	}

	store.mergeErr = nil
	acceptance, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if acceptance.Created {
		t.Fatal("the redelivery reported that it created the turn")
	}
	if got := playerPointers(t, store); len(got) != 1 || got[0] != acceptance.TurnEntityID {
		t.Fatalf("the redelivery left %v on the player, want [%s]; the pointer the crash lost is never "+
			"written again if a duplicate skips it", got, acceptance.TurnEntityID)
	}
}

// The hazard the terminal check exists for: a redelivery of an OLD, finished
// action arriving while the player is midway through a NEW turn. Repointing them
// at the finished one would have the gate hand them a third turn while the
// second is still running.
func TestAccept_ARedeliveryOfAFinishedTurnDoesNotRunThePointerBackwards(t *testing.T) {
	store, recorder, first := acceptedTurn(t)
	advanceTo(t, recorder, first,
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseCompanion, vocabulary.PhaseNarrating, vocabulary.PhaseComplete)

	second := testAction()
	second.ActionID = "act-2"
	live, err := recorder.Accept(t.Context(), second)
	if err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	// The first action is redelivered now, long after its turn completed.
	if _, err := recorder.Accept(t.Context(), testAction()); err != nil {
		t.Fatalf("redelivery of the finished action: %v", err)
	}

	got := playerPointers(t, store)
	if len(got) != 1 || got[0] != live.TurnEntityID {
		t.Fatalf("the player points at %v, want the LIVE turn [%s]; a pointer moved back to a finished turn "+
			"unblocks a player who is already mid-turn", got, live.TurnEntityID)
	}
}

// H1: the pointer must not move BACKWARDS onto an older turn while a NEWER one
// is still running.
//
// The terminal check on the turn being pointed at is necessary and not
// sufficient. It answers "a turn that has ended is not one anybody holds", and
// the guard needs the converse — "a turn that has not ended is the one they
// hold" — which is false the moment two turns are unfinished at once. The
// transient-failure path this recorder is built to survive creates exactly that,
// with no crash and no hostile client:
//
//  1. the pointer write for T1 fails, so A1 naks with a 30-second delay;
//  2. the gate reads a terminal pointer, admits A2, and T2 becomes the live turn;
//  3. A1 redelivers into an existing, still-running T1.
//
// Step 3 is where an unguarded write repoints the player at T1 and abandons T2 —
// after which T1 resolving unlocks the gate while T2 is still live, and the
// "one extra turn" bound becomes a cascade.
func TestAccept_ARedeliveryDoesNotStealThePointerFromANewerLiveTurn(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)

	// 1. T1 is created and the pointer write fails. The action is nak'd.
	store.mergeErr = errors.New("nats: no responders available")
	if _, err := recorder.Accept(t.Context(), testAction()); err == nil {
		t.Fatal("a failed pointer write was reported as a clean accept")
	}
	store.mergeErr = nil

	// 2. The gate sees no pointer at all, admits a second action, and T2
	//    becomes the turn the player is actually running.
	second := testAction()
	second.ActionID = "act-2"
	live, err := recorder.Accept(t.Context(), second)
	if err != nil {
		t.Fatalf("second Accept: %v", err)
	}
	if got := playerPointers(t, store); len(got) != 1 || got[0] != live.TurnEntityID {
		t.Fatalf("the second turn left the player pointing at %v, want [%s]", got, live.TurnEntityID)
	}

	// 3. A1 redelivers. T1 exists and is still running — and it is NOT the turn
	//    this player is on.
	stale, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if stale.Phase.IsTerminal() {
		t.Fatal("the redelivered turn had already ended, so this test exercises the terminal guard rather " +
			"than the one it exists for")
	}

	got := playerPointers(t, store)
	if len(got) != 1 || got[0] != live.TurnEntityID {
		t.Fatalf("the redelivery moved the pointer to %v; the player is running %s, and abandoning it means "+
			"the gate reopens as soon as the OLD turn resolves while the new one is still live",
			got, live.TurnEntityID)
	}
}

// The same hazard on the CREATED branch, which is reachable by the same means:
// A1's create fails transiently, A2 is admitted and becomes the live turn, and
// A1's redelivery then creates T1 for the first time. The turn is fresh, so
// there is no recorded phase to make it look stale — only the player's current
// pointer can tell the recorder it is late.
func TestAccept_AFirstTimeCreateDoesNotStealThePointerFromANewerLiveTurn(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)

	store.createErr = errors.New("nats: timeout")
	if _, err := recorder.Accept(t.Context(), testAction()); err == nil {
		t.Fatal("a failed create was reported as a clean accept")
	}
	store.createErr = nil

	second := testAction()
	second.ActionID = "act-2"
	live, err := recorder.Accept(t.Context(), second)
	if err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	late, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if !late.Created {
		t.Fatal("the redelivery found an existing turn, so this exercises the duplicate branch rather than " +
			"the create branch it is written for")
	}

	got := playerPointers(t, store)
	if len(got) != 1 || got[0] != live.TurnEntityID {
		t.Fatalf("a first-time create moved the pointer to %v, abandoning the live %s", got, live.TurnEntityID)
	}
}

// The heal must survive the guard: a pointer at a TERMINAL turn is not an
// obstacle, so the crash gap still closes on redelivery.
func TestAccept_TheBackwardsGuardStillLetsTheCrashGapHeal(t *testing.T) {
	store, recorder, first := acceptedTurn(t)
	advanceTo(t, recorder, first,
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseCompanion, vocabulary.PhaseNarrating, vocabulary.PhaseComplete)

	// The next turn's pointer write fails, leaving the player pointing at the
	// finished one.
	second := testAction()
	second.ActionID = "act-2"
	store.mergeErr = errors.New("nats: no responders available")
	if _, err := recorder.Accept(t.Context(), second); err == nil {
		t.Fatal("a failed pointer write was reported as a clean accept")
	}
	store.mergeErr = nil

	healed, err := recorder.Accept(t.Context(), second)
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if got := playerPointers(t, store); len(got) != 1 || got[0] != healed.TurnEntityID {
		t.Fatalf("the redelivery left the player pointing at %v, want the healed [%s]; a pointer at a "+
			"FINISHED turn is not an obstacle and the gap must still close", got, healed.TurnEntityID)
	}
}

// The guard asks whether the player holds a DIFFERENT live turn, and the
// difference matters when the pointer holds several values — the signature of a
// write that took an appending lane.
//
// A redelivery of the turn the player is ALREADY on must not read its own turn
// as the obstacle: writing converges the pointer back to one value on the merge
// lane, and treating itself as an obstacle would leave the gate reading two and
// answering a coin flip about whether that player may act.
func TestAccept_ARedeliveryConvergesAPointerThatHoldsSeveralValues(t *testing.T) {
	store, recorder, live := acceptedTurn(t)

	// An older, finished turn is appended beside the live one, which is what an
	// appending write leaves behind.
	finished := testAction()
	finished.ActionID = "act-0"
	finishedEntity := strings.Replace(live.TurnEntityID, live.TurnID, "turn-act-0", 1)
	putTurnPhase(t, store, finishedEntity, vocabulary.PhaseComplete)
	appendPointer(t, store, testPlayerID, "turn-act-0", finishedEntity)

	if got := playerPointers(t, store); len(got) != 2 {
		t.Fatalf("the fixture holds %d pointers, want 2; this test would prove nothing about the "+
			"several-values case", len(got))
	}

	if _, err := recorder.Accept(t.Context(), testAction()); err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	got := playerPointers(t, store)
	if len(got) != 1 || got[0] != live.TurnEntityID {
		t.Fatalf("the redelivery left the player holding %v, want the single live [%s]; a turn that reads "+
			"itself as its own obstacle leaves the gate answering a coin flip", got, live.TurnEntityID)
	}
}

// putTurnPhase plants a turn entity in a phase, directly in the fixture.
func putTurnPhase(t *testing.T, store *fakeStore, turnEntityID string, phase vocabulary.TurnPhase) {
	t.Helper()
	store.entities[turnEntityID] = &graph.EntityState{
		ID:          turnEntityID,
		MessageType: turn.EntityMessageType,
		Version:     1,
		Triples: []message.Triple{{
			Subject:   turnEntityID,
			Predicate: vocabulary.TurnPhaseCurrent.String(),
			Object:    string(phase),
			Source:    turn.Source,
			Timestamp: testTime,
		}},
	}
}

// appendPointer adds a SECOND current-turn pointer the way an appending lane
// would, which is the anomaly the guard has to converge rather than freeze.
func appendPointer(t *testing.T, store *fakeStore, playerID, turnID, turnEntityID string) {
	t.Helper()
	pointer := &payload.PlayerTurn{PlayerID: playerID, TurnID: turnID, TurnEntityID: turnEntityID}
	triples, err := pointer.Triples(playerID, turn.Source, testTime)
	if err != nil {
		t.Fatalf("compose the pointer: %v", err)
	}
	store.entities[playerID].Triples = append(store.entities[playerID].Triples, triples...)
}

// A failed pointer write is TRANSIENT, so intake naks and redelivers rather than
// terminating. Terminating would throw away a move the player can never
// resubmit, to spare a redelivery of a turn that already exists.
func TestAccept_AFailingPointerWriteIsNotARejectedAction(t *testing.T) {
	store := newFakeStore()
	store.mergeErr = errors.New("nats: timeout")

	_, err := newRecorder(t, store).Accept(t.Context(), testAction())
	if err == nil {
		t.Fatal("a failed pointer write was reported as a clean accept")
	}
	var rejected *turn.RejectedActionError
	if errors.As(err, &rejected) {
		t.Fatal("a failed pointer write was classified as a permanently rejected action; intake would " +
			"terminate the delivery and the player's move would be gone")
	}
}

// The pointer is a claim about a specific player. A recorder that wrote it onto
// whatever entity happened to be handed to it would grant a bystander a live
// turn, so the payload states the player and the projection requires the write's
// destination to agree.
func TestAccept_ThePointerLandsOnThePlayerTheActionNames(t *testing.T) {
	store, _, acceptance := acceptedTurn(t)

	for id := range store.entities {
		if id == testPlayerID || id == acceptance.TurnEntityID {
			continue
		}
		t.Fatalf("accepting a turn touched unexpected entity %q", id)
	}
	if store.mergesInto(testPlayerID) != 1 {
		t.Fatalf("the accept wrote to the player %d time(s), want exactly one",
			store.mergesInto(testPlayerID))
	}

	// And the projection refuses the mismatch directly, which is what makes the
	// property above a type rather than a coincidence of this call site.
	pointer := &payload.PlayerTurn{
		PlayerID:     testPlayerID,
		TurnID:       acceptance.TurnID,
		TurnEntityID: acceptance.TurnEntityID,
	}
	if _, err := pointer.Triples(testSceneID, turn.Source, testTime); err == nil {
		t.Fatal("a player pointer was projected onto an entity that is not the player it names")
	}
}
