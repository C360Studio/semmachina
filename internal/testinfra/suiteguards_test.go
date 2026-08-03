package testinfra_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// These are guards over the SUITE rather than over any package's behavior, and
// they live here because internal/testinfra already owns the two module-wide
// test policies: SkipEnv (the loud opt-out) and the harness import boundary.
//
// All three answer the same question in different registers: does a green run
// actually mean the thing it claims? The CI gate asks it dynamically
// (scripts/check-no-skips.sh reads `go test -json` and refuses a run that
// skipped anything); these ask it statically, which is the half the dynamic
// check cannot see — a t.Skip behind a condition that happens to be false on
// today's runner is invisible to a JSON scan and obvious to a source scan.
//
// Each guard is paired with an anti-vacuity companion that proves the detector
// finds what it is looking for, because a search that can never match passes
// forever and reads exactly like a clean module.

// allowedSkips is every t.Skip call site the module is permitted to contain,
// with the number of calls in each file.
//
// The map is matched EXACTLY in both directions. A new skip anywhere fails, a
// second skip in an allowlisted file fails, and an allowlisted entry whose skip
// has been deleted also fails — an allowlist nobody prunes is how a guard
// quietly stops guarding.
//
// Every entry here is the SAME skip: testinfra.Require's opt-out, reached when
// SEMMACHINA_SKIP_INTEGRATION is set. Two of them are local re-checks in
// packages whose TestMain needs the harness before any test runs. There is no
// other legitimate reason for this module to skip a test — a missing
// prerequisite FAILS here by design (see SkipEnv's doc comment), because a
// suite that reports `ok` for a seam it never exercised is the coverage
// illusion this project spent a week learning to distrust.
var allowedSkips = map[string]int{
	"internal/testinfra/harness.go":          1,
	"internal/boot/boot_integration_test.go": 1,
	"internal/e2e/harness_test.go":           1,
}

// skipFuncs are the testing.TB methods that abandon a test. Matched exactly
// rather than by prefix: testinfra.Skipped() is an ordinary predicate whose
// name begins the same way, and a prefix match would both flag it and teach
// everyone to ignore the guard.
var skipFuncs = map[string]bool{"Skip": true, "Skipf": true, "SkipNow": true}

func TestSuite_SkipsOnlyWhereTheOptOutLives(t *testing.T) {
	found, files := scanSkips(t, moduleRoot(t))

	if maps.Equal(found, allowedSkips) {
		return
	}
	var lines []string
	for _, file := range slices.Sorted(maps.Keys(found)) {
		if allowedSkips[file] != found[file] {
			lines = append(lines, fmt.Sprintf("  %s: found %d, allowed %d", file, found[file], allowedSkips[file]))
		}
	}
	for _, file := range slices.Sorted(maps.Keys(allowedSkips)) {
		if _, ok := found[file]; !ok {
			lines = append(lines, fmt.Sprintf("  %s: found 0, allowed %d (stale allowlist entry)",
				file, allowedSkips[file]))
		}
	}
	t.Fatalf("the module's t.Skip call sites do not match the allowlist (%d files scanned):\n%s\n"+
		"A skip is a test reporting `ok` for work it did not do. If a prerequisite is missing, FAIL — "+
		"see the SkipEnv doc comment. If this is genuinely the opt-out, add it to allowedSkips with a reason.",
		files, strings.Join(lines, "\n"))
}

// The guard above is a search, and a search that cannot match passes forever.
// Three premises hold it up and all three are checked: the detector recognizes
// a skip when it sees one, it does NOT recognize the similarly-named predicate
// next door, and the walk covers a realistic amount of source.
func TestSkipScan_WouldActuallyFindASkip(t *testing.T) {
	const probe = `package p

import "testing"

func TestA(t *testing.T) {
	if Skipped() {
		t.Skip("opted out")
	}
	t.Skipf("also a skip: %d", 1)
	t.SkipNow()
}

func Skipped() bool { return false }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", probe, 0)
	if err != nil {
		t.Fatalf("parse the probe: %v", err)
	}
	if got := countSkips(file); got != 3 {
		t.Fatalf("the detector found %d skips in a probe holding exactly 3 (and one Skipped() predicate "+
			"that must not count); the allowlist guard is not measuring what it claims", got)
	}

	_, files := scanSkips(t, moduleRoot(t))
	if files < 40 {
		t.Fatalf("the skip scan read only %d Go files; the walk is not covering the module", files)
	}
}

// sourceURL matches any URL literal in source. Paired with allowedURLHosts and
// the exact-file exception below, it finds one whose host is neither loopback
// nor a reserved documentation name.
//
// This is the structural half of "CI runs token-free". The workflow supplies no
// model credentials, but "we did not configure a live endpoint" is exactly the
// shape of gate that passes because it did nothing — a fixture or default
// config that hardcodes a provider URL could make CI reach the network without
// any workflow step changing. The one committed live-smoke example is inert in
// CI and allowed by exact file and URL; moving that URL or adding another still
// fails. The persona layer never names a URL (it resolves a capability), so a
// new remote endpoint anywhere else is worth a failing test.
var (
	sourceURL          = regexp.MustCompile(`https?://[^\s"'` + "`" + `\\)]+`)
	geminiLiveEndpoint = "https:/" + "/generativelanguage.googleapis.com/v1beta/openai"

	// RFC 2606 reserves example.com/.org/.net for documentation and .invalid
	// for names that must never resolve. They appear here only as opaque string
	// data in tests — nothing dials them — and are reserved precisely so that
	// remains safe.
	allowedURLHosts = map[string]bool{
		"127.0.0.1":     true,
		"localhost":     true,
		"[::1]":         true,
		"::1":           true,
		"example.com":   true,
		"example.org":   true,
		"example.net":   true,
		"graph.invalid": true,
	}

	// These tracked package-manager/compiler metadata files contain registry
	// tarball and documentation URLs, not runtime endpoint configuration.
	ignoredURLScanFiles = map[string]bool{
		"web/package-lock.json": true,
		"web/tsconfig.json":     true,
	}

	// This is data for a manual paid smoke, not a default or test endpoint. The
	// exception is deliberately exact in both dimensions: another endpoint in
	// this file, or this endpoint copied into executable source/default config,
	// fails the suite guard.
	allowedRemoteURLs = map[string]map[string]bool{
		"configs/instance.gemini36-flash.example.json": {
			geminiLiveEndpoint: true,
		},
		"configs/instance.gemini36-flash.bellweather.example.json": {
			geminiLiveEndpoint: true,
		},
		"configs/instance.gemini35-flash-lite.bellweather.example.json": {
			geminiLiveEndpoint: true,
		},
	}
)

func TestSuite_NamesNoRemoteEndpoint(t *testing.T) {
	root := moduleRoot(t)
	scanned := 0
	var offenders []string

	walk(t, root, func(path, rel string, entry fs.DirEntry) {
		if ignoredURLScanFiles[rel] {
			return
		}
		switch filepath.Ext(entry.Name()) {
		case ".go", ".json", ".jsonl", ".yaml", ".yml":
		default:
			return
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		scanned++
		for _, raw := range sourceURL.FindAllString(string(body), -1) {
			candidate := strings.TrimRight(raw, ".,;:")
			parsed, err := url.Parse(candidate)
			if err != nil {
				continue
			}
			if allowedURLHosts[parsed.Hostname()] || allowedURLHosts[parsed.Host] {
				continue
			}
			if allowedRemoteURLs[rel][candidate] {
				continue
			}
			offenders = append(offenders, fmt.Sprintf("  %s: %s", rel, raw))
		}
	})

	if scanned < 40 {
		t.Fatalf("the endpoint scan read only %d files; the walk is not covering the module", scanned)
	}
	if len(offenders) > 0 {
		t.Fatalf("source or fixtures name %d remote URL(s):\n%s\n"+
			"CI runs the full loop token-free against internal/mockmodel on loopback. A remote URL in the "+
			"module is how a gate starts reaching the network without any workflow step saying so. "+
			"Live-model runs require an exact-file exception and must never become a committed default.",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// Anti-vacuity for the scan above: the regex must actually match a remote URL,
// and the allowlist must not be so broad that everything passes.
func TestEndpointScan_WouldActuallyFindARemoteURL(t *testing.T) {
	// Assembled from parts rather than written whole, because the scan above
	// reads THIS file too: a literal provider URL here would make the guard fail
	// on its own anti-vacuity probe. Split after the scheme — the regex needs at
	// least one character after `//`, so the first fragment matches nothing on
	// its own. (Verified the hard way: written whole, it failed exactly this
	// test's own guard, which is the most direct proof available that the scan
	// finds a remote URL wherever it appears.)
	probe := `url := "https://` + `api.openai.com/v1"; local := "http://127.0.0.1:11434/v1"`
	matches := sourceURL.FindAllString(probe, -1)
	if len(matches) != 2 {
		t.Fatalf("the URL regex found %d URLs in a probe holding 2: %v", len(matches), matches)
	}
	remote, err := url.Parse(matches[0])
	if err != nil {
		t.Fatalf("parse the remote probe URL: %v", err)
	}
	if allowedURLHosts[remote.Hostname()] {
		t.Fatalf("the allowlist accepts %s, so the scan would pass a live provider endpoint", remote.Hostname())
	}
	if !allowedRemoteURLs["configs/instance.gemini36-flash.example.json"][geminiLiveEndpoint] {
		t.Fatal("the committed Gemini live-smoke example is not covered by its exact-file exception")
	}
	if !allowedRemoteURLs["configs/instance.gemini36-flash.bellweather.example.json"][geminiLiveEndpoint] {
		t.Fatal("the Bellweather Gemini paid-smoke example is not covered by its exact-file exception")
	}
	if !allowedRemoteURLs["configs/instance.gemini35-flash-lite.bellweather.example.json"][geminiLiveEndpoint] {
		t.Fatal("the Bellweather Flash-Lite paid-smoke example is not covered by its exact-file exception")
	}
	if allowedRemoteURLs["configs/instance.example.json"][geminiLiveEndpoint] {
		t.Fatal("the Gemini live endpoint is allowed in the default instance configuration")
	}
	if allowedRemoteURLs["cmd/bellweather-smoke/main.go"][geminiLiveEndpoint] {
		t.Fatal("the Gemini live endpoint is allowed in executable smoke source")
	}
	for file, endpoints := range allowedRemoteURLs {
		if len(endpoints) != 1 || !endpoints[geminiLiveEndpoint] {
			t.Fatalf("remote endpoint exception for %s is broader than the one exact Gemini URL: %v", file, endpoints)
		}
	}
	loopback, err := url.Parse(matches[1])
	if err != nil {
		t.Fatalf("parse the loopback probe URL: %v", err)
	}
	if !allowedURLHosts[loopback.Hostname()] {
		t.Fatalf("the allowlist rejects %s, so the scan would fail every honest run", loopback.Hostname())
	}
}

// moduleRoot resolves the module root from this package's directory, asserting
// the go.mod is there rather than assuming it: a moved package would otherwise
// silently scan a subtree and report a clean bill of health for two files.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at the presumed module root %s: %v", root, err)
	}
	return root
}

// formatFuncs are the testing.TB helpers whose whole job is formatting. Called
// with a single argument they are formatting nothing, and the non-formatting
// sibling (Fatal, Error, Log, Skip) is what was meant.
var formatFuncs = map[string]string{
	"Fatalf": "Fatal",
	"Errorf": "Error",
	"Logf":   "Log",
	"Skipf":  "Skip",
}

// `t.Fatalf("a sentence")` is not a bug today and is a bug the first time
// someone edits the sentence to contain a percent sign — at which point the
// failure message is corrupted precisely when it is being read, during a
// failure. go vet does not catch it: its printf analyzer flags a NON-CONSTANT
// format string and a mismatched argument count, and a constant string with no
// arguments is neither. revive has no rule for it either.
//
// So the guard is here. It is deliberately narrow — exactly one argument to a
// method whose name ends in f, called on a receiver named like a testing.TB —
// which makes it unambiguous rather than stylistic.
//
// The receiver filter is what keeps it honest, and it was added after the first
// version flagged twenty-odd `fmt.Errorf("constant")` calls in production code.
// Those are a different question with a different answer (errors.New), and a
// guard that raises it here would be a style argument wearing a correctness
// guard's clothes. The cost is that `h.t.Fatalf(...)` — a TB reached through a
// struct field — is not seen; type-checking the module to close that would make
// this the slowest test in the suite for the rarest shape.
var testingReceivers = map[string]bool{"t": true, "b": true, "tb": true, "f": true}

func TestSuite_UsesTheNonFormattingLogHelpers(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	var offenders []string
	files := 0

	walk(t, root, func(path, rel string, entry fs.DirEntry) {
		if !strings.HasSuffix(entry.Name(), ".go") {
			return
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		files++
		for _, hit := range findFormatlessFormatCalls(file) {
			position := fset.Position(hit.Pos())
			selector := hit.Fun.(*ast.SelectorExpr)
			offenders = append(offenders, fmt.Sprintf("  %s:%d: %s -> %s",
				rel, position.Line, selector.Sel.Name, formatFuncs[selector.Sel.Name]))
		}
	})

	if files < 40 {
		t.Fatalf("the format-call scan read only %d Go files; the walk is not covering the module", files)
	}
	if len(offenders) > 0 {
		t.Fatalf("%d formatting call(s) with nothing to format:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// Anti-vacuity: the detector must find the one-argument call and must leave the
// legitimate two-argument one alone.
func TestFormatCallScan_WouldActuallyFindOne(t *testing.T) {
	const probe = `package p

import "testing"

func TestA(t *testing.T) {
	t.Fatalf("no verbs here")
	t.Fatalf("a verb: %d", 1)
	t.Fatal("already right")
	_ = fmt.Errorf("a constant error, which is a different question")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", probe, 0)
	if err != nil {
		t.Fatalf("parse the probe: %v", err)
	}
	hits := findFormatlessFormatCalls(file)
	if len(hits) != 1 {
		t.Fatalf("the detector found %d one-argument formatting calls in a probe holding exactly 1", len(hits))
	}
}

func findFormatlessFormatCalls(file *ast.File) []*ast.CallExpr {
	var hits []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || !testingReceivers[receiver.Name] {
			return true
		}
		if _, ok := formatFuncs[selector.Sel.Name]; ok && len(call.Args) == 1 {
			hits = append(hits, call)
		}
		return true
	})
	return hits
}

// walk visits every file under root that could carry module source, skipping
// the directories `go build ./...` also ignores.
func walk(t *testing.T, root string, visit func(path, rel string, entry fs.DirEntry)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		visit(path, filepath.ToSlash(rel), entry)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module source: %v", err)
	}
}

// scanSkips returns skip counts by module-relative file, and the number of Go
// files it read.
func scanSkips(t *testing.T, root string) (map[string]int, int) {
	t.Helper()
	found := make(map[string]int)
	files := 0
	fset := token.NewFileSet()

	walk(t, root, func(path, rel string, entry fs.DirEntry) {
		if !strings.HasSuffix(entry.Name(), ".go") {
			return
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		files++
		if n := countSkips(file); n > 0 {
			found[rel] = n
		}
	})
	return found, files
}

// countSkips counts calls to a testing.TB skip method in one parsed file.
//
// The match is on the METHOD NAME, not on the receiver's type: type-checking
// the module would make this guard the slowest test in the suite, and no
// package here declares a Skip/Skipf/SkipNow of its own for it to confuse.
func countSkips(file *ast.File) int {
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if skipFuncs[selector.Sel.Name] {
			count++
		}
		return true
	})
	return count
}
