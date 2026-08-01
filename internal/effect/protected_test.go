package effect

import (
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestValidateEffectPredicate_RejectsCanonicalMysteryTruth(t *testing.T) {
	for _, predicate := range vocabulary.ImmutablePredicates() {
		if violated := validateEffectPredicate(predicate); violated == nil {
			t.Errorf("effect gate accepted immutable predicate %q", predicate)
		}
	}
	if violated := validateEffectPredicate(vocabulary.CharacterStatusCurrent); violated != nil {
		t.Fatalf("effect gate rejected ordinary mutable predicate: %v", violated.err)
	}
}
