package testinfra_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

// testinfra is a NORMAL package. It imports testing and testcontainers, but
// nothing about its shape is test-only: it has no _test.go suffix, so it
// compiles into the module build and the toolchain will import it into anything
// that asks. "It is imported only from _test files" is a claim about the module,
// and the compiler does not check it.
//
// The failure that claim exists to prevent is not subtle — a cmd/ binary that
// imports the harness links a Docker-starting, container-terminating dependency
// into a production process — but it is SILENT: `go build ./...` stays green,
// `go vet` says nothing, and the binary only misbehaves where it runs. No cmd/
// exists yet, which is exactly why the guard is cheap to add now and awkward to
// add after the first one is written.
//
// So the boundary is checked the way internal/dice checks determinism: by
// reading the module's source. The scan covers every non-test file in the whole
// module, including packages that do not exist yet.
const harnessDir = "internal/testinfra"

// scan is one pass over the module's Go source, split by the only distinction
// that matters here.
type scan struct {
	// root is the module root, and modulePath the path declared in its go.mod.
	root       string
	modulePath string
	// harnessPkg is the import path under test, DERIVED from go.mod rather than
	// written out: a hardcoded path that drifted from the module would make
	// every check below scan for a string nothing can contain.
	harnessPkg string

	// prodFiles is every non-test .go file, module-relative.
	prodFiles []string
	// prodImporters and testImporters are the files that import the harness.
	prodImporters []string
	testImporters []string
}

func scanModule(t *testing.T) scan {
	t.Helper()

	// The test's working directory is its own package directory, so the module
	// root is two levels up. Asserted rather than assumed: a moved package would
	// otherwise silently scan a subtree.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod at the presumed module root %s: %v", root, err)
	}
	modulePath := modfile.ModulePath(gomod)
	if modulePath == "" {
		t.Fatalf("go.mod at %s declares no module path", root)
	}

	result := scan{
		root:       root,
		modulePath: modulePath,
		harnessPkg: modulePath + "/" + harnessDir,
	}

	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			// .git and friends carry no buildable Go source; vendor/ would be
			// third-party code this module does not author.
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}

		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		if !isTest {
			result.prodFiles = append(result.prodFiles, rel)
		}
		for _, spec := range file.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if imported != result.harnessPkg && !strings.HasPrefix(imported, result.harnessPkg+"/") {
				continue
			}
			if isTest {
				result.testImporters = append(result.testImporters, rel)
			} else {
				result.prodImporters = append(result.prodImporters, rel)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk the module source: %v", walkErr)
	}
	return result
}

func TestTestinfra_IsImportedByNoNonTestPackage(t *testing.T) {
	result := scanModule(t)
	if len(result.prodImporters) == 0 {
		return
	}
	t.Fatalf("%d non-test file(s) import %s: %v\n"+
		"The harness starts a Docker container and calls testing.T; a binary that links it ships that. "+
		"Move whatever is needed into a package that does not, or make the importing file a _test.go.",
		len(result.prodImporters), result.harnessPkg, result.prodImporters)
}

// The guard above is a search for a string, and a search that can never match
// passes forever. Three premises make it real, and all three are checked here:
// the walk reads non-test files, the import path it looks for is one that files
// actually write, and the harness itself is inside the scanned subtree.
func TestImportScan_WouldActuallyFindAnImport(t *testing.T) {
	result := scanModule(t)

	if len(result.testImporters) == 0 {
		t.Fatalf("no _test.go file in the module imports %s, so the scan has never matched anything "+
			"and the production check above proves nothing", result.harnessPkg)
	}
	if !slices.Contains(result.prodFiles, harnessDir+"/harness.go") {
		t.Fatalf("the scan did not read %s/harness.go as a non-test file; it read %d non-test files "+
			"rooted at %s", harnessDir, len(result.prodFiles), result.root)
	}
	// A module with almost no source would make the walk look healthy while
	// covering nothing.
	if len(result.prodFiles) < 10 {
		t.Fatalf("the scan found only %d non-test files under %s; the walk is not covering the module: %v",
			len(result.prodFiles), result.root, result.prodFiles)
	}
}
