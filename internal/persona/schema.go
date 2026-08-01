package persona

import (
	"fmt"

	"github.com/c360studio/semstreams/agentic"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The names the personas exit through.
//
// They are custom tools rather than upstream's `decide`, and the reasoning is
// worth stating where the names are, because `decide` was the obvious candidate:
// F4 names it as the ready-made closed-vocabulary seam, and its allowlist
// mechanism (a rule-supplied set validated in the executor, rejected with
// ToolErrorInvalidArgs so the model can self-correct) is exactly the shape this
// package wants.
//
// Three things made it the wrong vehicle, and only the first is about taste:
//
//   - decide's schema is {action, reason, subtopics[], retry_hint}. A verdict is
//     four closed scalars, a bounded typed modifier list, and three bands of
//     typed effect intents. Nothing on decide can carry that except a JSON
//     document smuggled through a string field — which moves the schema OUT of
//     the tool definition, where a model can see it, and into a parser, where it
//     cannot. For the slice's one schema-bearing exit on a mid-tier model
//     (ADR-026, F5), that is the wrong direction.
//   - decide publishes coordinator.decision.* triples onto the LOOP entity
//     through AddTriplesBatch — the APPENDING lane. Our scalars are
//     single-valued facts on the TURN entity and must travel the merge lane, or a
//     duplicate exit leaves a turn holding two plausibilities with no error
//     anywhere (F14). Using decide would put the verdict in the wrong place, on
//     the wrong lane, and still need a second write to reach the turn.
//   - MetadataKeyDecideActionAllowlist closes ONE string field. We close four
//     scalar fields, the effect vocabulary inside every band, the per-modifier
//     and summed-modifier bounds, and the coherence between the reported roll
//     gate and the declared band set. One allowlist cannot express any of that.
//
// What IS re-derived from decide, deliberately: the invalid-args-with-the-
// allowed-set correction shape, StopLoop with the full payload in Content for a
// downstream reader, and the split between small rule-matchable triples and a
// bulky payload behind a reference (F6).
const (
	// VerdictToolName is the adjudicator's single exit.
	VerdictToolName = "submit_verdict"
	// NarrationToolName is the narrator's single exit.
	NarrationToolName = "submit_narration"
	// CaseDecisionToolName is the casekeeper's single exit.
	CaseDecisionToolName = "submit_case_decision"
	// CompanionDecisionToolName is the companion persona's structural exit.
	CompanionDecisionToolName = "submit_companion_decision"
)

func companionDecisionToolDefinition() agentic.ToolDefinition {
	kinds := payload.CompanionDecisionKinds()
	kindValues := make([]string, len(kinds))
	for index, kind := range kinds {
		kindValues[index] = string(kind)
	}
	hints := []string{""}
	for _, hint := range vocabulary.HintLevels() {
		hints = append(hints, string(hint))
	}
	return agentic.ToolDefinition{
		Name: CompanionDecisionToolName,
		Description: "Exit once with the companion's structural decision. Dialogue and rationale belong to " +
			"the narrator and are not accepted here.",
		Strict: true,
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"kind", "hint_level", "evidence_refs", "target_ref"},
			"properties": map[string]any{
				"kind": map[string]any{"type": "string", "enum": kindValues},
				"hint_level": map[string]any{"type": "string", "enum": hints,
					"description": "One closed hint level for hint; otherwise an empty string."},
				"evidence_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "Zero to eight evidence references already known by this companion."},
				"target_ref": map[string]any{"type": "string",
					"description": "Optional structural target entity ID; use an empty string for none."},
			},
		},
	}
}

func caseDecisionToolDefinition() agentic.ToolDefinition {
	kinds := payload.CaseDecisionKinds()
	kindValues := make([]string, len(kinds))
	for idx, kind := range kinds {
		kindValues[idx] = string(kind)
	}
	ref := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	refs := func(description string) map[string]any {
		return map[string]any{
			"type": "array", "description": description,
			"items": map[string]any{"type": "string"},
		}
	}
	return agentic.ToolDefinition{
		Name: CaseDecisionToolName,
		Description: "Exit once with the closed structural interpretation of the player's mystery action. " +
			"Use empty accusation-reference strings for every non-accuse kind.",
		Strict: true,
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"kind", "target_refs", "reveal_refs", "culprit_ref", "method_ref", "motive_ref"},
			"properties": map[string]any{
				"kind": map[string]any{"type": "string", "enum": kindValues,
					"description": "The closed interpretation kind."},
				"target_refs": refs("Zero to eight authorized entity references targeted by the action."),
				"reveal_refs": refs("Zero to twelve eligible evidence references proposed for revelation."),
				"culprit_ref": ref("For accuse only: the proposed culprit entity ID; otherwise an empty string."),
				"method_ref":  ref("For accuse only: the proposed method entity ID; otherwise an empty string."),
				"motive_ref":  ref("For accuse only: the proposed motive entity ID; otherwise an empty string."),
			},
		},
	}
}

// verdictToolDefinition builds the adjudicator's terminal-tool definition.
//
// Every enumeration comes from vocabulary.Sets(), never from a literal here. The
// closed vocabulary has three consumers — this schema, the applier's validation,
// and the rule pack — and a schema that restated the sets would be the one that
// drifts, since it is the only one a compiler never checks against the others.
//
// Strict is deliberately FALSE, and for a sharper reason than F4's provider
// table. Strict mode requires every property to be listed in `required` at every
// object level, and the band set is a discriminated union: a rolling verdict
// declares exactly miss/partial/full and a no-roll verdict declares exactly auto,
// so a schema that demanded all four would force the model to author bands the
// engine must refuse (D12: the engine never fabricates a band the adjudicator
// did not declare). The banded shape cannot be expressed in strict mode's subset
// without lying about it — which is a second, independent reason the executor is
// the enforcement of record.
func verdictToolDefinition() agentic.ToolDefinition {
	return agentic.ToolDefinition{
		Name: VerdictToolName,
		Description: "Exit with your judgment of the player's action. Call this exactly once. " +
			"Report the fiction's own answer: whether it admits the action, what is at stake, what shape a " +
			"failure takes, whether the dice should be consulted at all, and what would change in the world " +
			"under each outcome you are entitled to declare. Every value is drawn from a closed vocabulary " +
			"and an unlisted value is rejected and returned to you for correction.",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"scalars", "bands", "rationale"},
			"properties": map[string]any{
				"scalars": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"plausibility", "risk", "consequence", "requires_roll"},
					"properties": map[string]any{
						"plausibility": enumProperty(vocabulary.KindPlausibility,
							"Whether the fiction admits the action at all."),
						"risk": enumProperty(vocabulary.KindRisk,
							"What is at stake if the action goes wrong."),
						"consequence": enumProperty(vocabulary.KindConsequence,
							"The shape a miss or a partial success takes."),
						"requires_roll": map[string]any{
							"type": "boolean",
							"description": "Whether the dice are consulted. This judgment is yours and is " +
								"read from the fiction, never from a table. True means you must declare the " +
								"miss, partial and full bands; false means you must declare exactly the auto " +
								"band, which is whatever the fiction already determined and is not necessarily " +
								"success.",
						},
					},
				},
				"modifiers": map[string]any{
					"type": "array",
					"description": fmt.Sprintf(
						"Typed adjustments to the roll. At most %d, each between %d and %+d, and they must "+
							"sum to between %d and %+d so that every band stays reachable — a verdict may not "+
							"pre-decide what the dice say.",
						payload.MaxModifiers, payload.MinModifierValue, payload.MaxModifierValue,
						payload.MinModifierTotal, payload.MaxModifierTotal),
					"maxItems": payload.MaxModifiers,
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"source", "value"},
						"properties": map[string]any{
							"source": enumProperty(vocabulary.KindModifierSource,
								"Where this adjustment comes from in the fiction."),
							"value": map[string]any{
								"type":    "integer",
								"minimum": payload.MinModifierValue,
								"maximum": payload.MaxModifierValue,
							},
							"note": map[string]any{
								"type":      "string",
								"maxLength": payload.MaxModifierNoteBytes,
								"description": "One clause naming the fact in the scene this adjustment " +
									"reads from.",
							},
						},
					},
				},
				"bands": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"description": "Proposed world changes per outcome. Declare exactly miss, partial and " +
						"full when requires_roll is true, or exactly auto when it is false. A band may be " +
						"empty; a band you were not entitled to declare is rejected.",
					"properties": map[string]any{
						string(vocabulary.BandAuto):    intentArraySchema("The single band of a verdict that declined the dice."),
						string(vocabulary.BandMiss):    intentArraySchema("What changes on a 6 or under."),
						string(vocabulary.BandPartial): intentArraySchema("What changes on a 7 to 9."),
						string(vocabulary.BandFull):    intentArraySchema("What changes on a 10 or over."),
					},
				},
				"rationale": map[string]any{
					"type":      "string",
					"maxLength": payload.MaxRationaleBytes,
					"description": "Your reasoning, in a short paragraph, for the player's resolution card. " +
						"It is never read by a rule.",
				},
			},
		},
	}
}

// intentArraySchema is one band's proposed effect intents.
func intentArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"type", "target"},
			"properties": map[string]any{
				"type": enumProperty(vocabulary.KindEffectType,
					"Which kind of change this is. It decides which of the fields below are allowed; "+
						"carrying a field belonging to another kind is rejected."),
				"target": map[string]any{
					"type":        "string",
					"description": "The six-part entity ID of the thing that changes.",
				},
				"attribute": enumProperty(vocabulary.KindAttribute, "set_attribute only."),
				"value": map[string]any{
					"type":        "integer",
					"description": "set_attribute only. Bounded per attribute.",
				},
				"status":   enumProperty(vocabulary.KindStatus, "set_status only."),
				"location": map[string]any{"type": "string", "description": "move_entity only: a location entity ID."},
				"relation": enumProperty(vocabulary.KindRelation, "add_relationship and remove_relationship only."),
				"object": map[string]any{
					"type":        "string",
					"description": "add_relationship and remove_relationship only: the other entity's ID.",
				},
			},
		},
	}
}

// narrationToolDefinition builds the narrator's terminal-tool definition.
//
// It has one property, and that is the point rather than an accident of scope.
// The narrator voices an outcome that is already committed: the classes were
// judged by the adjudicator, the band was chosen by seeded dice, and the world
// changes were validated and written by the applier. Any second field here would
// be a persona restating a decision it did not make, with nothing keeping the
// restatement and the record in agreement.
//
// Strict is TRUE here, and the contrast with the verdict tool is the argument for
// where enforcement lives: this schema satisfies strict mode's subset (one
// object, additionalProperties false, every property required, no nesting), so
// on an OpenAI-compatible runtime the provider will also refuse a malformed
// call. That is free upside and nothing depends on it — the executor validates
// identically, because on Anthropic and Gemini this flag is silently dropped
// (F4).
//
// Because it is strict, the schema stays inside the CONSERVATIVE subset: type,
// description, required, additionalProperties, and nothing else. The prose bound
// is stated in the description and enforced in the executor rather than declared
// as maxLength, deliberately — a strict schema carrying a keyword the runtime
// does not accept returns a 400 from the provider, and that is a failure a
// token-free CI run cannot see and a live run finds on its first turn. The
// verdict tool is not strict, so it spends validation keywords freely; this one
// cannot afford to, and the bound it gives up was never the enforcement of
// record anyway.
func narrationToolDefinition() agentic.ToolDefinition {
	return agentic.ToolDefinition{
		Name: NarrationToolName,
		Description: "Exit with the prose for this turn. Call this exactly once. You are voicing an outcome " +
			"that has already been decided and already changed the world: narrate it, and do not decide, " +
			"soften, or add to it.",
		Strict: true,
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"prose"},
			"properties": map[string]any{
				"prose": map[string]any{
					"type": "string",
					"description": fmt.Sprintf(
						"The narration the player reads. Plain prose — no headings, no lists, no mechanics. "+
							"Two to four sentences for an ordinary turn; %d bytes is the hard limit.",
						content.MaxProseBytes),
				},
			},
		},
	}
}

// strictSchemaKeywords is the conservative subset a schema declared Strict may
// use. It is asserted by TestNarrationSchema_StaysInsideTheConservativeStrict
// -Subset, which exists because the failure it prevents is invisible until a
// live run: a provider that rejects an unsupported keyword returns a 400, and
// every token-free test in this repo would still be green.
var strictSchemaKeywords = map[string]bool{
	"type": true, "description": true, "properties": true,
	"required": true, "additionalProperties": true, "enum": true, "items": true,
}

// StrictSchemaKeywords returns the conservative keyword subset a strict tool
// schema may use.
func StrictSchemaKeywords() map[string]bool {
	out := make(map[string]bool, len(strictSchemaKeywords))
	for keyword := range strictSchemaKeywords {
		out[keyword] = true
	}
	return out
}

// enumProperty renders one closed vocabulary set as a JSON-Schema string enum.
//
// The values are read out of vocabulary.Sets() at call time, so a value added to
// the vocabulary appears in the schema without anybody remembering to add it,
// and a kind that does not exist panics at construction rather than shipping a
// property with no enum — which would silently be "any string".
func enumProperty(kind vocabulary.Kind, description string) map[string]any {
	values, ok := vocabulary.Sets()[kind]
	if !ok || len(values) == 0 {
		// Unreachable for the registered kinds this file names, and a panic
		// rather than an empty enum because an enum-less string property is an
		// OPEN vocabulary wearing a closed one's clothes: it would validate at
		// the provider, sail through the schema, and be caught only by the
		// executor — one layer later than the model can act on.
		panic(fmt.Sprintf("persona: vocabulary kind %q has no registered values to build an enum from", kind))
	}
	return map[string]any{
		"type":        "string",
		"enum":        values,
		"description": description,
	}
}
