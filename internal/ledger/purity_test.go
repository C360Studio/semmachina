package ledger_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// "Replay reads persona output; it never regenerates it" is a claim about what
// this package CANNOT do, and no behavioral test establishes it. A reader that
// fell back to re-running the narrator when an object was missing would pass
// every test that has a narration in the store — which is all of them — and
// would only be caught by the one campaign whose prose had been garbage
// collected, months later, with the difference indistinguishable from the
// original.
//
// So the claim is checked the only way it can be: by reading this package's
// source and its linked dependency tree. Both halves are needed and neither is
// sufficient. The import scan catches a direct call into a model API; the
// dependency check catches a persona reached through a helper package, which the
// import scan of THIS package's files would never see.

// forbiddenImports are the ways prose could be produced rather than read.
var forbiddenImports = map[string]string{
	"github.com/c360studio/semmachina/internal/persona":   "a persona re-run is a new rendition, not a replay",
	"github.com/c360studio/semmachina/internal/stage":     "the turn loop RUNS turns; the ledger records ones that ran",
	"github.com/c360studio/semmachina/internal/mockmodel": "a scripted model endpoint is still a model endpoint",
	"github.com/c360studio/semmachina/internal/scene":     "assembling context is what you do BEFORE calling a model",
	"github.com/c360studio/semstreams/agentic":            "an agentic loop here could only regenerate an artifact",
	"github.com/c360studio/semstreams/model":              "a model endpoint in the archive is history being invented",
}

// forbiddenDeps are packages that must not appear anywhere in the linked
// dependency tree, not merely in this package's own imports.
//
// The upstream `agentic` and `model` packages are deliberately absent from this
// list even though they are in forbiddenImports: the framework's own component
// and rule configuration types import them, so they arrive transitively in
// anything that names a stream. Listing them here would make the test fail for a
// reason that has nothing to do with replay honesty — and a test that fails for
// the wrong reason gets deleted. What matters, and what is checked, is that the
// ENGINE's persona machinery is unreachable.
var forbiddenDeps = []string{
	"github.com/c360studio/semmachina/internal/persona",
	"github.com/c360studio/semmachina/internal/stage",
	"github.com/c360studio/semmachina/internal/mockmodel",
	"github.com/c360studio/semmachina/internal/scene",
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
	if len(files) < 4 {
		t.Fatalf("found %d non-test source files; the purity scan would be vacuous", len(files))
	}
	return files, names
}

func TestLedgerPackage_ImportsNothingThatCouldRegenerateAnArtifact(t *testing.T) {
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

// The import scan reads this package's files; a persona reached through a helper
// package would be invisible to it. This reads what the linker actually pulls in.
func TestLedgerPackage_LinksNoneOfTheEnginesPersonaMachinery(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(deps) < 10 {
		t.Fatalf("go list -deps returned %d packages; the check is looking at the wrong thing", len(deps))
	}

	for _, forbidden := range forbiddenDeps {
		if slices.Contains(deps, forbidden) {
			t.Fatalf("the ledger links %s; replay honesty rests on a persona being UNREACHABLE from here, "+
				"not merely uncalled", forbidden)
		}
	}

	// Anti-vacuity: the packages that SHOULD be linked are, so a check that
	// found nothing is a check that read a real dependency tree.
	for _, required := range []string{
		"github.com/c360studio/semmachina/internal/dice",
		"github.com/c360studio/semmachina/internal/content",
		"github.com/c360studio/semmachina/internal/campaign",
	} {
		if !slices.Contains(deps, required) {
			t.Fatalf("the ledger does not link %s; the dependency check is reading the wrong package", required)
		}
	}
}

// The scan is only as good as its premise: it has to be reading the files that
// actually contain the replay path. A rename that moved the reader into a file
// the scan skipped would leave every check above passing over nothing.
func TestLedgerPackage_ScanCoversTheReplayReader(t *testing.T) {
	_, names := packageFiles(t)
	for _, required := range []string{"replay.go", "writer.go", "manifest.go", "reconcile.go"} {
		if !slices.Contains(names, required) {
			t.Fatalf("the purity scan did not read %s; it read %v", required, names)
		}
	}
}
