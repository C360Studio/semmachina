package world_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestLoadPackage_RefusesRulesThatReadOrWriteProtectedMysteryTruth(t *testing.T) {
	for _, predicate := range vocabulary.ImmutablePredicates() {
		predicate := predicate
		t.Run(predicate.String()+" condition", func(t *testing.T) {
			rule := ruleJSON("reads_truth",
				fmt.Sprintf(`{"field":%q,"operator":"exists","value":true}`, predicate),
				"on_enter", ``)
			err := loadWithRule(t, rule)
			if err == nil {
				t.Fatalf("world rule read protected predicate %q", predicate)
			}
			for _, want := range []string{predicate.String(), "reserved", "reads_truth"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
		})

		t.Run(predicate.String()+" mutation", func(t *testing.T) {
			rule := ruleJSON("writes_truth", ``, "on_enter",
				fmt.Sprintf(`{"type":"update_triple","predicate":%q,"object":"replacement"}`, predicate))
			err := loadWithRule(t, rule)
			if err == nil {
				t.Fatalf("world rule wrote protected predicate %q", predicate)
			}
			for _, want := range []string{predicate.String(), "reserved", "writes_truth"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}
