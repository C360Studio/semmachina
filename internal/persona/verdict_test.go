package persona_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// arguments decodes a JSON literal into the shape the framework's model client
// actually hands an executor.
//
// A literal rather than a Go map, because the wire is where the fidelity is: the
// client decodes provider JSON, so every number arrives as a float64 and every
// absent field is absent rather than zero. A hand-built map[string]any with Go
// ints would test a shape no provider produces.
func arguments(t *testing.T, literal string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(literal), &decoded); err != nil {
		t.Fatalf("the test fixture is not valid JSON: %v", err)
	}
	return decoded
}

const validRollingVerdict = `{
  "scalars": {
    "plausibility": "plausible",
    "risk": "moderate",
    "consequence": "setback",
    "requires_roll": true
  },
  "modifiers": [
    {"source": "equipment", "value": 1, "note": "the crowbar bites on the winch housing"},
    {"source": "condition", "value": -1, "note": "Hollis is already watching him"}
  ],
  "bands": {
    "miss":    [{"type": "set_status", "target": "c360.semmachina.world1.starter.character.rook", "status": "restrained"}],
    "partial": [{"type": "set_attribute", "target": "c360.semmachina.world1.starter.character.rook", "attribute": "stamina", "value": 4}],
    "full":    [{"type": "move_entity", "target": "c360.semmachina.world1.starter.character.rook", "location": "c360.semmachina.world1.starter.scene.gatehouse"}]
  },
  "rationale": "The winch will move for a crowbar, but not quietly, and the sentry is three strides away."
}`

const validNoRollVerdict = `{
  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
  "modifiers": [],
  "bands": {"auto": [{"type": "add_relationship", "target": "c360.semmachina.world1.starter.character.rook", "relation": "carries", "object": "c360.semmachina.world1.starter.item.crowbar"}]},
  "rationale": "Nothing is at stake and the crowbar is at his feet."
}`

type verdictHarness struct {
	executor  *persona.VerdictExecutor
	artifacts *fakeArtifacts
	graph     *fakeGraph
	journal   *journal
	metadata  map[string]any
}

func newVerdictHarness(t *testing.T) *verdictHarness {
	t.Helper()
	j := &journal{}
	artifacts := newFakeArtifacts(j)
	store := newFakeGraph(j)
	store.seedTurn()
	executor, err := persona.NewVerdictExecutor(artifacts, store,
		persona.WithClock(func() time.Time { return testTime }))
	if err != nil {
		t.Fatalf("NewVerdictExecutor: %v", err)
	}
	return &verdictHarness{
		executor:  executor,
		artifacts: artifacts,
		graph:     store,
		journal:   j,
		metadata:  injected(t, persona.Adjudicator(), ""),
	}
}

func (h *verdictHarness) exit(t *testing.T, literal string) (agentic.ToolResult, error) {
	t.Helper()
	return h.executor.Execute(context.Background(),
		call(persona.VerdictToolName, arguments(t, literal), h.metadata))
}

// refuses asserts a rejection the model can act on AND that the world is
// untouched. The second half is the one that matters: a validator that rejects
// and still writes is a validator that has already lost.
func (h *verdictHarness) refuses(t *testing.T, literal string, wants ...string) agentic.ToolResult {
	t.Helper()
	result, err := h.exit(t, literal)
	if err != nil {
		t.Fatalf("a correctable refusal returned a Go error (%v); the loop would end the turn instead of "+
			"handing the model its correction", err)
	}
	if result.ErrorKind != agentic.ToolErrorInvalidArgs {
		t.Fatalf("the exit was answered with %q (%s), want %q so the loop feeds the correction back",
			result.ErrorKind, result.Error, agentic.ToolErrorInvalidArgs)
	}
	if result.StopLoop {
		t.Fatal("a rejected exit stopped the loop; the model never gets its correction")
	}
	for _, want := range wants {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("the correction is %q, and does not mention %q — the model cannot act on it",
				result.Error, want)
		}
	}
	if h.artifacts.verdictPuts != 0 {
		t.Fatalf("a rejected verdict was stored %d time(s)", h.artifacts.verdictPuts)
	}
	if h.graph.merges != 0 {
		t.Fatalf("a rejected verdict wrote %d triple set(s) to the graph", h.graph.merges)
	}
	return result
}

// ---------------------------------------------------------------- accepted

func TestVerdict_LandsFourScalarsAndOneReferenceAndNothingElse(t *testing.T) {
	h := newVerdictHarness(t)

	result, err := h.exit(t, validRollingVerdict)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != "" {
		t.Fatalf("a valid exit was refused: %s", result.Error)
	}
	if !result.StopLoop {
		t.Fatal("the terminal tool did not stop the loop; the persona would keep iterating after exiting")
	}

	stored := h.graph.entities[testTurnEntityID]
	got := map[string]any{}
	for _, triple := range stored.Triples {
		if _, duplicate := got[triple.Predicate]; duplicate {
			t.Fatalf("the turn holds two values for %s; every predicate here is single-valued and the merge "+
				"lane is what keeps it that way", triple.Predicate)
		}
		got[triple.Predicate] = triple.Object
	}

	want := map[string]any{
		vocabulary.TurnVerdictPlausibility.String(): "plausible",
		vocabulary.TurnVerdictRisk.String():         "moderate",
		vocabulary.TurnVerdictConsequence.String():  "setback",
		vocabulary.TurnVerdictRequiresRoll.String(): true,
		vocabulary.TurnVerdictRef.String():          h.artifacts.ref(t, vocabulary.TurnVerdictRef, testTurnID).String(),
	}
	if len(got) != len(want) {
		t.Fatalf("the verdict landed %d predicates (%v), want exactly the four rule-matched scalars and one "+
			"reference", len(got), sortedKeys(got))
	}
	for predicate, value := range want {
		if got[predicate] != value {
			t.Fatalf("%s landed as %#v, want %#v", predicate, got[predicate], value)
		}
	}

	// F6, stated as an assertion rather than a comment: the bulky, LLM-authored
	// half of a verdict is reachable and is NOT on the rule-matching surface.
	for _, triple := range stored.Triples {
		object := fmt.Sprint(triple.Object)
		for _, leaked := range []string{"crowbar", "winch", "restrained", "stamina", "equipment"} {
			if strings.Contains(object, leaked) && triple.Predicate != vocabulary.TurnVerdictRef.String() {
				t.Fatalf("%s carries %q; bands, modifiers and rationale travel by reference", triple.Predicate, object)
			}
		}
	}
}

func TestVerdict_StoresTheWholeJudgmentWithTheIdentityTheModelNeverSaw(t *testing.T) {
	h := newVerdictHarness(t)
	if _, err := h.exit(t, validRollingVerdict); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	ref := h.artifacts.ref(t, vocabulary.TurnVerdictRef, testTurnID)
	stored, err := h.artifacts.GetVerdict(context.Background(), ref)
	if err != nil {
		t.Fatalf("the verdict is not readable at the reference the turn carries: %v", err)
	}
	if stored.TurnID != testTurnID || stored.ActionID != testActionID || stored.SceneID != testSceneID {
		t.Fatalf("the stored verdict is filed under %s/%s/%s, want the injected identity",
			stored.TurnID, stored.ActionID, stored.SceneID)
	}
	if len(stored.Bands) != 3 || len(stored.Modifiers) != 2 || stored.Rationale == "" {
		t.Fatalf("the stored verdict lost its bulky half: %d bands, %d modifiers, rationale %q",
			len(stored.Bands), len(stored.Modifiers), stored.Rationale)
	}
}

func TestVerdict_StoresTheArtifactBeforeTheReferenceReachesTheGraph(t *testing.T) {
	h := newVerdictHarness(t)
	if _, err := h.exit(t, validRollingVerdict); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.journal.entries) != 2 {
		t.Fatalf("the exit performed %d writes (%v), want a put and a merge", len(h.journal.entries), h.journal.entries)
	}
	if !strings.HasPrefix(h.journal.entries[0], "put ") || !strings.HasPrefix(h.journal.entries[1], "merge ") {
		t.Fatalf("the write order was %v; a reference committed before its object is a turn the dice and the "+
			"applier cannot resolve, while an unreferenced object is garbage a retry overwrites", h.journal.entries)
	}
}

// D12: the adjudicator holds the roll gate. A verdict that declines the dice for
// an action the mapping would have sent to them is VALID and proceeds; the
// disagreement is recorded, not rejected. Rejecting it would make a two-axis
// lookup the thing that decides when the dice come out, which is precisely what
// a fiction-first engine exists not to do.
func TestVerdict_DisagreeingWithTheAdvisoryMappingIsAcceptedAndReported(t *testing.T) {
	h := newVerdictHarness(t)

	// plausible + moderate maps to "roll"; this verdict declines.
	declined := `{
	  "scalars": {"plausibility": "plausible", "risk": "moderate", "consequence": "setback", "requires_roll": false},
	  "bands": {"auto": [{"type": "set_status", "target": "c360.semmachina.world1.starter.character.rook", "status": "wounded"}]},
	  "rationale": "He is already inside the arc of the spear; there is nothing left for the dice to decide."
	}`
	advised, err := vocabulary.RequiresRoll(vocabulary.PlausibilityPlausible, vocabulary.RiskModerate)
	if err != nil {
		t.Fatalf("RequiresRoll: %v", err)
	}
	if !advised {
		t.Fatal("this case no longer disagrees with the mapping, so it is not testing what it claims")
	}

	result, err := h.exit(t, declined)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != "" {
		t.Fatalf("a verdict that declined the dice was refused: %s", result.Error)
	}
	if agrees, _ := result.Metadata["gate_agrees"].(bool); agrees {
		t.Fatal("the tool result reports the gate agreeing with the mapping; the divergence rate is the " +
			"measurement that says whether the persona's judgment tracks it")
	}
	if reported, _ := result.Metadata["requires_roll"].(bool); reported {
		t.Fatal("the tool result reports a roll the verdict declined")
	}

	// The engine never fabricates a band the adjudicator did not author.
	bands, _ := result.Metadata["declared_bands"].([]string)
	if !slices.Equal(bands, []string{string(vocabulary.BandAuto)}) {
		t.Fatalf("the accepted band set is %v, want exactly the auto band the verdict declared", bands)
	}
	if got := firstObject(t, h.graph.entities[testTurnEntityID], vocabulary.TurnVerdictRequiresRoll); got != false {
		t.Fatalf("the turn records requires-roll as %#v; the REPORTED gate is what the rule pack routes on", got)
	}
}

// The other half of "the disagreement SHALL be recorded": the operator-facing
// one. The comparison is derivable from the stored verdict forever, but a
// divergence nobody is told about is a quality and cost signal that only exists
// if somebody thinks to go looking, and the whole reason D12 gives the gate away
// is that the divergence RATE is the measurement that says whether giving it
// away was right.
func TestVerdict_WarnsOnlyWhenTheGateDisagreesWithTheMapping(t *testing.T) {
	cases := map[string]struct {
		literal  string
		wantWarn bool
	}{
		"a verdict that declines the dice the mapping would have called for": {
			literal: `{
			  "scalars": {"plausibility": "plausible", "risk": "moderate", "consequence": "setback", "requires_roll": false},
			  "bands": {"auto": []},
			  "rationale": "Nothing left for the dice to decide."
			}`,
			wantWarn: true,
		},
		"a verdict that agrees with it": {
			literal:  validRollingVerdict,
			wantWarn: false,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVerdictHarness(t)
			var logged bytes.Buffer
			executor, err := persona.NewVerdictExecutor(h.artifacts, h.graph,
				persona.WithClock(func() time.Time { return testTime }),
				persona.WithLogger(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{
					Level: slog.LevelWarn,
				}))))
			if err != nil {
				t.Fatalf("NewVerdictExecutor: %v", err)
			}

			result, err := executor.Execute(context.Background(),
				call(persona.VerdictToolName, arguments(t, testCase.literal), h.metadata))
			if err != nil || result.ErrorKind != "" {
				t.Fatalf("the verdict was not accepted: %v / %s", err, result.Error)
			}

			warned := strings.Contains(logged.String(), "roll gate disagrees")
			if warned != testCase.wantWarn {
				t.Fatalf("warned=%v, want %v; the log said: %q", warned, testCase.wantWarn, logged.String())
			}
			if testCase.wantWarn && !strings.Contains(logged.String(), testTurnID) {
				t.Fatalf("the warning does not name the turn it is about: %q", logged.String())
			}
		})
	}
}

// At-least-once delivery is the normal case, so a repeated exit must converge
// rather than accumulate. The derived content key and the replacing merge lane
// are what make it converge; this is the proof that both are in the path.
func TestVerdict_RepeatedExitConverges(t *testing.T) {
	h := newVerdictHarness(t)
	for range 3 {
		if _, err := h.exit(t, validRollingVerdict); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	stored := h.graph.entities[testTurnEntityID]
	for _, predicate := range append(vocabulary.VerdictScalarPredicates(), vocabulary.TurnVerdictRef) {
		if got := objectsFor(stored, predicate); len(got) != 1 {
			t.Fatalf("after three exits the turn holds %d values for %s: %v", len(got), predicate, got)
		}
	}
	if len(h.artifacts.verdicts) != 1 {
		t.Fatalf("three exits stored %d verdicts; the key is derived from the turn so a repeat re-puts "+
			"identically", len(h.artifacts.verdicts))
	}
}

// ---------------------------------------------------------------- refused

func TestVerdict_RefusesAnOutOfVocabularyClass(t *testing.T) {
	h := newVerdictHarness(t)
	h.refuses(t, `{
	  "scalars": {"plausibility": "vibes", "risk": "moderate", "consequence": "setback", "requires_roll": true},
	  "bands": {"miss": [], "partial": [], "full": []},
	  "rationale": "A class nobody registered, asserted with total confidence."
	}`, "vibes", string(vocabulary.PlausibilityPlausible))
}

func TestVerdict_RefusesAnOutOfVocabularyEffectType(t *testing.T) {
	h := newVerdictHarness(t)
	// This is the shipped scenario pack's `invalid-effect` step: an effect type
	// nobody registered. It is refused HERE, at the tool boundary, and never
	// reaches the applier — the content store will not store a verdict that
	// cannot pass its own contract, so deferring the check would mean either
	// storing an invalid artifact or failing the turn on something the model
	// could have corrected in one iteration.
	h.refuses(t, `{
	  "scalars": {"plausibility": "plausible", "risk": "low", "consequence": "complication", "requires_roll": false},
	  "bands": {"auto": [{"type": "teleport_entity", "target": "c360.semmachina.world1.starter.character.rook", "location": "c360.semmachina.world1.starter.scene.gatehouse"}]},
	  "rationale": "An effect type nobody registered."
	}`, "teleport_entity")
}

// The shipped pack's `malformed-exit` step 0: a verdict that calls for the dice
// and then declares the band set of a turn that did not. No JSON Schema can
// express that constraint — it is a relationship between two fields — which is
// exactly why the tool boundary rather than provider strict-mode is the
// enforcement of record.
func TestVerdict_RefusesABandSetItsOwnRollGateDoesNotCallFor(t *testing.T) {
	h := newVerdictHarness(t)
	h.refuses(t, `{
	  "scalars": {"plausibility": "unlikely", "risk": "high", "consequence": "harm", "requires_roll": true},
	  "bands": {"auto": [{"type": "set_status", "target": "c360.semmachina.world1.starter.character.rook", "status": "wounded"}]},
	  "rationale": "A verdict that calls for the dice and declares the band set of a turn that did not."
	}`, string(vocabulary.BandMiss))

	// And the mirror: a no-roll verdict that declares the three roll bands.
	h2 := newVerdictHarness(t)
	h2.refuses(t, `{
	  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
	  "bands": {"miss": [], "partial": [], "full": []},
	  "rationale": "Declining the dice and declaring their bands anyway."
	}`, string(vocabulary.BandAuto))
}

// Omitting the roll gate and declining the dice are DIFFERENT verdicts with
// opposite band sets and opposite costs. A plain bool would make them the same
// value, so the argument type carries a pointer and this is the test that says
// why.
func TestVerdict_RefusesAnOmittedRollGateRatherThanReadingItAsDeclined(t *testing.T) {
	h := newVerdictHarness(t)
	result := h.refuses(t, `{
	  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none"},
	  "bands": {"auto": []},
	  "rationale": "No roll gate at all."
	}`, "requires_roll")
	if !strings.Contains(result.Error, "declining") {
		t.Fatalf("the correction is %q; it must say that omitting the gate and declining the dice are "+
			"different verdicts, or the model will read the refusal as a formatting complaint", result.Error)
	}
}

// The framework client substitutes an EMPTY argument map for a tool call whose
// arguments are not valid JSON, and drops tool calls entirely from a truncated
// response. So what a malformed or cut-off exit looks like from here is not
// broken text — it is nothing at all, and the correction has to make sense on
// its own.
func TestVerdict_RefusesTheEmptyArgumentMapAMalformedCallArrivesAs(t *testing.T) {
	h := newVerdictHarness(t)
	h.refuses(t, `{}`, "cut off")
}

func TestVerdict_RefusesAFieldNobodyRegistered(t *testing.T) {
	cases := map[string]string{
		// The identity contract, from the other side: a model that supplies an
		// identifier is told no rather than having it silently overridden, so
		// "the engine used its own" is never a thing the model believed
		// otherwise.
		"an identifier the engine already knows": `{
		  "turn_id": "turn-act-9",
		  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
		  "bands": {"auto": []},
		  "rationale": "Echoing a turn id it was never given."
		}`,
		"an effect smuggled outside the bands": `{
		  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
		  "bands": {"auto": []},
		  "effects": [{"type": "set_status", "target": "c360.semmachina.world1.starter.character.rook", "status": "dead"}],
		  "rationale": "A change proposed under a name nobody reads."
		}`,
		"a field inside an intent": `{
		  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
		  "bands": {"auto": [{"type": "set_status", "target": "c360.semmachina.world1.starter.character.rook", "status": "wounded", "permanent": true}]},
		  "rationale": "An intent carrying a field the vocabulary does not have."
		}`,
	}
	for name, literal := range cases {
		t.Run(name, func(t *testing.T) {
			newVerdictHarness(t).refuses(t, literal)
		})
	}
}

// Modifiers are bounded so a verdict cannot pre-determine the band — the dice
// have to be able to reach every outcome the verdict declared, or seeded
// determinism is decorating a result the persona already fixed.
func TestVerdict_RefusesModifiersThatPutABandOutOfReach(t *testing.T) {
	h := newVerdictHarness(t)
	h.refuses(t, `{
	  "scalars": {"plausibility": "plausible", "risk": "moderate", "consequence": "setback", "requires_roll": true},
	  "modifiers": [{"source": "trait", "value": 3}, {"source": "equipment", "value": 3}],
	  "bands": {"miss": [], "partial": [], "full": []},
	  "rationale": "Six points of help, which makes a miss unreachable."
	}`)
}

func TestVerdict_RefusesAnAttributeOutsideItsRegisteredBounds(t *testing.T) {
	h := newVerdictHarness(t)
	h.refuses(t, `{
	  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
	  "bands": {"auto": [{"type": "set_attribute", "target": "c360.semmachina.world1.starter.character.rook", "attribute": "health", "value": 9000}]},
	  "rationale": "Nine thousand health."
	}`, "health")
}

// ------------------------------------------------------- engine-side failures

// A spawn that did not stamp its identity is not something the model can fix, so
// it must not be handed back as a correction: the persona would spend its whole
// budget re-emitting a verdict that was right the first time.
func TestVerdict_TreatsAMissingInjectedIdentityAsAnEngineFailure(t *testing.T) {
	h := newVerdictHarness(t)
	h.metadata = map[string]any{persona.MetadataKeyTurnID: testTurnID}

	result, err := h.exit(t, validRollingVerdict)
	if err == nil {
		t.Fatal("a spawn with no injected identity was reported as an ordinary tool outcome")
	}
	if result.ErrorKind != agentic.ToolErrorInternal {
		t.Fatalf("the failure is %q, want %q — the model cannot correct a spawn",
			result.ErrorKind, agentic.ToolErrorInternal)
	}
	if h.artifacts.verdictPuts != 0 || h.graph.merges != 0 {
		t.Fatal("a verdict with no turn to belong to was written somewhere")
	}
}

func TestVerdict_ReportsAStoreFailureAsTransientAndWritesNoReference(t *testing.T) {
	h := newVerdictHarness(t)
	h.artifacts.verdictErr = errors.New("object store unreachable")

	result, err := h.exit(t, validRollingVerdict)
	if err == nil {
		t.Fatal("a store failure was reported as success")
	}
	if result.ErrorKind != agentic.ToolErrorNetwork {
		t.Fatalf("the failure is %q, want %q", result.ErrorKind, agentic.ToolErrorNetwork)
	}
	if h.graph.merges != 0 {
		t.Fatal("the reference was committed for a verdict that was never stored; that is the correctness " +
			"bug the write ordering exists to prevent")
	}
}

func TestVerdict_ReportsAGraphFailureAsTransientAndLeavesTheArtifactOrphaned(t *testing.T) {
	h := newVerdictHarness(t)
	h.graph.mergeErr = errors.New("graph-ingest unreachable")

	result, err := h.exit(t, validRollingVerdict)
	if err == nil {
		t.Fatal("a graph failure was reported as success")
	}
	if result.ErrorKind != agentic.ToolErrorNetwork {
		t.Fatalf("the failure is %q, want %q", result.ErrorKind, agentic.ToolErrorNetwork)
	}
	// The survivable half of the ordering: the verdict is stored and nothing
	// points at it, so a retry re-puts identically and commits the reference.
	if len(h.artifacts.verdicts) != 1 {
		t.Fatalf("the verdict is not recoverable: %d stored", len(h.artifacts.verdicts))
	}
	if got := objectsFor(h.graph.entities[testTurnEntityID], vocabulary.TurnVerdictRef); len(got) != 0 {
		t.Fatalf("the turn carries %v after a failed merge", got)
	}
}

func TestVerdict_RefusesAToolThatIsNotItsOwn(t *testing.T) {
	h := newVerdictHarness(t)
	result, err := h.executor.Execute(context.Background(),
		call(persona.NarrationToolName, arguments(t, validRollingVerdict), h.metadata))
	if err == nil {
		t.Fatal("a foreign tool name was executed")
	}
	if result.ErrorKind != agentic.ToolErrorNotFound {
		t.Fatalf("the failure is %q, want %q", result.ErrorKind, agentic.ToolErrorNotFound)
	}
}

// ------------------------------------------------------------------ shapes

// A no-roll verdict is the other half of the band contract, and it has to be
// accepted as readily as a rolling one — D12's "the engine never fabricates a
// band the adjudicator did not author" is only meaningful if the single-band
// shape is first-class.
func TestVerdict_AcceptsTheSingleAutoBandOfAVerdictThatDeclinedTheDice(t *testing.T) {
	h := newVerdictHarness(t)
	result, err := h.exit(t, validNoRollVerdict)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != "" {
		t.Fatalf("a no-roll verdict was refused: %s", result.Error)
	}

	var stored payload.Verdict
	if err := json.Unmarshal([]byte(result.Content), &stored); err != nil {
		t.Fatalf("the loop result is not a verdict: %v", err)
	}
	if len(stored.Bands) != 1 {
		t.Fatalf("the accepted verdict carries %d bands", len(stored.Bands))
	}
	if _, ok := stored.Bands[vocabulary.BandAuto]; !ok {
		t.Fatalf("the accepted verdict's single band is not %q", vocabulary.BandAuto)
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}
