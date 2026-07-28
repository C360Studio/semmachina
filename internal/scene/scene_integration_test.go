package scene_test

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Two of this component's claims are only true of a REAL graph.
//
// "Who is in this scene" is a reverse lookup, and the reverse direction is
// maintained by graph-index off a KV watch — eventually consistent, and served
// only once its initial build is complete. A fake that answered instantly would
// hide the property a caller has to design around.
//
// And a referential stub is minted by graph-ingest, not by us. The whole hazard
// is that it answers a read successfully; the only place to observe a real one
// is against the component that makes them.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

// Each test gets its own world namespace, so tests sharing one broker cannot see
// each other's rooms.
var integrationCounter atomic.Int64

// liveScene is one world namespace holding a scene, the people in it, and a turn
// happening there.
type liveScene struct {
	harness   *testinfra.Harness
	store     *graphio.Store
	assembler *scene.Assembler
	namespace string

	sceneID      string
	turnID       string
	turnEntityID string
}

func (l *liveScene) id(t *testing.T, kind, instance string) string {
	t.Helper()
	composed, err := vocabulary.ComposeEntityID(testOrg, l.namespace, testTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose %s/%s: %v", kind, instance, err)
	}
	return composed
}

// create writes one entity through graph-ingest's atomic create lane, which is
// how every entity in this engine is born.
func (l *liveScene) create(t *testing.T, entityID string, triples ...message.Triple) {
	t.Helper()
	state := &graph.EntityState{
		ID: entityID,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version:   1,
		UpdatedAt: time.Now().UTC(),
	}
	for _, triple := range triples {
		triple.Subject = entityID
		triple.Source = "scene-integration-test"
		triple.Timestamp = time.Now().UTC()
		triple.Confidence = 1
		state.Triples = append(state.Triples, triple)
	}
	if _, err := l.store.CreateEntity(t.Context(), state); err != nil {
		t.Fatalf("create %s: %v", entityID, err)
	}
	l.harness.AwaitEntity(t, entityID)
}

// startScene builds a room with two people in it and a turn under way.
func startScene(t *testing.T) *liveScene {
	t.Helper()
	harness := testinfra.Require(t)
	harness.RequireIndex(t)

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	assembler, err := scene.NewAssembler(store)
	if err != nil {
		t.Fatalf("NewAssembler: %v", err)
	}

	live := &liveScene{
		harness:   harness,
		store:     store,
		assembler: assembler,
		namespace: fmt.Sprintf("scenew%d", integrationCounter.Add(1)),
	}
	live.sceneID = live.id(t, "scene", "gatehouse")

	// The scene FIRST. An entity referencing it before it exists mints a
	// referential stub at its key, and the atomic create then loses to that stub
	// — the same ordering the world importer and the campaign gate live under.
	live.create(t, live.sceneID,
		fact(vocabulary.WorldEntityName, "The Gatehouse"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindScene)),
		fact(vocabulary.SceneAttributeTension, 3),
	)
	live.create(t, live.id(t, "character", "rook"),
		fact(vocabulary.WorldEntityName, "Rook"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
		fact(vocabulary.CharacterAttributeHealth, 8),
		fact(vocabulary.CharacterStatusCurrent, string(vocabulary.StatusHealthy)),
		fact(vocabulary.WorldLocationCurrent, live.sceneID),
	)
	live.create(t, live.id(t, "character", "wren"),
		fact(vocabulary.WorldEntityName, "Wren"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
		fact(vocabulary.CharacterAttributeHealth, 6),
		fact(vocabulary.WorldLocationCurrent, live.sceneID),
	)
	// The player is a durable graph entity bound to the character they play,
	// exactly as instance configuration writes it — never a connection.
	live.create(t, live.id(t, "player", "p1"),
		fact(vocabulary.PlayerCharacterCurrent, live.id(t, "character", "rook")),
	)

	live.submitAction(t)
	return live
}

// submitAction puts a turn in the graph the way play does: through the real
// recorder, which stores the player's words and creates the turn carrying the
// reference to them.
func (l *liveScene) submitAction(t *testing.T) {
	t.Helper()

	backend, err := content.NewObjectStore(t.Context(), l.harness.Client,
		content.WithBucket("SCENE_CONTENT_"+l.namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	contentStore, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	identity := turn.Identity{Org: testOrg, WorldNS: l.namespace, Template: testTemplate}
	recorder, err := turn.NewRecorder(l.store, contentStore, identity)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	acceptance, err := recorder.Accept(t.Context(), &payload.PlayerAction{
		ActionID:   "act-1",
		PlayerID:   l.id(t, "player", "p1"),
		CampaignID: l.id(t, "campaign", "main"),
		SceneID:    l.sceneID,
		Text:       "I lever the gate open with the crowbar.",
		ArrivedAt:  time.Now().UTC(),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-7"},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	l.turnID = acceptance.TurnID
	l.turnEntityID = acceptance.TurnEntityID
	l.harness.AwaitEntity(t, l.turnEntityID)
}

// assembleWhen polls until the assembled view satisfies a condition.
//
// The wait is for the INDEX, not for the graph: membership is a reverse lookup
// maintained off a KV watch, so an edge written a moment ago is not queryable
// the same instant. Everything the view reads out of ENTITY_STATES is current at
// the moment the query ran.
func (l *liveScene) assembleWhen(t *testing.T, want string, ready func(*scene.View) bool) *scene.View {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last *scene.View
	var lastErr error
	for time.Now().Before(deadline) {
		view, err := l.assembler.Assemble(t.Context(), l.turnID, l.turnEntityID)
		switch {
		case err != nil:
			lastErr = err
		case ready(view):
			return view
		default:
			last = view
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("the context never became %s; last error: %v", want, lastErr)
	}
	t.Fatalf("the context never became %s; last view had members %v, neighbours %v, excluded %+v",
		want, ids(last.Members), ids(last.Neighbours), last.Excluded)
	return nil
}

// The standing invariant, against a real graph: personas judge CURRENT
// authoritative state at execution time, never a submission-time snapshot. The
// action was accepted while Rook was healthy at 8; by the time the persona runs
// he is wounded at 3, and that is the world the persona is owed.
func TestIntegration_TheContextReflectsAChangeMadeAfterTheActionWasSubmitted(t *testing.T) {
	live := startScene(t)
	rook := live.id(t, "character", "rook")

	before := live.assembleWhen(t, "populated", func(v *scene.View) bool { return len(v.Members) == 2 })
	if got := memberFact(t, before, rook, vocabulary.CharacterAttributeHealth); fmt.Sprint(got) != "8" {
		t.Fatalf("at submission the courier's health reads %v, want 8; this test would prove nothing", got)
	}

	// The world moves between submission and execution. Under email-cadence play
	// that gap can be days, which is the whole reason no snapshot travels with
	// the action.
	wounded := []message.Triple{
		{
			Subject: rook, Predicate: vocabulary.CharacterAttributeHealth.String(), Object: 3,
			Source: "scene-integration-test", Timestamp: time.Now().UTC(), Confidence: 1,
		},
		{
			Subject: rook, Predicate: vocabulary.CharacterStatusCurrent.String(),
			Object: string(vocabulary.StatusWounded),
			Source: "scene-integration-test", Timestamp: time.Now().UTC(), Confidence: 1,
		},
	}
	if _, err := live.store.MergeTriples(t.Context(), rook, wounded); err != nil {
		t.Fatalf("wound the courier: %v", err)
	}

	after := live.assembleWhen(t, "carrying the wounded courier", func(v *scene.View) bool {
		return fmt.Sprint(memberFactOrNil(v, rook, vocabulary.CharacterAttributeHealth)) == "3"
	})
	if got := memberFact(t, after, rook, vocabulary.CharacterStatusCurrent); got != string(vocabulary.StatusWounded) {
		t.Fatalf("the persona's context reports status %v, want the current %q", got, vocabulary.StatusWounded)
	}
	if !after.AssembledAt.After(before.AssembledAt) {
		t.Fatalf("the second assembly is stamped %v, not after the first at %v; the view has to record when "+
			"it ran, or 'execution time' is unverifiable", after.AssembledAt, before.AssembledAt)
	}
}

// The other half of execution-time reads: somebody who was not in the room when
// the player acted is in it when the persona runs.
func TestIntegration_SomebodyWhoArrivedAfterSubmissionIsInTheContext(t *testing.T) {
	live := startScene(t)
	live.assembleWhen(t, "populated", func(v *scene.View) bool { return len(v.Members) == 2 })

	hollis := live.id(t, "character", "hollis")
	live.create(t, hollis,
		fact(vocabulary.WorldEntityName, "Hollis"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
		fact(vocabulary.CharacterAttributeHealth, 9),
		fact(vocabulary.WorldLocationCurrent, live.sceneID),
	)

	view := live.assembleWhen(t, "carrying the sentry", func(v *scene.View) bool {
		return slicesContain(ids(v.Members), hollis)
	})
	if len(view.Members) != 3 {
		t.Fatalf("the room holds %v, want three people", ids(view.Members))
	}
	if got := memberFact(t, view, hollis, vocabulary.WorldEntityName); got != "Hollis" {
		t.Fatalf("the new arrival's name reads %v", got)
	}
}

// F11 against the component that actually mints stubs. A referenced-but-
// undelivered entity is queryable and factless, and handing one to a persona is
// a silent context hole: the courier carries a thing with no name and the
// narrator describes it anyway.
func TestIntegration_AReferencedButUndeliveredEntityIsExcludedAsAStub(t *testing.T) {
	live := startScene(t)
	hollis := live.id(t, "character", "hollis")
	lantern := live.id(t, "item", "lantern")

	// The reference alone mints the stub. Nothing ever delivers the lantern's
	// own facts — which is exactly what a half-imported world looks like.
	//
	// It has to arrive on the CREATE lane, not the merge lane. Only the
	// fact-arrival and create lanes walk an entity's relationship triples and
	// materialize absent targets (upstream: update_with_triples "does NOT run
	// ensureRelationshipTargetsExist"), so merging a new reference onto an
	// existing entity leaves the target genuinely absent rather than stubbed —
	// which is a different bug with a different answer.
	live.create(t, hollis,
		fact(vocabulary.WorldEntityName, "Hollis"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)),
		fact(vocabulary.WorldLocationCurrent, live.sceneID),
		fact(vocabulary.WorldRelationCarries, lantern),
	)

	// Anti-vacuity: the thing at that key really is a stub, and it really does
	// answer a read. A test that excluded an entity that simply did not exist
	// would prove nothing about stubs at all.
	deadline := time.Now().Add(20 * time.Second)
	var stub *graph.EntityState
	for time.Now().Before(deadline) {
		state, err := live.harness.QueryEntity(t.Context(), lantern)
		if err == nil {
			stub = state
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if stub == nil {
		t.Fatal("no stub was minted at the referenced key; this test proves nothing")
	}
	if !stub.IsStub() {
		t.Fatalf("the referenced key holds a real entity, not a stub: %+v", stub.MessageType)
	}

	view := live.assembleWhen(t, "aware of the lantern", func(v *scene.View) bool {
		for _, excluded := range v.Excluded {
			if excluded.ID == lantern {
				return true
			}
		}
		return false
	})

	for _, entity := range view.Entities() {
		if entity.ID == lantern {
			t.Fatal("a referential stub was handed to a persona as a thing in the world")
		}
	}
	for _, excluded := range view.Excluded {
		if excluded.ID == lantern && excluded.Reason != scene.ExcludedStub {
			t.Fatalf("the stub was excluded as %q, want %q", excluded.Reason, scene.ExcludedStub)
		}
	}
}

// The engine's own turn entities point at the scene — one per turn ever taken
// there — so a reader that took every incoming edge as membership would grow a
// persona's context with the campaign's history. Against the real index, where
// those edges genuinely exist.
func TestIntegration_PastTurnsPointingAtTheSceneAreNotMembers(t *testing.T) {
	live := startScene(t)

	// The turn under way already points at the scene, so the edge this test is
	// about is present by construction. Wait for the index to carry it — an
	// assertion that the assembler ignored an edge the index never had would
	// pass for the wrong reason — then confirm the assembler does not treat it
	// as a person in the room.
	deadline := time.Now().Add(20 * time.Second)
	sawTurn := false
	for !sawTurn && time.Now().Before(deadline) {
		incoming, err := live.store.IncomingRelationships(t.Context(), live.sceneID)
		if err != nil {
			t.Fatalf("IncomingRelationships: %v", err)
		}
		for _, edge := range incoming {
			if edge.FromEntityID == live.turnEntityID &&
				edge.Predicate == vocabulary.TurnActionScene.String() {
				sawTurn = true
			}
		}
		if !sawTurn {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !sawTurn {
		t.Fatal("the index never carried the turn's edge into the scene; this test proves nothing")
	}

	view := live.assembleWhen(t, "populated", func(v *scene.View) bool { return len(v.Members) == 2 })
	if slicesContain(ids(view.Members), live.turnEntityID) {
		t.Fatal("the turn entity was assembled as a person in the room")
	}
}

func memberFact(t *testing.T, view *scene.View, entityID string, predicate vocabulary.Predicate) any {
	t.Helper()
	got := memberFactOrNil(view, entityID, predicate)
	if got == nil {
		t.Fatalf("entity %s carries no %s in the assembled context", entityID, predicate)
	}
	return got
}

func memberFactOrNil(view *scene.View, entityID string, predicate vocabulary.Predicate) any {
	for _, entity := range view.Entities() {
		if entity.ID != entityID {
			continue
		}
		if objects := entity.Objects(predicate); len(objects) > 0 {
			return objects[0]
		}
	}
	return nil
}
