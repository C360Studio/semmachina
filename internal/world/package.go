package world

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/c360studio/semstreams/persona"

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
// every entity line, and the presence of its rule and persona directories all
// checked out. Downstream code therefore never has to ask whether a package is
// valid, only what it says.
type Package struct {
	// Manifest is the validated manifest v0.
	Manifest Manifest
	// Entities are the template entities in file order.
	Entities []TemplateEntity
	// RuleFiles and PersonaFiles are the package-relative paths of the rule
	// pack and persona configurations, sorted. This slice is loaded but not
	// interpreted here: rule semantics belong to the rule processor, and
	// interpreting them at world-load would put a second rule parser in the
	// tree.
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

	ruleFiles, err := loadJSONDir(fsys, RulesDir, checkWellFormedJSON)
	if err != nil {
		return nil, err
	}
	personaFiles, err := loadJSONDir(fsys, PersonasDir, checkPersonaRecord)
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

// loadJSONDir requires the directory to exist and to hold at least one .json
// file that passes the supplied check.
func loadJSONDir(fsys fs.FS, dir string, check func(data []byte) error) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
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
	if len(files) == 0 {
		return nil, fmt.Errorf("%s/: %w", dir, errors.New("holds no .json files"))
	}
	sort.Strings(files)
	return files, nil
}

// checkWellFormedJSON is the rule pack's gate: syntax only.
//
// It deliberately does NOT run rule.ValidateDefinition. That validator requires
// every condition field to be DECLARED in semstreams' process-global predicate
// registry (vocabulary.RequireDeclaredPredicate), and SemMachina's predicates
// are not registered there yet — registering them is the rule pack's own wiring
// (task group 8), and it is a decision with teeth, because the same registry
// carries the RuleOpaque flag that would stop a rule branching on narration.
// Running a validator here that is guaranteed to reject our own rule pack would
// make world loading fail for a reason that has nothing to do with the world.
func checkWellFormedJSON(data []byte) error {
	if !json.Valid(data) {
		return errors.New("is not well-formed JSON")
	}
	return nil
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
