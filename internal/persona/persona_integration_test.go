package persona_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// A terminal tool's exit is a claim about the GRAPH and about an OBJECT STORE,
// and both are places the claim can be false in ways no fake reproduces. The
// mutation API offers two lanes that accept the same triples — one replaces by
// (subject, predicate) and one appends — and the appending one leaves a turn
// holding two verdicts with a success response and no error anywhere (F14). A
// resumed persona is exactly the duplicate write that finds it.
//
// The object store is the other half: "the prose survived a process that died"
// is not a thing a map can be wrong about.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

// Each test gets its own world namespace and bucket, so tests sharing one broker
// cannot see each other's writes.
var namespaceCounter atomic.Int64

// live is one turn entity in a real graph, with real stores behind the two
// terminal tools.
type live struct {
	harness   *testinfra.Harness
	graph     *graphio.Store
	content   *content.Store
	verdict   *persona.VerdictExecutor
	narration *persona.NarrationExecutor
	metadata  func(band vocabulary.OutcomeBand) map[string]any
	turnID    string
	entityID  string
	sceneID   string
	actionID  string
}

func startLive(t *testing.T) *live {
	t.Helper()
	harness := testinfra.Require(t)
	namespace := fmt.Sprintf("w%d", namespaceCounter.Add(1))

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	backend, err := content.NewObjectStore(t.Context(), harness.Client,
		content.WithBucket("PERSONA_"+namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	verdict, err := persona.NewVerdictExecutor(artifacts, store)
	if err != nil {
		t.Fatalf("NewVerdictExecutor: %v", err)
	}
	narration, err := persona.NewNarrationExecutor(artifacts, store)
	if err != nil {
		t.Fatalf("NewNarrationExecutor: %v", err)
	}

	actionID := "act-1"
	turnID := payload.TurnIDForAction(actionID)
	entityID := composeID(t, namespace, "turn", turnID)
	sceneID := composeID(t, namespace, "scene", "gatehouse")
	createTurn(t, store, entityID)

	world := &live{
		harness: harness, graph: store, content: artifacts,
		verdict: verdict, narration: narration,
		turnID: turnID, entityID: entityID, sceneID: sceneID, actionID: actionID,
	}
	world.metadata = func(band vocabulary.OutcomeBand) map[string]any {
		return roundTripped(t, world.identity(), band)
	}
	return world
}

func (w *live) identity() persona.Identity {
	return persona.Identity{
		TurnID: w.turnID, TurnEntityID: w.entityID, ActionID: w.actionID, SceneID: w.sceneID,
	}
}

func composeID(t *testing.T, namespace, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID("c360", namespace, "starter", kind, instance)
	if err != nil {
		t.Fatalf("compose %s id: %v", kind, err)
	}
	return id
}

// roundTripped renders the injected metadata through JSON, which is how it
// actually reaches a tool call: the task travels as a message.
func roundTripped(t *testing.T, identity persona.Identity, band vocabulary.OutcomeBand) map[string]any {
	t.Helper()
	spec := persona.Adjudicator()
	if band != "" {
		spec = persona.Narrator()
	}
	task, err := spec.Task(persona.TaskRequest{Identity: identity, Band: band, Prompt: "the world"})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	encoded, err := json.Marshal(task.Metadata)
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	return decoded
}

func createTurn(t *testing.T, store *graphio.Store, id string) {
	t.Helper()
	if _, err := store.CreateEntity(t.Context(), &graph.EntityState{
		ID:          id,
		MessageType: message.Type{Domain: payload.Domain, Category: payload.CategoryTurnState, Version: payload.SchemaVersion},
		Version:     1,
		UpdatedAt:   testTime,
		Triples: []message.Triple{{
			Subject:    id,
			Predicate:  vocabulary.TurnPhaseCurrent.String(),
			Object:     string(vocabulary.PhaseAdjudicating),
			Source:     "test",
			Timestamp:  testTime,
			Confidence: 1.0,
		}},
	}); err != nil {
		t.Fatalf("create turn entity: %v", err)
	}
}

// verdictArgumentsFor renders a valid exit whose targets exist in this world.
func (w *live) verdictArguments(t *testing.T) map[string]any {
	t.Helper()
	return arguments(t, fmt.Sprintf(`{
	  "scalars": {"plausibility": "certain", "risk": "none", "consequence": "none", "requires_roll": false},
	  "modifiers": [],
	  "bands": {"auto": [{"type": "set_status", "target": %q, "status": "wounded"}]},
	  "rationale": "A verdict against a real graph."
	}`, composeIDFromEntity(w.entityID, "character", "rook")))
}

// composeIDFromEntity borrows the world namespace off an existing id, so a
// scripted target names an entity in THIS test's world.
func composeIDFromEntity(entityID, kind, instance string) string {
	parts := splitSix(entityID)
	return fmt.Sprintf("%s.%s.%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3], kind, instance)
}

func splitSix(entityID string) [6]string {
	var out [6]string
	start, index := 0, 0
	for i := 0; i <= len(entityID) && index < 6; i++ {
		if i == len(entityID) || entityID[i] == '.' {
			out[index] = entityID[start:i]
			index++
			start = i + 1
		}
	}
	return out
}

// The F14 regression guard for the adjudicator's exit, against a real broker: a
// resumed persona that exits a second time must leave ONE verdict on the turn.
// The negative control below is the whole reason this test exists — a distinct
// observation of the same scalar through the appending lane leaves two, with a
// success response and no error anywhere. beta.159 correctly deduplicates a
// byte-identical six-field retry, so provenance is varied deliberately.
func TestIntegration_ARepeatedVerdictLeavesOneValuePerPredicate(t *testing.T) {
	world := startLive(t)
	arguments := world.verdictArguments(t)

	for attempt := range 2 {
		result, err := world.verdict.Execute(context.Background(),
			call(persona.VerdictToolName, arguments, world.metadata("")))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if result.ErrorKind != "" {
			t.Fatalf("attempt %d was refused: %s", attempt, result.Error)
		}
	}

	state := world.harness.AwaitEntity(t, world.entityID)
	for _, predicate := range append(vocabulary.VerdictScalarPredicates(), vocabulary.TurnVerdictRef) {
		if got := testinfra.ObjectsFor(state, predicate.String()); len(got) != 1 {
			t.Fatalf("after two exits the turn holds %d values for %s: %v", len(got), predicate, got)
		}
	}

	// The negative control: the same triples through the lane that appends. A
	// test that only ever saw the merge lane could not tell a correct write from
	// a lane that happens to be idempotent for other reasons.
	appendSameVerdict(t, world, state)
	after := world.harness.AwaitEntity(t, world.entityID)
	if got := testinfra.ObjectsFor(after, vocabulary.TurnVerdictPlausibility.String()); len(got) != 2 {
		t.Fatalf("the appending lane left %d values for %s; if it leaves one, this graph is not the trap the "+
			"merge lane exists to avoid and the guard above proves nothing",
			len(got), vocabulary.TurnVerdictPlausibility)
	}
}

// appendSameVerdict writes one of the verdict's scalars through
// graph.mutation.triple.add_batch — the APPENDING lane the executor must never
// use.
func appendSameVerdict(t *testing.T, world *live, state *graph.EntityState) {
	t.Helper()
	var existing message.Triple
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnVerdictPlausibility.String() {
			existing = triple
		}
	}
	if existing.Predicate == "" {
		t.Fatal("the turn carries no plausibility to duplicate")
	}
	existing.Context = "append-lane-negative-control"
	request, err := json.Marshal(graph.AddTriplesBatchRequest{Triples: []message.Triple{existing}})
	if err != nil {
		t.Fatalf("encode batch add: %v", err)
	}
	reply, err := world.harness.Client.RequestClassified(
		t.Context(), "graph.mutation.triple.add_batch", request, graphio.DefaultTimeout)
	if err != nil {
		t.Fatalf("the appending lane refused the write, so the control proves nothing: %v", err)
	}
	var response graph.AddTriplesBatchResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		t.Fatalf("decode append-lane response: %v", err)
	}
	if response.WrittenCount != 1 || response.Deduplicated != 0 || len(response.FailedSubjects) != 0 {
		t.Fatalf("distinct-context add response = %+v, want written=1 deduplicated=0 and no failures", response)
	}
}

// The narration's own F14 guard, plus the durability claim: the prose is
// readable by a store handle that never wrote it, through the reference the turn
// carries.
func TestIntegration_NarrationLandsOnePointerAndTheProseSurvives(t *testing.T) {
	world := startLive(t)
	const prose = "The gate comes up smooth and quiet, and for four long strides nobody in the yard is looking north."

	for attempt := range 2 {
		result, err := world.narration.Execute(context.Background(), call(
			persona.NarrationToolName,
			arguments(t, fmt.Sprintf(`{"prose": %q}`, prose)),
			world.metadata(vocabulary.BandFull),
		))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if result.ErrorKind != "" {
			t.Fatalf("attempt %d was refused: %s", attempt, result.Error)
		}
	}

	state := world.harness.AwaitEntity(t, world.entityID)
	refs := testinfra.ObjectsFor(state, vocabulary.TurnNarrationRef.String())
	if len(refs) != 1 {
		t.Fatalf("after two exits the turn holds %d narration references: %v", len(refs), refs)
	}
	// Nothing else landed. The narrator's whole mark on the graph is a pointer.
	if got := len(state.Triples); got < 2 {
		t.Fatalf("the turn carries %d triples, so the phase or the reference is missing", got)
	}

	ref, err := content.ParseRef(fmt.Sprint(refs[0]))
	if err != nil {
		t.Fatalf("the recorded reference is not resolvable: %v", err)
	}

	// A genuinely fresh handle with a cold cache: a cached read would prove only
	// that this process remembers what it just wrote.
	reader := freshContentStore(t, world.harness.Client, ref.Instance)
	stored, err := reader.GetNarration(t.Context(), ref)
	if err != nil {
		t.Fatalf("the prose is not readable through the reference the turn carries: %v", err)
	}
	if stored.Prose != prose {
		t.Fatalf("the stored prose is %q", stored.Prose)
	}
	if stored.Band != vocabulary.BandFull {
		t.Fatalf("the stored narration is filed under band %q", stored.Band)
	}
	if stored.TurnID != world.turnID {
		t.Fatalf("the stored narration is filed under turn %q", stored.TurnID)
	}
}

func freshContentStore(t *testing.T, client *natsclient.Client, bucket string) *content.Store {
	t.Helper()
	backend, err := content.NewObjectStore(t.Context(), client, content.WithBucket(bucket))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	store, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// The resume check against the graph it actually reads: a stage whose verdict is
// on the turn is skipped, and one whose narration is not is run. Both answers
// come from a real query surface rather than from a map that cannot be a stub or
// return two values.
func TestIntegration_TheResumeCheckReadsTheArtifactTheGraphActuallyHolds(t *testing.T) {
	world := startLive(t)
	guard, err := persona.NewGuard(world.graph)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	before, err := guard.Check(t.Context(), persona.Adjudicator(), world.turnID, world.entityID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if before.Decision != persona.DecisionRun {
		t.Fatalf("a turn with no verdict was answered %q", before.Decision)
	}

	result, err := world.verdict.Execute(context.Background(),
		call(persona.VerdictToolName, world.verdictArguments(t), world.metadata("")))
	if err != nil || result.ErrorKind != "" {
		t.Fatalf("the verdict did not land: %v / %s", err, result.Error)
	}
	world.harness.AwaitEntity(t, world.entityID)

	after, err := guard.Check(t.Context(), persona.Adjudicator(), world.turnID, world.entityID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if after.Decision != persona.DecisionSkip {
		t.Fatalf("a turn whose verdict is on the graph was answered %q; the resumed stage would pay for the "+
			"same judgment twice", after.Decision)
	}
	if after.Ref.IsZero() {
		t.Fatal("the skip carries no reference to what the finished attempt produced")
	}
	stored, err := world.content.GetVerdict(t.Context(), after.Ref)
	if err != nil {
		t.Fatalf("the reference the resume check returned does not resolve: %v", err)
	}
	if stored.TurnID != world.turnID {
		t.Fatalf("the resolved verdict belongs to turn %q", stored.TurnID)
	}

	// The narrator's stage is still outstanding on the same turn, which is what
	// makes the check per-stage rather than "has this turn produced anything".
	narrator, err := guard.Check(t.Context(), persona.Narrator(), world.turnID, world.entityID)
	if err != nil {
		t.Fatalf("Check(narrator): %v", err)
	}
	if narrator.Decision != persona.DecisionRun {
		t.Fatal("the narrator was skipped on the strength of the adjudicator's work")
	}
}

// A rejected exit must leave the graph and the store exactly as it found them.
// Against a real broker this is the difference between a validator and a
// validator that has already written.
func TestIntegration_ARefusedExitLeavesTheWorldUntouched(t *testing.T) {
	world := startLive(t)
	before := world.harness.AwaitEntity(t, world.entityID)

	result, err := world.verdict.Execute(context.Background(), call(
		persona.VerdictToolName,
		arguments(t, `{
		  "scalars": {"plausibility": "vibes", "risk": "none", "consequence": "none", "requires_roll": false},
		  "bands": {"auto": []},
		  "rationale": "A class nobody registered."
		}`),
		world.metadata(""),
	))
	if err != nil {
		t.Fatalf("a correctable refusal returned a Go error: %v", err)
	}
	if result.ErrorKind != agentic.ToolErrorInvalidArgs {
		t.Fatalf("the exit was answered with %q", result.ErrorKind)
	}

	after := world.harness.AwaitEntity(t, world.entityID)
	if len(after.Triples) != len(before.Triples) {
		t.Fatalf("a refused verdict changed the turn from %d triples to %d",
			len(before.Triples), len(after.Triples))
	}
}
