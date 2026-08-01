package vocabulary_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The derivation has to find the reference predicates that actually exist, and
// exactly those. A suffix rule that matched nothing would report a clean,
// empty, useless answer to every caller — the storage layer would have no
// predicates to key on and nothing would fail.
func TestStorageRefPredicates_AreTheRefSuffixedOnes(t *testing.T) {
	refs := vocabulary.StorageRefPredicates()
	if len(refs) == 0 {
		t.Fatal("no storage-reference predicates were derived; the derivation is vacuous and every caller " +
			"keyed on it silently does nothing")
	}

	derived := make(map[vocabulary.Predicate]bool, len(refs))
	for _, p := range refs {
		derived[p] = true
		if !strings.HasSuffix(p.String(), "."+vocabulary.StorageRefProperty) {
			t.Fatalf("%q was derived as a storage reference but does not end in %q",
				p, vocabulary.StorageRefProperty)
		}
	}

	// And the other direction, off the authoritative list: a `.ref` predicate
	// the derivation missed would be a reference with no storage slot, which is
	// a triple pointing at a location nothing computes.
	for _, p := range vocabulary.AllPredicates() {
		suffixed := strings.HasSuffix(p.String(), "."+vocabulary.StorageRefProperty)
		if suffixed != derived[p] {
			t.Fatalf("predicate %q: name says storage-reference=%v, derivation says %v", p, suffixed, derived[p])
		}
	}
}

// The five reference predicates the slice writes, named explicitly. A
// derivation test alone would pass if someone deleted a predicate.
func TestStorageRefPredicates_CoverEveryTurnArtifact(t *testing.T) {
	want := map[vocabulary.Predicate]string{
		vocabulary.TurnActionRef:          "action",
		vocabulary.TurnCaseDecisionRef:    "case-decision",
		vocabulary.TurnKnowledgeRef:       "knowledge",
		vocabulary.TurnVerdictRef:         "verdict",
		vocabulary.TurnRollRef:            "roll",
		vocabulary.TurnEffectsRef:         "effects",
		vocabulary.TurnNarrationRef:       "narration",
		vocabulary.TurnFailureRef:         "failure",
		vocabulary.RevelationTestimonyRef: "testimony",
	}

	got := vocabulary.StorageRefPredicates()
	if len(got) != len(want) {
		t.Fatalf("derived %d storage-reference predicates, want %d: %v", len(got), len(want), got)
	}
	for _, p := range got {
		slot, ok := want[p]
		if !ok {
			t.Fatalf("%q is a storage-reference predicate this test does not know about; every reference "+
				"needs a stored artifact behind it", p)
		}
		derivedSlot, err := vocabulary.ArtifactSlot(p)
		if err != nil {
			t.Fatalf("ArtifactSlot(%q): %v", p, err)
		}
		if derivedSlot != slot {
			t.Fatalf("%q stores under slot %q, want %q", p, derivedSlot, slot)
		}
	}
}

// Two reference predicates sharing one slot would overwrite each other's
// artifact at the same key — a turn's verdict landing on top of its narration,
// with a success from every layer.
func TestArtifactSlots_AreDistinctPerReferencePredicate(t *testing.T) {
	seen := make(map[string]vocabulary.Predicate)
	for _, p := range vocabulary.StorageRefPredicates() {
		slot, err := vocabulary.ArtifactSlot(p)
		if err != nil {
			t.Fatalf("ArtifactSlot(%q): %v", p, err)
		}
		if other, clash := seen[slot]; clash {
			t.Fatalf("%q and %q both store under slot %q; one turn's artifacts would collide at one key",
				p, other, slot)
		}
		seen[slot] = p
	}
}

// A value-bearing predicate has no artifact behind it, and asking for one is a
// caller bug rather than an empty answer: an empty slot would compose a key
// that silently addresses the wrong object.
func TestArtifactSlot_RefusesAPredicateThatIsNotAReference(t *testing.T) {
	for _, p := range []vocabulary.Predicate{
		vocabulary.TurnPhaseCurrent,
		vocabulary.TurnRollBand,
		vocabulary.WorldEntityName,
		"not a predicate at all",
	} {
		if slot, err := vocabulary.ArtifactSlot(p); err == nil {
			t.Fatalf("ArtifactSlot(%q) returned slot %q; that predicate carries a value, not a reference",
				p, slot)
		}
		if vocabulary.IsStorageRef(p) {
			t.Fatalf("%q was classified as a storage reference", p)
		}
	}
}
