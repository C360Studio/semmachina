package persona_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type narrationHarness struct {
	executor  *persona.NarrationExecutor
	artifacts *fakeArtifacts
	graph     *fakeGraph
	journal   *journal
	metadata  map[string]any
}

func newNarrationHarness(t *testing.T, band vocabulary.OutcomeBand) *narrationHarness {
	t.Helper()
	j := &journal{}
	artifacts := newFakeArtifacts(j)
	store := newFakeGraph(j)
	// The world as the narrator finds it: the outcome is already committed.
	store.seedTurn(
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseNarrating)),
		fact(vocabulary.TurnRollBand, string(band)),
		fact(vocabulary.TurnEffectsBatch, "batch-"+testTurnID),
	)
	executor, err := persona.NewNarrationExecutor(artifacts, store,
		persona.WithClock(func() time.Time { return testTime }))
	if err != nil {
		t.Fatalf("NewNarrationExecutor: %v", err)
	}
	return &narrationHarness{
		executor:  executor,
		artifacts: artifacts,
		graph:     store,
		journal:   j,
		metadata:  injected(t, persona.Narrator(), band),
	}
}

func fact(predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{
		Subject:   testTurnEntityID,
		Predicate: predicate.String(),
		Object:    object,
		Source:    "test",
		Timestamp: testTime,
	}
}

func (h *narrationHarness) exit(t *testing.T, literal string) (agentic.ToolResult, error) {
	t.Helper()
	return h.executor.Execute(context.Background(),
		call(persona.NarrationToolName, arguments(t, literal), h.metadata))
}

const validNarration = `{"prose": "The gate lifts a hand's width and stops, and Rook is breathing hard through it. Wren does not look up from the winch, which is its own kind of answer."}`

// ---------------------------------------------------------------- accepted

func TestNarration_LandsOnePointerAtProseAndNothingElse(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandPartial)

	before := len(h.graph.entities[testTurnEntityID].Triples)
	result, err := h.exit(t, validNarration)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != "" {
		t.Fatalf("a valid narration was refused: %s", result.Error)
	}
	if !result.StopLoop {
		t.Fatal("the terminal tool did not stop the loop")
	}

	stored := h.graph.entities[testTurnEntityID]
	if len(stored.Triples) != before+1 {
		t.Fatalf("the narration added %d triples; it may add exactly one, the pointer at its prose",
			len(stored.Triples)-before)
	}
	ref := h.artifacts.ref(t, vocabulary.TurnNarrationRef, testTurnID)
	if got := firstObject(t, stored, vocabulary.TurnNarrationRef); got != ref.String() {
		t.Fatalf("the turn records %#v for %s, want %s", got, vocabulary.TurnNarrationRef, ref)
	}

	// The prose itself never reaches the graph. Everything on the turn is a
	// closed-vocabulary scalar or a pointer; a rule that could see the prose
	// would be a rule that can branch on fiction.
	for _, triple := range stored.Triples {
		if object, ok := triple.Object.(string); ok && strings.Contains(object, "Wren") {
			t.Fatalf("%s carries narration prose: %q", triple.Predicate, object)
		}
	}
}

// The whole crash-safety argument of this stage, and it is invisible in any
// end-state assertion: after a successful run the world looks identical either
// way. Only the write ORDER distinguishes a recoverable crash from a turn that
// tells the player it finished and has nothing to show them.
func TestNarration_WritesProseBeforeTheReference(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandFull)
	if _, err := h.exit(t, validNarration); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.journal.entries) != 2 {
		t.Fatalf("the exit performed %d writes (%v), want a put and a merge",
			len(h.journal.entries), h.journal.entries)
	}
	if !strings.HasPrefix(h.journal.entries[0], "put ") || !strings.HasPrefix(h.journal.entries[1], "merge ") {
		t.Fatalf("the write order was %v; a reference to missing prose is a correctness bug and an "+
			"unreferenced object is only garbage, so the order is fixed rather than convenient",
			h.journal.entries)
	}
}

// The band is engine knowledge — the dice chose it, or the verdict declined
// them — so the stored narration records the band it was TOLD to voice. A
// narration filed under a band it does not voice is exactly the drift this
// engine exists to make detectable, and it has to be detectable after the fact.
func TestNarration_RecordsTheBandTheEngineToldItToVoice(t *testing.T) {
	for _, band := range vocabulary.OutcomeBands() {
		t.Run(string(band), func(t *testing.T) {
			h := newNarrationHarness(t, band)
			if _, err := h.exit(t, validNarration); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			ref := h.artifacts.ref(t, vocabulary.TurnNarrationRef, testTurnID)
			stored := h.artifacts.narrations[ref.Key]
			if stored == nil {
				t.Fatalf("nothing stored at %s", ref)
			}
			if stored.Band != band {
				t.Fatalf("the narration is filed under band %q, want %q", stored.Band, band)
			}
			if stored.TurnID != testTurnID {
				t.Fatalf("the narration is filed under turn %q", stored.TurnID)
			}
		})
	}
}

func TestNarration_RepeatedExitConverges(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandMiss)
	for range 3 {
		if _, err := h.exit(t, validNarration); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	if got := objectsFor(h.graph.entities[testTurnEntityID], vocabulary.TurnNarrationRef); len(got) != 1 {
		t.Fatalf("after three exits the turn holds %d narration references: %v", len(got), got)
	}
	if len(h.artifacts.narrations) != 1 {
		t.Fatalf("three exits stored %d narrations", len(h.artifacts.narrations))
	}
}

// ---------------------------------------------------------------- refused

// This is the tool an over-helpful narrator would try to propose an effect
// through, and the answer has to be a refusal rather than a silent drop: a
// change the fiction promised and the world never received is the shape where
// narration and state quietly disagree.
func TestNarration_RefusesAnythingThatIsNotProse(t *testing.T) {
	cases := map[string]string{
		"a proposed effect":              `{"prose": "The gate gives.", "effects": [{"type": "set_status", "target": "c360.semmachina.world1.starter.character.rook", "status": "dead"}]}`,
		"a band it was not asked for":    `{"prose": "The gate gives.", "band": "full"}`,
		"an identifier the engine knows": `{"prose": "The gate gives.", "turn_id": "turn-act-9"}`,
		"a salience mark nobody reads":   `{"prose": "The gate gives.", "salience": "high"}`,
	}
	for name, literal := range cases {
		t.Run(name, func(t *testing.T) {
			h := newNarrationHarness(t, vocabulary.BandFull)
			result, err := h.exit(t, literal)
			if err != nil {
				t.Fatalf("a correctable refusal returned a Go error: %v", err)
			}
			if result.ErrorKind != agentic.ToolErrorInvalidArgs {
				t.Fatalf("the exit was answered with %q (%s), want %q",
					result.ErrorKind, result.Error, agentic.ToolErrorInvalidArgs)
			}
			if h.artifacts.narrationPuts != 0 || h.graph.merges != 0 {
				t.Fatal("a refused narration wrote something")
			}
			if !strings.Contains(result.Error, "prose") {
				t.Fatalf("the correction is %q; it must say what this tool actually takes", result.Error)
			}
		})
	}
}

func TestNarration_RefusesAnEmptyOrAbsentProse(t *testing.T) {
	for name, literal := range map[string]string{
		"absent": `{}`,
		"empty":  `{"prose": ""}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newNarrationHarness(t, vocabulary.BandFull)
			result, err := h.exit(t, literal)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.ErrorKind != agentic.ToolErrorInvalidArgs {
				t.Fatalf("the exit was answered with %q, want a correction", result.ErrorKind)
			}
			if h.graph.merges != 0 {
				t.Fatal("the turn was told a narration exists that does not; the player would be shown nothing")
			}
		})
	}
}

func TestNarration_RefusesProseBeyondItsBudget(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandFull)
	oversized, err := json.Marshal(map[string]string{"prose": strings.Repeat("a", content.MaxProseBytes+1)})
	if err != nil {
		t.Fatalf("build the oversized fixture: %v", err)
	}
	result, execErr := h.exit(t, string(oversized))
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	if result.ErrorKind != agentic.ToolErrorInvalidArgs {
		t.Fatalf("prose past the budget was answered with %q", result.ErrorKind)
	}
	if h.artifacts.narrationPuts != 0 {
		t.Fatal("oversized prose was stored")
	}
}

// ------------------------------------------------------- engine-side failures

func TestNarration_TreatsAMissingBandAsAnEngineFailure(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandFull)
	delete(h.metadata, persona.MetadataKeyBand)

	result, err := h.exit(t, validNarration)
	if err == nil {
		t.Fatal("a narrator spawned without the band it is voicing produced a narration anyway")
	}
	if result.ErrorKind != agentic.ToolErrorInternal {
		t.Fatalf("the failure is %q, want %q — the model cannot correct a spawn",
			result.ErrorKind, agentic.ToolErrorInternal)
	}
	if h.artifacts.narrationPuts != 0 || h.graph.merges != 0 {
		t.Fatal("a narration with no band to be filed under was written somewhere")
	}
}

func TestNarration_LeavesTheProseRecoverableWhenTheReferenceCannotBeCommitted(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandPartial)
	h.graph.mergeErr = errors.New("graph-ingest unreachable")

	result, err := h.exit(t, validNarration)
	if err == nil {
		t.Fatal("a graph failure was reported as success")
	}
	if result.ErrorKind != agentic.ToolErrorNetwork {
		t.Fatalf("the failure is %q, want %q", result.ErrorKind, agentic.ToolErrorNetwork)
	}
	if len(h.artifacts.narrations) != 1 {
		t.Fatal("the prose was not stored, so the retry has to regenerate it — a second billed call for words " +
			"that were already written")
	}
	if got := objectsFor(h.graph.entities[testTurnEntityID], vocabulary.TurnNarrationRef); len(got) != 0 {
		t.Fatalf("the turn carries %v after a failed merge; a reference to prose nobody can find is worse "+
			"than no reference", got)
	}
}

func TestNarration_RefusesAToolThatIsNotItsOwn(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandFull)
	result, err := h.executor.Execute(context.Background(),
		call(persona.VerdictToolName, arguments(t, validNarration), h.metadata))
	if err == nil {
		t.Fatal("a foreign tool name was executed")
	}
	if result.ErrorKind != agentic.ToolErrorNotFound {
		t.Fatalf("the failure is %q, want %q", result.ErrorKind, agentic.ToolErrorNotFound)
	}
}

// The narration spec's second scenario, made concrete: the narrator executes and
// the turn's applied effects are identical before and after. Its tool cannot
// mutate the world, so the only thing it may add to the graph is its own
// pointer.
func TestNarration_ChangesNothingTheApplierCommitted(t *testing.T) {
	h := newNarrationHarness(t, vocabulary.BandPartial)
	// A committed effect on a world entity, beside the turn.
	h.graph.entities[testCharacterID] = &graph.EntityState{
		ID:          testCharacterID,
		MessageType: message.Type{Domain: "semmachina", Category: "world_entity", Version: "v1"},
		Triples: []message.Triple{{
			Subject:   testCharacterID,
			Predicate: vocabulary.CharacterStatusCurrent.String(),
			Object:    string(vocabulary.StatusWounded),
		}},
	}
	before := len(h.graph.entities[testCharacterID].Triples)

	if _, err := h.exit(t, validNarration); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	after := h.graph.entities[testCharacterID]
	if len(after.Triples) != before {
		t.Fatalf("the world entity carries %d facts after narration, want the %d it had before",
			len(after.Triples), before)
	}
	if got := firstObject(t, after, vocabulary.CharacterStatusCurrent); got != string(vocabulary.StatusWounded) {
		t.Fatalf("narration changed a committed effect to %#v", got)
	}
	if h.graph.merges != 1 {
		t.Fatalf("the narration issued %d merges; it writes to the turn and to nothing else", h.graph.merges)
	}
}
