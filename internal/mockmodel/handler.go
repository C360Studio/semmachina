package mockmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/c360studio/semstreams/model"
	"github.com/c360studio/semstreams/model/wire"
)

const (
	// BasePath is what an endpoint URL points at: the OpenAI-compatible root,
	// exactly as a real provider's `.../v1` is configured.
	BasePath = "/v1"

	// ChatCompletionsPath is the only route the stub answers. Everything else
	// is refused by name, because a base URL missing its /v1 is a configuration
	// mistake that should read as one rather than as a dead endpoint.
	ChatCompletionsPath = BasePath + "/chat/completions"

	// FrozenCreated is the `created` timestamp on every response.
	//
	// A constant, not a clock: same scenario in, same bytes out. It is a
	// plausible instant rather than zero so a response in a log reads as a
	// frozen stub rather than as a field somebody forgot to set.
	FrozenCreated int64 = 1_700_000_000

	// DeclaredContextWindow is the context size the stub's endpoint config
	// advertises so the loop's budget math has a number to work with. The stub
	// never truncates anything; callers who want to exercise a narrower window
	// set their own value on the returned config.
	DeclaredContextWindow = 128_000

	// objectChatCompletion is the OpenAI `object` discriminator.
	objectChatCompletion = "chat.completion"

	// refusalErrorType is the `error.type` on every stub refusal, so a caller
	// can tell "the fixture did not cover this" from a scripted provider
	// failure without parsing prose.
	refusalErrorType = "mockmodel_refusal"
)

// FailureCode is the stable identifier on a stub refusal. Tests assert on
// these rather than on message text, so the messages stay free to explain
// themselves at length.
type FailureCode string

const (
	// FailureRoute — the request did not arrive at the chat-completions route.
	FailureRoute FailureCode = "unknown_route"
	// FailureMalformed — the request body was not a chat-completions request.
	FailureMalformed FailureCode = "malformed_request"
	// FailureStreaming — the request asked for SSE, which is not scripted.
	FailureStreaming FailureCode = "streaming_not_scripted"
	// FailureUnroutable — no declared role matches the request.
	FailureUnroutable FailureCode = "no_matching_role"
	// FailureAmbiguous — more than one declared role matches the request.
	FailureAmbiguous FailureCode = "ambiguous_role"
	// FailureUnscripted — the active scenario has no script for the role.
	FailureUnscripted FailureCode = "role_not_scripted"
	// FailureExhausted — the caller ran off the end of the role's script. In a
	// turn-loop test this is usually the finding, not the plumbing: a resumed
	// or redelivered turn called the model a second time.
	FailureExhausted FailureCode = "script_exhausted"
)

// Handler is the running stub: an http.Handler with a scripted mouth and a
// call log. Wrap it in httptest.NewServer (or any http.Server) to run it.
//
// Safe for concurrent use. Concurrent callers of one role consume distinct
// steps; which caller gets which step is the network's business, and a test
// that cares about order must not issue overlapping calls.
type Handler struct {
	fixture *Fixture

	mu       sync.Mutex
	scenario string
	// cursor is the next step index per (scenario, role).
	cursor map[scriptKey]int
	// served counts answered requests per (scenario, role) and is what
	// response and tool-call identifiers are derived from, so a repeating step
	// does not hand out the same tool_call id twice.
	served map[scriptKey]int
	calls  []Call
}

type scriptKey struct {
	scenario string
	role     string
}

// Call is one request the stub received, recorded whether or not it was
// answered.
type Call struct {
	// Scenario was active when the request arrived.
	Scenario string
	// Role the request routed to; empty when routing failed.
	Role string
	// Step is the index of the scripted step served, or -1 when none was.
	Step int
	// Ordinal is the 0-based count of requests already answered for this
	// (scenario, role) — the number the response identifiers are built from.
	Ordinal int
	// Model, Tools and Messages are the structural facts of the request. They
	// are what a test asserts against when it wants to know what the prompt
	// builder actually sent (did the action text reach the adjudicator?).
	Model    string
	Tools    []string
	Messages []wire.Message
	// Body is the raw request body.
	Body []byte
	// Status is the HTTP status the stub answered with.
	Status int
	// Refusal is set when the STUB refused; empty for both a served step and a
	// scripted provider failure.
	Refusal FailureCode
	// Usage is the accounting the served step reported.
	Usage Usage
}

// Totals is the stub's aggregate accounting — the number CI asserts a
// flat-cost-per-turn claim against.
type Totals struct {
	Calls            int
	Refusals         int
	PromptTokens     int
	CompletionTokens int
}

// TotalTokens returns the summed token count across every answered call.
func (t Totals) TotalTokens() int { return t.PromptTokens + t.CompletionTokens }

// New builds a stub from a fixture with one scenario active.
//
// The scenario is required rather than defaulted. A stub that fell back to
// "the first scenario" would answer a test that forgot to choose, which is the
// one thing a scripted endpoint must never do.
func New(fixture *Fixture, scenario string) (*Handler, error) {
	if fixture == nil {
		return nil, fmt.Errorf("mockmodel: fixture is required")
	}
	if err := fixture.Validate(); err != nil {
		return nil, err
	}
	if _, ok := fixture.Scenario(scenario); !ok {
		return nil, fmt.Errorf("mockmodel: fixture declares no scenario %q (it has %v)",
			scenario, fixture.ScenarioNames())
	}
	return &Handler{
		fixture:  fixture,
		scenario: scenario,
		cursor:   map[scriptKey]int{},
		served:   map[scriptKey]int{},
	}, nil
}

// EndpointConfig returns the model_registry entry that points a SemStreams
// model client at a running stub.
//
// This is the whole "retarget, don't rewrite" claim in one function: the
// config it returns differs from a live endpoint's only in URL and model name,
// so swapping in a real provider is a config edit and never a code change.
func EndpointConfig(baseURL, modelName string) *model.EndpointConfig {
	return &model.EndpointConfig{
		Provider:      "openai",
		URL:           strings.TrimRight(baseURL, "/") + BasePath,
		Model:         modelName,
		MaxTokens:     DeclaredContextWindow,
		SupportsTools: true,
	}
}

// Scenario returns the active scenario name.
func (h *Handler) Scenario() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scenario
}

// SetScenario switches the active scenario. Script positions are kept per
// scenario, so switching away and back resumes where it left off.
func (h *Handler) SetScenario(name string) error {
	if _, ok := h.fixture.Scenario(name); !ok {
		return fmt.Errorf("mockmodel: fixture declares no scenario %q (it has %v)",
			name, h.fixture.ScenarioNames())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scenario = name
	return nil
}

// Reset clears every script position and the call log.
//
// For reusing one stub across independent cases. A crash-resume test must NOT
// call it: the point of that test is that the resumed turn consumes no further
// step, and a reset would hand it a fresh one.
func (h *Handler) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cursor = map[scriptKey]int{}
	h.served = map[scriptKey]int{}
	h.calls = nil
}

// Calls returns every request the stub received, in arrival order.
func (h *Handler) Calls() []Call {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Call(nil), h.calls...)
}

// CallsFor returns the calls that routed to one role.
func (h *Handler) CallsFor(role string) []Call {
	var out []Call
	for _, call := range h.Calls() {
		if call.Role == role {
			out = append(out, call)
		}
	}
	return out
}

// Totals returns the aggregate accounting across every recorded call.
func (h *Handler) Totals() Totals {
	var totals Totals
	for _, call := range h.Calls() {
		totals.Calls++
		if call.Refusal != "" {
			totals.Refusals++
		}
		totals.PromptTokens += call.Usage.PromptTokens
		totals.CompletionTokens += call.Usage.CompletionTokens
	}
	return totals
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != ChatCompletionsPath || r.Method != http.MethodPost {
		h.refuse(w, Call{Step: -1, Scenario: h.Scenario()}, FailureRoute, fmt.Sprintf(
			"the stub answers POST %s only; this request was %s %s. An endpoint URL must end in %s.",
			ChatCompletionsPath, r.Method, r.URL.Path, BasePath))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.refuse(w, Call{Step: -1, Scenario: h.Scenario()}, FailureMalformed,
			fmt.Sprintf("reading the request body failed: %v", err))
		return
	}

	var req wire.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.refuse(w, Call{Step: -1, Scenario: h.Scenario(), Body: body}, FailureMalformed,
			fmt.Sprintf("the body is not an OpenAI chat-completions request: %v", err))
		return
	}

	call := Call{
		Scenario: h.Scenario(),
		Step:     -1,
		Model:    req.Model,
		Tools:    toolNames(&req),
		Messages: req.Messages,
		Body:     body,
	}

	if req.Stream {
		h.refuse(w, call, FailureStreaming,
			"the stub does not script server-sent events; set the endpoint's stream flag to false")
		return
	}

	role, denied := h.routeRole(&req, call.Tools)
	if denied != nil {
		h.refuse(w, call, denied.code, denied.detail)
		return
	}
	call.Role = role

	taken, denied := h.take(call.Scenario, role)
	if denied != nil {
		h.refuse(w, call, denied.code, denied.detail)
		return
	}
	call.Step = taken.index
	call.Ordinal = taken.ordinal

	h.answer(w, call, taken.step)
}

// refusal is a reason the stub cannot answer, carried as one value so the
// serving path never has to keep a code and its explanation in step by hand.
type refusal struct {
	code   FailureCode
	detail string
}

func deny(code FailureCode, format string, args ...any) *refusal {
	return &refusal{code: code, detail: fmt.Sprintf(format, args...)}
}

// taken is the outcome of advancing one script.
type taken struct {
	step    Step
	index   int
	ordinal int
}

// routeRole resolves the persona role from the request's structure.
func (h *Handler) routeRole(req *wire.ChatCompletionRequest, tools []string) (string, *refusal) {
	var matched []string
	for i := range h.fixture.Roles {
		if h.fixture.Roles[i].Match.matches(req.Model, tools) {
			matched = append(matched, h.fixture.Roles[i].Name)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return "", deny(FailureUnroutable,
			"no scripted role matches model %q with tools %v; the fixture declares %s",
			req.Model, tools, h.describeRoles())
	default:
		return "", deny(FailureAmbiguous,
			"model %q with tools %v matches %v; a request must route to exactly one role",
			req.Model, tools, matched)
	}
}

// take advances the script for one (scenario, role) and returns the step to
// answer with.
func (h *Handler) take(scenario, role string) (taken, *refusal) {
	steps, ok := h.stepsFor(scenario, role)
	if !ok {
		return taken{}, deny(FailureUnscripted,
			"scenario %q scripts no responses for role %q", scenario, role)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	key := scriptKey{scenario: scenario, role: role}
	index := h.cursor[key]
	if index >= len(steps) {
		return taken{}, deny(FailureExhausted,
			"scenario %q scripts %d response(s) for role %q and this is call %d; the loop called the model "+
				"more times than the scenario allows",
			scenario, len(steps), role, index+1)
	}

	step := steps[index]
	if !step.Repeat {
		h.cursor[key] = index + 1
	}
	ordinal := h.served[key]
	h.served[key] = ordinal + 1
	return taken{step: step, index: index, ordinal: ordinal}, nil
}

func (h *Handler) stepsFor(scenario, role string) ([]Step, bool) {
	found, ok := h.fixture.Scenario(scenario)
	if !ok {
		return nil, false
	}
	for i := range found.Scripts {
		if found.Scripts[i].Role == role {
			return found.Scripts[i].Steps, true
		}
	}
	return nil, false
}

// answer writes a scripted step.
func (h *Handler) answer(w http.ResponseWriter, call Call, step Step) {
	if step.Kind == StepHTTPError {
		call.Status = step.Status
		h.record(call)
		writeJSON(w, step.Status, errorEnvelope{Error: errorBody{
			Message: step.Error.Message,
			Type:    step.Error.Type,
			Code:    step.Error.Code,
		}})
		return
	}

	call.Usage = *step.Usage
	call.Status = http.StatusOK
	h.record(call)
	writeJSON(w, http.StatusOK, buildResponse(call, step))
}

// refuse writes a stub refusal: always 400, which the framework client treats
// as non-retryable, so a fixture gap fails once and explains itself instead of
// being retried into a timeout.
func (h *Handler) refuse(w http.ResponseWriter, call Call, code FailureCode, detail string) {
	call.Status = http.StatusBadRequest
	call.Refusal = code
	h.record(call)
	writeJSON(w, http.StatusBadRequest, errorEnvelope{
		// The code is repeated INSIDE the message on purpose. Clients flatten a
		// provider error to its message string — the framework's own does, and
		// that string is what a CI failure prints — so a code that lived only
		// in the structured field would be invisible exactly when it is needed.
		Error: errorBody{
			Message: fmt.Sprintf("mockmodel [%s]: %s", code, detail),
			Type:    refusalErrorType,
			Code:    string(code),
		}})
}

func (h *Handler) record(call Call) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, call)
}

func (h *Handler) describeRoles() string {
	parts := make([]string, 0, len(h.fixture.Roles))
	for i := range h.fixture.Roles {
		role := &h.fixture.Roles[i]
		parts = append(parts, fmt.Sprintf("%s(model=%q, tools=%v)", role.Name, role.Match.Model, role.Match.Tools))
	}
	return strings.Join(parts, ", ")
}

// buildResponse renders a step as the framework's own wire response type, so
// the bytes on the socket are the bytes the framework's decoder was written
// against rather than a second, hand-maintained opinion of the shape.
func buildResponse(call Call, step Step) wire.ChatCompletionResponse {
	message := wire.Message{Role: "assistant"}
	if step.Content != "" {
		// Errors are impossible: the input is a Go string.
		_ = message.SetContentString(step.Content)
	}
	for i := range step.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, wire.ToolCall{
			ID:   fmt.Sprintf("call-%s-%s-%d-%d", call.Scenario, call.Role, call.Ordinal, i),
			Type: "function",
			Function: wire.Function{
				Name:      step.ToolCalls[i].Name,
				Arguments: step.ToolCalls[i].arguments(),
			},
		})
	}

	return wire.ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%s-%s-%d", call.Scenario, call.Role, call.Ordinal),
		Object:  objectChatCompletion,
		Created: FrozenCreated,
		Model:   call.Model,
		Choices: []wire.Choice{{
			Index:        0,
			Message:      &message,
			FinishReason: finishReason(step),
		}},
		Usage: &wire.Usage{
			PromptTokens:     step.Usage.PromptTokens,
			CompletionTokens: step.Usage.CompletionTokens,
			TotalTokens:      step.Usage.Total(),
		},
	}
}

// finishReason maps a step kind to the provider field the framework client
// branches on.
func finishReason(step Step) string {
	switch step.Kind {
	case StepToolCall:
		return "tool_calls"
	case StepTruncated:
		return "length"
	case StepText:
		return "stop"
	case StepHTTPError:
		// Unreachable: an error step never reaches the response builder.
		return ""
	default:
		// Unreachable: validation admits only the kinds above.
		return ""
	}
}

// arguments returns the wire form of a scripted call's arguments: a raw script
// verbatim, or the fixture's own JSON compacted.
func (c ScriptedToolCall) arguments() string {
	if c.ArgumentsRaw != nil {
		return *c.ArgumentsRaw
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, c.Arguments); err != nil {
		// Unreachable: validation rejects arguments that are not valid JSON.
		return string(c.Arguments)
	}
	return compacted.String()
}

func toolNames(req *wire.ChatCompletionRequest) []string {
	if len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for i := range req.Tools {
		names = append(names, req.Tools[i].Function.Name)
	}
	return names
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		// Unreachable for the concrete types above; a 500 with the reason is
		// still better than a truncated body.
		http.Error(w, fmt.Sprintf("mockmodel: encoding the response failed: %v", err),
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}
