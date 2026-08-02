package mockmodel

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

// turnLoopScenariosPath is the embedded path of the shipped scenario pack.
const turnLoopScenariosPath = "scenarios/turnloop.json"

const bellweatherScenariosPath = "scenarios/bellweather.json"

// TurnLoopInstancePrefix is the four leading positions of every effect target
// the shipped pack names: org, platform, world namespace, template.
//
// An effect intent names a SIX-PART entity ID, so a pack that proposes an effect
// is bound to one world instance and cannot be otherwise — there is no wildcard
// in an entity id. The prefix is stated here so the binding is a constant a test
// can rebind rather than a string spread through a JSON file, and
// TurnLoopScenariosIn is the rebinding.
const TurnLoopInstancePrefix = "c360.semmachina.world1.starter."

// BellweatherInstancePrefix is the authored Bellweather pack's six-part ID prefix.
const BellweatherInstancePrefix = "c360.semmachina.world1.bellweather-maze."

// The pack is embedded rather than read from disk for the same reason the
// starter world is: a broken or missing file has to be a test-time failure
// rather than a run that quietly finds no fixture and covers nothing.
//
//go:embed scenarios/*.json
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

// TurnLoopScenariosIn returns the shipped pack with every effect target rebound
// to one world namespace.
//
// # Why a rebinding exists at all
//
// Because the pack's effect targets are six-part entity IDs (see
// TurnLoopInstancePrefix) and an end-to-end suite wants ISOLATION: a run whose
// tests share one world namespace shares one campaign, one player and one mutable
// set of world facts, so a scenario that wounds a character changes what the next
// scenario reads. Order-dependent state is the cheapest way for an end-to-end test
// to pass for the wrong reason, and the alternative to rebinding is a second copy
// of the pack per world.
//
// # Why it refuses rather than returning the pack unchanged
//
// A substitution that matched nothing would hand back a pack still bound to
// `world1` — every effect naming an entity the caller's world does not contain,
// every turn failing on a missing target, and the diagnosis pointing at the
// applier rather than at this call. So a pack with no occurrence of the prefix is
// an error: it means the pack was re-authored and this function is now silently
// a no-op.
//
// The namespace is checked for the one character that would corrupt an entity id
// without failing anything here — a dot, which adds a position and turns a
// six-part id into a seven-part one. Everything else about namespace legality is
// the world loader's contract, and it will refuse there.
func TurnLoopScenariosIn(worldNS string) (*Fixture, error) {
	if worldNS == "" {
		return nil, fmt.Errorf("rebinding the scenario pack needs a world namespace")
	}
	if strings.Contains(worldNS, ".") {
		return nil, fmt.Errorf(
			"world namespace %q contains a dot; entity ids are positional, so this would add a position and "+
				"produce targets no world can contain", worldNS)
	}

	data, err := fs.ReadFile(scenarios, turnLoopScenariosPath)
	if err != nil {
		return nil, fmt.Errorf("read the embedded scenario pack: %w", err)
	}
	rebound := strings.Replace(TurnLoopInstancePrefix, "world1", worldNS, 1)
	if !strings.Contains(string(data), TurnLoopInstancePrefix) {
		return nil, fmt.Errorf(
			"the scenario pack contains no %q, so rebinding it to %q would be a silent no-op and every effect "+
				"would name an entity another world instance contains",
			TurnLoopInstancePrefix, worldNS)
	}
	return ParseFixture([]byte(strings.ReplaceAll(string(data), TurnLoopInstancePrefix, rebound)))
}

// BellweatherScenariosIn returns the nine-turn mystery acceptance pack rebound
// to one isolated world namespace.
func BellweatherScenariosIn(worldNS string) (*Fixture, error) {
	if worldNS == "" {
		return nil, fmt.Errorf("rebinding the Bellweather scenario pack needs a world namespace")
	}
	if strings.Contains(worldNS, ".") {
		return nil, fmt.Errorf("world namespace %q contains a dot", worldNS)
	}
	data, err := fs.ReadFile(scenarios, bellweatherScenariosPath)
	if err != nil {
		return nil, fmt.Errorf("read the embedded Bellweather scenario pack: %w", err)
	}
	rebound := strings.Replace(BellweatherInstancePrefix, "world1", worldNS, 1)
	if !strings.Contains(string(data), BellweatherInstancePrefix) {
		return nil, fmt.Errorf("the Bellweather scenario pack contains no %q", BellweatherInstancePrefix)
	}
	return ParseFixture([]byte(strings.ReplaceAll(string(data), BellweatherInstancePrefix, rebound)))
}
