package scene_test

import (
	"errors"
	"testing"

	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func placedGatehouse(t *testing.T) *fakeGraph {
	t.Helper()
	g := newFakeGraph()
	sceneID := id(t, "scene", "gatehouse")
	locationID := id(t, "location", "gatehouse-place")
	roadID := id(t, "location", "north-road")
	rook := id(t, "character", "rook")
	wren := id(t, "character", "wren")
	crowbar := id(t, "item", "crowbar")

	g.put(sceneID,
		fact(vocabulary.WorldEntityName, "The Gatehouse"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindScene)),
		fact(vocabulary.SceneLocationCurrent, locationID),
	)
	g.put(locationID,
		fact(vocabulary.WorldEntityName, "Gatehouse Place"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindLocation)),
		fact(vocabulary.LocationRelationConnectsTo, roadID),
	)
	g.put(roadID,
		fact(vocabulary.WorldEntityName, "North Road"),
		fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindLocation)),
	)
	g.put(rook,
		fact(vocabulary.WorldEntityName, "Rook"),
		fact(vocabulary.WorldLocationCurrent, locationID),
		fact(vocabulary.WorldRelationCarries, crowbar),
	)
	g.put(wren,
		fact(vocabulary.WorldEntityName, "Wren"),
		fact(vocabulary.WorldLocationCurrent, locationID),
	)
	g.put(crowbar, fact(vocabulary.WorldEntityName, "A bent crowbar"))
	g.put(id(t, "player", "p1"), fact(vocabulary.PlayerCharacterCurrent, rook))
	g.put(testTurnEntityID,
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
		fact(vocabulary.TurnActionPlayer, id(t, "player", "p1")),
		fact(vocabulary.TurnActionScene, sceneID),
	)
	return g
}

func TestAssemble_ResolvesSceneThroughItsLocation(t *testing.T) {
	g := placedGatehouse(t)
	view := assemble(t, g)

	if view.LocationID != id(t, "location", "gatehouse-place") ||
		view.Location.ID != view.LocationID {
		t.Fatalf("assembled location = %q / %+v", view.LocationID, view.Location)
	}
	if got := ids(view.Members); len(got) != 2 ||
		got[0] != id(t, "character", "rook") || got[1] != id(t, "character", "wren") {
		t.Fatalf("location members = %v", got)
	}
	for _, neighbour := range view.Neighbours {
		if neighbour.ID == view.SceneID || neighbour.ID == view.LocationID {
			t.Fatalf("fixed entity %s was hydrated again as a neighbour", neighbour.ID)
		}
	}
	if g.gets+g.incoming+g.batches > 6 {
		t.Fatalf("assembly issued %d reads, want at most six", g.gets+g.incoming+g.batches)
	}
	if view.Size.Entities != len(view.Entities()) || len(view.Entities()) < 3 {
		t.Fatalf("view size = %+v for %d entities", view.Size, len(view.Entities()))
	}
}

func TestAssemble_RefusesInvalidSceneLocation(t *testing.T) {
	tests := map[string]func(*testing.T, *fakeGraph){
		"missing": func(t *testing.T, g *fakeGraph) {
			g.entities[id(t, "scene", "gatehouse")].Triples = nil
		},
		"duplicate": func(t *testing.T, g *fakeGraph) {
			sceneID := id(t, "scene", "gatehouse")
			g.entities[sceneID].Triples = append(g.entities[sceneID].Triples,
				fact(vocabulary.SceneLocationCurrent, id(t, "location", "north-road")))
		},
		"stub": func(t *testing.T, g *fakeGraph) {
			g.putStub(id(t, "location", "gatehouse-place"))
		},
		"wrong kind": func(t *testing.T, g *fakeGraph) {
			locationID := id(t, "location", "gatehouse-place")
			g.put(locationID, fact(vocabulary.WorldEntityKind, string(vocabulary.EntityKindCharacter)))
		},
	}
	for name, arrange := range tests {
		t.Run(name, func(t *testing.T) {
			g := placedGatehouse(t)
			arrange(t, g)
			if _, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID); err == nil {
				t.Fatal("invalid scene location assembled")
			}
		})
	}
}

func TestAssemble_RefusesLegacySceneOccupancy(t *testing.T) {
	g := placedGatehouse(t)
	rook := id(t, "character", "rook")
	for index := range g.entities[rook].Triples {
		if g.entities[rook].Triples[index].Predicate == vocabulary.WorldLocationCurrent.String() {
			g.entities[rook].Triples[index].Object = id(t, "scene", "gatehouse")
		}
	}

	_, err := newAssembler(t, g).Assemble(t.Context(), testTurnID, testTurnEntityID)
	var absent *scene.AbsentActorError
	if !errors.As(err, &absent) {
		t.Fatalf("legacy scene occupancy returned %v, want actor-presence refusal", err)
	}
}
