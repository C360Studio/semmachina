package egress_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestByTurn_ComposesTheWholeResolutionCardFromDurableState(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)

	delivery := h.mustDeliver(t, testTurnID)
	result := delivery.Result

	if result.TurnID != testTurnID || result.ActionID != testActionID {
		t.Fatalf("result names turn %q / action %q, want %q / %q",
			result.TurnID, result.ActionID, testTurnID, testActionID)
	}
	if result.PlayerID != testPlayerID {
		t.Fatalf("result belongs to %q, want %q", result.PlayerID, testPlayerID)
	}
	if result.Phase != vocabulary.PhaseComplete {
		t.Fatalf("result phase = %q, want complete", result.Phase)
	}
	if !result.ResolvedAt.Equal(testTime) {
		t.Fatalf("resolved_at = %s, want the moment the phase was recorded (%s); a composition stamp would "+
			"make retrieval time part of the record", result.ResolvedAt, testTime)
	}

	if result.Resolution == nil {
		t.Fatal("a completed turn carries no resolution; the card is what makes the outcome legible")
	}
	verdict := result.Resolution.Verdict
	if verdict.Plausibility != vocabulary.PlausibilityPlausible ||
		verdict.Risk != vocabulary.RiskHigh ||
		verdict.Consequence != vocabulary.ConsequenceHarm ||
		!verdict.RequiresRoll {
		t.Fatalf("the card's verdict is %+v, want the scalars the rule pack routed the turn on", verdict)
	}
	if result.Resolution.Band != vocabulary.BandPartial {
		t.Fatalf("band = %q, want the band the dice selected", result.Resolution.Band)
	}
	roll := result.Resolution.Roll
	if roll == nil {
		t.Fatal("a rolled turn carries no roll; a total with no dice behind it explains nothing")
	}
	if roll.Total != 9 || roll.ModifierTotal != 1 || len(roll.Dice) != 2 {
		t.Fatalf("roll = %+v, want the recorded dice and modifiers", roll)
	}
	if roll.Mechanic != vocabulary.Mechanic2d6PbtaV1 {
		t.Fatalf("roll mechanic = %q; a total of 9 means nothing without the mechanic it was thrown under",
			roll.Mechanic)
	}

	if delivery.Narration == nil || delivery.Narration.Prose == "" {
		t.Fatal("the delivery carries no prose; no client can resolve an obj:// reference, so a delivery of " +
			"the bare reference is a result the player cannot read")
	}
	if delivery.Result.NarrationRef == "" {
		t.Fatal("the delivered result dropped its narration reference; the delivered document must be the " +
			"published one, not a rewritten shape")
	}
}

// The delivered document carries this turn's prose and nothing else bulky. The
// engine's flat-per-turn cost claim is about tokens; this is the same claim about
// the wire, and it is the one "append everything" would break.
func TestByTurn_CarriesOneTurnsProseAndNoOtherArtifact(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)

	delivery := h.mustDeliver(t, testTurnID)
	encoded, err := delivery.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal the delivery: %v", err)
	}
	body := string(encoded)

	// The verdict's rationale is the adjudicator's own prose about its judgment,
	// and it must never reach a player: it is a second, unvoiced account of the
	// turn beside the narrator's.
	if strings.Contains(body, "The gate is heavy but the crowbar bites") {
		t.Fatal("the delivered document carries the adjudicator's rationale; only the narrator voices a turn")
	}
	// The action reference and the effect batch reference are engine bookkeeping.
	for _, leaked := range []string{
		refFor(vocabulary.TurnActionRef, testTurnID),
		refFor(vocabulary.TurnEffectsRef, testTurnID),
		refFor(vocabulary.TurnVerdictRef, testTurnID),
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("the delivered document carries %s; only the narration reference is a player's business",
				leaked)
		}
	}
	if !strings.Contains(body, testNarration(testTurnID, vocabulary.BandPartial).Prose) {
		t.Fatal("the delivered document does not carry the prose; the whole reason the server dereferences " +
			"is that the client cannot")
	}
}

func TestByAction_AnswersWithTheTurnThatActionProduced(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)

	delivery, err := h.results.ByAction(t.Context(), testActionID)
	if err != nil {
		t.Fatalf("ByAction: %v", err)
	}
	if delivery.Result.TurnID != testTurnID {
		t.Fatalf("ByAction answered with turn %q, want %q", delivery.Result.TurnID, testTurnID)
	}
}

// A turn still running is answered as such rather than as absent. The two are
// different answers to the player, and a surface that spelled them the same way
// would leave them unable to tell waiting from a typo.
func TestByTurn_ATurnStillRunningIsNotAResult(t *testing.T) {
	h := newHarness(t)
	h.graph.putTurn(newTurn(t, testTurnID).
		accepted(testPlayerID).
		adjudicated(testVerdict(testTurnID, true)).
		phase(vocabulary.PhaseResolving, testTime).
		build())

	_, err := h.results.ByTurn(t.Context(), testTurnID)
	if !errors.Is(err, egress.ErrNoResult) {
		t.Fatalf("ByTurn on a live turn = %v, want ErrNoResult", err)
	}
	if errors.Is(err, egress.ErrTurnNotFound) {
		t.Fatal("a running turn was reported as a turn that does not exist")
	}

	_, err = h.results.ByTurn(t.Context(), "turn-act-nothing")
	if !errors.Is(err, egress.ErrTurnNotFound) {
		t.Fatalf("ByTurn on an unknown turn = %v, want ErrTurnNotFound", err)
	}
}

// Both failed shapes, because they are what a terminal surface exists for and
// they are the ones a completion-keyed surface cannot answer.
func TestByTurn_AnswersForBothShapesOfFailedTurn(t *testing.T) {
	t.Run("died before the narrator ran", func(t *testing.T) {
		h := newHarness(t)
		h.graph.putTurn(newTurn(t, testTurnID).
			accepted(testPlayerID).
			failed(vocabulary.FailureTurnStranded, testTime).
			build())

		delivery := h.mustDeliver(t, testTurnID)
		if delivery.Result.Phase != vocabulary.PhaseFailed {
			t.Fatalf("phase = %q, want failed", delivery.Result.Phase)
		}
		if delivery.Result.FailureReason != vocabulary.FailureTurnStranded {
			t.Fatalf("failure_reason = %q, want the recorded code", delivery.Result.FailureReason)
		}
		if delivery.Narration != nil {
			t.Fatalf("a turn that died before the narrator carries prose: %q", delivery.Narration.Prose)
		}
		if delivery.Result.Resolution != nil {
			t.Fatal("a turn that failed before adjudication carries a resolution; there was no verdict to " +
				"build one from, and a zero-valued card would report classes nobody judged")
		}
	})

	t.Run("abandoned after its prose landed", func(t *testing.T) {
		h := newHarness(t)
		verdict := testVerdict(testTurnID, true)
		roll := testRoll(testTurnID)
		h.graph.putTurn(newTurn(t, testTurnID).
			accepted(testPlayerID).
			adjudicated(verdict).
			rolled(roll).
			applied(payload.NewEffectBatch(testTurnID, roll.Band, nil)).
			narrated().
			failed(vocabulary.FailureTurnStranded, testTime).
			build())
		h.artifacts.rolls[refFor(vocabulary.TurnRollRef, testTurnID)] = roll
		h.artifacts.narrations[refFor(vocabulary.TurnNarrationRef, testTurnID)] =
			testNarration(testTurnID, roll.Band)

		delivery := h.mustDeliver(t, testTurnID)
		if delivery.Result.Phase != vocabulary.PhaseFailed {
			t.Fatalf("phase = %q, want failed", delivery.Result.Phase)
		}
		if delivery.Narration == nil {
			t.Fatal("a turn abandoned after its prose landed delivered no prose; that throws away fiction the " +
				"player already earned")
		}
		if delivery.Result.Resolution == nil || delivery.Result.Resolution.Roll == nil {
			t.Fatal("a failed turn that reached the dice delivered no roll")
		}
	})
}

// A no-roll verdict resolves in the auto band and has no dice. nil is that fact,
// not an empty roll — which would render as a card claiming a total of zero.
func TestByTurn_ANoRollTurnBandsAutoAndCarriesNoDice(t *testing.T) {
	h := newHarness(t)
	verdict := testVerdict(testTurnID, false)
	h.graph.putTurn(newTurn(t, testTurnID).
		accepted(testPlayerID).
		adjudicated(verdict).
		applied(payload.NewEffectBatch(testTurnID, vocabulary.BandAuto, nil)).
		narrated().
		phase(vocabulary.PhaseComplete, testTime).
		build())
	h.artifacts.narrations[refFor(vocabulary.TurnNarrationRef, testTurnID)] =
		testNarration(testTurnID, vocabulary.BandAuto)

	delivery := h.mustDeliver(t, testTurnID)
	if delivery.Result.Resolution.Band != vocabulary.BandAuto {
		t.Fatalf("band = %q, want auto", delivery.Result.Resolution.Band)
	}
	if delivery.Result.Resolution.Roll != nil {
		t.Fatalf("a no-roll turn carries a roll: %+v", delivery.Result.Resolution.Roll)
	}
}

// A turn that ended between adjudication and the applier acted on NO band. Naming
// one would tell the player the engine did something it had not.
func TestByTurn_ATurnThatSelectedNoBandReportsNone(t *testing.T) {
	h := newHarness(t)
	h.graph.putTurn(newTurn(t, testTurnID).
		accepted(testPlayerID).
		adjudicated(testVerdict(testTurnID, false)).
		failed(vocabulary.FailureTurnStranded, testTime).
		build())

	delivery := h.mustDeliver(t, testTurnID)
	if delivery.Result.Resolution == nil {
		t.Fatal("a turn that WAS adjudicated carries no resolution; the verdict's classes are what it earned")
	}
	if got := delivery.Result.Resolution.Band; got != "" {
		t.Fatalf("band = %q for a turn the applier never ran; no band was acted on", got)
	}
}

// The reference-to-missing-prose case, which the content store's write ordering
// says cannot happen and which therefore must be loud when it does.
func TestByTurn_AReferenceToProseNobodyStoredIsItsOwnFailure(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	delete(h.artifacts.narrations, refFor(vocabulary.TurnNarrationRef, testTurnID))

	_, err := h.results.ByTurn(t.Context(), testTurnID)
	if !errors.Is(err, egress.ErrNarrationMissing) {
		t.Fatalf("ByTurn with missing prose = %v, want ErrNarrationMissing", err)
	}
	if !errors.Is(err, content.ErrArtifactNotFound) {
		t.Errorf("the failure %q does not carry the store's own sentinel", err)
	}
}

// The same check on the other artifact a result dereferences. A roll reference
// resolving to another turn's dice would explain this turn's outcome with
// somebody else's numbers — the same class of defect as the prose one, and it is
// the half a test that only covered narration would leave open.
func TestByTurn_RefusesDiceBelongingToAnotherTurn(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.artifacts.rolls[refFor(vocabulary.TurnRollRef, testTurnID)] = testRoll("turn-act-9")

	_, err := h.results.ByTurn(t.Context(), testTurnID)
	if err == nil {
		t.Fatal("a result was composed carrying another turn's dice")
	}
	if !strings.Contains(err.Error(), "turn-act-9") {
		t.Errorf("the failure %q does not name the roll's real turn", err)
	}
}

// A narration reference resolving to ANOTHER turn's prose is one player reading
// another's fiction through the durable surface rather than through a socket.
func TestByTurn_RefusesProseBelongingToAnotherTurn(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.artifacts.narrations[refFor(vocabulary.TurnNarrationRef, testTurnID)] =
		testNarration("turn-act-9", vocabulary.BandPartial)

	_, err := h.results.ByTurn(t.Context(), testTurnID)
	if err == nil {
		t.Fatal("a result was composed carrying another turn's prose")
	}
	if !strings.Contains(err.Error(), "turn-act-9") {
		t.Errorf("the failure %q does not name the prose's real turn", err)
	}
}

// ---------------------------------------------------------------- latest

func TestLatest_AnswersWithTheTurnThatEndedMostRecently(t *testing.T) {
	h := newHarness(t)
	earlier := h.completedTurn(t, "turn-act-1", testPlayerID, testTime)
	later := h.completedTurn(t, "turn-act-2", testPlayerID, testTime.Add(time.Hour))
	h.graph.player(t, testPlayerID, later, later)

	delivery, err := h.results.Latest(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if delivery.Result.TurnID != "turn-act-2" {
		t.Fatalf("Latest answered with %q, want the turn that ended most recently (earlier was %s)",
			delivery.Result.TurnID, earlier.ID)
	}
}

// The seam this pointer exists for. While a turn is LIVE, player.turn.current
// names it and nothing else in the graph names the terminal one before it — so a
// surface reading only that pointer would answer a player mid-turn with silence.
func TestLatest_AnswersWhileTheNextTurnIsStillRunning(t *testing.T) {
	h := newHarness(t)
	finished := h.completedTurn(t, "turn-act-1", testPlayerID, testTime)
	running := newTurn(t, "turn-act-2").
		accepted(testPlayerID).
		phase(vocabulary.PhaseAdjudicating, testTime.Add(time.Hour)).
		build()
	h.graph.putTurn(running)
	h.graph.player(t, testPlayerID, running, finished)

	delivery, err := h.results.Latest(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("Latest while a turn runs: %v", err)
	}
	if delivery.Result.TurnID != "turn-act-1" {
		t.Fatalf("Latest answered with %q, want the last turn that ENDED", delivery.Result.TurnID)
	}
}

// The crash gap the recorder's write order produces: the phase landed, the
// resolved pointer did not. player.turn.current still names the turn that just
// ended, which is why reading BOTH pointers is what makes the ordering safe.
func TestLatest_CoversTheGapWhereTheResolvedPointerHasNotCaughtUp(t *testing.T) {
	h := newHarness(t)
	older := h.completedTurn(t, "turn-act-1", testPlayerID, testTime)
	newest := h.completedTurn(t, "turn-act-2", testPlayerID, testTime.Add(time.Hour))
	// current names the turn that just ended; resolved is still one behind.
	h.graph.player(t, testPlayerID, newest, older)

	delivery, err := h.results.Latest(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if delivery.Result.TurnID != "turn-act-2" {
		t.Fatalf("Latest answered with %q; the current pointer named the turn that just ended and the "+
			"resolved pointer had not caught up, which is exactly the gap both pointers are read to cover",
			delivery.Result.TurnID)
	}
}

// The scenario the spec names: a failed turn must win over the previous success.
func TestLatest_AFailedTurnAnswersRatherThanThePreviousSuccess(t *testing.T) {
	h := newHarness(t)
	succeeded := h.completedTurn(t, "turn-act-1", testPlayerID, testTime)
	failed := newTurn(t, "turn-act-2").
		accepted(testPlayerID).
		failed(vocabulary.FailureTurnStranded, testTime.Add(time.Hour)).
		build()
	h.graph.putTurn(failed)
	h.graph.player(t, testPlayerID, failed, failed)

	delivery, err := h.results.Latest(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if delivery.Result.TurnID != "turn-act-2" {
		t.Fatalf("Latest answered with %q (the earlier success was %s); the player who most needs an answer "+
			"is the one whose turn failed", delivery.Result.TurnID, succeeded.ID)
	}
	if delivery.Result.Phase != vocabulary.PhaseFailed {
		t.Fatalf("phase = %q, want failed", delivery.Result.Phase)
	}
}

func TestLatest_APlayerWhoHasFinishedNoTurnIsToldSo(t *testing.T) {
	h := newHarness(t)
	running := newTurn(t, testTurnID).
		accepted(testPlayerID).
		phase(vocabulary.PhaseAdjudicating, testTime).
		build()
	h.graph.putTurn(running)
	h.graph.player(t, testPlayerID, running, nil)

	_, err := h.results.Latest(t.Context(), testPlayerID)
	if !errors.Is(err, egress.ErrNoResult) {
		t.Fatalf("Latest for a player mid-first-turn = %v, want ErrNoResult", err)
	}
}

// A pointer at a turn nothing created must not deny a player the answer their
// other pointer holds. Every degenerate reading falls back rather than refusing.
func TestLatest_SurvivesAPointerAtATurnThatDoesNotExist(t *testing.T) {
	h := newHarness(t)
	finished := h.completedTurn(t, "turn-act-1", testPlayerID, testTime)
	ghost := newTurn(t, "turn-act-ghost").accepted(testPlayerID).build()

	h.graph.player(t, testPlayerID, ghost, finished) // ghost is never stored

	delivery, err := h.results.Latest(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("Latest with a dangling pointer: %v", err)
	}
	if delivery.Result.TurnID != "turn-act-1" {
		t.Fatalf("Latest answered with %q, want the turn that is actually there", delivery.Result.TurnID)
	}
}

// The bound. An append-lane write on a player entity must degrade into a slightly
// stale answer, never into a scan of that player's whole history on every request.
func TestLatest_ReadsAtMostTheBoundedCandidateSet(t *testing.T) {
	h := newHarness(t)
	h.graph.player(t, testPlayerID, nil, nil)
	player := h.graph.entities[testPlayerID]
	for i := range egress.MaxLatestCandidates + 12 {
		turnID := "turn-act-flood" + string(rune('a'+i))
		// FAILED turns, so every candidate composes a real result. Turns that
		// could not compose would make the loop bail on the first one and the
		// bound below would hold for the wrong reason — which is exactly what an
		// earlier version of this test did, and it survived removing the bound.
		state := newTurn(t, turnID).
			accepted(testPlayerID).
			failed(vocabulary.FailureTurnStranded, testTime.Add(time.Duration(i)*time.Minute)).
			build()
		h.graph.putTurn(state)
		player.Triples = append(player.Triples, message.Triple{
			Subject:   testPlayerID,
			Predicate: vocabulary.PlayerTurnResolved.String(),
			Object:    state.ID,
			Source:    "append-lane",
			Timestamp: testTime,
		})
	}

	h.graph.reads = nil
	if _, err := h.results.Latest(t.Context(), testPlayerID); err != nil {
		t.Fatalf("Latest over a flooded pointer must still ANSWER, not refuse: %v", err)
	}

	turnReads := 0
	for _, id := range h.graph.reads {
		if id != testPlayerID {
			turnReads++
		}
	}
	if turnReads > egress.MaxLatestCandidates {
		t.Fatalf("Latest read %d turns, want at most %d; a corrupted pointer must not turn every retrieval "+
			"into a history scan", turnReads, egress.MaxLatestCandidates)
	}
	if turnReads == 0 {
		t.Fatal("Latest read no turns at all; the bound assertion above would pass for a surface that " +
			"answered nothing")
	}
}

// A single-valued predicate holding two values is an append-lane write, and the
// composition must say so rather than report whichever value it happened to read.
func TestByTurn_RefusesATurnHoldingTwoValuesForASingleValuedFact(t *testing.T) {
	h := newHarness(t)
	state := h.completedTurn(t, testTurnID, testPlayerID, testTime)
	state.Triples = append(state.Triples, message.Triple{
		Subject:   state.ID,
		Predicate: vocabulary.TurnRollBand.String(),
		Object:    string(vocabulary.BandFull),
		Source:    "append-lane",
		Timestamp: testTime,
	})

	_, err := h.results.ByTurn(t.Context(), testTurnID)
	if err == nil {
		t.Fatal("a turn holding two bands composed a result; the player would be shown a coin flip")
	}
	if !strings.Contains(err.Error(), vocabulary.TurnRollBand.String()) {
		t.Errorf("the failure %q does not name the doubled fact", err)
	}
}

// ---------------------------------------------------------------- construction

func TestNewResults_RefusesAHalfWiredSurface(t *testing.T) {
	h := newHarness(t)
	tests := map[string]func() error{
		"no graph": func() error {
			_, err := egress.NewResults(nil, h.artifacts, testIdentity(), testCampaignID)
			return err
		},
		"no artifact reader": func() error {
			_, err := egress.NewResults(h.graph, nil, testIdentity(), testCampaignID)
			return err
		},
		"no turn identity": func() error {
			_, err := egress.NewResults(h.graph, h.artifacts, turnIdentityZero(), testCampaignID)
			return err
		},
		"campaign is not an entity id": func() error {
			_, err := egress.NewResults(h.graph, h.artifacts, testIdentity(), "main")
			return err
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			if err := build(); err == nil {
				t.Fatal("a half-wired result surface was accepted")
			}
		})
	}
}

func turnIdentityZero() turn.Identity { return turn.Identity{} }
