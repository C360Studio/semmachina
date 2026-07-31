package resume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnTypeSegment is the turn entity's position in the six-part ID.
//
// Restated here rather than imported from internal/stage for the reason the
// ledger restates it: the dependency would drag the whole executing half of the
// turn loop — personas, the effect applier, the scene assembler — into a package
// whose entire job is to read turn entities and publish references. This pass
// must be unable to run a persona itself, and the cheapest way to be unable to
// is to not link one.
const TurnTypeSegment = "turn"

// Source names this pass as the producer of the triples and triggers it writes.
const Source = "turn-resume"

// MaxAttempts bounds how many times one turn may be re-triggered as stranded
// before it is failed on the record.
//
// TWO RE-TRIGGERS, which is THREE sightings: the pass acts on the first two
// times it finds the turn parked, and ends it on the third. Both halves of the
// number are a decision.
//
// Not one, because the first re-trigger can be interrupted by the very thing
// that stranded the turn. A process that crashes on boot strands its turns,
// re-triggers them next time, and crashes again before the stage runs; a budget
// of one would end a perfectly recoverable turn the second time a flapping
// deployment looked at it, which is the failure mode a bound is supposed to
// survive.
//
// Not more, because every re-trigger against a persona phase is a fresh billed
// spawn — the artifact is missing, so the resume guard has nothing to skip on
// and the model is called again. A stage that has produced no artifact after two
// re-triggers is not going to produce one on a third, and the pass that gives up
// has better information than the pass that tried first: it knows the earlier
// attempts did not work.
//
// The budget is per TURN and not per phase, deliberately. A turn that has had to
// be rescued twice in two different phases is a turn something is systematically
// wrong with, and the thing being bounded is the player's wait and the engine's
// spend, not fairness between stages.
const MaxAttempts = 2

// DefaultPageLimit is how many turn entities one page of the scan asks for.
// Zero would take the server's own default; asking explicitly makes the page
// size a decision rather than an inheritance.
const DefaultPageLimit = 200

// maxPages bounds the paging loop.
//
// A cursor that stops advancing would otherwise spin forever against a live
// broker, and "the boot hung" is the worst diagnosis in the catalogue. At the
// default page size this admits two hundred thousand turns before it refuses,
// which is far past anything an MVP campaign reaches and far short of forever.
const maxPages = 1000

// TurnStore is the graph surface this pass needs: one read that enumerates and
// one write that merges.
//
// The write lane is the entity MERGE lane and nothing else. The only predicate
// written here is a counter, and a counter committed through a triple-add lane
// APPENDS — leaving the turn holding two counts, both true, with a success
// response and no error, and a bound that has silently stopped bounding.
type TurnStore interface {
	EntitiesWithPrefix(ctx context.Context, prefix, cursor string, limit int) (graphio.PrefixPage, error)
	MergeTriples(
		ctx context.Context,
		entityID string,
		triples []message.Triple,
		opts ...graphio.MergeOption,
	) (*graph.EntityState, error)
}

// TriggerPublisher puts a stage trigger back on the stage stream.
//
// JetStream and not core NATS, and the difference is the whole point of
// publishing at all: a stage trigger is a request exactly one handler must work
// through, and a core publish issued at boot — before any stage consumer has
// bound — would be delivered to nobody and reported as a success. The JetStream
// publish waits for the server's acknowledgement, so a pass that runs before the
// stage stream exists is an error rather than a silent no-op.
type TriggerPublisher interface {
	PublishToStream(ctx context.Context, subject string, data []byte) error
}

// TurnFailer ends a turn explicitly, with a closed reason.
type TurnFailer interface {
	Fail(
		ctx context.Context,
		turnID, turnEntityID string,
		reason vocabulary.FailureReason,
		detail content.Ref,
	) (turn.Transition, error)
}

// DetailStore is the durable home of an abandoned turn's explanation.
type DetailStore interface {
	PutFailureDetail(ctx context.Context, turnEntityID string, detail *content.FailureDetail) (content.Ref, error)
}

// QueuedTriggers is the measured view of what the substrate is still holding.
//
// It replaces an inference, and the replacement is the whole design. The pass
// used to ask the rule pack whether a turn's hop had been published, and a rule
// pack cannot answer that: a rule fires and its publish can still fail — a
// circuit-broken client, a disconnected one, a refused PubAck, an action-level
// iteration cap — leaving a matching rule and no message. Every patch to the
// inference surfaced another route to the same gap. So the pass measures.
type QueuedTriggers interface {
	// Settle blocks until the stage stream has stopped moving, so the set below
	// is read as a fact rather than as a sample of something in flight.
	Settle(ctx context.Context) error
	// Pending returns, per turn entity ID, how many stage triggers are queued
	// for it and not yet acknowledged.
	Pending(ctx context.Context) (map[string]int, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ TurnStore        = (*graphio.Store)(nil)
	_ TurnFailer       = (*turn.Recorder)(nil)
	_ DetailStore      = (*content.Store)(nil)
	_ QueuedTriggers   = (*WorkQueues)(nil)
	_ TriggerPublisher = (*natsclient.Client)(nil)
)

// TurnFailure is one turn the pass could not put back in motion.
type TurnFailure struct {
	// TurnEntityID names the turn.
	TurnEntityID string
	// Err is why it could not be resumed.
	Err error
}

// Reconciliation is what one pass found and did.
type Reconciliation struct {
	// Scanned is how many turn entities were examined.
	Scanned int
	// Unborn is how many were referential stubs — queryable, factless, and not
	// yet a turn. Nothing has gone wrong when an entity is referenced before it
	// is born, and a stub has no phase to resume.
	Unborn int
	// Resolved is how many had already ended. They are not this pass's business:
	// a terminal turn owes nobody a stage.
	Resolved int
	// Queued is how many turns have a stage trigger the substrate is still
	// holding for them, and which the pass therefore LEFT ALONE.
	//
	// MEASURED, not inferred: read off the stage stream from each stage
	// consumer's acknowledgement floor. That distinction is the whole correction
	// this field carries. The pass used to decide the same thing by asking
	// whether a rule matched the turn, which answers a question about rules — and
	// a rule that fires can still fail to publish, so the two come apart exactly
	// where a turn goes quiet.
	//
	// Acting on this set would be the mirror mistake: re-triggering a stage that
	// already has a trigger coming means two deliveries, and on the first hop
	// two deliveries mean two Guard.Check calls that both correctly say run, and
	// two billed spawns.
	//
	// A non-zero count is ordinary on the boot after a crash. A count that stays
	// non-zero across consecutive boots is not: it means a stage consumer is not
	// draining what the pack published.
	Queued int
	// StageRetriggered is how many turns were genuinely stranded mid-stage and
	// had their own stage re-triggered, spending one attempt each.
	StageRetriggered int
	// Unadvanceable is how many turns were seen with nothing queued and no stage
	// that could help, and had the sighting COUNTED rather than acted on.
	//
	// Counting rather than ending is what makes the pass immune to its own
	// snapshot being a moment old: the queued set is read once, before the turn
	// pages, so a redelivered persona task can land a verdict and publish a hop
	// in between — and a turn ended on that reading would be a turn seconds from
	// resolving, failed terminally with its live trigger then declined. A turn
	// genuinely stuck is seen this way on every pass and ends when the budget is
	// gone; one caught mid-race has moved on by the next.
	Unadvanceable int
	// Abandoned is how many turns had exhausted their budget and were failed on
	// the record rather than left waiting.
	Abandoned int
	// Failures names every turn a human should look at, with the reason.
	//
	// It is not only the turns the pass could not act on. A turn ABANDONED
	// because it carries its stage's artifact and still has no trigger appears
	// here as well as in Abandoned: the player's wait is over either way, and the
	// engine has still reached a state it cannot explain — a stage finished and
	// the hop it should have produced does not exist. An ordinary budget
	// exhaustion is not listed, because that one is explained by its own record.
	Failures []TurnFailure
}

// Reconciler is the boot-time stranded-turn pass for one campaign.
type Reconciler struct {
	turns      TurnStore
	publisher  TriggerPublisher
	failer     TurnFailer
	details    DetailStore
	queued     QueuedTriggers
	turnPrefix string
	logger     *slog.Logger
	pageLimit  int
	attempts   int
	now        func() time.Time
}

// Option configures a Reconciler.
type Option func(*Reconciler)

// WithLogger sets the pass's logger.
func WithLogger(logger *slog.Logger) Option {
	return func(r *Reconciler) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// WithPageLimit overrides how many turn entities one page asks for. Tests use it
// to exercise the paging loop against a handful of turns.
func WithPageLimit(limit int) Option {
	return func(r *Reconciler) { r.pageLimit = limit }
}

// WithMaxAttempts overrides the per-turn re-trigger budget. Tests use it to
// reach the abandonment path without booting a world MaxAttempts+1 times.
func WithMaxAttempts(attempts int) Option {
	return func(r *Reconciler) { r.attempts = attempts }
}

// WithClock overrides the pass's clock, so a written triple's timestamp is an
// assertable value.
func WithClock(now func() time.Time) Option {
	return func(r *Reconciler) {
		if now != nil {
			r.now = now
		}
	}
}

// NewReconciler builds the stranded-turn pass for one campaign.
//
// The campaign id is configuration rather than something read off a turn, which
// is honest about the MVP: instance-per-world means one process serves one
// campaign, and the turn entity carries no campaign reference to read anyway. It
// is validated here so a misconfigured deployment fails at boot rather than
// silently scanning a prefix no turn lives under — which would report a clean
// pass over an empty set, the most convincing way to find nothing.
func NewReconciler(
	turns TurnStore,
	publisher TriggerPublisher,
	failer TurnFailer,
	details DetailStore,
	queued QueuedTriggers,
	campaignID string,
	opts ...Option,
) (*Reconciler, error) {
	switch {
	case turns == nil:
		return nil, errors.New("the stranded-turn pass requires a turn store")
	case publisher == nil:
		return nil, errors.New("the stranded-turn pass requires a trigger publisher")
	case failer == nil:
		return nil, errors.New("the stranded-turn pass requires a turn recorder")
	case details == nil:
		return nil, errors.New("the stranded-turn pass requires a detail store")
	case queued == nil:
		return nil, errors.New(
			"the stranded-turn pass requires a measured view of the queued stage triggers; without one it cannot " +
				"tell a turn the substrate is already carrying work for from a turn nothing will ever run, and " +
				"would spend a billed re-trigger on both")
	}
	if err := types.ValidateEntityID(campaignID); err != nil {
		return nil, fmt.Errorf("the stranded-turn pass requires a canonical campaign entity id: %w", err)
	}
	turnPrefix, err := vocabulary.SiblingTypePrefix(campaignID, TurnTypeSegment)
	if err != nil {
		return nil, err
	}

	reconciler := &Reconciler{
		turns: turns, publisher: publisher, failer: failer, details: details, queued: queued,
		turnPrefix: turnPrefix, logger: slog.Default(),
		pageLimit: DefaultPageLimit, attempts: MaxAttempts, now: time.Now,
	}
	for _, opt := range opts {
		opt(reconciler)
	}
	if reconciler.pageLimit <= 0 {
		return nil, errors.New("the stranded-turn pass requires a positive page limit")
	}
	if reconciler.attempts <= 0 {
		return nil, errors.New(
			"the stranded-turn pass requires a positive attempt budget; zero would abandon every stranded turn " +
				"on the first boot that noticed it")
	}
	return reconciler, nil
}

// Reconcile walks every turn in the world and puts the parked ones back in
// motion.
//
// # Where it goes in the boot sequence
//
// AFTER the stage-trigger stream exists, AFTER the rule processor has started,
// and BEFORE the player-action consumer binds. All three are load-bearing and
// only one of them can be enforced from here.
//
//   - The stream must exist because a JetStream publish into a subject no stream
//     captures is refused, so an early pass would report every turn as
//     unresumable. Mechanical, and loud when violated.
//   - The rule processor must have STARTED, because its bootstrap replay
//     publishes into the very set this pass reads, and whether it has finished
//     when Start returns is a race — measured both ways. A pass that read the
//     queued set before the processor was up would see an empty set for a turn
//     about to receive a trigger, and would then either re-trigger it or end it.
//     This one cannot be checked from here — an unstarted processor is
//     indistinguishable from a quiet one — so it is a composition constraint,
//     stated here and in the package doc.
//   - No player action may be accepted during the pass, or a turn born
//     mid-scan reads as stranded a millisecond after its creation.
//
// The gap between "started" and "finished replaying" IS enforced: Settle waits
// for the stage stream to stop moving before the queued set is read, so nobody
// has to pick a sleep.
//
// A per-turn failure is COLLECTED rather than returned, so one corrupt turn
// record does not stop every turn behind it from being resumed. That matters
// more here than anywhere else in the loop: this pass exists because a turn can
// be left waiting, and aborting halfway would leave a different set of turns
// waiting for a different reason.
func (r *Reconciler) Reconcile(ctx context.Context) (Reconciliation, error) {
	var (
		report Reconciliation
		cursor string
	)
	if err := r.queued.Settle(ctx); err != nil {
		return report, err
	}
	// ONE snapshot for the whole scan, taken after the stream went quiet. Reading
	// it per turn would make each turn's disposition depend on a different moment,
	// and the moments this pass cares about are the ones where something changes.
	queued, err := r.queued.Pending(ctx)
	if err != nil {
		return report, fmt.Errorf("read the queued stage triggers: %w", err)
	}
	for page := 0; ; page++ {
		if page >= maxPages {
			return report, fmt.Errorf(
				"the stranded-turn pass read %d pages of turns without exhausting the cursor; treating that as a "+
					"non-advancing cursor rather than hanging the boot", page)
		}
		result, err := r.turns.EntitiesWithPrefix(ctx, r.turnPrefix, cursor, r.pageLimit)
		if err != nil {
			return report, fmt.Errorf("enumerate turns under %s: %w", r.turnPrefix, err)
		}
		for idx := range result.Entities {
			r.reconcileTurn(ctx, &result.Entities[idx], queued, &report)
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor {
			return report, fmt.Errorf(
				"the turn enumeration returned the same cursor twice under %s; the scan is not advancing",
				r.turnPrefix)
		}
		cursor = result.NextCursor
	}

	if len(report.Failures) == 0 {
		return report, nil
	}
	errs := make([]error, 0, len(report.Failures))
	for _, failure := range report.Failures {
		errs = append(errs, fmt.Errorf("turn %s: %w", failure.TurnEntityID, failure.Err))
	}
	return report, errors.Join(errs...)
}

// reconcileTurn decides and acts on one enumerated turn.
//
// queued is the measured set of turns the substrate is still holding a stage
// trigger for. It is read once, before the scan, so every turn is judged against
// the same snapshot — and the snapshot is only taken once the stream has stopped
// moving.
func (r *Reconciler) reconcileTurn(
	ctx context.Context,
	state *graph.EntityState,
	queued map[string]int,
	report *Reconciliation,
) {
	report.Scanned++

	if state.IsStub() {
		report.Unborn++
		return
	}
	turnID, err := turnIDOf(state.ID)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	phase, err := recordedPhase(state)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	if phase.IsTerminal() {
		report.Resolved++
		return
	}

	if count := queued[state.ID]; count > 0 {
		// The substrate is holding work for this turn. Counted, not touched:
		// re-triggering on top of a queued trigger is two deliveries, and on a
		// persona hop two deliveries are two billed spawns.
		r.logger.Info("a turn already has a stage trigger queued for it; leaving it to the stage stream",
			"turn", state.ID, "phase", phase, "queued", count)
		report.Queued++
		return
	}

	// The subject is derived before anything is spent, so a phase this pass has
	// no move for costs no budget.
	subject, subjectErr := resumeSubject(phase)
	if subjectErr != nil {
		r.countTowardAbandonment(ctx, turnID, state, phase, fmt.Sprintf(
			"this pass has no stage to re-run for it: %v. A turn left here means the hop that should have "+
				"carried it was never published", subjectErr), report)
		return
	}

	if present, artifact := carriesStageArtifact(state, phase); present {
		// The stage FINISHED — its artifact is on the turn — and nothing is
		// queued to carry the turn onward. Re-running its own stage cannot help:
		// the recorder reads a re-entry into the phase the turn is already in as
		// a resume, the persona guard finds the artifact and skips, and nothing
		// is written.
		//
		// But this turn is NOT ended on first sighting, and the reason is a race
		// that costs a player their turn. The queued set is one snapshot taken
		// before the turn pages are read, so between the two a redelivered
		// persona task can land its verdict, the pack can fire, and a trigger can
		// be published — leaving a stale snapshot saying "nothing queued", a
		// fresh page saying "artifact present", and a live trigger on its way.
		// Ending here would fail a turn that was seconds from resolving, and
		// `failed` is terminal: the trigger arrives and is declined.
		//
		// So the sighting is COUNTED instead, through the same budget every other
		// disposition uses. It costs no publish and no model call — which is also
		// the answer to why spending budget on a stage that would decline to act
		// is acceptable here: nothing is spent but a number. A transient race
		// resolves by the next pass, when the turn has moved on; a hop that
		// genuinely was never published still ends the turn, one pass later.
		r.countTowardAbandonment(ctx, turnID, state, phase, fmt.Sprintf(
			"the turn carries %s, so its stage finished — and no work is queued for it on any stage or persona "+
				"queue, so nothing will carry it onward. Re-running the stage it is parked in cannot help: it "+
				"would re-enter a phase the turn is already in and find the artifact already recorded. The hop "+
				"that should have followed was never published",
			artifact), report)
		return
	}
	r.retriggerStage(ctx, turnID, state, phase, subject, report)
}

// countTowardAbandonment spends one budget unit on a turn this pass has no move
// for, and ends it once the budget is gone.
//
// It publishes nothing, so the unit costs a graph write and no model call. That
// is the whole reason a budget is appropriate for a turn nothing can re-run: it
// is not paying for attempts, it is counting SIGHTINGS, and requiring more than
// one is what makes the pass immune to its own snapshot being a moment old. A
// turn genuinely stuck is seen again on the next pass and again on the one after;
// a turn caught mid-race has moved on by then and is never seen here again.
//
// When the budget does run out the diagnosis lands in two places. On the TURN, as
// the closed reason and a stored explanation, because that is the player's record
// and the ledger's. And in the pass's Failures, because reaching here means a
// sequencing rule fired and its publish did not — a state the engine cannot
// explain to itself, and one an operator should read about rather than discover
// in a manifest.
func (r *Reconciler) countTowardAbandonment(
	ctx context.Context,
	turnID string,
	state *graph.EntityState,
	phase vocabulary.TurnPhase,
	because string,
	report *Reconciliation,
) {
	spent, err := recordedAttempts(state)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	if spent < r.attempts {
		if err := r.countAttempt(ctx, turnID, state.ID, spent+1); err != nil {
			report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
			return
		}
		r.logger.Warn("a turn was seen with nothing queued for it and no stage that could help; counting the "+
			"sighting rather than ending it, in case the reading was a moment old",
			"turn", state.ID, "phase", phase, "sighting", spent+1, "budget", r.attempts, "why", because)
		report.Unadvanceable++
		return
	}

	r.abandon(ctx, turnID, state, phase, "the turn was found parked in "+string(phase)+" and "+because+
		". It was seen this way on every one of its "+fmt.Sprint(r.attempts+1)+" recovery passes.", report)
	report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: fmt.Errorf(
		"was ended as unresumable after %d sightings: parked in %q with nothing queued, and %s",
		spent+1, phase, because)})
}

// retriggerStage re-runs the stage a stranded turn is parked in, spending one
// attempt — or ends the turn when the budget is gone.
//
// This is the ONLY branch that publishes anything, and the reason is the whole
// shape of the pass. Every other non-terminal turn either has a trigger the
// substrate is holding for it or is a pack defect; this one has neither, so a
// message from here is the only message that will ever reach its stage. It is
// also the only branch that can re-run a billed model call, which is why it is
// the branch with a budget.
//
// The counter is written BEFORE the trigger is published, and that order is the
// only one a bound may take. Published first, a crash between the two would
// leave the next boot re-triggering with the same count, forever; counted first,
// the same crash spends an attempt without making one, which the next boot
// corrects by spending the next. A bound may lose an attempt; it may not lose
// track of them.
func (r *Reconciler) retriggerStage(
	ctx context.Context,
	turnID string,
	state *graph.EntityState,
	phase vocabulary.TurnPhase,
	subject string,
	report *Reconciliation,
) {
	spent, err := recordedAttempts(state)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	if spent >= r.attempts {
		r.abandon(ctx, turnID, state, phase, exhaustedExplanation(phase, spent), report)
		return
	}

	if err := r.countAttempt(ctx, turnID, state.ID, spent+1); err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	if err := r.publish(ctx, state.ID, subject); err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	r.logger.Warn("a turn was stranded mid-stage with nothing running it; re-triggering its stage",
		"turn", state.ID, "phase", phase, "attempt", spent+1, "budget", r.attempts, "subject", subject)
	report.StageRetriggered++
}

// abandon ends a turn whose re-trigger budget is gone.
//
// The turn ENDS either way. A detail store that refuses does not buy back the
// wait this pass exists to end: the failure is recorded ref-lessly and the loss
// is reported, because a closed reason code with no explanation is a poorer
// record and a finished turn, while the alternative is a better record of a turn
// nobody can play past. Fail is called with a ZERO reference in that case, which
// is what makes the missing explanation legible rather than dangling — the
// projection writes no detail predicate at all, so a reader finds a turn that
// never claimed to have one.
func (r *Reconciler) abandon(
	ctx context.Context,
	turnID string,
	state *graph.EntityState,
	phase vocabulary.TurnPhase,
	explanation string,
	report *Reconciliation,
) {
	detail := &content.FailureDetail{
		TurnID:  turnID,
		Reason:  vocabulary.FailureTurnStranded,
		Message: explanation,
	}
	ref, storeErr := r.details.PutFailureDetail(ctx, state.ID, detail)
	if storeErr != nil {
		r.logger.Error("an abandoned turn's explanation could not be stored; ending it without one",
			"turn", state.ID, "phase", phase, "error", storeErr)
		ref = content.Ref{}
	}

	transition, err := r.failer.Fail(ctx, turnID, state.ID, vocabulary.FailureTurnStranded, ref)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: fmt.Errorf(
			"exhausted its %d re-trigger attempts in %q and could not be failed: %w", r.attempts, phase, err)})
		return
	}
	r.logger.Error("a stranded turn was ended explicitly rather than left waiting",
		"turn", state.ID, "phase", phase, "budget", r.attempts,
		"outcome", transition.Outcome, "detail", ref.String(), "why", explanation)
	report.Abandoned++
}

// exhaustedExplanation renders the record of a turn whose budget ran out.
//
// The arithmetic is stated in the units that happened: spent is how many times
// the stage was re-TRIGGERED, and the pass that gives up is the one after the
// last of them, so the turn was SEEN spent+1 times. Neither number is the budget,
// and neither is a count of boots — three passes can run in one process.
func exhaustedExplanation(phase vocabulary.TurnPhase, spent int) string {
	return fmt.Sprintf(
		"the turn was found parked in %q with no stage trigger queued for it, and its %s stage was re-triggered "+
			"%d time(s) across %d recovery passes without producing an artifact. The re-trigger budget is what "+
			"stops a turn nobody can finish from waiting forever; a turn that reaches it ends explicitly rather "+
			"than stalling.",
		phase, phase, spent, spent+1)
}

// resumeSubject returns the stage trigger that puts a turn parked in a phase back
// in motion.
//
// For every phase a stage ENTERS this is that phase's own trigger, and there is
// nothing to derive: the stage that owns the phase is the stage that owes the
// turn.
//
// `accepted` is the exception, and it is exactly ONE fact wide. A turn is CREATED
// in `accepted` by intake's atomic create — no stage is ever triggered to enter
// it, which is why SubjectForPhase refuses it — so the stage such a turn is owed
// is the FIRST stage, and rulepack.StagePhases already states that: it is
// documented as the phases a stage runner is triggered to enter, IN TURN ORDER,
// with `accepted` excluded precisely because nothing enters it. The pack's
// matching hop is one rule with one condition (`turn.phase.current eq accepted`),
// so there is no branch here and nothing conditional is being reconstructed.
// TestPack_FirstHopIsTheFirstStagePhase holds the two statements together.
//
// # Index 0 only, and this is the line not to cross
//
// StagePhases() is an indexable slice, and the next reader will meet it while
// holding a turn stuck in some other phase. Reaching for StagePhases()[i+1] to
// advance that turn is a ROUTE TABLE — a second statement of the turn FSM beside
// the JSON one — and it is the exact thing deleted from this package once
// already, in the form of a rule-pack matcher that inferred hops. Index 0 is safe
// because it answers "what starts a turn", which has one answer; every other
// index answers "what comes next", which has a condition attached (a verdict's
// roll gate routes to the dice or straight to the applier) and belongs to the
// pack alone.
//
// A turn resumed this way spends an attempt like any other re-trigger, because it
// costs the same thing: the adjudicator spawn the turn was always owed.
func resumeSubject(phase vocabulary.TurnPhase) (string, error) {
	if phase != vocabulary.PhaseAccepted {
		return rulepack.SubjectForPhase(phase)
	}
	stages := rulepack.StagePhases()
	if len(stages) == 0 {
		return "", fmt.Errorf(
			"a turn is parked in %q and the engine declares no stage phases at all, so nothing can start it",
			phase)
	}
	subject, err := rulepack.SubjectForPhase(stages[0])
	if err != nil {
		return "", fmt.Errorf("the first stage phase %q has no trigger subject: %w", stages[0], err)
	}
	return subject, nil
}

// countAttempt records this pass's mark on the turn.
func (r *Reconciler) countAttempt(ctx context.Context, turnID, turnEntityID string, attempt int) error {
	record := &payload.TurnResume{TurnID: turnID, Attempts: attempt}
	triples, err := record.Triples(turnEntityID, Source, r.now().UTC())
	if err != nil {
		return err
	}
	if _, err := r.turns.MergeTriples(ctx, turnEntityID, triples); err != nil {
		return fmt.Errorf("record resume attempt %d on turn %s: %w", attempt, turnEntityID, err)
	}
	return nil
}

// publish puts one stage trigger back on the stage stream.
//
// The payload is the rule engine's own publication shape, because the stage
// runner's parser is the rule engine's consumer and this pass is standing in for
// a message the rule engine already decided to send. It carries a REFERENCE and
// nothing else — the turn entity id — so a re-triggered stage reads the world as
// it is now rather than as this pass found it.
func (r *Reconciler) publish(ctx context.Context, turnEntityID, subject string) error {
	body, err := json.Marshal(map[string]any{
		"entity_id": turnEntityID,
		"subject":   subject,
		"source":    Source,
		"timestamp": r.now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("encode the stage trigger for turn %s: %w", turnEntityID, err)
	}
	if err := r.publisher.PublishToStream(ctx, subject, body); err != nil {
		return fmt.Errorf(
			"republish %s for turn %s: %w; a stage trigger travels on the stage STREAM, so a publish the server "+
				"does not acknowledge has reached no durable consumer", subject, turnEntityID, err)
	}
	return nil
}

// carriesStageArtifact reports whether the turn holds any artifact the stage
// owning its phase would have written, naming the first one found.
//
// ANY rather than all, and that is sound rather than lax: every phase's
// artifacts land in ONE merge write, so the set is all-or-nothing on the graph.
// A turn carrying some but not all of them is a write that took an appending
// lane or a partial commit, and either way the presence of one is the honest
// answer to "did this stage get as far as writing".
func carriesStageArtifact(state *graph.EntityState, phase vocabulary.TurnPhase) (bool, vocabulary.Predicate) {
	artifacts, known := vocabulary.StageArtifacts(phase)
	if !known {
		return false, ""
	}
	for _, artifact := range artifacts {
		for _, triple := range state.Triples {
			if triple.Predicate == artifact.String() {
				return true, artifact
			}
		}
	}
	return false, ""
}

// recordedAttempts reads how many times this pass has already re-triggered a
// turn.
//
// An absent predicate is zero, which is the one reading that must not be an
// error: most turns have never been rescued. Everything else is refused. Two
// values for a single-valued counter is the signature of a write that took an
// appending lane, and a reader taking the first would be choosing which budget
// the turn is held to; a non-integral value is a corrupted record rather than a
// count to round.
func recordedAttempts(state *graph.EntityState) (int, error) {
	attempts, err := payload.ResumeAttemptsFromTriples(state.Triples)
	if err != nil {
		return 0, fmt.Errorf("read persisted resume attempts: %w", err)
	}
	return attempts, nil
}

// recordedPhase reads a turn's phase without demanding that it be non-terminal.
func recordedPhase(state *graph.EntityState) (vocabulary.TurnPhase, error) {
	var objects []any
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnPhaseCurrent.String() {
			objects = append(objects, triple.Object)
		}
	}
	switch len(objects) {
	case 0:
		return "", fmt.Errorf(
			"carries no %s; a turn without a phase can be neither resumed nor archived", vocabulary.TurnPhaseCurrent)
	case 1:
	default:
		return "", fmt.Errorf(
			"holds %d values for the single-valued %s; a phase written on an appending lane leaves this pass "+
				"reading a coin flip", len(objects), vocabulary.TurnPhaseCurrent)
	}
	value, ok := objects[0].(string)
	if !ok {
		return "", fmt.Errorf("records a %T phase, want a string", objects[0])
	}
	return vocabulary.ParseTurnPhase(value)
}

// turnIDOf reads the turn identity out of a turn entity's six-part ID.
func turnIDOf(turnEntityID string) (string, error) {
	if err := types.ValidateEntityID(turnEntityID); err != nil {
		return "", fmt.Errorf("%q is not a canonical six-part entity ID: %w", turnEntityID, err)
	}
	parts := strings.Split(turnEntityID, ".")
	if segment := parts[len(parts)-2]; segment != TurnTypeSegment {
		return "", fmt.Errorf(
			"%q has type segment %q rather than %q; this pass resumes TURNS, and nothing else in the world has "+
				"a phase to be parked in", turnEntityID, segment, TurnTypeSegment)
	}
	turnID := parts[len(parts)-1]
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return "", err
	}
	return turnID, nil
}
