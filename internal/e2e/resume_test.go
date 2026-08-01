package e2e_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// crashPoint is one place a process can die mid-turn, named by the lane whose
// deliveries stop.
type crashPoint struct {
	// Name is the subtest name.
	Name string
	// WorldNS isolates the case: its own campaign, its own copy of the world.
	WorldNS string
	// Stream and Consumer are the lane paused to make the gap.
	Stream   string
	Consumer func(t *testing.T) string
	// Parked is the phase the turn is left in when the lane stops.
	Parked vocabulary.TurnPhase
	// Landed is the artifact that must already be on the turn at the crash —
	// what the dead process finished — and Missing is the one that must not be,
	// which is what makes the gap the one this case names.
	Landed  []vocabulary.Predicate
	Missing []vocabulary.Predicate
}

// A crash in every lane of the turn loop, and a second boot that finishes the
// turn without buying a single extra model call.
//
// # The gap is made, not caught
//
// Polling for a fact and then stopping the engine is a race, and its loser is
// silent: the kill lands after the next stage already ran, every assertion still
// passes, and the test proves a weaker thing than its name. So each case PAUSES
// the lane it wants to die in. A paused consumer is indistinguishable from a dead
// process to everything upstream of it — the trigger is published, the stream
// captures it, nothing consumes it — and the state the crash lands in is then
// asserted rather than assumed.
//
// The case that matters most is `applying`: the roll is recorded and the effects
// are not, which is the window where a resumed turn could re-roll (it must not:
// the dice re-derive) or double-apply (it must not: the batch is keyed on the
// turn).
//
// # What "resumed" has to mean
//
// Not "the turn eventually completed" — a re-triggered stage that re-ran its
// persona would also complete, and would have cost a second billed call for a
// judgment the engine already had. So the call budget is the assertion: one
// adjudication and one narration across BOTH processes, enforced by scripts that
// hold exactly one step each. A second call runs off the end and is refused.
func TestE2E_ACrashInEachStageLaneIsResumedByTheNextBoot(t *testing.T) {
	stageConsumer := func(phase vocabulary.TurnPhase) func(*testing.T) string {
		return func(*testing.T) string { return rulepack.StageConsumerName(phase) }
	}

	for _, point := range []crashPoint{
		{
			Name:     "before interpretation is triggered",
			WorldNS:  "e2ecrash1",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseInterpreting),
			Parked:   vocabulary.PhaseAccepted,
			Landed:   []vocabulary.Predicate{vocabulary.TurnActionRef},
			Missing:  []vocabulary.Predicate{vocabulary.TurnCaseDecisionRef},
		},
		{
			Name:     "after the case decision and before adjudication is triggered",
			WorldNS:  "e2ecrash2",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseAdjudicating),
			Parked:   vocabulary.PhaseInterpreting,
			Landed: []vocabulary.Predicate{
				vocabulary.TurnActionRef,
				vocabulary.TurnCaseDecisionRef,
			},
			Missing: []vocabulary.Predicate{vocabulary.TurnVerdictRef},
		},
		{
			// The gap internal/resume exists for: the stage acknowledged its
			// trigger when it PUBLISHED the persona task, so the only durable
			// record that work is owed is the unacknowledged task itself.
			Name:     "after the persona task is published and before its artifact lands",
			WorldNS:  "e2ecrash3",
			Stream:   persona.TaskStream,
			Consumer: func(t *testing.T) string { return consumerNameFor(t, persona.TaskStream, persona.TaskSubjectFilter) },
			Parked:   vocabulary.PhaseAdjudicating,
			Landed:   []vocabulary.Predicate{vocabulary.TurnActionRef},
			Missing:  []vocabulary.Predicate{vocabulary.TurnVerdictRef},
		},
		{
			Name:     "after the verdict and before the dice",
			WorldNS:  "e2ecrash4",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseResolving),
			Parked:   vocabulary.PhaseAdjudicating,
			Landed:   []vocabulary.Predicate{vocabulary.TurnVerdictRef},
			Missing:  []vocabulary.Predicate{vocabulary.TurnRollBand},
		},
		{
			// The one the task names as the minimum, and the sharpest: the roll is
			// a fact and the world has not moved.
			Name:     "between the roll and the effects",
			WorldNS:  "e2ecrash5",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseApplying),
			Parked:   vocabulary.PhaseResolving,
			Landed:   []vocabulary.Predicate{vocabulary.TurnVerdictRef, vocabulary.TurnRollBand},
			Missing:  []vocabulary.Predicate{vocabulary.TurnEffectsBatch},
		},
		{
			Name:     "after the effects and before the companion stage",
			WorldNS:  "e2ecrash6",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseCompanion),
			Parked:   vocabulary.PhaseApplying,
			Landed:   []vocabulary.Predicate{vocabulary.TurnEffectsBatch},
			Missing:  []vocabulary.Predicate{vocabulary.TurnCompanionStageRef},
		},
		{
			Name:     "after the companion stage and before the narrator",
			WorldNS:  "e2ecrash7",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseNarrating),
			Parked:   vocabulary.PhaseCompanion,
			Landed:   []vocabulary.Predicate{vocabulary.TurnCompanionStageRef},
			Missing:  []vocabulary.Predicate{vocabulary.TurnNarrationRef},
		},
		{
			Name:     "after the narration and before the turn closes",
			WorldNS:  "e2ecrash8",
			Stream:   rulepack.StageStream,
			Consumer: stageConsumer(vocabulary.PhaseComplete),
			Parked:   vocabulary.PhaseNarrating,
			Landed:   []vocabulary.Predicate{vocabulary.TurnNarrationRef},
			Missing:  nil,
		},
	} {
		t.Run(point.Name, func(t *testing.T) {
			w := newWorld(t, point.WorldNS, "crash-resume")

			// The lane stops BEFORE the action is submitted, so nothing races the
			// pause. The consumer has to exist to be paused, which is why the world
			// is booted first.
			resumeLane := pauseConsumer(t, point.Stream, point.Consumer(t))

			player := w.dial(t)
			response := player.submit(t, "crash-1", "I put the crowbar under the winch and lift.")
			if response.Status != payload.StatusAccepted {
				t.Fatalf("the engine refused the submission: %+v", response.Refusal)
			}
			turnEntityID := w.turnEntity(t, response.TurnID)

			// The work is durably owed to the lane nobody is reading. This is the
			// crash: from here the process could die and the substrate still knows.
			requireQueued(t, point.Stream, point.Consumer(t), 30*time.Second)

			// Wait for everything that was supposed to finish, then assert what was
			// not supposed to start. Without the first half the second is vacuous —
			// a turn that had done nothing yet would satisfy every Missing check.
			for _, predicate := range point.Landed {
				awaitFact(t, turnEntityID, predicate, 30*time.Second)
			}
			for _, predicate := range point.Missing {
				requireAbsent(t, turnEntityID, predicate,
					"this crash is supposed to land BEFORE that artifact, so the kill point is not the one "+
						"this case names")
			}
			if phase := phaseOf(t, turnEntityID); phase != point.Parked {
				t.Fatalf("the turn is parked in %q, want %q", phase, point.Parked)
			}

			// The process dies.
			w.crash()

			// ...and comes back. The lane is released first so the second boot has
			// the same substrate a real restart would: a queue holding work, and
			// nothing else different.
			resumeLane()
			w.boot(t)

			if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
				t.Fatalf("after the restart the turn ended in %q (failure reason %q); a crash mid-turn must be "+
					"resumed, not abandoned", phase,
					stringObject(t, entityState(t, turnEntityID), vocabulary.TurnFailureReason))
			}

			// Nothing was re-billed. This is the whole claim: the boot-time
			// stranded-turn pass left work the substrate still owned alone, and the
			// stage guards declined every duplicate they were handed.
			requireCallBudget(t, w, 1, 1)

			// The world moved once, and the archive holds the turn once.
			state := entityState(t, turnEntityID)
			if stringObject(t, state, vocabulary.TurnEffectsBatch) == "" {
				t.Error("the resumed turn completed with no effect-batch marker; the narration hop is gated on " +
					"that marker, so a completed turn without one means the gate is not doing its job")
			}
			band := vocabulary.OutcomeBand(stringObject(t, state, vocabulary.TurnRollBand))
			requireCrashResumeBandCommitted(t, w, band)

			// Turn completion and archive append are two different consumers of
			// the resolved notification. The graph may become terminal before the
			// ledger consumer has appended its manifest, so wait on the archive's
			// authoritative subject before asserting its cardinality.
			manifest := awaitManifest(t, response.TurnID)
			if count := manifestsFor(t, response.TurnID); count != 1 {
				t.Errorf("the archive holds %d manifests for turn %s, want exactly 1", count, response.TurnID)
			}

			// And the replay reader agrees the recorded roll is still the roll its
			// own inputs produce. A resumed turn that had re-rolled would fail here
			// rather than merely look complete.
			replay, err := w.replayReader(t).Replay(t.Context(), manifest)
			if err != nil {
				t.Fatalf("replay the resumed turn: %v", err)
			}
			if replay.Roll == nil || replay.Reproduced == nil {
				t.Fatalf("the replay of a rolled turn reproduced nothing: %+v", replay)
			}

			requireNothingQueuedFor(t, turnEntityID)
		})
	}
}

// A stage trigger delivered TWICE for a stage that already finished must not buy
// a second billed call.
//
// # Why this is not covered by the crash cases above
//
// Found by mutation, which is the only way it would have been: the persona resume
// guard was changed to always answer RUN, and every crash case above still passed.
// They pause a lane, so the trigger they park is delivered exactly once — the
// guard is never asked a question it could get wrong. What exercises it is a
// SECOND trigger arriving for a stage whose artifact is already on the turn, which
// is what a rule that re-fires, a redelivered trigger, or a re-triggering
// reconciliation pass all produce.
//
// The state is arranged rather than waited for: pausing the dice lane leaves the
// turn in `adjudicating` WITH its verdict, which is exactly the shape the guard
// exists to answer about — the phase is written on stage ENTRY and cannot
// distinguish "entered" from "finished", and the artifact can.
func TestE2E_ADuplicateStageTriggerForAFinishedStageBuysNoSecondCall(t *testing.T) {
	w := newWorld(t, "e2eguard", "crash-resume")

	// The dice lane stops, so the turn holds still in `adjudicating` with the
	// adjudicator's artifact already landed.
	release := pauseConsumer(t, rulepack.StageStream, rulepack.StageConsumerName(vocabulary.PhaseResolving))

	player := w.dial(t)
	response := player.submit(t, "guard-1", "I put the crowbar under the winch and lift.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)

	awaitFact(t, turnEntityID, vocabulary.TurnVerdictRef, 30*time.Second)
	if phase := phaseOf(t, turnEntityID); phase != vocabulary.PhaseAdjudicating {
		t.Fatalf("the turn is in %q, want %q; this test needs a stage that has FINISHED and a turn still "+
			"sitting in its phase", phase, vocabulary.PhaseAdjudicating)
	}

	// The same trigger again, byte for byte, as a redelivery or a re-firing rule
	// would produce it.
	republishStageTrigger(t, vocabulary.PhaseAdjudicating, turnEntityID)

	release()
	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
		t.Fatalf("the turn ended in %q (failure reason %q)", phase,
			stringObject(t, entityState(t, turnEntityID), vocabulary.TurnFailureReason))
	}

	// One adjudication, not two. The duplicate found the artifact already on the
	// turn and cost nothing.
	requireCallBudget(t, w, 1, 1)
	requireNothingQueuedFor(t, turnEntityID)
}

// republishStageTrigger puts one phase's trigger for a turn back on the stream and
// waits for the stage to have finished with it.
//
// The BYTES the rule engine published, re-published: a hand-built trigger would be
// a message production never produces, and the waiting half is what makes the
// assertion that follows about a duplicate that was PROCESSED rather than one that
// is still in flight.
func republishStageTrigger(t *testing.T, phase vocabulary.TurnPhase, turnEntityID string) {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("subject for %s: %v", phase, err)
	}
	stream, err := jetStream(t).Stream(t.Context(), rulepack.StageStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", rulepack.StageStream, err)
	}

	reader, err := stream.CreateConsumer(t.Context(), jetstream.ConsumerConfig{
		FilterSubject:     subject,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("read the %s lane: %v", subject, err)
	}
	batch, err := reader.Fetch(4096, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch the %s lane: %v", subject, err)
	}
	var original []byte
	for msg := range batch.Messages() {
		trigger, parseErr := stage.ParseTrigger(msg.Data())
		if parseErr != nil || trigger.TurnEntityID != turnEntityID {
			continue
		}
		original = msg.Data()
	}
	if original == nil {
		t.Fatalf("no %s trigger names %s, so there is nothing to duplicate", subject, turnEntityID)
	}
	if err := requireBroker(t).Client.PublishToStream(t.Context(), subject, original); err != nil {
		t.Fatalf("republish the %s trigger: %v", subject, err)
	}

	// Wait until the stage has finished with the duplicate, so a call counted
	// afterwards is a call it did or did not make rather than one still coming.
	raw, err := stream.GetLastMsgForSubject(t.Context(), subject)
	if err != nil {
		t.Fatalf("read back the republished trigger: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		consumer, err := jetStream(t).Consumer(t.Context(), rulepack.StageStream,
			rulepack.StageConsumerName(phase))
		if err != nil {
			t.Fatalf("read the %s stage consumer: %v", phase, err)
		}
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the %s stage consumer info: %v", phase, err)
		}
		if info.AckFloor.Stream >= raw.Sequence {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s stage has not finished with the duplicate trigger at sequence %d (floor %d)",
				phase, raw.Sequence, info.AckFloor.Stream)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// requireCrashResumeBandCommitted asserts the world change the crash-resume
// scenario's verdict declared for the band the dice actually chose.
//
// Band-agnostic by necessity — this scenario runs on a freshly minted campaign
// seed, so which band it lands on is not knowable in advance — and specific
// anyway, because "the effect-batch marker exists" would be satisfied by a batch
// that committed nothing.
func requireCrashResumeBandCommitted(t *testing.T, w *world, band vocabulary.OutcomeBand) {
	t.Helper()
	rook := entityState(t, w.entity("character", starterCharacter))
	hollis := entityState(t, w.entity("character", starterSentry))

	switch band {
	case vocabulary.BandMiss:
		if got := stringObject(t, rook, vocabulary.CharacterStatusCurrent); got !=
			string(vocabulary.StatusRestrained) {
			t.Errorf("the turn landed on %q and Rook's status is %q, want %q",
				band, got, vocabulary.StatusRestrained)
		}
	case vocabulary.BandPartial:
		if got := stringObject(t, rook, vocabulary.CharacterAttributeStamina); got != "4" {
			t.Errorf("the turn landed on %q and Rook's stamina is %s, want 4", band, got)
		}
	case vocabulary.BandFull:
		if got := objectsFor(hollis, vocabulary.WorldRelationHostileTo); len(got) != 0 {
			t.Errorf("the turn landed on %q and Hollis is still hostile to %v", band, got)
		}
	default:
		t.Fatalf("the turn recorded band %q, which the dice cannot select", band)
	}
}
