package persona

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnFailer ends a turn explicitly, with a closed reason.
type TurnFailer interface {
	Fail(
		ctx context.Context,
		turnID, turnEntityID string,
		reason vocabulary.FailureReason,
		detail content.Ref,
	) (turn.Transition, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ TurnFailer = (*turn.Recorder)(nil)

// DetailStore is the durable home of a failure's explanation.
type DetailStore interface {
	PutFailureDetail(ctx context.Context, turnEntityID string, detail *content.FailureDetail) (content.Ref, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ DetailStore = (*content.Store)(nil)

// Bounds on the two fragments the spawner supplies to a cap exhaustion.
//
// The stored explanation has a hard budget (content.MaxFailureMessageBytes) and
// the content store enforces it BEFORE it writes, so an unbounded fragment here
// does not produce a long explanation. It produces a refused write, a
// RecordCapExhausted that returns before the turn is ever failed, and a turn left
// in `adjudicating` with a player waiting on it — precisely the silent stall this
// whole path exists to replace. LastError makes that reachable rather than
// theoretical: it is whatever the loop reported when it gave up, which is usually
// a tool refusal quoting the model's own words back.
//
// Truncation is the right kindness HERE and nowhere else in the engine. These are
// engine-authored diagnostics, where a trimmed sentence still names the field
// that went wrong; prose is the product and is never trimmed to fit, and a
// verdict is refused rather than shortened.
//
// The two budgets plus the fixed wording fit inside the message budget with room
// to spare, and TestCapExhaustion_FitsTheStoredBudgetAtItsWidest is what keeps
// that true as the wording changes.
const (
	// MaxLoopIDBytes bounds the loop identifier quoted into the explanation.
	MaxLoopIDBytes = 128
	// MaxLastErrorBytes bounds the loop's own last words.
	MaxLastErrorBytes = 3 * 1024
	// MaxLoopReasonBytes bounds the loop's own reason code, which is UPSTREAM's
	// vocabulary and therefore not a value this engine can size from its own
	// closed set.
	MaxLoopReasonBytes = 128
)

// elision marks a fragment the explanation had to cut.
const elision = "… (truncated)"

// CapExhaustion is what a spawner knows about a loop that ran out of budget.
type CapExhaustion struct {
	// Role is the persona that did not exit.
	Role Role
	// LoopID is the loop that ran out, so the trajectory can be found.
	LoopID string
	// Iterations is how many the loop actually used.
	Iterations int
	// LastError is whatever the loop reported when it gave up — very often the
	// tool boundary's last refusal, which is the sentence that says WHY the
	// persona could not produce an acceptable exit.
	LastError string
}

// RecordCapExhausted ends a turn whose persona never exited.
//
// This is the explicit half of bounded cognition (M5). The cap itself is
// upstream's — the loop counts its own iterations and stops — but a loop that
// stops having produced nothing leaves a turn sitting in `adjudicating` with a
// player waiting on it, and "the turn stalls silently" is exactly the failure the
// cap was supposed to prevent. So cap exhaustion has an explicit fallback and
// this is it: the turn ends, on the record, with a closed reason code.
//
// The reason on the graph is a CODE and the explanation is a reference, which is
// the same discipline every other failure here follows. The explanation is worth
// storing rather than logging because it is usually the most useful sentence in
// the whole turn: a persona that burned its budget almost always did so being
// refused by its own terminal tool, and that refusal names the field it could not
// get right.
//
// Detail first, then the failure — the same ordering as every other artifact
// write, for the same reason: a turn carrying a reference to an explanation
// nobody stored is worse than one carrying no reference at all.
//
// The turn ENDS either way. A detail store that refuses does not buy back the
// stall this function exists to prevent: the failure is recorded ref-lessly and
// the loss is reported as a *DetailLostError, whose Transition says the turn is
// resolved. A non-nil error from this function therefore does NOT mean the stage
// still needs running — check the transition, or the type.
func RecordCapExhausted(
	ctx context.Context,
	failer TurnFailer,
	details DetailStore,
	identity Identity,
	exhaustion CapExhaustion,
) (turn.Transition, error) {
	spec, err := SpecFor(exhaustion.Role)
	if err != nil {
		return turn.Transition{}, err
	}
	return record(ctx, failer, details, identity,
		vocabulary.FailurePersonaCapExhausted, content.FailureClassAgentLimit, exhaustion.describe(spec))
}

// LoopFailure is what a watcher knows about a persona loop that ended without
// exiting, for a reason that is NOT its iteration cap.
//
// It is the commoner half of the same event and it had no code until the
// vocabulary grew one: a model error, a provider refusal, a handler fault inside
// the loop. From the turn's point of view the two are indistinguishable —
// nobody is going to produce the artifact this stage was spawned for — so the
// disposition is the same and only the closed code differs.
type LoopFailure struct {
	// Role is the persona that did not exit.
	Role Role
	// LoopID is the loop that ended, so the trajectory can be found.
	LoopID string
	// Reason is the loop's OWN reason code. It is upstream's vocabulary, not
	// this engine's, which is exactly why it stays behind the reference: an
	// open set of producer-authored codes on rule-matching surface would be the
	// closure hole the failure vocabulary exists to close.
	Reason string
	// Iterations is how many the loop used before it stopped.
	Iterations int
	// LastError is whatever the loop reported when it gave up.
	LastError string
}

// RecordLoopFailed ends a turn whose persona loop failed for a reason other than
// its cap.
//
// It exists because the alternative was a log line. That branch used to say the
// turn was "left in its stage until the next boot's recovery replay" — and the
// replay it named does not happen (a parked turn matches no rule and has not
// been written since the rule last evaluated it), so the sentence described a
// rescue nothing performed. The turn simply waited.
//
// Everything else is RecordCapExhausted's discipline, unchanged: the code on the
// graph is closed vocabulary, the loop's own words ride behind a reference, and
// the turn ends whether or not the explanation could be stored.
func RecordLoopFailed(
	ctx context.Context,
	failer TurnFailer,
	details DetailStore,
	identity Identity,
	failure LoopFailure,
) (turn.Transition, error) {
	spec, err := SpecFor(failure.Role)
	if err != nil {
		return turn.Transition{}, err
	}
	return record(ctx, failer, details, identity,
		vocabulary.FailurePersonaLoopFailed, classifyLoopFailure(failure.Reason), failure.describe(spec))
}

// classifyLoopFailure maps only upstream's structured Reason field. It never
// scans Error/Message text, where provider bodies and credentials may appear.
func classifyLoopFailure(reason string) content.FailureClass {
	switch reason {
	case "model_error":
		return content.FailureClassProviderModel
	case "length_truncated":
		return content.FailureClassModelOutputLimit
	case "handler_error", "spawn_identity_birth_failed":
		return content.FailureClassAgentRuntime
	default:
		return content.FailureClassUnknown
	}
}

// record is the one detail-then-failure path both persona endings run through.
//
// Shared rather than re-derived per reason, because the ordering IS the
// contract: store the explanation, then fail with a reference to it; and if the
// store refuses, fail anyway. A second copy of that would be a second chance to
// return early on a store fault and leave the turn exactly where the whole path
// exists to rescue it from.
func record(
	ctx context.Context,
	failer TurnFailer,
	details DetailStore,
	identity Identity,
	reason vocabulary.FailureReason,
	class content.FailureClass,
	explanation string,
) (turn.Transition, error) {
	if failer == nil {
		return turn.Transition{}, errors.New("recording a persona failure requires a turn recorder")
	}
	if details == nil {
		return turn.Transition{}, errors.New("recording a persona failure requires a detail store")
	}
	if err := identity.Validate(); err != nil {
		return turn.Transition{}, err
	}

	detail := &content.FailureDetail{
		TurnID: identity.TurnID, Reason: reason, Class: class, Message: explanation,
	}
	ref, storeErr := details.PutFailureDetail(ctx, identity.TurnEntityID, detail)
	if storeErr != nil {
		return failWithoutDetail(ctx, failer, identity, reason, storeErr)
	}
	return failer.Fail(ctx, identity.TurnID, identity.TurnEntityID, reason, ref)
}

// failWithoutDetail ends the turn when its explanation could not be stored.
//
// Detail-first is the ordering everywhere else because a turn referencing an
// explanation nobody stored is worse than one carrying no reference at all. That
// argument bounds what the reference may claim; it does not license ABANDONING
// the turn. A store that refuses is the moment the turn is most likely to be left
// mid-stage, and returning here would leave it exactly where the cap was supposed
// to rescue it from — a player waiting on a stage that will never run again.
//
// The composition above is bounded so this path is a STORE fault rather than a
// self-inflicted one, but bounding cannot reach an unreachable bucket. A closed
// reason code with no explanation is a poorer record and a finished turn; the
// alternative is a better record of a turn nobody can play past.
//
// Fail is called with a ZERO reference, which is what makes the missing
// explanation legible rather than dangling: the projection writes no detail
// predicate at all, so a reader finds a turn that never claimed to have one.
func failWithoutDetail(
	ctx context.Context,
	failer TurnFailer,
	identity Identity,
	reason vocabulary.FailureReason,
	storeErr error,
) (turn.Transition, error) {
	stored := fmt.Errorf("store the %s detail for turn %s: %w", reason, identity.TurnEntityID, storeErr)
	transition, failErr := failer.Fail(
		ctx, identity.TurnID, identity.TurnEntityID, reason, content.Ref{})
	if failErr != nil {
		// Both halves are reported: the turn is genuinely stalled now, and which
		// of the two surfaces is down decides where an operator looks.
		return turn.Transition{}, errors.Join(stored,
			fmt.Errorf("fail turn %s without an explanation: %w", identity.TurnEntityID, failErr))
	}
	return transition, &DetailLostError{
		TurnEntityID: identity.TurnEntityID,
		Transition:   transition,
		Err:          stored,
	}
}

// DetailLostError reports a turn that ENDED without its explanation.
//
// It is a type rather than a wrapped sentence because the two things a caller
// needs to know point in opposite directions, and a bare error can only say one
// of them: the turn is RESOLVED — nobody is waiting, and re-running the stage
// would be a second bill for a judgment that already ran out — while the content
// store REFUSED a write, which is an infrastructure fault worth surfacing and
// worth retrying on its own terms. A caller that reads the error as "the failure
// did not record" draws exactly the wrong conclusion about the turn.
type DetailLostError struct {
	// TurnEntityID is the turn that ended without its explanation.
	TurnEntityID string
	// Transition is what the failure write actually did, so a caller routing on
	// this error does not have to guess whether the turn ended.
	Transition turn.Transition
	// Err is the store's refusal.
	Err error
}

// Error implements error.
func (e *DetailLostError) Error() string {
	return fmt.Sprintf(
		"turn %s ended in phase %q carrying no explanation: the detail store refused the write: %v. The closed "+
			"reason code is on the graph and the diagnosis is not; the turn needs nothing, the store does",
		e.TurnEntityID, e.Transition.Phase, e.Err)
}

// Unwrap exposes the store's refusal.
func (e *DetailLostError) Unwrap() error { return e.Err }

// describe renders the stored explanation.
//
// Every caller-supplied fragment is clipped on the way in rather than the whole
// message being clipped on the way out, because the two ends of this sentence are
// not equally worth keeping: the head names the persona and the exit it never
// reached, the tail says what the cap is for, and only the middle is unbounded.
// A single trailing cut would drop the framing and keep the padding.
func (e CapExhaustion) describe(spec Spec) string {
	message := fmt.Sprintf(
		"the %s persona used %d of its %d permitted iterations without exiting through %s",
		e.Role, e.Iterations, spec.MaxIterations, spec.Tool.Name)
	if e.LoopID != "" {
		message += fmt.Sprintf(" (loop %s)", clip(e.LoopID, MaxLoopIDBytes))
	}
	if e.LastError != "" {
		message += fmt.Sprintf("; the last thing it was told was: %s", clip(e.LastError, MaxLastErrorBytes))
	}
	return message + ". The iteration cap is what stops a persona costing a turn forever; a turn that " +
		"reaches it ends explicitly rather than stalling."
}

// describe renders an ordinary loop failure's stored explanation.
//
// The loop's OWN reason code is quoted into the sentence rather than mapped onto
// the closed vocabulary, and that is the whole reason this text exists. Upstream
// grows its reason set on its own schedule, so a mapping here would be a table
// that silently stops covering the case an operator is actually looking at; the
// graph gets one closed code that says which ENDING happened, and the sentence
// says what upstream called it.
//
// Every caller-supplied fragment is clipped on the way in, for the reason the
// cap's is: the stored explanation has a hard budget the content store enforces
// BEFORE it writes, so an unbounded fragment produces a refused write rather
// than a long message — and a refused write is how this path stops ending the
// turn it exists to end.
func (f LoopFailure) describe(spec Spec) string {
	message := fmt.Sprintf(
		"the %s persona loop ended without exiting through %s, and not because it ran out of iterations: the "+
			"loop reported %q after %d of its %d permitted iterations",
		f.Role, spec.Tool.Name, clip(f.Reason, MaxLoopReasonBytes), f.Iterations, spec.MaxIterations)
	if f.LoopID != "" {
		message += fmt.Sprintf(" (loop %s)", clip(f.LoopID, MaxLoopIDBytes))
	}
	if f.LastError != "" {
		message += fmt.Sprintf("; the last thing it was told was: %s", clip(f.LastError, MaxLastErrorBytes))
	}
	return message + ". A loop that stops trying and a loop that runs out of budget leave the turn in the same " +
		"place, so both end it explicitly rather than leaving a player waiting on a stage nothing will run again."
}

// clip bounds one diagnostic fragment to limit bytes, keeping its front.
//
// The front is where a refusal names the field it could not accept; the back is
// where a wrapped chain repeats itself. The cut lands on a rune boundary because
// a fragment severed mid-sequence is not text any more — it round-trips through
// JSON as a replacement character and reads as corruption rather than as
// trimming.
//
// The elision marker is spent OUT of the budget rather than added to it, and the
// marker itself is dropped entirely when the budget cannot hold it. Otherwise the
// one property this function exists to provide would be conditional on the
// caller's limit being comfortably larger than a constant it cannot see — which
// is how a bound that holds today stops holding when somebody tightens it.
func clip(text string, limit int) string {
	if limit < 0 {
		limit = 0
	}
	if len(text) <= limit {
		return text
	}
	marker := elision
	if len(marker) > limit {
		marker = ""
	}
	keep := truncateAtRune(text, limit-len(marker))
	return text[:keep] + marker
}

// truncateAtRune returns the largest length at or below limit that does not
// split a rune.
func truncateAtRune(text string, limit int) int {
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return limit
}
