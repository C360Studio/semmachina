package boot

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/internal/rulepack"
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

	for _, selected := range e.mechanics {
		definition := selected.definition
		if previous, duplicate := seenIDs[definition.ID]; duplicate {
			return nil, fmt.Errorf(
				"duplicate rule id %q in selected mechanics file %s; already declared by %s",
				definition.ID, selected.file, previous)
		}
		seenIDs[definition.ID] = fmt.Sprintf("selected mechanics file %s", selected.file)
		if err := rule.ValidateDefinition(definition); err != nil {
			return nil, fmt.Errorf(
				"selected mechanics file %s rule %q failed runtime validation: %w",
				selected.file, definition.ID, err)
		}
		config.InlineRules = append(config.InlineRules, definition)
		if !seenPatterns[definition.Entity.Pattern] {
			watchPatterns = append(watchPatterns, definition.Entity.Pattern)
			seenPatterns[definition.Entity.Pattern] = true
		}
	}
	config.EntityWatchBuckets[rulepack.EntityStatesBucket] = watchPatterns
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("composed engine and selected world rule configuration: %w", err)
	}
	raw, err = json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode the composed rule configuration: %w", err)
	}
	return raw, nil
}
