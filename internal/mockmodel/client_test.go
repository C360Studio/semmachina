package mockmodel_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/model"
	agenticmodel "github.com/c360studio/semstreams/processor/agentic-model"

	"github.com/c360studio/semmachina/internal/mockmodel"
)

// The whole point of the stub is that the PRODUCTION model client reaches it.
// A test that only exercised a hand-rolled fake would prove the fixture format
// and nothing about the contract, so every case here goes through
// agentic-model's own Client — the same constructor the agentic-model
// component calls per endpoint.
func TestProductionSDKClient_ReceivesTheScriptedToolCall(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")
	client := newClient(t, handler.endpoint(adjudicatorModel))

	resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
	if err != nil {
		t.Fatalf("the production client could not talk to the stub: %v", err)
	}

	if resp.Status != agentic.StatusToolCall {
		t.Fatalf("status is %q, want %q — a scripted tool call must arrive as a tool call",
			resp.Status, agentic.StatusToolCall)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.Message.ToolCalls))
	}
	call := resp.Message.ToolCalls[0]
	if call.Name != "submit_verdict" {
		t.Fatalf("tool name is %q, want submit_verdict", call.Name)
	}
	scalars, ok := call.Arguments["scalars"].(map[string]any)
	if !ok {
		t.Fatalf("scripted arguments did not survive the wire: %#v", call.Arguments)
	}
	if scalars["plausibility"] != "plausible" {
		t.Fatalf("plausibility arrived as %#v, want %q", scalars["plausibility"], "plausible")
	}
}

// ADR-037's wire backend is the other production path to the same endpoint,
// and it is per-endpoint configuration rather than a code path a caller picks.
// If the stub only satisfied the SDK client, flipping one endpoint to "wire"
// would take CI down for reasons that have nothing to do with the game.
func TestProductionWireClient_ReceivesTheScriptedToolCall(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")
	endpoint := handler.endpoint(adjudicatorModel)
	endpoint.WireBackend = "wire"
	client := newClient(t, endpoint)

	resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
	if err != nil {
		t.Fatalf("the wire-backend client could not talk to the stub: %v", err)
	}
	if resp.Status != agentic.StatusToolCall {
		t.Fatalf("status is %q, want %q", resp.Status, agentic.StatusToolCall)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "submit_verdict" {
		t.Fatalf("tool calls arrived as %+v", resp.Message.ToolCalls)
	}
	if resp.TokenUsage.PromptTokens != 1200 {
		t.Fatalf("usage is %+v, want the scripted 1200 prompt tokens", resp.TokenUsage)
	}
}

// Token usage is scripted so that spend has something to measure in a
// token-free run (M7). A stub that returned no usage would make every CI turn
// look free, which is the one number the flat-cost-per-turn claim cannot
// afford to invent.
func TestProductionSDKClient_CarriesScriptedTokenUsage(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")
	client := newClient(t, handler.endpoint(adjudicatorModel))

	resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if resp.TokenUsage.PromptTokens != 1200 || resp.TokenUsage.CompletionTokens != 180 {
		t.Fatalf("token usage is %+v, want the scripted 1200/180", resp.TokenUsage)
	}

	totals := handler.Totals()
	if totals.PromptTokens != 1200 || totals.CompletionTokens != 180 || totals.Calls != 1 {
		t.Fatalf("stub totals are %+v, want one call at 1200/180", totals)
	}
	if totals.TotalTokens() != 1380 {
		t.Fatalf("total tokens is %d, want 1380", totals.TotalTokens())
	}
}

// F4: provider strict-mode is silently ignored on some runtimes, so the
// executor's own validation is the enforcement of record — and a stub that
// could only emit valid exits could not prove that enforcement works. Each
// case here is a shape 7.2's executor must reject, delivered through the real
// client so the executor will see exactly this.
func TestProductionSDKClient_DeliversBadOutputUnsanitised(t *testing.T) {
	t.Run("an out-of-vocabulary class arrives verbatim", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "out-of-vocabulary")
		client := newClient(t, handler.endpoint(adjudicatorModel))

		resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
		if err != nil {
			t.Fatalf("chat completion: %v", err)
		}
		scalars, ok := resp.Message.ToolCalls[0].Arguments["scalars"].(map[string]any)
		if !ok {
			t.Fatalf("arguments arrived as %#v", resp.Message.ToolCalls[0].Arguments)
		}
		if scalars["plausibility"] != "vibes" {
			t.Fatalf("plausibility arrived as %#v; the stub must not sanitise an invalid class into a "+
				"valid one, or the rejection path would never run", scalars["plausibility"])
		}
	})

	t.Run("arguments that are not JSON reach the loop as an empty object", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "malformed-arguments")
		client := newClient(t, handler.endpoint(adjudicatorModel))

		resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
		if err != nil {
			t.Fatalf("chat completion: %v", err)
		}
		// Documented framework behaviour, pinned here because the executor's
		// rejection reason depends on it: unparseable arguments are replaced
		// with an EMPTY map, so what 7.2 must reject is a call missing every
		// required field, not a call carrying broken text.
		if len(resp.Message.ToolCalls) != 1 {
			t.Fatalf("got %d tool calls, want 1", len(resp.Message.ToolCalls))
		}
		if len(resp.Message.ToolCalls[0].Arguments) != 0 {
			t.Fatalf("arguments are %#v, want the framework's empty-map substitution",
				resp.Message.ToolCalls[0].Arguments)
		}
	})

	t.Run("a prose answer arrives with no tool call at all", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "prose-instead-of-exit")
		client := newClient(t, handler.endpoint(adjudicatorModel))

		resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
		if err != nil {
			t.Fatalf("chat completion: %v", err)
		}
		if resp.Status != agentic.StatusComplete {
			t.Fatalf("status is %q, want %q", resp.Status, agentic.StatusComplete)
		}
		if len(resp.Message.ToolCalls) != 0 {
			t.Fatalf("a text step produced tool calls: %+v", resp.Message.ToolCalls)
		}
		if !strings.Contains(resp.Message.Content, "Roll a 7") {
			t.Fatalf("content is %q, want the scripted prose", resp.Message.Content)
		}
	})

	t.Run("a truncated exit loses its tool call", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "truncated-exit")
		client := newClient(t, handler.endpoint(adjudicatorModel))

		resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
		if err != nil {
			t.Fatalf("chat completion: %v", err)
		}
		if resp.Status != agentic.StatusLengthTruncated {
			t.Fatalf("status is %q, want %q", resp.Status, agentic.StatusLengthTruncated)
		}
		if len(resp.Message.ToolCalls) != 0 {
			t.Fatalf("the client kept %d tool call(s) from a truncated response; it is documented to drop "+
				"them, and a persona whose exit vanished is the shape 7.2 has to handle",
				len(resp.Message.ToolCalls))
		}
	})
}

// A scripted 5xx is retryable, so the client's real two-curve retry runs
// against the stub — and the script advancing per HTTP ATTEMPT (not per
// logical call) is what makes the recovery observable.
func TestProductionSDKClient_RetriesAScriptedProviderFailure(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "provider-failure")
	client := newClient(t, handler.endpoint(adjudicatorModel))

	resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1"))
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if resp.Status != agentic.StatusToolCall {
		t.Fatalf("status is %q (%s), want the retry to have reached the second step", resp.Status, resp.Error)
	}
	if resp.RetryCount != 1 {
		t.Fatalf("retry count is %d, want 1", resp.RetryCount)
	}

	calls := handler.Calls()
	if len(calls) != 2 {
		t.Fatalf("the stub saw %d calls, want 2 (the failure and the retry)", len(calls))
	}
	if calls[0].Status != 503 || calls[1].Status != 200 {
		t.Fatalf("statuses were %d then %d, want 503 then 200", calls[0].Status, calls[1].Status)
	}
}

// A fixture gap must fail ONCE and say why. 400 is the deliberate choice: the
// framework client treats 4xx as non-retryable, so an unroutable request does
// not burn three attempts and surface as a timeout with no cause.
func TestProductionSDKClient_SurfacesARefusalWithoutRetrying(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")
	client := newClient(t, handler.endpoint("some-model-nobody-scripted"))

	request := adjudicatorRequest("req-1")
	resp, err := client.ChatCompletion(t.Context(), request)
	if err != nil {
		t.Fatalf("the client returned a transport error rather than an error response: %v", err)
	}
	if resp.Status != agentic.StatusError {
		t.Fatalf("status is %q, want %q", resp.Status, agentic.StatusError)
	}
	if !strings.Contains(resp.Error, string(mockmodel.FailureUnroutable)) {
		t.Fatalf("the error the loop sees is %q; it must carry the failure code, since a client flattens "+
			"a provider error to its message", resp.Error)
	}
	if calls := handler.Calls(); len(calls) != 1 {
		t.Fatalf("the stub saw %d calls; a refusal must not be retried", len(calls))
	}
	if refusal := handler.Calls()[0].Refusal; refusal != mockmodel.FailureUnroutable {
		t.Fatalf("the recorded refusal is %q, want %q", refusal, mockmodel.FailureUnroutable)
	}
}

// Running off the end of a script is the finding in a duplicate-delivery or
// crash-resume test: the loop called the model a second time for a turn that
// had already been judged. It has to be a refusal naming the role, never a
// second helpful answer.
func TestProductionSDKClient_RefusesASecondCallTheScenarioDidNotBudgetFor(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")
	client := newClient(t, handler.endpoint(adjudicatorModel))

	if _, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-1")); err != nil {
		t.Fatalf("first call: %v", err)
	}
	resp, err := client.ChatCompletion(t.Context(), adjudicatorRequest("req-2"))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if resp.Status != agentic.StatusError {
		t.Fatalf("the second call was answered with status %q; a scenario that budgets one adjudication "+
			"must not silently pay for two", resp.Status)
	}
	if !strings.Contains(resp.Error, string(mockmodel.FailureExhausted)) {
		t.Fatalf("the error is %q, want it to name %q", resp.Error, mockmodel.FailureExhausted)
	}
	if !strings.Contains(resp.Error, "adjudicator") {
		t.Fatalf("the error is %q, want it to name the exhausted role", resp.Error)
	}
}

// "A live model is a model_registry retarget" is only true if the stub's
// endpoint is an ordinary registry entry. Validating it through the real
// registry is how that claim stops being an assertion.
func TestStubEndpoint_IsAValidModelRegistryEntry(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")

	registry := &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"mock-adjudicator": handler.endpoint(adjudicatorModel),
			"mock-narrator":    handler.endpoint(narratorModel),
		},
		Capabilities: map[string]*model.CapabilityConfig{
			"fiction_adjudication": {Preferred: []string{"mock-adjudicator"}, RequiresTools: true},
			"narration":            {Preferred: []string{"mock-narrator"}, RequiresTools: true},
		},
		Defaults: model.DefaultsConfig{Model: "mock-adjudicator"},
	}
	if err := registry.Validate(); err != nil {
		t.Fatalf("the stub's endpoint config is not a valid registry entry: %v", err)
	}

	resolved, err := model.ResolveEndpoint(registry, "fiction_adjudication")
	if err != nil {
		t.Fatalf("resolve the adjudication capability: %v", err)
	}
	if resolved.Model != adjudicatorModel {
		t.Fatalf("the capability resolved to model %q, want %q", resolved.Model, adjudicatorModel)
	}
	if !strings.HasSuffix(resolved.URL, mockmodel.BasePath) {
		t.Fatalf("the resolved URL is %q; a model client appends /chat/completions to it, so it must end "+
			"at the OpenAI-compatible root", resolved.URL)
	}
}

// helpers ------------------------------------------------------------------

const (
	adjudicatorModel = "semmachina-mock-adjudicator"
	narratorModel    = "semmachina-mock-narrator"
)

type stub struct {
	*mockmodel.Handler
	baseURL string
}

func (s *stub) endpoint(name string) *model.EndpointConfig {
	return mockmodel.EndpointConfig(s.baseURL, name)
}

func serve(t *testing.T, fixture *mockmodel.Fixture, scenario string) *stub {
	t.Helper()
	handler, err := mockmodel.New(fixture, scenario)
	if err != nil {
		t.Fatalf("build the stub: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &stub{Handler: handler, baseURL: server.URL}
}

func newClient(t *testing.T, endpoint *model.EndpointConfig) *agenticmodel.Client {
	t.Helper()
	client, err := agenticmodel.NewClient(endpoint)
	if err != nil {
		t.Fatalf("build the production model client: %v", err)
	}
	return client
}

func adjudicatorRequest(id string) agentic.AgentRequest {
	return agentic.AgentRequest{
		RequestID: id,
		Model:     adjudicatorModel,
		Messages: []agentic.ChatMessage{
			{Role: "system", Content: "You judge fiction."},
			{Role: "user", Content: "Rook levers the gate with the crowbar."},
		},
		Tools: []agentic.ToolDefinition{{
			Name:        "submit_verdict",
			Description: "Exit with the banded verdict.",
			Parameters:  map[string]any{"type": "object"},
		}},
	}
}
