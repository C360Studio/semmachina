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

// violation is a rejection reason on its way to a RejectionError. It carries
// the closed code with the message so the two cannot be paired wrongly at the
// call site — the site that knows WHY a check failed is the site that knows
// which code it is.
type violation struct {
	code vocabulary.FailureReason
	err  error
}

func (v *violation) Error() string { return v.err.Error() }
func (v *violation) Unwrap() error { return v.err }

func invalid(format string, args ...any) *violation {
	return &violation{code: vocabulary.FailureEffectInvalid, err: fmt.Errorf(format, args...)}
}

func missing(format string, args ...any) *violation {
	return &violation{code: vocabulary.FailureEffectEntityMissing, err: fmt.Errorf(format, args...)}
}

func wrongKind(format string, args ...any) *violation {
	return &violation{code: vocabulary.FailureEffectEntityKind, err: fmt.Errorf(format, args...)}
}

// entityView is one resolved entity: its state and the kind it declares.
type entityView struct {
	state *graph.EntityState
	kind  vocabulary.EntityKind
}

// triplesFor returns every triple this entity records for a predicate.
//
// The whole triple, not just its object, because a multi-valued write has to
// republish siblings it never intended to change (the merge lane replaces the
// predicate's whole set) and graph.MergeTriples stores what it is handed
// verbatim. Carrying only the object would restamp every sibling with this
// turn's provenance — the answer to "when did Rook pick up the crowbar?" would
// become the turn he picked up the rations.
func (v entityView) triplesFor(predicate vocabulary.Predicate) []message.Triple {
	var out []message.Triple
	for _, triple := range v.state.Triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple)
		}
	}
	return out
}

// resolver reads each named entity at most once per batch.
//
// Reading once is not only an optimization: an intent set that touched one
// entity twice would otherwise be validated against two different snapshots,
// and the second fold would start from a set the first fold had already
// changed.
type resolver struct {
	store Store
	seen  map[string]entityView
}

func newResolver(store Store) *resolver {
	return &resolver{store: store, seen: make(map[string]entityView)}
}

// view resolves an entity an intent names, refusing anything an effect must not
// be committed against.
//
// A referential stub is the trap worth naming: graph-ingest materializes one the
// moment another entity references an ID, so the ID resolves, the read succeeds,
// and the entity carries none of its own facts. Committing against it would
// write real state onto something that was never born, and the stub MARKER
// triple persists after real birth — so only the envelope answers the question.
func (r *resolver) view(ctx context.Context, id string) (entityView, *violation) {
	if view, ok := r.seen[id]; ok {
		return view, nil
	}

	state, err := r.store.GetEntity(ctx, id)
	if err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return entityView{}, missing("entity %s does not exist", id)
		}
		return entityView{}, &violation{
			code: vocabulary.FailureEffectEntityMissing,
			err:  fmt.Errorf("read entity %s: %w", id, err),
		}
	}
	if state.IsStub() {
		return entityView{}, missing(
			"entity %s exists only as a referential stub: it carries none of its own facts, "+
				"so it has been referenced but never born", id)
	}

	kind, kindErr := kindOf(state)
	if kindErr != nil {
		return entityView{}, kindErr
	}
	view := entityView{state: state, kind: kind}
	r.seen[id] = view
	return view, nil
}

// kindOf reads the entity kind a world entity declares.
//
// An entity with no declared kind cannot be type-checked, so it is refused
// rather than waved through. That also keeps the engine's own bookkeeping
// entities — turns and campaigns, which are not things in the fiction and carry
// no kind — out of reach of an effect intent.
func kindOf(state *graph.EntityState) (vocabulary.EntityKind, *violation) {
	var declared []any
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.WorldEntityKind.String() {
			declared = append(declared, triple.Object)
		}
	}
	switch len(declared) {
	case 0:
		return "", wrongKind(
			"entity %s declares no %s, so no effect can be type-checked against it",
			state.ID, vocabulary.WorldEntityKind)
	case 1:
	default:
		return "", wrongKind(
			"entity %s declares %d kinds; a single-valued fact holding several values is the signature "+
				"of a write that took the appending lane", state.ID, len(declared))
	}

	text, ok := declared[0].(string)
	if !ok {
		return "", wrongKind("entity %s declares a %T kind, want a string", state.ID, declared[0])
	}
	kind, err := vocabulary.ParseEntityKind(text)
	if err != nil {
		return "", wrongKind("entity %s: %w", state.ID, err)
	}
	return kind, nil
}

// objectWrite is one object in a predicate's desired value set.
//
// resident is the triple the entity ALREADY records for this object, carried so
// a value this batch merely has to republish keeps the provenance it was
// written with. It is nil for anything this batch is the cause of — including
// an object the world already held that this batch nonetheless re-asserts is
// NOT nil, because a convergent re-add changes nothing and must not rewrite the
// fact's history either.
type objectWrite struct {
	object   any
	resident *message.Triple
}

// predicateWrite is the complete desired value set for one predicate on one
// entity.
//
// "Complete" is the load-bearing word. The merge lane replaces a predicate's
// whole value set, so a multi-valued predicate must travel as every value it
// should end up holding — the current ones plus or minus this batch's change —
// and a writer that sent only the new value would delete the siblings and be
// told it succeeded.
type predicateWrite struct {
	predicate vocabulary.Predicate
	objects   []objectWrite
	reference bool
	multi     bool
	// named records the objects THIS BATCH has already spoken about for this
	// predicate, which is how an intent set that contradicts itself is caught.
	// It is distinct from objects, which tracks the world.
	named map[string]bool
}

// targetWrite is one entity's whole write: every predicate this batch touches
// on it, in first-touch order.
type targetWrite struct {
	entityID string
	order    []vocabulary.Predicate
	writes   map[vocabulary.Predicate]*predicateWrite
}

// triples renders the write, returning the triples to merge and the predicates
// to clear.
//
// An emptied predicate has to be CLEARED rather than merged: the add list
// replaces a predicate's values with the ones it carries, so carrying none
// leaves the predicate exactly as it was, and "the last carried item was put
// down" would commit as "it is still carried" with a success response.
func (w *targetWrite) triples(source, context string, at time.Time) ([]message.Triple, []string) {
	var triples []message.Triple
	var cleared []string

	for _, predicate := range w.order {
		write := w.writes[predicate]
		if len(write.objects) == 0 {
			cleared = append(cleared, predicate.String())
			continue
		}
		for _, value := range write.objects {
			// A sibling this batch never touched travels VERBATIM — including
			// its subject. The merge lane forces it back onto the wire; that is
			// a lane constraint, not a licence to restamp somebody else's fact
			// with this turn's provenance. Verbatim also keeps it honest: a
			// resident triple whose subject is not this entity is refused by the
			// merge client's foreign-subject guard rather than laundered onto
			// this entity under the applier's own name.
			if value.resident != nil {
				triples = append(triples, *value.resident)
				continue
			}
			datatype := ""
			if write.reference {
				// Marks the object as an entity ID so the graph's contract
				// validator checks it as one instead of guessing from its shape.
				datatype = message.EntityReferenceDatatype
			}
			triples = append(triples, message.Triple{
				Subject:   w.entityID,
				Predicate: predicate.String(),
				Object:    value.object,
				Source:    source,
				Timestamp: at,
				// A committed effect is a fact the engine recorded as decided,
				// never an inference about the world.
				Confidence: 1.0,
				Context:    context,
				Datatype:   datatype,
			})
		}
	}
	return triples, cleared
}

// plan is the validated, ordered set of per-entity writes for one batch.
type plan struct {
	order   []string
	targets map[string]*targetWrite
}

func (p *plan) target(entityID string) *targetWrite {
	if existing, ok := p.targets[entityID]; ok {
		return existing
	}
	write := &targetWrite{entityID: entityID, writes: make(map[vocabulary.Predicate]*predicateWrite)}
	p.targets[entityID] = write
	p.order = append(p.order, entityID)
	return write
}

// buildPlan validates every intent and folds it into the per-entity writes.
//
// It completes before Apply issues any write, which is what makes the
// validation-driven all-or-nothing true: a batch rejected here applies nothing
// at all. Nothing in it reads fiction or consults a model — every check is a
// lookup against registered vocabulary, registered bounds, or the graph.
func buildPlan(
	ctx context.Context,
	resolve *resolver,
	specs vocabulary.AttributeSpecSet,
	batch *payload.EffectBatch,
) (*plan, error) {
	built := &plan{targets: make(map[string]*targetWrite)}

	for index := range batch.Intents {
		intent := &batch.Intents[index]
		if err := foldIntent(ctx, resolve, specs, built, intent); err != nil {
			return nil, &RejectionError{
				Intent: index,
				Type:   intent.Type,
				Target: intent.Target,
				Code:   err.code,
				Err:    err.err,
			}
		}
	}
	return built, nil
}

// foldIntent validates one intent and merges it into the plan.
func foldIntent(
	ctx context.Context,
	resolve *resolver,
	specs vocabulary.AttributeSpecSet,
	built *plan,
	intent *payload.EffectIntent,
) *violation {
	// Mutation validates the intent and returns the predicate and the object
	// TOGETHER. Taking the pair from one call is deliberate: a predicate-only
	// accessor would leave this function running its own switch over the same
	// closed set to find the object, and two switches over one set kept in step
	// by hand is a leak waiting for the next effect type.
	predicate, object, err := intent.Mutation()
	if err != nil {
		return &violation{code: vocabulary.FailureEffectInvalid, err: err}
	}
	if violated := checkBounds(specs, predicate, object); violated != nil {
		return violated
	}

	target, violated := resolve.view(ctx, intent.Target)
	if violated != nil {
		return violated
	}
	if !vocabulary.AllowsSubjectKind(predicate, target.kind) {
		allowed, _ := vocabulary.SubjectKindsFor(predicate)
		return wrongKind("%s cannot be carried by %s, which is a %q (allowed kinds: %v)",
			predicate, intent.Target, target.kind, allowed)
	}

	if vocabulary.IsEntityReference(predicate) {
		if violated := checkReferencedObject(ctx, resolve, predicate, intent.Target, object); violated != nil {
			return violated
		}
	}
	return foldValue(built.target(intent.Target), target, predicate, object, intent.Type)
}

// checkBounds applies THIS applier's attribute bounds to a numeric write.
//
// Mutation has already checked the intent against the engine defaults at the
// payload boundary, which is a world-blind floor on what a persona may propose.
// This is the world's own contract, and it is asked through
// AttributeForPredicate — the registered inverse — rather than by re-switching
// on effect type, so the predicate the applier is about to WRITE is the thing
// whose bounds get checked.
func checkBounds(specs vocabulary.AttributeSpecSet, predicate vocabulary.Predicate, object any) *violation {
	attribute, ok := vocabulary.AttributeForPredicate(predicate)
	if !ok {
		return nil
	}
	value, ok := object.(int)
	if !ok {
		// Unreachable: Mutation returns *EffectIntent.Value, an int, for every
		// attribute predicate. Kept fail-closed so an unbounded number cannot
		// reach the graph through a future effect type.
		return invalid("%s is a bounded attribute but the intent supplies a %T", predicate, object)
	}
	if err := specs.Check(attribute, value); err != nil {
		return &violation{code: vocabulary.FailureEffectInvalid, err: err}
	}
	return nil
}

// checkReferencedObject validates the entity on the far end of a reference.
//
// It is the object-side twin of the subject-kind rule, and it matters for the
// same reason: nothing in `world.location.current` says a character is moved
// into a scene rather than into a crowbar. The existence check earns its keep
// separately — the merge lane does not materialize a referential stub for a
// relationship target, so an unchecked reference does not create the entity it
// names, it just points at nothing forever.
//
// A RETRACTION is checked the same way, deliberately. Removal is a set
// difference over entity IDs, so an object that does not exist simply never
// matches and the write silently does nothing — and an intent that quietly does
// nothing is worse than one that says why. Nothing in this slice can produce a
// dangling edge for a removal to clean up (the applier validates on the way in,
// and the importer rejects a dangling template reference), so the strict reading
// costs nothing today; if entity deletion ever lands, revisit this line rather
// than discovering it from a retraction that cannot be made.
func checkReferencedObject(
	ctx context.Context,
	resolve *resolver,
	predicate vocabulary.Predicate,
	targetID string,
	object any,
) *violation {
	objectID, ok := object.(string)
	if !ok {
		// Unreachable: Mutation returns an entity ID for every reference-valued
		// predicate. Kept fail-closed so a future effect type cannot open a hole.
		return invalid("%s expects an entity reference but the intent supplies a %T", predicate, object)
	}
	if objectID == targetID {
		return invalid("%s names %s as its own object; an entity cannot stand in that relation to itself",
			predicate, targetID)
	}

	view, violated := resolve.view(ctx, objectID)
	if violated != nil {
		return violated
	}
	if !vocabulary.AllowsObjectKind(predicate, view.kind) {
		allowed, _ := vocabulary.ObjectKindsFor(predicate)
		return wrongKind("%s cannot point at %s, which is a %q (allowed kinds: %v)",
			predicate, objectID, view.kind, allowed)
	}
	return nil
}

// foldValue merges one validated (predicate, object) pair into the target's
// desired value set.
func foldValue(
	write *targetWrite,
	target entityView,
	predicate vocabulary.Predicate,
	object any,
	effectType vocabulary.EffectType,
) *violation {
	entry, violated := write.entryFor(target, predicate)
	if violated != nil {
		return violated
	}

	key := fmt.Sprint(object)
	if !entry.multi {
		if len(entry.named) > 0 {
			return invalid(
				"%s on %s is single-valued and this batch sets it twice; the graph would keep whichever "+
					"value was written last and neither would be wrong",
				predicate, write.entityID)
		}
		entry.named[key] = true
		entry.objects = []objectWrite{{object: object}}
		return nil
	}

	if entry.named[key] {
		return invalid("this batch names %s %s twice on %s", predicate, key, write.entityID)
	}
	entry.named[key] = true

	// Set semantics on both operations, and that is what makes recovery work:
	// re-applying a batch after a partial commit re-adds an object the world
	// already holds and re-removes one it no longer has. Treating either as an
	// error would make the converging re-application fail instead.
	if effectType == vocabulary.EffectRemoveRelationship {
		entry.objects = withoutObject(entry.objects, key)
		return nil
	}
	entry.objects = withObject(entry.objects, key, object)
	return nil
}

// entryFor returns the write for a predicate, seeding a multi-valued one from
// the entity's CURRENT triples so the published set is complete.
//
// It seeds whole triples rather than bare objects because a seeded value is a
// fact this batch is not changing: republishing it is the merge lane's demand,
// and it must arrive with the provenance it already had.
func (w *targetWrite) entryFor(target entityView, predicate vocabulary.Predicate) (*predicateWrite, *violation) {
	if existing, ok := w.writes[predicate]; ok {
		return existing, nil
	}

	entry := &predicateWrite{
		predicate: predicate,
		reference: vocabulary.IsEntityReference(predicate),
		multi:     vocabulary.IsMultiValued(predicate),
		named:     make(map[string]bool),
	}
	if entry.multi {
		for _, triple := range target.triplesFor(predicate) {
			if _, ok := triple.Object.(string); !ok {
				return nil, invalid("entity %s records a %T object for %s, which is entity-valued",
					w.entityID, triple.Object, predicate)
			}
			entry.objects = append(entry.objects, objectWrite{object: triple.Object, resident: &triple})
		}
	}

	w.writes[predicate] = entry
	w.order = append(w.order, predicate)
	return entry, nil
}

// withObject adds an object to the set, leaving an object the world already
// holds exactly as it is — including its resident triple. A convergent re-add
// changes nothing about the world, so it must not rewrite the fact's history.
func withObject(objects []objectWrite, key string, object any) []objectWrite {
	for _, existing := range objects {
		if fmt.Sprint(existing.object) == key {
			return objects
		}
	}
	return append(objects, objectWrite{object: object})
}

func withoutObject(objects []objectWrite, key string) []objectWrite {
	kept := make([]objectWrite, 0, len(objects))
	for _, existing := range objects {
		if fmt.Sprint(existing.object) != key {
			kept = append(kept, existing)
		}
	}
	return kept
}
