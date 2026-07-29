package persona_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/model"
	agenticmodel "github.com/c360studio/semstreams/processor/agentic-model"

	"github.com/c360studio/semmachina/internal/mockmodel"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The endpoint names a deployment wires the two capabilities to. They are
// configuration, which is the whole point: the same names hold a live model.
const (
	mockAdjudicatorModel = "semmachina-mock-adjudicator"
	mockNarratorModel    = "semmachina-mock-narrator"
)

// stub is a running mock model endpoint plus the registry that points at it.
type stub struct {
	handler  *mockmodel.Handler
	registry *model.Registry
}

// serveScenario starts the shipped scenario pack on a real HTTP server and
// wires it into a real model registry.
//
// Everything below the executor is production: the framework's own model client
// speaks to a real socket, the endpoint is resolved through the real registry by
// CAPABILITY, and the tool call that comes back is decoded by the client rather
// than assembled by a test. That is what makes these tests say something about
// what a provider does to an exit on the way here — the client normalises token
// totals, infers a tool call regardless of finish reason, and substitutes an
// empty argument map for arguments it cannot parse, and the executor has to
// survive all three.
func serveScenario(t *testing.T, scenario string) *stub {
	t.Helper()
	fixture, err := mockmodel.TurnLoopScenarios()
	if err != nil {
		t.Fatalf("load the shipped scenario pack: %v", err)
	}
	handler, err := mockmodel.New(fixture, scenario)
	if err != nil {
		t.Fatalf("start scenario %q: %v", scenario, err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	registry := &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"mock-adjudicator": mockmodel.EndpointConfig(server.URL, mockAdjudicatorModel),
			"mock-narrator":    mockmodel.EndpointConfig(server.URL, mockNarratorModel),
		},
		Capabilities: map[string]*model.CapabilityConfig{
			persona.CapabilityAdjudication: {Preferred: []string{"mock-adjudicator"}, RequiresTools: true},
			persona.CapabilityNarration:    {Preferred: []string{"mock-narrator"}, RequiresTools: true},
		},
		Defaults: model.DefaultsConfig{Model: "mock-adjudicator"},
	}
	if err := registry.Validate(); err != nil {
		t.Fatalf("the token-free deployment is not a valid model registry: %v", err)
	}
	return &stub{handler: handler, registry: registry}
}

// ask spawns one persona against the stub and returns what the production model
// client decoded.
func (s *stub) ask(t *testing.T, spec persona.Spec, band vocabulary.OutcomeBand) agentic.AgentResponse {
	t.Helper()
	task, err := spec.Task(persona.TaskRequest{
		Identity: testIdentity(),
		Band:     band,
		Prompt:   "the assembled world, rendered",
	})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	resolved, endpoint, err := model.ResolveEndpointWithConfig(s.registry, spec.Capability)
	if err != nil {
		t.Fatalf("resolve %q: %v", spec.Capability, err)
	}
	client, err := agenticmodel.NewClient(endpoint)
	if err != nil {
		t.Fatalf("build the production model client: %v", err)
	}
	response, err := client.ChatCompletion(t.Context(), agentic.AgentRequest{
		RequestID:  "req-" + task.TaskID,
		LoopID:     "loop-1",
		Role:       task.Role,
		Model:      resolved.Model,
		Messages:   []agentic.ChatMessage{{Role: "user", Content: task.Prompt}},
		Tools:      task.Tools,
		ToolChoice: task.ToolChoice,
	})
	if err != nil {
		t.Fatalf("the production client could not reach the stub: %v", err)
	}
	return response
}

// dispatch hands a decoded tool call to an executor exactly as the agentic loop
// does: the model's arguments, the engine's metadata.
func dispatch(
	t *testing.T,
	executor interface {
		Execute(context.Context, agentic.ToolCall) (agentic.ToolResult, error)
	},
	spec persona.Spec,
	band vocabulary.OutcomeBand,
	response agentic.AgentResponse,
) (agentic.ToolResult, error) {
	t.Helper()
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("the model answered with %d tool calls (status %q), want one exit",
			len(response.Message.ToolCalls), response.Status)
	}
	toolCall := response.Message.ToolCalls[0]
	toolCall.Metadata = injected(t, spec, band)
	toolCall.LoopID = "loop-1"
	return executor.Execute(context.Background(), toolCall)
}

// ------------------------------------------------------- the happy path

// The whole loop from a socket to a triple, token-free: a scripted verdict
// crosses the wire, is decoded by the framework's client, is validated by the
// executor, and lands on the turn.
func TestWire_AScriptedVerdictReachesTheGraphThroughProductionCode(t *testing.T) {
	stub := serveScenario(t, "no-roll")
	spec := persona.Adjudicator()
	h := newVerdictHarness(t)

	result, err := dispatch(t, h.executor, spec, "", stub.ask(t, spec, ""))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != "" {
		t.Fatalf("the scripted verdict was refused: %s", result.Error)
	}
	if got := firstObject(t, h.graph.entities[testTurnEntityID], vocabulary.TurnVerdictPlausibility); got !=
		string(vocabulary.PlausibilityCertain) {
		t.Fatalf("the turn records plausibility %#v", got)
	}
	if got := firstObject(t, h.graph.entities[testTurnEntityID], vocabulary.TurnVerdictRequiresRoll); got != false {
		t.Fatalf("the turn records requires-roll as %#v", got)
	}
	// One turn, one adjudication. The script budgets exactly that, so a second
	// call would be a refusal rather than a second billed answer.
	if calls := stub.handler.CallsFor("adjudicator"); len(calls) != 1 {
		t.Fatalf("the stub answered %d adjudications for one turn", len(calls))
	}
	if totals := stub.handler.Totals(); totals.TotalTokens() == 0 {
		t.Fatal("the run reports zero tokens; a token-free CI run still has to be able to measure spend")
	}
}

func TestWire_AScriptedNarrationReachesTheGraphThroughProductionCode(t *testing.T) {
	stub := serveScenario(t, "no-roll")
	spec := persona.Narrator()
	h := newNarrationHarness(t, vocabulary.BandAuto)

	result, err := dispatch(t, h.executor, spec, vocabulary.BandAuto, stub.ask(t, spec, vocabulary.BandAuto))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != "" {
		t.Fatalf("the scripted narration was refused: %s", result.Error)
	}
	ref := h.artifacts.ref(t, vocabulary.TurnNarrationRef, testTurnID)
	stored := h.artifacts.narrations[ref.Key]
	if stored == nil || !strings.Contains(stored.Prose, "bread") {
		t.Fatalf("the scripted prose is not what was stored: %+v", stored)
	}
	if got := firstObject(t, h.graph.entities[testTurnEntityID], vocabulary.TurnNarrationRef); got != ref.String() {
		t.Fatalf("the turn records %#v for the narration reference", got)
	}
}

// ------------------------------------------------------- the rejection paths

// The scenario that exists to prove a refusal is a RETRY and not a dead turn:
// the pack's first adjudicator step declares a band set its own roll gate does
// not call for, and its second is the correction. Nothing in a JSON Schema can
// express the constraint the first one breaks — it is a relationship between two
// fields — so this is the case F4 says provider strict-mode will not catch.
func TestWire_AMalformedExitIsRefusedWithACorrectionTheModelThenSatisfies(t *testing.T) {
	stub := serveScenario(t, "malformed-exit")
	spec := persona.Adjudicator()
	h := newVerdictHarness(t)

	first, err := dispatch(t, h.executor, spec, "", stub.ask(t, spec, ""))
	if err != nil {
		t.Fatalf("the first exit returned a Go error rather than a correction: %v", err)
	}
	if first.ErrorKind != agentic.ToolErrorInvalidArgs {
		t.Fatalf("the incoherent exit was answered with %q (%s)", first.ErrorKind, first.Error)
	}
	if h.graph.merges != 0 {
		t.Fatal("a refused verdict reached the graph")
	}

	second, err := dispatch(t, h.executor, spec, "", stub.ask(t, spec, ""))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if second.ErrorKind != "" {
		t.Fatalf("the corrected exit was refused too: %s", second.Error)
	}
	if !second.StopLoop {
		t.Fatal("the corrected exit did not stop the loop")
	}
	// Two model calls for one turn, which is what a correction round costs, and
	// it is inside the adjudicator's budget.
	if calls := stub.handler.CallsFor("adjudicator"); len(calls) != 2 {
		t.Fatalf("the correction round took %d calls", len(calls))
	}
	if 2 > persona.AdjudicatorMaxIterations {
		t.Fatal("the adjudicator's iteration budget cannot afford a single correction round")
	}
}

// The shipped pack's invalid-effect scenario proposes an effect type nobody
// registered. It is refused HERE, at the tool boundary, and never reaches the
// applier — the content store will not store a verdict that cannot pass its own
// contract, so the choice is between refusing at the exit and storing an invalid
// artifact.
func TestWire_AnOutOfVocabularyEffectIsRefusedAtTheExit(t *testing.T) {
	stub := serveScenario(t, "invalid-effect")
	spec := persona.Adjudicator()
	h := newVerdictHarness(t)

	result, err := dispatch(t, h.executor, spec, "", stub.ask(t, spec, ""))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != agentic.ToolErrorInvalidArgs {
		t.Fatalf("an out-of-vocabulary effect type was accepted: %q", result.ErrorKind)
	}
	if h.artifacts.verdictPuts != 0 || h.graph.merges != 0 {
		t.Fatal("a verdict proposing an unregistered effect type was written somewhere")
	}
	for _, want := range []string{"teleport_entity", string(vocabulary.EffectMoveEntity)} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("the correction is %q, and does not mention %q", result.Error, want)
		}
	}
}

// A persona that answers in prose never calls its terminal tool at all. There is
// nothing for an executor to reject, which is exactly why this is a scenario:
// the engine's answer is the iteration budget and the explicit failure at the
// end of it, not a validator.
func TestWire_AProseAnswerProducesNoExitForTheExecutorToJudge(t *testing.T) {
	stub := serveScenario(t, "prose-instead-of-exit")
	spec := persona.Adjudicator()

	response := stub.ask(t, spec, "")
	if len(response.Message.ToolCalls) != 0 {
		t.Fatalf("a text-only completion produced %d tool calls", len(response.Message.ToolCalls))
	}
	if response.Status != agentic.StatusComplete {
		t.Fatalf("the response status is %q", response.Status)
	}
	if response.Message.Content == "" {
		t.Fatal("the scripted prose did not survive the wire")
	}
}

// The mock's repeating step answers forever, which is the only way to tell a cap
// that works from a cap that was never reached: the persona reaches for context
// it will never get and never exits. The loop's enforcement is upstream's; what
// this proves is that the budget we hand it is the ONLY thing standing between
// this persona and an unbounded bill.
func TestWire_APersonaThatNeverTerminatesIsStoppedOnlyByItsBudget(t *testing.T) {
	stub := serveScenario(t, "never-terminates")
	spec := persona.Adjudicator()

	for iteration := range spec.MaxIterations {
		response := stub.ask(t, spec, "")
		if response.Status != agentic.StatusToolCall {
			t.Fatalf("iteration %d answered with %q; the scenario is supposed to keep going forever",
				iteration, response.Status)
		}
		if len(response.Message.ToolCalls) != 1 {
			t.Fatalf("iteration %d produced %d tool calls", iteration, len(response.Message.ToolCalls))
		}
		if name := response.Message.ToolCalls[0].Name; name == spec.Tool.Name {
			t.Fatalf("iteration %d called the terminal tool %q; this scenario must never terminate",
				iteration, name)
		}
	}
	if calls := stub.handler.CallsFor("adjudicator"); len(calls) != spec.MaxIterations {
		t.Fatalf("the stub answered %d times, want the budget's %d", len(calls), spec.MaxIterations)
	}

	// And the explicit failure at the end of the budget, which is what turns a
	// stalled turn into a resolved one.
	artifacts := newFakeArtifacts(&journal{})
	failer := &fakeFailer{}
	transition, err := persona.RecordCapExhausted(t.Context(), failer, artifacts, testIdentity(),
		persona.CapExhaustion{Role: spec.Role, LoopID: "loop-1", Iterations: spec.MaxIterations})
	if err != nil {
		t.Fatalf("RecordCapExhausted: %v", err)
	}
	if transition.Phase != vocabulary.PhaseFailed || failer.reason != vocabulary.FailurePersonaCapExhausted {
		t.Fatalf("the exhausted turn ended as %q/%q", transition.Phase, failer.reason)
	}
}

// ------------------------------------------------- provider-shaped damage

// F18: the framework client substitutes an EMPTY argument map for arguments it
// cannot parse. So a truncated or malformed exit does not reach the executor as
// broken text — it reaches it as a call with nothing in it, and the correction
// has to make sense for that.
func TestWire_ArgumentsNoEncoderWouldProduceArriveEmptyAndAreRefused(t *testing.T) {
	stub := serveLocal(t, damagedFixture(t), "malformed-arguments")
	spec := persona.Adjudicator()
	h := newVerdictHarness(t)

	response := stub.ask(t, spec, "")
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("the damaged call produced %d tool calls", len(response.Message.ToolCalls))
	}
	if got := len(response.Message.ToolCalls[0].Arguments); got != 0 {
		t.Fatalf("the client passed %d arguments through; it is documented to substitute an empty map, and "+
			"the executor's correction depends on that", got)
	}

	result, err := dispatch(t, h.executor, spec, "", response)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorKind != agentic.ToolErrorInvalidArgs {
		t.Fatalf("an empty argument map was answered with %q", result.ErrorKind)
	}
	if !strings.Contains(result.Error, "cut off") {
		t.Fatalf("the correction is %q; a model whose call was truncated needs to be told to emit the whole "+
			"call again, not that a field is missing", result.Error)
	}
	if h.graph.merges != 0 {
		t.Fatal("an empty exit wrote something")
	}
}

// A truncated response loses its tool calls entirely — the client drops them
// rather than trusting half-written arguments — so a persona whose exit was cut
// off looks identical to one that never exited, and the iteration budget is
// again the thing that ends it.
func TestWire_ATruncatedExitArrivesWithNoToolCallAtAll(t *testing.T) {
	stub := serveLocal(t, damagedFixture(t), "truncated-exit")
	response := stub.ask(t, persona.Adjudicator(), "")

	if response.Status != agentic.StatusLengthTruncated {
		t.Fatalf("the response status is %q, want %q", response.Status, agentic.StatusLengthTruncated)
	}
	if len(response.Message.ToolCalls) != 0 {
		t.Fatalf("a truncated response kept %d tool calls", len(response.Message.ToolCalls))
	}
}

// serveLocal runs a fixture the shipped pack does not carry.
func serveLocal(t *testing.T, fixture *mockmodel.Fixture, scenario string) *stub {
	t.Helper()
	handler, err := mockmodel.New(fixture, scenario)
	if err != nil {
		t.Fatalf("start scenario %q: %v", scenario, err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &stub{
		handler: handler,
		registry: &model.Registry{
			Endpoints: map[string]*model.EndpointConfig{
				"mock-adjudicator": mockmodel.EndpointConfig(server.URL, mockAdjudicatorModel),
			},
			Capabilities: map[string]*model.CapabilityConfig{
				persona.CapabilityAdjudication: {Preferred: []string{"mock-adjudicator"}, RequiresTools: true},
			},
			Defaults: model.DefaultsConfig{Model: "mock-adjudicator"},
		},
	}
}

// damagedFixture scripts the two shapes no JSON encoder would produce. They are
// not in the shipped pack because they are not turn-loop SCENARIOS — nothing
// about the game changes — they are provider damage, and what they test is the
// executor's behaviour on the far side of a tolerant client.
func damagedFixture(t *testing.T) *mockmodel.Fixture {
	t.Helper()
	fixture, err := mockmodel.ParseFixture([]byte(`{
	  "description": "Provider damage, for the executor's rejection paths.",
	  "roles": [{"name": "adjudicator", "match": {"model": "semmachina-mock-adjudicator"}}],
	  "scenarios": [
	    {
	      "name": "malformed-arguments",
	      "scripts": [{
	        "role": "adjudicator",
	        "steps": [{
	          "kind": "tool_call",
	          "usage": {"prompt_tokens": 1240, "completion_tokens": 40},
	          "tool_calls": [{"name": "submit_verdict", "arguments_raw": "{\"scalars\": {\"plausibility\": \"plau"}]
	        }]
	      }]
	    },
	    {
	      "name": "truncated-exit",
	      "scripts": [{
	        "role": "adjudicator",
	        "steps": [{
	          "kind": "truncated",
	          "usage": {"prompt_tokens": 1240, "completion_tokens": 512},
	          "tool_calls": [{"name": "submit_verdict", "arguments": {"scalars": {"plausibility": "plausible"}}}]
	        }]
	      }]
	    }
	  ]
	}`))
	if err != nil {
		t.Fatalf("the damaged fixture does not load: %v", err)
	}
	return fixture
}

// The token-free deployment has to satisfy the same boot gate a live one does,
// or the gate is only ever exercised against configurations that were going to
// work anyway.
func TestWire_TheTokenFreeDeploymentPassesTheBootGate(t *testing.T) {
	stub := serveScenario(t, "no-roll")
	if err := persona.CheckRegistryAll(stub.registry); err != nil {
		t.Fatalf("the mock deployment does not satisfy the persona boot gate: %v", err)
	}
	for _, spec := range persona.Specs() {
		resolved, err := model.ResolveEndpoint(stub.registry, spec.Capability)
		if err != nil {
			t.Fatalf("resolve %q: %v", spec.Capability, err)
		}
		if !strings.HasPrefix(resolved.Model, "semmachina-mock-") {
			t.Fatalf("persona %q resolved to model %q", spec.Role, resolved.Model)
		}
	}
	// A live run is a registry retarget and nothing else, so the timeout the
	// spec carries has to be one the framework can actually parse.
	for _, spec := range persona.Specs() {
		if _, err := time.ParseDuration(spec.Timeout.String()); err != nil {
			t.Fatalf("persona %q carries an unparseable timeout: %v", spec.Role, err)
		}
	}
}
