package vocabulary_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Every test in this file writes the semstreams PROCESS-GLOBAL predicate
// registry, so none of them may be t.Parallel and every one of them restores
// what it found. A leaked registration would make a later test pass for a
// reason it never stated — which is the exact failure mode this whole
// registration pass exists to make impossible.

func TestRegisterPredicates_DeclaresEveryPredicateTheEngineWrites(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()

	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}
	for _, predicate := range vocabulary.AllPredicates() {
		if err := ssvocab.RequireDeclaredPredicate(predicate.String()); err != nil {
			t.Errorf("predicate %s is not declared after registration: %v", predicate, err)
		}
	}
}

// The anti-vacuity control for the test above: without registration the gate
// rule configuration runs actually REFUSES these predicates, so "declared after
// registration" is a fact about the registration and not about the gate being
// permissive.
func TestRequireDeclaredPredicate_RefusesACanonicalButUnregisteredPredicate(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()

	err := ssvocab.RequireDeclaredPredicate(vocabulary.TurnPhaseCurrent.String())
	if err == nil {
		t.Fatalf("an unregistered %s was accepted; the rule pack's load-time gate proves nothing",
			vocabulary.TurnPhaseCurrent)
	}
	if _, parseErr := ssvocab.ParsePredicate(vocabulary.TurnPhaseCurrent.String()); parseErr != nil {
		t.Fatalf("%s does not even parse, so the refusal above is about syntax rather than declaration: %v",
			vocabulary.TurnPhaseCurrent, parseErr)
	}
}

func TestRegisterPredicates_MarksExactlyTheFictionBearingPredicatesRuleOpaque(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()

	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}

	want := map[vocabulary.Predicate]bool{}
	for _, predicate := range vocabulary.RuleOpaquePredicates() {
		want[predicate] = true
	}
	if len(want) == 0 {
		t.Fatal("no predicate is declared rule-opaque; M1 would then be a review convention again")
	}
	for _, predicate := range vocabulary.AllPredicates() {
		if got := ssvocab.IsRuleOpaque(predicate.String()); got != want[predicate] {
			t.Errorf("predicate %s: rule-opaque = %v, want %v", predicate, got, want[predicate])
		}
	}
}

// A reference is a structural pointer, not the thing it points at. Turn
// sequencing closes the turn by matching the arrival of turn.narration.ref, so
// flagging a reference predicate rule-opaque would fail the whole pack at load.
func TestRegisterPredicates_LeavesEveryStorageReferenceRuleMatchable(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()

	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}
	refs := vocabulary.StorageRefPredicates()
	if len(refs) == 0 {
		t.Fatal("no storage-reference predicates found; this test would pass vacuously")
	}
	for _, predicate := range refs {
		if ssvocab.IsRuleOpaque(predicate.String()) {
			t.Errorf("reference predicate %s is rule-opaque; the rule pack cannot match a stage's artifact landing",
				predicate)
		}
	}
}

func TestRegisterPredicates_IsIdempotent(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()

	for attempt := range 3 {
		if err := vocabulary.RegisterPredicates(); err != nil {
			t.Fatalf("RegisterPredicates attempt %d: %v", attempt, err)
		}
	}
	for _, predicate := range vocabulary.RuleOpaquePredicates() {
		if !ssvocab.IsRuleOpaque(predicate.String()) {
			t.Errorf("predicate %s lost its rule-opaque flag across repeated registration", predicate)
		}
	}
}

// Registration OVERWRITES rather than amends, so a prior declaration of one of
// our predicates cannot leave it rule-matchable behind our back.
func TestRegisterPredicates_OverridesAPriorContradictoryDeclaration(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()

	opaque := vocabulary.RuleOpaquePredicates()[0]
	ssvocab.Register(opaque.String(), ssvocab.WithRuleOpaque(false))
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}
	if !ssvocab.IsRuleOpaque(opaque.String()) {
		t.Errorf("%s stayed rule-matchable after a contradictory earlier declaration", opaque)
	}
}

// The two directions that matter, run through the REAL rule-config validator
// rather than through IsRuleOpaque: this is the gate the rule pack actually
// faces at load.

func TestRuleValidation_RefusesAConditionOnAFictionBearingPredicate(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}

	for _, opaque := range vocabulary.RuleOpaquePredicates() {
		err := rule.ValidateDefinition(definitionMatching(opaque, "anything"))
		if err == nil {
			t.Errorf("a rule branching on %s loaded; M1 is not enforced by the substrate", opaque)
			continue
		}
		if !strings.Contains(err.Error(), "rule-opaque") {
			t.Errorf("%s was refused for the wrong reason: %v", opaque, err)
		}
	}
}

func TestRuleValidation_AcceptsAConditionOnANarrationReference(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}

	definition := definitionMatching(vocabulary.TurnNarrationRef, "")
	definition.Conditions[0].Operator = "ne"
	if err := rule.ValidateDefinition(definition); err != nil {
		t.Fatalf("a rule matching %s did not load: %v", vocabulary.TurnNarrationRef, err)
	}
}

func TestRuleValidation_RefusesAConditionOnAnUnregisteredPredicate(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}

	// Canonical three-segment lower-kebab, and nobody declared it.
	err := rule.ValidateDefinition(definitionMatching("turn.phase.previous", "accepted"))
	if err == nil {
		t.Fatal("a rule branching on an undeclared predicate loaded; registration would then be optional")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared predicate refused for the wrong reason: %v", err)
	}
}

// A rule ACTION that writes an undeclared predicate is refused on the same
// gate, which is why the registration pass covers every predicate the engine
// writes rather than only the ones it matches on.
func TestRuleValidation_RefusesAnActionWritingAnUndeclaredPredicate(t *testing.T) {
	defer ssvocab.SnapshotRegistry()()
	ssvocab.ClearRegistry()
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}

	definition := definitionMatching(vocabulary.TurnPhaseCurrent, "accepted")
	definition.OnEnter = []rule.Action{{
		Type:      rule.ActionTypeAddTriple,
		Predicate: "turn.phase.previous",
		Object:    "accepted",
	}}
	err := rule.ValidateDefinition(definition)
	if err == nil {
		t.Fatal("a rule writing an undeclared predicate loaded")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared action predicate refused for the wrong reason: %v", err)
	}
}

func definitionMatching(field vocabulary.Predicate, value string) rule.Definition {
	return rule.Definition{
		ID:      "registration-test",
		Type:    "expression",
		Enabled: true,
		Logic:   "and",
		Conditions: []expression.ConditionExpression{{
			Field:    field.String(),
			Operator: "eq",
			Value:    value,
			Required: true,
		}},
	}
}
