package boot_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/world"
)

// selectedPersonaWorld is pure fixture construction shared by unit and
// acceptance tests. Keeping it untagged lets boot.New's pre-start validation
// remain in the Docker-free lane.
func selectedPersonaWorld(t *testing.T, adjudicatorID, narratorID, unselectedID string) fstest.MapFS {
	t.Helper()
	source, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("StarterWorld: %v", err)
	}
	worldFS := make(fstest.MapFS)
	if err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := fs.ReadFile(source, name)
		if readErr != nil {
			return readErr
		}
		worldFS[name] = &fstest.MapFile{Data: data}
		return nil
	}); err != nil {
		t.Fatalf("copy starter world: %v", err)
	}
	worldFS["personas/selected-adjudicator.json"] = &fstest.MapFile{Data: personaRecord(adjudicatorID, "adjudicator")}
	worldFS["personas/selected-narrator.json"] = &fstest.MapFile{Data: personaRecord(narratorID, "narrator")}
	worldFS["personas/unselected.json"] = &fstest.MapFile{Data: personaRecord(unselectedID, "narrator")}
	worldFS[world.PacksFile] = &fstest.MapFile{Data: []byte(`version: 1
defaults:
  persona_pack: selected
  mechanics_pack: empty
persona_packs:
  selected:
    files:
      - personas/selected-adjudicator.json
      - personas/selected-narrator.json
  unselected:
    files:
      - personas/selected-adjudicator.json
      - personas/unselected.json
mechanics_packs:
  empty:
    files: []
`)}
	return worldFS
}
