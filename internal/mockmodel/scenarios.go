package mockmodel

import (
	"embed"
	"fmt"
	"io/fs"
	"sync"
)

// turnLoopScenariosPath is the embedded path of the shipped scenario pack.
const turnLoopScenariosPath = "scenarios/turnloop.json"

// The pack is embedded rather than read from disk for the same reason the
// starter world is: a broken or missing file has to be a test-time failure
// rather than a run that quietly finds no fixture and covers nothing.
//
//go:embed scenarios/turnloop.json
var scenarios embed.FS

var (
	turnLoopOnce    sync.Once
	turnLoopFixture *Fixture
	turnLoopErr     error
)

// TurnLoopScenarios returns the shipped turn-loop scenario pack, parsed and
// validated once.
//
// Three things about the content are worth knowing before depending on it, and
// the file's own description repeats them: the terminal tool names and argument
// shapes are data that follows the persona loops rather than defining them;
// effect targets are six-part entity IDs and therefore bound to one world
// instance (org c360, namespace world1, template starter); and no scenario can
// choose an outcome band, because a verdict declares all three and the seeded
// dice pick one — the miss/partial/full packs differ only in the narrator's
// voice.
//
// The returned fixture is shared. Nothing in the serving path mutates it, and
// per-run state lives on the Handler, so a caller may hand the same fixture to
// several stubs.
func TurnLoopScenarios() (*Fixture, error) {
	turnLoopOnce.Do(func() {
		data, err := fs.ReadFile(scenarios, turnLoopScenariosPath)
		if err != nil {
			turnLoopErr = fmt.Errorf("read the embedded scenario pack: %w", err)
			return
		}
		turnLoopFixture, turnLoopErr = ParseFixture(data)
	})
	return turnLoopFixture, turnLoopErr
}
