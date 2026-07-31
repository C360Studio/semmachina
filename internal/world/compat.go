package world

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// EngineModulePath is the substrate module a world manifest declares
// compatibility against.
const EngineModulePath = "github.com/c360studio/semstreams"

// EngineVersion is the semstreams version this build links.
//
// It is a constant rather than a runtime lookup because runtime/debug's build
// info records NO module dependencies in a `go test` binary, so a build-info
// path would be untestable exactly where world loading is tested. The
// constant's honesty is enforced instead by a test that reads go.mod's require
// directive — bumping the pin without touching this line fails the suite,
// which is the right moment to re-read every world's engine_compat.
const EngineVersion = "v1.0.0-beta.159"

// Operator is one comparison in an engine-compatibility constraint.
type Operator string

// The closed operator set. Range shorthands (`^`, `~`) are deliberately
// absent: their interaction with prereleases is where compatibility checks
// quietly stop meaning what the author thought, and the engine ships as a
// prerelease.
const (
	// OpGreaterOrEqual admits versions at or above the bound.
	OpGreaterOrEqual Operator = ">="
	// OpLessOrEqual admits versions at or below the bound.
	OpLessOrEqual Operator = "<="
	// OpGreater admits versions strictly above the bound.
	OpGreater Operator = ">"
	// OpLess admits versions strictly below the bound.
	OpLess Operator = "<"
	// OpEqual admits exactly one version.
	OpEqual Operator = "="
)

// operators are matched longest-first so ">=" is never read as ">" followed by
// a version beginning "=".
var operators = []Operator{OpGreaterOrEqual, OpLessOrEqual, OpGreater, OpLess, OpEqual}

// Comparison is one operator applied to one bound version.
type Comparison struct {
	Op      Operator
	Version string // normalized with a leading "v"
}

// String returns the canonical text of the comparison.
func (c Comparison) String() string { return string(c.Op) + c.Version }

// admits reports whether version satisfies this comparison under semver
// precedence.
func (c Comparison) admits(version string) bool {
	cmp := semver.Compare(version, c.Version)
	switch c.Op {
	case OpGreaterOrEqual:
		return cmp >= 0
	case OpLessOrEqual:
		return cmp <= 0
	case OpGreater:
		return cmp > 0
	case OpLess:
		return cmp < 0
	case OpEqual:
		return cmp == 0
	default:
		// Unreachable: ParseConstraint admits only the operators above.
		return false
	}
}

// Constraint is a conjunction of comparisons — a version satisfies it only if
// every comparison admits it.
//
// The grammar is deliberately small and total: whitespace-separated
// comparisons, each an operator immediately followed by a semver version whose
// leading "v" is optional. Everything about it is decided by semver
// PRECEDENCE, including prereleases, which is the part that surprises people:
// v1.0.0-beta.158 sorts BELOW v1.0.0, so ">=v1.0.0" excludes the engine this
// repo currently pins. That is correct semver and it is why the constraint is
// spelled out comparison by comparison rather than hidden behind a caret.
type Constraint struct {
	comparisons []Comparison
}

// ParseConstraint parses an engine-compatibility expression.
func ParseConstraint(expr string) (Constraint, error) {
	fields := strings.Fields(expr)
	if len(fields) == 0 {
		return Constraint{}, fmt.Errorf("constraint is empty")
	}

	comparisons := make([]Comparison, 0, len(fields))
	for _, field := range fields {
		comparison, err := parseComparison(field)
		if err != nil {
			return Constraint{}, err
		}
		comparisons = append(comparisons, comparison)
	}
	return Constraint{comparisons: comparisons}, nil
}

func parseComparison(field string) (Comparison, error) {
	for _, op := range operators {
		rest, found := strings.CutPrefix(field, string(op))
		if !found {
			continue
		}
		version := rest
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !semver.IsValid(version) {
			return Comparison{}, fmt.Errorf(
				"comparison %q does not name a valid semver version (want e.g. %sv1.2.3 or %sv1.2.3-beta.4)",
				field, op, op)
		}
		// semver.Canonical drops build metadata and normalizes v1.2 → v1.2.0.
		// Requiring the canonical form back means a partial version like
		// ">=v1.0" is rejected rather than silently widened to ">=v1.0.0".
		if semver.Canonical(version) != version {
			return Comparison{}, fmt.Errorf(
				"comparison %q is not a canonical major.minor.patch version (got %q, canonical is %q)",
				field, version, semver.Canonical(version))
		}
		return Comparison{Op: op, Version: version}, nil
	}
	return Comparison{}, fmt.Errorf(
		"comparison %q does not start with one of %s", field, strings.Join(operatorStrings(), ", "))
}

func operatorStrings() []string {
	out := make([]string, 0, len(operators))
	for _, op := range operators {
		out = append(out, string(op))
	}
	return out
}

// Comparisons returns the parsed conjunction.
func (c Constraint) Comparisons() []Comparison {
	out := make([]Comparison, len(c.comparisons))
	copy(out, c.comparisons)
	return out
}

// String returns the normalized constraint text — the form the comparisons
// actually ran against, not the author's spelling.
func (c Constraint) String() string {
	parts := make([]string, 0, len(c.comparisons))
	for _, comparison := range c.comparisons {
		parts = append(parts, comparison.String())
	}
	return strings.Join(parts, " ")
}

// Admits reports whether version satisfies every comparison. A version that is
// not valid semver satisfies nothing.
func (c Constraint) Admits(version string) bool {
	if !semver.IsValid(version) {
		return false
	}
	for _, comparison := range c.comparisons {
		if !comparison.admits(version) {
			return false
		}
	}
	return true
}

// CheckEngineCompat parses a manifest's engine_compat expression and applies it
// to an engine version, returning a reason that names both when it excludes.
func CheckEngineCompat(expr, engineVersion string) error {
	constraint, err := ParseConstraint(expr)
	if err != nil {
		return err
	}
	if !semver.IsValid(engineVersion) {
		return fmt.Errorf("engine version %q is not valid semver", engineVersion)
	}
	if !constraint.Admits(engineVersion) {
		return fmt.Errorf("engine_compat %q excludes the running engine %s",
			constraint.String(), engineVersion)
	}
	return nil
}
