package world

import (
	"bytes"
	"errors"
	"fmt"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ManifestFile is the manifest's fixed name inside a world package.
const ManifestFile = "manifest.yaml"

// Manifest is manifest v0: exactly five fields.
//
// The field set is closed on purpose. A world package is the product boundary
// between an author and the engine, and every field the engine reads is a
// promise it has to keep across versions; a manifest that silently tolerates
// unknown keys is one where an author's `bounds:` block does nothing and
// nobody finds out until play feels wrong.
type Manifest struct {
	// ID is the template identity. It becomes a POSITION of every entity ID
	// the package materializes, so it is bound by the entity-ID alphabet.
	ID string `yaml:"id"`
	// Name is the human title.
	Name string `yaml:"name"`
	// Version is the template's own semver. A template is an immutable
	// versioned product; the version is what makes "the same package" a
	// checkable claim rather than a hope.
	Version string `yaml:"version"`
	// EngineCompat is a Constraint expression over the semstreams version.
	EngineCompat string `yaml:"engine_compat"`
	// Description is the author's summary.
	Description string `yaml:"description"`
}

// FieldError reports a manifest field that violates the v0 contract. The field
// name is carried separately from the reason so an operator reading a failed
// import sees WHICH field failed without parsing prose.
type FieldError struct {
	File  string
	Field string
	Err   error
}

// Error implements error.
func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: field %q: %v", e.File, e.Field, e.Err)
}

// Unwrap exposes the underlying reason.
func (e *FieldError) Unwrap() error { return e.Err }

func manifestFieldErr(field string, err error) error {
	return &FieldError{File: ManifestFile, Field: field, Err: err}
}

// ParseManifest parses and fully validates manifest v0 against a running
// engine version.
//
// Compatibility is checked HERE, before any entity is read, because the spec's
// contract is that an incompatible package fails before anything is
// materialized. A partially imported world is worse than a rejected one: it is
// a campaign that half-exists.
func ParseManifest(data []byte, engineVersion string) (Manifest, error) {
	if engineVersion == "" {
		return Manifest{}, errors.New(
			"engine version is required: engine_compat cannot be checked against an unknown engine")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}

	if err := manifest.validate(engineVersion); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) validate(engineVersion string) error {
	for field, value := range map[string]string{
		"id":            m.ID,
		"name":          m.Name,
		"version":       m.Version,
		"engine_compat": m.EngineCompat,
		"description":   m.Description,
	} {
		if value == "" {
			return manifestFieldErr(field, errors.New("is required"))
		}
	}

	if err := vocabulary.ValidateIDSegment(m.ID); err != nil {
		return manifestFieldErr("id", err)
	}

	// Template versions are authored without the Go "v" prefix (0.1.0 reads
	// naturally in a manifest), so normalize before validating; the engine
	// version, which comes from a Go module path, already carries it.
	//
	// Canonical form is required, not merely valid form: semver.IsValid accepts
	// "0.1", and a package published as 0.1 while another is published as 0.1.0
	// is two names for one version — exactly the ambiguity an immutable
	// versioned product cannot afford.
	version := "v" + m.Version
	if !semver.IsValid(version) || semver.Canonical(version) != version {
		return manifestFieldErr("version",
			fmt.Errorf("%q is not a canonical semver major.minor.patch version", m.Version))
	}

	if err := CheckEngineCompat(m.EngineCompat, engineVersion); err != nil {
		return manifestFieldErr("engine_compat", err)
	}
	return nil
}
