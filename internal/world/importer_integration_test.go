package world_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// worldNamespace gives each integration test its own world namespace, so tests
// sharing one broker cannot see each other's entities. Disjointness by
// namespace is the property under test in the two-worlds requirement, so the
// isolation mechanism and the assertion are the same mechanism.
var worldCounter atomic.Int64

func worldNamespace(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("itw%d", worldCounter.Add(1))
}

func starterPlan(t *testing.T, namespace string) *world.Plan {
	t.Helper()
	instance := starterInstance()
	instance.WorldNS = namespace

	plan, err := starterPackage(t).Resolve(instance)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return plan
}

// The spec's "Import is graph-visible and single-writer" scenario, against a
// real NATS with a real graph-ingest. Every entity is asserted whole — the
// projected kind triple, the literals, and the rewritten references — because
// "it showed up" is a much weaker claim than "it showed up correct".
func TestIntegration_ImportIsGraphVisible(t *testing.T) {
	harness := requireInfra(t)
	namespace := worldNamespace(t)
	plan := starterPlan(t, namespace)

	stamp := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	importer, err := world.NewImporter(harness.client, world.WithClock(func() time.Time { return stamp }))
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	result, err := importer.Import(t.Context(), plan)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !reflect.DeepEqual(result.Entities, plan.IDs()) {
		t.Fatalf("import reported %v, plan declared %v", result.Entities, plan.IDs())
	}
	if len(result.Sequences) != len(plan.Entities) {
		t.Fatalf("broker acknowledged %d of %d entities", len(result.Sequences), len(plan.Entities))
	}

	for _, planned := range plan.Entities {
		state := harness.awaitEntity(t, planned.ID)

		if state.ID != planned.ID {
			t.Fatalf("queried %s, got %s", planned.ID, state.ID)
		}
		if got := firstObject(state, vocabulary.WorldEntityKind.String()); got != string(planned.Kind) {
			t.Fatalf("entity %s kind triple = %v, want %q", planned.ID, got, planned.Kind)
		}

		for _, fact := range planned.Facts {
			objects := objectsFor(state, fact.Predicate.String())
			if len(objects) == 0 {
				t.Fatalf("entity %s has no %q triple", planned.ID, fact.Predicate)
			}
			want := fmt.Sprint(fact.Object)
			found := false
			for _, object := range objects {
				if fmt.Sprint(object) == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("entity %s %q = %v, want %v", planned.ID, fact.Predicate, objects, fact.Object)
			}
		}

		// Provenance proves WHICH writer produced the state, which is the
		// single-writer half of the requirement: these triples arrived on the
		// Graphable fact lane carrying the importer's source, so nothing wrote
		// ENTITY_STATES behind graph-ingest's back.
		for _, triple := range state.Triples {
			if triple.Source == "" {
				t.Fatalf("entity %s has a triple with no source: %+v", planned.ID, triple)
			}
		}
		if state.MessageType.Domain != "semmachina" {
			t.Fatalf("entity %s was ingested as message type %v, not a SemMachina payload",
				planned.ID, state.MessageType)
		}
	}
}

// A world entity carrying author prose must be born with the content indexing
// profile. The profile is stamped at entity BIRTH and immutable afterwards, so
// an entity born under the default control floor could never be embedded
// without being recreated — this is only observable against a real ingest.
func TestIntegration_ImportedEntitiesAreBornWithTheContentIndexingProfile(t *testing.T) {
	harness := requireInfra(t)
	plan := starterPlan(t, worldNamespace(t))

	importer, err := world.NewImporter(harness.client)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	if _, err := importer.Import(t.Context(), plan); err != nil {
		t.Fatalf("Import: %v", err)
	}

	state := harness.awaitEntity(t, plan.Entities[0].ID)
	if got := firstObject(state, "entity.indexing.profile"); got != "content" {
		t.Fatalf("indexing profile = %v, want content; a world entity born under the "+
			"control floor can never be embedded, and the profile is immutable", got)
	}
}

// The spec's "Re-import is a no-op" scenario. State equality is asserted over
// the FACTS an entity asserts, not the whole record: the KV revision and the
// stored update time legitimately advance on any write, and requiring them to
// be identical would be asserting that the second import did nothing at all,
// which is not what convergence means.
//
// tripleSet COUNTS occurrences rather than collecting a set, which is what
// makes this a test of replace semantics: if the fact lane appended instead of
// replacing, every predicate would come back with a count of two and the
// comparison would fail. "No duplicate or competing triples" is the actual
// requirement, and a set-valued comparison would not see a duplicate at all.
func TestIntegration_ReimportConvergesToTheSameGraphState(t *testing.T) {
	harness := requireInfra(t)
	namespace := worldNamespace(t)

	importer, err := world.NewImporter(harness.client)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	first := starterPlan(t, namespace)
	if _, err := importer.Import(t.Context(), first); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	// Draining BEFORE the baseline is read is what makes this a convergence
	// test rather than a race. Without it the baseline can be captured from a
	// referential stub — an entity that exists because something referenced it,
	// carrying none of its own facts — and the second import would then "change"
	// state that the first import had simply not finished writing.
	awaitStreamDrained(t, harness)

	before := make(map[string]map[tripleKey]int, len(first.Entities))
	for _, planned := range first.Entities {
		before[planned.ID] = tripleSet(harness.awaitEntity(t, planned.ID))
	}

	// Resolved independently, to prove convergence rests on deterministic
	// identity rather than on reusing the same in-memory plan.
	second := starterPlan(t, namespace)
	if !reflect.DeepEqual(first.IDs(), second.IDs()) {
		t.Fatalf("the same package resolved to different IDs:\n%v\n%v", first.IDs(), second.IDs())
	}
	if _, err := importer.Import(t.Context(), second); err != nil {
		t.Fatalf("second Import: %v", err)
	}

	// The second import must be observably applied before the comparison, or a
	// no-op result could just be the first import's state read early. Wait for
	// the stream sequence graph-ingest has consumed to catch up.
	awaitStreamDrained(t, harness)

	for _, planned := range second.Entities {
		after := tripleSet(harness.awaitEntity(t, planned.ID))
		if !reflect.DeepEqual(before[planned.ID], after) {
			t.Fatalf("entity %s changed across a re-import:\nbefore %v\nafter  %v",
				planned.ID, before[planned.ID], after)
		}
	}
}

// The spec's "Same template, two worlds" scenario, materialized. Two campaigns
// from one package must be independently addressable in one live graph.
func TestIntegration_OneTemplateIntoTwoWorldsStaysDisjoint(t *testing.T) {
	harness := requireInfra(t)

	importer, err := world.NewImporter(harness.client)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	one := starterPlan(t, worldNamespace(t))
	two := starterPlan(t, worldNamespace(t))

	if _, err := importer.Import(t.Context(), one); err != nil {
		t.Fatalf("Import world one: %v", err)
	}
	if _, err := importer.Import(t.Context(), two); err != nil {
		t.Fatalf("Import world two: %v", err)
	}

	seen := make(map[string]bool, len(one.Entities))
	for _, planned := range one.Entities {
		state := harness.awaitEntity(t, planned.ID)
		seen[state.ID] = true
	}
	for _, planned := range two.Entities {
		state := harness.awaitEntity(t, planned.ID)
		if seen[state.ID] {
			t.Fatalf("entity %s belongs to both worlds", state.ID)
		}
	}

	// Independently addressable also means independently MUTABLE identity: the
	// two worlds' characters must reference their own scenes, not each other's.
	locationOne := firstObject(
		harness.awaitEntity(t, characterID(t, one)), vocabulary.WorldLocationCurrent.String())
	locationTwo := firstObject(
		harness.awaitEntity(t, characterID(t, two)), vocabulary.WorldLocationCurrent.String())
	if locationOne == locationTwo {
		t.Fatalf("both worlds' characters stand in the same scene %v", locationOne)
	}
	if !strings.Contains(fmt.Sprint(locationOne), "."+one.WorldNS+".") {
		t.Fatalf("world %s character is located in %v", one.WorldNS, locationOne)
	}
}

// The player entity is instance configuration, and the binding has to survive
// all the way into the graph — that is the difference between a template that
// can be played twice and a template that names its own player.
func TestIntegration_PlayerBindingMaterializesFromInstanceConfig(t *testing.T) {
	harness := requireInfra(t)
	plan := starterPlan(t, worldNamespace(t))

	importer, err := world.NewImporter(harness.client)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	if _, err := importer.Import(t.Context(), plan); err != nil {
		t.Fatalf("Import: %v", err)
	}

	player := plan.Entities[len(plan.Entities)-1]
	state := harness.awaitEntity(t, player.ID)

	if got := firstObject(state, vocabulary.WorldEntityKind.String()); got != string(vocabulary.EntityKindPlayer) {
		t.Fatalf("player entity kind = %v", got)
	}
	character := firstObject(state, vocabulary.PlayerCharacterCurrent.String())
	if character == nil {
		t.Fatal("the materialized player carries no played-character binding")
	}

	// The bound character must be a real, queryable entity of this world —
	// a binding pointing at nothing would look identical in the payload.
	bound := harness.awaitEntity(t, fmt.Sprint(character))
	if got := firstObject(bound, vocabulary.WorldEntityKind.String()); got != string(vocabulary.EntityKindCharacter) {
		t.Fatalf("the player is bound to a %v, not a character", got)
	}
}

// A publish failure part-way through must be reported with the entity that
// failed, not swallowed into a partial success. The real broker will not fail
// on demand, so the failure is injected at the production publish surface.
func TestIntegration_ImportReportsAPartialPublishFailure(t *testing.T) {
	plan := starterPlan(t, "failns")

	publisher := &failingPublisher{failAfter: 2, err: errors.New("broker said no")}
	importer, err := world.NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	result, err := importer.Import(context.Background(), plan)
	if err == nil {
		t.Fatal("Import reported success after a publish failure")
	}
	if len(result.Entities) != 2 {
		t.Fatalf("result reported %d published entities, want the 2 that succeeded", len(result.Entities))
	}
	if !strings.Contains(err.Error(), plan.Entities[2].ID) {
		t.Fatalf("error %q does not name the entity that failed", err)
	}
	if !strings.Contains(err.Error(), "broker said no") {
		t.Fatalf("error %q loses the broker's reason", err)
	}
}

type failingPublisher struct {
	failAfter int
	err       error
	calls     int
	subjects  []string
}

func (p *failingPublisher) PublishToStreamWithAck(
	_ context.Context, subject string, _ []byte,
) (*jetstream.PubAck, error) {
	p.calls++
	p.subjects = append(p.subjects, subject)
	if p.calls > p.failAfter {
		return nil, p.err
	}
	return &jetstream.PubAck{Sequence: uint64(p.calls)}, nil
}

// characterID returns the mapped ID of the plan's player character.
func characterID(t *testing.T, plan *world.Plan) string {
	t.Helper()
	for _, entity := range plan.Entities {
		if entity.Kind == vocabulary.EntityKindCharacter && entity.Template.LocalID == "rook" {
			return entity.ID
		}
	}
	t.Fatal("plan has no rook character")
	return ""
}

// awaitStreamDrained waits until graph-ingest's consumer has no pending
// messages, so a second import is provably applied before state is compared.
func awaitStreamDrained(t *testing.T, harness *testInfra) {
	t.Helper()
	ctx := t.Context()

	js, err := harness.client.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	stream, err := js.Stream(ctx, entityStream)
	if err != nil {
		t.Fatalf("stream %s: %v", entityStream, err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		consumers := stream.ListConsumers(ctx)
		pending := uint64(0)
		seen := 0
		for info := range consumers.Info() {
			seen++
			pending += info.NumPending + uint64(info.NumAckPending)
		}
		if err := consumers.Err(); err != nil {
			t.Fatalf("list consumers: %v", err)
		}
		if seen > 0 && pending == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("graph-ingest never drained the entity stream")
}
