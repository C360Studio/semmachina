package e2e_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The turn resolves with nobody connected, and the answer is still there when the
// player comes back.
//
// This is the email-cadence claim made concrete. The player submits and leaves —
// no socket, no session, nothing in adapter memory — and every step after that has
// to complete without anybody waiting on it. Three things are asserted that a
// clock-dependent engine would fail: the turn completes, the delivery path
// ACKNOWLEDGES a result with zero recipients rather than retrying it forever, and
// the answer is composed from durable state whenever it is finally asked for.
func TestE2E_ATurnResolvesWithNobodyConnectedAndTheAnswerWaits(t *testing.T) {
	w := newWorld(t, "e2egap", "no-roll")

	player := w.dial(t)
	response := player.submit(t, "gap-1", "I take the supper and go.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)

	// The player leaves. Everything from here happens with no session for this
	// player anywhere in the process.
	player.close()

	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
		t.Fatalf("the turn ended in %q with nobody watching; no turn-loop step may wait on a player", phase)
	}

	// Zero connected recipients is an ORDINARY outcome. If it were an error the
	// delivery consumer would nak and redeliver forever, and both of these are
	// where that shows — the second one specifically, because the queue view above
	// covers the stage runners and the agentic loop and not the delivery lane.
	requireNothingQueuedFor(t, turnEntityID)
	requireResolvedNotificationAcked(t, turnEntityID)

	// The answer is composed from the graph and the object store, by the same
	// production surface the push path uses. Nothing was remembered.
	first, err := w.results(t).Latest(t.Context(), w.playerID)
	if err != nil {
		t.Fatalf("retrieve the player's last terminal result: %v", err)
	}
	if first.Result.TurnID != response.TurnID {
		t.Errorf("retrieval answered with turn %q, want %q", first.Result.TurnID, response.TurnID)
	}
	if first.Narration == nil || first.Narration.Prose == "" {
		t.Fatal("the retrieved result carries no prose; a player who was away when their turn resolved is " +
			"answered by the same path as one who was watching")
	}

	// Retrieved again after a real gap, the record says the same thing. ResolvedAt
	// is when the TURN ended, and a surface that quietly stamped retrieval time
	// would make a result read differently on Tuesday than it did on Monday —
	// which is precisely the assumption email-cadence play forbids.
	time.Sleep(1200 * time.Millisecond)
	second, err := w.results(t).Latest(t.Context(), w.playerID)
	if err != nil {
		t.Fatalf("retrieve the same result again: %v", err)
	}
	if !second.Result.ResolvedAt.Equal(first.Result.ResolvedAt) {
		t.Errorf("the same turn reports resolved_at %s and then %s; retrieval time has become part of the "+
			"record", first.Result.ResolvedAt, second.Result.ResolvedAt)
	}
	if !bytesEqual(t, first, second) {
		t.Error("two retrievals of one turn produced different documents; retrieval is supposed to be a " +
			"reading of durable state, and a reading that changes is a reading with memory in it")
	}
}

// A player who reconnects between submitting and resolving is delivered to on the
// NEW connection.
//
// Identity is a graph entity and the connection is a detail: the router resolves a
// player's live sessions at DELIVERY time, so a binding captured at submission
// would deliver a turn's fiction to a socket that is already gone. Proving that
// needs the reconnect to happen INSIDE the turn, which is why the narration lane
// is paused — the reconnect is then an event the turn happens across rather than
// a race the test hopes to win.
func TestE2E_ADeliveryFollowsThePlayerAcrossAReconnect(t *testing.T) {
	w := newWorld(t, "e2ereconnect", "duplicate-delivery")

	held := pauseConsumer(t, rulepack.StageStream, rulepack.StageConsumerName(vocabulary.PhaseNarrating))

	first := w.dial(t)
	response := first.submit(t, "reconnect-1", "I pocket the supper and keep walking.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)

	// The turn is now mid-flight and cannot finish: the narration trigger is
	// durably queued and nothing is reading it.
	requireQueued(t, rulepack.StageStream, rulepack.StageConsumerName(vocabulary.PhaseNarrating), 30*time.Second)

	// The player's connection goes away and a new one arrives. Same credential,
	// same player entity, a connection the engine has never seen before.
	first.close()
	second := w.dial(t)

	held()

	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
		t.Fatalf("the turn ended in %q", phase)
	}

	delivery := second.await(t, playersocket.FrameTurnDelivery, turnBudget).Delivery
	if delivery.Result.TurnID != response.TurnID {
		t.Errorf("the reconnected socket received turn %q, want %q", delivery.Result.TurnID, response.TurnID)
	}
	if delivery.Result.PlayerID != w.playerID {
		t.Errorf("the delivery names player %q, want %q", delivery.Result.PlayerID, w.playerID)
	}

	// What was PUSHED and what durable retrieval ANSWERS are the same document.
	// They are assembled by one composition from one record, and a divergence
	// between them would mean a player who was watching and a player who came back
	// are told different things about one turn.
	retrieved, err := w.results(t).ByTurn(t.Context(), response.TurnID)
	if err != nil {
		t.Fatalf("retrieve the turn by id: %v", err)
	}
	if !bytesEqual(t, delivery, retrieved) {
		t.Error("the pushed delivery and the retrieved one are different documents")
	}

	// Retrieval by ACTION id answers with the same turn, which is what a client
	// holding only its own idempotency-derived id can ask.
	byAction, err := w.results(t).ByAction(t.Context(), response.ActionID)
	if err != nil {
		t.Fatalf("retrieve the turn by action id: %v", err)
	}
	if byAction.Result.TurnID != response.TurnID {
		t.Errorf("retrieval by action %q answered with turn %q", response.ActionID, byAction.Result.TurnID)
	}

	requireNothingQueuedFor(t, turnEntityID)
}

// bytesEqual compares two deliveries as the bytes a client would receive.
//
// The canonical marshalers rather than reflect.DeepEqual, because the claim is
// about the DOCUMENT: two structs that differ only in an unexported or
// zero-valued field are the same document, and two that marshal differently are
// not, whatever the structs look like.
func bytesEqual(t *testing.T, a, b *payload.TurnDelivery) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("encode a delivery: %v", err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("encode a delivery: %v", err)
	}
	if string(left) != string(right) {
		t.Logf("first:  %s", left)
		t.Logf("second: %s", right)
		return false
	}
	return true
}
