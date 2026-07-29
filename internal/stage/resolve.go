package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Source is the triple Source stamped on the facts this package writes.
const Source = "turn-stage"

// VerdictReader resolves the verdict reference a turn carries.
type VerdictReader interface {
	GetVerdict(ctx context.Context, ref content.Ref) (*payload.Verdict, error)
}

// RollStore is the durable home of the roll's replayable half.
type RollStore interface {
	PutRoll(ctx context.Context, turnEntityID string, roll *payload.RollResult) (content.Ref, error)
}

// TurnWriter is the merge lane the turn's own facts are written through.
//
// The merge lane and nothing else: every predicate a stage lands on a turn is
// single-valued, and the triple-add lanes APPEND, so a duplicate trigger through
// one leaves a turn holding two bands with a success response and no error
// anywhere (F14).
type TurnWriter interface {
	MergeTriples(
		ctx context.Context,
		entityID string,
		triples []message.Triple,
		opts ...graphio.MergeOption,
	) (*graph.EntityState, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ VerdictReader = (*content.Store)(nil)
	_ RollStore     = (*content.Store)(nil)
	_ TurnWriter    = (*graphio.Store)(nil)
)

// Resolver is the stage that rolls the dice.
type Resolver struct {
	recorder PhaseRecorder
	turns    TurnReader
	verdicts VerdictReader
	rolls    RollStore
	writer   TurnWriter
	resolver *dice.Resolver
	now      func() time.Time
	logger   *slog.Logger
}

// ResolverOption configures a Resolver stage.
type ResolverOption func(*Resolver)

// WithResolverClock overrides the stage's clock so a written triple's timestamp
// is an assertable value.
func WithResolverClock(now func() time.Time) ResolverOption {
	return func(r *Resolver) {
		if now != nil {
			r.now = now
		}
	}
}

// WithResolverLogger sets the stage's logger.
func WithResolverLogger(logger *slog.Logger) ResolverOption {
	return func(r *Resolver) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// NewResolver builds the dice stage.
func NewResolver(
	recorder PhaseRecorder,
	turns TurnReader,
	verdicts VerdictReader,
	rolls RollStore,
	writer TurnWriter,
	resolver *dice.Resolver,
	opts ...ResolverOption,
) (*Resolver, error) {
	switch {
	case recorder == nil:
		return nil, errors.New("the dice stage requires a phase recorder")
	case turns == nil:
		return nil, errors.New("the dice stage requires a turn reader")
	case verdicts == nil:
		return nil, errors.New("the dice stage requires a verdict reader")
	case rolls == nil:
		return nil, errors.New("the dice stage requires a roll store")
	case writer == nil:
		return nil, errors.New("the dice stage requires a graph writer")
	case resolver == nil:
		return nil, errors.New("the dice stage requires a dice resolver")
	}
	stage := &Resolver{
		recorder: recorder, turns: turns, verdicts: verdicts, rolls: rolls,
		writer: writer, resolver: resolver, now: time.Now, logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(stage)
	}
	return stage, nil
}

// Phase implements Stage.
func (r *Resolver) Phase() vocabulary.TurnPhase { return vocabulary.PhaseResolving }

// Run enters resolution, rolls once, and records the roll.
//
// The whole stage is idempotent without a guard of its own, which is why it has
// none: the roll is a pure function of the campaign seed and the turn id, the
// resolver refuses to produce a second one for a turn that already carries one
// (and cross-checks the recorded values against a fresh derivation while it is
// there), the content key is derived from the turn so a re-put writes identical
// bytes to the identical key, and the triples go down the merge lane, which
// replaces. A redelivered trigger therefore converges rather than accumulating.
//
// The object is stored BEFORE the reference reaches the graph. A turn pointing
// at a roll nobody stored cannot be replayed or explained; an unreferenced
// object is garbage the next attempt overwrites at the same key.
func (r *Resolver) Run(ctx context.Context, trigger Trigger) error {
	run, err := enter(ctx, r.recorder, trigger, r.Phase())
	if err != nil {
		return err
	}
	if !run {
		return nil
	}

	verdict, err := r.verdict(ctx, trigger)
	if err != nil {
		return err
	}
	resolution, err := r.resolver.Resolve(ctx, verdict, trigger.TurnEntityID)
	if err != nil {
		// A no-roll verdict reaching the dice is a mis-wired pack, not a turn
		// outcome: the applier is the stage that should have been triggered, and
		// the verdict is intact. Naming it separately keeps a pack bug out of the
		// player's turn record.
		if errors.Is(err, dice.ErrNoRollRequired) {
			return fmt.Errorf(
				"turn %s reached the dice with a verdict that declined them; the no-roll path goes straight "+
					"to effect application: %w", trigger.TurnEntityID, err)
		}
		return err
	}
	if !resolution.Rolled {
		r.logger.Info("turn already carries its roll; not rolling again",
			"turn", trigger.TurnEntityID, "band", resolution.Roll.Band)
		return nil
	}

	ref, err := r.rolls.PutRoll(ctx, trigger.TurnEntityID, resolution.Roll)
	if err != nil {
		return fmt.Errorf("store the roll for turn %s: %w", trigger.TurnEntityID, err)
	}
	triples, err := resolution.Roll.Triples(trigger.TurnEntityID, ref.String(), Source, r.now().UTC())
	if err != nil {
		return err
	}
	if _, err := r.writer.MergeTriples(ctx, trigger.TurnEntityID, triples); err != nil {
		return fmt.Errorf("record the roll on turn %s: %w", trigger.TurnEntityID, err)
	}
	return nil
}

// verdict resolves the judgment this roll is for.
func (r *Resolver) verdict(ctx context.Context, trigger Trigger) (*payload.Verdict, error) {
	state, err := readTurn(ctx, r.turns, trigger.TurnEntityID)
	if err != nil {
		return nil, err
	}
	return verdictOf(ctx, r.verdicts, state)
}

// verdictOf follows a turn's verdict reference.
func verdictOf(ctx context.Context, verdicts VerdictReader, state *graph.EntityState) (*payload.Verdict, error) {
	ref, err := soleRef(state, vocabulary.TurnVerdictRef)
	if err != nil {
		return nil, fmt.Errorf(
			"%w; a turn past adjudication without a verdict has nothing to resolve or apply", err)
	}
	verdict, err := verdicts.GetVerdict(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve the verdict for turn %s: %w", state.ID, err)
	}
	return verdict, nil
}
