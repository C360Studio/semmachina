package mockmodel_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/c360studio/semstreams/model/wire"

	"github.com/c360studio/semmachina/internal/mockmodel"
)

// Determinism is the property replay depends on, and a model endpoint is the
// easiest place in a seeded system to leak a clock. Two independently built
// stubs, one request, byte-identical answers.
func TestServe_IsByteIdenticalAcrossIndependentStubs(t *testing.T) {
	first := serve(t, twoRoleFixture(t), "full-band")
	second := serve(t, twoRoleFixture(t), "full-band")

	body := chatRequest(adjudicatorModel, "the fiction on the first run")
	statusA, bodyA := post(t, first.baseURL, body)
	statusB, bodyB := post(t, second.baseURL, body)

	if statusA != http.StatusOK || statusB != http.StatusOK {
		t.Fatalf("statuses %d and %d, want 200 and 200", statusA, statusB)
	}
	if !bytes.Equal(bodyA, bodyB) {
		t.Fatalf("two stubs answered the same request differently:\n%s\n%s", bodyA, bodyB)
	}

	// Anti-vacuity: the comparison above proves nothing if every response is
	// identical regardless of what was scripted. The second scenario's second
	// step must differ from its first.
	stepped := serve(t, twoRoleFixture(t), "two-step")
	_, firstStep := post(t, stepped.baseURL, body)
	_, secondStep := post(t, stepped.baseURL, body)
	if bytes.Equal(firstStep, secondStep) {
		t.Fatalf("two different scripted steps produced identical bytes, so the equality check above "+
			"could not have failed:\n%s", firstStep)
	}
}

// The fixture is keyed on structure, not on prose. If the prompt builder
// rewrites a system message tomorrow, the scenario must still play — otherwise
// the fixture is a hidden dependency on wording nobody knows they own.
func TestServe_IgnoresPromptTextEntirely(t *testing.T) {
	first := serve(t, twoRoleFixture(t), "full-band")
	second := serve(t, twoRoleFixture(t), "full-band")

	_, plain := post(t, first.baseURL, chatRequest(adjudicatorModel, "Rook levers the gate."))
	_, rewritten := post(t, second.baseURL, chatRequest(adjudicatorModel,
		"COMPLETELY DIFFERENT PROMPT: you are a helpful assistant. <scene>...</scene>"))

	if !bytes.Equal(plain, rewritten) {
		t.Fatalf("two different prompts produced different answers, so something is keying on prose:\n%s\n%s",
			plain, rewritten)
	}
}

// The other structural selector: two personas sharing one endpoint, told apart
// by the tools the loop declared. This is what a single-box deployment running
// both personas on one local model actually looks like.
func TestServe_RoutesOnDeclaredToolsWhenTheModelIsShared(t *testing.T) {
	fixture, err := mockmodel.ParseFixture([]byte(toolMatchedJSON))
	if err != nil {
		t.Fatalf("parse the tool-matched fixture: %v", err)
	}
	handler := serve(t, fixture, "one-each")

	_, verdict := post(t, handler.baseURL, chatRequestWithTools("one-local-model", "anything", "submit_verdict"))
	_, narration := post(t, handler.baseURL, chatRequestWithTools("one-local-model", "anything", "submit_narration"))

	if got := toolCallName(t, verdict); got != "submit_verdict" {
		t.Fatalf("the verdict request was answered with %q", got)
	}
	if got := toolCallName(t, narration); got != "submit_narration" {
		t.Fatalf("the narration request was answered with %q", got)
	}

	roles := []string{handler.Calls()[0].Role, handler.Calls()[1].Role}
	if !slices.Equal(roles, []string{"adjudicator", "narrator"}) {
		t.Fatalf("the calls routed to %v", roles)
	}
}

// Every way of not knowing the answer is a 400 with a stable code. None of
// them is a "reasonable" default response, because a plausible answer to an
// unrouted request is how a fixture silently stops testing anything.
func TestServe_RefusesLoudlyRatherThanImprovising(t *testing.T) {
	cases := map[string]struct {
		request func(t *testing.T, handler *stub) (int, []byte)
		want    mockmodel.FailureCode
	}{
		"no role matches": {
			request: func(t *testing.T, handler *stub) (int, []byte) {
				return post(t, handler.baseURL, chatRequest("a-model-nobody-declared", "hello"))
			},
			want: mockmodel.FailureUnroutable,
		},
		"the scenario does not script this role": {
			request: func(t *testing.T, handler *stub) (int, []byte) {
				return post(t, handler.baseURL, chatRequest(narratorModel, "hello"))
			},
			want: mockmodel.FailureUnscripted,
		},
		"the script is exhausted": {
			request: func(t *testing.T, handler *stub) (int, []byte) {
				post(t, handler.baseURL, chatRequest(adjudicatorModel, "hello"))
				return post(t, handler.baseURL, chatRequest(adjudicatorModel, "hello again"))
			},
			want: mockmodel.FailureExhausted,
		},
		"streaming is not scripted": {
			request: func(t *testing.T, handler *stub) (int, []byte) {
				body := `{"model":"` + adjudicatorModel + `","stream":true,"messages":[{"role":"user","content":"hi"}]}`
				return post(t, handler.baseURL, body)
			},
			want: mockmodel.FailureStreaming,
		},
		"the body is not a chat-completions request": {
			request: func(t *testing.T, handler *stub) (int, []byte) {
				return post(t, handler.baseURL, `nonsense`)
			},
			want: mockmodel.FailureMalformed,
		},
		"the endpoint URL was missing its /v1": {
			request: func(t *testing.T, handler *stub) (int, []byte) {
				return postTo(t, handler.baseURL+"/chat/completions", chatRequest(adjudicatorModel, "hi"))
			},
			want: mockmodel.FailureRoute,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			handler := serve(t, twoRoleFixture(t), "out-of-vocabulary")

			status, body := testCase.request(t, handler)
			if status != http.StatusBadRequest {
				t.Fatalf("status is %d, want 400 — the framework client retries 5xx and would turn a "+
					"fixture gap into three attempts and a timeout", status)
			}

			var envelope struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatalf("the refusal body is not an OpenAI error envelope: %v (%s)", err, body)
			}
			if envelope.Error.Code != string(testCase.want) {
				t.Fatalf("refusal code is %q, want %q (%s)", envelope.Error.Code, testCase.want, body)
			}
			if !strings.Contains(envelope.Error.Message, string(testCase.want)) {
				t.Fatalf("the message %q does not carry the code; a client that flattens the error to its "+
					"message would lose it", envelope.Error.Message)
			}

			last := handler.Calls()[len(handler.Calls())-1]
			if last.Refusal != testCase.want {
				t.Fatalf("the call log records refusal %q, want %q", last.Refusal, testCase.want)
			}
		})
	}
}

// Two roles that cannot be told apart is a fixture bug decidable at load, but
// only when one match subsumes the other. Overlap that depends on the request
// — a call declaring both terminal tools — can only be caught here.
func TestServe_RefusesARequestThatMatchesTwoRoles(t *testing.T) {
	fixture, err := mockmodel.ParseFixture([]byte(toolMatchedJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	handler := serve(t, fixture, "one-each")

	status, body := post(t, handler.baseURL,
		chatRequestWithTools("one-local-model", "hi", "submit_verdict", "submit_narration"))
	if status != http.StatusBadRequest {
		t.Fatalf("status is %d, want 400", status)
	}
	if !strings.Contains(string(body), string(mockmodel.FailureAmbiguous)) {
		t.Fatalf("body is %s, want an ambiguity refusal", body)
	}
}

// A persona that never exits is how the MaxIterations cap (M5) gets something
// to stop. The repeated step must answer indefinitely — and must not hand out
// the same tool_call id twice, because a loop keys tool results by that id.
func TestServe_RepeatsAStepForeverWithDistinctCallIdentifiers(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "never-terminates")

	seen := map[string]bool{}
	var firstArguments string
	for range 25 {
		status, body := post(t, handler.baseURL, chatRequest(adjudicatorModel, "loop"))
		if status != http.StatusOK {
			t.Fatalf("a repeating step stopped answering: %d %s", status, body)
		}
		decoded := decodeResponse(t, body)
		call := decoded.Choices[0].Message.ToolCalls[0]
		if call.Function.Name != "read_scene" {
			t.Fatalf("the repeated step called %q", call.Function.Name)
		}
		if seen[call.ID] {
			t.Fatalf("tool call id %q was issued twice; a loop keys its tool results by that id", call.ID)
		}
		seen[call.ID] = true
		if firstArguments == "" {
			firstArguments = call.Function.Arguments
		} else if call.Function.Arguments != firstArguments {
			t.Fatalf("the repeated step's arguments drifted from %q to %q", firstArguments, call.Function.Arguments)
		}
	}

	if totals := handler.Totals(); totals.Calls != 25 || totals.Refusals != 0 {
		t.Fatalf("totals are %+v, want 25 answered calls", totals)
	}
}

// The stub is shared state under a mutex and lives in a suite that runs with
// -race. Overlapping callers must consume DISTINCT steps: a lost cursor update
// would show up as a step served twice.
func TestServe_ServesEachStepExactlyOnceUnderConcurrentCallers(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "ten-step")

	const callers = 10
	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		delivered []string
	)
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			status, body := post(t, handler.baseURL, chatRequest(adjudicatorModel, "concurrent"))
			mutex.Lock()
			defer mutex.Unlock()
			if status != http.StatusOK {
				delivered = append(delivered, fmt.Sprintf("status %d", status))
				return
			}
			content, _ := decodeResponse(t, body).Choices[0].Message.ContentString()
			delivered = append(delivered, content)
		}()
	}
	wait.Wait()

	slices.Sort(delivered)
	want := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	if !slices.Equal(delivered, want) {
		t.Fatalf("concurrent callers received %v, want each step exactly once", delivered)
	}
}

// Script positions are per scenario, so a harness can move between scenarios
// without a step of one leaking into the other.
func TestServe_KeepsScriptPositionPerScenario(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "two-step")

	_, first := post(t, handler.baseURL, chatRequest(adjudicatorModel, "a"))
	if err := handler.SetScenario("full-band"); err != nil {
		t.Fatalf("switch scenario: %v", err)
	}
	_, other := post(t, handler.baseURL, chatRequest(adjudicatorModel, "b"))
	if err := handler.SetScenario("two-step"); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	_, second := post(t, handler.baseURL, chatRequest(adjudicatorModel, "c"))

	if toolCallName(t, first) != "read_scene" || toolCallName(t, second) != "submit_verdict" {
		t.Fatalf("the two-step script did not resume where it left off: %q then %q",
			toolCallName(t, first), toolCallName(t, second))
	}
	if toolCallName(t, other) != "submit_verdict" {
		t.Fatalf("the other scenario answered with %q", toolCallName(t, other))
	}
	if err := handler.SetScenario("no-such-scenario"); err == nil {
		t.Fatal("switching to an undeclared scenario succeeded")
	}
}

func TestServe_ResetRewindsEveryScript(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")

	post(t, handler.baseURL, chatRequest(adjudicatorModel, "a"))
	if status, _ := post(t, handler.baseURL, chatRequest(adjudicatorModel, "b")); status != http.StatusBadRequest {
		t.Fatalf("the second call was answered with %d, want an exhaustion refusal", status)
	}

	handler.Reset()
	if calls := handler.Calls(); len(calls) != 0 {
		t.Fatalf("reset left %d calls in the log", len(calls))
	}
	if status, _ := post(t, handler.baseURL, chatRequest(adjudicatorModel, "c")); status != http.StatusOK {
		t.Fatalf("the script did not rewind: %d", status)
	}
}

// The call log is how a turn-loop test asks what the prompt builder actually
// sent. Without the messages, "the adjudicator received a scene and no action"
// is unprovable from the outside.
func TestServe_RecordsTheStructuralFactsOfEachRequest(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")
	post(t, handler.baseURL, chatRequestWithTools(adjudicatorModel,
		"Rook levers the gate with the crowbar.", "submit_verdict", "read_scene"))

	calls := handler.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	call := calls[0]
	if call.Role != "adjudicator" || call.Scenario != "full-band" {
		t.Fatalf("call routed to %q in scenario %q", call.Role, call.Scenario)
	}
	if call.Step != 0 || call.Ordinal != 0 {
		t.Fatalf("call recorded step %d ordinal %d, want 0 and 0", call.Step, call.Ordinal)
	}
	if !slices.Equal(call.Tools, []string{"submit_verdict", "read_scene"}) {
		t.Fatalf("recorded tools are %v", call.Tools)
	}
	if len(call.Messages) != 1 {
		t.Fatalf("recorded %d messages, want 1", len(call.Messages))
	}
	content, ok := call.Messages[0].ContentString()
	if !ok || !strings.Contains(content, "crowbar") {
		t.Fatalf("the recorded message content is %q; a test must be able to prove the action text "+
			"reached the persona", content)
	}
	if call.Usage.PromptTokens != 1200 {
		t.Fatalf("the recorded usage is %+v", call.Usage)
	}
	if !bytes.Contains(call.Body, []byte("crowbar")) {
		t.Fatalf("the raw body was not recorded")
	}
	if len(handler.CallsFor("narrator")) != 0 {
		t.Fatalf("CallsFor returned calls for a role that was never invoked")
	}
}

// The wire shape has to be a provider's, not merely one a particular client
// tolerates. Both fields here are ones the framework's SDK path happens to
// paper over — it infers a tool call from the presence of tool_calls, and it
// discards unparseable arguments — so neither is observable through the client
// and both are exactly what a different consumer would read.
func TestServe_EmitsTheProviderShapeAndNotJustAClientTolerableOne(t *testing.T) {
	t.Run("a tool call finishes for the reason a provider gives", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "full-band")
		_, body := post(t, handler.baseURL, chatRequest(adjudicatorModel, "hi"))
		if reason := decodeResponse(t, body).Choices[0].FinishReason; reason != "tool_calls" {
			t.Fatalf("finish_reason is %q, want %q", reason, "tool_calls")
		}
	})

	t.Run("scripted raw arguments reach the wire byte for byte", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "malformed-arguments")
		_, body := post(t, handler.baseURL, chatRequest(adjudicatorModel, "hi"))

		const want = `{"scalars": {"plausibility":`
		got := decodeResponse(t, body).Choices[0].Message.ToolCalls[0].Function.Arguments
		if got != want {
			t.Fatalf("arguments arrived as %q, want the fixture's own truncated bytes %q; a stub that "+
				"repaired them could not produce the failure it was written for", got, want)
		}
	})

	t.Run("a text answer finishes as a stop", func(t *testing.T) {
		handler := serve(t, twoRoleFixture(t), "prose-instead-of-exit")
		_, body := post(t, handler.baseURL, chatRequest(adjudicatorModel, "hi"))
		decoded := decodeResponse(t, body)
		if reason := decoded.Choices[0].FinishReason; reason != "stop" {
			t.Fatalf("finish_reason is %q, want %q", reason, "stop")
		}
		if len(decoded.Choices[0].Message.ToolCalls) != 0 {
			t.Fatal("a text step emitted tool calls")
		}
	})
}

// Usage has to be right ON THE WIRE, not merely in the stub's own ledger. The
// framework client recomputes the total from the two counts, so a wrong
// total_tokens is invisible there and would surface later in whatever reads
// the provider's own field.
func TestServe_ReportsUsageTheWayAProviderDoes(t *testing.T) {
	handler := serve(t, twoRoleFixture(t), "full-band")

	_, body := post(t, handler.baseURL, chatRequest(adjudicatorModel, "hi"))
	usage := decodeResponse(t, body).Usage
	if usage == nil {
		t.Fatal("the response carries no usage block")
	}
	if usage.PromptTokens != 1200 || usage.CompletionTokens != 180 {
		t.Fatalf("wire usage is %+v, want the scripted 1200/180", *usage)
	}
	if usage.TotalTokens != 1380 {
		t.Fatalf("wire total_tokens is %d, want %d — a consumer that trusts the provider's own total "+
			"would read a free turn", usage.TotalTokens, 1380)
	}
}

// A stub built on a scenario nobody declared must refuse to start. Defaulting
// to the first scenario would answer a harness that forgot to choose.
func TestNew_RefusesAnUndeclaredScenario(t *testing.T) {
	if _, err := mockmodel.New(twoRoleFixture(t), "not-a-scenario"); err == nil {
		t.Fatal("a stub started on a scenario the fixture does not declare")
	}
	if _, err := mockmodel.New(nil, "full-band"); err == nil {
		t.Fatal("a stub started with no fixture")
	}

	// A fixture built in Go never went through the loader, so New has to
	// validate it itself. The invalid part is deliberately NOT the scenario
	// name: a fixture that fails only the name check would pass through a New
	// that skipped validation entirely.
	handBuilt := &mockmodel.Fixture{
		Roles:     []mockmodel.Role{{Name: "a", Match: mockmodel.RoleMatch{Model: "m"}}},
		Scenarios: []mockmodel.Scenario{{Name: "s"}},
	}
	if _, err := mockmodel.New(handBuilt, "s"); err == nil {
		t.Fatal("a hand-built fixture scripting nothing was accepted; New must validate what it is handed")
	}
}

// The loader cannot produce a tool call whose arguments are not valid JSON —
// the outer decode fails first — so this check is only reachable through a
// fixture built in Go. It stays because that is a real way to build one, and
// because the response builder's "unreachable" fallback for a compaction
// failure is only true while this holds.
func TestNew_RefusesHandBuiltArgumentsThatAreNotJSON(t *testing.T) {
	broken := &mockmodel.Fixture{
		Roles: []mockmodel.Role{{Name: "a", Match: mockmodel.RoleMatch{Model: "m"}}},
		Scenarios: []mockmodel.Scenario{{
			Name: "s",
			Scripts: []mockmodel.Script{{
				Role: "a",
				Steps: []mockmodel.Step{{
					Kind:      mockmodel.StepToolCall,
					Usage:     &mockmodel.Usage{PromptTokens: 1, CompletionTokens: 1},
					ToolCalls: []mockmodel.ScriptedToolCall{{Name: "t", Arguments: []byte(`{"oops`)}},
				}},
			}},
		}},
	}
	_, err := mockmodel.New(broken, "s")
	if err == nil {
		t.Fatal("a tool call whose arguments are not valid JSON was accepted; use arguments_raw for that")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("rejection is %q", err)
	}
}

// helpers ------------------------------------------------------------------

func chatRequest(model, prompt string) string {
	return chatRequestWithTools(model, prompt)
}

func chatRequestWithTools(model, prompt string, tools ...string) string {
	type function struct {
		Name string `json:"name"`
	}
	type tool struct {
		Type     string   `json:"type"`
		Function function `json:"function"`
	}
	body := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []tool `json:"tools,omitempty"`
	}{Model: model}
	body.Messages = append(body.Messages, struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: prompt})
	for _, name := range tools {
		body.Tools = append(body.Tools, tool{Type: "function", Function: function{Name: name}})
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func post(t *testing.T, baseURL, body string) (int, []byte) {
	t.Helper()
	return postTo(t, baseURL+mockmodel.ChatCompletionsPath, body)
}

func postTo(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post to the stub: %v", err)
	}
	defer response.Body.Close()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatalf("read the stub's response: %v", err)
	}
	return response.StatusCode, buffer.Bytes()
}

func decodeResponse(t *testing.T, body []byte) wire.ChatCompletionResponse {
	t.Helper()
	var decoded wire.ChatCompletionResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the stub's response is not a chat completion: %v (%s)", err, body)
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Message == nil {
		t.Fatalf("the response carries no assistant message: %s", body)
	}
	return decoded
}

func toolCallName(t *testing.T, body []byte) string {
	t.Helper()
	decoded := decodeResponse(t, body)
	calls := decoded.Choices[0].Message.ToolCalls
	if len(calls) != 1 {
		t.Fatalf("the response carries %d tool calls, want 1: %s", len(calls), body)
	}
	return calls[0].Function.Name
}
