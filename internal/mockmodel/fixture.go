package mockmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// StepKind names the response shape a scripted step produces. The set is
// closed: an unknown kind is a fixture error, never a default.
type StepKind string

const (
	// StepToolCall answers with an assistant message carrying tool calls and
	// finish_reason "tool_calls". It is the ordinary persona exit AND the
	// vehicle for every malformed exit — the arguments are the fixture's own
	// bytes, so an out-of-vocabulary class or a missing required field is a
	// scripted response like any other.
	StepToolCall StepKind = "tool_call"

	// StepText answers with content only and finish_reason "stop". This is the
	// text-only completion: a persona that answered in prose instead of exiting
	// through its terminal tool. The executor's rejection of it is exactly the
	// behaviour F4 says we cannot delegate to provider strict-mode.
	StepText StepKind = "text"

	// StepTruncated answers with finish_reason "length". Worth its own kind
	// because the framework client treats truncation specially: it DROPS any
	// accumulated tool calls rather than trusting half-written arguments, so a
	// truncated tool call reaches the loop as a completion with no exit at all.
	StepTruncated StepKind = "truncated"

	// StepHTTPError answers with a non-2xx status and an OpenAI-shaped error
	// envelope — the provider failure path (rate limit, 5xx, refusal), which
	// the client's retry and the registry's circuit breaker both read.
	StepHTTPError StepKind = "http_error"
)

// Fixture is a whole scripted model: the roles it can recognise and the
// scenarios it can play.
type Fixture struct {
	// Description is free text for the humans maintaining the file. JSON has no
	// comments and a fixture that cannot explain itself rots.
	Description string `json:"description,omitempty"`
	// Roles are matched in declaration order; a request must match exactly one.
	Roles []Role `json:"roles"`
	// Scenarios are the scripts one of which is active at a time.
	Scenarios []Scenario `json:"scenarios"`
}

// Role is a persona identified by what its REQUEST looks like on the wire.
type Role struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Match       RoleMatch `json:"match"`
}

// RoleMatch is the structural test that routes a request to a role.
//
// Both selectors describe configuration or loop wiring, never prompt content:
// Model is what the endpoint config put in the request's `model` field, and
// Tools are the tool names the loop declared. Matching prose was considered and
// rejected — see the package doc.
type RoleMatch struct {
	// Model must equal the request's model exactly. Empty means "any model",
	// which is only valid alongside a Tools constraint.
	Model string `json:"model,omitempty"`
	// Tools must ALL be declared on the request. A superset is fine: a persona
	// carrying read-only tools beside its terminal one still matches on the
	// terminal one.
	Tools []string `json:"tools,omitempty"`
}

// Scenario is one playable script: an ordered list of responses per role.
type Scenario struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scripts     []Script `json:"scripts"`
}

// Script is one role's ordered responses within a scenario.
//
// A slice rather than a map keyed by role, so the fixture has a declared order,
// a duplicated role is a load error instead of a silent last-wins overwrite,
// and nothing in the serving path ever iterates a map.
type Script struct {
	Role  string `json:"role"`
	Steps []Step `json:"steps"`
}

// Step is one scripted response.
type Step struct {
	Kind        StepKind `json:"kind"`
	Description string   `json:"description,omitempty"`

	// Content is the assistant message text. Required by StepText; allowed
	// alongside tool calls, which real models do emit.
	Content string `json:"content,omitempty"`

	// ToolCalls are emitted verbatim, in order.
	ToolCalls []ScriptedToolCall `json:"tool_calls,omitempty"`

	// Usage is the token accounting this step reports. Required on every
	// answering step: an unpriced call makes a CI run look free (M7).
	Usage *Usage `json:"usage,omitempty"`

	// Status and Error belong to StepHTTPError.
	Status int            `json:"status,omitempty"`
	Error  *ScriptedError `json:"error,omitempty"`

	// Repeat makes this step answer every further call to its role instead of
	// advancing. It is how a persona that never terminates is scripted, which
	// is what makes an iteration cap (M5) testable. Legal only on the last step
	// of a script — a repeat in the middle would make every later step dead.
	Repeat bool `json:"repeat,omitempty"`
}

// Usage is the token accounting a step reports.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Total returns the token total the endpoint reports for the step.
func (u Usage) Total() int { return u.PromptTokens + u.CompletionTokens }

// ScriptedError is the body of a StepHTTPError response.
type ScriptedError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

// ScriptedToolCall is one tool call in a scripted response.
//
// The two argument forms exist for opposite jobs. Arguments carries a JSON
// object the fixture author can read and edit, which covers valid exits and
// every SEMANTICALLY invalid one — an out-of-vocabulary class, a missing
// required field, a band the verdict was not entitled to declare.
// ArgumentsRaw carries a literal string that no JSON encoder would produce: a
// truncated object, an unquoted key, a bare number. Models emit those, and the
// framework client's response to them (substituting an empty argument map) is
// itself behaviour the executor has to survive.
type ScriptedToolCall struct {
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	ArgumentsRaw *string         `json:"arguments_raw,omitempty"`
}

// ParseFixture decodes and validates a fixture document.
//
// Unknown fields are rejected. A fixture is hand-edited configuration for a
// test suite that will otherwise report `ok`, so a misspelled key has to be an
// error at load: "arguments_row" silently ignored would produce a tool call
// with no arguments and a green run.
func ParseFixture(data []byte) (*Fixture, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var fixture Fixture
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("decode mock-model fixture: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("decode mock-model fixture: trailing content after the document")
	}
	if err := fixture.Validate(); err != nil {
		return nil, err
	}
	return &fixture, nil
}

// Validate checks everything about a fixture that can be checked without a
// request in hand.
func (f *Fixture) Validate() error {
	if len(f.Roles) == 0 {
		return fmt.Errorf("fixture declares no roles; every request would be unroutable")
	}
	if len(f.Scenarios) == 0 {
		return fmt.Errorf("fixture declares no scenarios; there would be nothing to play")
	}

	seen := make(map[string]bool, len(f.Roles))
	for i := range f.Roles {
		role := &f.Roles[i]
		if err := validName("role name", role.Name); err != nil {
			return err
		}
		if seen[role.Name] {
			return fmt.Errorf("role %q is declared twice", role.Name)
		}
		seen[role.Name] = true
		if err := role.Match.validate(role.Name); err != nil {
			return err
		}
	}
	if err := f.validateRolesAreDistinguishable(); err != nil {
		return err
	}

	scenarios := make(map[string]bool, len(f.Scenarios))
	for i := range f.Scenarios {
		scenario := &f.Scenarios[i]
		if err := validName("scenario name", scenario.Name); err != nil {
			return err
		}
		if scenarios[scenario.Name] {
			return fmt.Errorf("scenario %q is declared twice", scenario.Name)
		}
		scenarios[scenario.Name] = true
		if err := scenario.validate(seen); err != nil {
			return err
		}
	}
	return nil
}

// validateRolesAreDistinguishable rejects the ambiguity that is decidable
// before any request arrives.
//
// If one role's match is a subset of another's — no model constraint or the
// same one, and a subset of the tools — then every request matching the
// narrower role also matches the wider one, and the stub can only ever answer
// with an ambiguity refusal. Catching that at load turns a fixture bug into a
// sentence instead of a mystery in CI at 3am. Ambiguity that depends on what a
// particular request declares is still caught at request time.
func (f *Fixture) validateRolesAreDistinguishable() error {
	for i := range f.Roles {
		for j := range f.Roles {
			if i == j {
				continue
			}
			wide, narrow := &f.Roles[i], &f.Roles[j]
			if wide.Match.subsumes(narrow.Match) {
				return fmt.Errorf(
					"role %q matches every request role %q does (model %q, tools %v), so no request could "+
						"ever route unambiguously; give them disjoint models or disjoint tools",
					wide.Name, narrow.Name, wide.Match.Model, wide.Match.Tools)
			}
		}
	}
	return nil
}

// Scenario returns the named scenario.
func (f *Fixture) Scenario(name string) (*Scenario, bool) {
	for i := range f.Scenarios {
		if f.Scenarios[i].Name == name {
			return &f.Scenarios[i], true
		}
	}
	return nil, false
}

// ScenarioNames returns every scenario name in declaration order.
func (f *Fixture) ScenarioNames() []string {
	names := make([]string, 0, len(f.Scenarios))
	for i := range f.Scenarios {
		names = append(names, f.Scenarios[i].Name)
	}
	return names
}

func (m RoleMatch) validate(role string) error {
	if m.Model == "" && len(m.Tools) == 0 {
		return fmt.Errorf("role %q matches on neither model nor tools, so it would match every request", role)
	}
	seen := make(map[string]bool, len(m.Tools))
	for _, tool := range m.Tools {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("role %q declares an empty tool name", role)
		}
		if seen[tool] {
			return fmt.Errorf("role %q declares tool %q twice", role, tool)
		}
		seen[tool] = true
	}
	return nil
}

// matches reports whether a request with this model and these declared tools
// routes to the role.
func (m RoleMatch) matches(model string, declared []string) bool {
	if m.Model != "" && m.Model != model {
		return false
	}
	for _, want := range m.Tools {
		if !slices.Contains(declared, want) {
			return false
		}
	}
	return true
}

// subsumes reports whether every request matching other also matches m.
func (m RoleMatch) subsumes(other RoleMatch) bool {
	if m.Model != "" && m.Model != other.Model {
		return false
	}
	for _, tool := range m.Tools {
		if !slices.Contains(other.Tools, tool) {
			return false
		}
	}
	return true
}

func (s *Scenario) validate(roles map[string]bool) error {
	if len(s.Scripts) == 0 {
		return fmt.Errorf("scenario %q scripts no roles", s.Name)
	}
	seen := make(map[string]bool, len(s.Scripts))
	for i := range s.Scripts {
		script := &s.Scripts[i]
		if !roles[script.Role] {
			return fmt.Errorf("scenario %q scripts role %q, which the fixture does not declare",
				s.Name, script.Role)
		}
		if seen[script.Role] {
			return fmt.Errorf("scenario %q scripts role %q twice", s.Name, script.Role)
		}
		seen[script.Role] = true
		if len(script.Steps) == 0 {
			return fmt.Errorf("scenario %q scripts role %q with no steps", s.Name, script.Role)
		}
		for idx := range script.Steps {
			step := &script.Steps[idx]
			if err := step.validate(); err != nil {
				return fmt.Errorf("scenario %q role %q step %d: %w", s.Name, script.Role, idx, err)
			}
			if step.Repeat && idx != len(script.Steps)-1 {
				return fmt.Errorf(
					"scenario %q role %q step %d repeats but is not the last step; every step after it "+
						"would be unreachable", s.Name, script.Role, idx)
			}
		}
	}
	return nil
}

func (s *Step) validate() error {
	switch s.Kind {
	case StepToolCall:
		if len(s.ToolCalls) == 0 {
			return fmt.Errorf("%s declares no tool calls", s.Kind)
		}
	case StepText:
		if s.Content == "" {
			return fmt.Errorf("%s declares no content", s.Kind)
		}
		if len(s.ToolCalls) > 0 {
			return fmt.Errorf("%s must not declare tool calls; use %s for a call", s.Kind, StepToolCall)
		}
	case StepTruncated:
		if s.Content == "" && len(s.ToolCalls) == 0 {
			return fmt.Errorf("%s declares neither content nor tool calls, so nothing was truncated", s.Kind)
		}
	case StepHTTPError:
		return s.validateHTTPError()
	default:
		return fmt.Errorf("unknown step kind %q (want one of %s, %s, %s, %s)",
			s.Kind, StepToolCall, StepText, StepTruncated, StepHTTPError)
	}

	if s.Status != 0 || s.Error != nil {
		return fmt.Errorf("%s must not declare a status or an error body; use %s", s.Kind, StepHTTPError)
	}
	if s.Usage == nil {
		return fmt.Errorf("%s declares no usage; an unpriced call makes a token-free run look free", s.Kind)
	}
	if s.Usage.PromptTokens <= 0 || s.Usage.CompletionTokens <= 0 {
		return fmt.Errorf("%s declares usage %d/%d; both counts must be positive",
			s.Kind, s.Usage.PromptTokens, s.Usage.CompletionTokens)
	}
	for i := range s.ToolCalls {
		if err := s.ToolCalls[i].validate(); err != nil {
			return fmt.Errorf("tool call %d: %w", i, err)
		}
	}
	return nil
}

func (s *Step) validateHTTPError() error {
	if s.Status < 400 || s.Status > 599 {
		return fmt.Errorf("%s declares status %d; a failure status is 4xx or 5xx", s.Kind, s.Status)
	}
	if s.Error == nil || s.Error.Message == "" {
		return fmt.Errorf("%s declares no error message", s.Kind)
	}
	if s.Content != "" || len(s.ToolCalls) > 0 {
		return fmt.Errorf("%s must not declare content or tool calls; the body is the error envelope", s.Kind)
	}
	if s.Usage != nil {
		return fmt.Errorf("%s must not declare usage; a failed call returns no accounting", s.Kind)
	}
	return nil
}

func (c *ScriptedToolCall) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("tool call has no name")
	}
	switch {
	case len(c.Arguments) > 0 && c.ArgumentsRaw != nil:
		return fmt.Errorf("tool call %q declares both arguments and arguments_raw; pick one", c.Name)
	case len(c.Arguments) > 0:
		if !json.Valid(c.Arguments) {
			return fmt.Errorf("tool call %q has arguments that are not valid JSON", c.Name)
		}
		if trimmed := bytes.TrimLeft(c.Arguments, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
			return fmt.Errorf(
				"tool call %q has non-object arguments; a model emits an object, and a fixture that "+
					"needs to emit something else should say so through arguments_raw", c.Name)
		}
	case c.ArgumentsRaw != nil:
		// Deliberately unchecked: emitting bytes no encoder would produce is
		// the entire reason this field exists.
	default:
		return fmt.Errorf("tool call %q declares no arguments; use arguments {} for an empty object", c.Name)
	}
	return nil
}

// validName keeps identifiers safe to embed in a response id and easy to read
// in a failure message. It mirrors the lower-kebab discipline the rest of the
// engine uses for names that travel.
func validName(what, name string) error {
	if name == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if len(name) > 64 {
		return fmt.Errorf("%s %q is %d bytes; the limit is 64", what, name, len(name))
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return fmt.Errorf("%s %q must be lower-kebab ([a-z0-9-], not starting with '-')", what, name)
		}
	}
	return nil
}
