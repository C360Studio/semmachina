package effect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Source is the triple Source stamped on every fact the applier commits. It is
// how a later reader tells a change play made from the starting state a world
// package authored.
const Source = "effect-applier"

// Store is the graph surface the applier needs.
//
// The write method is the MERGE lane and only the merge lane. There is no seam
// here for triple.add or .add_batch, which append: a single-valued effect
// committed through those accumulates values instead of replacing them,
// silently and with a success response.
type Store interface {
	// GetEntity reads one entity, reporting an absent one as
	// graphio.ErrEntityNotFound rather than as an empty state.
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
	// MergeTriples writes one entity's triples with replace-by-predicate
	// semantics. It is per-entity: every triple must target entityID.
	MergeTriples(
		ctx context.Context,
		entityID string,
		triples []message.Triple,
		opts ...graphio.MergeOption,
	) (*graph.EntityState, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Store = (*graphio.Store)(nil)

// Applier commits validated effect batches.
type Applier struct {
	store Store
	now   func() time.Time
	specs vocabulary.AttributeSpecSet
}

// Option configures an Applier.
type Option func(*Applier)

// WithClock overrides the applier's clock. Tests use it so a committed triple's
// timestamp is an assertable value rather than whatever time it happened to be.
func WithClock(now func() time.Time) Option {
	return func(a *Applier) { a.now = now }
}

// WithAttributeSpecs sets the attribute bounds this applier enforces.
//
// Bounds are WORLD data — a grim survival world wants health 0-4 where a heroic
// one wants 0-40 — so they are a value the applier is HANDED, exactly as the
// world loader is handed LoadOptions.AttributeSpecs. Taking the set at
// construction is what keeps that a single contract with two consumers: wiring
// a world's set into the turn loop becomes an argument, not a second decision
// about where bounds live.
//
// The default is the engine's numbers, which is also what EffectIntent.Validate
// checks at the payload boundary. That payload gate is a floor on what a
// PERSONA may propose and is deliberately world-blind; this one is the world's
// own contract. Today they are the same numbers, so a set NARROWER than the
// defaults is enforced here and a wider one would still be stopped at the
// payload gate — when a world first supplies wider bounds, that gate becomes
// shape-only and this stays the sole bounds authority.
func WithAttributeSpecs(specs vocabulary.AttributeSpecSet) Option {
	return func(a *Applier) { a.specs = specs }
}

// NewApplier builds an applier over a graph store.
func NewApplier(store Store, opts ...Option) (*Applier, error) {
	if store == nil {
		return nil, errors.New("effect applier requires a graph store")
	}
	applier := &Applier{store: store, now: time.Now, specs: vocabulary.DefaultAttributeSpecs()}
	for _, opt := range opts {
		opt(applier)
	}
	if applier.now == nil {
		return nil, errors.New("effect applier requires a clock")
	}
	if applier.specs.IsZero() {
		return nil, errors.New(
			"effect applier requires attribute bounds; the zero set answers 'unknown attribute' for every " +
				"registered attribute, so every set_attribute effect would be refused")
	}
	return applier, nil
}

// Outcome is one apply attempt's result.
type Outcome struct {
	// BatchID is the batch identity derived from the turn.
	BatchID string
	// Applied reports that THIS call committed the batch. False means the turn
	// already recorded it — a duplicate trigger or a crash-restart — and the
	// world was left alone.
	Applied bool
	// Targets are the entities written, in commit order. Empty when Applied is
	// false, and legitimately empty for a band that declares no world change.
	Targets []string
}

// Apply validates the batch and commits it, or refuses the turn.
//
// The order of operations is the contract:
//
//  1. The batch validates as a payload — identity derived from the turn, band in
//     vocabulary, every intent in vocabulary and inside its registered bounds.
//  2. The turn entity is read. A turn already recording this batch is a
//     duplicate trigger: nothing is written and Applied is false.
//  3. EVERY intent is validated against the graph — the existence of each entity
//     it names, and whether the predicate may live on that kind of thing —
//     before any write is issued. This is the all-or-nothing.
//  4. One merge per target entity, in first-touch order.
//  5. The batch marker lands on the turn LAST. A crash before it leaves an
//     applied world and an unmarked turn, and re-application converges because
//     every write replaces; the reverse order would leave a turn claiming
//     effects that never landed, which nothing can repair.
//
// batchRef is the storage reference to the stored batch payload. It is a
// parameter rather than something derived here because the applier does not own
// prose or payload storage: it is handed the reference the turn loop already
// wrote, the same way the roll projection is handed its own.
func (a *Applier) Apply(
	ctx context.Context,
	batch *payload.EffectBatch,
	turnEntityID string,
	batchRef string,
) (Outcome, error) {
	if batch == nil {
		return Outcome{}, errors.New("apply requires an effect batch")
	}
	if err := batch.Validate(); err != nil {
		return Outcome{}, &RejectionError{Intent: -1, Code: vocabulary.FailureEffectInvalid, Err: err}
	}
	// The entity and the batch are one turn wearing two shapes, and nothing
	// downstream cross-checks them: the identity is derived from the payload
	// and the marker lands on the entity, so a mismatched pair passes every
	// later guard while changing the world under the wrong turn's paperwork.
	if err := payload.RequireTurnEntityID(batch.TurnID, turnEntityID); err != nil {
		return Outcome{}, err
	}
	if batchRef == "" {
		return Outcome{}, errors.New("apply requires a reference to the stored batch payload")
	}

	applied, err := a.alreadyApplied(ctx, batch, turnEntityID, batchRef)
	if err != nil {
		return Outcome{}, err
	}
	if applied {
		return Outcome{BatchID: batch.BatchID}, nil
	}

	committed, err := buildPlan(ctx, newResolver(a.store), a.specs, batch)
	if err != nil {
		return Outcome{}, err
	}
	return a.commit(ctx, batch, committed, turnEntityID, batchRef)
}

// commit issues the per-entity merges and then marks the turn.
func (a *Applier) commit(
	ctx context.Context,
	batch *payload.EffectBatch,
	committed *plan,
	turnEntityID string,
	batchRef string,
) (Outcome, error) {
	at := a.now().UTC()
	written := make([]string, 0, len(committed.order))

	for _, entityID := range committed.order {
		triples, cleared := committed.targets[entityID].triples(Source, batch.TurnID, at)
		if _, err := a.store.MergeTriples(ctx, entityID, triples,
			graphio.WithClearedPredicates(cleared...)); err != nil {
			// The merge lane is per-entity and its response carries no
			// failed-subject list, so this is where a multi-entity batch stops
			// being atomic. A response error cannot say whether this target
			// mutated before the reply failed, so written is only the confirmed
			// response prefix and recovery re-applies this target too.
			return Outcome{}, &CommitError{
				BatchID: batch.BatchID, Target: entityID, Committed: written, Err: err,
			}
		}
		written = append(written, entityID)
	}

	marker, err := batch.Triples(turnEntityID, batchRef, Source, at)
	if err != nil {
		return Outcome{}, &CommitError{
			BatchID: batch.BatchID, Target: turnEntityID, Committed: written, Err: err,
		}
	}
	if _, err := a.store.MergeTriples(ctx, turnEntityID, marker); err != nil {
		return Outcome{}, &CommitError{
			BatchID: batch.BatchID, Target: turnEntityID, Committed: written, Err: err,
		}
	}
	return Outcome{BatchID: batch.BatchID, Applied: true, Targets: written}, nil
}

// alreadyApplied reports whether the turn already records this batch.
//
// Every anomaly is an error rather than a shrug. A turn holding two batch
// identities is the signature of a write that took the appending lane, and a
// reader taking the first value it found would be reading a coin flip; a turn
// holding an identity with no reference is a half-written record whose payload
// the ledger and replay cannot reach; a turn recording somebody else's batch is
// a turn whose own identity derivation disagrees with its record.
//
// Both halves of the marker are checked, because the identity alone cannot
// distinguish a re-trigger from a re-STORE. The identity is derived from the
// turn, so a second attempt whose payload was written to a new location derives
// exactly the same id — reporting that as applied would leave the recorded
// reference pointing at the first payload and the second orphaned, and the
// reference is the half the ledger and replay actually read.
func (a *Applier) alreadyApplied(
	ctx context.Context,
	batch *payload.EffectBatch,
	turnEntityID string,
	batchRef string,
) (bool, error) {
	state, err := a.store.GetEntity(ctx, turnEntityID)
	if err != nil {
		return false, fmt.Errorf("read turn entity %s: %w", turnEntityID, err)
	}
	// A stub is queryable and factless, so "no batch recorded" read off one
	// would be a false negative — and this component's answer to that question
	// decides whether the world is changed a second time.
	if state.IsStub() {
		return false, fmt.Errorf(
			"turn entity %s is a referential stub: it holds no facts, so its applied-batch state is unknown",
			turnEntityID)
	}

	identities := objectsFor(state, vocabulary.TurnEffectsBatch)
	references := objectsFor(state, vocabulary.TurnEffectsRef)
	for _, objects := range [][]any{identities, references} {
		if len(objects) > 1 {
			return false, fmt.Errorf(
				"turn entity %s holds %d values for a single-valued effect predicate; single-valued facts "+
					"must be written on the replace lane, never appended", turnEntityID, len(objects))
		}
	}
	if len(identities) != len(references) {
		return false, fmt.Errorf(
			"turn entity %s records %d batch identities and %d batch references; an applied batch lands "+
				"in one write or not at all", turnEntityID, len(identities), len(references))
	}
	if len(identities) == 0 {
		return false, nil
	}

	recorded, ok := identities[0].(string)
	if !ok {
		return false, fmt.Errorf("turn entity %s records a %T batch identity, want a string",
			turnEntityID, identities[0])
	}
	if recorded != batch.BatchID {
		return false, fmt.Errorf(
			"turn entity %s already records batch %q, but this batch derives %q from the same turn; "+
				"one turn has one batch", turnEntityID, recorded, batch.BatchID)
	}

	recordedRef, ok := references[0].(string)
	if !ok {
		return false, fmt.Errorf("turn entity %s records a %T batch reference, want a string",
			turnEntityID, references[0])
	}
	if recordedRef != batchRef {
		return false, fmt.Errorf(
			"turn entity %s records batch %q at %q, but this apply carries %q; the identity is derived from "+
				"the turn, so a re-stored payload derives the same id — reporting this as applied would orphan "+
				"the payload the ledger and replay would read",
			turnEntityID, recorded, recordedRef, batchRef)
	}
	return true, nil
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
