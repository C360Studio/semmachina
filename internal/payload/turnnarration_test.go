package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testNarrationRef = "obj://SEMMACHINA_CONTENT/turn/turn-act-1/narration"

func validTurnNarration() *payload.TurnNarration {
	return &payload.TurnNarration{TurnID: testTurnID, NarrationRef: testNarrationRef}
}

func TestTurnNarration_ValidFixtureIsAccepted(t *testing.T) {
	if err := validTurnNarration().Validate(); err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}
}

// This record exists only to carry the pointer, so a record without one is a
// narration the turn would advertise and nobody could read. The projection
// refuses it too — belt and braces on the one predicate whose object comes from
// a stage that has just finished reading fiction.
func TestTurnNarration_RejectsARecordWithNothingToPointAt(t *testing.T) {
	narration := validTurnNarration()
	narration.NarrationRef = ""

	err := narration.Validate()
	if err == nil {
		t.Fatal("a narration record with no reference passed its own contract")
	}
	if !strings.Contains(err.Error(), "narration ref") {
		t.Fatalf("the rejection is %q and does not name the missing field", err)
	}

	if _, err := narration.Triples(testTurnEntity, "narrator", testTime); err == nil {
		t.Fatal("the projection accepted a record with no reference")
	}
}

func TestTurnNarration_RejectsATurnItCannotBelongTo(t *testing.T) {
	for name, turnID := range map[string]string{
		"absent":        "",
		"two segments":  "turn.act-1",
		"not a segment": "-leading",
	} {
		t.Run(name, func(t *testing.T) {
			narration := validTurnNarration()
			narration.TurnID = turnID
			if err := narration.Validate(); err == nil {
				t.Fatal("a narration filed under an unusable turn id was accepted")
			}
		})
	}
}

// The narrator's whole mark on the graph: one triple, and it is a pointer. Every
// structural fact about the turn was landed by the stage that decided it, so a
// narration restating one would put a second, softer copy of the world's state
// on the surface rules match.
func TestTurnNarration_ProjectsExactlyOneReferenceTriple(t *testing.T) {
	triples, err := validTurnNarration().Triples(testTurnEntity, "narrator", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("the narration projected %d triples: %+v", len(triples), triples)
	}
	triple := triples[0]
	if triple.Subject != testTurnEntity {
		t.Fatalf("the triple lands on %q", triple.Subject)
	}
	if triple.Predicate != vocabulary.TurnNarrationRef.String() {
		t.Fatalf("the triple's predicate is %q, want %q", triple.Predicate, vocabulary.TurnNarrationRef)
	}
	if triple.Object != testNarrationRef {
		t.Fatalf("the triple's object is %#v", triple.Object)
	}
	if triple.Context != testTurnID {
		t.Fatalf("the triple's correlation context is %q, want the turn", triple.Context)
	}
	if triple.Confidence != 1.0 {
		t.Fatalf("the triple's confidence is %v; a recorded exit is stated, never inferred", triple.Confidence)
	}
}

// The hazard the *.ref naming contract exists for: a sentence handed to a
// reference predicate is a bounded scalar string, so it passes the type switch
// and the byte bound and lands free text where rules match. The projection's
// grammar check is what stops it, and a narrator is the likeliest producer of
// one.
func TestTurnNarration_RefusesProseWhereAPointerBelongs(t *testing.T) {
	for name, ref := range map[string]string{
		"a sentence":       "The gate lifts a hand's width and stops.",
		"a bare key":       "turn/turn-act-1/narration",
		"no key":           "obj://SEMMACHINA_CONTENT",
		"no instance":      "obj:///turn/turn-act-1/narration",
		"a different verb": "https://example.com/prose",
	} {
		t.Run(name, func(t *testing.T) {
			narration := &payload.TurnNarration{TurnID: testTurnID, NarrationRef: ref}
			if _, err := narration.Triples(testTurnEntity, "narrator", testTime); err == nil {
				t.Fatalf("%q reached the graph as a narration reference", ref)
			}
		})
	}
}

// turn_id and the turn entity ID are one fact wearing two shapes, and the
// projection is where the pairing is checked on the way to the graph.
func TestTurnNarration_RefusesAnotherTurnsEntity(t *testing.T) {
	if _, err := validTurnNarration().Triples(
		"c360.semmachina.world1.starter.turn.turn-act-9", "narrator", testTime); err == nil {
		t.Fatal("a narration was filed against another turn's entity")
	}
}

// The category is deliberately unregistered — nothing publishes a
// TurnNarration — and the non-registration is held by
// TestEntityOnlyCategories_AreDeliberatelyUnregistered, which owns that list.
// What belongs here is that the schema names the category that list checks.
func TestTurnNarration_NamesTheCategoryTheRegistryTestKeepsUnregistered(t *testing.T) {
	schema := validTurnNarration().Schema()
	if schema.Domain != payload.Domain ||
		schema.Category != payload.CategoryTurnNarration ||
		schema.Version != payload.SchemaVersion {
		t.Fatalf("the schema is %v, want the deliberately-unregistered turn-narration coordinates", schema)
	}
}
