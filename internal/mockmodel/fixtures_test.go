package mockmodel_test

import (
	"testing"

	"github.com/c360studio/semmachina/internal/mockmodel"
)

// twoRoleFixture is the package's own working fixture. It is JSON parsed
// through the production loader rather than a Go literal, because the loader —
// unknown-field rejection included — is part of what these tests are for.
//
// The scenarios here are shaped to exercise the STUB. The scenarios the turn
// loop is played against ship in scenarios/turnloop.json.
func twoRoleFixture(t *testing.T) *mockmodel.Fixture {
	t.Helper()
	fixture, err := mockmodel.ParseFixture([]byte(twoRoleJSON))
	if err != nil {
		t.Fatalf("the package's own fixture does not load: %v", err)
	}
	return fixture
}

const twoRoleJSON = `{
  "description": "Two personas, one endpoint each, scripted for the stub's own tests.",
  "roles": [
    {
      "name": "adjudicator",
      "description": "Matched on the model name the endpoint config carries.",
      "match": {"model": "semmachina-mock-adjudicator"}
    },
    {
      "name": "narrator",
      "match": {"model": "semmachina-mock-narrator"}
    }
  ],
  "scenarios": [
    {
      "name": "full-band",
      "description": "One valid banded verdict, then one narration.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 1200, "completion_tokens": 180},
              "tool_calls": [
                {
                  "name": "submit_verdict",
                  "arguments": {
                    "scalars": {
                      "plausibility": "plausible",
                      "risk": "moderate",
                      "consequence": "setback",
                      "requires_roll": true
                    },
                    "bands": {"miss": [], "partial": [], "full": []}
                  }
                }
              ]
            }
          ]
        },
        {
          "role": "narrator",
          "steps": [
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 900, "completion_tokens": 240},
              "tool_calls": [
                {"name": "submit_narration", "arguments": {"prose": "The gate gives with a squeal."}}
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "out-of-vocabulary",
      "description": "A verdict whose plausibility class is not in the closed set.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 1200, "completion_tokens": 90},
              "tool_calls": [
                {
                  "name": "submit_verdict",
                  "arguments": {"scalars": {"plausibility": "vibes", "risk": "moderate"}}
                }
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "malformed-arguments",
      "description": "Arguments truncated mid-object, as a cut-off model emits them.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 1200, "completion_tokens": 40},
              "tool_calls": [
                {"name": "submit_verdict", "arguments_raw": "{\"scalars\": {\"plausibility\":"}
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "prose-instead-of-exit",
      "description": "The persona answered in prose and never called its terminal tool.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "text",
              "content": "Sure! Rook probably manages it. Roll a 7 or better.",
              "usage": {"prompt_tokens": 1200, "completion_tokens": 22}
            }
          ]
        }
      ]
    },
    {
      "name": "truncated-exit",
      "description": "finish_reason=length with a tool call the client must refuse to trust.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "truncated",
              "usage": {"prompt_tokens": 1200, "completion_tokens": 4096},
              "tool_calls": [
                {"name": "submit_verdict", "arguments": {"scalars": {"plausibility": "plausible"}}}
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "provider-failure",
      "description": "The endpoint fails once and then answers, so the client's real retry curve runs.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "http_error",
              "status": 503,
              "error": {"message": "model is loading", "type": "server_error", "code": "model_loading"}
            },
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 1200, "completion_tokens": 180},
              "tool_calls": [
                {"name": "submit_verdict", "arguments": {"scalars": {"plausibility": "plausible"}}}
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "never-terminates",
      "description": "A persona that keeps calling a non-terminal tool forever, so an iteration cap has something to stop.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "tool_call",
              "repeat": true,
              "usage": {"prompt_tokens": 1200, "completion_tokens": 30},
              "tool_calls": [
                {"name": "read_scene", "arguments": {"scene_id": "again"}}
              ]
            }
          ]
        }
      ]
    },
    {
      "name": "two-step",
      "description": "Two adjudicator responses, for the ordering and determinism tests.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 100, "completion_tokens": 10},
              "tool_calls": [{"name": "read_scene", "arguments": {"step": "first"}}]
            },
            {
              "kind": "tool_call",
              "usage": {"prompt_tokens": 200, "completion_tokens": 20},
              "tool_calls": [{"name": "submit_verdict", "arguments": {"step": "second"}}]
            }
          ]
        }
      ]
    },
    {
      "name": "ten-step",
      "description": "Ten distinct responses, so a concurrency test can prove each is served exactly once.",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [
            {"kind": "text", "content": "0", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "1", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "2", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "3", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "4", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "5", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "6", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "7", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "8", "usage": {"prompt_tokens": 1, "completion_tokens": 1}},
            {"kind": "text", "content": "9", "usage": {"prompt_tokens": 1, "completion_tokens": 1}}
          ]
        }
      ]
    }
  ]
}`

// toolMatchedJSON routes on declared tool names instead of the model name —
// the configuration a single-box deployment lands in when both personas share
// one local model.
const toolMatchedJSON = `{
  "roles": [
    {"name": "adjudicator", "match": {"tools": ["submit_verdict"]}},
    {"name": "narrator", "match": {"tools": ["submit_narration"]}}
  ],
  "scenarios": [
    {
      "name": "one-each",
      "scripts": [
        {
          "role": "adjudicator",
          "steps": [{
            "kind": "tool_call",
            "usage": {"prompt_tokens": 10, "completion_tokens": 5},
            "tool_calls": [{"name": "submit_verdict", "arguments": {"from": "adjudicator"}}]
          }]
        },
        {
          "role": "narrator",
          "steps": [{
            "kind": "tool_call",
            "usage": {"prompt_tokens": 10, "completion_tokens": 5},
            "tool_calls": [{"name": "submit_narration", "arguments": {"from": "narrator"}}]
          }]
        }
      ]
    }
  ]
}`
