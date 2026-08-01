package rulepack_test

import (
	"encoding/json"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestAccusationUniversalBarrierIsRoutedAndRequiredBeforeNarration(t *testing.T) {
	declare(t)
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatal(err)
	}
	var cfg rule.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	portFound, interpretationFound, applicationFound := false, false, false
	for _, port := range cfg.Ports.Outputs {
		if port.Subject == rulepack.SubjectAccusation && port.StreamName == rulepack.StageStream {
			portFound = true
		}
	}
	for _, definition := range cfg.InlineRules {
		switch definition.ID {
		case "turn-interpretation-enters-adjudication":
			interpretationFound = true
			if len(definition.Conditions) != 2 {
				t.Fatalf("interpretation conditions = %#v", definition.Conditions)
			}
			for _, actions := range [][]rule.Action{definition.OnEnter, definition.OnRecovery} {
				found := false
				for _, action := range actions {
					found = found || action.Subject == rulepack.SubjectAccusation
				}
				if !found {
					t.Fatalf("every committed interpretation does not publish accusation: %#v", actions)
				}
			}
		case "turn-committed-effects-enter-narration":
			applicationFound = true
			fields := map[string]bool{}
			for _, condition := range definition.Conditions {
				fields[condition.Field] = true
			}
			for _, required := range []string{
				vocabulary.TurnEffectsBatch.String(), vocabulary.TurnKnowledgeRef.String(),
				vocabulary.TurnAccusationRef.String(),
			} {
				if !fields[required] {
					t.Fatalf("narration can race missing barrier %s: %#v", required, definition.Conditions)
				}
			}
		}
	}
	if !portFound || !interpretationFound || !applicationFound {
		t.Fatalf("wiring missing: port=%v interpretation=%v application=%v",
			portFound, interpretationFound, applicationFound)
	}
}

func TestApplyingRuleDoesNotMatchUntilAccusationArtifactLands(t *testing.T) {
	declare(t)
	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	var applying rule.Definition
	for _, definition := range definitions {
		if definition.ID == "turn-committed-effects-enter-narration" {
			applying = definition
		}
	}
	if applying.ID == "" {
		t.Fatal("missing applying rule")
	}
	entityID := "c360.semmachina.test.starter.turn.turn-one"
	state := &graph.EntityState{ID: entityID, Triples: []message.Triple{
		{Subject: entityID, Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseApplying)},
		{Subject: entityID, Predicate: vocabulary.TurnEffectsBatch.String(), Object: "batch-one"},
		{Subject: entityID, Predicate: vocabulary.TurnKnowledgeRef.String(), Object: "obj://TEST/turn/turn-one/knowledge"},
	}}
	evaluator := expression.NewExpressionEvaluator()
	expr := expression.LogicalExpression{Conditions: applying.Conditions, Logic: applying.Logic}
	matched, err := evaluator.Evaluate(state, expr)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("applying rule matched before turn.accusation.ref landed")
	}
	state.Triples = append(state.Triples, message.Triple{Subject: entityID,
		Predicate: vocabulary.TurnAccusationRef.String(), Object: "obj://TEST/turn/turn-one/accusation"})
	matched, err = evaluator.Evaluate(state, expr)
	if err != nil || !matched {
		t.Fatalf("applying rule after accusation artifact = %v, %v", matched, err)
	}
}
