package rulepack

import (
	"fmt"

	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// CaseEntityPattern is the exact case lifecycle watch scope.
const CaseEntityPattern = "*.semmachina.*.*.case.*"

type caseEdge struct {
	id   string
	kind vocabulary.CaseLifecycleEventKind
	from vocabulary.CasePhase
	to   vocabulary.CasePhase
}

var caseEdges = []caseEdge{
	{"case-body-observed-enters-discovery", vocabulary.CaseEventBodyObserved, vocabulary.CasePhaseColdOpen, vocabulary.CasePhaseDiscovery},
	{"case-investigation-started-enters-investigation", vocabulary.CaseEventInvestigationStarted, vocabulary.CasePhaseDiscovery, vocabulary.CasePhaseInvestigation},
	{"case-accusation-submitted-enters-accusation", vocabulary.CaseEventAccusationSubmitted, vocabulary.CasePhaseInvestigation, vocabulary.CasePhaseAccusation},
	{"case-correct-accusation-enters-denouement", vocabulary.CaseEventAccusationCorrect, vocabulary.CasePhaseAccusation, vocabulary.CasePhaseDenouement},
}

// CaseLifecycleDefinitions returns one built-in rule for each legal case edge.
func CaseLifecycleDefinitions() ([]rule.Definition, error) {
	definitions := make([]rule.Definition, 0, len(caseEdges))
	for _, edge := range caseEdges {
		action := rule.Action{
			Type:     rule.ActionTypeLifecycleTransition,
			Workflow: caseflow.WorkflowName,
			Phase:    string(edge.to),
		}
		definition := rule.Definition{
			ID:          edge.id,
			Type:        "expression",
			Name:        edge.id,
			Description: "Advance the case only from a complete structural event receipt.",
			Enabled:     true,
			Logic:       "and",
			Entity:      rule.EntityConfig{Pattern: CaseEntityPattern},
			Conditions: []expression.ConditionExpression{
				{Field: vocabulary.CaseLifecyclePhase.String(), Operator: "eq", Value: string(edge.from)},
				{Field: vocabulary.CaseLifecycleEventID.String(), Operator: "ne", Value: ""},
				{Field: vocabulary.CaseLifecycleEventKindPredicate.String(), Operator: "eq", Value: string(edge.kind)},
				{Field: vocabulary.CaseLifecycleFromPhase.String(), Operator: "eq", Value: string(edge.from)},
				{Field: vocabulary.CaseLifecycleToPhase.String(), Operator: "eq", Value: string(edge.to)},
			},
			OnEnter:    []rule.Action{action},
			OnRecovery: []rule.Action{action},
		}
		if err := rule.ValidateDefinition(definition); err != nil {
			return nil, fmt.Errorf("case lifecycle rule %q: %w", definition.ID, err)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}
