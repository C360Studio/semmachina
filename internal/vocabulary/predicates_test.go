package vocabulary_test

import (
	"errors"
	"strings"
	"testing"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Every predicate this package defines must be accepted by the SAME parser
// the ENTITY_STATES write gate runs. A locally-written regex would prove
// nothing: semstreams rejects a non-conforming predicate at write time, so a
// predicate that fails here is a triple that silently never lands.
func TestPredicates_AcceptedByUpstreamWriteGateParser(t *testing.T) {
	predicates := vocabulary.AllPredicates()
	if len(predicates) == 0 {
		t.Fatal("AllPredicates() is empty; the engine writes no predicates")
	}

	for _, p := range predicates {
		t.Run(p.String(), func(t *testing.T) {
			parts, err := ssvocab.ParsePredicate(p.String())
			if err != nil {
				t.Fatalf("upstream ParsePredicate rejected %q: %v", p, err)
			}
			if got := parts.String(); got != p.String() {
				t.Fatalf("upstream reassembly changed the identity: got %q want %q", got, p)
			}
			if parts.Domain == "" || parts.Category == "" || parts.Property == "" {
				t.Fatalf("upstream decomposition has an empty segment: %+v", parts)
			}
		})
	}
}

// Guards the test above from being vacuous. If the upstream parser accepted
// everything, TestPredicates_AcceptedByUpstreamWriteGateParser would pass no
// matter what we declared. These are the exact shapes the design's F2 finding
// says are rejected.
func TestUpstreamWriteGateParser_RejectsTheShapesF2Names(t *testing.T) {
	rejected := map[string]string{
		"two segments (the pre-F2 turn.phase)": "turn.phase",
		"four segments":                        "turn.verdict.requires.roll",
		"underscore in a segment":              "turn.verdict.requires_roll",
		"uppercase segment start":              "Turn.phase.current",
		"empty middle segment":                 "turn..current",
		"trailing hyphen":                      "turn.phase.current-",
		"leading digit":                        "turn.phase.1current",
		"empty":                                "",
		"segment over 64 bytes":                "turn.phase." + strings.Repeat("a", 65),
	}

	for name, predicate := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := ssvocab.ParsePredicate(predicate); err == nil {
				t.Fatalf("upstream ParsePredicate accepted %q; the write-gate proof above is vacuous", predicate)
			}
		})
	}
}

// ValidatePredicate must return the same decision as the write gate, because
// its whole purpose is to let a component reject before issuing a write.
func TestValidatePredicate_MatchesUpstreamDecision(t *testing.T) {
	cases := []string{
		"turn.phase.current",
		"turn.verdict.requires-roll",
		"turn.phase",
		"turn.verdict.requires_roll",
		"",
		"a.b.c",
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, upstreamErr := ssvocab.ParsePredicate(c)
			localErr := vocabulary.ValidatePredicate(vocabulary.Predicate(c))
			if (upstreamErr == nil) != (localErr == nil) {
				t.Fatalf("decision diverged for %q: upstream=%v local=%v", c, upstreamErr, localErr)
			}
			if localErr != nil {
				var contractErr *ssvocab.PredicateValidationError
				if !errors.As(localErr, &contractErr) {
					t.Fatalf("expected the upstream contract error to survive wrapping, got %T", localErr)
				}
			}
		})
	}
}

func TestAllPredicates_HasNoDuplicates(t *testing.T) {
	seen := make(map[vocabulary.Predicate]int)
	for i, p := range vocabulary.AllPredicates() {
		if first, dup := seen[p]; dup {
			t.Fatalf("predicate %q listed at index %d and %d", p, first, i)
		}
		seen[p] = i
	}
}

func TestAllPredicates_IsACopyCallersCannotCorrupt(t *testing.T) {
	first := vocabulary.AllPredicates()
	original := first[0]
	first[0] = "mutated.by.caller"

	if got := vocabulary.AllPredicates()[0]; got != original {
		t.Fatalf("a caller mutated the shared predicate list: got %q want %q", got, original)
	}
}

// The verdict's rule-matched projection must be exactly the small scalars.
// Anything bulky travels by reference, so a predicate that appears here and
// is not a registered scalar is a design regression, not a typo.
func TestVerdictScalarPredicates_AreRegisteredAndScalarOnly(t *testing.T) {
	all := make(map[vocabulary.Predicate]bool, len(vocabulary.AllPredicates()))
	for _, p := range vocabulary.AllPredicates() {
		all[p] = true
	}

	scalars := vocabulary.VerdictScalarPredicates()
	want := []vocabulary.Predicate{
		vocabulary.TurnVerdictPlausibility,
		vocabulary.TurnVerdictRisk,
		vocabulary.TurnVerdictConsequence,
		vocabulary.TurnVerdictRequiresRoll,
	}
	if len(scalars) != len(want) {
		t.Fatalf("verdict scalar predicates: got %v want %v", scalars, want)
	}
	for i, p := range want {
		if scalars[i] != p {
			t.Fatalf("verdict scalar predicate %d: got %q want %q", i, scalars[i], p)
		}
		if !all[p] {
			t.Fatalf("verdict scalar predicate %q is not in AllPredicates()", p)
		}
	}
	for _, p := range scalars {
		if p == vocabulary.TurnVerdictRef {
			t.Fatal("the verdict reference is not a scalar; bulky banded intents must ride by reference")
		}
	}
}
