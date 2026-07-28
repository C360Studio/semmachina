package dice_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// "No wall clock, no global RNG, no unseeded randomness" is a claim about what
// the code CANNOT do, and no behavioral test can establish it. A clock-seeded
// roller passes "roll twice and compare" whenever the two calls land in the
// same nanosecond, and a roller that mixed in one unseeded draw would still
// reproduce most of the time. Both would ship.
//
// So the claim is checked the only way it can be: by reading this package's
// source. The scan is narrow and mechanical — an import list and the selectors
// applied to the `rand` package identifier — which is exactly what makes it
// hard to argue with.

// forbiddenImports are the entropy and ambient-state sources a deterministic
// component must not reach for, each with the reason it is banned.
var forbiddenImports = map[string]string{
	"time":        "a wall clock is ambient state; a roll that read one would not replay",
	"math/rand":   "math/rand (v1) has a global, implicitly seeded source",
	"crypto/rand": "cryptographic randomness is unreproducible by design; it belongs to seed MINTING, not rolling",
	"os":          "environment and process state are ambient inputs",
	"sync":        "shared mutable state between rolls is exactly what a per-roll generator avoids",
}

func packageFiles(t *testing.T) (*token.FileSet, []*ast.File, []string) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var (
		files []*ast.File
		names []string
	)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files = append(files, file)
		names = append(names, name)
	}
	if len(files) < 2 {
		t.Fatalf("found %d non-test source files; the purity scan would be vacuous", len(files))
	}
	return fset, files, names
}

func TestDicePackage_ImportsNoClockAndNoUnseededRandomness(t *testing.T) {
	_, files, names := packageFiles(t)

	for idx, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
			}
			if reason, forbidden := forbiddenImports[path]; forbidden {
				t.Fatalf("%s imports %q: %s", names[idx], path, reason)
			}
		}
	}
}

// math/rand/v2's TOP-LEVEL functions (rand.IntN, rand.Uint64, rand.N) draw from
// a global, randomly seeded source — the same hazard as v1, one import path
// later. Only the explicit constructor may be used; every draw after that comes
// from the returned generator, whose seed the component derived.
func TestDicePackage_UsesOnlyTheExplicitPCGConstructorFromMathRandV2(t *testing.T) {
	const allowed = "NewPCG"

	_, files, names := packageFiles(t)
	uses := 0

	for idx, file := range files {
		// Confirm this file imports math/rand/v2 under the plain name `rand`;
		// a rename would make the selector scan below look at the wrong ident.
		imported := false
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import: %v", err)
			}
			if path != "math/rand/v2" {
				continue
			}
			if spec.Name != nil {
				t.Fatalf("%s imports math/rand/v2 as %q; the purity scan reads selectors on `rand`",
					names[idx], spec.Name.Name)
			}
			imported = true
		}
		if !imported {
			continue
		}

		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != "rand" || ident.Obj != nil {
				// ident.Obj != nil means a local declaration shadowing the
				// package name, which is not a package-level call.
				return true
			}
			if selector.Sel.Name != allowed {
				t.Fatalf("%s calls rand.%s; math/rand/v2's package-level functions draw from a global, "+
					"randomly seeded source. Only rand.%s is allowed.",
					names[idx], selector.Sel.Name, allowed)
			}
			uses++
			return true
		})
	}

	// Anti-vacuity: if nothing referenced rand.NewPCG, the scan above proved
	// nothing and the component is not generating dice the way it claims.
	if uses == 0 {
		t.Fatal("no rand.NewPCG reference found; the purity scan is looking at the wrong thing")
	}
}

// The roll gate belongs to the adjudicator (D12). The behavioral test proves
// the current code honors the reported gate; this proves the advisory mapping
// has no call site here at all, so it cannot be reintroduced as a precondition
// by someone who reads the mapping's name and assumes it is the authority.
func TestDicePackage_NeverCallsTheAdvisoryRollGateMapping(t *testing.T) {
	const advisory = "RequiresRoll"

	_, files, names := packageFiles(t)
	for idx, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != advisory {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != "vocabulary" {
				// verdict.Scalars.RequiresRoll is the REPORTED gate and is
				// exactly what this package must read.
				return true
			}
			t.Fatalf("%s calls vocabulary.%s; that mapping is advisory, and a component that gated on it "+
				"would take the roll decision back from the fiction (D12)", names[idx], advisory)
			return false
		})
	}
}

// A per-roll generator is the property that makes a roll independent of the
// order turns were resolved in. Holding one on the Roller would be invisible to
// the import scan, so the shape is checked too: no struct field in this package
// may be a *rand.PCG.
func TestDicePackage_HoldsNoGeneratorAsState(t *testing.T) {
	_, files, names := packageFiles(t)

	for idx, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			structType, ok := node.(*ast.StructType)
			if !ok || structType.Fields == nil {
				return true
			}
			for _, field := range structType.Fields.List {
				if !mentionsPCG(field.Type) {
					continue
				}
				t.Fatalf("%s declares a struct field of a PCG type; a generator kept between rolls makes a "+
					"turn's dice depend on what was rolled before it", names[idx])
			}
			return true
		})
	}
}

func mentionsPCG(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "PCG" {
			found = true
		}
		return !found
	})
	return found
}

// The scan is only as good as its premise: it must be reading the files that
// actually contain the roller. A rename that moved the derivation into a file
// the scan skipped would leave every check above passing over nothing.
func TestDicePackage_ScanCoversTheDerivation(t *testing.T) {
	_, _, names := packageFiles(t)
	if !slices.Contains(names, "roller.go") {
		t.Fatalf("the purity scan did not read roller.go; it read %v", names)
	}
}
