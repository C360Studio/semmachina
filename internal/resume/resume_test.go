package resume_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/resume"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testCampaignID = "c360.semmachina.riverbend.starter.campaign.instance"
	testTurnPrefix = "c360.semmachina.riverbend.starter.turn"
)

func turnEntityID(turnID string) string { return testTurnPrefix + "." + turnID }

// turnEntity builds a turn as the graph returns one: numbers arrive from a JSON
// round trip as float64, which is the shape the attempt counter has to survive.
func turnEntity(turnID string, facts map[vocabulary.Predicate]any) graph.EntityState {
	id := turnEntityID(turnID)
	triples := make([]message.Triple, 0, len(facts))
	for predicate, object := range facts {
		triples = append(triples, message.Triple{
			Subject: id, Predicate: predicate.String(), Object: object,
			Source: "test", Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
		})
	}
	return graph.EntityState{
		ID: id,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryTurnState, Version: payload.SchemaVersion,
		},
		Version: 1, Triples: triples,
	}
}

func parked(turnID string, phase vocabulary.TurnPhase) graph.EntityState {
	return turnEntity(turnID, map[vocabulary.Predicate]any{
		vocabulary.TurnPhaseCurrent: string(phase),
		// Always present, because the birth record always is. A pass that read
		// one of these as an artifact would call every parked turn finished.
		vocabulary.TurnActionPlayer: "c360.semmachina.riverbend.starter.player.one",
		vocabulary.TurnActionScene:  "c360.semmachina.riverbend.starter.scene.gatehouse",
	})
}

// --- fakes ------------------------------------------------------------------

type merge struct {
	entityID string
	triples  []message.Triple
}

type fakeTurns struct {
	pages   []graphio.PrefixPage
	page    int
	prefix  string
	merges  []merge
	listErr error
	mergeFn func(entityID string) error
}

func (f *fakeTurns) EntitiesWithPrefix(
	_ context.Context, prefix, _ string, _ int,
) (graphio.PrefixPage, error) {
	f.prefix = prefix
	if f.listErr != nil {
		return graphio.PrefixPage{}, f.listErr
	}
	if f.page >= len(f.pages) {
		return graphio.PrefixPage{}, nil
	}
	page := f.pages[f.page]
	f.page++
	return page, nil
}

func (f *fakeTurns) MergeTriples(
	_ context.Context, entityID string, triples []message.Triple, _ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	if f.mergeFn != nil {
		if err := f.mergeFn(entityID); err != nil {
			return nil, err
		}
	}
	f.merges = append(f.merges, merge{entityID: entityID, triples: triples})
	return &graph.EntityState{ID: entityID}, nil
}

type published struct {
	subject  string
	entityID string
}

type fakePublisher struct {
	sent []published
	err  error
	// order records every write and publish across both fakes, so the ordering
	// between counting an attempt and making it is assertable rather than
	// assumed.
	order *[]string
}

func (f *fakePublisher) PublishToStream(_ context.Context, subject string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	var body struct {
		EntityID string `json:"entity_id"`
		Subject  string `json:"subject"`
		Source   string `json:"source"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return err
	}
	if body.Subject != subject {
		return fmt.Errorf("payload names subject %q, published on %q", body.Subject, subject)
	}
	if body.Source != resume.Source {
		return fmt.Errorf("payload source %q, want %q", body.Source, resume.Source)
	}
	f.sent = append(f.sent, published{subject: subject, entityID: body.EntityID})
	if f.order != nil {
		*f.order = append(*f.order, "publish:"+body.EntityID)
	}
	return nil
}

type recordedFailure struct {
	turnID       string
	turnEntityID string
	reason       vocabulary.FailureReason
	detail       content.Ref
}

type fakeFailer struct {
	failures []recordedFailure
	err      error
}

func (f *fakeFailer) Fail(
	_ context.Context, turnID, turnEntityID string,
	reason vocabulary.FailureReason, detail content.Ref,
) (turn.Transition, error) {
	f.failures = append(f.failures, recordedFailure{turnID, turnEntityID, reason, detail})
	if f.err != nil {
		return turn.Transition{}, f.err
	}
	return turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}, nil
}

type fakeDetails struct {
	stored []*content.FailureDetail
	err    error
}

func (f *fakeDetails) PutFailureDetail(
	_ context.Context, _ string, detail *content.FailureDetail,
) (content.Ref, error) {
	f.stored = append(f.stored, detail)
	if f.err != nil {
		return content.Ref{}, f.err
	}
	return content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + detail.TurnID + "/failure"}, nil
}

// fakeQueued stands in for the measured stage stream.
type fakeQueued struct {
	pending    map[string]int
	pendingErr error
	settleErr  error
	settled    int
	reads      int
}

func (f *fakeQueued) Settle(context.Context) error {
	f.settled++
	return f.settleErr
}

func (f *fakeQueued) Pending(context.Context) (map[string]int, error) {
	f.reads++
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}
	return f.pending, nil
}

type harness struct {
	turns     *fakeTurns
	publisher *fakePublisher
	failer    *fakeFailer
	details   *fakeDetails
	queued    *fakeQueued
	order     []string
	pass      *resume.Reconciler
}

func newHarness(t *testing.T, entities []graph.EntityState, opts ...resume.Option) *harness {
	t.Helper()
	h := &harness{
		turns:   &fakeTurns{pages: []graphio.PrefixPage{{Entities: entities}}},
		failer:  &fakeFailer{},
		details: &fakeDetails{},
		queued:  &fakeQueued{pending: map[string]int{}},
	}
	h.publisher = &fakePublisher{order: &h.order}
	h.turns.mergeFn = func(entityID string) error {
		h.order = append(h.order, "count:"+entityID)
		return nil
	}

	pass, err := resume.NewReconciler(
		h.turns, h.publisher, h.failer, h.details, h.queued, testCampaignID,
		append([]resume.Option{resume.WithClock(func() time.Time {
			return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		})}, opts...)...)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	h.pass = pass
	return h
}

func subjectFor(t *testing.T, phase vocabulary.TurnPhase) string {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("SubjectForPhase(%s): %v", phase, err)
	}
	return subject
}

// --- tests ------------------------------------------------------------------

// A turn the substrate is already holding a trigger for must be left entirely
// alone. This is the MEASURED half of the design: the fact is read off the stage
// stream, not inferred from whether a rule would match.
//
// Re-triggering here is the defect this test holds shut, and it is not a small
// one: two deliveries on a persona hop are two Guard.Check calls, and where no
// artifact exists yet both correctly say run — two billed spawns, counted by
// neither this pass's budget nor the rule engine's max_iterations.
func TestReconcile_LeavesATurnWithAQueuedTriggerAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts map[vocabulary.Predicate]any
	}{
		{
			name: "mid-chain, artifact present",
			facts: map[vocabulary.Predicate]any{
				vocabulary.TurnPhaseCurrent:        string(vocabulary.PhaseAdjudicating),
				vocabulary.TurnVerdictRequiresRoll: true,
				vocabulary.TurnVerdictRef:          "obj://SEMMACHINA_CONTENT/turn/turn-act-1/verdict",
			},
		},
		{
			// The first hop, where a duplicate costs the most: no artifact
			// exists yet, so the resume guard cannot skip either delivery.
			name: "the first hop, with no artifact to skip on",
			facts: map[vocabulary.Predicate]any{
				vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseAccepted),
				vocabulary.TurnActionPlayer: "c360.semmachina.riverbend.starter.player.one",
				vocabulary.TurnActionScene:  "c360.semmachina.riverbend.starter.scene.gatehouse",
			},
		},
		{
			// Parked with NO artifact — the stranded shape — but a trigger is
			// queued for it anyway, because the stage crashed holding it. The
			// substrate will redeliver; this pass must not add a second.
			name: "parked mid-stage with a redelivery pending",
			facts: map[vocabulary.Predicate]any{
				vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseAdjudicating),
				vocabulary.TurnActionPlayer: "c360.semmachina.riverbend.starter.player.one",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entity := turnEntity("turn-act-1", tc.facts)
			h := newHarness(t, []graph.EntityState{entity})
			h.queued.pending[entity.ID] = 1

			report, err := h.pass.Reconcile(t.Context())
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if report.Queued != 1 || report.StageRetriggered != 0 || report.Abandoned != 0 {
				t.Fatalf("report = %+v; a turn with a queued trigger is counted, not acted on", report)
			}
			if len(h.publisher.sent) != 0 {
				t.Fatalf("the pass published %v on top of a trigger the substrate already holds",
					h.publisher.sent)
			}
			if len(h.turns.merges) != 0 {
				t.Fatalf("the pass wrote %d counters for a turn it did not act on", len(h.turns.merges))
			}
		})
	}
}

// The set is read ONCE, after the stream has settled, and every turn is judged
// against that one snapshot. Reading per turn would make each disposition depend
// on a different moment, and the moments this pass cares about are the ones where
// something changes.
func TestReconcile_SettlesTheStreamThenReadsTheQueuedSetOnce(t *testing.T) {
	h := newHarness(t, []graph.EntityState{
		parked("turn-act-1", vocabulary.PhaseAdjudicating),
		parked("turn-act-2", vocabulary.PhaseResolving),
	})

	if _, err := h.pass.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if h.queued.settled != 1 {
		t.Errorf("settled %d times, want exactly 1", h.queued.settled)
	}
	if h.queued.reads != 1 {
		t.Errorf("read the queued set %d times for 2 turns, want exactly 1 snapshot", h.queued.reads)
	}
}

// A stream that will not hold still means the pass has been run at the wrong
// point in the boot sequence. Acting on a moving target is how a turn about to
// receive a trigger gets re-triggered or ended.
func TestReconcile_RefusesToActOnAStreamThatWillNotSettle(t *testing.T) {
	h := newHarness(t, []graph.EntityState{parked("turn-act-1", vocabulary.PhaseAdjudicating)})
	h.queued.settleErr = errors.New("the stage stream is still being published to")

	report, err := h.pass.Reconcile(t.Context())
	if err == nil {
		t.Fatal("the pass acted on a stage stream that was still moving")
	}
	if report.Scanned != 0 || len(h.publisher.sent) != 0 || len(h.turns.merges) != 0 {
		t.Fatalf("the pass scanned %d turns and acted before the stream settled: %+v", report.Scanned, report)
	}
}

// A queued-set read that fails is not an empty queued set. Treating it as one
// would re-trigger every turn in the world on top of whatever is already coming.
func TestReconcile_RefusesToActOnAQueuedSetItCouldNotRead(t *testing.T) {
	h := newHarness(t, []graph.EntityState{parked("turn-act-1", vocabulary.PhaseAdjudicating)})
	h.queued.pendingErr = errors.New("the stage consumer could not be read")

	report, err := h.pass.Reconcile(t.Context())
	if err == nil {
		t.Fatal("an unreadable queued set was treated as an empty one")
	}
	if report.Scanned != 0 || len(h.publisher.sent) != 0 {
		t.Fatalf("the pass acted without knowing what was queued: %+v", report)
	}
}

// No rule matches: the stage never produced anything, so the turn is genuinely
// stranded and its OWN stage is re-triggered — this one can re-bill a persona,
// so it spends an attempt.
func TestReconcile_RetriggersAStrandedStageAndCountsTheAttempt(t *testing.T) {
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseAdjudicating,
		vocabulary.PhaseResolving,
		vocabulary.PhaseApplying,
		vocabulary.PhaseCompanion,
		vocabulary.PhaseNarrating,
	} {
		t.Run(string(phase), func(t *testing.T) {
			h := newHarness(t, []graph.EntityState{parked("turn-act-1", phase)})

			report, err := h.pass.Reconcile(t.Context())
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if report.StageRetriggered != 1 || report.Queued != 0 {
				t.Fatalf("report = %+v; a turn no rule matches is stranded, not stalled", report)
			}
			want := subjectFor(t, phase)
			if len(h.publisher.sent) != 1 || h.publisher.sent[0].subject != want {
				t.Fatalf("published %v, want the parked phase's own trigger %s", h.publisher.sent, want)
			}
			if len(h.turns.merges) != 1 {
				t.Fatalf("wrote %d attempt counters, want 1; an unbounded re-trigger is not a bound",
					len(h.turns.merges))
			}
			assertAttempts(t, h.turns.merges[0], 1)
		})
	}
}

// A turn still in `accepted` with nothing queued has never started: its first hop
// was published and lost, or never published at all. It is re-triggered into the
// FIRST stage — the one derivation this pass makes — and it spends an attempt
// like any other re-trigger, because it costs the same thing: the adjudicator
// spawn the turn was always owed.
//
// The alternative, which this replaced, was ending the turn. That killed a
// player's action over one failed publish when republishing simply works.
func TestReconcile_StartsATurnThatNeverStarted(t *testing.T) {
	entity := turnEntity("turn-act-1", map[vocabulary.Predicate]any{
		vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseAccepted),
		vocabulary.TurnActionPlayer: "c360.semmachina.riverbend.starter.player.one",
		vocabulary.TurnActionScene:  "c360.semmachina.riverbend.starter.scene.gatehouse",
	})
	h := newHarness(t, []graph.EntityState{entity}) // nothing queued

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.StageRetriggered != 1 || report.Abandoned != 0 {
		t.Fatalf("report = %+v; an accepted turn with no queued hop is started, not ended", report)
	}

	// The FIRST stage, which for this engine is adjudication. Naming the phase
	// rather than the literal subject is what makes this a test of the
	// derivation instead of a copy of it.
	want := subjectFor(t, rulepack.StagePhases()[0])
	if len(h.publisher.sent) != 1 || h.publisher.sent[0].subject != want {
		t.Fatalf("published %v, want one trigger on the first stage's subject %s", h.publisher.sent, want)
	}
	if len(h.turns.merges) != 1 {
		t.Fatalf("wrote %d attempt counters, want 1; a re-trigger that costs a persona spawn must be bounded "+
			"like every other", len(h.turns.merges))
	}
	assertAttempts(t, h.turns.merges[0], 1)
}

// And it is bounded: a first hop that keeps going missing ends the turn rather
// than buying an adjudicator on every boot forever.
func TestReconcile_BoundsStartingATurnThatNeverStarted(t *testing.T) {
	entity := turnEntity("turn-act-1", map[vocabulary.Predicate]any{
		vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseAccepted),
		vocabulary.TurnActionPlayer: "c360.semmachina.riverbend.starter.player.one",
		vocabulary.TurnActionScene:  "c360.semmachina.riverbend.starter.scene.gatehouse",
	})
	entity.Triples = append(entity.Triples, message.Triple{
		Subject: entity.ID, Predicate: vocabulary.TurnResumeAttempts.String(),
		Object: float64(2), Source: "turn-resume",
		Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
	})
	h := newHarness(t, []graph.EntityState{entity}, resume.WithMaxAttempts(2))

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Abandoned != 1 || report.StageRetriggered != 0 {
		t.Fatalf("report = %+v; an accepted turn whose budget is gone must end like any other", report)
	}
	if len(h.publisher.sent) != 0 {
		t.Fatalf("an abandoned turn was still started: %v", h.publisher.sent)
	}
}

// The count is read off the turn and incremented, not restarted. A pass that
// always wrote 1 would re-trigger forever.
func TestReconcile_ContinuesTheCountItFindsOnTheTurn(t *testing.T) {
	entity := parked("turn-act-1", vocabulary.PhaseAdjudicating)
	entity.Triples = append(entity.Triples, message.Triple{
		Subject: entity.ID, Predicate: vocabulary.TurnResumeAttempts.String(),
		// float64, as a JSON round trip through the graph delivers it.
		Object: float64(1), Source: "turn-resume",
		Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
	})
	h := newHarness(t, []graph.EntityState{entity}, resume.WithMaxAttempts(3))

	if _, err := h.pass.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(h.turns.merges) != 1 {
		t.Fatalf("wrote %d counters, want 1", len(h.turns.merges))
	}
	assertAttempts(t, h.turns.merges[0], 2)
}

// The attempt is COUNTED before it is MADE. The other order loses the count to a
// crash between the two, and a bound that can lose track of itself is not a
// bound.
func TestReconcile_CountsTheAttemptBeforeMakingIt(t *testing.T) {
	h := newHarness(t, []graph.EntityState{parked("turn-act-1", vocabulary.PhaseAdjudicating)})

	if _, err := h.pass.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	id := turnEntityID("turn-act-1")
	want := []string{"count:" + id, "publish:" + id}
	if strings.Join(h.order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v; a crash between the two must spend an attempt rather than repeat one",
			h.order, want)
	}
}

// The budget has an ending. A turn that has not produced its artifact across the
// whole budget is ended on the record rather than left waiting forever.
func TestReconcile_AbandonsATurnWhoseBudgetIsGone(t *testing.T) {
	entity := parked("turn-act-1", vocabulary.PhaseAdjudicating)
	entity.Triples = append(entity.Triples, message.Triple{
		Subject: entity.ID, Predicate: vocabulary.TurnResumeAttempts.String(),
		Object: float64(2), Source: "turn-resume",
		Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
	})
	h := newHarness(t, []graph.EntityState{entity}, resume.WithMaxAttempts(2))

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Abandoned != 1 || report.StageRetriggered != 0 {
		t.Fatalf("report = %+v; the budget was spent and the turn must end", report)
	}
	if len(h.publisher.sent) != 0 {
		t.Fatalf("an abandoned turn was still re-triggered: %v", h.publisher.sent)
	}
	if len(h.turns.merges) != 0 {
		t.Fatalf("an abandoned turn still spent an attempt: %d writes", len(h.turns.merges))
	}
	if len(h.failer.failures) != 1 {
		t.Fatalf("recorded %d failures, want 1", len(h.failer.failures))
	}
	failure := h.failer.failures[0]
	if failure.reason != vocabulary.FailureTurnStranded {
		t.Errorf("recorded reason %q, want %q", failure.reason, vocabulary.FailureTurnStranded)
	}
	if failure.detail.IsZero() {
		t.Error("the abandoned turn carries no explanation reference")
	}
	if len(h.details.stored) != 1 || h.details.stored[0].Reason != vocabulary.FailureTurnStranded {
		t.Errorf("stored detail = %+v, want one carrying the stranded code", h.details.stored)
	}
}

// The turn ENDS either way. A detail store that refuses does not buy back the
// wait this pass exists to end.
func TestReconcile_AbandonsTheTurnEvenWhenTheExplanationCannotBeStored(t *testing.T) {
	entity := parked("turn-act-1", vocabulary.PhaseAdjudicating)
	entity.Triples = append(entity.Triples, message.Triple{
		Subject: entity.ID, Predicate: vocabulary.TurnResumeAttempts.String(),
		Object: float64(2), Source: "turn-resume",
		Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
	})
	h := newHarness(t, []graph.EntityState{entity}, resume.WithMaxAttempts(2))
	h.details.err = errors.New("object store unreachable")

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Abandoned != 1 {
		t.Fatalf("report = %+v; a store fault left the turn waiting", report)
	}
	if len(h.failer.failures) != 1 {
		t.Fatalf("recorded %d failures, want 1", len(h.failer.failures))
	}
	if !h.failer.failures[0].detail.IsZero() {
		t.Error("the turn claims a detail reference the store never wrote")
	}
}

// A turn carrying its phase's artifact with nothing queued cannot be helped by
// re-running its own stage: the recorder reads the re-entry as a resume and the
// persona guard skips on the artifact, so nothing advances.
//
// It must NOT be ended on the first sighting, and that is a correctness
// requirement rather than caution. The queued set is one snapshot taken before
// the turn pages are read, so between the two a redelivered persona task can land
// its verdict and the pack can publish a hop — leaving a stale snapshot saying
// "nothing queued", a fresh page saying "artifact present", and a live trigger on
// its way. Ending there fails a turn seconds from resolving, terminally, and the
// trigger that arrives afterwards is declined.
//
// So the sighting is counted, at the cost of one graph write and no model call.
func TestReconcile_CountsAnUnadvanceableTurnBeforeEndingIt(t *testing.T) {
	entity := turnEntity("turn-act-1", map[vocabulary.Predicate]any{
		vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseNarrating),
		vocabulary.TurnNarrationRef: "obj://SEMMACHINA_CONTENT/turn/turn-act-1/narration",
	})
	h := newHarness(t, []graph.EntityState{entity}) // nothing queued

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Unadvanceable != 1 || report.Abandoned != 0 {
		t.Fatalf("report = %+v; the first sighting is counted, not acted on — the snapshot may be a moment old",
			report)
	}
	if len(h.failer.failures) != 0 {
		t.Fatalf("a turn was ended on one reading: %+v", h.failer.failures)
	}
	if len(h.publisher.sent) != 0 {
		t.Fatalf("the pass re-triggered a stage that would decline to act: %v", h.publisher.sent)
	}
	if len(h.turns.merges) != 1 {
		t.Fatalf("wrote %d counters, want 1; the sighting has to be recorded or the budget never advances",
			len(h.turns.merges))
	}
	assertAttempts(t, h.turns.merges[0], 1)
}

// And once it has been seen that way on every pass its budget allows, it ends —
// the player's wait is over, and the diagnosis surfaces twice, on the turn and in
// the report.
//
// This is the shape a rule that fired with a failed publish leaves behind, and it
// is why the pass measures messages rather than reading the pack: no reading of
// the pack distinguishes it from a hop still in flight.
func TestReconcile_EndsAnUnadvanceableTurnOnceItsBudgetIsGone(t *testing.T) {
	entity := turnEntity("turn-act-1", map[vocabulary.Predicate]any{
		vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseNarrating),
		vocabulary.TurnNarrationRef: "obj://SEMMACHINA_CONTENT/turn/turn-act-1/narration",
	})
	entity.Triples = append(entity.Triples, message.Triple{
		Subject: entity.ID, Predicate: vocabulary.TurnResumeAttempts.String(),
		Object: float64(2), Source: "turn-resume",
		Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
	})
	h := newHarness(t, []graph.EntityState{entity}, resume.WithMaxAttempts(2))

	report, err := h.pass.Reconcile(t.Context())
	if err == nil {
		t.Fatal("a turn the engine cannot advance at all was reported as a clean pass")
	}
	if report.Abandoned != 1 || report.Unadvanceable != 0 || report.StageRetriggered != 0 {
		t.Fatalf("report = %+v; the budget was gone and the turn must end", report)
	}
	if len(h.publisher.sent) != 0 {
		t.Fatalf("the pass re-triggered a stage that would decline to act: %v", h.publisher.sent)
	}
	if len(h.failer.failures) != 1 || h.failer.failures[0].reason != vocabulary.FailureTurnStranded {
		t.Fatalf("recorded failures = %+v, want one carrying the stranded code", h.failer.failures)
	}
	if len(h.details.stored) != 1 || !strings.Contains(h.details.stored[0].Message, "narration") {
		t.Fatalf("the stored explanation does not name the artifact that proves the stage finished: %+v",
			h.details.stored)
	}
	if len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err.Error(), "narration") {
		t.Fatalf("failures = %+v; an unexplainable engine state must reach a human as well as the turn",
			report.Failures)
	}
}

// A turn that has ended owes nobody a stage, and a stub is not yet a turn.
func TestReconcile_LeavesResolvedTurnsAndUnbornStubsAlone(t *testing.T) {
	stub := graph.EntityState{ID: turnEntityID("turn-act-stub"), MessageType: graph.StubMessageType}
	h := newHarness(t, []graph.EntityState{
		parked("turn-act-done", vocabulary.PhaseComplete),
		parked("turn-act-dead", vocabulary.PhaseFailed),
		stub,
	})

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Resolved != 2 || report.Unborn != 1 || report.Scanned != 3 {
		t.Fatalf("report = %+v; want 2 resolved, 1 unborn, 3 scanned", report)
	}
	if len(h.publisher.sent) != 0 || len(h.turns.merges) != 0 || len(h.failer.failures) != 0 {
		t.Fatal("the pass acted on a turn that owed nothing")
	}
}

// One corrupt turn record must not stop every turn behind it from being resumed.
// This pass exists because a turn can be left waiting; aborting halfway would
// leave a different set waiting for a different reason.
func TestReconcile_CollectsFailuresAndKeepsGoing(t *testing.T) {
	corrupt := parked("turn-act-1", vocabulary.PhaseAdjudicating)
	corrupt.Triples = append(corrupt.Triples, message.Triple{
		Subject: corrupt.ID, Predicate: vocabulary.TurnPhaseCurrent.String(),
		Object: string(vocabulary.PhaseNarrating), Source: "appending-lane",
		Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
	})
	h := newHarness(t, []graph.EntityState{corrupt, parked("turn-act-2", vocabulary.PhaseResolving)})

	report, err := h.pass.Reconcile(t.Context())
	if err == nil {
		t.Fatal("a turn holding two phases was accepted")
	}
	if len(report.Failures) != 1 {
		t.Fatalf("failures = %+v, want exactly the corrupt turn", report.Failures)
	}
	if report.StageRetriggered != 1 {
		t.Fatalf("report = %+v; the turn BEHIND the corrupt one was never resumed", report)
	}
}

// The scan follows the cursor. A pass that read one page and stopped would
// resume a prefix of the world and call it the whole thing.
func TestReconcile_FollowsThePagingCursor(t *testing.T) {
	h := newHarness(t, nil)
	h.turns.pages = []graphio.PrefixPage{
		{Entities: []graph.EntityState{parked("turn-act-1", vocabulary.PhaseAdjudicating)}, NextCursor: "p2"},
		{Entities: []graph.EntityState{parked("turn-act-2", vocabulary.PhaseResolving)}},
	}

	report, err := h.pass.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Scanned != 2 || report.StageRetriggered != 2 {
		t.Fatalf("report = %+v; the second page was never read", report)
	}
}

// A cursor that does not advance would spin forever against a live broker, and
// "the boot hung" is the worst diagnosis in the catalogue.
func TestReconcile_RefusesANonAdvancingCursor(t *testing.T) {
	h := newHarness(t, nil)
	h.turns.pages = []graphio.PrefixPage{
		{Entities: []graph.EntityState{parked("turn-act-1", vocabulary.PhaseAdjudicating)}, NextCursor: "same"},
		{Entities: []graph.EntityState{parked("turn-act-2", vocabulary.PhaseResolving)}, NextCursor: "same"},
	}

	if _, err := h.pass.Reconcile(t.Context()); err == nil {
		t.Fatal("a repeating cursor was followed rather than refused")
	}
}

// The scan is derived from the campaign entity, so one world's pass cannot reach
// another world's turns.
func TestReconcile_ScansOnlyItsOwnCampaignsTurns(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.pass.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if h.turns.prefix != testTurnPrefix {
		t.Fatalf("scanned prefix %q, want %q", h.turns.prefix, testTurnPrefix)
	}
}

// A counter holding two values is the signature of an appending-lane write, and
// a reader taking the first would be choosing which budget the turn is held to.
func TestReconcile_RefusesACounterWrittenOnAnAppendingLane(t *testing.T) {
	entity := parked("turn-act-1", vocabulary.PhaseAdjudicating)
	for _, value := range []float64{1, 2} {
		entity.Triples = append(entity.Triples, message.Triple{
			Subject: entity.ID, Predicate: vocabulary.TurnResumeAttempts.String(),
			Object: value, Source: "appending-lane",
			Timestamp: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Confidence: 1.0,
		})
	}
	h := newHarness(t, []graph.EntityState{entity})

	report, err := h.pass.Reconcile(t.Context())
	if err == nil {
		t.Fatal("a turn holding two attempt counts was accepted; the bound would be unenforceable")
	}
	if len(h.publisher.sent) != 0 {
		t.Fatal("a turn with an unreadable budget was re-triggered anyway")
	}
	if len(report.Failures) != 1 {
		t.Fatalf("failures = %+v, want one", report.Failures)
	}
}

func TestNewReconciler_RefusesAMissingDependencyOrABadCampaign(t *testing.T) {
	turns := &fakeTurns{}
	publisher := &fakePublisher{}
	failer := &fakeFailer{}
	details := &fakeDetails{}
	queued := &fakeQueued{}

	for _, tc := range []struct {
		name string
		call func() (*resume.Reconciler, error)
	}{
		{"no turn store", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(nil, publisher, failer, details, queued, testCampaignID)
		}},
		{"no publisher", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(turns, nil, failer, details, queued, testCampaignID)
		}},
		{"no failer", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(turns, publisher, nil, details, queued, testCampaignID)
		}},
		{"no detail store", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(turns, publisher, failer, nil, queued, testCampaignID)
		}},
		{"no queued-trigger view", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(turns, publisher, failer, details, nil, testCampaignID)
		}},
		{"a campaign id that is not an entity", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(turns, publisher, failer, details, queued, "riverbend")
		}},
		{"a zero attempt budget", func() (*resume.Reconciler, error) {
			return resume.NewReconciler(turns, publisher, failer, details, queued, testCampaignID,
				resume.WithMaxAttempts(0))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.call(); err == nil {
				t.Fatal("the pass was built without something it cannot work without")
			}
		})
	}
}

func assertAttempts(t *testing.T, written merge, want int) {
	t.Helper()
	var counted []any
	for _, triple := range written.triples {
		if triple.Predicate == vocabulary.TurnResumeAttempts.String() {
			counted = append(counted, triple.Object)
		}
	}
	if len(counted) != 1 {
		t.Fatalf("the attempt write carries %d counter triples, want exactly 1", len(counted))
	}
	if got, ok := counted[0].(int); !ok || got != want {
		t.Fatalf("recorded attempt %v (%T), want %d", counted[0], counted[0], want)
	}
	// A counter that travelled with the phase would make this pass a second
	// owner of the single-valued fact the turn recorder owns.
	for _, triple := range written.triples {
		if triple.Predicate == vocabulary.TurnPhaseCurrent.String() {
			t.Fatal("the attempt write also carries the turn's phase")
		}
	}
}
