package world_test

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/c360studio/semmachina/internal/world"
)

const validCatalog = `version: 1
defaults:
  persona_pack: dusk
  mechanics_pack: quiet
persona_packs:
  dusk:
    files:
      - personas/narrator.json
      - personas/shared.json
  bright:
    files:
      - personas/bright.json
      - personas/shared.json
mechanics_packs:
  quiet:
    files: []
  reactive:
    files:
      - rules/00-stub.json
`

func catalogPackageFS() fstest.MapFS {
	fsys := minimalPackageFS()
	fsys[world.PacksFile] = &fstest.MapFile{Data: []byte(validCatalog)}
	fsys["personas/shared.json"] = &fstest.MapFile{Data: []byte(
		`{"id":"shared/adjudicator","category":100,"roles":["adjudicator"],"content":"Judge."}`)}
	fsys["personas/bright.json"] = &fstest.MapFile{Data: []byte(
		`{"id":"bright/narrator","category":100,"roles":["narrator"],"content":"Narrate brightly."}`)}
	return fsys
}

func TestLoadPackage_LoadsClosedVersionedExperienceCatalog(t *testing.T) {
	pkg, err := world.LoadPackage(catalogPackageFS(), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}

	if pkg.Catalog.Version != 1 || pkg.Catalog.DefaultPersonaPack != "dusk" ||
		pkg.Catalog.DefaultMechanicsPack != "quiet" {
		t.Fatalf("catalog identity/defaults = %#v", pkg.Catalog)
	}
	if got, want := pkg.Catalog.PersonaPacks["dusk"],
		[]string{"personas/narrator.json", "personas/shared.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dusk persona files = %v, want %v", got, want)
	}
	if got := pkg.Catalog.PersonaPacks["bright"]; len(got) != 2 || got[1] != "personas/shared.json" {
		t.Fatalf("cross-pack persona reuse was not preserved: %v", got)
	}
	if got := pkg.Catalog.MechanicsPacks["quiet"]; got == nil || len(got) != 0 {
		t.Fatalf("empty mechanics pack = %#v, want a declared empty list", got)
	}
}

func TestLoadPackage_NoCatalogExposesLegacyImplicitDefaultsWithoutActivatingRules(t *testing.T) {
	pkg, err := world.LoadPackage(minimalPackageFS(), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}

	if pkg.Catalog.DefaultPersonaPack != "default" || pkg.Catalog.DefaultMechanicsPack != "default" {
		t.Fatalf("legacy defaults = %#v", pkg.Catalog)
	}
	if got, want := pkg.Catalog.PersonaPacks["default"],
		[]string{"personas/adjudicator.json", "personas/narrator.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("implicit persona pack = %v, want %v", got, want)
	}
	if got := pkg.Catalog.MechanicsPacks["default"]; got == nil || len(got) != 0 {
		t.Fatalf("implicit mechanics pack = %#v; legacy rules must remain inert", got)
	}
}

func TestLoadPackage_RejectsInvalidExperienceCatalogBeforeResolution(t *testing.T) {
	cases := map[string]struct {
		catalog string
		name    string
	}{
		"unknown field": {
			catalog: strings.Replace(validCatalog, "version: 1", "version: 1\nsurprise: true", 1),
			name:    "surprise",
		},
		"unknown version": {
			catalog: strings.Replace(validCatalog, "version: 1", "version: 2", 1),
			name:    "version",
		},
		"missing persona default": {
			catalog: strings.Replace(validCatalog, "  persona_pack: dusk\n", "", 1),
			name:    "persona_pack",
		},
		"missing mechanics default": {
			catalog: strings.Replace(validCatalog, "  mechanics_pack: quiet\n", "", 1),
			name:    "mechanics_pack",
		},
		"default names no pack": {
			catalog: strings.Replace(validCatalog, "persona_pack: dusk", "persona_pack: absent", 1),
			name:    "absent",
		},
		"empty persona pack": {
			catalog: strings.Replace(validCatalog,
				"  dusk:\n    files:\n      - personas/narrator.json\n      - personas/shared.json",
				"  dusk:\n    files: []", 1),
			name: "dusk",
		},
		"duplicate within pack": {
			catalog: strings.Replace(validCatalog, "      - personas/shared.json\n  bright:",
				"      - personas/shared.json\n      - personas/shared.json\n  bright:", 1),
			name: "personas/shared.json",
		},
		"missing file": {
			catalog: strings.Replace(validCatalog, "personas/bright.json", "personas/missing.json", 1),
			name:    "personas/missing.json",
		},
		"path traversal": {
			catalog: strings.Replace(validCatalog, "personas/bright.json", "personas/../rules/00-stub.json", 1),
			name:    "personas/../rules/00-stub.json",
		},
		"outside matching directory": {
			catalog: strings.Replace(validCatalog, "personas/bright.json", "rules/00-stub.json", 1),
			name:    "rules/00-stub.json",
		},
		"wrong extension": {
			catalog: strings.Replace(validCatalog, "personas/bright.json", "personas/bright.yaml", 1),
			name:    "personas/bright.yaml",
		},
		"invalid pack name": {
			catalog: strings.Replace(validCatalog, "  bright:", "  bright.voice:", 1),
			name:    "bright.voice",
		},
		"unknown pack field": {
			catalog: strings.Replace(validCatalog, "  bright:\n    files:",
				"  bright:\n    description: nope\n    files:", 1),
			name: "description",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := catalogPackageFS()
			fsys[world.PacksFile] = &fstest.MapFile{Data: []byte(tc.catalog)}
			_, err := world.LoadPackage(fsys, world.LoadOptions{})
			if err == nil {
				t.Fatal("LoadPackage accepted an invalid experience catalog")
			}
			if !strings.Contains(err.Error(), world.PacksFile) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("catalog refusal %q does not name %q and %q", err, world.PacksFile, tc.name)
			}
		})
	}
}

func TestLoadPackage_CatalogFilesUseTheCurrentPersonaAndRuleValidators(t *testing.T) {
	for name, tc := range map[string]struct {
		from, to, file, data, names string
	}{
		"persona": {
			from: "personas/bright.json", to: "personas/variants/bright.json",
			file: "personas/variants/bright.json",
			data: `{"id":"bright/narrator","category":100,"roles":["narrator"],` +
				`"content":"Bright.","model":"package-chosen"}`,
			names: "model",
		},
		"rule": {
			from: "rules/00-stub.json", to: "rules/variants/unsafe.json",
			file: "rules/variants/unsafe.json",
			data: `{"id":"unsafe","type":"expression","name":"unsafe","enabled":true,` +
				`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[` +
				`{"field":"turn.phase.current","operator":"eq","value":"complete"}],"logic":"and"}`,
			names: "turn.phase.current",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fsys := catalogPackageFS()
			fsys[world.PacksFile] = &fstest.MapFile{Data: []byte(strings.Replace(validCatalog, tc.from, tc.to, 1))}
			fsys[tc.file] = &fstest.MapFile{Data: []byte(tc.data)}
			_, err := world.LoadPackage(fsys, world.LoadOptions{})
			if err == nil {
				t.Fatal("LoadPackage accepted a catalog file rejected by the current validator")
			}
			for _, want := range []string{world.PacksFile, tc.file, tc.names} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("catalog validation refusal %q does not name %q", err, want)
				}
			}
		})
	}
}
