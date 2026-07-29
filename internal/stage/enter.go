package stage

import (
	"context"
	"fmt"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// PhaseRecorder is the guarded phase-write surface every stage enters through.
type PhaseRecorder interface {
	// Advance moves the turn into a phase, guarded by the phase it is recorded
	// in.
	Advance(ctx context.Context, turnID, turnEntityID string, to vocabulary.TurnPhase) (turn.Transition, error)
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

// TurnReader reads the turn entity a stage is working on.
type TurnReader interface {
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ PhaseRecorder = (*turn.Recorder)(nil)
	_ TurnFailer    = (*turn.Recorder)(nil)
)

// enter claims a stage by writing its phase, and reports whether the stage
// should run.
//
// Every stage begins here, and the ORDER matters more than the call does. The
// recorder answers a different question from every other guard in the loop —
// "is this transition legal, and is this stage still the turn's business?" — and
// it answers it against the FSM, which is the only place that knows a hop was
// skipped. Asking it FIRST means a stale trigger for a stage the turn has left
// costs one read and stops, before a scene is assembled or a model is called.
//
// Three answers, and each is a different instruction:
//
//   - Advanced: this call claimed the stage. Run it.
//   - Resumed: the turn was already in this phase — an interrupted attempt
//     re-entering itself, or a redelivered trigger. Nothing was written, and the
//     stage runs, because the phase is written on ENTRY and therefore never said
//     the stage finished. What stops a resumed persona stage costing a second
//     billed call is the artifact guard, not this.
//   - Declined: the turn has moved past this stage, or ended. Doing nothing is
//     the correct response to at-least-once delivery, and the trigger is
//     acknowledged.
//
// Anything else is an IllegalTransitionError and travels back to the caller,
// which naks it: a hop that skips a stage is a wiring bug, and a turn that
// advanced without running a stage is worse than a turn that stalled.
func enter(
	ctx context.Context,
	recorder PhaseRecorder,
	trigger Trigger,
	phase vocabulary.TurnPhase,
) (bool, error) {
	transition, err := recorder.Advance(ctx, trigger.TurnID, trigger.TurnEntityID, phase)
	if err != nil {
		return false, err
	}
	return transition.Outcome != turn.OutcomeDeclined, nil
}

// objectsFor returns every object an entity records for a predicate.
func objectsFor(state *graph.EntityState, predicate vocabulary.Predicate) []any {
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

// soleObject reads a single-valued predicate off a turn entity.
//
// Two values is an error rather than a choice. A single-valued predicate holding
// two objects is the signature of a write that took an appending lane, and a
// reader that takes the first is choosing which verdict, roll, or band the
// campaign remembers.
func soleObject(state *graph.EntityState, predicate vocabulary.Predicate) (any, bool, error) {
	objects := objectsFor(state, predicate)
	switch len(objects) {
	case 0:
		return nil, false, nil
	case 1:
		return objects[0], true, nil
	default:
		return nil, false, fmt.Errorf(
			"turn entity %s holds %d values for the single-valued %s; a fact written on an appending lane "+
				"leaves this reader picking one at random", state.ID, len(objects), predicate)
	}
}

// soleRef reads one required storage reference off a turn entity.
func soleRef(state *graph.EntityState, predicate vocabulary.Predicate) (content.Ref, error) {
	object, present, err := soleObject(state, predicate)
	if err != nil {
		return content.Ref{}, err
	}
	if !present {
		return content.Ref{}, fmt.Errorf("turn entity %s carries no %s", state.ID, predicate)
	}
	value, ok := object.(string)
	if !ok {
		return content.Ref{}, fmt.Errorf(
			"turn entity %s records a %T value for %s, want a storage reference", state.ID, object, predicate)
	}
	ref, err := content.ParseRef(value)
	if err != nil {
		return content.Ref{}, fmt.Errorf("turn entity %s records an unresolvable %s: %w", state.ID, predicate, err)
	}
	if ref.IsZero() {
		return content.Ref{}, fmt.Errorf("turn entity %s records an empty %s", state.ID, predicate)
	}
	return ref, nil
}

// readTurn reads a turn entity and refuses a referential stub.
//
// A stub is queryable and factless, so every "is this predicate present?"
// question answered off one comes back a false negative — which for a stage
// means re-running work the turn already recorded.
func readTurn(ctx context.Context, reader TurnReader, turnEntityID string) (*graph.EntityState, error) {
	state, err := reader.GetEntity(ctx, turnEntityID)
	if err != nil {
		return nil, fmt.Errorf("read turn entity %s: %w", turnEntityID, err)
	}
	if state == nil {
		return nil, fmt.Errorf("read turn entity %s: the graph returned nothing", turnEntityID)
	}
	if state.IsStub() {
		return nil, fmt.Errorf(
			"turn entity %s is a referential stub: it holds no facts, so every artifact this stage looks for "+
				"reads as absent", turnEntityID)
	}
	return state, nil
}
