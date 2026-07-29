package mockmodel_test

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

// "Same scenario in, same bytes out" is a claim about what this package CANNOT
// do, and the byte-equality tests can only sample it. A response id built from
// a nanosecond timestamp is identical across two calls in the same
// millisecond; a step chosen with a random tiebreak reproduces most of the
// time. Both would pass a comparison and fail a replay.
//
// So the claim is also checked the way the dice component's is: by reading
// this package's own source for the ambient inputs a deterministic stub must
// not have.
var forbiddenImports = map[string]string{
	"time":         "a wall clock is ambient state; a response that read one would differ between runs",
	"math/rand":    "math/rand (v1) has a global, implicitly seeded source",
	"math/rand/v2": "a scripted endpoint has nothing to randomise; selection is the fixture's job",
	"crypto/rand":  "unreproducible by design, and nothing here needs entropy",
	"os":           "environment and process state are ambient inputs to an answer that must come from the fixture",
	"testing":      "this is test INFRASTRUCTURE, not a test: importing testing registers flags in whatever binary serves the stub",
}

func packageFiles(t *testing.T) ([]*ast.File, []string) {
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
	if len(files) < 3 {
		t.Fatalf("found %d non-test source files; the purity scan would be vacuous", len(files))
	}
	return files, names
}

func TestMockModelPackage_ReadsNoClockAndDrawsNoRandomness(t *testing.T) {
	files, names := packageFiles(t)

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

// The scan is only as good as its premise: it has to be reading the file that
// actually builds the response. A rename that moved response construction into
// a file the scan skipped would leave the check above passing over nothing.
func TestMockModelPackage_ScanCoversTheResponseBuilder(t *testing.T) {
	files, names := packageFiles(t)
	if !slices.Contains(names, "handler.go") {
		t.Fatalf("the purity scan did not read handler.go; it read %v", names)
	}

	found := false
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			decl, ok := node.(*ast.FuncDecl)
			if ok && decl.Name.Name == "buildResponse" {
				found = true
			}
			return !found
		})
	}
	if !found {
		t.Fatal("the scanned files declare no buildResponse; the scan is looking at the wrong package")
	}
}
