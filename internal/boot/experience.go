package boot

import (
	"fmt"
	"io/fs"
	"strings"

	sspersona "github.com/c360studio/semstreams/persona"
	entitytypes "github.com/c360studio/semstreams/pkg/types"
	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/internal/world"
)

type selectedMechanicsDefinition struct {
	file       string
	definition rule.Definition
}

// bindSelectedExperience owns decoded values for the lifetime of the Engine.
// Runtime steps never return to the mutable package filesystem.
func bindSelectedExperience(
	fsys fs.FS, plan *world.Plan,
) ([]sspersona.Persona, []selectedMechanicsDefinition, error) {
	personas := make([]sspersona.Persona, 0, len(plan.Experience.PersonaFiles))
	for _, name := range plan.Experience.PersonaFiles {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, nil, fmt.Errorf("bind selected persona file %s: %w", name, err)
		}
		record, err := world.DecodePersonaRecord(data)
		if err != nil {
			return nil, nil, fmt.Errorf("bind selected persona file %s: %w", name, err)
		}
		owned := *record
		owned.Roles = append([]string(nil), record.Roles...)
		personas = append(personas, owned)
	}
	if err := world.ValidatePersonaRoleCoverage(plan.Experience.PersonaPack, personas); err != nil {
		return nil, nil, fmt.Errorf("bind selected personas: %w", err)
	}

	mechanics := make([]selectedMechanicsDefinition, 0)
	for _, name := range plan.Experience.MechanicsFiles {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, nil, fmt.Errorf("bind selected mechanics file %s: %w", name, err)
		}
		definitions, err := world.DecodeRuleDefinitions(data)
		if err != nil {
			return nil, nil, fmt.Errorf("bind selected mechanics file %s: %w", name, err)
		}
		for _, definition := range definitions {
			narrowed, err := narrowWorldDefinition(plan, definition)
			if err != nil {
				return nil, nil, fmt.Errorf("bind selected mechanics file %s: %w", name, err)
			}
			mechanics = append(mechanics, selectedMechanicsDefinition{file: name, definition: narrowed})
		}
	}
	return personas, mechanics, nil
}

func validateCachedPersonas(plan *world.Plan, records []sspersona.Persona) error {
	if len(records) != len(plan.Experience.PersonaFiles) {
		return fmt.Errorf("cached persona count %d does not match sealed selected file count %d",
			len(records), len(plan.Experience.PersonaFiles))
	}
	for index := range records {
		if err := world.ValidatePersonaRecord(&records[index]); err != nil {
			return fmt.Errorf("validate cached persona record %s: %w",
				plan.Experience.PersonaFiles[index], err)
		}
	}
	return nil
}

func narrowWorldDefinition(plan *world.Plan, definition rule.Definition) (rule.Definition, error) {
	if definition.Type != "expression" {
		return rule.Definition{}, fmt.Errorf(
			"world rule %q has type %q, which has no instance-scoped entity pattern",
			definition.ID, definition.Type)
	}
	if definition.Entity.Pattern == "" {
		return rule.Definition{}, fmt.Errorf("world rule %q has no instance-scopeable entity pattern", definition.ID)
	}
	primary, err := narrowWorldPattern(plan, definition.Entity.Pattern)
	if err != nil {
		return rule.Definition{}, fmt.Errorf("world rule %q entity.pattern: %w", definition.ID, err)
	}
	related := make([]string, len(definition.RelatedPatterns))
	for index, pattern := range definition.RelatedPatterns {
		related[index], err = narrowWorldPattern(plan, pattern)
		if err != nil {
			return rule.Definition{}, fmt.Errorf("world rule %q related_patterns[%d]: %w",
				definition.ID, index, err)
		}
	}
	definition.Entity.Pattern = primary
	definition.Entity.WatchBuckets = append([]string(nil), definition.Entity.WatchBuckets...)
	definition.RelatedPatterns = related
	return definition, nil
}

func narrowWorldPattern(plan *world.Plan, pattern string) (string, error) {
	if err := entitytypes.ValidateEntityIDPattern(pattern); err != nil {
		return "", err
	}
	parts := strings.Split(pattern, ".")
	instance := []string{plan.Org, "semmachina", plan.WorldNS, plan.TemplateID}
	for index, exact := range instance {
		switch parts[index] {
		case "*":
			parts[index] = exact
		case exact:
		default:
			return "", fmt.Errorf("position %d literal %q is incompatible with instance %q",
				index+1, parts[index], exact)
		}
	}
	return strings.Join(parts, "."), nil
}
