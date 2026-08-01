package rulepack_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestCaseLifecycleRulesAreExactAndRecoverySafe(t *testing.T) {
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatal(err)
	}
	definitions, err := rulepack.CaseLifecycleDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 4 {
		t.Fatalf("definitions = %d, want 4", len(definitions))
	}
	for _, definition := range definitions {
		if definition.Entity.Pattern != rulepack.CaseEntityPattern || len(definition.Conditions) != 5 {
			t.Fatalf("rule %q has pattern %q and %d conditions", definition.ID, definition.Entity.Pattern, len(definition.Conditions))
		}
		if !reflect.DeepEqual(definition.OnEnter, definition.OnRecovery) {
			t.Fatalf("rule %q entry/recovery actions differ", definition.ID)
		}
		if len(definition.OnEnter) != 1 || definition.OnEnter[0].Type != rule.ActionTypeLifecycleTransition {
			t.Fatalf("rule %q actions = %+v", definition.ID, definition.OnEnter)
		}
	}
}

func TestProcessorConfigWatchesCases(t *testing.T) {
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatal(err)
	}
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatal(err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if got := config.EntityWatchBuckets[rulepack.EntityStatesBucket]; !reflect.DeepEqual(got, []string{rulepack.EntityPattern, rulepack.CaseEntityPattern}) {
		t.Fatalf("watch patterns = %v", got)
	}
}
