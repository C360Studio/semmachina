package world_test

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/c360studio/semmachina/internal/world"
)

// The engine version a manifest is checked against must be the version this
// binary actually links. debug.ReadBuildInfo cannot supply it: a `go test`
// binary records no module dependencies (verified — bi.Deps is empty), so a
// build-info lookup would be dead code in every test that mattered.
//
// The constant is therefore the source, and THIS test is what keeps it
// honest: it reads the authoritative require directive out of go.mod. Bumping
// the pin without updating the constant fails here, which is exactly the
// moment someone should be re-reading every world manifest's engine_compat.
func TestEngineVersion_MatchesTheGoModRequireDirective(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("parse go.mod: %v", err)
	}
	required := ""
	for _, req := range parsed.Require {
		if req.Mod.Path == world.EngineModulePath {
			required = req.Mod.Version
			break
		}
	}
	if required == "" {
		t.Fatalf("go.mod has no require directive for %s", world.EngineModulePath)
	}

	if got, want := world.EngineVersion, required; got != want {
		t.Fatalf("world.EngineVersion is %q but go.mod requires %q; "+
			"re-check every world manifest's engine_compat before updating the constant", got, want)
	}
	if !semver.IsValid(world.EngineVersion) {
		t.Fatalf("world.EngineVersion %q is not valid semver", world.EngineVersion)
	}
}

func TestParseConstraint_AcceptsTheDocumentedGrammar(t *testing.T) {
	for _, expr := range []string{
		">=v1.0.0-beta.150",
		">=v1.0.0-beta.150 <v2.0.0",
		"=v1.0.0-beta.158",
		">v0.9.0 <=v1.0.0-beta.158",
		">=1.0.0-beta.150 <2.0.0", // the leading v is optional
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := world.ParseConstraint(expr); err != nil {
				t.Fatalf("ParseConstraint(%q): %v", expr, err)
			}
		})
	}
}

func TestParseConstraint_RejectsMalformedExpressions(t *testing.T) {
	rejected := map[string]string{
		"empty":                     "",
		"blank":                     "   ",
		"bare version, no operator": "v1.0.0",
		"unknown operator":          "~>v1.0.0",
		"caret range":               "^v1.0.0",
		"operator with no version":  ">=",
		"not a version":             ">=banana",
		"double operator":           ">=>v1.0.0",
		"partial version":           ">=v1.0",
	}
	for name, expr := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := world.ParseConstraint(expr); err == nil {
				t.Fatalf("ParseConstraint(%q) accepted a malformed constraint", expr)
			}
		})
	}
}

// Prerelease handling is the whole reason this grammar is explicit rather than
// a range shorthand. The engine ships as v1.0.0-beta.N, and by semver
// precedence every v1.0.0-beta.N sorts BELOW v1.0.0 — so `>=v1.0.0` excludes
// the running engine. A world author has to be able to see that in the
// constraint they wrote.
func TestConstraint_AdmitsAndExcludesUnderSemverPrecedence(t *testing.T) {
	cases := []struct {
		expr    string
		version string
		admits  bool
	}{
		{">=v1.0.0-beta.150", "v1.0.0-beta.158", true},
		{">=v1.0.0-beta.150", "v1.0.0-beta.149", false},
		{">=v1.0.0-beta.150", "v1.0.0-beta.1500", true},
		{">=v1.0.0-beta.150 <v2.0.0", "v1.0.0-beta.158", true},
		{">=v1.0.0-beta.150 <v2.0.0", "v2.0.0", false},
		{">=v1.0.0-beta.150 <v2.0.0", "v1.4.2", true},
		{">=v1.0.0", "v1.0.0-beta.158", false},
		{"=v1.0.0-beta.158", "v1.0.0-beta.158", true},
		{"=v1.0.0-beta.158", "v1.0.0-beta.159", false},
		{"<=v1.0.0-beta.158", "v1.0.0-beta.158", true},
		{">v1.0.0-beta.158", "v1.0.0-beta.158", false},
	}

	for _, tc := range cases {
		t.Run(tc.expr+" vs "+tc.version, func(t *testing.T) {
			constraint, err := world.ParseConstraint(tc.expr)
			if err != nil {
				t.Fatalf("ParseConstraint: %v", err)
			}
			if got := constraint.Admits(tc.version); got != tc.admits {
				t.Fatalf("Admits(%q) = %v, want %v", tc.version, got, tc.admits)
			}
		})
	}
}

func TestConstraint_StringRoundTrips(t *testing.T) {
	constraint, err := world.ParseConstraint(">=1.0.0-beta.150 <2.0.0")
	if err != nil {
		t.Fatalf("ParseConstraint: %v", err)
	}
	// Normalized: the optional leading v is materialized so the recorded form
	// is the one comparisons actually ran against.
	if got, want := constraint.String(), ">=v1.0.0-beta.150 <v2.0.0"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestConstraint_AdmitsRejectsAnInvalidVersion(t *testing.T) {
	constraint, err := world.ParseConstraint(">=v1.0.0-beta.150")
	if err != nil {
		t.Fatalf("ParseConstraint: %v", err)
	}
	if constraint.Admits("not-a-version") {
		t.Fatal("Admits accepted a value that is not a semver version")
	}
}

// The rejection reason has to name the constraint AND the version it excluded,
// or an operator staring at a failed boot has to go read the manifest to find
// out what happened.
func TestCheckEngineCompat_NamesTheConstraintAndTheVersion(t *testing.T) {
	err := world.CheckEngineCompat(">=v9.0.0", "v1.0.0-beta.158")
	if err == nil {
		t.Fatal("CheckEngineCompat admitted an engine the constraint excludes")
	}
	for _, want := range []string{">=v9.0.0", "v1.0.0-beta.158"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection reason %q omits %q", err.Error(), want)
		}
	}
}
