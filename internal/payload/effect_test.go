package payload_test

import (
	"strings"
	"testing"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Every effect type has a valid shape and a set of invalid ones. The
// cross-type cases matter most: an intent that carries a field belonging to a
// different effect type is an effect the fiction promised and the world would
// never receive, so it must be a rejection rather than a silent drop.
func TestEffectIntent_ValidatesPerTypeShape(t *testing.T) {
	valid := map[string]payload.EffectIntent{
		"set_attribute": {
			Type: vocabulary.EffectSetAttribute, Target: testCharacter,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
		},
		"set_attribute at the lower bound": {
			Type: vocabulary.EffectSetAttribute, Target: testCharacter,
			Attribute: vocabulary.AttributeHealth, Value: intPtr(0),
		},
		"set_status": {
			Type: vocabulary.EffectSetStatus, Target: testCharacter,
			Status: vocabulary.StatusDead,
		},
		"move_entity": {
			Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: testLocation,
		},
		"add_relationship": {
			Type: vocabulary.EffectAddRelationship, Target: testCharacter,
			Relation: vocabulary.RelationKnows, Object: testAlly,
		},
		"remove_relationship": {
			Type: vocabulary.EffectRemoveRelationship, Target: testCharacter,
			Relation: vocabulary.RelationHostileTo, Object: testAlly,
		},
	}

	for name, intent := range valid {
		t.Run("accepts "+name, func(t *testing.T) {
			if err := intent.Validate(); err != nil {
				t.Fatalf("a well-formed %s intent was rejected: %v", intent.Type, err)
			}
		})
	}

	invalid := []struct {
		name    string
		intent  payload.EffectIntent
		wantErr string
	}{
		{
			name:    "unregistered effect type",
			intent:  payload.EffectIntent{Type: "delete_entity", Target: testCharacter},
			wantErr: "effect_type",
		},
		{
			name:    "empty effect type",
			intent:  payload.EffectIntent{Target: testCharacter},
			wantErr: "effect_type",
		},
		{
			name: "target is not a canonical entity id",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: "rook", Status: vocabulary.StatusWounded,
			},
			wantErr: "target",
		},
		{
			name: "set_attribute with an unregistered attribute",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: "hp", Value: intPtr(3),
			},
			wantErr: "attribute",
		},
		{
			name: "set_attribute above the registered maximum",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(11),
			},
			wantErr: "bounds",
		},
		{
			name: "set_attribute below the registered minimum",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(-1),
			},
			wantErr: "bounds",
		},
		{
			name: "set_attribute without a value",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth,
			},
			wantErr: "value",
		},
		{
			name: "set_attribute carrying a status",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(3),
				Status: vocabulary.StatusWounded,
			},
			wantErr: "status",
		},
		{
			name: "set_status with an unregistered status",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: "poisoned",
			},
			wantErr: "status",
		},
		{
			name: "set_status carrying a location",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: testCharacter,
				Status: vocabulary.StatusWounded, Location: testLocation,
			},
			wantErr: "location",
		},
		{
			name: "move_entity without a location",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectMoveEntity, Target: testCharacter,
			},
			wantErr: "location",
		},
		{
			name: "move_entity to a non-entity location",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: "courtyard",
			},
			wantErr: "location",
		},
		{
			name: "relationship with an unregistered relation",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: testCharacter,
				Relation: "owns", Object: testAlly,
			},
			wantErr: "relation",
		},
		{
			name: "relationship without an object",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: testCharacter,
				Relation: vocabulary.RelationKnows,
			},
			wantErr: "object",
		},
		{
			name: "relationship carrying an attribute value",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectRemoveRelationship, Target: testCharacter,
				Relation: vocabulary.RelationKnows, Object: testAlly,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(2),
			},
			wantErr: "attribute",
		},
	}

	for _, tc := range invalid {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			intent := tc.intent
			err := intent.Validate()
			if err == nil {
				t.Fatal("Validate accepted an intent outside the closed effect contract")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// Every effect type must map to a predicate the write gate accepts, or the
// applier builds a mutation the graph silently refuses.
func TestEffectIntent_MutationIsRegisteredAndWriteGateValid(t *testing.T) {
	registered := make(map[vocabulary.Predicate]bool)
	for _, p := range vocabulary.AllPredicates() {
		registered[p] = true
	}

	intents := []payload.EffectIntent{
		{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusWounded},
		{Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: testLocation},
		{
			Type: vocabulary.EffectRemoveRelationship, Target: testCharacter,
			Relation: vocabulary.RelationKnows, Object: testAlly,
		},
	}
	for _, attr := range vocabulary.Attributes() {
		spec, ok := vocabulary.AttributeSpecFor(attr)
		if !ok {
			t.Fatalf("no spec for attribute %q", attr)
		}
		intents = append(intents, payload.EffectIntent{
			Type: vocabulary.EffectSetAttribute, Target: testCharacter,
			Attribute: attr, Value: intPtr(spec.Min),
		})
	}
	for _, rel := range vocabulary.Relations() {
		intents = append(intents, payload.EffectIntent{
			Type: vocabulary.EffectAddRelationship, Target: testCharacter,
			Relation: rel, Object: testAlly,
		})
	}

	coveredTypes := make(map[vocabulary.EffectType]bool)
	for _, intent := range intents {
		predicate, object, err := intent.Mutation()
		if err != nil {
			t.Fatalf("intent %+v has no mutation: %v", intent, err)
		}
		if !registered[predicate] {
			t.Fatalf("intent %+v writes %q, which is not in AllPredicates()", intent, predicate)
		}
		if _, err := ssvocab.ParsePredicate(predicate.String()); err != nil {
			t.Fatalf("the write gate would reject %q: %v", predicate, err)
		}
		if object == nil {
			t.Fatalf("intent %+v produced predicate %q with no object to write", intent, predicate)
		}
		coveredTypes[intent.Type] = true
	}

	for _, effectType := range vocabulary.EffectTypes() {
		if !coveredTypes[effectType] {
			t.Fatalf("effect type %q has no mutation coverage in this test", effectType)
		}
	}
}

// Predicate and object come back together so the applier never needs its own
// parallel switch over the same closed set. This pins which field supplies the
// object for each effect type — the mapping that second switch would have had
// to duplicate.
func TestEffectIntent_MutationPairsThePredicateWithTheRightField(t *testing.T) {
	cases := []struct {
		name          string
		intent        payload.EffectIntent
		wantPredicate vocabulary.Predicate
		wantObject    any
	}{
		{
			name: "set_attribute writes Value",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(4),
			},
			wantPredicate: vocabulary.CharacterAttributeHealth,
			wantObject:    4,
		},
		{
			name: "set_attribute writes an explicit zero, not an omission",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(0),
			},
			wantPredicate: vocabulary.CharacterAttributeHealth,
			wantObject:    0,
		},
		{
			name: "set_status writes Status",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusWounded,
			},
			wantPredicate: vocabulary.CharacterStatusCurrent,
			wantObject:    "wounded",
		},
		{
			name: "move_entity writes Location",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: testLocation,
			},
			wantPredicate: vocabulary.WorldLocationCurrent,
			wantObject:    testLocation,
		},
		{
			name: "add_relationship writes Object",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: testCharacter,
				Relation: vocabulary.RelationAlliedWith, Object: testAlly,
			},
			wantPredicate: vocabulary.WorldRelationAlliedWith,
			wantObject:    testAlly,
		},
		{
			name: "remove_relationship writes Object",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectRemoveRelationship, Target: testCharacter,
				Relation: vocabulary.RelationHostileTo, Object: testAlly,
			},
			wantPredicate: vocabulary.WorldRelationHostileTo,
			wantObject:    testAlly,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			predicate, object, err := tc.intent.Mutation()
			if err != nil {
				t.Fatalf("Mutation: %v", err)
			}
			if predicate != tc.wantPredicate {
				t.Fatalf("predicate = %q, want %q", predicate, tc.wantPredicate)
			}
			// Rules compare against plain JSON scalars. A named string type
			// serializes identically, so a rule matching "wounded" would
			// quietly never fire while every wire assertion still passed.
			// Checked before equality because the two differ only by type.
			switch object.(type) {
			case int, string:
			default:
				t.Fatalf("object is %T, want a plain int or string", object)
			}
			if object != tc.wantObject {
				t.Fatalf("object = %#v (%T), want %#v (%T)", object, object, tc.wantObject, tc.wantObject)
			}
		})
	}
}

// The guard has to be symmetric over EVERY field the pair is built from. The
// set_status and move_entity cases are the ones the predicate-only accessor
// waved through: it validated Attribute and Relation, then handed back a
// rule-matched predicate for an out-of-vocabulary status — LLM-authored text
// arriving on the graph's rule-matching surface.
func TestEffectIntent_MutationRefusesOutOfVocabularyIntents(t *testing.T) {
	cases := []struct {
		name   string
		intent payload.EffectIntent
	}{
		{
			name:   "unregistered effect type",
			intent: payload.EffectIntent{Type: "delete_entity", Target: testCharacter},
		},
		{
			name: "unregistered attribute",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: "hp", Value: intPtr(1),
			},
		},
		{
			name: "attribute value outside registered bounds",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetAttribute, Target: testCharacter,
				Attribute: vocabulary.AttributeHealth, Value: intPtr(900),
			},
		},
		{
			name: "unregistered relation",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: testCharacter,
				Relation: "owns", Object: testAlly,
			},
		},
		{
			name: "unregistered status",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: "poisoned",
			},
		},
		{
			name: "location that is not a canonical entity id",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectMoveEntity, Target: testCharacter, Location: "courtyard",
			},
		},
		{
			name: "relationship object that is not a canonical entity id",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectAddRelationship, Target: testCharacter,
				Relation: vocabulary.RelationKnows, Object: "wren",
			},
		},
		{
			name: "target that is not a canonical entity id",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: "rook", Status: vocabulary.StatusWounded,
			},
		},
		{
			name: "fields belonging to another effect type",
			intent: payload.EffectIntent{
				Type: vocabulary.EffectSetStatus, Target: testCharacter,
				Status: vocabulary.StatusWounded, Location: testLocation,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			predicate, object, err := tc.intent.Mutation()
			if err == nil {
				t.Fatalf("an out-of-vocabulary intent produced the mutation %q = %#v", predicate, object)
			}
			if predicate != "" || object != nil {
				t.Fatalf("a refused mutation must carry nothing writable, got %q = %#v", predicate, object)
			}
		})
	}
}

// Mutation must refuse exactly what Validate refuses. If it could accept an
// intent Validate rejects, the applier would have a second, weaker door.
func TestEffectIntent_MutationRefusesEverythingValidateRefuses(t *testing.T) {
	intents := []payload.EffectIntent{
		{},
		{Type: vocabulary.EffectSetAttribute, Target: testCharacter},
		{Type: vocabulary.EffectSetStatus, Target: testCharacter},
		{Type: vocabulary.EffectMoveEntity, Target: testCharacter},
		{Type: vocabulary.EffectAddRelationship, Target: testCharacter},
		{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusDead},
		{
			Type: vocabulary.EffectSetAttribute, Target: testCharacter,
			Attribute: vocabulary.AttributeTension, Value: intPtr(1),
		},
	}
	for idx := range intents {
		intent := intents[idx]
		validateErr := intent.Validate()
		_, _, mutationErr := intent.Mutation()
		if (validateErr == nil) != (mutationErr == nil) {
			t.Fatalf("intent %+v: Validate err=%v but Mutation err=%v", intent, validateErr, mutationErr)
		}
	}
}

// A zero value must be distinguishable from an omitted one, or "health 0"
// (the death threshold) silently becomes "health unchanged" over the wire.
func TestEffectIntent_ZeroValueSurvivesTheWireDistinctFromOmitted(t *testing.T) {
	batch := payload.NewEffectBatch(testTurnID, vocabulary.BandMiss, []payload.EffectIntent{{
		Type: vocabulary.EffectSetAttribute, Target: testCharacter,
		Attribute: vocabulary.AttributeHealth, Value: intPtr(0),
	}})

	decoded, ok := decode(t, publish(t, batch)).(*payload.EffectBatch)
	if !ok {
		t.Fatal("decoder produced the wrong type")
	}
	value := decoded.Intents[0].Value
	if value == nil {
		t.Fatal("an explicit health of 0 arrived as an omitted value")
	}
	if *value != 0 {
		t.Fatalf("value = %d, want 0", *value)
	}
}
