package vocabulary_test

import (
	"slices"
	"testing"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestPlaceVocabulary_IsClosedCanonicalAndRegistered(t *testing.T) {
	if !vocabulary.EntityKindLocation.Valid() ||
		!slices.Contains(vocabulary.EntityKinds(), vocabulary.EntityKindLocation) {
		t.Fatal("location is not in the closed entity-kind set")
	}

	want := map[vocabulary.Predicate]string{
		vocabulary.SceneLocationCurrent:       "scene.location.current",
		vocabulary.LocationRelationConnectsTo: "location.relation.connects-to",
		vocabulary.GeoLocationLatitude:        ssvocab.GeoLocationLatitude,
		vocabulary.GeoLocationLongitude:       ssvocab.GeoLocationLongitude,
	}
	registered := make(map[vocabulary.Predicate]bool)
	for _, predicate := range vocabulary.AllPredicates() {
		registered[predicate] = true
	}
	for predicate, canonical := range want {
		if predicate.String() != canonical {
			t.Errorf("predicate = %q, want canonical %q", predicate, canonical)
		}
		if !registered[predicate] {
			t.Errorf("predicate %q is absent from AllPredicates", predicate)
		}
	}
}

func TestPlaceVocabulary_RegistersKindsAndMultiplicity(t *testing.T) {
	cases := []struct {
		predicate vocabulary.Predicate
		subject   vocabulary.EntityKind
		object    vocabulary.EntityKind
		multi     bool
	}{
		{vocabulary.SceneLocationCurrent, vocabulary.EntityKindScene, vocabulary.EntityKindLocation, false},
		{vocabulary.WorldLocationCurrent, vocabulary.EntityKindCharacter, vocabulary.EntityKindLocation, false},
		{vocabulary.LocationRelationConnectsTo, vocabulary.EntityKindLocation, vocabulary.EntityKindLocation, true},
	}
	for _, tc := range cases {
		if !vocabulary.AllowsSubjectKind(tc.predicate, tc.subject) {
			t.Errorf("%q does not allow subject kind %q", tc.predicate, tc.subject)
		}
		if !vocabulary.AllowsObjectKind(tc.predicate, tc.object) {
			t.Errorf("%q does not allow object kind %q", tc.predicate, tc.object)
		}
		if got := vocabulary.IsMultiValued(tc.predicate); got != tc.multi {
			t.Errorf("IsMultiValued(%q) = %v, want %v", tc.predicate, got, tc.multi)
		}
	}

	for _, predicate := range []vocabulary.Predicate{
		vocabulary.GeoLocationLatitude,
		vocabulary.GeoLocationLongitude,
	} {
		if !vocabulary.AllowsSubjectKind(predicate, vocabulary.EntityKindLocation) {
			t.Errorf("%q is not allowed on locations", predicate)
		}
		if vocabulary.AllowsSubjectKind(predicate, vocabulary.EntityKindScene) {
			t.Errorf("%q is allowed on scenes", predicate)
		}
		if vocabulary.IsEntityReference(predicate) || vocabulary.IsMultiValued(predicate) {
			t.Errorf("%q is not a single-valued numeric literal", predicate)
		}
	}
}
