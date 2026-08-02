package world_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/world"
)

func TestOpenPackageDirectory_RefusesCatalogSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	packageDir := filepath.Join(base, "package")
	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	packageFiles := catalogPackageFS()
	packageFiles[world.PacksFile].Data = []byte(strings.Replace(validCatalog,
		"personas/bright.json", "personas/variants/escape.json", 1))
	for name, file := range packageFiles {
		fullPath := filepath.Join(packageDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, file.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outsidePersona := filepath.Join(outsideDir, "escape.json")
	if err := os.WriteFile(outsidePersona, []byte(
		`{"id":"outside/narrator","category":100,"roles":["narrator"],"content":"Escaped."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(packageDir, "personas", "variants", "escape.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePersona, link); err != nil {
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, errors.ErrUnsupported) {
			t.Skipf("symlink creation is not permitted or supported: %v", err)
		}
		t.Fatalf("create escape symlink: %v", err)
	}

	root, err := world.OpenPackageDirectory(packageDir)
	if err != nil {
		t.Fatalf("OpenPackageDirectory: %v", err)
	}
	defer root.Close() //nolint:errcheck // test cleanup
	_, err = world.LoadPackage(root, world.LoadOptions{})
	if err == nil {
		t.Fatal("LoadPackage followed a catalog-declared symlink outside the package root")
	}
	if !strings.Contains(err.Error(), "personas/variants/escape.json") {
		t.Fatalf("symlink refusal %q does not name the catalog path", err)
	}
}
