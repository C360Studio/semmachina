package vocabulary

import (
	"fmt"

	ssvocab "github.com/c360studio/semstreams/vocabulary"
)

// StorageRefProperty is the property segment every storage-reference predicate
// ends in.
//
// It is a CONTRACT and not a naming habit. A predicate whose object is a
// storage reference is the engine's escape hatch from the triple-object
// budget — prose, banded intents, dice, the batch that was refused all reach
// the graph through one — and three separate rules key off recognizing one:
//
//   - the object is a pointer, so the predicate stays rule-matchable while a
//     fiction-bearing predicate does not (task 8.0's RuleOpaque pass);
//   - the pointer resolves in exactly one place, so the artifact's storage key
//     is DERIVED from the predicate rather than chosen per writer;
//   - nothing else may be written there, which is why the name says so — a
//     sentence handed to turn.failure.ref passes every shape gate on the way
//     to the graph.
//
// Deriving all three from the name means a new reference predicate cannot be
// added without inheriting them, and TestStorageRefPredicates_AreTheRefSuffixed
// -Ones proves the derivation is not vacuous.
const StorageRefProperty = "ref"

// IsStorageRef reports whether p's object is a reference to a stored artifact
// rather than a value.
func IsStorageRef(p Predicate) bool {
	parts, err := ssvocab.ParsePredicate(p.String())
	if err != nil {
		return false
	}
	return parts.Property == StorageRefProperty
}

// StorageRefPredicates returns every predicate whose object is a storage
// reference, in AllPredicates() order.
func StorageRefPredicates() []Predicate {
	out := make([]Predicate, 0, len(allPredicates))
	for _, p := range allPredicates {
		if IsStorageRef(p) {
			out = append(out, p)
		}
	}
	return out
}

// ArtifactSlot is the storage-key segment that identifies WHICH artifact a
// reference predicate points at.
//
// It is the predicate's CATEGORY segment, derived rather than tabulated:
// turn.action.ref stores under `action`, turn.narration.ref under `narration`.
// A lookup table mapping predicate to slot would be a second place for the
// pairing to be stated, and therefore a place for it to disagree — the storage
// key would say `action` while the triple said narration, and both would look
// right in isolation.
//
// The categories are distinct across the reference predicates by construction
// (they are what makes the predicates distinct), which
// TestArtifactSlots_AreDistinctPerReferencePredicate holds to.
func ArtifactSlot(p Predicate) (string, error) {
	parts, err := ssvocab.ParsePredicate(p.String())
	if err != nil {
		return "", fmt.Errorf("predicate %q has no artifact slot: %w", p, err)
	}
	if parts.Property != StorageRefProperty {
		return "", fmt.Errorf(
			"predicate %q is not a storage reference (its property segment is %q, not %q), so it addresses no "+
				"stored artifact", p, parts.Property, StorageRefProperty)
	}
	return parts.Category, nil
}
