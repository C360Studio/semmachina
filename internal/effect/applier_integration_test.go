package effect_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// "The effect landed and replaced what was there" is a claim about the GRAPH,
// and the graph is where it can be false. The mutation API offers lanes that
// both accept the same triples: one merges by (subject, predicate) and one
// appends, and the appending one leaves a character accumulating health values
// with a success response and no error anywhere. It is also where the SECOND
// half of that trap lives — the merge lane replaces a predicate's whole value
// set, so a relationship write that sent only its new value would delete the
// siblings just as quietly. Neither is visible to a fake that has one lane.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

// Each test gets its own world namespace, so tests sharing one broker cannot
// see each other's entities.
var namespaceCounter atomic.Int64

const templateID = "starter"

// liveWorld is one imported starter world plus a turn entity in its applying
// phase — the state the applier is triggered in.
type liveWorld struct {
	harness   *testinfra.Harness
	store     *graphio.Store
	applier   *effect.Applier
	ids       map[string]string
	namespace string
	turn      string
}

// nextTurn creates a second turn entity in this world, which is what a test
// needs to write the same fact twice: the batch identity derives from the turn,
// so a genuine second write needs a genuine second turn.
func (w *liveWorld) nextTurn(t *testing.T, turnIdentity string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID("c360", w.namespace, templateID, "turn", turnIdentity)
	if err != nil {
		t.Fatalf("compose turn id: %v", err)
	}
	createTurn(t, w.store, id)
	return id
}

// entity returns the six-part ID the importer composed for a template-local id.
func (w *liveWorld) entity(localID string) string {
	id, ok := w.ids[localID]
	if !ok {
		panic("no imported entity for local id " + localID)
	}
	return id
}

func (w *liveWorld) state(t *testing.T, localID string) *graph.EntityState {
	t.Helper()
	return w.harness.AwaitEntity(t, w.entity(localID))
}

func (w *liveWorld) objects(t *testing.T, localID string, predicate vocabulary.Predicate) []any {
	t.Helper()
	return testinfra.ObjectsFor(w.state(t, localID), predicate.String())
}

// startWorld imports the shipped starter package through the REAL importer and
// real graph-ingest, then creates the turn entity the applier marks.
//
// The world is imported rather than hand-assembled because the properties under
// test are properties of the entities graph-ingest actually stores — including
// the multi-valued `carries` set the fixture authors, which is the whole reason
// the sibling-preservation trap is reachable.
func startWorld(t *testing.T) *liveWorld {
	t.Helper()
	harness := testinfra.Require(t)

	namespace := fmt.Sprintf("effw%d", namespaceCounter.Add(1))
	fsys, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("fixtures.StarterWorld: %v", err)
	}
	pkg, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	plan, err := pkg.Resolve(world.InstanceConfig{
		Org:     "c360",
		WorldNS: namespace,
		Player:  world.PlayerBinding{LocalID: "p1", Name: "Coby", Character: "local:rook"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	importer, err := world.NewImporter(harness.Client)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	if _, err := importer.Import(t.Context(), plan); err != nil {
		t.Fatalf("Import: %v", err)
	}

	ids := make(map[string]string, len(plan.Entities))
	for _, planned := range plan.Entities {
		ids[planned.Template.LocalID] = planned.ID
		// The import is asynchronous — the publish ack is durability, not
		// materialization — so every entity is awaited before an effect names it.
		harness.AwaitEntity(t, planned.ID)
	}

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	turn, err := vocabulary.ComposeEntityID("c360", namespace, templateID, "turn", turnID)
	if err != nil {
		t.Fatalf("compose turn id: %v", err)
	}
	createTurn(t, store, turn)

	applier, err := effect.NewApplier(store, effect.WithClock(func() time.Time { return applyTime }))
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	return &liveWorld{
		harness: harness, store: store, applier: applier,
		ids: ids, namespace: namespace, turn: turn,
	}
}

func createTurn(t *testing.T, store *graphio.Store, id string) {
	t.Helper()
	if _, err := store.CreateEntity(t.Context(), &graph.EntityState{
		ID:          id,
		MessageType: message.Type{Domain: payload.Domain, Category: "turn", Version: payload.SchemaVersion},
		Version:     1,
		UpdatedAt:   applyTime,
		Triples: []message.Triple{{
			Subject:    id,
			Predicate:  vocabulary.TurnPhaseCurrent.String(),
			Object:     string(vocabulary.PhaseApplying),
			Source:     effect.Source,
			Timestamp:  applyTime,
			Confidence: 1.0,
		}},
	}); err != nil {
		t.Fatalf("create turn entity: %v", err)
	}
}

// The spec's committed-effects scenario, against a real graph: two entities, a
// numeric attribute and a relocation, both queryable afterwards and both
// REPLACING what was there rather than joining it.
func TestIntegration_CommittedEffectsAreGraphVisibleAndReplacePriorValues(t *testing.T) {
	live := startWorld(t)

	before := live.objects(t, "rook", vocabulary.CharacterAttributeHealth)
	if len(before) != 1 || fmt.Sprint(before[0]) != "8" {
		t.Fatalf("the imported world starts with health %v, want [8]", before)
	}

	outcome, err := live.applier.Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: live.entity("rook"),
			Attribute: vocabulary.AttributeHealth, Value: intPtr(5),
		},
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: live.entity("gatehouse"),
			Attribute: vocabulary.AttributeTension, Value: intPtr(7),
		},
	), live.turn, batchRef)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !outcome.Applied || len(outcome.Targets) != 2 {
		t.Fatalf("outcome = %+v", outcome)
	}

	health := live.objects(t, "rook", vocabulary.CharacterAttributeHealth)
	if len(health) != 1 {
		t.Fatalf("the character holds %d health values (%v); a single-valued fact must replace", len(health), health)
	}
	// The rule engine's threshold operators compare numerically, so a value
	// stored as "5" would write cleanly and make every threshold rule over it
	// quietly never fire.
	number, ok := health[0].(float64)
	if !ok {
		t.Fatalf("health stored as %T (%v); a rule's numeric comparison would never match", health[0], health[0])
	}
	if int(number) != 5 {
		t.Fatalf("health = %v, want 5", number)
	}

	tension := live.objects(t, "gatehouse", vocabulary.SceneAttributeTension)
	if len(tension) != 1 || fmt.Sprint(tension[0]) != "7" {
		t.Fatalf("scene tension = %v, want [7]", tension)
	}

	// The facts the batch did not touch must survive untouched.
	if got := live.objects(t, "rook", vocabulary.CharacterStatusCurrent); len(got) != 1 ||
		got[0] != string(vocabulary.StatusHealthy) {
		t.Fatalf("the write disturbed an untouched fact: status = %v", got)
	}
	if got := live.objects(t, "rook", vocabulary.WorldEntityName); len(got) != 1 {
		t.Fatalf("the write disturbed the entity name: %v", got)
	}
}

// THE regression guard for F14. Applying the same single-valued effect twice
// must leave exactly ONE value. Through the appending lane this leaves two,
// both stored, with a success response, an empty failed-subject list, and no
// error anywhere — so the property is proven against real graph-ingest rather
// than against a fake that has one lane.
//
// The second application is forced past the idempotency guard on a second turn
// entity, because the guard would otherwise make this pass for the wrong reason:
// what is under test is the LANE, not the guard.
func TestIntegration_ASingleValuedEffectAppliedTwiceLeavesOneValue(t *testing.T) {
	live := startWorld(t)

	first := payload.NewEffectBatch(turnID, vocabulary.BandPartial, []payload.EffectIntent{{
		Type: vocabulary.EffectSetAttribute, Target: live.entity("rook"),
		Attribute: vocabulary.AttributeHealth, Value: intPtr(5),
	}})
	if _, err := live.applier.Apply(t.Context(), first, live.turn, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := live.objects(t, "rook", vocabulary.CharacterAttributeHealth); len(got) != 1 {
		t.Fatalf("after one application the character holds %d health values: %v", len(got), got)
	}

	// A second turn, a second batch, the same single-valued fact.
	secondTurn := live.nextTurn(t, "turn-act-2")
	second := payload.NewEffectBatch("turn-act-2", vocabulary.BandPartial, []payload.EffectIntent{{
		Type: vocabulary.EffectSetAttribute, Target: live.entity("rook"),
		Attribute: vocabulary.AttributeHealth, Value: intPtr(3),
	}})
	if _, err := live.applier.Apply(t.Context(), second, secondTurn, "obj://effects/batch-turn-act-2"); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	health := live.objects(t, "rook", vocabulary.CharacterAttributeHealth)
	if len(health) != 1 {
		t.Fatalf("two applications left %d health values (%v); the write took the appending lane, "+
			"and a character with two healths has no health", len(health), health)
	}
	if fmt.Sprint(health[0]) != "3" {
		t.Fatalf("health = %v, want the newer value 3", health[0])
	}
}

// F14's second hat, against a real graph. The starter world's courier carries
// two things; adding a third must leave three. The merge lane replaces a
// predicate's WHOLE value set, so a writer that published only the new object
// would leave the character carrying one item and would be told it succeeded.
func TestIntegration_AddingARelationshipDoesNotDropTheSiblings(t *testing.T) {
	live := startWorld(t)

	carried := live.objects(t, "rook", vocabulary.WorldRelationCarries)
	if len(carried) != 2 {
		t.Fatalf("the imported world starts with %d carried items (%v), want 2 — "+
			"this test proves nothing against a single-valued fixture", len(carried), carried)
	}

	if _, err := live.applier.Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectAddRelationship, Target: live.entity("rook"),
		Relation: vocabulary.RelationCarries, Object: live.entity("rations"),
	}), live.turn, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after := live.objects(t, "rook", vocabulary.WorldRelationCarries)
	if len(after) != 3 {
		t.Fatalf("after picking up one more item the character carries %v; the merge lane replaced "+
			"the whole set and the siblings were dropped", after)
	}
	held := map[string]bool{}
	for _, object := range after {
		held[fmt.Sprint(object)] = true
	}
	for _, local := range []string{"crowbar", "lantern", "rations"} {
		if !held[live.entity(local)] {
			t.Fatalf("the character no longer carries %s: %v", local, after)
		}
	}
}

// Completing the set is forced; RESTAMPING it is a choice, and this is where
// the choice is visible. graph.MergeTriples stores what it is handed verbatim,
// so the provenance of a sibling the applier only had to republish is entirely
// the applier's to keep — and after this turn the graph must still be able to
// answer "when did Rook pick up the crowbar, and who wrote that?" with the
// import, not with the turn he picked up the rations.
func TestIntegration_RepublishedSiblingsKeepTheProvenanceTheGraphStored(t *testing.T) {
	live := startWorld(t)

	before := map[any]message.Triple{}
	for _, triple := range live.state(t, "rook").Triples {
		if triple.Predicate == vocabulary.WorldRelationCarries.String() {
			before[triple.Object] = triple
		}
	}
	if len(before) != 2 {
		t.Fatalf("the imported world starts with %d carried items, want 2", len(before))
	}
	for object, triple := range before {
		if triple.Source != payload.WorldImportSource {
			t.Fatalf("imported %v carries source %q; the fixture cannot prove provenance survives",
				object, triple.Source)
		}
	}

	if _, err := live.applier.Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectAddRelationship, Target: live.entity("rook"),
		Relation: vocabulary.RelationCarries, Object: live.entity("rations"),
	}), live.turn, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after := map[any]message.Triple{}
	for _, triple := range live.state(t, "rook").Triples {
		if triple.Predicate == vocabulary.WorldRelationCarries.String() {
			after[triple.Object] = triple
		}
	}
	if len(after) != 3 {
		t.Fatalf("the character carries %d items after picking one up, want 3", len(after))
	}

	for object, was := range before {
		is := after[object]
		if is.Source != was.Source || !is.Timestamp.Equal(was.Timestamp) || is.Context != was.Context {
			t.Fatalf("the graph now records %v as (source %q, at %s, context %q), was (%q, %s, %q); "+
				"the applier restamped a fact this turn did not change",
				object, is.Source, is.Timestamp, is.Context, was.Source, was.Timestamp, was.Context)
		}
	}
	if added := after[live.entity("rations")]; added.Source != effect.Source || added.Context != turnID {
		t.Fatalf("the newly carried item records (source %q, context %q), want this turn's own provenance",
			added.Source, added.Context)
	}
}

// Retracting the last value of a predicate has to actually empty it. The add
// list cannot express an empty set, so this is the removal list or nothing.
func TestIntegration_RemovingTheLastRelationshipEmptiesThePredicate(t *testing.T) {
	live := startWorld(t)

	if got := live.objects(t, "rook", vocabulary.WorldRelationHostileTo); len(got) != 1 {
		t.Fatalf("the imported world starts with %d hostilities, want 1", len(got))
	}

	if _, err := live.applier.Apply(t.Context(), batchOf(payload.EffectIntent{
		Type: vocabulary.EffectRemoveRelationship, Target: live.entity("rook"),
		Relation: vocabulary.RelationHostileTo, Object: live.entity("hollis"),
	}), live.turn, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := live.objects(t, "rook", vocabulary.WorldRelationHostileTo); len(got) != 0 {
		t.Fatalf("the retracted hostility is still recorded: %v", got)
	}
	// Its neighbours must survive: clearing one predicate must not clear the
	// entity.
	if got := live.objects(t, "rook", vocabulary.WorldRelationKnows); len(got) != 1 {
		t.Fatalf("clearing one predicate disturbed another: knows = %v", got)
	}
}

// The spec's rejection scenario, against a real graph: the world is unchanged
// and the batch is not recorded on the turn. Validation completing before any
// write is what makes that true — the valid first intent must not land.
func TestIntegration_ARejectedBatchLeavesTheWorldUnchanged(t *testing.T) {
	live := startWorld(t)
	before := testinfra.TripleSet(live.state(t, "rook"))

	_, err := live.applier.Apply(t.Context(), batchOf(
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: live.entity("rook"),
			Attribute: vocabulary.AttributeHealth, Value: intPtr(2),
		},
		payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: live.entity("gatehouse"),
			Attribute: vocabulary.AttributeHealth, Value: intPtr(2),
		},
	), live.turn, batchRef)

	if err == nil {
		t.Fatal("health landed on a scene")
	}
	if reason, _ := effect.FailureReasonFor(err); reason != vocabulary.FailureEffectEntityKind {
		t.Fatalf("rejection reason = %q, want %q (%v)", reason, vocabulary.FailureEffectEntityKind, err)
	}

	after := testinfra.TripleSet(live.state(t, "rook"))
	if len(after) != len(before) {
		t.Fatalf("the rejected batch changed the world: %v became %v", before, after)
	}
	for key, count := range before {
		if after[key] != count {
			t.Fatalf("fact %v changed from %d to %d under a rejected batch", key, count, after[key])
		}
	}
	turnState := live.harness.AwaitEntity(t, live.turn)
	if got := testinfra.ObjectsFor(turnState, vocabulary.TurnEffectsBatch.String()); len(got) != 0 {
		t.Fatalf("a rejected batch marked the turn as applied: %v", got)
	}
}

// The spec's convergence scenario. A partial commit is a real outcome here —
// the merge lane is per-entity — so recovery is re-application under the same
// batch identity, and it must converge the world to the full intended state
// with the already-committed target unchanged by the replacement.
func TestIntegration_ReapplicationAfterAPartialCommitConverges(t *testing.T) {
	live := startWorld(t)

	// Force a partial commit: the second target does not exist, so its merge
	// fails after the first has already landed. The absent entity is reachable
	// only by writing around the applier's own validation, so the batch is
	// committed through a store that lets the first write through and fails the
	// second — the shape a broker blip has.
	intents := []payload.EffectIntent{
		{
			Type: vocabulary.EffectSetAttribute, Target: live.entity("rook"),
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		{
			Type: vocabulary.EffectSetAttribute, Target: live.entity("crowbar"),
			Attribute: vocabulary.AttributeQuantity, Value: intPtr(0),
		},
	}
	flaky := &flakyStore{Store: live.store, failFor: live.entity("crowbar")}
	partialApplier, err := effect.NewApplier(flaky, effect.WithClock(func() time.Time { return applyTime }))
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}

	_, err = partialApplier.Apply(t.Context(), batchOf(intents...), live.turn, batchRef)
	var commit *effect.CommitError
	if !errors.As(err, &commit) {
		t.Fatalf("Apply returned %v, want a *effect.CommitError", err)
	}
	if commit.Target != live.entity("crowbar") {
		t.Fatalf("the failure names %q, want the target whose write did not land", commit.Target)
	}

	// Half the batch is in the world and the turn is unmarked, which is exactly
	// what makes re-application both necessary and safe.
	if got := live.objects(t, "rook", vocabulary.CharacterAttributeHealth); len(got) != 1 ||
		fmt.Sprint(got[0]) != "4" {
		t.Fatalf("the committed half is not in the world: health = %v", got)
	}
	turnState := live.harness.AwaitEntity(t, live.turn)
	if got := testinfra.ObjectsFor(turnState, vocabulary.TurnEffectsBatch.String()); len(got) != 0 {
		t.Fatalf("a partially committed batch marked the turn applied: %v", got)
	}

	// Re-application under the same batch identity, now against a healthy store.
	outcome, err := live.applier.Apply(t.Context(), batchOf(intents...), live.turn, batchRef)
	if err != nil {
		t.Fatalf("re-application: %v", err)
	}
	if !outcome.Applied {
		t.Fatal("re-application skipped the batch; the turn was never marked")
	}

	if got := live.objects(t, "rook", vocabulary.CharacterAttributeHealth); len(got) != 1 ||
		fmt.Sprint(got[0]) != "4" {
		t.Fatalf("re-application disturbed the already-committed target: health = %v", got)
	}
	if got := live.objects(t, "crowbar", vocabulary.ItemAttributeQuantity); len(got) != 1 ||
		fmt.Sprint(got[0]) != "0" {
		t.Fatalf("re-application did not converge the failed target: quantity = %v", got)
	}
	if got := testinfra.ObjectsFor(live.harness.AwaitEntity(t, live.turn),
		vocabulary.TurnEffectsBatch.String()); len(got) != 1 {
		t.Fatalf("the turn records %d batch identities after recovery: %v", len(got), got)
	}
}

// A relationship intent re-applied after a partial commit must converge rather
// than reject: the world already holds the object the batch adds, and treating
// that as a duplicate would make the recovering write fail instead of settle.
func TestIntegration_ReapplyingARelationshipTheWorldAlreadyHoldsConverges(t *testing.T) {
	live := startWorld(t)

	intent := payload.EffectIntent{
		Type: vocabulary.EffectAddRelationship, Target: live.entity("rook"),
		Relation: vocabulary.RelationCarries, Object: live.entity("rations"),
	}
	if _, err := live.applier.Apply(t.Context(), batchOf(intent), live.turn, batchRef); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// A second turn replays the same intent, the way a recovering apply stage
	// would after its marker write was lost.
	secondTurn := live.nextTurn(t, "turn-act-2")
	replay := payload.NewEffectBatch("turn-act-2", vocabulary.BandPartial, []payload.EffectIntent{intent})
	if _, err := live.applier.Apply(t.Context(), replay, secondTurn, "obj://effects/batch-turn-act-2"); err != nil {
		t.Fatalf("replaying an already-held relationship failed instead of converging: %v", err)
	}

	after := live.objects(t, "rook", vocabulary.WorldRelationCarries)
	if len(after) != 3 {
		t.Fatalf("the replayed relationship left %d carried items (%v), want 3", len(after), after)
	}
}

// flakyStore is the real store with one target's merges failing. It exists for
// the partial-commit path, which a healthy broker will not produce on demand —
// every read and every other write is the real thing.
type flakyStore struct {
	*graphio.Store
	failFor string
}

func (s *flakyStore) MergeTriples(
	ctx context.Context,
	entityID string,
	triples []message.Triple,
	opts ...graphio.MergeOption,
) (*graph.EntityState, error) {
	if entityID == s.failFor {
		return nil, errors.New("simulated broker failure")
	}
	return s.Store.MergeTriples(ctx, entityID, triples, opts...)
}
