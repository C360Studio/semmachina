// Package mockmodel is the token-free model endpoint the turn loop runs
// against in CI: an HTTP stub speaking the OpenAI-compatible
// chat-completions contract that the framework's own model client already
// speaks, answering with responses a fixture scripts per persona role.
//
// # What it replaces, and what it deliberately does not
//
// The mock replaces the model and nothing else (design D11). The production
// agentic loop, the production tool executors, the payload registry, the NATS
// wire paths, and the framework's HTTP client all run unchanged; the only
// substitution is the process on the other end of the socket. A live-model run
// is therefore a `model_registry` retarget — point the endpoint URL somewhere
// real — with no code change anywhere, and live inference is never required by
// CI.
//
// The stub is re-derived from the model-endpoint contract as the pinned
// semstreams exposes it (model.EndpointConfig, processor/agentic-model's SDK
// and wire clients, model/wire's request and response types). Nothing here is
// lifted from semdragon: it is a pattern donor, not an import (M6).
//
// # Selection is structural, never prompt text
//
// A request is routed to a persona ROLE by what the request structurally is —
// the `model` field the endpoint config put on the wire, and/or the tool names
// the loop declared — and never by matching substrings of an assembled prompt.
// Prompt wording belongs to the prompt builder and will change; a fixture keyed
// on it goes silently wrong the first time someone rewrites a system message,
// and a silently-wrong fixture is worse than no fixture. The second selector is
// the active SCENARIO, which the harness sets explicitly.
//
// Everything the stub cannot route is a LOUD failure: an unmatched role, an
// ambiguous role, a role the active scenario does not script, and a script the
// caller has run off the end of all answer HTTP 400 with a stable failure code.
// 400 is chosen deliberately — the framework client treats 4xx as
// non-retryable, so a misconfigured fixture fails once and says why instead of
// being retried three times into a confusing timeout. There is no default
// "reasonable" response, ever.
//
// # Exhaustion is an assertion, not just hygiene
//
// Because a script holds exactly as many steps as the turn is allowed model
// calls, running off the end is precisely the failure that duplicate-delivery
// and crash-resume tests exist to catch: a resumed turn that re-invokes the
// adjudicator, or a redelivered action that adjudicates twice, gets a 400 and a
// named role instead of quietly costing a second (in production, billed) call.
// [Handler.Calls] is the direct form of the same assertion when a test would
// rather count than provoke.
//
// # Determinism
//
// Same scenario, same call order, same bytes. The stub reads no wall clock,
// draws no randomness, and never iterates a map to build a response: response
// and tool-call identifiers are derived from (scenario, role, call ordinal),
// `created` is a frozen constant, and scripted arguments are emitted from the
// fixture's own bytes. Replay and CI both depend on this, and a model endpoint
// is the easiest place in an otherwise-seeded system to leak a clock.
//
// The claim covers the SCRIPTED PAYLOAD. HTTP transport headers are the Go
// server's business and do carry a Date.
//
// # Bad output is a first-class capability
//
// F4 established that provider strict-mode is silently ignored on some
// runtimes, so executor-side validation is our enforcement of record — and a
// stub that could only emit valid exits would be unable to prove that
// enforcement works. Scripted steps therefore express out-of-vocabulary
// values, missing required fields, arguments that are not even valid JSON
// (`arguments_raw`), text-only completions with no tool call at all
// ([StepText]), truncation ([StepTruncated]), and provider failures
// ([StepHTTPError]). A step may also repeat forever, which is how a persona
// that never terminates — and therefore the MaxIterations cap (M5) — becomes
// testable.
//
// # Cost
//
// Every scripted step must declare token usage, and the stub refuses a fixture
// that omits it. Zero-cost turns are exactly the silent wrongness that would
// make the spend ledger and the flat-cost-per-turn claim untestable (M7);
// [Handler.Totals] is what a CI assertion measures.
package mockmodel
