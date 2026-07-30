package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every binary's bootstrap has to make two registrations, and both failures are
// silent in a way that looks like an idle game rather than a broken one.
//
//   - payload.RegisterPayloads. Without it a decoder built over the registry
//     consumes every player action and decodes none of them, because "this is not
//     a player action" is an ordinary answer for a decoder. The engine accepts no
//     turns and reports nothing.
//   - vocabulary.RegisterPredicates. The rule processor's config validation
//     rejects a canonical-but-undeclared predicate, so the turn-sequencing pack
//     does not load — an engine that takes actions and never adjudicates them.
//
// internal/boot CHECKS the first (it round-trips a payload through the registry
// it is handed) and cannot check the second, because the predicate registry is
// upstream's process-global map with no reader this engine can ask. So this test
// exists for the shape neither covers: a SECOND binary, added later, whose author
// copies main.go's structure and not its bootstrap. A grep over cmd/ is the only
// thing that sees a package that does not exist yet.
//
// It is an AST scan rather than a text grep so a call inside a comment or a
// string does not satisfy it.
func TestEveryCommandRegistersItsBootstrap(t *testing.T) {
	required := map[string]string{
		"payload.RegisterPayloads": "a binary without it consumes every player action, decodes none, and " +
			"accepts no turns",
		"vocabulary.RegisterPredicates": "a binary without it cannot load the turn-sequencing rule pack, so it " +
			"takes actions and never adjudicates them",
	}

	commands := mainPackages(t, "..")
	if len(commands) == 0 {
		t.Fatal("no main packages found under cmd/; this check would be vacuous")
	}
	for dir, calls := range commands {
		for call, why := range required {
			if !calls[call] {
				t.Errorf("%s never calls %s: %s", dir, call, why)
			}
		}
	}
}

// mainPackages returns, per command directory, the set of qualified calls its
// non-test sources make.
func mainPackages(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	found := map[string]map[string]bool{}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		if file.Name.Name != "main" {
			return nil
		}
		dir := filepath.Dir(path)
		if found[dir] == nil {
			found[dir] = map[string]bool{}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			found[dir][pkg.Name+"."+selector.Sel.Name] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk cmd/: %v", err)
	}
	return found
}
