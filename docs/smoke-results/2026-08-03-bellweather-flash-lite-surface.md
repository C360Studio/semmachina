# Bellweather Flash-Lite Surface Acceptance — 2026-08-03

## Result

**Passed.** The operator authorized the paid real-browser acceptance. The stack started at
`2026-08-03 17:36:23 America/Chicago`, and Playwright reported one passing test in 1.0 minute.
The run used Gemini 3.5 Flash-Lite through
`configs/instance.gemini35-flash-lite.bellweather.example.json`.

The command was:

```sh
SEMMACHINA_PAID_SMOKE=1 task smoke:gemini:surface
```

This record is separate from the
[2026-08-02 Gemini 3.6 Flash direct-smoke baseline](2026-08-02-bellweather-gemini.md). The older
result remains historical evidence and was not rerun or rewritten for this acceptance.

## Startup and readiness

The managed runner built the surface, started a fresh Bellweather engine and NATS state, exposed
the fixed loopback player, beta.159 GraphQL, and diagnostic endpoints, started the loopback Node
surface, and placed the HTTPS/WSS test proxy in front of it. Two independent diagnostic `/ready`
polls returned HTTP 200 before the browser journey relied on the stack.

The closed checkpoints reached, in order:

```text
runner_started
build_complete
stack_ready
diagnostic_ready
surface_ready
proxy_ready
browser_tests_started
unauthorized_world_verified
surface_document_loaded
login_submitted
login_http_verified
action_controls_visible
schematic_world_visible
clock_visible
pre_action_ready
first_action_submit_started
first_action_accepted
first_turn_complete
second_action_submit_started
second_action_accepted
second_turn_complete
browser_tests_complete
```

## Browser and game evidence

The browser first proved the action-free boundary: unauthenticated world projection returned 401,
creator login succeeded over the same origin, all five Bellweather locations appeared as labelled
schematic nodes, and the clock reported `not_configured`.

The paid journey then proved:

- The first fixed body-observation action produced one unique terminal card and advanced the
  authoritative case phase to `discovery`.
- Selecting Continue removed that first terminal card before the next action.
- The second fixed hint request produced a different turn ID and one unique terminal card.
- The second card contained the exact Kit entity and visible Kit text.
- The diagnostic `kit_hint_proof` was true. Its strict parser admits only the persisted
  `request_hint` / `player-hint` / `case-decision` route.

## Authority and secret boundary

The browser made only same-origin HTTPS requests and opened only the same-origin
`wss://127.0.0.1:4181/api/player` socket. It sent no browser `Authorization` header and made no
direct GraphQL or NATS request. The upstream player Bearer was absent from browser storage, DOM,
resource URLs, and the rendered page.

No Gemini key, creator credential, player Bearer, provider request or response body, prompt, cost,
or billing detail was printed or recorded.

## Teardown

The managed process-group teardown completed. No listeners remained on `4173`, `4181`, `43101`,
`43102`, or `43103`; no generated binary remained. Docker was empty after one follow-up Ryuk poll.
