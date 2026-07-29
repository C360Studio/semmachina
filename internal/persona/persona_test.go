package persona_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/model"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testActionID     = "act-1"
	testTurnID       = "turn-act-1"
	testTurnEntityID = "c360.semmachina.world1.starter.turn.turn-act-1"
	testSceneID      = "c360.semmachina.world1.starter.scene.gatehouse"
	testPlayerID     = "c360.semmachina.world1.starter.player.solo"
	testCharacterID  = "c360.semmachina.world1.starter.character.rook"
	testSentryID     = "c360.semmachina.world1.starter.character.hollis"
	testCrowbarID    = "c360.semmachina.world1.starter.item.crowbar"
	testInstance     = "TEST_CONTENT"
)

var testTime = time.Date(2026, 7, 28, 18, 30, 0, 0, time.UTC)

func testIdentity() persona.Identity {
	return persona.Identity{
		TurnID:       testTurnID,
		TurnEntityID: testTurnEntityID,
		ActionID:     testActionID,
		SceneID:      testSceneID,
	}
}

// journal records the ORDER of writes across the two stores a persona's exit
// touches.
//
// It exists because "the artifact is stored before the reference reaches the
// graph" is the entire crash-safety argument of both terminal tools, and neither
// store can observe it alone: each sees only its own call. The failure it guards
// against — a reference committed to a turn whose object is not there yet — is
// invisible in every end-state assertion, because the end state after a
// successful run is identical either way.
type journal struct{ entries []string }

func (j *journal) add(entry string) { j.entries = append(j.entries, entry) }

// fakeArtifacts is an in-memory content store that keeps the one property the
// production store's key derivation buys: a repeated write lands on the same
// reference rather than producing a second one.
type fakeArtifacts struct {
	journal *journal

	actions    map[string]*payload.PlayerAction
	verdicts   map[string]*payload.Verdict
	narrations map[string]*content.Narration
	failures   map[string]*content.FailureDetail

	verdictErr   error
	narrationErr error
	failureErr   error

	verdictPuts   int
	narrationPuts int
}

func newFakeArtifacts(j *journal) *fakeArtifacts {
	return &fakeArtifacts{
		journal:    j,
		actions:    map[string]*payload.PlayerAction{},
		verdicts:   map[string]*payload.Verdict{},
		narrations: map[string]*content.Narration{},
		failures:   map[string]*content.FailureDetail{},
	}
}

func (a *fakeArtifacts) ref(t *testing.T, predicate vocabulary.Predicate, turnID string) content.Ref {
	t.Helper()
	key, err := content.KeyFor(predicate, content.SubjectTurn, turnID)
	if err != nil {
		t.Fatalf("KeyFor(%s): %v", predicate, err)
	}
	return content.Ref{Instance: testInstance, Key: key}
}

func (a *fakeArtifacts) put(predicate vocabulary.Predicate, turnID string) (content.Ref, error) {
	key, err := content.KeyFor(predicate, content.SubjectTurn, turnID)
	if err != nil {
		return content.Ref{}, err
	}
	a.journal.add("put " + key)
	return content.Ref{Instance: testInstance, Key: key}, nil
}

func (a *fakeArtifacts) PutVerdict(
	_ context.Context,
	turnEntityID string,
	verdict *payload.Verdict,
) (content.Ref, error) {
	a.verdictPuts++
	if a.verdictErr != nil {
		return content.Ref{}, a.verdictErr
	}
	if err := payload.RequireTurnEntityID(verdict.TurnID, turnEntityID); err != nil {
		return content.Ref{}, err
	}
	// The production store refuses to store an artifact that cannot pass its own
	// contract, and that refusal is load-bearing here: it is why the tool
	// boundary MUST validate rather than defer to a downstream consumer.
	if err := verdict.Validate(); err != nil {
		return content.Ref{}, err
	}
	ref, err := a.put(vocabulary.TurnVerdictRef, verdict.TurnID)
	if err != nil {
		return content.Ref{}, err
	}
	stored := *verdict
	a.verdicts[ref.Key] = &stored
	return ref, nil
}

func (a *fakeArtifacts) GetVerdict(_ context.Context, ref content.Ref) (*payload.Verdict, error) {
	stored, ok := a.verdicts[ref.Key]
	if !ok {
		return nil, fmt.Errorf("resolve %s: %w", ref, content.ErrArtifactNotFound)
	}
	return stored, nil
}

func (a *fakeArtifacts) PutNarration(
	_ context.Context,
	turnEntityID string,
	narration *content.Narration,
) (content.Ref, error) {
	a.narrationPuts++
	if a.narrationErr != nil {
		return content.Ref{}, a.narrationErr
	}
	if err := payload.RequireTurnEntityID(narration.TurnID, turnEntityID); err != nil {
		return content.Ref{}, err
	}
	if err := narration.Validate(); err != nil {
		return content.Ref{}, err
	}
	ref, err := a.put(vocabulary.TurnNarrationRef, narration.TurnID)
	if err != nil {
		return content.Ref{}, err
	}
	stored := *narration
	a.narrations[ref.Key] = &stored
	return ref, nil
}

func (a *fakeArtifacts) GetAction(_ context.Context, ref content.Ref) (*payload.PlayerAction, error) {
	stored, ok := a.actions[ref.Key]
	if !ok {
		return nil, fmt.Errorf("resolve %s: %w", ref, content.ErrArtifactNotFound)
	}
	return stored, nil
}

func (a *fakeArtifacts) PutFailureDetail(
	_ context.Context,
	turnEntityID string,
	detail *content.FailureDetail,
) (content.Ref, error) {
	if a.failureErr != nil {
		return content.Ref{}, a.failureErr
	}
	if err := payload.RequireTurnEntityID(detail.TurnID, turnEntityID); err != nil {
		return content.Ref{}, err
	}
	if err := detail.Validate(); err != nil {
		return content.Ref{}, err
	}
	ref, err := a.put(vocabulary.TurnFailureRef, detail.TurnID)
	if err != nil {
		return content.Ref{}, err
	}
	stored := *detail
	a.failures[ref.Key] = &stored
	return ref, nil
}

// fakeGraph is an in-memory graph whose merge lane REPLACES by (subject,
// predicate) — the lane every single-valued predicate the engine writes has to
// travel. Its appending sibling below exists so the readers' anomaly checks are
// exercised against the shape that actually produces them.
type fakeGraph struct {
	journal  *journal
	entities map[string]*graph.EntityState
	mergeErr error
	merges   int
	getErr   error
}

func newFakeGraph(j *journal) *fakeGraph {
	return &fakeGraph{journal: j, entities: map[string]*graph.EntityState{}}
}

func (g *fakeGraph) seedTurn(triples ...message.Triple) {
	g.entities[testTurnEntityID] = &graph.EntityState{
		ID:          testTurnEntityID,
		MessageType: message.Type{Domain: payload.Domain, Category: payload.CategoryTurnState, Version: "v1"},
		Triples:     triples,
	}
}

func (g *fakeGraph) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	_ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	g.merges++
	g.journal.add("merge " + entityID)
	if g.mergeErr != nil {
		return nil, g.mergeErr
	}
	stored, ok := g.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("merge into %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	for _, triple := range triples {
		if triple.Subject != entityID {
			return nil, fmt.Errorf("triple %q targets %q, not %q", triple.Predicate, triple.Subject, entityID)
		}
		replaced := false
		for idx := range stored.Triples {
			if stored.Triples[idx].Predicate == triple.Predicate {
				stored.Triples[idx] = triple
				replaced = true
				break
			}
		}
		if !replaced {
			stored.Triples = append(stored.Triples, triple)
		}
	}
	return stored, nil
}

func (g *fakeGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	if g.getErr != nil {
		return nil, g.getErr
	}
	stored, ok := g.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	return stored, nil
}

func objectsFor(state *graph.EntityState, predicate vocabulary.Predicate) []any {
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

func firstObject(t *testing.T, state *graph.EntityState, predicate vocabulary.Predicate) any {
	t.Helper()
	objects := objectsFor(state, predicate)
	if len(objects) != 1 {
		t.Fatalf("entity %s holds %d values for %s, want exactly one", state.ID, len(objects), predicate)
	}
	return objects[0]
}

// call builds a tool call the way the agentic loop delivers one: arguments from
// the model, metadata from the engine.
func call(name string, arguments map[string]any, metadata map[string]any) agentic.ToolCall {
	return agentic.ToolCall{ID: "call-1", Name: name, Arguments: arguments, Metadata: metadata, LoopID: "loop-1"}
}

// injected is the metadata a spawner stamps, rendered the way it arrives after a
// JSON round trip through the task message.
func injected(t *testing.T, spec persona.Spec, band vocabulary.OutcomeBand) map[string]any {
	t.Helper()
	task, err := spec.Task(persona.TaskRequest{
		Identity: testIdentity(),
		Band:     band,
		Prompt:   "a prompt",
	})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	encoded, err := json.Marshal(task.Metadata)
	if err != nil {
		t.Fatalf("encode task metadata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode task metadata: %v", err)
	}
	return decoded
}

// ------------------------------------------------------------------- specs

func TestSpecs_AreTheTwoPlacesAModelIsAllowedToSpeak(t *testing.T) {
	specs := persona.Specs()
	if len(specs) != 2 {
		t.Fatalf("the engine declares %d personas, want exactly the adjudicator and the narrator", len(specs))
	}
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			t.Fatalf("persona %q does not satisfy its own contract: %v", spec.Role, err)
		}
		if len(spec.Tool.Parameters) == 0 {
			t.Fatalf("persona %q advertises a tool with no parameters", spec.Role)
		}
	}
	if _, err := persona.SpecFor("chronicler"); err == nil {
		t.Fatal("SpecFor accepted a role this engine does not run")
	}
}

// Bounded cognition (M5) is a property of the TYPE, not of the two values that
// happen to be shipped. A spec is exported and constructible, so a future
// persona — or a rule pack author reaching for one — must not be able to build a
// loop with no ceiling: a persona that can iterate forever is a turn that can
// cost forever, and there is no downstream gate that would notice.
func TestSpec_RefusesAPersonaWithNoCeiling(t *testing.T) {
	unbounded := persona.Adjudicator()
	for name, budget := range map[string]int{"unset": 0, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			unbounded.MaxIterations = budget
			if err := unbounded.Validate(); err == nil {
				t.Fatal("a persona with no iteration ceiling passed validation")
			}
			if _, err := unbounded.Task(persona.TaskRequest{
				Identity: testIdentity(), Prompt: "the world",
			}); err == nil {
				t.Fatal("a persona with no iteration ceiling was spawned")
			}
		})
	}

	noClock := persona.Narrator()
	noClock.Timeout = 0
	if err := noClock.Validate(); err == nil {
		t.Fatal("a persona with no timeout passed validation; a model call that never returns is the other " +
			"way a turn costs forever")
	}

	// The resume check reads a POINTER at what a persona produced. A predicate
	// carrying a value instead cannot say whether the interrupted attempt
	// finished, which is the one question the check exists to answer.
	valueNotPointer := persona.Adjudicator()
	valueNotPointer.Artifact = vocabulary.TurnVerdictPlausibility
	if err := valueNotPointer.Validate(); err == nil {
		t.Fatal("a persona whose completion mark is a value rather than a reference passed validation")
	}
}

// F5 is the whole reason this is asserted rather than assumed: ADR-026 records
// upstream hitting schema-adherence collapse on small models, and the design's
// original placement of the adjudicator on the small slot is the exact
// configuration that warns against. Demoting it later is an experiment; shipping
// it demoted by accident is the failure.
func TestAdjudicator_StartsOnTheMidSlotAndCarriesTheOnlySchemaBearingExit(t *testing.T) {
	adjudicator := persona.Adjudicator()
	if adjudicator.Slot != persona.SlotMid {
		t.Fatalf("the adjudicator is configured for the %q slot, want %q", adjudicator.Slot, persona.SlotMid)
	}
	if adjudicator.Tool.Name != persona.VerdictToolName {
		t.Fatalf("the adjudicator exits through %q", adjudicator.Tool.Name)
	}

	narrator := persona.Narrator()
	if narrator.Slot != persona.SlotLarge {
		t.Fatalf("the narrator is configured for the %q slot, want %q", narrator.Slot, persona.SlotLarge)
	}
	// The trade ADR-026 recommends, stated as a property: exactly one persona
	// carries a nested schema, and it is not the one chosen for prose.
	if nested := nestedProperties(t, narrator.Tool); nested != 0 {
		t.Fatalf("the narrator's exit has %d nested object properties; its schema burden is supposed to be "+
			"zero so the model chosen for prose is never chosen for schema adherence", nested)
	}
	if nested := nestedProperties(t, adjudicator.Tool); nested == 0 {
		t.Fatal("the adjudicator's exit has no nested structure, so this test is not measuring what it claims")
	}
}

// nestedProperties counts object-or-array properties at the top level of a tool
// schema: the shape a small model loses track of.
func nestedProperties(t *testing.T, tool agentic.ToolDefinition) int {
	t.Helper()
	properties, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q has no properties map", tool.Name)
	}
	count := 0
	for _, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := property["type"].(string); kind == "object" || kind == "array" {
			count++
		}
	}
	return count
}

// Every enumeration in the adjudicator's schema has to be the vocabulary's own,
// because the closed set has three consumers — this schema, the applier, and the
// rule pack — and this is the only one a compiler never checks against the
// others. A hand-typed enum here would drift silently and produce exits the
// applier refuses.
func TestVerdictSchema_EnumeratesFromTheVocabularyAndNotFromLiterals(t *testing.T) {
	tool := persona.Adjudicator().Tool
	sets := vocabulary.Sets()

	found := map[vocabulary.Kind]bool{}
	walkEnums(t, tool.Parameters, func(path string, values []string) {
		matched := false
		for kind, want := range sets {
			if slices.Equal(values, want) {
				found[kind] = true
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("the enum at %s is %v, which is not any closed set in internal/vocabulary", path, values)
		}
	})

	for _, kind := range []vocabulary.Kind{
		vocabulary.KindPlausibility, vocabulary.KindRisk, vocabulary.KindConsequence,
		vocabulary.KindModifierSource, vocabulary.KindEffectType, vocabulary.KindStatus,
		vocabulary.KindAttribute, vocabulary.KindRelation,
	} {
		if !found[kind] {
			t.Fatalf("the verdict schema never enumerates the %q set, so the model is never shown it", kind)
		}
	}
}

// walkEnums visits every "enum" list in a JSON-Schema document.
func walkEnums(t *testing.T, node any, visit func(path string, values []string)) {
	t.Helper()
	var walk func(path string, node any)
	walk = func(path string, node any) {
		object, ok := node.(map[string]any)
		if !ok {
			return
		}
		if raw, present := object["enum"]; present {
			values, ok := raw.([]string)
			if !ok {
				t.Fatalf("the enum at %s is a %T, want []string", path, raw)
			}
			visit(path, values)
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			walk(path+"."+key, object[key])
		}
	}
	walk("$", node)
}

// The banded shape is a discriminated union — three bands XOR one auto — and
// strict mode requires every property to be listed in `required`. Declaring it
// strict would therefore demand the model author bands the engine must refuse.
// This is a second, independent reason executor validation is the enforcement of
// record, and it is asserted so that a well-meaning "turn strict on everywhere"
// change has to confront it.
func TestVerdictSchema_IsNotDeclaredStrictBecauseTheBandSetCannotBe(t *testing.T) {
	tool := persona.Adjudicator().Tool
	if tool.Strict {
		t.Fatal("the verdict tool is declared strict; strict mode requires every property in `required`, and " +
			"a verdict must declare exactly the band set its roll gate calls for")
	}
	bands, ok := tool.Parameters["properties"].(map[string]any)["bands"].(map[string]any)
	if !ok {
		t.Fatal("the verdict schema has no bands property")
	}
	if _, present := bands["required"]; present {
		t.Fatal("the bands object lists required properties; a verdict declares miss/partial/full or auto, " +
			"never both, so no band can be unconditionally required")
	}

	// The narrator's does satisfy the subset, and takes the free upside.
	narration := persona.Narrator().Tool
	if !narration.Strict {
		t.Fatal("the narration tool is not declared strict, and its one-property schema does satisfy strict " +
			"mode's subset; the flag is free where it is honoured and a no-op where it is not")
	}
}

// A schema declared strict is one the PROVIDER validates, and a provider that
// meets a validation keyword it does not support returns a 400. That failure is
// invisible to every token-free test in this repo and arrives on the first live
// turn, so a strict schema stays inside a conservative keyword subset and the
// bounds it gives up are enforced where they were always the enforcement of
// record: the executor.
func TestStrictSchemas_StayInsideTheConservativeKeywordSubset(t *testing.T) {
	allowed := persona.StrictSchemaKeywords()
	checked := 0
	for _, spec := range persona.Specs() {
		if !spec.Tool.Strict {
			continue
		}
		walkSchemaKeywords(t, spec.Tool.Parameters, func(path, keyword string) {
			checked++
			if !allowed[keyword] {
				t.Fatalf("persona %q declares its exit strict and uses %q at %s; a provider that does not "+
					"support the keyword answers 400, and no token-free test would ever see it",
					spec.Role, keyword, path)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no strict schema was walked; either no persona declares one or the walk is broken")
	}

	// Anti-vacuity: the NON-strict schema does use keywords outside the subset,
	// which is exactly why it is not declared strict.
	loose := 0
	walkSchemaKeywords(t, persona.Adjudicator().Tool.Parameters, func(_, keyword string) {
		if !allowed[keyword] {
			loose++
		}
	})
	if loose == 0 {
		t.Fatal("the non-strict schema uses only conservative keywords, so the subset check above is not " +
			"constraining anything")
	}
}

// walkSchemaKeywords visits every key in a JSON-Schema document, skipping the
// names of properties (which are field names, not keywords).
func walkSchemaKeywords(t *testing.T, node any, visit func(path, keyword string)) {
	t.Helper()
	var walk func(path string, node any, inProperties bool)
	walk = func(path string, node any, inProperties bool) {
		object, ok := node.(map[string]any)
		if !ok {
			return
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if !inProperties {
				visit(path, key)
			}
			walk(path+"."+key, object[key], key == "properties")
		}
	}
	walk("$", node, false)
}

// The identity contract, asserted against the schema itself: a tool that never
// mentions a turn cannot invite a model to invent one.
func TestTerminalTools_NeverAskTheModelForAnIdentifierTheEngineKnows(t *testing.T) {
	forbidden := []string{"turn_id", "turn", "action_id", "action", "scene_id", "scene", "player_id", "loop_id"}
	for _, spec := range persona.Specs() {
		properties, ok := spec.Tool.Parameters["properties"].(map[string]any)
		if !ok {
			t.Fatalf("persona %q advertises a tool with no properties", spec.Role)
		}
		for name := range properties {
			if slices.Contains(forbidden, name) {
				t.Fatalf("persona %q asks the model for %q; identifiers are engine knowledge — a live model "+
					"would hallucinate one and a scripted one could not be written at all", spec.Role, name)
			}
		}
	}
}

func TestTask_CarriesTheBudgetTheToolsAndTheInjectedIdentity(t *testing.T) {
	spec := persona.Adjudicator()
	task, err := spec.Task(persona.TaskRequest{Identity: testIdentity(), Prompt: "the world, rendered"})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}

	if task.Role != string(persona.RoleAdjudicator) {
		t.Fatalf("task role is %q; the loop filters the world package's prompt fragments by it", task.Role)
	}
	if task.Model != persona.CapabilityAdjudication {
		t.Fatalf("task model is %q, want the registry capability %q", task.Model, persona.CapabilityAdjudication)
	}
	if task.MaxIterations == nil || *task.MaxIterations != spec.MaxIterations {
		t.Fatalf("task iteration budget is %v, want %d", task.MaxIterations, spec.MaxIterations)
	}
	if len(task.Tools) != 1 || task.Tools[0].Name != persona.VerdictToolName {
		t.Fatalf("task advertises %d tools (%+v), want exactly the terminal one", len(task.Tools), task.Tools)
	}
	if task.ToolChoice == nil || task.ToolChoice.Mode != "required" {
		t.Fatalf("task tool choice is %+v, want a required exit", task.ToolChoice)
	}
	if task.Timeout != spec.Timeout.String() {
		t.Fatalf("task timeout is %q, want %q", task.Timeout, spec.Timeout)
	}

	identity, err := persona.IdentityFrom(task.Metadata)
	if err != nil {
		t.Fatalf("the task's own metadata does not yield an identity: %v", err)
	}
	if identity != testIdentity() {
		t.Fatalf("the injected identity round-tripped as %+v", identity)
	}
}

// The narration spec's "no mutation-capable tool" made structural: the task
// carries an explicit allowlist of one, and Tools being non-nil is what makes it
// an override rather than "whatever happens to be registered".
func TestNarratorTask_AdvertisesOneToolAndItCannotChangeTheWorld(t *testing.T) {
	task, err := persona.Narrator().Task(persona.TaskRequest{
		Identity: testIdentity(),
		Band:     vocabulary.BandPartial,
		Prompt:   "the world, rendered",
	})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if task.Tools == nil {
		t.Fatal("the narrator's task carries a nil tool list, which the loop reads as 'no override' and falls " +
			"back to global discovery — every registered tool, including the adjudicator's exit")
	}
	if len(task.Tools) != 1 || task.Tools[0].Name != persona.NarrationToolName {
		t.Fatalf("the narrator may call %+v", task.Tools)
	}
	if err := agentic.ValidateToolsAllowed(
		[]agentic.ToolCall{{Name: persona.VerdictToolName}},
		[]string{task.Tools[0].Name},
	); err == nil {
		t.Fatal("the narrator's allowlist admits the adjudicator's exit, which proposes world changes")
	}
}

func TestNarratorTask_RefusesToRunWithoutTheBandItIsVoicing(t *testing.T) {
	if _, err := persona.Narrator().Task(persona.TaskRequest{
		Identity: testIdentity(),
		Prompt:   "the world, rendered",
	}); err == nil {
		t.Fatal("the narrator was spawned with no outcome band; it would have to guess which outcome it is " +
			"voicing, and a narration filed under the wrong band is drift the engine manufactured itself")
	}
	if _, err := persona.Narrator().Task(persona.TaskRequest{
		Identity: testIdentity(),
		Band:     "triumphant",
		Prompt:   "the world, rendered",
	}); err == nil {
		t.Fatal("the narrator was spawned with a band outside the closed set")
	}
}

func TestAdjudicatorTask_RefusesABandItRunsBefore(t *testing.T) {
	if _, err := persona.Adjudicator().Task(persona.TaskRequest{
		Identity: testIdentity(),
		Band:     vocabulary.BandFull,
		Prompt:   "the world, rendered",
	}); err == nil {
		t.Fatal("the adjudicator was spawned carrying an outcome band; it runs before any band exists, so a " +
			"band in its context is a number the engine had not chosen yet")
	}
}

func TestTask_RefusesAnIncoherentIdentity(t *testing.T) {
	cases := map[string]persona.Identity{
		"turn entity addressing another turn": {
			TurnID:       testTurnID,
			TurnEntityID: "c360.semmachina.world1.starter.turn.turn-act-9",
			ActionID:     testActionID,
			SceneID:      testSceneID,
		},
		"action that does not derive the turn": {
			TurnID: testTurnID, TurnEntityID: testTurnEntityID, ActionID: "act-9", SceneID: testSceneID,
		},
		"scene that is not an entity": {
			TurnID: testTurnID, TurnEntityID: testTurnEntityID, ActionID: testActionID, SceneID: "gatehouse",
		},
	}
	for name, identity := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := persona.Adjudicator().Task(persona.TaskRequest{
				Identity: identity, Prompt: "the world",
			}); err == nil {
				t.Fatal("the spawn was accepted; the identity it injects would be filed against the wrong turn")
			}
		})
	}
}

func TestTask_RefusesAnEmptyPrompt(t *testing.T) {
	if _, err := persona.Adjudicator().Task(persona.TaskRequest{Identity: testIdentity()}); err == nil {
		t.Fatal("a persona was spawned with no prompt; it would answer from its system prompt alone, which " +
			"means judging an action nobody described in a world nobody assembled")
	}
}

// ---------------------------------------------------------------- identity

func TestIdentityFrom_RefusesWhatTheSpawnerDidNotStamp(t *testing.T) {
	complete := injected(t, persona.Adjudicator(), "")
	for key := range complete {
		partial := map[string]any{}
		for k, v := range complete {
			if k != key {
				partial[k] = v
			}
		}
		if _, err := persona.IdentityFrom(partial); err == nil {
			t.Fatalf("an identity missing %q was accepted; the executor would file the exit against a turn it "+
				"invented", key)
		}
	}
	if _, err := persona.IdentityFrom(nil); err == nil {
		t.Fatal("a tool call with no metadata at all yielded an identity")
	}
	// A JSON round trip is the shape metadata actually arrives in, so a
	// non-string value is a real possibility rather than a hypothetical.
	broken := injected(t, persona.Adjudicator(), "")
	broken[persona.MetadataKeyTurnID] = 7.0
	if _, err := persona.IdentityFrom(broken); err == nil {
		t.Fatal("a numeric turn id was accepted")
	}
}

// ------------------------------------------------------------- registration

func TestRegisterTools_WiresBothExitsOrNeither(t *testing.T) {
	registry := agentictools.NewExecutorRegistry()
	artifacts := newFakeArtifacts(&journal{})
	if err := persona.RegisterTools(registry, artifacts, newFakeGraph(&journal{})); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range registry.ListTools() {
		names[tool.Name] = true
	}
	for _, want := range []string{persona.VerdictToolName, persona.NarrationToolName} {
		if !names[want] {
			t.Fatalf("the registry advertises %v, missing %q", names, want)
		}
		if registry.GetTool(want) == nil {
			t.Fatalf("tool %q is advertised and does not dispatch", want)
		}
	}

	// A second executor answering to one of these names is a second opinion
	// about what a verdict is, and it must not be a silent last-wins.
	if err := persona.RegisterTools(registry, artifacts, newFakeGraph(&journal{})); err == nil {
		t.Fatal("registering the persona tools twice was accepted")
	}
}

func TestRegisterTools_RefusesToBuildAnExitWithNowhereToWrite(t *testing.T) {
	if err := persona.RegisterTools(agentictools.NewExecutorRegistry(), nil, newFakeGraph(&journal{})); err == nil {
		t.Fatal("a terminal tool was built with no content store; its artifact would have nowhere to live but " +
			"the graph's rule-matching surface")
	}
	if err := persona.RegisterTools(
		agentictools.NewExecutorRegistry(), newFakeArtifacts(&journal{}), nil); err == nil {
		t.Fatal("a terminal tool was built with no graph surface")
	}
}

// ------------------------------------------------------------ registry gate

// A persona whose only exit is a tool call, pointed at an endpoint that cannot
// call tools, can never terminate: it burns its whole budget every turn and
// fails as a cap exhaustion, which describes the symptom and hides the cause.
// Catching it at boot is the difference between a configuration error and a
// mystery.
func TestCheckRegistry_RefusesAnEndpointThatCannotCallTools(t *testing.T) {
	toolless := &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"local": {Provider: "openai", URL: "http://localhost:1/v1", Model: "m", MaxTokens: 8192},
		},
		Capabilities: map[string]*model.CapabilityConfig{
			persona.CapabilityAdjudication: {Preferred: []string{"local"}},
			persona.CapabilityNarration:    {Preferred: []string{"local"}},
		},
		Defaults: model.DefaultsConfig{Model: "local"},
	}
	if err := persona.CheckRegistryAll(toolless); err == nil {
		t.Fatal("a deployment wired both personas to a tool-less endpoint and booted healthy")
	}

	capable := &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"local": {
				Provider: "openai", URL: "http://localhost:1/v1", Model: "m",
				MaxTokens: 8192, SupportsTools: true,
			},
		},
		Capabilities: map[string]*model.CapabilityConfig{
			persona.CapabilityAdjudication: {Preferred: []string{"local"}, RequiresTools: true},
			persona.CapabilityNarration:    {Preferred: []string{"local"}, RequiresTools: true},
		},
		Defaults: model.DefaultsConfig{Model: "local"},
	}
	if err := capable.Validate(); err != nil {
		t.Fatalf("the control registry is not a valid one: %v", err)
	}
	if err := persona.CheckRegistryAll(capable); err != nil {
		t.Fatalf("a correctly wired deployment was refused: %v", err)
	}
}

func TestCheckRegistry_RefusesADeploymentThatDeclaresNoCapability(t *testing.T) {
	empty := &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"local": {Provider: "openai", URL: "http://localhost:1/v1", Model: "m", MaxTokens: 8192, SupportsTools: true},
		},
		Defaults: model.DefaultsConfig{Model: "local"},
	}
	err := persona.CheckRegistryAll(empty)
	if err == nil {
		t.Fatal("a deployment declaring neither persona capability was accepted")
	}
	if !strings.Contains(err.Error(), persona.CapabilityAdjudication) {
		t.Fatalf("the refusal is %q; it must name the capability the operator has to declare", err)
	}
}

// ------------------------------------------------------- cap exhaustion

// The cap itself is upstream's — the loop counts and stops. What is ours is the
// budget we hand it and the explicit failure recorded when it runs out: a loop
// that stops having produced nothing leaves a turn sitting in its stage with a
// player waiting on it, which is the silent stall the cap existed to prevent.
func TestRecordCapExhausted_EndsTheTurnWithAClosedCodeAndAStoredExplanation(t *testing.T) {
	j := &journal{}
	artifacts := newFakeArtifacts(j)
	// Both writers record into ONE sequence, which is the only way the ordering
	// below is observable: each store sees only its own call, and the end state
	// after a successful run is identical either way.
	failer := &fakeFailer{journal: j}

	transition, err := persona.RecordCapExhausted(t.Context(), failer, artifacts, testIdentity(),
		persona.CapExhaustion{
			Role:       persona.RoleAdjudicator,
			LoopID:     "loop-7",
			Iterations: persona.AdjudicatorMaxIterations,
			LastError:  "risk: \"catastrophic\" is not in the closed vocabulary",
		})
	if err != nil {
		t.Fatalf("RecordCapExhausted: %v", err)
	}
	if transition.Phase != vocabulary.PhaseFailed {
		t.Fatalf("the turn ended in phase %q", transition.Phase)
	}
	if failer.reason != vocabulary.FailurePersonaCapExhausted {
		t.Fatalf("the recorded reason is %q, want the closed cap-exhaustion code", failer.reason)
	}
	if failer.detail.IsZero() {
		t.Fatal("the turn failed with no reference to an explanation; a closed code alone cannot say which " +
			"field the persona could not get right")
	}

	stored := artifacts.failures[failer.detail.Key]
	if stored == nil {
		t.Fatalf("nothing was stored at %s", failer.detail)
	}
	for _, want := range []string{"adjudicator", "loop-7", "catastrophic", persona.VerdictToolName} {
		if !strings.Contains(stored.Message, want) {
			t.Fatalf("the stored explanation is %q, missing %q", stored.Message, want)
		}
	}

	// Detail first, then the failure — a turn advertising an explanation nobody
	// stored is worse than one carrying no reference at all.
	if len(j.entries) != 2 {
		t.Fatalf("the cap-exhaustion path performed %d writes (%v), want the detail and the failure",
			len(j.entries), j.entries)
	}
	if !strings.HasPrefix(j.entries[0], "put ") || !strings.HasPrefix(j.entries[1], "fail ") {
		t.Fatalf("the write order was %v, want the detail stored before the turn was failed", j.entries)
	}
}

func TestRecordCapExhausted_RefusesARoleThisEngineDoesNotRun(t *testing.T) {
	_, err := persona.RecordCapExhausted(t.Context(), &fakeFailer{}, newFakeArtifacts(&journal{}), testIdentity(),
		persona.CapExhaustion{Role: "chronicler", Iterations: 3})
	if err == nil {
		t.Fatal("a cap exhaustion was recorded for a persona that does not exist")
	}
}

// fakeFailer records the one call the cap-exhaustion path makes.
type fakeFailer struct {
	journal *journal
	reason  vocabulary.FailureReason
	detail  content.Ref
	err     error
}

func (f *fakeFailer) Fail(
	_ context.Context,
	turnID, turnEntityID string,
	reason vocabulary.FailureReason,
	detail content.Ref,
) (turn.Transition, error) {
	if f.journal != nil {
		f.journal.add("fail " + turnEntityID)
	}
	if f.err != nil {
		return turn.Transition{}, f.err
	}
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return turn.Transition{}, err
	}
	if _, err := vocabulary.ParseFailureReason(string(reason)); err != nil {
		return turn.Transition{}, err
	}
	f.reason = reason
	f.detail = detail
	return turn.Transition{
		Previous: vocabulary.PhaseAdjudicating,
		Phase:    vocabulary.PhaseFailed,
		Outcome:  turn.OutcomeAdvanced,
	}, nil
}
