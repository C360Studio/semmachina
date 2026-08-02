package world

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	sspersona "github.com/c360studio/semstreams/persona"
	"gopkg.in/yaml.v3"

	enginepersona "github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// PacksFile is the optional, versioned experience catalog.
const PacksFile = "packs.yaml"

// PackCatalog is the validated set of package-authored experience choices.
// File lists are package-relative, sorted, and safe to copy into a Plan.
type PackCatalog struct {
	Version              int
	DefaultPersonaPack   string
	DefaultMechanicsPack string
	PersonaPacks         map[string][]string
	MechanicsPacks       map[string][]string
	personaCoverage      map[string]personaRoleCoverage
}

type personaRoleCoverage uint8

const (
	coversAdjudicator personaRoleCoverage = 1 << iota
	coversNarrator
	requiredPersonaCoverage = coversAdjudicator | coversNarrator
)

type catalogWire struct {
	Version        int                 `yaml:"version"`
	Defaults       catalogDefaultsWire `yaml:"defaults"`
	PersonaPacks   map[string]packWire `yaml:"persona_packs"`
	MechanicsPacks map[string]packWire `yaml:"mechanics_packs"`
}

type catalogDefaultsWire struct {
	PersonaPack   string `yaml:"persona_pack"`
	MechanicsPack string `yaml:"mechanics_pack"`
}

type packWire struct {
	Files []string `yaml:"files"`
}

func loadCatalog(
	fsys fs.FS, legacyPersonaFiles []string,
) (PackCatalog, bool, error) {
	data, err := fs.ReadFile(fsys, PacksFile)
	if errors.Is(err, fs.ErrNotExist) {
		catalog := implicitCatalog(legacyPersonaFiles)
		coverage, coverageErr := personaCoverageForFiles(fsys, legacyPersonaFiles)
		if coverageErr != nil {
			return PackCatalog{}, false, coverageErr
		}
		catalog.personaCoverage = map[string]personaRoleCoverage{"default": coverage}
		return catalog, false, nil
	}
	if err != nil {
		return PackCatalog{}, false, fmt.Errorf("read %s: %w", PacksFile, err)
	}

	catalog, err := parseCatalog(fsys, data)
	if err != nil {
		return PackCatalog{}, true, err
	}
	return catalog, true, nil
}

func implicitCatalog(personaFiles []string) PackCatalog {
	personas := cloneStrings(personaFiles)
	sort.Strings(personas)
	return PackCatalog{
		Version:              1,
		DefaultPersonaPack:   "default",
		DefaultMechanicsPack: "default",
		PersonaPacks:         map[string][]string{"default": personas},
		MechanicsPacks:       map[string][]string{"default": {}},
	}
}

func parseCatalog(fsys fs.FS, data []byte) (PackCatalog, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var wire catalogWire
	if err := decoder.Decode(&wire); err != nil {
		return PackCatalog{}, fmt.Errorf("%s: %w", PacksFile, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("contains more than one YAML document")
		}
		return PackCatalog{}, fmt.Errorf("%s: %w", PacksFile, err)
	}
	if wire.Version != 1 {
		return PackCatalog{}, catalogError("version", fmt.Errorf("got %d, want 1", wire.Version))
	}
	if wire.Defaults.PersonaPack == "" {
		return PackCatalog{}, catalogError("defaults.persona_pack", errors.New("is required"))
	}
	if wire.Defaults.MechanicsPack == "" {
		return PackCatalog{}, catalogError("defaults.mechanics_pack", errors.New("is required"))
	}
	if wire.PersonaPacks == nil {
		return PackCatalog{}, catalogError("persona_packs", errors.New("is required"))
	}
	if wire.MechanicsPacks == nil {
		return PackCatalog{}, catalogError("mechanics_packs", errors.New("is required"))
	}

	personas, err := validatePacks(fsys, "persona_packs", PersonasDir, wire.PersonaPacks, false, checkPersonaRecord)
	if err != nil {
		return PackCatalog{}, err
	}
	mechanics, err := validatePacks(fsys, "mechanics_packs", RulesDir, wire.MechanicsPacks, true, checkRuleFile)
	if err != nil {
		return PackCatalog{}, err
	}
	if _, ok := personas[wire.Defaults.PersonaPack]; !ok {
		return PackCatalog{}, catalogError("defaults.persona_pack",
			fmt.Errorf("%q names no persona pack", wire.Defaults.PersonaPack))
	}
	if _, ok := mechanics[wire.Defaults.MechanicsPack]; !ok {
		return PackCatalog{}, catalogError("defaults.mechanics_pack",
			fmt.Errorf("%q names no mechanics pack", wire.Defaults.MechanicsPack))
	}
	personaCoverage := make(map[string]personaRoleCoverage, len(personas))
	for name, files := range personas {
		coverage, err := personaCoverageForFiles(fsys, files)
		if err != nil {
			return PackCatalog{}, err
		}
		personaCoverage[name] = coverage
	}

	return PackCatalog{
		Version:              1,
		DefaultPersonaPack:   wire.Defaults.PersonaPack,
		DefaultMechanicsPack: wire.Defaults.MechanicsPack,
		PersonaPacks:         personas,
		MechanicsPacks:       mechanics,
		personaCoverage:      personaCoverage,
	}, nil
}

func validatePacks(
	fsys fs.FS,
	field, dir string,
	packs map[string]packWire,
	emptyAllowed bool,
	check func([]byte) error,
) (map[string][]string, error) {
	out := make(map[string][]string, len(packs))
	names := make([]string, 0, len(packs))
	for name := range packs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pack := packs[name]
		if err := vocabulary.ValidateIDSegment(name); err != nil {
			return nil, catalogError(field, fmt.Errorf("pack name %q: %w", name, err))
		}
		if pack.Files == nil {
			return nil, catalogError(field+"."+name+".files", errors.New("is required"))
		}
		if len(pack.Files) == 0 && !emptyAllowed {
			return nil, catalogError(field+"."+name+".files", errors.New("persona pack is empty"))
		}

		seen := make(map[string]bool, len(pack.Files))
		files := make([]string, 0, len(pack.Files))
		for _, file := range pack.Files {
			if err := validatePackPath(file, dir); err != nil {
				return nil, catalogError(field+"."+name+".files", err)
			}
			if seen[file] {
				return nil, catalogError(field+"."+name+".files",
					fmt.Errorf("duplicate file %q", file))
			}
			seen[file] = true
			data, err := fs.ReadFile(fsys, file)
			if err != nil {
				return nil, catalogError(field+"."+name+".files", fmt.Errorf("read %q: %w", file, err))
			}
			if err := check(data); err != nil {
				return nil, catalogError(field+"."+name+".files", fmt.Errorf("%s: %w", file, err))
			}
			files = append(files, file)
		}
		sort.Strings(files)
		out[name] = files
	}
	return out, nil
}

func validatePackPath(file, dir string) error {
	if file == "" {
		return errors.New("file path is empty")
	}
	if !fs.ValidPath(file) || path.Clean(file) != file {
		return fmt.Errorf("file path %q is not a clean package-relative path", file)
	}
	if !strings.HasPrefix(file, dir+"/") {
		return fmt.Errorf("file path %q must remain below %s/", file, dir)
	}
	if path.Ext(file) != ".json" {
		return fmt.Errorf("file path %q must have the .json extension", file)
	}
	return nil
}

func catalogError(field string, err error) error {
	return &FieldError{File: PacksFile, Field: field, Err: err}
}

func catalogFiles(packs map[string][]string) []string {
	seen := make(map[string]bool)
	for _, files := range packs {
		for _, file := range files {
			seen[file] = true
		}
	}
	files := make([]string, 0, len(seen))
	for file := range seen {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append(make([]string, 0, len(in)), in...)
}

func clonePackCatalog(in PackCatalog) PackCatalog {
	out := PackCatalog{
		Version:              in.Version,
		DefaultPersonaPack:   in.DefaultPersonaPack,
		DefaultMechanicsPack: in.DefaultMechanicsPack,
	}
	if in.PersonaPacks != nil {
		out.PersonaPacks = make(map[string][]string, len(in.PersonaPacks))
		for name, files := range in.PersonaPacks {
			out.PersonaPacks[name] = cloneStrings(files)
		}
	}
	if in.MechanicsPacks != nil {
		out.MechanicsPacks = make(map[string][]string, len(in.MechanicsPacks))
		for name, files := range in.MechanicsPacks {
			out.MechanicsPacks[name] = cloneStrings(files)
		}
	}
	if in.personaCoverage != nil {
		out.personaCoverage = make(map[string]personaRoleCoverage, len(in.personaCoverage))
		for name, coverage := range in.personaCoverage {
			out.personaCoverage[name] = coverage
		}
	}
	return out
}

func isZeroCatalog(catalog PackCatalog) bool {
	return catalog.Version == 0 &&
		catalog.DefaultPersonaPack == "" &&
		catalog.DefaultMechanicsPack == "" &&
		catalog.PersonaPacks == nil &&
		catalog.MechanicsPacks == nil &&
		catalog.personaCoverage == nil
}

func personaCoverageForFiles(fsys fs.FS, files []string) (personaRoleCoverage, error) {
	var coverage personaRoleCoverage
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return 0, fmt.Errorf("%s: read persona roles from %s: %w", PacksFile, file, err)
		}
		record, err := DecodePersonaRecord(data)
		if err != nil {
			return 0, fmt.Errorf("%s: decode persona roles from %s: %w", PacksFile, file, err)
		}
		coverage |= personaCoverageForRecords([]sspersona.Persona{*record})
	}
	return coverage, nil
}

func personaCoverageForRecords(records []sspersona.Persona) personaRoleCoverage {
	var coverage personaRoleCoverage
	for _, record := range records {
		for _, role := range record.Roles {
			switch enginepersona.Role(role) {
			case enginepersona.RoleAdjudicator:
				coverage |= coversAdjudicator
			case enginepersona.RoleNarrator:
				coverage |= coversNarrator
			}
		}
	}
	return coverage
}

// ValidatePersonaRoleCoverage rechecks that construction-bound persona values
// collectively serve both engine-required roles.
func ValidatePersonaRoleCoverage(pack string, records []sspersona.Persona) error {
	return validatePersonaCoverage(pack, personaCoverageForRecords(records))
}

func validatePersonaCoverage(pack string, coverage personaRoleCoverage) error {
	var missing []string
	if coverage&coversAdjudicator == 0 {
		missing = append(missing, string(enginepersona.RoleAdjudicator))
	}
	if coverage&coversNarrator == 0 {
		missing = append(missing, string(enginepersona.RoleNarrator))
	}
	if len(missing) != 0 {
		return fmt.Errorf(
			"instance experience persona pack %q is missing required role(s): %s",
			pack, strings.Join(missing, ", "))
	}
	return nil
}

func (p *Package) resolveExperience(selection ExperienceSelection) (ResolvedExperience, error) {
	var catalog PackCatalog
	switch {
	case p.validatedCatalog != nil:
		catalog = *p.validatedCatalog
	case isZeroCatalog(p.Catalog):
		// Package values constructed directly by tests and trusted callers retain
		// the same legacy fallback as packages loaded without packs.yaml.
		catalog = implicitCatalog(p.PersonaFiles)
	default:
		return ResolvedExperience{}, errors.New(
			"package experience catalog was not validated by LoadPackage")
	}

	personaPack := selection.PersonaPack
	if personaPack == "" {
		personaPack = catalog.DefaultPersonaPack
	}
	mechanicsPack := selection.MechanicsPack
	if mechanicsPack == "" {
		mechanicsPack = catalog.DefaultMechanicsPack
	}

	personaFiles, ok := catalog.PersonaPacks[personaPack]
	if !ok {
		return ResolvedExperience{}, fmt.Errorf("instance experience persona pack %q is not declared by %s",
			personaPack, PacksFile)
	}
	if coverage, known := catalog.personaCoverage[personaPack]; known {
		if err := validatePersonaCoverage(personaPack, coverage); err != nil {
			return ResolvedExperience{}, err
		}
	}
	mechanicsFiles, ok := catalog.MechanicsPacks[mechanicsPack]
	if !ok {
		return ResolvedExperience{}, fmt.Errorf("instance experience mechanics pack %q is not declared by %s",
			mechanicsPack, PacksFile)
	}

	personas := cloneStrings(personaFiles)
	mechanics := cloneStrings(mechanicsFiles)
	sort.Strings(personas)
	sort.Strings(mechanics)
	return ResolvedExperience{
		PersonaPack:    personaPack,
		MechanicsPack:  mechanicsPack,
		PersonaFiles:   personas,
		MechanicsFiles: mechanics,
	}, nil
}
