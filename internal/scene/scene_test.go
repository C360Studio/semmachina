package scene_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testOrg      = "c360"
	testWorldNS  = "world1"
	testTemplate = "starter"

	testTurnID       = "turn-act-1"
	testTurnEntityID = "c360.semmachina.world1.starter.turn.turn-act-1"
)

var testTime = time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)

func id(t *testing.T, kind, instance string) string {
	t.Helper()
	composed, err := vocabulary.ComposeEntityID(testOrg, testWorldNS, testTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose %s/%s: %v", kind, instance, err)
	}
	return composed
}

// fakeGraph is an in-memory graph that keeps the two properties this component
// actually depends on: a stub is an entity with a zero envelope, and the
// incoming index answers the reverse direction a triple cannot be read from.
//
// Everything about whether a REAL graph behaves that way is proved in
// scene_integration_test.go against real graph-ingest and real graph-index; this
// exists for the shapes a live broker will not produce on demand.
type fakeGraph struct {
	entities map[string]*graph.EntityState

	// hidden names entities whose outgoing edges the incoming index does not
	// report yet. It is how a LAGGING index is expressed: the fact is in
	// ENTITY_STATES and the reverse lookup has not applied it, which is the one
	// state a live index will not hold still in.
	hidden map[string]bool
	// remembered holds edges the index reports for entities the store no longer
	// resolves, keyed by the entity they point AT — the opposite disagreement,
	// and one a separate projection can genuinely hold.
	remembered map[string][]graph.IncomingEntry

	getErr      error
	batchErr    error
	incomingErr error

	gets     int
	batches  int
	incoming int
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{
		entities:   map[string]*graph.EntityState{},
		hidden:     map[string]bool{},
		remembered: map[string][]graph.IncomingEntry{},
	}
}

// hideFromIndex stops the incoming index from reporting an entity's edges while
// leaving the entity and its facts exactly as they are.
func (g *fakeGraph) hideFromIndex(entityID string) { g.hidden[entityID] = true }

// rememberEdge makes the index report an edge independently of the entity that
// asserted it, so the edge can outlive the entity's readability.
func (g *fakeGraph) rememberEdge(from string, predicate vocabulary.Predicate, to string) {
	g.remembered[to] = append(g.remembered[to],
		graph.IncomingEntry{FromEntityID: from, Predicate: predicate.String()})
}

// put stores a real entity: an envelope plus its own facts.
func (g *fakeGraph) put(entityID string, triples ...message.Triple) {
	state := &graph.EntityState{
		ID:          entityID,
		MessageType: message.Type{Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion},
		Version:     1,
		UpdatedAt:   testTime,
	}
	for _, triple := range triples {
		triple.Subject = entityID
		triple.Source = "test"
		triple.Timestamp = testTime
		triple.Confidence = 1
		state.Triples = append(state.Triples, triple)
	}
	g.entities[entityID] = state
}

// putStub stores a referential stub in the shape graph-ingest actually mints:
// the stub ENVELOPE plus the marker triples, and none of the entity's own facts.
//
// The envelope is the load-bearing part. IsStub keys on it and not on the
// marker, because the marker persists after the entity's real birth — so a
// fixture that faked a stub with a marker alone would be testing a check nobody
// should be making.
func (g *fakeGraph) putStub(entityID string) {
	g.entities[entityID] = &graph.EntityState{
		ID:          entityID,
		MessageType: graph.StubMessageType,
		Triples: []message.Triple{{
			Subject:    entityID,
			Predicate:  graph.PredStubMarker,
			Object:     true,
			Source:     "graph-ingest",
			Timestamp:  testTime,
			Confidence: 1,
		}},
	}
}

func (g *fakeGraph) GetEntity(_ context.Context, entityID string) (*graph.EntityState, error) {
	g.gets++
	if g.getErr != nil {
		return nil, g.getErr
	}
	state, ok := g.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	return state, nil
}

func (g *fakeGraph) GetEntities(_ context.Context, ids []string) (graphio.BatchResult, error) {
	g.batches++
	if g.batchErr != nil {
		return graphio.BatchResult{}, g.batchErr
	}
	var result graphio.BatchResult
	for _, entityID := range ids {
		state, ok := g.entities[entityID]
		if !ok {
			result.Missing = append(result.Missing,
				graph.MissingEntity{ID: entityID, Reason: graph.MissingNotFound})
			continue
		}
		result.Entities = append(result.Entities, *state)
	}
	return result, nil
}

func (g *fakeGraph) IncomingRelationships(_ context.Context, entityID string) ([]graph.IncomingEntry, error) {
	g.incoming++
	if g.incomingErr != nil {
		return nil, g.incomingErr
	}
	entries := append([]graph.IncomingEntry(nil), g.remembered[entityID]...)
	for _, state := range g.entities {
		if g.hidden[state.ID] {
			continue
		}
		for _, triple := range state.Triples {
			if object, ok := triple.Object.(string); ok && object == entityID {
				entries = append(entries, graph.IncomingEntry{
					FromEntityID: state.ID, Predicate: triple.Predicate,
				})
			}
		}
	}
	return entries, nil
}

// gatehouse builds the starter scene's shape: a room, three people in it, a
// crowbar one of them carries, and a turn happening there.
func gatehouse(t *testing.T) *fakeGraph {
	t.Helper()
	g := newFakeGraph()

	sceneID := id(t, "scene", "gatehouse")
	rook := id(t, "character", "rook")
	wren := id(t, "character", "wren")
	crowbar := id(t, "item", "crowbar")

	g.put(sceneID,
		fact(vocabulary.WorldEntityName, "The Gatehouse"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindScene)),
		fact(vocabulary.SceneAttributeTension, 3),
	)
	g.put(rook,
		fact(vocabulary.WorldEntityName, "Rook"),
		fact(vocabulary.CharacterAttributeHealth, 8),
		fact(vocabulary.CharacterStatusCurrent, string(vocabulary.StatusHealthy)),
		fact(vocabulary.WorldLocationCurrent, sceneID),
		fact(vocabulary.WorldRelationCarries, crowbar),
		fact(vocabulary.WorldRelationKnows, wren),
	)
	g.put(wren,
		fact(vocabulary.WorldEntityName, "Wren"),
		fact(vocabulary.CharacterAttributeHealth, 6),
		fact(vocabulary.WorldLocationCurrent, sceneID),
	)
	// The crowbar is NOT in the scene: it is carried, so it is a 1-hop
	// neighbour rather than a member.
	g.put(crowbar,
		fact(vocabulary.WorldEntityName, "A bent crowbar"),
		fact(vocabulary.ItemAttributeQuantity, 1),
	)
	// The player is a graph entity, never a connection, and is one hop from the
	// turn rather than a thing in the room.
	g.put(id(t, "player", "p1"),
		fact(vocabulary.PlayerCharacterCurrent, rook),
	)
	g.put(testTurnEntityID,
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
		fact(vocabulary.TurnActionPlayer, id(t, "player", "p1")),
		fact(vocabulary.TurnActionScene, sceneID),
		fact(vocabulary.TurnActionRef, "obj://SEMMACHINA_CONTENT/turn/"+testTurnID+"/action"),
	)
	return g
}

func fact(predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{Predicate: predicate.String(), Object: object}
}

func newAssembler(t *testing.T, g scene.Graph, opts ...scene.Option) *scene.Assembler {
	t.Helper()
	options := append([]scene.Option{scene.WithClock(func() time.Time { return testTime })}, opts...)
	assembler, err := scene.NewAssembler(g, options...)
	if err != nil {
		t.Fatalf("NewAssembler: %v", err)
	}
	return assembler
}

func assemble(t *testing.T, g scene.Graph, opts ...scene.Option) *scene.View {
	t.Helper()
	view, err := newAssembler(t, g, opts...).Assemble(t.Context(), testTurnID, testTurnEntityID)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return view
}

func ids(entities []scene.Entity) []string {
	out := make([]string, 0, len(entities))
	for _, entity := range entities {
		out = append(out, entity.ID)
	}
	return out
}

// ------------------------------------------------------------- the shape

// The scene comes from the TURN, not from the caller. A caller that supplied it
// could supply the one the action named at submission time, which is the
// snapshot this whole component exists not to be.
func TestAssemble_ReadsTheSceneOffTheTurnAndReturnsItsMembers(t *testing.T) {
	g := gatehouse(t)
	view := assemble(t, g)

	if view.SceneID != id(t, "scene", "gatehouse") {
		t.Fatalf("assembled scene %q", view.SceneID)
	}
	if got := ids(view.Members); len(got) != 2 ||
		got[0] != id(t, "character", "rook") || got[1] != id(t, "character", "wren") {
		t.Fatalf("members are %v, want the two characters in the room", got)
	}
	if got := ids(view.Neighbours); len(got) != 2 ||
		got[0] != id(t, "item", "crowbar") || got[1] != id(t, "player", "p1") {
		t.Fatalf("neighbours are %v, want the carried item and the player one hop out", got)
	}
	if got := view.Scene.Objects(vocabulary.WorldEntityName); len(got) != 1 || got[0] != "The Gatehouse" {
		t.Fatalf("the scene's own facts are missing: %v", got)
	}
	if got := view.Turn.Objects(vocabulary.TurnPhaseCurrent); len(got) != 1 {
		t.Fatalf("the turn's artifacts are missing: %v", got)
	}
	if !view.AssembledAt.Equal(testTime) {
		t.Fatalf("the view is stamped %v, want the time the query ran", view.AssembledAt)
	}
}

// The turn's own artifacts travel as REFERENCES. Resolving them is the prompt
// builder's business; this component answers what the graph says, and the graph
// says where the prose is.
func TestAssemble_CarriesTheTurnsArtifactReferencesWithoutResolvingThem(t *testing.T) {
	view := assemble(t, gatehouse(t))

	refs := view.Turn.Objects(vocabulary.TurnActionRef)
	if len(refs) != 1 {
		t.Fatalf("the turn carries %d action references: %v", len(refs), refs)
	}
	text, _ := refs[0].(string)
	if !strings.HasPrefix(text, "obj://") {
		t.Fatalf("the action reference reads %q, want a storage reference", text)
	}
}

// A persona shown a courier, an apprentice, and a sentry has no way to tell
// which one the player is unless the turn's player reference is followed. The
// player is a graph entity bound to a character — never a connection — so
// "who is acting" is a fact one hop from the turn.
func TestAssemble_CarriesThePlayerSoAPersonaKnowsWhoIsActing(t *testing.T) {
	view := assemble(t, gatehouse(t))
	player := id(t, "player", "p1")

	if !slicesContain(ids(view.Neighbours), player) {
		t.Fatalf("the player is not in the context (%v); a persona cannot tell which of the people in the "+
			"room the player controls", ids(view.Neighbours))
	}
	for _, entity := range view.Neighbours {
		if entity.ID != player {
			continue
		}
		if got := entity.Objects(vocabulary.PlayerCharacterCurrent); len(got) != 1 ||
			got[0] != id(t, "character", "rook") {
			t.Fatalf("the player's played-character binding reads %v", got)
		}
	}
}

// ------------------------------------------------------------------ the actor

// The view names who is acting and says it PROVED they are in the room. Both
// halves matter: the id is what a prompt builder points at, and the absence of a
// doubt is what says the room was checked rather than assumed.
func TestAssemble_NamesTheActingCharacterItProvedIsInTheRoom(t *testing.T) {
	view := assemble(t, gatehouse(t))

	if view.Actor.PlayerID != id(t, "player", "p1") {
		t.Fatalf("the view names player %q", view.Actor.PlayerID)
	}
	if view.Actor.CharacterID != id(t, "character", "rook") {
		t.Fatalf("the view names acting character %q", view.Actor.CharacterID)
	}
	if !view.Actor.Verified() {
		t.Fatalf("the actor is unverified (%q) in a room that holds them", view.Actor.Doubt)
	}
}

// The hole membership cannot report. Scene membership comes only from the edges
// pointing AT the scene, so a character whose world.location.current the index
// has not applied yet is not a CANDIDATE — and a candidate is the only thing an
// Exclusion can name. Without this check the adjudicator is handed a room that
// does not contain the person acting, with no error and no exclusion.
//
// ErrIndexNotReady does not cover it: upstream's readiness latch flips as soon as
// initial enumeration completes over an empty graph (target == 0 on a fresh
// instance-per-world boot, before the world import writes anything), and the
// query gate never fires on ordinary lag afterwards.
func TestAssemble_RefusesARoomThatDoesNotHoldTheActingCharacter(t *testing.T) {
	g := gatehouse(t)
	rook := id(t, "character", "rook")

	// Anti-vacuity: with the index caught up, this same fixture assembles and
	// Rook is in the room.
	if !slicesContain(ids(assemble(t, g).Members), rook) {
		t.Fatal("the fixture does not put the acting character in the room; this test would prove nothing")
	}

	// The index has not applied Rook's membership edge yet. His facts are in
	// ENTITY_STATES either way — this is lag, not absence.
	g.hideFromIndex(rook)

	_, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("a room without the person acting in it was handed to a persona; nothing anywhere reports it")
	}
	var absent *scene.AbsentActorError
	if !errors.As(err, &absent) {
		t.Fatalf("refusal is a %T, want an AbsentActorError naming the actor and the scene", err)
	}
	if absent.CharacterID != rook || absent.SceneID != id(t, "scene", "gatehouse") {
		t.Fatalf("the refusal names character %q in scene %q", absent.CharacterID, absent.SceneID)
	}
	if absent.PlayerID != id(t, "player", "p1") {
		t.Fatalf("the refusal names player %q", absent.PlayerID)
	}
	if absent.Excluded != "" {
		t.Fatalf("the refusal blames exclusion %q; this character was never a candidate at all, which is a "+
			"different repair from one the assembler refused", absent.Excluded)
	}
}

// The other way the actor goes missing, and the one with a different repair: the
// index HAS the membership edge and the entity store does not return what it
// points at. The index is a separate projection, so the two can disagree — and
// reporting that as "the index is lagging" would send an operator to wait for
// something that already finished.
func TestAssemble_SaysSoWhenTheActingCharacterWasACandidateAndWasRefused(t *testing.T) {
	g := gatehouse(t)
	rook := id(t, "character", "rook")
	// The edge survives in the index; the entity behind it does not resolve.
	g.rememberEdge(rook, vocabulary.WorldLocationCurrent, id(t, "scene", "gatehouse"))
	delete(g.entities, rook)

	_, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID)
	var absent *scene.AbsentActorError
	if !errors.As(err, &absent) {
		t.Fatalf("assembling a room whose actor did not hydrate returned %v, want an AbsentActorError", err)
	}
	if absent.Excluded != scene.ExcludedMissing {
		t.Fatalf("the refusal blames %q, want the exclusion the assembler actually made", absent.Excluded)
	}
}

// Where the binding genuinely cannot be read, the view says so rather than
// passing silently: an unverifiable actor is a fact about the world, and a
// refusal there would fail every turn in a campaign whose player entity was
// never written.
func TestAssemble_ReportsAnUnverifiableActorRatherThanPassingSilently(t *testing.T) {
	cases := map[string]struct {
		arrange func(*testing.T, *fakeGraph)
		want    scene.ActorDoubt
	}{
		"the player entity was never delivered": {
			arrange: func(t *testing.T, g *fakeGraph) {
				t.Helper()
				g.putStub(id(t, "player", "p1"))
			},
			want: scene.ActorPlayerAbsent,
		},
		"the player plays nobody": {
			arrange: func(t *testing.T, g *fakeGraph) {
				t.Helper()
				g.put(id(t, "player", "p1"))
			},
			want: scene.ActorBindingAbsent,
		},
		"the player plays two characters": {
			arrange: func(t *testing.T, g *fakeGraph) {
				t.Helper()
				g.put(id(t, "player", "p1"),
					fact(vocabulary.PlayerCharacterCurrent, id(t, "character", "rook")),
					fact(vocabulary.PlayerCharacterCurrent, id(t, "character", "wren")),
				)
			},
			want: scene.ActorBindingAmbiguous,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := gatehouse(t)
			tc.arrange(t, g)

			view := assemble(t, g)
			if view.Actor.Doubt != tc.want {
				t.Fatalf("the view reports doubt %q, want %q", view.Actor.Doubt, tc.want)
			}
			if view.Actor.Verified() {
				t.Fatal("an actor nobody could resolve was reported as verified")
			}
			if view.Actor.PlayerID != id(t, "player", "p1") {
				t.Fatalf("the view names player %q even so", view.Actor.PlayerID)
			}
		})
	}
}

// The cross-check reads what the view already holds — the turn names the player,
// the player is hydrated one hop out — so it must not cost a round trip. A
// presence check that issued its own read would make the fixed query shape a
// fixed-plus-one query shape.
func TestAssemble_ChecksTheActorWithoutIssuingAnotherRead(t *testing.T) {
	g := gatehouse(t)
	assemble(t, g)

	if g.gets != 2 || g.batches != 2 || g.incoming != 1 {
		t.Fatalf("assembly issued %d single reads, %d batches, %d membership reads; the actor cross-check "+
			"must be pure in-memory work over data the view already carries", g.gets, g.batches, g.incoming)
	}
}

func TestAssemble_RefusesATurnThatNamesNoPlayer(t *testing.T) {
	g := gatehouse(t)
	sceneID := id(t, "scene", "gatehouse")
	// A turn whose birth record established a scene and no player: nothing
	// downstream can say which of the people in the room is acting.
	g.put(testTurnEntityID,
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
		fact(vocabulary.TurnActionScene, sceneID),
	)

	if _, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID); err == nil {
		t.Fatal("a context was assembled for a turn nobody is taking")
	}
}

// A world of a thousand rooms costs exactly what this room costs. The read count
// is what makes that true, so it is asserted rather than described.
func TestAssemble_IssuesAFixedNumberOfReads(t *testing.T) {
	g := gatehouse(t)
	assemble(t, g)

	if g.gets != 2 {
		t.Fatalf("issued %d single reads, want the turn and the scene", g.gets)
	}
	if g.incoming != 1 {
		t.Fatalf("issued %d membership reads, want one", g.incoming)
	}
	if g.batches != 2 {
		t.Fatalf("issued %d batch reads, want one for the members and one for their neighbours; a read per "+
			"entity is a fan-out that grows with the scene", g.batches)
	}
}

// The traversal stops at one hop, and it stops because nothing recurses. The
// crowbar is a neighbour; what the crowbar points at is not in the view at all.
func TestAssemble_StopsAtOneHop(t *testing.T) {
	g := gatehouse(t)
	crowbar := id(t, "item", "crowbar")
	forge := id(t, "scene", "forge")
	g.put(forge, fact(vocabulary.WorldEntityName, "The Forge"))
	// The crowbar was made in the forge: two hops from the gatehouse.
	g.entities[crowbar].Triples = append(g.entities[crowbar].Triples, message.Triple{
		Subject: crowbar, Predicate: vocabulary.WorldLocationCurrent.String(), Object: forge,
		Source: "test", Timestamp: testTime, Confidence: 1,
	})

	view := assemble(t, g)
	for _, entity := range view.Entities() {
		if entity.ID == forge {
			t.Fatal("a two-hop entity reached the context; the depth bound is not structural")
		}
	}
}

// Every turn ever taken in a room points at that room. Treating those edges as
// membership would grow a persona's context with the campaign's history — the
// exact opposite of a flat per-turn cost.
func TestAssemble_DoesNotMistakePastTurnsForPeopleInTheRoom(t *testing.T) {
	g := gatehouse(t)
	sceneID := id(t, "scene", "gatehouse")
	for _, past := range []string{"turn-act-old1", "turn-act-old2"} {
		g.put(id(t, "turn", past),
			fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseComplete)),
			fact(vocabulary.TurnActionScene, sceneID),
		)
	}

	view := assemble(t, g)
	if len(view.Members) != 2 {
		t.Fatalf("the room has %d members after two past turns: %v", len(view.Members), ids(view.Members))
	}
	for _, entity := range view.Entities() {
		if strings.Contains(entity.ID, ".turn.") && entity.ID != testTurnEntityID {
			t.Fatalf("a past turn (%s) is in this turn's context", entity.ID)
		}
	}
}

// ------------------------------------------------------------------ stubs

// F11, and the reason this test exists at all: a referenced-but-undelivered
// entity answers a read successfully while carrying none of its own facts.
// Handing one to a persona is a silent context hole — the room quietly has one
// fewer thing in it and nothing errors.
func TestAssemble_ExcludesAStubNeighbourAndSaysSo(t *testing.T) {
	g := gatehouse(t)
	rook := id(t, "character", "rook")
	lantern := id(t, "item", "lantern")

	// Rook carries a lantern that was never imported: the reference alone mints
	// a queryable, factless entity at that key.
	g.entities[rook].Triples = append(g.entities[rook].Triples, message.Triple{
		Subject: rook, Predicate: vocabulary.WorldRelationCarries.String(), Object: lantern,
		Source: "test", Timestamp: testTime, Confidence: 1,
	})
	g.putStub(lantern)

	view := assemble(t, g)

	for _, entity := range view.Neighbours {
		if entity.ID == lantern {
			t.Fatal("a referential stub was handed to a persona as a thing in the world")
		}
	}
	found := false
	for _, excluded := range view.Excluded {
		if excluded.ID == lantern {
			found = true
			if excluded.Reason != scene.ExcludedStub {
				t.Fatalf("the stub was excluded as %q, want %q", excluded.Reason, scene.ExcludedStub)
			}
		}
	}
	if !found {
		t.Fatal("the stub was dropped without being reported; a half-loaded room and a small room look " +
			"identical to every caller")
	}
}

// The marker triple a stub carries PERSISTS after the entity's real facts land,
// so a marker-based check reports a fully-loaded entity as a stub forever. Only
// the envelope distinguishes them, which is why IsStub is the discriminator and
// why this test builds a real entity that still carries the marker.
func TestAssemble_UsesTheEnvelopeNotTheMarkerToRecognizeAStub(t *testing.T) {
	g := gatehouse(t)
	rook := id(t, "character", "rook")

	// Rook was referenced before being delivered, so he carries the stub marker
	// AND his own facts, with a real envelope. Nothing removes the marker at
	// birth; only the envelope flips.
	g.entities[rook].Triples = append(g.entities[rook].Triples, message.Triple{
		Subject: rook, Predicate: graph.PredStubMarker, Object: true,
		Source: "graph-ingest", Timestamp: testTime, Confidence: 1,
	})
	if g.entities[rook].IsStub() {
		t.Fatal("the fixture entity reads as a stub; this test would pass vacuously")
	}

	view := assemble(t, g)
	if !slicesContain(ids(view.Members), rook) {
		t.Fatal("a fully-loaded entity that still carries its birth marker was excluded as a stub; a " +
			"marker-based check reports every referenced-then-delivered entity as missing forever")
	}
	// And the marker itself does not reach the persona: it is not registered
	// vocabulary, so the projection drops it.
	for _, entity := range view.Members {
		if entity.ID != rook {
			continue
		}
		for _, triple := range entity.Triples {
			if triple.Predicate == graph.PredStubMarker {
				t.Fatal("a framework identity marker reached a persona's context")
			}
		}
	}
}

// A stub at the SCENE is fatal rather than an exclusion: a persona cannot be
// asked to judge what happens in a room with no name. This is the shape a
// half-imported world actually produces, since every member references the scene
// and the scene's own facts may not have landed yet.
func TestAssemble_RefusesAStubScene(t *testing.T) {
	g := gatehouse(t)
	g.putStub(id(t, "scene", "gatehouse"))

	_, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("a context was assembled from a scene that holds none of its own facts")
	}
	if !strings.Contains(err.Error(), "stub") {
		t.Fatalf("refusal %q does not name the stub", err)
	}
}

func TestAssemble_RefusesAStubTurn(t *testing.T) {
	g := gatehouse(t)
	g.putStub(testTurnEntityID)

	if _, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID); err == nil {
		t.Fatal("a context was assembled for a turn that holds none of its own facts")
	}
}

// An id the graph did not return at all is reported too. Upstream stopped
// shortening batch responses silently for exactly this reason; passing that on
// rather than dropping it is the point.
func TestAssemble_ReportsAMemberTheGraphDidNotReturn(t *testing.T) {
	g := gatehouse(t)
	ghost := id(t, "character", "ghost")
	// An edge into the scene from an entity that does not exist: the index has
	// the edge, the entity store has nothing.
	g.put(ghost, fact(vocabulary.WorldLocationCurrent, id(t, "scene", "gatehouse")))
	delete(g.entities, ghost)
	g.entities[id(t, "character", "rook")].Triples = append(
		g.entities[id(t, "character", "rook")].Triples, message.Triple{
			Subject: id(t, "character", "rook"), Predicate: vocabulary.WorldRelationKnows.String(),
			Object: ghost, Source: "test", Timestamp: testTime, Confidence: 1,
		})

	view := assemble(t, g)
	found := false
	for _, excluded := range view.Excluded {
		if excluded.ID == ghost && excluded.Reason == scene.ExcludedMissing {
			found = true
		}
	}
	if !found {
		t.Fatalf("an entity the graph did not return was dropped without being reported: %+v", view.Excluded)
	}
}

// ------------------------------------------------------------------ bounds

// The cap is a real ceiling, and it is checked BEFORE the batch is issued: an
// oversized scene must be refused rather than fetched and then refused, or the
// read the bound exists to bound has already happened.
func TestAssemble_RefusesAnOversizeSceneRatherThanTrimmingIt(t *testing.T) {
	g := gatehouse(t)
	sceneID := id(t, "scene", "gatehouse")
	for idx := range 10 {
		g.put(id(t, "character", fmt.Sprintf("extra%d", idx)),
			fact(vocabulary.WorldEntityName, "Somebody"),
			fact(vocabulary.WorldLocationCurrent, sceneID),
		)
	}

	assembler := newAssembler(t, g, scene.WithMaxEntities(4))
	batchesBefore := g.batches

	_, err := assembler.Assemble(t.Context(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("an oversized scene assembled a context; a persona handed part of a room narrates a room " +
			"that is not there")
	}
	var oversize *scene.OversizeError
	if !errors.As(err, &oversize) {
		t.Fatalf("refusal is a %T, want an OversizeError naming the scene and the cap", err)
	}
	if oversize.SceneID != sceneID {
		t.Fatalf("the refusal names scene %q", oversize.SceneID)
	}
	if g.batches != batchesBefore {
		t.Fatalf("the oversized batch was issued anyway (%d reads); the cap has to be checked against the "+
			"candidate count, not the result", g.batches-batchesBefore)
	}
}

// The NEIGHBOUR stage is the one with fan-out: members are people in a room and
// there are only so many, while one member carrying a satchel of things adds a
// neighbour apiece. So the cap has to be checked again after the boundary is
// known, and against the candidate count — not after hydrating it.
func TestAssemble_RefusesWhenTheMembersFitAndTheirNeighboursDoNot(t *testing.T) {
	g := gatehouse(t)
	rook := id(t, "character", "rook")
	for idx := range 5 {
		item := id(t, "item", fmt.Sprintf("trinket%d", idx))
		g.put(item, fact(vocabulary.WorldEntityName, "A trinket"))
		g.entities[rook].Triples = append(g.entities[rook].Triples, message.Triple{
			Subject: rook, Predicate: vocabulary.WorldRelationCarries.String(), Object: item,
			Source: "test", Timestamp: testTime, Confidence: 1,
		})
	}

	// Two members plus the scene and the turn is four; the boundary adds the
	// player, the crowbar, and five trinkets.
	assembler := newAssembler(t, g, scene.WithMaxEntities(6))
	batchesBefore := g.batches

	_, err := assembler.Assemble(t.Context(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("a context over the cap assembled anyway; the member stage fits and the neighbour stage is " +
			"where a crowded inventory blows the bound")
	}
	var oversize *scene.OversizeError
	if !errors.As(err, &oversize) {
		t.Fatalf("refusal is a %T, want an OversizeError", err)
	}
	if oversize.Entities != 11 {
		t.Fatalf("the refusal counts %d entities, want the scene, the turn, two members, and seven "+
			"neighbours", oversize.Entities)
	}
	if g.batches != batchesBefore+1 {
		t.Fatalf("the assembly issued %d batches; the members must hydrate (they are how the boundary is "+
			"known) and the oversized neighbour batch must not", g.batches-batchesBefore)
	}
}

// The cap and the reported size have to count the same things. They did not: the
// cap counted the scene, the members, and their neighbours while the view also
// carries the TURN — so a context could exceed its own ceiling by one and then
// report the excess in Size, which is the number an operator would use to decide
// the ceiling was holding.
func TestAssemble_TheCapCountsEverythingTheViewCarries(t *testing.T) {
	g := gatehouse(t)

	exact := assemble(t, g, scene.WithMaxEntities(6))
	if exact.Size.Entities != 6 {
		t.Fatalf("the view carries %d entities at a cap of 6: %s", exact.Size.Entities, exact.Size)
	}

	_, err := newAssembler(t, g, scene.WithMaxEntities(5)).Assemble(t.Context(), testTurnID, testTurnEntityID)
	var oversize *scene.OversizeError
	if !errors.As(err, &oversize) {
		t.Fatalf("a six-entity context assembled under a cap of five (%v); the cap is not counting what the "+
			"view carries", err)
	}
}

// "How big was this context" has to have an answer, or a flat per-turn cost is
// an assertion rather than something anybody can check.
func TestAssemble_ReportsItsOwnSize(t *testing.T) {
	view := assemble(t, gatehouse(t))

	if view.Size.Entities != len(view.Entities()) {
		t.Fatalf("the view reports %d entities and carries %d", view.Size.Entities, len(view.Entities()))
	}
	counted := 0
	for _, entity := range view.Entities() {
		counted += len(entity.Triples)
	}
	if view.Size.Triples != counted {
		t.Fatalf("the view reports %d triples and carries %d", view.Size.Triples, counted)
	}
	if view.Size.Bytes <= 0 {
		t.Fatalf("the view reports %d bytes for %d triples", view.Size.Bytes, counted)
	}

	// And the measure MOVES with the context, or it is a constant wearing a
	// number's clothes.
	bigger := gatehouse(t)
	bigger.put(id(t, "character", "hollis"),
		fact(vocabulary.WorldEntityName, "Hollis"),
		fact(vocabulary.WorldLocationCurrent, id(t, "scene", "gatehouse")),
	)
	larger := assemble(t, bigger)
	if larger.Size.Bytes <= view.Size.Bytes || larger.Size.Entities <= view.Size.Entities {
		t.Fatalf("a bigger scene measured %s, the smaller one %s", larger.Size, view.Size)
	}
}

func TestNewAssembler_RefusesAnUnboundedContext(t *testing.T) {
	if _, err := scene.NewAssembler(newFakeGraph(), scene.WithMaxEntities(0)); err == nil {
		t.Fatal("an assembler was built with no entity cap; per-turn cost stops being flat the first time " +
			"a scene fills up")
	}
	if _, err := scene.NewAssembler(nil); err == nil {
		t.Fatal("an assembler was built with no graph")
	}
}

// ----------------------------------------------------------- read-only

// graph-ingest is the sole ENTITY_STATES writer, and this is where that stops
// being a promise. An assembler holding a merge method is one refactor away from
// repairing the world it was asked to describe.
func TestGraph_CarriesNoWriteMethod(t *testing.T) {
	surface := reflect.TypeOf((*scene.Graph)(nil)).Elem()

	reads := map[string]bool{"GetEntity": true, "GetEntities": true, "IncomingRelationships": true}
	for idx := range surface.NumMethod() {
		name := surface.Method(idx).Name
		if !reads[name] {
			t.Fatalf("the assembler's graph surface carries %q, which is not one of the three reads; "+
				"graph-ingest is the sole ENTITY_STATES writer and this component reads", name)
		}
	}
	if surface.NumMethod() != len(reads) {
		t.Fatalf("the graph surface has %d methods, want %d", surface.NumMethod(), len(reads))
	}
}

// -------------------------------------------------------------- refusals

// turn_id and the turn entity id are one fact wearing two shapes, and a stage
// handed one turn's entity with another turn's id would assemble a different
// turn's scene and hand it to a persona as this turn's world.
func TestAssemble_RefusesATurnEntityAddressingADifferentTurn(t *testing.T) {
	g := gatehouse(t)
	if _, err := newAssembler(t, g).Assemble(t.Context(), "turn-act-2", testTurnEntityID); err == nil {
		t.Fatal("a context was assembled for a turn the entity does not address")
	}
	if g.gets != 0 {
		t.Fatal("a mismatched pair still issued a read")
	}
}

func TestAssemble_RefusesATurnWithNoSceneAndATurnWithTwo(t *testing.T) {
	sceneID := "c360.semmachina.world1.starter.scene.gatehouse"

	noScene := newFakeGraph()
	noScene.put(testTurnEntityID, fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)))
	if _, err := newAssembler(t, noScene).Assemble(t.Context(), testTurnID, testTurnEntityID); err == nil {
		t.Fatal("a turn with no scene assembled a context")
	}

	twoScenes := newFakeGraph()
	twoScenes.put(testTurnEntityID,
		fact(vocabulary.TurnActionScene, sceneID),
		fact(vocabulary.TurnActionScene, "c360.semmachina.world1.starter.scene.forge"),
	)
	_, err := newAssembler(t, twoScenes).Assemble(t.Context(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("a turn holding two scenes assembled a context from one of them at random")
	}
}

// A membership read that failed is not an empty room. Continuing on it would
// hand a persona an empty scene and call it the world.
func TestAssemble_FailsWhenTheMembershipReadFails(t *testing.T) {
	g := gatehouse(t)
	g.incomingErr = errors.New("index not ready")

	if _, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID); err == nil {
		t.Fatal("a failed membership read produced a context; an empty room and an unavailable index would " +
			"be the same view")
	}
}

func slicesContain(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
