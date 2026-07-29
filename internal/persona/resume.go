package persona

import (
	"context"
	"errors"
	"fmt"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnReader is the read surface the resume check needs. One method, and it is
// a read: nothing about deciding whether to re-run a persona changes the world.
type TurnReader interface {
	// GetEntity reads one entity, reporting an absent one as
	// graphio.ErrEntityNotFound rather than as an empty state.
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ TurnReader = (*graphio.Store)(nil)

// Decision is what the resume check tells a spawner to do. It never reaches the
// graph — the graph records artifacts, not deliberations — so it is an
// in-process answer rather than vocabulary.
type Decision string

// The two answers.
const (
	// DecisionRun means the stage has produced nothing and the persona must be
	// spawned.
	DecisionRun Decision = "run"
	// DecisionSkip means the stage's artifact is already on the turn, so the
	// interrupted attempt actually finished and the caller should advance rather
	// than pay for the same judgment twice.
	DecisionSkip Decision = "skip"
)

// Resumption is one resume check's answer.
type Resumption struct {
	// Role is the persona the check was made for.
	Role Role
	// Decision is what to do.
	Decision Decision
	// Ref is the artifact the finished attempt produced. Zero when the decision
	// is to run.
	Ref content.Ref
}

// Guard answers the only question a resumed stage actually needs answered:
// did the interrupted attempt finish?
//
// The phase cannot answer it. A phase is written on stage ENTRY — it is what
// claims the stage — so `adjudicating` means "somebody started adjudicating",
// which is equally true of an attempt that crashed mid-model-call and one that
// wrote its verdict and died before the phase moved on. Re-running on that
// signal is not wrong, exactly: the terminal tool is idempotent, the content key
// is derived, and the merge lane replaces, so the world converges. It is just
// PAID. A persona re-run is a second billed model call for a judgment the engine
// already has, and under at-least-once delivery that is not a rare event.
//
// The artifact reference can answer it, because it is written LAST. A verdict
// reference on the turn means the verdict was stored and committed; a narration
// reference means the prose is durable and the player can be shown it. So the
// check is: look for the stage's own reference, and skip the persona when it is
// there.
//
// No compare-and-swap is required for this, which is the other half of why it is
// worth doing. Two spawners racing the same resumed stage both read the same
// reference and both skip; two that both find it absent both run, and the
// terminal tool's derived key and replacing lane mean the SECOND one's artifact
// overwrites the first's. That is convergence on one artifact, not on the first
// artifact: two runs of a persona are two model calls and the loser's judgment is
// discarded, which is a cost rather than a corruption. The turn is left carrying
// exactly one verdict, or exactly one narration, and one reference to it. The
// check removes that cost, not a correctness hazard, and it does not need a
// primitive the query surface cannot currently offer (F15).
type Guard struct {
	graph TurnReader
}

// NewGuard builds a resume check over a graph read surface.
func NewGuard(reader TurnReader) (*Guard, error) {
	if reader == nil {
		return nil, errors.New("the persona resume check requires a graph read surface")
	}
	return &Guard{graph: reader}, nil
}

// Check reports whether a persona's stage still has work to do.
//
// Every anomaly is an error rather than a shrug, because this answer decides
// whether a model call is made. A turn holding two references for one
// single-valued reference predicate is the signature of a write that took an
// appending lane, and a reader taking the first would be choosing which of two
// verdicts the campaign remembers; a referential stub is queryable and factless,
// so "no artifact recorded" read off one would be a false negative that buys a
// second call every time; and a value that is not a resolvable reference is a
// pointer at nothing, which is worse than an absent one because it reads as
// finished.
func (g *Guard) Check(ctx context.Context, spec Spec, turnID, turnEntityID string) (Resumption, error) {
	if err := spec.Validate(); err != nil {
		return Resumption{}, err
	}
	// The turn and its entity are one fact wearing two shapes, and this is a
	// seam where they arrive as two arguments: a check made against turn A's
	// entity for turn B would skip a stage that never ran.
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return Resumption{}, err
	}

	state, err := g.graph.GetEntity(ctx, turnEntityID)
	if err != nil {
		return Resumption{}, fmt.Errorf("read turn entity %s: %w", turnEntityID, err)
	}
	if state == nil {
		return Resumption{}, fmt.Errorf("read turn entity %s: the graph returned nothing", turnEntityID)
	}
	if state.IsStub() {
		return Resumption{}, fmt.Errorf(
			"turn entity %s is a referential stub: it holds no facts, so whether the %s stage finished is "+
				"unknown, and reading that as 'not finished' would buy a second model call every attempt",
			turnEntityID, spec.Role)
	}

	objects := objectsFor(state, spec.Artifact)
	switch len(objects) {
	case 0:
		return Resumption{Role: spec.Role, Decision: DecisionRun}, nil
	case 1:
	default:
		return Resumption{}, fmt.Errorf(
			"turn entity %s holds %d values for the single-valued %s; a reference written on an appending lane "+
				"leaves this check choosing which %s the campaign remembers",
			turnEntityID, len(objects), spec.Artifact, spec.Role)
	}

	value, ok := objects[0].(string)
	if !ok {
		return Resumption{}, fmt.Errorf("turn entity %s records a %T value for %s, want a storage reference",
			turnEntityID, objects[0], spec.Artifact)
	}
	ref, err := content.ParseRef(value)
	if err != nil {
		return Resumption{}, fmt.Errorf(
			"turn entity %s records %q for %s, which is not a resolvable storage reference: %w; a pointer at "+
				"nothing reads as a finished stage", turnEntityID, value, spec.Artifact, err)
	}
	if ref.IsZero() {
		return Resumption{}, fmt.Errorf(
			"turn entity %s records an empty %s; an artifact predicate is written only when there is an "+
				"artifact", turnEntityID, spec.Artifact)
	}
	return Resumption{Role: spec.Role, Decision: DecisionSkip, Ref: ref}, nil
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
