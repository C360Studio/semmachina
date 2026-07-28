package world_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/world"
)

const goodManifest = `id: starter
name: The Gatehouse at Dusk
version: 0.1.0
engine_compat: ">=v1.0.0-beta.150 <v2.0.0"
description: One scene, one player character, and a gate that will not open quietly.
`

func TestParseManifest_AcceptsTheV0Shape(t *testing.T) {
	manifest, err := world.ParseManifest([]byte(goodManifest), world.EngineVersion)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.ID != "starter" {
		t.Fatalf("id = %q", manifest.ID)
	}
	if manifest.Name != "The Gatehouse at Dusk" {
		t.Fatalf("name = %q", manifest.Name)
	}
	if manifest.Version != "0.1.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if manifest.EngineCompat != ">=v1.0.0-beta.150 <v2.0.0" {
		t.Fatalf("engine_compat = %q", manifest.EngineCompat)
	}
	if manifest.Description == "" {
		t.Fatal("description is empty")
	}
}

// v0 is "these five fields, nothing else". A manifest carrying a sixth field
// is an author expecting behavior the loader will not deliver, so it fails
// rather than being silently ignored — the spec's "nothing else" only means
// something if an extra field is an error.
func TestParseManifest_RejectsUnknownFields(t *testing.T) {
	extra := goodManifest + "author: someone\n"
	_, err := world.ParseManifest([]byte(extra), world.EngineVersion)
	if err == nil {
		t.Fatal("ParseManifest accepted an unknown manifest field")
	}
	if !strings.Contains(err.Error(), "author") {
		t.Fatalf("rejection reason %q does not name the unknown field", err)
	}
}

func TestParseManifest_RejectsAMissingRequiredField(t *testing.T) {
	required := []string{"id", "name", "version", "engine_compat", "description"}
	for _, field := range required {
		t.Run(field, func(t *testing.T) {
			var kept []string
			for _, line := range strings.Split(strings.TrimSuffix(goodManifest, "\n"), "\n") {
				if !strings.HasPrefix(line, field+":") {
					kept = append(kept, line)
				}
			}
			partial := strings.Join(kept, "\n") + "\n"
			if strings.Count(partial, "\n") != 4 {
				t.Fatalf("test setup removed the wrong number of lines:\n%s", partial)
			}

			_, err := world.ParseManifest([]byte(partial), world.EngineVersion)
			if err == nil {
				t.Fatalf("ParseManifest accepted a manifest with no %s", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("rejection reason %q does not name the missing field %q", err, field)
			}
		})
	}
}

// The manifest id becomes a position of every entity ID the package produces,
// so it faces the entity-ID alphabet, not the predicate alphabet.
func TestParseManifest_RejectsAnIDThatCannotBeAnEntityIDSegment(t *testing.T) {
	for name, id := range map[string]string{
		"dotted":         "starter.world",
		"leading hyphen": "-starter",
		"space":          "starter world",
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(goodManifest, "id: starter", "id: "+quoted(id), 1)
			_, err := world.ParseManifest([]byte(bad), world.EngineVersion)
			if err == nil {
				t.Fatalf("ParseManifest accepted id %q", id)
			}
			if !strings.Contains(err.Error(), "id") {
				t.Fatalf("rejection reason %q does not name the id field", err)
			}
		})
	}
}

func TestParseManifest_RejectsAVersionThatIsNotCanonicalSemver(t *testing.T) {
	for name, version := range map[string]string{
		"not a version":       "one",
		"partial":             "0.1",
		"go-style v prefix":   "v0.1.0",
		"four parts":          "0.1.0.1",
		"leading whitespace":  " 0.1.0",
		"build metadata only": "0.1.0+build.7",
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(goodManifest, "version: 0.1.0", "version: "+quoted(version), 1)
			_, err := world.ParseManifest([]byte(bad), world.EngineVersion)
			if err == nil {
				t.Fatalf("ParseManifest accepted template version %q", version)
			}
			if !strings.Contains(err.Error(), "version") {
				t.Fatalf("rejection reason %q does not name the version field", err)
			}
		})
	}
}

func TestParseManifest_AcceptsAPrereleaseTemplateVersion(t *testing.T) {
	ok := strings.Replace(goodManifest, "version: 0.1.0", `version: "0.2.0-draft.1"`, 1)
	if _, err := world.ParseManifest([]byte(ok), world.EngineVersion); err != nil {
		t.Fatalf("ParseManifest rejected a prerelease template version: %v", err)
	}
}

// The spec's "Manifest validation" scenario: a constraint that excludes the
// running engine fails the import, with a reason naming the violation.
func TestParseManifest_RejectsAConstraintThatExcludesTheRunningEngine(t *testing.T) {
	bad := strings.Replace(goodManifest,
		`engine_compat: ">=v1.0.0-beta.150 <v2.0.0"`,
		`engine_compat: ">=v9.9.9"`, 1)

	_, err := world.ParseManifest([]byte(bad), world.EngineVersion)
	if err == nil {
		t.Fatal("ParseManifest accepted a manifest whose engine_compat excludes this engine")
	}
	for _, want := range []string{"engine_compat", ">=v9.9.9", world.EngineVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection reason %q omits %q", err, want)
		}
	}
}

func TestParseManifest_RejectsAMalformedConstraint(t *testing.T) {
	bad := strings.Replace(goodManifest,
		`engine_compat: ">=v1.0.0-beta.150 <v2.0.0"`,
		`engine_compat: "^v1.0.0"`, 1)

	_, err := world.ParseManifest([]byte(bad), world.EngineVersion)
	if err == nil {
		t.Fatal("ParseManifest accepted a caret range, which the grammar does not define")
	}
	if !strings.Contains(err.Error(), "engine_compat") {
		t.Fatalf("rejection reason %q does not name the engine_compat field", err)
	}
}

func TestParseManifest_RejectsAnEmptyEngineVersion(t *testing.T) {
	if _, err := world.ParseManifest([]byte(goodManifest), ""); err == nil {
		t.Fatal("ParseManifest checked compatibility against an unknown engine version")
	}
}

func TestParseManifest_RejectsMalformedYAML(t *testing.T) {
	if _, err := world.ParseManifest([]byte("id: [unterminated\n"), world.EngineVersion); err == nil {
		t.Fatal("ParseManifest accepted malformed YAML")
	}
}

func quoted(s string) string { return `"` + s + `"` }
