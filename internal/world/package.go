package world

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/c360studio/semstreams/persona"
	"github.com/c360studio/semstreams/processor/rule"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// RulesDir holds the package's rule pack.
	RulesDir = "rules"
	// PersonasDir holds the package's persona configurations.
	PersonasDir = "personas"
)

// Package is a loaded, fully validated world template.
//
// Loading is deliberately total: a Package value exists only if its manifest,
// every entity line, its personas, and any rules it ships all checked out.
// Downstream code therefore never has to ask whether a package is valid, only
// what it says.
type Package struct {
	// Manifest is the validated manifest v0.
	Manifest Manifest
	// Entities are the template entities in file order.
	Entities []TemplateEntity
	// RuleFiles and PersonaFiles are the package-relative paths of the world's
	// reactions and persona configurations, sorted. RuleFiles may be empty: a
	// world that reacts to nothing is a legitimate world (see optionalDir).
	//
	// Rule SEMANTICS are not interpreted here — they belong to the rule
	// processor, and re-deriving them at world-load would put a second rule
	// engine in the tree. What is checked is the part an author can only be
	// told about at import: the predicate syntax of every condition field, and
	// the boundary between a world reaction and the engine's own turn loop
	// (see checkRuleFile).
	RuleFiles    []string
	PersonaFiles []string
}

// LoadOptions carries what the loader cannot read from the package itself.
type LoadOptions struct {
	// EngineVersion is the running semstreams version engine_compat is checked
	// against. Empty means EngineVersion, the version this build links.
	EngineVersion string
	// AttributeSpecs bounds the numeric starting values a package may declare.
	// Nil means the engine defaults.
	//
	// Bounds are not in manifest v0, whose field set is closed, so today they
	// can only come from the caller. The seam exists now so that a later
	// manifest version supplying its own bounds is a parsing change rather than
	// a redesign of where bounds are enforced.
	AttributeSpecs *vocabulary.AttributeSpecSet
}

func (o LoadOptions) engineVersion() string {
	if o.EngineVersion == "" {
		return EngineVersion
	}
	return o.EngineVersion
}

func (o LoadOptions) attributeSpecs() vocabulary.AttributeSpecSet {
	if o.AttributeSpecs == nil {
		return vocabulary.DefaultAttributeSpecs()
	}
	return *o.AttributeSpecs
}

// LoadPackage reads and validates a world package rooted at fsys.
//
// It fails on the FIRST violation rather than accumulating a report, because
// import is all-or-nothing: nothing is materialized until everything validates,
// so the author fixes one thing and re-runs a cheap, purely local check.
func LoadPackage(fsys fs.FS, opts LoadOptions) (*Package, error) {
	manifestData, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFile, err)
	}
	manifest, err := ParseManifest(manifestData, opts.engineVersion())
	if err != nil {
		return nil, err
	}

	entityData, err := fs.ReadFile(fsys, EntitiesFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", EntitiesFile, err)
	}
	entities, err := ParseEntities(bytes.NewReader(entityData), opts.attributeSpecs())
	if err != nil {
		return nil, err
	}

	ruleFiles, err := loadJSONDir(fsys, RulesDir, optionalDir, checkRuleFile)
	if err != nil {
		return nil, err
	}
	personaFiles, err := loadJSONDir(fsys, PersonasDir, requiredDir, checkPersonaRecord)
	if err != nil {
		return nil, err
	}

	return &Package{
		Manifest:     manifest,
		Entities:     entities,
		RuleFiles:    ruleFiles,
		PersonaFiles: personaFiles,
	}, nil
}

// dirRequirement says whether a package member directory has to be there.
type dirRequirement bool

const (
	// requiredDir means an absent or empty directory fails the load.
	requiredDir dirRequirement = true
	// optionalDir means an absent or empty directory yields no files.
	//
	// The rules directory is optional, and the reason is worth stating: a world
	// that reacts to nothing is a legitimate world — a scene, some characters,
	// and whatever the turn loop makes of them — and the "at least one rule
	// file" requirement is what put a placeholder rule in the starter package in
	// the first place. It cannot be satisfied by an empty directory either:
	// neither git nor embed.FS carries one, so "the directory must exist" would
	// mean "ship a file", which is the requirement being removed.
	//
	// Personas stay required. A package with no adjudicator and no narrator
	// cannot resolve a turn at all, and that is a broken world rather than a
	// quiet one.
	optionalDir dirRequirement = false
)

// loadJSONDir reads a package member directory of .json files, running check
// over each. A required directory must exist and hold at least one file.
func loadJSONDir(
	fsys fs.FS, dir string, requirement dirRequirement, check func(data []byte) error,
) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if requirement == optionalDir && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s/: %w", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".json" {
			continue
		}
		name := path.Join(dir, entry.Name())
		data, readErr := fs.ReadFile(fsys, name)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", name, readErr)
		}
		if checkErr := check(data); checkErr != nil {
			return nil, fmt.Errorf("%s: %w", name, checkErr)
		}
		files = append(files, name)
	}
	if len(files) == 0 && requirement == requiredDir {
		return nil, fmt.Errorf("%s/: %w", dir, errors.New("holds no .json files"))
	}
	sort.Strings(files)
	return files, nil
}

// checkRuleFile is the rule pack's gate: JSON syntax, PREDICATE syntax on every
// condition field, and the world-rule SCOPE boundary (see rulescope.go — a
// world may author reactions, never the engine's turn loop).
//
// It deliberately does NOT run rule.ValidateDefinition. That validator's
// condition check is two checks in one — vocabulary.RequireDeclaredPredicate is
// ParsePredicate followed by a registry lookup — and only the second half is
// premature. SemMachina's predicates are not in semstreams' process-global
// registry yet; registering them is the rule pack's own wiring (task group 8),
// and it is a decision with teeth, because the same registry carries the
// RuleOpaque flag that would stop a rule branching on narration. Running the
// declaration check here would reject our own rule pack for a reason that has
// nothing to do with the world.
//
// The SYNTAX half is safe today and is exactly what import owes an author. A
// condition field is a predicate, so it faces the three-segment lower-kebab
// alphabet an entity-ID segment does not (F9): `item.condition.rust_level` is
// legal-looking, imports cleanly without this check, and then fails when the
// rule processor loads the pack — with an error naming a rule id rather than a
// file. Upstream escapes non-predicate message/state fields with a `$` prefix
// (processor/rule/config_validation.go validateConditionFields); so does this.
func checkRuleFile(data []byte) error {
	if !json.Valid(data) {
		return errors.New("is not well-formed JSON")
	}

	definitions, err := decodeRuleDefinitions(data)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		for index, condition := range definition.Conditions {
			if condition.Field == "" || strings.HasPrefix(condition.Field, "$") {
				continue
			}
			if _, err := ssvocab.ParsePredicate(condition.Field); err != nil {
				return fmt.Errorf(
					"rule %q condition[%d] field %q is not a canonical three-segment lower-kebab "+
						"predicate (entity ids may carry underscores and uppercase, predicates may not): %w",
					definition.ID, index, condition.Field, err)
			}
		}
		// Alphabet first, then scope: a misspelled predicate is a typo the
		// author fixes, and a reserved one is a boundary they have to be told
		// about — reporting the typo as a boundary violation would send them
		// looking for the wrong thing.
		if err := checkRuleScope(definition); err != nil {
			return err
		}
	}
	return nil
}

// decodeRuleDefinitions decodes a rule file into the production Definition type,
// accepting an array or a single object exactly as the rule loader does
// (processor/rule/rule_loader.go loadRuleDefinitionsFromFiles). Decoding into
// the real type rather than a local mirror is what keeps the field names from
// drifting; a gate that read a `conditions` array the loader does not would
// check nothing.
func decodeRuleDefinitions(data []byte) ([]rule.Definition, error) {
	var definitions []rule.Definition
	if err := json.Unmarshal(data, &definitions); err == nil {
		return definitions, nil
	}

	var single rule.Definition
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, fmt.Errorf("is neither a rule nor an array of rules: %w", err)
	}
	return []rule.Definition{single}, nil
}

// checkPersonaRecord decodes a persona file as the upstream persona.Persona it
// will be loaded as, and applies that type's own validation.
//
// Driving the production type rather than a local mirror is the point: a
// persona file that loads here loads at boot. Unknown fields are rejected so an
// author who writes `model:` or `max_iterations:` is told immediately that
// those are deployment configuration on the agentic-loop component, not world
// content — a package that could choose its own model slot could choose its own
// spend.
func checkPersonaRecord(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var record persona.Persona
	if err := decoder.Decode(&record); err != nil {
		return err
	}
	return record.Validate()
}
