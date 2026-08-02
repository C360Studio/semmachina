package mockmodel_test

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/mockmodel"
	"github.com/c360studio/semmachina/internal/payload"
)

// The scenarios the end-to-end suite plays are named here so that deleting one
// is a failing test rather than a case that quietly stopped running. Each name
// corresponds to a case the turn loop has to survive.
var requiredScenarios = []string{
	"no-roll",
	"miss",
	"partial",
	"full",
	"invalid-effect",
	"out-of-vocabulary-effect",
	"malformed-exit",
	"duplicate-delivery",
	"crash-resume",
	"never-terminates",
	"prose-instead-of-exit",
}

func TestTurnLoopScenarios_ShipEveryCaseTheSuiteNeeds(t *testing.T) {
	fixture, err := mockmodel.TurnLoopScenarios()
	if err != nil {
		t.Fatalf("the shipped scenario pack does not load: %v", err)
	}

	names := fixture.ScenarioNames()
	for _, want := range requiredScenarios {
		if !slices.Contains(names, want) {
			t.Fatalf("the pack declares %v, which is missing the %q scenario", names, want)
		}
	}

	// Every scenario must be startable, which is also the cheapest proof that
	// the pack and the stub agree about role names.
	for _, name := range names {
		if _, err := mockmodel.New(fixture, name); err != nil {
			t.Fatalf("scenario %q cannot be started: %v", name, err)
		}
	}
}

func TestBellweatherScenarios_ShipTheNineTurnAcceptanceBudget(t *testing.T) {
	fixture, err := mockmodel.BellweatherScenariosIn("bellweather-e2e")
	if err != nil {
		t.Fatalf("load Bellweather scenario pack: %v", err)
	}
	scenario, ok := fixture.Scenario("bellweather-acceptance")
	if !ok {
		t.Fatalf("Bellweather pack scenarios = %v, want bellweather-acceptance", fixture.ScenarioNames())
	}
	wantSteps := map[string]int{"casekeeper": 9, "adjudicator": 9, "narrator": 9}
	for _, script := range scenario.Scripts {
		want, declared := wantSteps[script.Role]
		if !declared {
			t.Fatalf("Bellweather pack scripts unexpected model role %q", script.Role)
		}
		if len(script.Steps) != want {
			t.Fatalf("Bellweather role %s has %d steps, want %d", script.Role, len(script.Steps), want)
		}
		delete(wantSteps, script.Role)
	}
	if len(wantSteps) != 0 {
		t.Fatalf("Bellweather pack is missing role budgets %v", wantSteps)
	}
	var casekeeper *mockmodel.Script
	for index := range scenario.Scripts {
		if scenario.Scripts[index].Role == "casekeeper" {
			casekeeper = &scenario.Scripts[index]
			break
		}
	}
	if casekeeper == nil || len(casekeeper.Steps[3].ToolCalls) != 1 {
		t.Fatal("Bellweather share turn is missing its single case decision")
	}
	var share struct {
		Kind       payload.CaseDecisionKind `json:"kind"`
		TargetRefs []string                 `json:"target_refs"`
		RevealRefs []string                 `json:"reveal_refs"`
	}
	if err := json.Unmarshal(casekeeper.Steps[3].ToolCalls[0].Arguments, &share); err != nil {
		t.Fatalf("decode Bellweather share turn: %v", err)
	}
	prefix := "c360.semmachina.bellweather-e2e.bellweather-maze."
	if share.Kind != payload.CaseDecisionShare ||
		!slices.Equal(share.TargetRefs, []string{prefix + "character.kit-finch"}) ||
		!slices.Equal(share.RevealRefs, []string{prefix + "evidence.evidence-sedative"}) {
		t.Fatalf("Bellweather share turn = %#v; want Rowan's newly learned sedative evidence shared with Kit", share)
	}
	for _, forbidden := range []string{"submit_companion_decision", "solution-canary"} {
		for _, script := range scenario.Scripts {
			for _, step := range script.Steps {
				encoded, marshalErr := json.Marshal(step)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("explicit-hint pack spends or fabricates forbidden %q", forbidden)
				}
			}
		}
	}
}

// The pack is data for a contract defined elsewhere, and the failure mode that
// costs the most is a scripted verdict that no executor could ever accept. So
// every scripted verdict is decoded into the canonical payload and validated
// by production code — with the identity fields supplied the way the executor
// supplies them.
//
// That last part is the finding this test exists to record: turn, action, and
// scene identifiers are ENGINE knowledge. A terminal-tool schema that asked
// the model to echo them could not be scripted deterministically and would be
// hallucinated by a live model, so they are injected at the tool boundary and
// deliberately absent from every scripted argument set here.
func TestTurnLoopScenarios_ScriptVerdictsProductionCodeAccepts(t *testing.T) {
	fixture, err := mockmodel.TurnLoopScenarios()
	if err != nil {
		t.Fatalf("load the pack: %v", err)
	}

	const (
		sceneID = "c360.semmachina.world1.starter.scene.gatehouse"
		turnID  = "turn-mock-1"
	)
	// The steps expected to FAIL validation, named individually: a scenario
	// that scripts a rejection and then its correction contains both, so
	// marking the whole scenario invalid would stop checking the correction —
	// which is the half that has to be right.
	//
	// `invalid-effect` is deliberately NOT here any more, and its absence is now
	// the assertion. That scenario's whole job is to reach the APPLIER, so its
	// verdict has to pass every gate this walk applies: the payload validator is
	// world-blind, and "a character moved into an item" is a fact about the graph.
	// A verdict this walk could refuse would be refused at the tool boundary too,
	// and would never produce an applier rejection at all.
	rejected := map[string]map[int]bool{
		"out-of-vocabulary-effect": {0: true},
		"malformed-exit":           {0: true},
	}
	checked, refusals := 0, 0

	for _, name := range fixture.ScenarioNames() {
		scenario, _ := fixture.Scenario(name)
		for _, script := range scenario.Scripts {
			for stepIndex, step := range script.Steps {
				for _, call := range step.ToolCalls {
					if call.Name != "submit_verdict" {
						continue
					}
					checked++
					verdict := payload.Verdict{TurnID: turnID, ActionID: "act-mock-1", SceneID: sceneID}
					if err := json.Unmarshal(call.Arguments, &verdict); err != nil {
						t.Fatalf("scenario %q step %d: the scripted arguments do not decode into a verdict: %v",
							name, stepIndex, err)
					}
					err := verdict.Validate()
					mustFail := rejected[name][stepIndex]
					switch {
					case mustFail && err == nil:
						t.Fatalf("scenario %q step %d is supposed to be something the engine refuses, and "+
							"the engine accepted it", name, stepIndex)
					case mustFail:
						refusals++
					case err != nil:
						t.Fatalf("scenario %q step %d scripts a verdict production code rejects: %v",
							name, stepIndex, err)
					}
				}
			}
		}
	}

	// Anti-vacuity, in both directions: a walk that found no verdicts would
	// pass, and so would one that never reached the scenario whose entire
	// purpose is to be refused.
	if checked == 0 {
		t.Fatal("no scripted verdicts were checked; the walk is looking at the wrong tool name")
	}
	if refusals == 0 {
		t.Fatal("the scenario that must fail validation was never reached")
	}
}

// Effect targets are six-part entity IDs, so they name entities in one
// particular world instance. A typo there produces a turn that fails on a
// missing target for reasons that look like an engine bug, so the instance
// segment of every scripted target is checked against the starter world
// package that ships beside it.
func TestTurnLoopScenarios_TargetEntitiesTheStarterWorldActuallyContains(t *testing.T) {
	fixture, err := mockmodel.TurnLoopScenarios()
	if err != nil {
		t.Fatalf("load the pack: %v", err)
	}
	known := starterWorldLocalIDs(t)

	targets := 0
	for _, name := range fixture.ScenarioNames() {
		scenario, _ := fixture.Scenario(name)
		for _, script := range scenario.Scripts {
			for _, step := range script.Steps {
				for _, call := range step.ToolCalls {
					for _, id := range entityIDsIn(t, call.Arguments) {
						targets++
						parts := strings.Split(id, ".")
						if len(parts) != 6 {
							t.Fatalf("scenario %q references %q, which is not a six-part entity ID", name, id)
						}
						if !known[parts[5]] {
							t.Fatalf("scenario %q targets %q, and the starter world has no %q; the pack and "+
								"the world package have drifted apart", name, id, parts[5])
						}
					}
				}
			}
		}
	}
	if targets == 0 {
		t.Fatal("no entity references were found in the pack; the walk is looking at the wrong fields")
	}
}

// The rebinding an isolated end-to-end run depends on: every effect target moves
// to the caller's world namespace, and NOTHING else about the pack changes.
//
// The second half is the one worth testing. A substitution wide enough to catch
// the targets is wide enough to catch prose, and a pack whose narration silently
// mentioned the test's namespace would still pass every assertion about effects.
func TestTurnLoopScenariosIn_RebindsTargetsAndOnlyTargets(t *testing.T) {
	rebound, err := mockmodel.TurnLoopScenariosIn("e2eprobe")
	if err != nil {
		t.Fatalf("rebind the pack: %v", err)
	}
	shipped, err := mockmodel.TurnLoopScenarios()
	if err != nil {
		t.Fatalf("load the shipped pack: %v", err)
	}

	targets := 0
	for _, name := range rebound.ScenarioNames() {
		scenario, _ := rebound.Scenario(name)
		for _, script := range scenario.Scripts {
			for _, step := range script.Steps {
				for _, call := range step.ToolCalls {
					for _, id := range entityIDsIn(t, call.Arguments) {
						targets++
						if want := "c360.semmachina.e2eprobe.starter."; !strings.HasPrefix(id, want) {
							t.Errorf("scenario %q still targets %q, which is not under %q", name, id, want)
						}
					}
				}
			}
		}
	}
	if targets == 0 {
		t.Fatal("the rebound pack declares no effect targets; the walk is looking at the wrong fields")
	}

	// Prose is untouched, checked against the shipped pack rather than against a
	// literal, so this stays true when somebody rewrites the fiction.
	if got, want := prose(t, rebound), prose(t, shipped); !slices.Equal(got, want) {
		t.Errorf("rebinding changed the narrator's prose:\n got %q\nwant %q", got, want)
	}
	if len(prose(t, shipped)) == 0 {
		t.Fatal("the shipped pack scripts no prose, so the comparison above compares two empty lists")
	}
}

// A namespace that would corrupt an entity id, and a pack the substitution could
// not find, are both refusals rather than a quiet pass-through: either one leaves
// every effect naming an entity the caller's world does not contain.
func TestTurnLoopScenariosIn_RefusesWhatWouldSilentlyDoNothing(t *testing.T) {
	for _, ns := range []string{"", "two.parts"} {
		if _, err := mockmodel.TurnLoopScenariosIn(ns); err == nil {
			t.Errorf("the pack rebound to %q; every effect would name an unreachable entity", ns)
		}
	}
	if !strings.Contains(string(mustReadPack(t)), mockmodel.TurnLoopInstancePrefix) {
		t.Fatalf("the shipped pack no longer contains %q, so TurnLoopScenariosIn is a no-op and its refusal "+
			"is the only thing standing between an e2e run and a world full of missing targets",
			mockmodel.TurnLoopInstancePrefix)
	}
}

// helpers ------------------------------------------------------------------

// prose returns every scripted narration string in declaration order.
func prose(t *testing.T, fixture *mockmodel.Fixture) []string {
	t.Helper()
	var out []string
	for _, name := range fixture.ScenarioNames() {
		scenario, _ := fixture.Scenario(name)
		for _, script := range scenario.Scripts {
			for _, step := range script.Steps {
				for _, call := range step.ToolCalls {
					var args struct {
						Prose string `json:"prose"`
					}
					if err := json.Unmarshal(call.Arguments, &args); err == nil && args.Prose != "" {
						out = append(out, args.Prose)
					}
				}
			}
		}
	}
	return out
}

// mustReadPack reads the pack's bytes the way the loader does, through the
// repository rather than through the embed, so the anti-vacuity check above is
// about the file somebody edits.
func mustReadPack(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("scenarios/turnloop.json")
	if err != nil {
		t.Fatalf("read the scenario pack: %v", err)
	}
	return data
}

// entityIDsIn pulls every value the effect vocabulary treats as an entity
// reference out of a scripted argument set.
func entityIDsIn(t *testing.T, arguments json.RawMessage) []string {
	t.Helper()
	if len(arguments) == 0 {
		return nil
	}
	var decoded struct {
		Bands map[string][]struct {
			Target   string `json:"target"`
			Object   string `json:"object"`
			Location string `json:"location"`
		} `json:"bands"`
	}
	if err := json.Unmarshal(arguments, &decoded); err != nil {
		// Not a verdict-shaped argument set (a narration, say); nothing to check.
		return nil
	}
	// Bands are walked in sorted order so a failure names the same band every
	// run rather than whichever one Go's map iteration reached first.
	bands := make([]string, 0, len(decoded.Bands))
	for band := range decoded.Bands {
		bands = append(bands, band)
	}
	slices.Sort(bands)

	var out []string
	for _, band := range bands {
		for _, intent := range decoded.Bands[band] {
			for _, value := range []string{intent.Target, intent.Object, intent.Location} {
				if value != "" {
					out = append(out, value)
				}
			}
		}
	}
	return out
}

func starterWorldLocalIDs(t *testing.T) map[string]bool {
	t.Helper()
	world, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("open the starter world: %v", err)
	}
	file, err := world.Open("entities.jsonl")
	if err != nil {
		t.Fatalf("open entities.jsonl: %v", err)
	}
	defer file.Close()

	ids := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entity struct {
			LocalID string `json:"local_id"`
		}
		if err := json.Unmarshal([]byte(line), &entity); err != nil {
			t.Fatalf("decode a starter-world line: %v", err)
		}
		ids[entity.LocalID] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read entities.jsonl: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("the starter world package yielded no entities")
	}
	return ids
}
