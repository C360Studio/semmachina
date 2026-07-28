package effect_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	turnEntityID = "c360.semmachina.eff1.starter.turn.turn-act-1"
	turnID       = "turn-act-1"
	batchRef     = "obj://effects/batch-turn-act-1"

	rook      = "c360.semmachina.eff1.starter.character.rook"
	wren      = "c360.semmachina.eff1.starter.character.wren"
	crowbar   = "c360.semmachina.eff1.starter.item.crowbar"
	lantern   = "c360.semmachina.eff1.starter.item.lantern"
	rations   = "c360.semmachina.eff1.starter.item.rations"
	gatehouse = "c360.semmachina.eff1.starter.scene.gatehouse"
	courtyard = "c360.semmachina.eff1.starter.scene.courtyard"
	absent    = "c360.semmachina.eff1.starter.character.nobody"
)

var applyTime = time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)

// mergeCall is one per-entity write the applier issued.
type mergeCall struct {
	entityID string
	triples  []message.Triple
	cleared  []string
}

// fakeStore is the graph surface, scripted. It exists for the shapes a healthy
// broker will not produce on demand — a missing entity, a stub, a merge that
// fails after its predecessors committed. The success paths are proven against
// real graph-ingest in the integration suite.
type fakeStore struct {
	entities map[string]*graph.EntityState
	failOn   map[string]error
	merges   []mergeCall
	reads    []string
}

func (s *fakeStore) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	s.reads = append(s.reads, id)
	state, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	return state, nil
}

func (s *fakeStore) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	opts ...graphio.MergeOption,
) (*graph.EntityState, error) {
	// The real option type, applied to the real request struct, so the fake
	// records what graph-ingest would have received rather than a paraphrase.
	request := graph.UpdateEntityWithTriplesRequest{
		Entity:     &graph.EntityState{ID: entityID},
		AddTriples: triples,
	}
	for _, opt := range opts {
		opt(&request)
	}
	s.merges = append(s.merges, mergeCall{
		entityID: entityID, triples: request.AddTriples, cleared: request.RemoveTriples,
	})
	if err := s.failOn[entityID]; err != nil {
		return nil, err
	}
	return s.entities[entityID], nil
}

func (s *fakeStore) mergeFor(entityID string) (mergeCall, bool) {
	for _, call := range s.merges {
		if call.entityID == entityID {
			return call, true
		}
	}
	return mergeCall{}, false
}

func entityState(id string, kind vocabulary.EntityKind, facts ...message.Triple) *graph.EntityState {
	triples := []message.Triple{{
		Subject:    id,
		Predicate:  vocabulary.WorldEntityKind.String(),
		Object:     string(kind),
		Source:     "world-import",
		Confidence: 1.0,
	}}
	return &graph.EntityState{
		ID:          id,
		MessageType: message.Type{Domain: "semmachina", Category: "world_entity", Version: "v1"},
		Triples:     append(triples, facts...),
	}
}

func fact(subject string, predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{
		Subject: subject, Predicate: predicate.String(), Object: object,
		Source: "world-import", Confidence: 1.0,
	}
}

// turnEntity is a turn in its applying phase, carrying no batch yet.
func turnEntity() *graph.EntityState {
	return &graph.EntityState{
		ID:          turnEntityID,
		MessageType: message.Type{Domain: "semmachina", Category: "turn", Version: "v1"},
		Triples: []message.Triple{
			fact(turnEntityID, vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseApplying)),
		},
	}
}

// starterWorld is the fixture world's shape: Rook carries two things, knows
// Wren, and everybody is in the gatehouse.
func starterWorld() *fakeStore {
	return &fakeStore{
		entities: map[string]*graph.EntityState{
			turnEntityID: turnEntity(),
			rook: entityState(rook, vocabulary.EntityKindCharacter,
				fact(rook, vocabulary.CharacterAttributeHealth, 8),
				fact(rook, vocabulary.CharacterStatusCurrent, string(vocabulary.StatusHealthy)),
				fact(rook, vocabulary.WorldLocationCurrent, gatehouse),
				fact(rook, vocabulary.WorldRelationCarries, crowbar),
				fact(rook, vocabulary.WorldRelationCarries, lantern),
				fact(rook, vocabulary.WorldRelationKnows, wren),
			),
			wren:      entityState(wren, vocabulary.EntityKindCharacter),
			crowbar:   entityState(crowbar, vocabulary.EntityKindItem),
			lantern:   entityState(lantern, vocabulary.EntityKindItem),
			rations:   entityState(rations, vocabulary.EntityKindItem),
			gatehouse: entityState(gatehouse, vocabulary.EntityKindScene),
			courtyard: entityState(courtyard, vocabulary.EntityKindScene),
		},
		failOn: map[string]error{},
	}
}

func newApplier(t *testing.T, store effect.Store) *effect.Applier {
	t.Helper()
	applier, err := effect.NewApplier(store, effect.WithClock(func() time.Time { return applyTime }))
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	return applier
}

func batchOf(intents ...payload.EffectIntent) *payload.EffectBatch {
	return payload.NewEffectBatch(turnID, vocabulary.BandPartial, intents)
}

func intPtr(v int) *int { return &v }

func objectsOf(call mergeCall, predicate vocabulary.Predicate) []any {
	var out []any
	for _, triple := range call.triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

// ---------------------------------------------------------------- validation

// Rejection is a normal path with a machine-readable reason, and the reason
// must distinguish "this intent is nonsense" from "the entity it names is not
// there" from "that predicate cannot live on that kind of thing". The turn's
// recorded failure is a CODE, so the code has to be produced here.
func TestApply_RejectsEachFailureClassWithItsOwnReason(t *testing.T) {
	cases := []struct {
		name   string
		intent payload.EffectIntent
		reason vocabulary.FailureReason
		names  string
	}{
		{
			name: "out of vocabulary effect type",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectType("delete_entity"), Target: rook,
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  "delete_entity",
		},
		{
			name: "out of vocabulary status",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.Status("poisoned"),
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  "poisoned",
		},
		{
			name: "out of vocabulary relation",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: rook,
				Relation: vocabulary.Relation("married-to"), Object: wren,
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  "married-to",
		},
		{
			name: "value above the registered bound",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: rook,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(900),
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  "900",
		},
		{
			name: "value below the registered bound",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: rook,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(-1),
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  "-1",
		},
		{
			name: "fields belonging to another effect type",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: rook,
				Status: vocabulary.StatusWounded, Location: courtyard,
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  "location",
		},
		{
			name: "target that does not exist",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: absent,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
			},
			reason: vocabulary.FailureEffectEntityMissing,
			names:  absent,
		},
		{
			name: "referenced object that does not exist",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: rook,
				Relation: vocabulary.RelationKnows, Object: absent,
			},
			reason: vocabulary.FailureEffectEntityMissing,
			names:  absent,
		},
		{
			name: "attribute on the wrong kind of entity",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: gatehouse,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
			},
			reason: vocabulary.FailureEffectEntityKind,
			names:  gatehouse,
		},
		{
			name: "status on an item",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: crowbar, Status: vocabulary.StatusWounded,
			},
			reason: vocabulary.FailureEffectEntityKind,
			names:  crowbar,
		},
		{
			name: "moved into something that is not a place",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectMoveEntity, Target: rook, Location: crowbar,
			},
			reason: vocabulary.FailureEffectEntityKind,
			names:  crowbar,
		},
		{
			name: "carrying a person",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: rook,
				Relation: vocabulary.RelationCarries, Object: wren,
			},
			reason: vocabulary.FailureEffectEntityKind,
			names:  wren,
		},
		{
			name: "a scene moved into a scene",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectMoveEntity, Target: gatehouse, Location: courtyard,
			},
			reason: vocabulary.FailureEffectEntityKind,
			names:  gatehouse,
		},
		{
			name: "an entity related to itself",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: rook,
				Relation: vocabulary.RelationKnows, Object: rook,
			},
			reason: vocabulary.FailureEffectInvalid,
			names:  rook,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := starterWorld()

			_, err := newApplier(t, store).Apply(t.Context(), batchOf(tc.intent), turnEntityID, batchRef)
			if err == nil {
				t.Fatal("the intent was applied")
			}
			reason, ok := effect.FailureReasonFor(err)
			if !ok {
				t.Fatalf("rejection %v carries no failure reason code", err)
			}
			if reason != tc.reason {
				t.Fatalf("rejection reason = %q, want %q (%v)", reason, tc.reason, err)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection %q does not name %q", err.Error(), tc.names)
			}
			if len(store.merges) != 0 {
				t.Fatalf("a rejected batch issued %d writes; the world must be unchanged", len(store.merges))
			}
		})
	}
}

// A referential stub is queryable and carries none of the entity's own facts.
// Treating "the ID resolves" as "the entity is there" is how a turn commits a
// change to something that was never born — and the stub marker triple persists
// after real birth, so only the envelope answers the question.
func TestApply_RefusesAStubTargetEvenThoughItIsQueryable(t *testing.T) {
	store := starterWorld()
	store.entities[rations] = &graph.EntityState{
		ID:          rations,
		MessageType: graph.StubMessageType,
		Triples: []message.Triple{
			{Subject: rations, Predicate: graph.PredStubMarker, Object: true, Confidence: 1.0},
			{Subject: rations, Predicate: graph.PredStubReferencedBy, Object: rook, Confidence: 1.0},
		},
	}

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetAttribute, Target: rations,
		Attribute: vocabulary.AttributeQuantity, Value: intPtr(2),
	}), turnEntityID, batchRef)

	if err == nil {
		t.Fatal("an effect was applied to a referential stub")
	}
	if reason, _ := effect.FailureReasonFor(err); reason != vocabulary.FailureEffectEntityMissing {
		t.Fatalf("stub rejection reason = %q, want %q", reason, vocabulary.FailureEffectEntityMissing)
	}
	if len(store.merges) != 0 {
		t.Fatal("the applier wrote to a stub")
	}
}

// The all-or-nothing this lane can actually offer: every intent is checked
// before any write is issued, so a batch that fails validation at its last
// intent leaves nothing behind.
func TestApply_ValidatesEveryIntentBeforeIssuingAnyWrite(t *testing.T) {
	store := starterWorld()

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: rook,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		payload.EffectIntent{Type: vocabulary.EffectMoveEntity, Target: rook, Location: courtyard},
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: crowbar,
			Attribute: vocabulary.AttributeQuantity, Value: intPtr(500),
		},
	), turnEntityID, batchRef)

	if err == nil {
		t.Fatal("the batch was applied despite an invalid third intent")
	}
	if len(store.merges) != 0 {
		t.Fatalf("the applier issued %d writes before finishing validation: %+v", len(store.merges), store.merges)
	}
}

// One target, one single-valued predicate, two values in one batch is not two
// facts — it is one fact the batch cannot decide, and the graph would keep
// whichever the writer happened to send last.
func TestApply_RejectsABatchThatContradictsItself(t *testing.T) {
	cases := []struct {
		name    string
		intents []payload.EffectIntent
	}{
		{
			name: "two values for one attribute",
			intents: []payload.EffectIntent{
				{
					Type: vocabulary.EffectSetAttribute, Target: rook,
					Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
				},
				{
					Type: vocabulary.EffectSetAttribute, Target: rook,
					Attribute: vocabulary.AttributeHealth, Value: intPtr(6),
				},
			},
		},
		{
			name: "two destinations for one entity",
			intents: []payload.EffectIntent{
				{Type: vocabulary.EffectMoveEntity, Target: rook, Location: courtyard},
				{Type: vocabulary.EffectMoveEntity, Target: rook, Location: gatehouse},
			},
		},
		{
			name: "the same relationship asserted twice",
			intents: []payload.EffectIntent{
				{
					Type: vocabulary.EffectAddRelationship, Target: rook,
					Relation: vocabulary.RelationCarries, Object: rations,
				},
				{
					Type: vocabulary.EffectAddRelationship, Target: rook,
					Relation: vocabulary.RelationCarries, Object: rations,
				},
			},
		},
		{
			name: "one relationship added and retracted",
			intents: []payload.EffectIntent{
				{
					Type: vocabulary.EffectAddRelationship, Target: rook,
					Relation: vocabulary.RelationCarries, Object: rations,
				},
				{
					Type: vocabulary.EffectRemoveRelationship, Target: rook,
					Relation: vocabulary.RelationCarries, Object: rations,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := starterWorld()

			_, err := newApplier(t, store).Apply(t.Context(), batchOf(tc.intents...), turnEntityID, batchRef)
			if err == nil {
				t.Fatal("a self-contradicting batch was applied")
			}
			if reason, _ := effect.FailureReasonFor(err); reason != vocabulary.FailureEffectInvalid {
				t.Fatalf("reason = %q, want %q", reason, vocabulary.FailureEffectInvalid)
			}
			if len(store.merges) != 0 {
				t.Fatal("the applier wrote a batch it could not decide")
			}
		})
	}
}

// Two DIFFERENT single-valued facts on one target, and two different objects of
// one multi-valued predicate, are ordinary and must go through.
func TestApply_AllowsSeveralDistinctFactsOnOneTarget(t *testing.T) {
	store := starterWorld()

	outcome, err := newApplier(t, store).Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: rook,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		payload.EffectIntent{Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded},
		payload.EffectIntent{
			Type: vocabulary.EffectAddRelationship, Target: rook,
			Relation: vocabulary.RelationCarries, Object: rations,
		},
	), turnEntityID, batchRef)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !outcome.Applied {
		t.Fatal("the batch reported nothing applied")
	}

	call, ok := store.mergeFor(rook)
	if !ok {
		t.Fatal("no write reached the target")
	}
	if got := objectsOf(call, vocabulary.CharacterAttributeHealth); len(got) != 1 || got[0] != 4 {
		t.Fatalf("health written as %v, want [4]", got)
	}
	if got := objectsOf(call, vocabulary.CharacterStatusCurrent); len(got) != 1 ||
		got[0] != string(vocabulary.StatusWounded) {
		t.Fatalf("status written as %v", got)
	}
}

// ------------------------------------------------------------ commit shaping

// The merge lane replaces a predicate's WHOLE value set. So adding one carried
// item means publishing every carried item, and a writer that sends only the
// new one deletes the rest with a success response. This is F14 wearing its
// second hat, and it is invisible to any test that starts from an empty entity.
func TestApply_AddingARelationshipPreservesTheSiblingsTheMergeLaneWouldReplace(t *testing.T) {
	store := starterWorld()

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectAddRelationship, Target: rook,
		Relation: vocabulary.RelationCarries, Object: rations,
	}), turnEntityID, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	call, ok := store.mergeFor(rook)
	if !ok {
		t.Fatal("no write reached the target")
	}
	carried := map[any]bool{}
	for _, object := range objectsOf(call, vocabulary.WorldRelationCarries) {
		carried[object] = true
	}
	for _, want := range []string{crowbar, lantern, rations} {
		if !carried[want] {
			t.Fatalf("the write carries %v; %q was dropped — the merge lane replaces the whole set",
				objectsOf(call, vocabulary.WorldRelationCarries), want)
		}
	}
	if len(carried) != 3 {
		t.Fatalf("the write carries %v, want exactly the three items", carried)
	}
	// The untouched relationship must not be rewritten at all: a predicate
	// absent from the request is left alone, and touching it would put this
	// writer in charge of a fact it has no intent for.
	if got := objectsOf(call, vocabulary.WorldRelationKnows); len(got) != 0 {
		t.Fatalf("the write touched an untouched predicate: %v", got)
	}
}

// Completing the set is a lane constraint; RESTAMPING it is not. The merge
// lane forces every sibling of a multi-valued predicate back onto the wire, and
// graph.MergeTriples copies what it is given verbatim — so per-triple
// provenance is entirely the caller's to keep or destroy. A writer that rebuilt
// the siblings from their objects alone would answer "when did Rook pick up the
// crowbar?" with the turn he picked up the rations, and the chronicler and the
// continuity checker both read provenance.
func TestApply_KeepsTheProvenanceOfSiblingsItOnlyHadToRepublish(t *testing.T) {
	const (
		importSource  = "world-import"
		importContext = "starter@0.1.0"
	)
	importedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	imported := func(predicate vocabulary.Predicate, object any) message.Triple {
		return message.Triple{
			Subject: rook, Predicate: predicate.String(), Object: object,
			Source: importSource, Timestamp: importedAt, Confidence: 1.0,
			Context: importContext, Datatype: message.EntityReferenceDatatype,
		}
	}

	store := starterWorld()
	store.entities[rook] = entityState(rook, vocabulary.EntityKindCharacter,
		imported(vocabulary.WorldRelationCarries, crowbar),
		imported(vocabulary.WorldRelationCarries, lantern),
	)

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectAddRelationship, Target: rook,
		Relation: vocabulary.RelationCarries, Object: rations,
	}), turnEntityID, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	call, ok := store.mergeFor(rook)
	if !ok {
		t.Fatal("no write reached the target")
	}
	written := map[any]message.Triple{}
	for _, triple := range call.triples {
		if triple.Predicate == vocabulary.WorldRelationCarries.String() {
			written[triple.Object] = triple
		}
	}
	if len(written) != 3 {
		t.Fatalf("the write carries %d items, want three", len(written))
	}

	for _, untouched := range []string{crowbar, lantern} {
		got := written[untouched]
		if got.Source != importSource || !got.Timestamp.Equal(importedAt) || got.Context != importContext {
			t.Fatalf("republished %s as (source %q, at %s, context %q); "+
				"the applier restamped a fact it did not change",
				untouched, got.Source, got.Timestamp, got.Context)
		}
	}

	added := written[rations]
	if added.Source != effect.Source || !added.Timestamp.Equal(applyTime) || added.Context != turnID {
		t.Fatalf("the newly added item carries (source %q, at %s, context %q), "+
			"want this turn's own provenance", added.Source, added.Timestamp, added.Context)
	}
	if added.Datatype != message.EntityReferenceDatatype {
		t.Fatalf("the newly added item carries datatype %q; the graph would check it by shape rather than "+
			"as an entity id", added.Datatype)
	}
}

// Re-application after a partial commit re-adds an object the world already
// holds. That is a no-op on the SET, so it must be a no-op on the fact's
// history too: the answer to "when did this happen" is the turn that first
// committed it, not whichever retry converged it.
func TestApply_ReAddingAnObjectTheWorldAlreadyHoldsDoesNotRewriteItsHistory(t *testing.T) {
	firstApply := applyTime.Add(-time.Hour)
	store := starterWorld()
	store.entities[rook] = entityState(rook, vocabulary.EntityKindCharacter,
		message.Triple{
			Subject: rook, Predicate: vocabulary.WorldRelationCarries.String(), Object: crowbar,
			Source: effect.Source, Timestamp: firstApply, Confidence: 1.0, Context: "turn-act-0",
			Datatype: message.EntityReferenceDatatype,
		},
	)

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectAddRelationship, Target: rook,
		Relation: vocabulary.RelationCarries, Object: crowbar,
	}), turnEntityID, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	call, _ := store.mergeFor(rook)
	for _, triple := range call.triples {
		if triple.Predicate != vocabulary.WorldRelationCarries.String() {
			continue
		}
		if !triple.Timestamp.Equal(firstApply) || triple.Context != "turn-act-0" {
			t.Fatalf("a convergent re-add moved the fact to (at %s, context %q); "+
				"the world did not change, so its history must not either",
				triple.Timestamp, triple.Context)
		}
	}
}

func TestApply_RemovingARelationshipKeepsTheOtherValues(t *testing.T) {
	store := starterWorld()

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectRemoveRelationship, Target: rook,
		Relation: vocabulary.RelationCarries, Object: crowbar,
	}), turnEntityID, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	call, _ := store.mergeFor(rook)
	got := objectsOf(call, vocabulary.WorldRelationCarries)
	if len(got) != 1 || got[0] != lantern {
		t.Fatalf("after putting down the crowbar the write carries %v, want [%s]", got, lantern)
	}
}

// Retracting the LAST value of a predicate is the one thing the merge's add
// list cannot say: sending no triples for a predicate leaves it exactly as it
// was. Without the removal list, "Rook put down the last thing he was carrying"
// commits as "Rook is still carrying it".
func TestApply_RemovingTheLastValueClearsThePredicate(t *testing.T) {
	store := starterWorld()

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectRemoveRelationship, Target: rook,
		Relation: vocabulary.RelationKnows, Object: wren,
	}), turnEntityID, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	call, _ := store.mergeFor(rook)
	if len(objectsOf(call, vocabulary.WorldRelationKnows)) != 0 {
		t.Fatal("the cleared predicate was re-asserted")
	}
	found := false
	for _, cleared := range call.cleared {
		if cleared == vocabulary.WorldRelationKnows.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("the write cleared %v; the emptied predicate would survive", call.cleared)
	}
}

// A batch touching N entities is N merge calls, one per target, and each
// request carries only its own subject. graph-ingest splits a foreign subject
// off onto the APPENDING lane and logs the failure without returning it, so a
// mixed request would silently accumulate values on the entity it strayed onto.
func TestApply_IssuesOneMergePerTargetAndNeverSendsAForeignSubject(t *testing.T) {
	store := starterWorld()

	outcome, err := newApplier(t, store).Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: rook,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: crowbar,
			Attribute: vocabulary.AttributeQuantity, Value: intPtr(0),
		},
		payload.EffectIntent{Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded},
	), turnEntityID, batchRef)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Two targets plus the turn entity's batch marker.
	if len(store.merges) != 3 {
		t.Fatalf("issued %d merges, want one per target plus the turn marker: %+v", len(store.merges), store.merges)
	}
	for _, call := range store.merges {
		for _, triple := range call.triples {
			if triple.Subject != call.entityID {
				t.Fatalf("a merge into %s carried a triple for %s; graph-ingest would append it elsewhere",
					call.entityID, triple.Subject)
			}
		}
	}
	if len(outcome.Targets) != 2 || outcome.Targets[0] != rook || outcome.Targets[1] != crowbar {
		t.Fatalf("outcome targets = %v, want the two targets in first-touch order", outcome.Targets)
	}
	if store.merges[len(store.merges)-1].entityID != turnEntityID {
		t.Fatal("the batch marker was written before the effects; a crash between them would mark an unapplied turn")
	}
}

// Effects are the world's facts and the batch marker is the turn's paperwork.
// The marker must be exactly the identity plus the reference — the intent list
// is bulky, is read only by the applier and the ledger, and would put the
// adjudicator's proposals on the graph's rule-matching surface.
func TestApply_MarksTheTurnWithTheDerivedBatchIdentityAndTheReference(t *testing.T) {
	store := starterWorld()

	outcome, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded,
	}), turnEntityID, batchRef)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if outcome.BatchID != payload.BatchIDForTurn(turnID) {
		t.Fatalf("outcome batch id = %q, want the id derived from the turn", outcome.BatchID)
	}

	call, ok := store.mergeFor(turnEntityID)
	if !ok {
		t.Fatal("the turn was never marked; a duplicate trigger would apply the batch again")
	}
	got := map[string]any{}
	for _, triple := range call.triples {
		got[triple.Predicate] = triple.Object
	}
	if got[vocabulary.TurnEffectsBatch.String()] != outcome.BatchID {
		t.Fatalf("marker identity = %v, want %q", got[vocabulary.TurnEffectsBatch.String()], outcome.BatchID)
	}
	if got[vocabulary.TurnEffectsRef.String()] != batchRef {
		t.Fatalf("marker reference = %v, want %q", got[vocabulary.TurnEffectsRef.String()], batchRef)
	}
	if len(got) != 2 {
		t.Fatalf("the marker wrote %v; only the identity and the reference belong on the turn", got)
	}
}

// --------------------------------------------------------------- idempotency

func TestApply_DoesNotReapplyABatchTheTurnAlreadyRecords(t *testing.T) {
	store := starterWorld()
	store.entities[turnEntityID].Triples = append(store.entities[turnEntityID].Triples,
		fact(turnEntityID, vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(turnID)),
		fact(turnEntityID, vocabulary.TurnEffectsRef, batchRef),
	)

	outcome, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetAttribute, Target: rook,
		Attribute: vocabulary.AttributeHealth, Value: intPtr(1),
	}), turnEntityID, batchRef)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if outcome.Applied {
		t.Fatal("a duplicate trigger applied the batch a second time")
	}
	if outcome.BatchID != payload.BatchIDForTurn(turnID) {
		t.Fatalf("outcome batch id = %q", outcome.BatchID)
	}
	if len(store.merges) != 0 {
		t.Fatalf("a duplicate trigger issued %d writes", len(store.merges))
	}
}

// The turn entity and the batch arrive as independent arguments, and every
// guard here reads exactly one of them: the batch identity comes from the
// payload, the marker lands on the entity. A wiring bug pairing turn A's entity
// with turn B's batch therefore passes both guards, commits real world changes,
// and permanently poisons A — a later legitimate batch for A derives a
// different identity and is refused as "already records batch X" — while B is
// left unmarked and applies again on its next trigger.
func TestApply_RefusesABatchPairedWithAnotherTurnsEntity(t *testing.T) {
	store := starterWorld()
	foreignTurn := "c360.semmachina.eff1.starter.turn.turn-act-9"
	store.entities[foreignTurn] = &graph.EntityState{
		ID:          foreignTurn,
		MessageType: message.Type{Domain: "semmachina", Category: "turn", Version: "v1"},
		Triples:     []message.Triple{fact(foreignTurn, vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseApplying))},
	}

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded,
	}), foreignTurn, batchRef)

	if err == nil {
		t.Fatal("a batch was applied against another turn's entity")
	}
	if !strings.Contains(err.Error(), turnID) || !strings.Contains(err.Error(), "turn-act-9") {
		t.Fatalf("failure %q does not name both turns", err)
	}
	if len(store.merges) != 0 {
		t.Fatalf("the mismatch was detected after %d write(s); the world was changed", len(store.merges))
	}
}

// A turn carrying somebody ELSE's batch is not idempotency, it is a turn whose
// identity derivation disagrees with its own record.
func TestApply_RefusesATurnRecordingADifferentBatch(t *testing.T) {
	store := starterWorld()
	store.entities[turnEntityID].Triples = append(store.entities[turnEntityID].Triples,
		fact(turnEntityID, vocabulary.TurnEffectsBatch, "batch-turn-act-9"),
		fact(turnEntityID, vocabulary.TurnEffectsRef, batchRef),
	)

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef)
	if err == nil {
		t.Fatal("the applier accepted a turn recording a foreign batch")
	}
	if !strings.Contains(err.Error(), "batch-turn-act-9") {
		t.Fatalf("failure %q does not name the recorded batch", err.Error())
	}
	if len(store.merges) != 0 {
		t.Fatal("the applier wrote over a foreign batch record")
	}
}

// The marker is an identity AND a reference, and the identity alone cannot tell
// a re-trigger from a re-STORE: the identity is derived from the turn, so a
// second attempt that stored its payload at a new location derives the same id
// and would be waved through as applied — orphaning the reference nothing on
// the turn points at, which is precisely what the ledger and replay read.
func TestApply_RefusesATurnRecordingADifferentReferenceForThisBatch(t *testing.T) {
	store := starterWorld()
	const recordedRef = "obj://effects/batch-turn-act-1-first"
	store.entities[turnEntityID].Triples = append(store.entities[turnEntityID].Triples,
		fact(turnEntityID, vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(turnID)),
		fact(turnEntityID, vocabulary.TurnEffectsRef, recordedRef),
	)

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef)
	if err == nil {
		t.Fatal("a turn recording a different payload reference was reported as applied")
	}
	if !strings.Contains(err.Error(), recordedRef) || !strings.Contains(err.Error(), batchRef) {
		t.Fatalf("failure %q does not name both references", err)
	}
	if len(store.merges) != 0 {
		t.Fatal("the applier wrote over a recorded batch reference")
	}
}

// Two batch identities on one turn is the signature of a write that went down
// the APPEND lane. Reading the first value would be reading a coin flip.
func TestApply_RefusesATurnHoldingTwoBatchIdentities(t *testing.T) {
	store := starterWorld()
	store.entities[turnEntityID].Triples = append(store.entities[turnEntityID].Triples,
		fact(turnEntityID, vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(turnID)),
		fact(turnEntityID, vocabulary.TurnEffectsBatch, "batch-turn-act-9"),
		fact(turnEntityID, vocabulary.TurnEffectsRef, batchRef),
	)

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef)
	if err == nil {
		t.Fatal("a turn holding two batch identities was accepted")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Fatalf("failure %q does not report how many values it found", err.Error())
	}
}

// Half a marker is a half-written record: the identity without the reference
// means the batch payload is unreachable, which the ledger and replay need.
func TestApply_RefusesAHalfWrittenMarker(t *testing.T) {
	store := starterWorld()
	store.entities[turnEntityID].Triples = append(store.entities[turnEntityID].Triples,
		fact(turnEntityID, vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(turnID)),
	)

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef); err == nil {
		t.Fatal("a turn carrying an identity with no reference was treated as applied")
	}
}

func TestApply_RefusesAStubTurnEntity(t *testing.T) {
	store := starterWorld()
	store.entities[turnEntityID] = &graph.EntityState{ID: turnEntityID, MessageType: graph.StubMessageType}

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef); err == nil {
		t.Fatal("a stub turn was read for its batch state; 'no batch recorded' would be a false negative")
	}
}

// ------------------------------------------------------------ partial commit

// The merge lane is per-entity and has no failed-subject list, so a multi-target
// batch is not atomic at the transport. A write that fails after its
// predecessors committed must fail the TURN, naming the target that did not
// land — never be reported as applied.
func TestApply_ReportsAPartialCommitWithTheFailedTargetNamed(t *testing.T) {
	store := starterWorld()
	store.failOn[crowbar] = errors.New("merge rejected")

	outcome, err := newApplier(t, store).Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: rook,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: crowbar,
			Attribute: vocabulary.AttributeQuantity, Value: intPtr(0),
		},
	), turnEntityID, batchRef)

	if err == nil {
		t.Fatal("a partial commit was reported as success")
	}
	if outcome.Applied {
		t.Fatal("a partial commit reported the batch as applied")
	}
	if reason, _ := effect.FailureReasonFor(err); reason != vocabulary.FailureEffectCommitIncomplete {
		t.Fatalf("reason = %q, want %q", reason, vocabulary.FailureEffectCommitIncomplete)
	}

	var commit *effect.CommitError
	if !errors.As(err, &commit) {
		t.Fatalf("failure %v is not a *effect.CommitError; recovery cannot tell what landed", err)
	}
	if commit.Target != crowbar {
		t.Fatalf("failure names target %q, want %q", commit.Target, crowbar)
	}
	if len(commit.Committed) != 1 || commit.Committed[0] != rook {
		t.Fatalf("failure records committed targets %v, want [%s]", commit.Committed, rook)
	}
	// The turn must NOT be marked: a marked turn would make the next trigger
	// skip the batch and leave the half-written world in place forever.
	if _, marked := store.mergeFor(turnEntityID); marked {
		t.Fatal("the turn was marked applied after a partial commit")
	}
}

// The marker write is the last step, so its failure leaves a fully applied
// world and an unmarked turn. Re-application is convergent, but the turn must
// not be reported as applied either.
func TestApply_ReportsAFailedMarkerWriteAsAnIncompleteCommit(t *testing.T) {
	store := starterWorld()
	store.failOn[turnEntityID] = errors.New("merge rejected")

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded,
	}), turnEntityID, batchRef)

	if err == nil {
		t.Fatal("a failed marker write was reported as success")
	}
	var commit *effect.CommitError
	if !errors.As(err, &commit) || commit.Target != turnEntityID {
		t.Fatalf("failure %v does not name the turn entity as the write that did not land", err)
	}
}

// ------------------------------------------------------- failure classification

// Not every Apply failure is a turn OUTCOME, and the difference has to be a
// decided contract rather than something 6.3 discovers by hitting it. A
// corrupt turn record and a broken read are refusals to answer: recording a
// reason means writing to the very record just declared untrustworthy, and a
// NATS timeout would burn a player's turn for a connection fault. So they carry
// no code, and the caller escalates or retries instead of failing the turn.
func TestFailureReasonFor_LeavesTheAnomalyAndTransportClassesUnclassified(t *testing.T) {
	uncoded := map[string]func(*fakeStore){
		"a turn holding two batch identities": func(s *fakeStore) {
			s.entities[turnEntityID].Triples = append(s.entities[turnEntityID].Triples,
				fact(turnEntityID, vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(turnID)),
				fact(turnEntityID, vocabulary.TurnEffectsBatch, "batch-turn-act-9"),
				fact(turnEntityID, vocabulary.TurnEffectsRef, batchRef),
			)
		},
		"a half-written marker": func(s *fakeStore) {
			s.entities[turnEntityID].Triples = append(s.entities[turnEntityID].Triples,
				fact(turnEntityID, vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(turnID)))
		},
		"a turn recording a foreign batch": func(s *fakeStore) {
			s.entities[turnEntityID].Triples = append(s.entities[turnEntityID].Triples,
				fact(turnEntityID, vocabulary.TurnEffectsBatch, "batch-turn-act-9"),
				fact(turnEntityID, vocabulary.TurnEffectsRef, batchRef),
			)
		},
		"a stub turn entity": func(s *fakeStore) {
			s.entities[turnEntityID] = &graph.EntityState{ID: turnEntityID, MessageType: graph.StubMessageType}
		},
		"an unreadable turn entity": func(s *fakeStore) {
			delete(s.entities, turnEntityID)
		},
	}

	for name, damage := range uncoded {
		t.Run(name, func(t *testing.T) {
			store := starterWorld()
			damage(store)

			_, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef)
			if err == nil {
				t.Fatal("the damaged turn record was accepted")
			}
			reason, coded := effect.FailureReasonFor(err)
			if coded {
				t.Fatalf("failure %q was classified as turn reason %q; recording it would write a verdict "+
					"onto the record whose readability the applier just disputed", err, reason)
			}
			if reason != "" {
				t.Fatalf("an unclassified failure returned reason %q", reason)
			}
		})
	}

	// The other half of the contract: everything that IS a turn outcome carries
	// its code, so the false branch cannot quietly widen into the default.
	store := starterWorld()
	_, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetStatus, Target: absent, Status: vocabulary.StatusWounded,
	}), turnEntityID, batchRef)
	if reason, coded := effect.FailureReasonFor(err); !coded || reason != vocabulary.FailureEffectEntityMissing {
		t.Fatalf("a rejection classified as (%q, %v), want a coded %q",
			reason, coded, vocabulary.FailureEffectEntityMissing)
	}
}

// ------------------------------------------------------------- construction

func TestNewApplier_RefusesAnUnusableConfiguration(t *testing.T) {
	if _, err := effect.NewApplier(nil); err == nil {
		t.Fatal("an applier with no graph surface was built")
	}
	if _, err := effect.NewApplier(starterWorld(), effect.WithClock(nil)); err == nil {
		t.Fatal("an applier with no clock was built")
	}
	// The zero AttributeSpecSet is a legal Go value and an illegal bounds
	// contract: every Check against it fails as "unknown attribute", so an
	// applier holding one would reject every set_attribute effect in the game
	// and blame the vocabulary.
	if _, err := effect.NewApplier(starterWorld(),
		effect.WithAttributeSpecs(vocabulary.AttributeSpecSet{})); err == nil {
		t.Fatal("an applier with an empty bounds contract was built")
	}
}

// Bounds are WORLD data — a grim survival world wants health 0-4 where a heroic
// one wants 0-40 — so the applier has to be handed a set rather than read a
// package-level default. Taking it at construction is what makes a world-scoped
// set a wiring change instead of a second rewrite of the bounds contract.
func TestApply_EnforcesTheBoundsItWasConstructedWithRatherThanTheEngineDefaults(t *testing.T) {
	grim := map[vocabulary.Attribute]vocabulary.AttributeBounds{
		vocabulary.AttributeHealth:   {Min: 0, Max: 4},
		vocabulary.AttributeStamina:  {Min: 0, Max: 10},
		vocabulary.AttributeResolve:  {Min: 0, Max: 10},
		vocabulary.AttributeQuantity: {Min: 0, Max: 99},
		vocabulary.AttributeTension:  {Min: 0, Max: 10},
	}
	specs, err := vocabulary.NewAttributeSpecSet(grim)
	if err != nil {
		t.Fatalf("NewAttributeSpecSet: %v", err)
	}

	// Legal under the engine defaults (0-10), so the payload's own validation
	// passes it and only the applier's world-scoped set can refuse it.
	overHealed := payload.EffectIntent{
		Type: vocabulary.EffectSetAttribute, Target: rook,
		Attribute: vocabulary.AttributeHealth, Value: intPtr(8),
	}
	if err := overHealed.Validate(); err != nil {
		t.Fatalf("the fixture is not legal under the engine defaults, so it proves nothing: %v", err)
	}

	store := starterWorld()
	applier, err := effect.NewApplier(store,
		effect.WithClock(func() time.Time { return applyTime }),
		effect.WithAttributeSpecs(specs))
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}

	_, err = applier.Apply(t.Context(), batchOf(overHealed), turnEntityID, batchRef)
	if err == nil {
		t.Fatal("a value outside this world's bounds was committed under the engine defaults")
	}
	if reason, _ := effect.FailureReasonFor(err); reason != vocabulary.FailureEffectInvalid {
		t.Fatalf("reason = %q, want %q", reason, vocabulary.FailureEffectInvalid)
	}
	var bounds *vocabulary.OutOfBoundsError
	if !errors.As(err, &bounds) {
		t.Fatalf("failure %v does not name the bound it broke", err)
	}
	if bounds.Max != 4 {
		t.Fatalf("the rejection cites max %d; the applier checked the engine defaults, not its own set", bounds.Max)
	}
	if len(store.merges) != 0 {
		t.Fatal("the world was changed by a batch that failed validation")
	}

	// The same set must still ACCEPT what the world allows, or the test above
	// would pass on an applier that rejects everything.
	if _, err := applier.Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetAttribute, Target: rook,
		Attribute: vocabulary.AttributeHealth, Value: intPtr(3),
	}), turnEntityID, batchRef); err != nil {
		t.Fatalf("a value inside this world's bounds was refused: %v", err)
	}
}

func TestApply_RefusesAnUnusableCall(t *testing.T) {
	applier := newApplier(t, starterWorld())

	if _, err := applier.Apply(t.Context(), nil, turnEntityID, batchRef); err == nil {
		t.Fatal("a nil batch was applied")
	}
	if _, err := applier.Apply(t.Context(), batchOf(), "not-an-entity", batchRef); err == nil {
		t.Fatal("a non-canonical turn entity id was accepted")
	}
	if _, err := applier.Apply(t.Context(), batchOf(), turnEntityID, ""); err == nil {
		t.Fatal("a batch with no stored reference was applied; the payload would be unreachable")
	}
}

// An empty band is normal — a verdict may declare no world change — and it
// still has to mark the turn, or the rule chain stalls waiting for effects that
// were never going to come.
func TestApply_MarksTheTurnForAnEmptyBatch(t *testing.T) {
	store := starterWorld()

	outcome, err := newApplier(t, store).Apply(t.Context(), batchOf(), turnEntityID, batchRef)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !outcome.Applied || len(outcome.Targets) != 0 {
		t.Fatalf("empty batch outcome = %+v", outcome)
	}
	if len(store.merges) != 1 || store.merges[0].entityID != turnEntityID {
		t.Fatalf("an empty batch issued %+v, want only the turn marker", store.merges)
	}
}

// ----------------------------------------------------------------- closure

// Every effect type must write a predicate the kind rules actually cover.
//
// A predicate with no registered subject-kind rule fails AllowsSubjectKind for
// every kind, so its effect type would be permanently unappliable — a member of
// the closed vocabulary the adjudicator can propose, the payload accepts, and
// the applier can only ever reject. The fixture map is keyed by the closed set,
// so a new effect type arrives here as a test failure rather than as a dead
// vocabulary member discovered in play.
func TestEveryEffectType_WritesAPredicateTheKindRulesCover(t *testing.T) {
	fixtures := map[vocabulary.EffectType]payload.EffectIntent{
		vocabulary.EffectSetAttribute: {
			Type: vocabulary.EffectSetAttribute, Target: rook,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		vocabulary.EffectSetStatus: {
			Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded,
		},
		vocabulary.EffectMoveEntity: {
			Type: vocabulary.EffectMoveEntity, Target: rook, Location: courtyard,
		},
		vocabulary.EffectAddRelationship: {
			Type: vocabulary.EffectAddRelationship, Target: rook,
			Relation: vocabulary.RelationKnows, Object: wren,
		},
		vocabulary.EffectRemoveRelationship: {
			Type: vocabulary.EffectRemoveRelationship, Target: rook,
			Relation: vocabulary.RelationKnows, Object: wren,
		},
	}

	for _, effectType := range vocabulary.EffectTypes() {
		intent, ok := fixtures[effectType]
		if !ok {
			t.Fatalf("effect type %q has no fixture here; it has never been proven appliable", effectType)
		}
		predicate, _, err := intent.Mutation()
		if err != nil {
			t.Fatalf("effect type %q: %v", effectType, err)
		}
		if _, ok := vocabulary.SubjectKindsFor(predicate); !ok {
			t.Fatalf("effect type %q writes %q, which has no registered subject-kind rule; "+
				"the applier would reject every intent of this type forever", effectType, predicate)
		}
	}

	// Every attribute is reachable through set_attribute, so each one's
	// predicate needs the same rule.
	for _, attribute := range vocabulary.Attributes() {
		spec, ok := vocabulary.AttributeSpecFor(attribute)
		if !ok {
			t.Fatalf("attribute %q has no registered spec", attribute)
		}
		if _, ok := vocabulary.SubjectKindsFor(spec.Predicate); !ok {
			t.Fatalf("attribute %q writes %q, which no entity kind may carry", attribute, spec.Predicate)
		}
	}
}

// The engine's own bookkeeping entities — turns and campaigns — are not things
// in the fiction and carry no kind, so an intent naming one has nothing to be
// type-checked against and must be refused rather than waved through.
func TestApply_RefusesAnEffectAimedAtTheEngineOwnBookkeeping(t *testing.T) {
	store := starterWorld()

	_, err := newApplier(t, store).Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectSetAttribute, Target: turnEntityID,
		Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
	}), turnEntityID, batchRef)

	if err == nil {
		t.Fatal("an effect was applied to the turn entity")
	}
	if reason, _ := effect.FailureReasonFor(err); reason != vocabulary.FailureEffectEntityKind {
		t.Fatalf("reason = %q, want %q (%v)", reason, vocabulary.FailureEffectEntityKind, err)
	}
	if len(store.merges) != 0 {
		t.Fatal("the applier wrote a world fact onto the turn entity")
	}
}

// Each named entity is read once per batch, and that is a correctness property
// before it is a cost one: an intent set touching one entity twice would
// otherwise be folded against two different snapshots, and the second fold
// would start from a set the first fold had already changed.
func TestApply_ReadsEachNamedEntityOnce(t *testing.T) {
	store := starterWorld()

	if _, err := newApplier(t, store).Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: rook,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		payload.EffectIntent{Type: vocabulary.EffectSetStatus, Target: rook, Status: vocabulary.StatusWounded},
		payload.EffectIntent{
			Type: vocabulary.EffectAddRelationship, Target: rook,
			Relation: vocabulary.RelationCarries, Object: rations,
		},
		payload.EffectIntent{
			Type: vocabulary.EffectRemoveRelationship, Target: rook,
			Relation: vocabulary.RelationCarries, Object: crowbar,
		},
	), turnEntityID, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	counted := map[string]int{}
	for _, id := range store.reads {
		counted[id]++
	}
	for id, count := range counted {
		if count > 1 {
			t.Fatalf("entity %s was read %d times in one batch; the folds ran against different snapshots",
				id, count)
		}
	}
	// The turn, the target, and the two items the relationships name. The
	// RETRACTED item is read too: a removal names an object, and an object the
	// batch cannot resolve is an authoring error the applier surfaces rather
	// than a set-difference that quietly matches nothing.
	if len(counted) != 4 {
		t.Fatalf("the batch read %v, want the turn, the target, and the two named items", store.reads)
	}

	// And the accumulation still came out right across four intents on one target.
	call, _ := store.mergeFor(rook)
	carried := objectsOf(call, vocabulary.WorldRelationCarries)
	if len(carried) != 2 {
		t.Fatalf("after picking one item up and putting another down the character carries %v", carried)
	}
}
