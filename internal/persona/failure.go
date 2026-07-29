package persona

import (
	"context"
	"errors"
	"fmt"

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
func RecordCapExhausted(
	ctx context.Context,
	failer TurnFailer,
	details DetailStore,
	identity Identity,
	exhaustion CapExhaustion,
) (turn.Transition, error) {
	if failer == nil {
		return turn.Transition{}, errors.New("recording a cap exhaustion requires a turn recorder")
	}
	if details == nil {
		return turn.Transition{}, errors.New("recording a cap exhaustion requires a detail store")
	}
	if err := identity.Validate(); err != nil {
		return turn.Transition{}, err
	}
	spec, err := SpecFor(exhaustion.Role)
	if err != nil {
		return turn.Transition{}, err
	}

	detail := &content.FailureDetail{
		TurnID:  identity.TurnID,
		Reason:  vocabulary.FailurePersonaCapExhausted,
		Message: exhaustion.describe(spec),
	}
	ref, err := details.PutFailureDetail(ctx, identity.TurnEntityID, detail)
	if err != nil {
		return turn.Transition{}, fmt.Errorf(
			"store the cap-exhaustion detail for turn %s: %w", identity.TurnEntityID, err)
	}
	return failer.Fail(ctx, identity.TurnID, identity.TurnEntityID, vocabulary.FailurePersonaCapExhausted, ref)
}

// describe renders the stored explanation.
func (e CapExhaustion) describe(spec Spec) string {
	message := fmt.Sprintf(
		"the %s persona used %d of its %d permitted iterations without exiting through %s",
		e.Role, e.Iterations, spec.MaxIterations, spec.Tool.Name)
	if e.LoopID != "" {
		message += fmt.Sprintf(" (loop %s)", e.LoopID)
	}
	if e.LastError != "" {
		message += fmt.Sprintf("; the last thing it was told was: %s", e.LastError)
	}
	return message + ". The iteration cap is what stops a persona costing a turn forever; a turn that " +
		"reaches it ends explicitly rather than stalling."
}
