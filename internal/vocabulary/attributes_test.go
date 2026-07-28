package vocabulary_test

import (
	"errors"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Every attribute in the closed set must carry a registered contract, and
// every contract's predicate must be one the write gate will accept. A
// registered attribute without a spec is an intent the applier can neither
// bound-check nor write.
func TestAttributeSpecs_CoverEveryAttributeWithARegisteredPredicate(t *testing.T) {
	registeredPredicates := make(map[vocabulary.Predicate]bool)
	for _, p := range vocabulary.AllPredicates() {
		registeredPredicates[p] = true
	}

	seenPredicates := make(map[vocabulary.Predicate]vocabulary.Attribute)
	for _, attr := range vocabulary.Attributes() {
		spec, ok := vocabulary.AttributeSpecFor(attr)
		if !ok {
			t.Fatalf("attribute %q is in the closed set but has no registered spec", attr)
		}
		if spec.Attribute != attr {
			t.Fatalf("spec for %q reports attribute %q", attr, spec.Attribute)
		}
		if !registeredPredicates[spec.Predicate] {
			t.Fatalf("attribute %q writes predicate %q, which is not in AllPredicates()", attr, spec.Predicate)
		}
		if err := vocabulary.ValidatePredicate(spec.Predicate); err != nil {
			t.Fatalf("attribute %q predicate %q is rejected by the write gate: %v", attr, spec.Predicate, err)
		}
		if spec.Min >= spec.Max {
			t.Fatalf("attribute %q has an empty or inverted range [%d, %d]", attr, spec.Min, spec.Max)
		}
		if prior, dup := seenPredicates[spec.Predicate]; dup {
			t.Fatalf("attributes %q and %q both write predicate %q", prior, attr, spec.Predicate)
		}
		seenPredicates[spec.Predicate] = attr
	}
}

func TestAttributeSpecFor_RejectsUnregisteredAttributes(t *testing.T) {
	for _, name := range []string{"hp", "mana", "Health", "", "gold"} {
		if _, ok := vocabulary.AttributeSpecFor(vocabulary.Attribute(name)); ok {
			t.Fatalf("unregistered attribute %q returned a spec", name)
		}
	}
}

// The bounds gate is the applier's rejection path for
// "out-of-bounds value is rejected". It must distinguish an unknown attribute
// from a known attribute with a bad value, because the recorded failure
// reason has to name the actual violation.
func TestCheckAttributeValue_AcceptsInclusiveBoundsAndRejectsOutside(t *testing.T) {
	for _, attr := range vocabulary.Attributes() {
		spec, ok := vocabulary.AttributeSpecFor(attr)
		if !ok {
			t.Fatalf("no spec for %q", attr)
		}

		for _, value := range []int{spec.Min, spec.Min + 1, spec.Max - 1, spec.Max} {
			if err := vocabulary.CheckAttributeValue(attr, value); err != nil {
				t.Fatalf("%q value %d is inside [%d, %d] but was rejected: %v",
					attr, value, spec.Min, spec.Max, err)
			}
		}

		for _, value := range []int{spec.Min - 1, spec.Max + 1, spec.Max * 100, spec.Min - 1000} {
			err := vocabulary.CheckAttributeValue(attr, value)
			if err == nil {
				t.Fatalf("%q value %d is outside [%d, %d] but was accepted",
					attr, value, spec.Min, spec.Max)
			}
			var bounds *vocabulary.OutOfBoundsError
			if !errors.As(err, &bounds) {
				t.Fatalf("expected *vocabulary.OutOfBoundsError for %q value %d, got %T", attr, value, err)
			}
			if bounds.Attribute != attr || bounds.Value != value {
				t.Fatalf("bounds error does not name the offending intent: %+v", bounds)
			}
			if bounds.Min != spec.Min || bounds.Max != spec.Max {
				t.Fatalf("bounds error reports [%d, %d], registered range is [%d, %d]",
					bounds.Min, bounds.Max, spec.Min, spec.Max)
			}
		}
	}
}

func TestCheckAttributeValue_UnknownAttributeIsAVocabularyRejection(t *testing.T) {
	err := vocabulary.CheckAttributeValue("hp", 5)
	if err == nil {
		t.Fatal("expected an unregistered attribute to be rejected")
	}
	var unknown *vocabulary.UnknownValueError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
	}
	if unknown.Kind != vocabulary.KindAttribute {
		t.Fatalf("error names kind %q, want %q", unknown.Kind, vocabulary.KindAttribute)
	}

	var bounds *vocabulary.OutOfBoundsError
	if errors.As(err, &bounds) {
		t.Fatal("an unknown attribute must not be reported as a bounds violation")
	}
}

// Worlds are data and the engine is world-agnostic, so attribute bounds are
// numbers the engine is HANDED, not numbers compiled into it. The v1 defaults
// stay; the shape must already let a world package replace them at load
// without an engine change.
func TestAttributeSpecSet_AWorldSuppliesItsOwnBoundsWithoutTouchingTheEngineDefaults(t *testing.T) {
	bounds := make(map[vocabulary.Attribute]vocabulary.AttributeBounds)
	for _, attr := range vocabulary.Attributes() {
		bounds[attr] = vocabulary.AttributeBounds{Min: -5, Max: 100}
	}

	world, err := vocabulary.NewAttributeSpecSet(bounds)
	if err != nil {
		t.Fatalf("a world could not declare its own bounds: %v", err)
	}

	for _, attr := range vocabulary.Attributes() {
		// -5 is inside the world's range and below every engine default
		// minimum, so it separates the two sets for every attribute.
		if err := world.Check(attr, -5); err != nil {
			t.Fatalf("the world's own bounds rejected a value it registered for %q: %v", attr, err)
		}
		if err := vocabulary.CheckAttributeValue(attr, -5); err == nil {
			t.Fatalf("a world's bounds leaked into the engine defaults for %q", attr)
		}
		if err := world.Check(attr, 101); err == nil {
			t.Fatalf("the world's own upper bound for %q was not enforced", attr)
		}
		if err := world.Check(attr, -6); err == nil {
			t.Fatalf("the world's own lower bound for %q was not enforced", attr)
		}

		// Predicates stay engine vocabulary: a world tunes numbers and can
		// never redirect a write onto a different rule-matched predicate.
		worldSpec, ok := world.For(attr)
		if !ok {
			t.Fatalf("the world set has no spec for registered attribute %q", attr)
		}
		engineSpec, ok := vocabulary.AttributeSpecFor(attr)
		if !ok {
			t.Fatalf("the engine defaults have no spec for %q", attr)
		}
		if worldSpec.Predicate != engineSpec.Predicate {
			t.Fatalf("attribute %q writes %q in the world set and %q in the engine set",
				attr, worldSpec.Predicate, engineSpec.Predicate)
		}
	}
}

// The engine defaults are the v1 numbers, reachable as a set as well as
// through the package-level helpers every caller uses today.
func TestDefaultAttributeSpecs_AreWhatThePackageLevelHelpersEnforce(t *testing.T) {
	defaults := vocabulary.DefaultAttributeSpecs()
	for _, attr := range vocabulary.Attributes() {
		fromSet, ok := defaults.For(attr)
		if !ok {
			t.Fatalf("DefaultAttributeSpecs has no spec for %q", attr)
		}
		fromHelper, ok := vocabulary.AttributeSpecFor(attr)
		if !ok {
			t.Fatalf("AttributeSpecFor has no spec for %q", attr)
		}
		if fromSet != fromHelper {
			t.Fatalf("attribute %q: default set says %+v, package helper says %+v", attr, fromSet, fromHelper)
		}
	}
	if _, ok := defaults.For("hp"); ok {
		t.Fatal("the default set returned a spec for an unregistered attribute")
	}
}

func TestNewAttributeSpecSet_IsFailClosed(t *testing.T) {
	complete := func() map[vocabulary.Attribute]vocabulary.AttributeBounds {
		bounds := make(map[vocabulary.Attribute]vocabulary.AttributeBounds)
		for _, attr := range vocabulary.Attributes() {
			bounds[attr] = vocabulary.AttributeBounds{Min: 0, Max: 10}
		}
		return bounds
	}

	t.Run("a world must declare every registered attribute", func(t *testing.T) {
		bounds := complete()
		delete(bounds, vocabulary.AttributeTension)

		if _, err := vocabulary.NewAttributeSpecSet(bounds); err == nil {
			t.Fatal("a partial declaration was accepted; the world would silently run on engine numbers")
		}
	})

	t.Run("an unregistered attribute is a vocabulary rejection", func(t *testing.T) {
		bounds := complete()
		bounds["mana"] = vocabulary.AttributeBounds{Min: 0, Max: 10}

		_, err := vocabulary.NewAttributeSpecSet(bounds)
		if err == nil {
			t.Fatal("a world declared bounds for an attribute the engine has no predicate for")
		}
		var unknown *vocabulary.UnknownValueError
		if !errors.As(err, &unknown) {
			t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
		}
	})

	t.Run("an inverted range is rejected", func(t *testing.T) {
		bounds := complete()
		bounds[vocabulary.AttributeHealth] = vocabulary.AttributeBounds{Min: 10, Max: 0}

		if _, err := vocabulary.NewAttributeSpecSet(bounds); err == nil {
			t.Fatal("an inverted range was accepted; every value would be out of bounds")
		}
	})

	t.Run("a single-value range is legal", func(t *testing.T) {
		bounds := complete()
		bounds[vocabulary.AttributeTension] = vocabulary.AttributeBounds{Min: 3, Max: 3}

		set, err := vocabulary.NewAttributeSpecSet(bounds)
		if err != nil {
			t.Fatalf("a pinned attribute is a legal world choice: %v", err)
		}
		if err := set.Check(vocabulary.AttributeTension, 3); err != nil {
			t.Fatalf("the pinned value was rejected: %v", err)
		}
		if err := set.Check(vocabulary.AttributeTension, 4); err == nil {
			t.Fatal("a value outside the pinned range was accepted")
		}
	})
}

func TestRelationPredicates_CoverEveryRelationAndAreWriteGateValid(t *testing.T) {
	registered := make(map[vocabulary.Predicate]bool)
	for _, p := range vocabulary.AllPredicates() {
		registered[p] = true
	}

	seen := make(map[vocabulary.Predicate]vocabulary.Relation)
	for _, rel := range vocabulary.Relations() {
		predicate, ok := vocabulary.RelationPredicate(rel)
		if !ok {
			t.Fatalf("relation %q is in the closed set but has no predicate", rel)
		}
		if !registered[predicate] {
			t.Fatalf("relation %q maps to %q, which is not in AllPredicates()", rel, predicate)
		}
		if err := vocabulary.ValidatePredicate(predicate); err != nil {
			t.Fatalf("relation predicate %q is rejected by the write gate: %v", predicate, err)
		}
		if prior, dup := seen[predicate]; dup {
			t.Fatalf("relations %q and %q both map to %q", prior, rel, predicate)
		}
		seen[predicate] = rel
	}
}

func TestRelationPredicate_RejectsUnregisteredRelations(t *testing.T) {
	for _, name := range []string{"owns", "allied_with", "AlliedWith", ""} {
		if _, ok := vocabulary.RelationPredicate(vocabulary.Relation(name)); ok {
			t.Fatalf("unregistered relation %q returned a predicate", name)
		}
	}
}
