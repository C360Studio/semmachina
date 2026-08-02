package payload_test

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testWorldCharacter = "c360.semmachina.world1.starter.character.rook"
	testWorldScene     = "c360.semmachina.world1.starter.scene.gatehouse"
	testWorldItem      = "c360.semmachina.world1.starter.item.crowbar"
)

func validWorldEntity() *payload.WorldEntity {
	return &payload.WorldEntity{
		ID:   testWorldCharacter,
		Kind: vocabulary.EntityKindCharacter,
		Template: payload.TemplateRef{
			ID:      "starter",
			Version: "0.1.0",
			LocalID: "rook",
		},
		Facts: []payload.WorldFact{
			{Predicate: vocabulary.WorldEntityName, Object: "Rook"},
			{Predicate: vocabulary.WorldEntityDescription, Object: "A courier with a crowbar and a grudge."},
			{Predicate: vocabulary.CharacterAttributeHealth, Object: 8},
			{Predicate: vocabulary.CharacterStatusCurrent, Object: string(vocabulary.StatusHealthy)},
			{Predicate: vocabulary.WorldLocationCurrent, Object: testWorldScene, Reference: true},
			{Predicate: vocabulary.WorldRelationCarries, Object: testWorldItem, Reference: true},
		},
		RecordedAt: testTime,
	}
}

// The whole point of this payload is that it is a Graphable: graph-ingest
// extracts the entity from it on the ordinary fact lane. A payload that stopped
// satisfying the interface would compile, publish, and then be dropped at
// ingest with "payload does not implement Graphable".
func TestWorldEntity_SatisfiesGraphable(t *testing.T) {
	var _ graph.Graphable = (*payload.WorldEntity)(nil)
	var _ message.IndexingProfiler = (*payload.WorldEntity)(nil)

	entity := validWorldEntity()
	if entity.EntityID() != entity.ID {
		t.Fatalf("EntityID() = %q, want %q", entity.EntityID(), entity.ID)
	}
	if !ssvocab.IsValidIndexingProfile(entity.IndexingProfile()) {
		t.Fatalf("IndexingProfile() = %q, which graph-ingest would ignore", entity.IndexingProfile())
	}
}

// The projected triples are what actually lands in ENTITY_STATES, so they are
// asserted whole: subject, predicate, object, provenance, and the reference
// datatype that makes the graph validate an object as an entity ID.
func TestWorldEntity_TriplesProjectTheKindAndEveryFact(t *testing.T) {
	entity := validWorldEntity()
	triples := entity.Triples()

	if len(triples) != len(entity.Facts)+1 {
		t.Fatalf("got %d triples for %d facts; the kind triple should add exactly one",
			len(triples), len(entity.Facts))
	}

	kind := triples[0]
	if kind.Predicate != vocabulary.WorldEntityKind.String() {
		t.Fatalf("first triple is %q, want the projected kind triple", kind.Predicate)
	}
	if kind.Object != string(entity.Kind) {
		t.Fatalf("kind triple object = %v, want %q", kind.Object, entity.Kind)
	}

	for index, triple := range triples {
		if triple.Subject != entity.ID {
			t.Fatalf("triple %d subject = %q, want %q", index, triple.Subject, entity.ID)
		}
		if triple.Source != payload.WorldImportSource {
			t.Fatalf("triple %d source = %q, want %q", index, triple.Source, payload.WorldImportSource)
		}
		if !triple.Timestamp.Equal(entity.RecordedAt) {
			t.Fatalf("triple %d timestamp = %v, want the payload's recorded_at %v",
				index, triple.Timestamp, entity.RecordedAt)
		}
		if triple.Confidence != 1.0 {
			t.Fatalf("triple %d confidence = %v, want 1.0", index, triple.Confidence)
		}
		if triple.Context != "starter@0.1.0" {
			t.Fatalf("triple %d context = %q, want the template ref", index, triple.Context)
		}
	}

	for _, fact := range entity.Facts {
		found := false
		for _, triple := range triples {
			if triple.Predicate != fact.Predicate.String() || triple.Object != fact.Object {
				continue
			}
			found = true
			if fact.Reference && triple.Datatype != message.EntityReferenceDatatype {
				t.Fatalf("reference fact %q did not become an @id triple", fact.Predicate)
			}
			if !fact.Reference && triple.Datatype != "" {
				t.Fatalf("literal fact %q got datatype %q", fact.Predicate, triple.Datatype)
			}
		}
		if !found {
			t.Fatalf("fact %q (%v) produced no triple", fact.Predicate, fact.Object)
		}
	}
}

// Every triple the payload can produce faces the ENTITY_STATES write gate. If
// one of them is unparseable the entity silently never lands.
func TestWorldEntity_EveryProducedTripleSurvivesTheGraphContract(t *testing.T) {
	entity := validWorldEntity()
	state := &graph.EntityState{
		ID:          entity.ID,
		Triples:     entity.Triples(),
		MessageType: entity.Schema(),
		Version:     1,
	}
	if err := graph.ValidateEntityStateContract(state); err != nil {
		t.Fatalf("graph rejected the projected entity state: %v", err)
	}
}

func TestWorldEntity_TriplesAreEmptyForAnInvalidEntity(t *testing.T) {
	entity := validWorldEntity()
	entity.Kind = "dragon"

	if triples := entity.Triples(); len(triples) != 0 {
		t.Fatalf("an invalid entity produced %d triples; a half-entity would reach the graph unreported",
			len(triples))
	}
}

func TestWorldEntity_ValidateRejectsStructuralViolations(t *testing.T) {
	cases := map[string]struct {
		mutate func(*payload.WorldEntity)
		names  string
	}{
		"id is not six parts": {
			mutate: func(e *payload.WorldEntity) { e.ID = "rook" },
			names:  "id",
		},
		"kind outside the closed set": {
			mutate: func(e *payload.WorldEntity) { e.Kind = "dragon" },
			names:  "dragon",
		},
		"id type position disagrees with the declared kind": {
			mutate: func(e *payload.WorldEntity) {
				e.ID = "c360.semmachina.world1.starter.item.rook"
			},
			names: "but the payload declares kind",
		},
		"id instance position disagrees with the local id": {
			mutate: func(e *payload.WorldEntity) { e.Template.LocalID = "wren" },
			names:  "but the payload declares local id",
		},
		"id template position disagrees with the template id": {
			mutate: func(e *payload.WorldEntity) { e.Template.ID = "other" },
			names:  "but the payload declares",
		},
		"platform position is not semmachina": {
			mutate: func(e *payload.WorldEntity) {
				e.ID = "c360.elsewhere.world1.starter.character.rook"
			},
			names: "platform",
		},
		"missing template version": {
			mutate: func(e *payload.WorldEntity) { e.Template.Version = "" },
			names:  "template.version",
		},
		"zero recorded_at": {
			mutate: func(e *payload.WorldEntity) { e.RecordedAt = time0() },
			names:  "recorded_at",
		},
		"no facts": {
			mutate: func(e *payload.WorldEntity) { e.Facts = nil },
			names:  "declares no facts",
		},
		"fact outside the world vocabulary": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts[0].Predicate = vocabulary.TurnPhaseCurrent
			},
			names: "not a registered world fact",
		},
		"fact the kind cannot carry": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts[0] = payload.WorldFact{Predicate: vocabulary.SceneAttributeTension, Object: 3}
			},
			names: "cannot be carried",
		},
		"authored kind triple": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts[0] = payload.WorldFact{
					Predicate: vocabulary.WorldEntityKind,
					Object:    string(vocabulary.EntityKindCharacter),
				}
			},
			names: "projected from the kind field",
		},
		"repeated single-valued predicate": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = append(e.Facts, payload.WorldFact{
					Predicate: vocabulary.CharacterAttributeHealth, Object: 3,
				})
			},
			names: "single-valued",
		},
		"repeated identical relation": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = append(e.Facts, payload.WorldFact{
					Predicate: vocabulary.WorldRelationCarries, Object: testWorldItem, Reference: true,
				})
			},
			names: "same object",
		},
		"reference object is not an entity ID": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = []payload.WorldFact{
					{Predicate: vocabulary.WorldLocationCurrent, Object: "gatehouse", Reference: true},
				}
			},
			names: "six-part",
		},
		"reference flag on a numeric object": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = []payload.WorldFact{
					{Predicate: vocabulary.CharacterAttributeHealth, Object: 3, Reference: true},
				}
			},
			names: "reference",
		},
		"text fact over budget": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = []payload.WorldFact{{
					Predicate: vocabulary.WorldEntityDescription,
					Object:    strings.Repeat("a", payload.MaxFactTextBytes+1),
				}}
			},
			names: "budget",
		},
		"object of an undefined Go type": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = []payload.WorldFact{
					{Predicate: vocabulary.CharacterAttributeHealth, Object: true},
				}
			},
			names: "only string, int, and finite coordinate float64",
		},
		"float on a non-coordinate predicate": {
			mutate: func(e *payload.WorldEntity) {
				e.Facts = []payload.WorldFact{{Predicate: vocabulary.CharacterAttributeHealth, Object: 3.5}}
			},
			names: "coordinate",
		},
		"non-finite coordinate": {
			mutate: func(e *payload.WorldEntity) {
				e.ID = "c360.semmachina.world1.starter.location.gatehouse-place"
				e.Kind = vocabulary.EntityKindLocation
				e.Template.LocalID = "gatehouse-place"
				e.Facts = []payload.WorldFact{{Predicate: vocabulary.GeoLocationLatitude, Object: math.Inf(1)}}
			},
			names: "finite",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			entity := validWorldEntity()
			tc.mutate(entity)

			err := entity.Validate()
			if err == nil {
				t.Fatal("Validate accepted a structurally invalid world entity")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection reason %q does not mention %q", err, tc.names)
			}
		})
	}
}

// A character carrying two different items is the ordinary case, so the
// repetition rule must not have banned multi-valued relationships outright.
func TestWorldEntity_AllowsARelationToRepeatWithADifferentObject(t *testing.T) {
	entity := validWorldEntity()
	entity.Facts = append(entity.Facts, payload.WorldFact{
		Predicate: vocabulary.WorldRelationCarries,
		Object:    "c360.semmachina.world1.starter.item.lantern",
		Reference: true,
	})
	if err := entity.Validate(); err != nil {
		t.Fatalf("Validate rejected a character carrying two items: %v", err)
	}
}

// The object narrowing is the reason an attribute survives the wire as an
// integer. Without it a published 8 decodes as float64(8) and the fact that
// came back is not the fact that went out.
func TestWorldFact_UnmarshalNarrowsTheObjectToStringOrInt(t *testing.T) {
	var fact payload.WorldFact
	if err := json.Unmarshal([]byte(`{"predicate":"character.attribute.health","object":8}`), &fact); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, ok := fact.Object.(int); !ok || got != 8 {
		t.Fatalf("object decoded as %T(%v), want int(8)", fact.Object, fact.Object)
	}
}

func TestWorldFact_CanonicalCoordinateSurvivesTheWireAsFloat64(t *testing.T) {
	want := payload.WorldFact{Predicate: vocabulary.GeoLocationLatitude, Object: 41.8819}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got payload.WorldFact
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Predicate != want.Predicate || got.Object != want.Object || got.Reference {
		t.Fatalf("coordinate round trip = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped coordinate is invalid: %v", err)
	}
}

func TestWorldFact_UnmarshalRejectsUndefinedObjectShapes(t *testing.T) {
	rejected := map[string]string{
		"fractional number": `{"predicate":"character.attribute.health","object":3.5}`,
		"boolean":           `{"predicate":"world.entity.name","object":true}`,
		"null":              `{"predicate":"world.entity.name","object":null}`,
		"array":             `{"predicate":"world.entity.name","object":["a"]}`,
		"object":            `{"predicate":"world.entity.name","object":{"a":1}}`,
		"missing object":    `{"predicate":"world.entity.name"}`,
		"unknown field":     `{"predicate":"world.entity.name","object":"Rook","colour":"red"}`,
	}
	for name, body := range rejected {
		t.Run(name, func(t *testing.T) {
			var fact payload.WorldFact
			if err := json.Unmarshal([]byte(body), &fact); err == nil {
				t.Fatalf("unmarshal accepted %s", body)
			}
		})
	}
}

// Every fact predicate the vocabulary declares must be expressible by some
// entity kind. A world fact no kind can carry is dead vocabulary that reads as
// authoring surface.
func TestWorldEntity_EveryWorldFactIsCarryableBySomeKind(t *testing.T) {
	for _, predicate := range vocabulary.WorldFactPredicates() {
		kinds, ok := vocabulary.SubjectKindsFor(predicate)
		if !ok || len(kinds) == 0 {
			t.Fatalf("world fact %q has no carrying kind", predicate)
		}
		if predicate == vocabulary.WorldEntityKind {
			continue // projected, never authored
		}
		if !slices.ContainsFunc(kinds, func(k vocabulary.EntityKind) bool { return k.Valid() }) {
			t.Fatalf("world fact %q names no registered kind", predicate)
		}
	}
}
