package boot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/c360studio/semstreams/pkg/projection"
	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/world"
)

// ruleProcessorConfig composes the selected world mechanics after the fixed
// engine rules in one existing processor configuration. The resolved Plan is
// the sole inventory: Package.RuleFiles is intentionally never consulted.
func (e *Engine) ruleProcessorConfig() (json.RawMessage, error) {
	if e.plan == nil {
		return nil, errors.New("compose rule configuration: the world has no resolved plan")
	}
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		return nil, fmt.Errorf("build the fixed turn-sequencing rule configuration: %w", err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("decode the fixed turn-sequencing rule configuration: %w", err)
	}

	seenIDs := make(map[string]string, len(config.InlineRules))
	for index, definition := range config.InlineRules {
		if previous, duplicate := seenIDs[definition.ID]; duplicate {
			return nil, fmt.Errorf("fixed engine rule %q duplicates %s", definition.ID, previous)
		}
		seenIDs[definition.ID] = fmt.Sprintf("fixed engine rule at inline index %d", index)
	}
	watchPatterns := config.EntityWatchBuckets[rulepack.EntityStatesBucket]
	seenPatterns := make(map[string]bool, len(watchPatterns))
	for _, pattern := range watchPatterns {
		seenPatterns[pattern] = true
	}
	selectedDefinitions := make([]rule.Definition, 0, len(e.mechanics))

	for _, selected := range e.mechanics {
		definition := selected.definition
		if previous, duplicate := seenIDs[definition.ID]; duplicate {
			return nil, fmt.Errorf(
				"duplicate rule id %q in selected mechanics file %s; already declared by %s",
				definition.ID, selected.file, previous)
		}
		seenIDs[definition.ID] = fmt.Sprintf("selected mechanics file %s", selected.file)
		if err := bindSelectedMechanicsActions(
			&definition,
			selected.file,
			selectedMechanicsContractName(e.plan.Experience.MechanicsPack),
		); err != nil {
			return nil, err
		}
		if err := rule.ValidateDefinition(definition); err != nil {
			return nil, fmt.Errorf(
				"selected mechanics file %s rule %q failed runtime validation: %w",
				selected.file, definition.ID, err)
		}
		if definitionMutatesGraph(definition) {
			// The fixed turn pack only publishes stage triggers, so its base
			// configuration deliberately leaves graph integration off. Selected
			// mechanics can additionally author structural graph reactions. Their
			// typed reconciler is bound separately before activation; graph
			// integration also keeps the processor's graph-event lane enabled.
			config.EnableGraphIntegration = true
		}
		config.InlineRules = append(config.InlineRules, definition)
		selectedDefinitions = append(selectedDefinitions, definition)
		if !seenPatterns[definition.Entity.Pattern] {
			watchPatterns = append(watchPatterns, definition.Entity.Pattern)
			seenPatterns[definition.Entity.Pattern] = true
		}
	}
	config.EntityWatchBuckets[rulepack.EntityStatesBucket] = watchPatterns
	contract, err := selectedMechanicsProjection(e.plan, selectedDefinitions)
	if err != nil {
		return nil, err
	}
	if contract != nil {
		config.ProjectionContracts = []projection.Contract{*contract}
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("composed engine and selected world rule configuration: %w", err)
	}
	raw, err = json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode the composed rule configuration: %w", err)
	}
	return raw, nil
}

func selectedMechanicsContractName(pack string) string {
	return "mechanics-" + pack
}

func selectedMechanicsGroupName(predicate string) string {
	// Canonical predicates cannot contain underscores, while projection group
	// names may. Replacing their two dots is therefore a reversible,
	// collision-free encoding rather than a heuristic slug or truncated hash.
	return "predicate_" + strings.ReplaceAll(predicate, ".", "_")
}

func bindSelectedMechanicsActions(
	definition *rule.Definition,
	file string,
	contract string,
) error {
	lists := []struct {
		name    string
		actions *[]rule.Action
	}{
		{"on_enter", &definition.OnEnter},
		{"on_exit", &definition.OnExit},
		{"while_true", &definition.WhileTrue},
		{"on_recovery", &definition.OnRecovery},
		{"actions", &definition.Actions},
	}
	for _, list := range lists {
		for index := range *list.actions {
			action := &(*list.actions)[index]
			switch action.Type {
			case rule.ActionTypeAddTriple:
				return fmt.Errorf(
					"selected mechanics file %s rule %q %s[%d] uses add_triple; append semantics do not declare a complete desired group and cannot be migrated implicitly to beta.160 reconcile_predicates",
					file, definition.ID, list.name, index,
				)
			case rule.ActionTypeRemoveTriple, rule.ActionTypeUpdateTriple:
				action.Type = rule.ActionTypeReconcilePredicates
			case rule.ActionTypeReconcilePredicates:
			default:
				continue
			}

			group := selectedMechanicsGroupName(action.Predicate)
			if (action.ProjectionContract == "") != (action.ProjectionGroup == "") {
				return fmt.Errorf(
					"selected mechanics file %s rule %q %s[%d] must omit both projection selectors for derivation or declare both",
					file, definition.ID, list.name, index,
				)
			}
			if action.ProjectionContract != "" &&
				(action.ProjectionContract != contract || action.ProjectionGroup != group) {
				return fmt.Errorf(
					"selected mechanics file %s rule %q %s[%d] declares projection %q/%q; selected pack requires %q/%q",
					file, definition.ID, list.name, index,
					action.ProjectionContract, action.ProjectionGroup, contract, group,
				)
			}
			action.ProjectionContract = contract
			action.ProjectionGroup = group
		}
	}
	return nil
}

func selectedMechanicsProjection(plan *world.Plan, definitions []rule.Definition) (*projection.Contract, error) {
	if plan == nil {
		return nil, errors.New("selected mechanics projection requires a resolved world plan")
	}
	predicates := make(map[string]string)
	for _, definition := range definitions {
		for _, actions := range [][]rule.Action{
			definition.OnEnter, definition.OnExit, definition.WhileTrue, definition.OnRecovery, definition.Actions,
		} {
			for _, action := range actions {
				if action.Type != rule.ActionTypeReconcilePredicates {
					continue
				}
				predicates[action.Predicate] = action.ProjectionGroup
			}
		}
	}
	if len(predicates) == 0 {
		return nil, nil
	}
	predicateNames := make([]string, 0, len(predicates))
	for predicate := range predicates {
		predicateNames = append(predicateNames, predicate)
	}
	sort.Strings(predicateNames)
	groups := make([]projection.PredicateGroup, 0, len(predicateNames))
	for _, predicate := range predicateNames {
		groups = append(groups, projection.PredicateGroup{
			Name: predicates[predicate], Mode: projection.ModeReconcile, Predicates: []string{predicate},
		})
	}
	contract := projection.Contract{
		Name: selectedMechanicsContractName(plan.Experience.MechanicsPack),
		EntityPattern: strings.Join(
			[]string{plan.Org, "semmachina", plan.WorldNS, plan.TemplateID, "*", "*"}, ".",
		),
		Groups: groups,
	}
	if err := contract.Validate(); err != nil {
		return nil, fmt.Errorf("validate selected mechanics projection: %w", err)
	}
	return &contract, nil
}

func definitionMutatesGraph(definition rule.Definition) bool {
	for _, actions := range [][]rule.Action{
		definition.OnEnter,
		definition.OnExit,
		definition.WhileTrue,
		definition.OnRecovery,
		definition.Actions,
	} {
		for _, action := range actions {
			switch action.Type {
			case rule.ActionTypeAddTriple,
				rule.ActionTypeRemoveTriple,
				rule.ActionTypeUpdateTriple,
				rule.ActionTypeReconcilePredicates:
				return true
			}
		}
	}
	return false
}
