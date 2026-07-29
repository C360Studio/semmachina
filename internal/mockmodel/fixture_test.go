package mockmodel_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/mockmodel"
)

// A fixture is hand-edited configuration for a suite that otherwise reports
// `ok`, so every one of these has to be a load error. The failure mode being
// avoided is uniform: a fixture that loads while meaning something other than
// what its author wrote, and a green run that proves nothing.
func TestParseFixture_RejectsFixturesThatWouldSilentlyMisbehave(t *testing.T) {
	cases := map[string]struct {
		document string
		signal   string
	}{
		"a misspelled key": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"text","content":"x","usaeg":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "unknown field",
		},
		"no roles at all": {
			document: `{"roles":[],"scenarios":[]}`,
			signal:   "no roles",
		},
		"no scenarios at all": {
			document: `{"roles":[{"name":"a","match":{"model":"m"}}],"scenarios":[]}`,
			signal:   "no scenarios",
		},
		"two roles with one name": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}},{"name":"a","match":{"model":"n"}}]`,
				scenario("s", step("a"))),
			signal: `role "a" is declared twice`,
		},
		"a role name that cannot be embedded in a response id": {
			document: wrap(`"roles":[{"name":"Adjudicator","match":{"model":"m"}}]`,
				scenario("s", step("Adjudicator"))),
			signal: "lower-kebab",
		},
		"a role that matches everything": {
			document: wrap(`"roles":[{"name":"a","match":{}}]`, scenario("s", step("a"))),
			signal:   "neither model nor tools",
		},
		"a role whose match subsumes another's": {
			document: wrap(
				`"roles":[{"name":"wide","match":{"tools":["shared"]}},`+
					`{"name":"narrow","match":{"tools":["shared","own"]}}]`,
				scenario("s", step("wide"))),
			signal: "no request could ever route unambiguously",
		},
		"a scenario scripting an undeclared role": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`, scenario("s", step("b"))),
			signal:   "which the fixture does not declare",
		},
		"a scenario scripting one role twice": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", step("a"), step("a"))),
			signal: `scripts role "a" twice`,
		},
		"two scenarios with one name": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", step("a")), scenario("s", step("a"))),
			signal: `scenario "s" is declared twice`,
		},
		"a script with no steps": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[]}`)),
			signal: "with no steps",
		},
		"an unknown step kind": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"shrug","usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "unknown step kind",
		},
		"a tool_call with no tool call": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"tool_call","usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "declares no tool calls",
		},
		"a text step carrying a tool call": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"text","content":"x",`+
					`"usage":{"prompt_tokens":1,"completion_tokens":1},`+
					`"tool_calls":[{"name":"t","arguments":{}}]}]}`)),
			signal: "must not declare tool calls",
		},
		"a text step with nothing to say": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"text",`+
					`"usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "declares no content",
		},
		"a truncated step that truncated nothing": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"truncated","usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "nothing was truncated",
		},
		"a step with no usage": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"text","content":"x"}]}`)),
			signal: "makes a token-free run look free",
		},
		"a step priced at zero": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"text","content":"x",`+
					`"usage":{"prompt_tokens":0,"completion_tokens":3}}]}`)),
			signal: "both counts must be positive",
		},
		"an answering step carrying an error status": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"text","content":"x","status":503,`+
					`"usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "must not declare a status",
		},
		"an http_error with a success status": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"http_error","status":200,"error":{"message":"x"}}]}`)),
			signal: "a failure status is 4xx or 5xx",
		},
		"an http_error with no message": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"http_error","status":500}]}`)),
			signal: "declares no error message",
		},
		"an http_error priced as if it answered": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"http_error","status":500,"error":{"message":"x"},`+
					`"usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "must not declare usage",
		},
		"a tool call declaring both argument forms": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"tool_call","usage":{"prompt_tokens":1,"completion_tokens":1},`+
					`"tool_calls":[{"name":"t","arguments":{},"arguments_raw":"{"}]}]}`)),
			signal: "pick one",
		},
		"a tool call declaring neither argument form": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"tool_call","usage":{"prompt_tokens":1,"completion_tokens":1},`+
					`"tool_calls":[{"name":"t"}]}]}`)),
			signal: "declares no arguments",
		},
		"a tool call whose arguments are not an object": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[{"kind":"tool_call","usage":{"prompt_tokens":1,"completion_tokens":1},`+
					`"tool_calls":[{"name":"t","arguments":[1,2]}]}]}`)),
			signal: "non-object arguments",
		},
		"a repeating step with steps after it": {
			document: wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`,
				scenario("s", `{"role":"a","steps":[`+
					`{"kind":"text","content":"x","repeat":true,"usage":{"prompt_tokens":1,"completion_tokens":1}},`+
					`{"kind":"text","content":"y","usage":{"prompt_tokens":1,"completion_tokens":1}}]}`)),
			signal: "would be unreachable",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := mockmodel.ParseFixture([]byte(testCase.document))
			if err == nil {
				t.Fatalf("the fixture loaded; it should have been rejected for %q", testCase.signal)
			}
			if !strings.Contains(err.Error(), testCase.signal) {
				t.Fatalf("rejection is %q, which does not name %q", err, testCase.signal)
			}
		})
	}
}

// Anti-vacuity for the table above: the template the mutations are built from
// must itself load, or every case would pass for the wrong reason.
func TestParseFixture_AcceptsTheTemplateTheRejectionTableMutates(t *testing.T) {
	document := wrap(`"roles":[{"name":"a","match":{"model":"m"}}]`, scenario("s", step("a")))
	fixture, err := mockmodel.ParseFixture([]byte(document))
	if err != nil {
		t.Fatalf("the valid template does not load: %v", err)
	}
	if names := fixture.ScenarioNames(); len(names) != 1 || names[0] != "s" {
		t.Fatalf("scenario names are %v", names)
	}
	if _, ok := fixture.Scenario("nope"); ok {
		t.Fatal("an undeclared scenario resolved")
	}
}

// Two roles distinguished only by tools they do NOT share is the legitimate
// shape of the shared-endpoint configuration, and the subsumption check must
// not swallow it.
func TestParseFixture_AcceptsDisjointToolMatches(t *testing.T) {
	if _, err := mockmodel.ParseFixture([]byte(toolMatchedJSON)); err != nil {
		t.Fatalf("a fixture routing two roles on disjoint tools was rejected: %v", err)
	}
}

// helpers ------------------------------------------------------------------

func wrap(roles string, scenarios ...string) string {
	return `{` + roles + `,"scenarios":[` + strings.Join(scenarios, ",") + `]}`
}

func scenario(name string, scripts ...string) string {
	return `{"name":"` + name + `","scripts":[` + strings.Join(scripts, ",") + `]}`
}

func step(role string) string {
	return `{"role":"` + role + `","steps":[{"kind":"text","content":"x",` +
		`"usage":{"prompt_tokens":1,"completion_tokens":1}}]}`
}
