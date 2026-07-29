// Package persona is where the One Design Rule is actually tested.
//
// "Agentic judges fiction, rules match structure, components execute work" is a
// claim about a boundary, and this package IS that boundary: the two places in
// the engine where a language model's output stops being prose and becomes
// state. Everything downstream — the dice, the applier, the ledger, the rule
// pack — is deterministic and rule-opaque, and every one of them is only as
// trustworthy as the exit that fed it.
//
// # Two personas, two exits, one discipline
//
// The ADJUDICATOR reads the player's words and the current scene and exits once,
// through submit_verdict, with the slice's only schema-bearing structure: four
// closed-vocabulary scalars, a bounded typed modifier list, and proposed effect
// intents grouped by outcome band. The NARRATOR runs after the outcome is
// committed and exits through submit_narration with prose and nothing else — it
// voices what happened and has no tool that could change it.
//
// Both exits obey the same three rules:
//
//   - The VOCABULARY IS CLOSED AND THE EXECUTOR IS WHAT CLOSES IT. Provider
//     strict-mode is honoured on OpenAI-compatible runtimes, silently ignored on
//     Anthropic and Gemini, and best-effort on Ollama (F4), so schema enforcement
//     at the provider is a hint and never a guarantee. Every class, every effect
//     type, every bound, and the coherence between the reported roll gate and the
//     declared band set are checked here, at the tool boundary, before anything is
//     stored. A rejection is returned as ToolErrorInvalidArgs with the allowed set
//     named, so the model can correct itself on the next iteration rather than
//     costing the player a turn.
//   - IDENTITY IS INJECTED, NEVER ASKED FOR. The turn, the action, and the scene
//     are engine knowledge. A tool schema that asked the model to echo them could
//     not be scripted deterministically and would be hallucinated by a live model,
//     so they arrive on the tool call's metadata — set by the spawner, propagated
//     by the loop, unreachable from anything the model writes. The model supplies
//     judgment; the executor supplies who it is about. This is upstream's own
//     "attribution is derived, not supplied" (ADR-080), applied to the game.
//   - CONTENT FIRST, REFERENCE LAST. What the persona produced goes to the
//     content store before the triple that points at it reaches the graph. A
//     reference to a missing object is a correctness bug — a turn whose result
//     cannot be delivered or re-derived — while an unreferenced object is garbage
//     the next attempt overwrites at the same derived key.
//
// # What lands on the graph
//
// Only what a rule can match. The verdict projects four scalars plus one
// reference; the narration projects one reference and nothing else, because the
// narrator restating a decision it did not make would put a second, softer copy
// of the world's state on the rule-matching surface. The banded intents, the
// modifiers, the rationale, and the prose all travel by reference (F6).
//
// # Bounded cognition
//
// Every spawned loop carries an explicit per-task iteration budget and a
// timeout. The loop's ENFORCEMENT of that budget is upstream's; what is ours is
// the budget we hand it and the explicit failure we record when it runs out —
// RecordCapExhausted, which ends the turn with a closed reason code rather than
// leaving a player waiting on a persona that will never exit.
package persona
