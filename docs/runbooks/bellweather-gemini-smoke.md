# Bellweather Gemini Smoke

This is an operator-only, paid production smoke. A successful authorized run is recorded under
[Recorded runs](#recorded-runs). Run it again only after an operator explicitly authorizes the
additional provider spend.

The smoke boots the ordinary Bellweather package and production components, connects through
the authenticated player WebSocket, and executes two bounded provider turn chains:

1. Rowan observes Harold Wren's body and the case advances from `cold_open` to `discovery`.
2. Rowan explicitly asks Kit Finch for a hint and receives a committed, narrated Kit exchange.

The current acceptance default is Gemini 3.5 Flash-Lite through the dedicated
`configs/instance.gemini35-flash-lite.bellweather.example.json` configuration. The recorded
2026-08-02 run used Gemini 3.6 Flash and remains historical baseline evidence; it does not define
the current acceptance model or configuration.

The target is intentionally absent from the default Taskfile path and GitHub Actions. Unit and
deterministic full-stack acceptance do not call Gemini.

## Prerequisites

- Run from the repository root with the repository's supported Go and Task versions available.
- Make NATS available at `nats://127.0.0.1:4222`, as pinned by the smoke instance configuration.
- Allow outbound access to the configured Gemini endpoint.
- Obtain a Gemini API key that the operator is authorized to spend against.
- Keep the Bellweather fixture and
  `configs/instance.gemini35-flash-lite.bellweather.example.json` unchanged for the acceptance run.

The command creates a fresh world namespace by appending a UTC nanosecond run ID to
`bellweather-gemini35-flash-lite-smoke`. It therefore starts from the authored `cold_open` phase
instead of inheriting graph state from a previous smoke.

## Configure the local environment

Create the ignored local file once:

```sh
cp .env.example .env
```

Set both values in `.env` without committing or printing the key:

```dotenv
GEMINI_API_KEY=replace-with-the-operator-key
SEMMACHINA_PAID_SMOKE=1
```

Both gates are required. The Taskfile checks the exact paid opt-in and a non-empty key, and the
smoke command independently checks them again before boot. Leave
`SEMMACHINA_PAID_SMOKE=0` during ordinary development.

Do not use `env`, `printenv`, shell tracing, or command-line key arguments while diagnosing this
run. Task loads `.env` directly, and `.gitignore` excludes the file while retaining the blank
`.env.example` template.

## Run

Explicit operator authorization is the final prerequisite. Once it has been given, run:

```sh
task smoke:gemini:bellweather
```

The target invokes the fixed Gemini 3.5 Flash-Lite Bellweather configuration and package. Do not
add it to `default`, `test`, or CI, and do not replace the fixed actions with an open-ended play
session.

## Active evidence and abort policy

For each accepted action, the driver reads authoritative graph and durable-queue state every
30 seconds. Its snapshot includes the turn phase, pending work, stage and agent stream progress,
and player-action consumer progress. Logs report only closed operational fields such as action,
turn, phase, pending count, case phase, companion kind, and the fixed provider-chain count.

The no-movement observation budget is phase-aware:

| Current turn phase | Budget without phase or queue movement |
| --- | ---: |
| `accepted`, `resolving`, `applying` | 60 seconds |
| `interpreting`, `adjudicating`, `companion` | 120 seconds |
| `narrating` | 150 seconds |

Any phase change or authoritative queue-position change resets the current observation budget.
The driver also enforces a 180-second paid cap for each turn and a 390-second paid cap for the
whole smoke. These caps are distinct from the observation budget and report which cap ended the
run. The per-turn clock starts before the WebSocket action write, and that write carries the same
deadline; clearing the socket deadline after the frame keeps later writes independent.

The driver fails fast when any of these conditions is proved:

- The turn reaches the `failed` terminal phase.
- Graph, stream, consumer, queue, WebSocket, authentication, or configuration state is unreadable.
- The current phase's observation budget expires without a phase or queue-position change.
- A turn reaches its 180-second paid cap before terminal delivery.
- The whole smoke reaches its 390-second paid cap before completion.
- A turn is authoritatively complete but its terminal WebSocket delivery does not arrive within
  60 seconds.
- The body-observation chain does not reach authoritative `discovery` within 30 seconds after
  its terminal delivery.

The 60-second post-completion delivery bound and 30-second case-phase bound are separate evidence
checks; they do not replace or extend the paid caps. A correction round can legitimately continue
inside an agentic phase without changing the graph phase or observable queue position. The driver
cannot prove that hidden in-loop progress, so a slow correction can exhaust an observation budget
even while the engine is still working. Treat that result as a conservative paid-acceptance abort,
not as proof that the engine wedged.

The second chain is accepted only if the terminal player result names Kit and carries narrated
companion output. The driver then reads the turn entity and proves the exact persisted route:
`request_hint`, `player-hint`, and `case-decision`.

Do not treat quiet logs as success. Success is the final `Bellweather Gemini smoke passed` record
after both terminal deliveries, the discovery-phase proof, the persisted Kit-hint route, and
authoritative queue/turn progress. A non-zero exit is failure; retain the secret-safe stderr and
exit status for diagnosis.

## Failure handling

Do not rerun immediately after a failure, because each retry can incur another two provider turn
chains. First classify the returned error and the last authoritative `phase` and `pending` record:

- `turn reached the failed terminal phase` is terminal game-processing evidence.
- `agentic observation budget reached ... without phase or queue movement` means the phase-aware
  acceptance budget expired. It does not prove an engine wedge; inspect queue and provider evidence
  before making that claim.
- `paid per-turn cap ... reached` and `paid whole-smoke cap ... reached` identify which hard spend
  boundary cancelled the run. They also do not, by themselves, prove an engine wedge.
- A terminal-delivery timeout proves authoritative completion without timely player egress.
- A case-phase timeout proves that the completed body-observation turn did not advance the
  authoritative case to `discovery` within its separate 30-second evidence window.
- An authoritative read error identifies the graph, stream, consumer, or queue surface that failed.
- An opt-in, key, binding, authentication, or boot error occurs before useful acceptance evidence.

Call the engine wedged only when independent authoritative state proves it, for example a durable
work item whose delivery and acknowledgement state cannot advance. The smoke's acceptance-cost
timeouts intentionally stop earlier than that diagnosis in some correction-round cases.

The diagnostic logger deliberately receives no provider request body, prompt, response body,
credential, or configuration body. Preserve that property when adding diagnostics. Revoke the key
if it is ever exposed outside the ignored `.env` file or provider-secret environment.

## Teardown and recording

On every normal success or returned error, the command stops the production engine and closes the
WebSocket and observer client. The 390-second whole-smoke context also returns through that teardown
path.
If the process is forcibly terminated, verify that no smoke process remains before another run.

The fresh namespace prevents cross-run acceptance contamination; it does not promise to purge
persisted NATS data. Apply the deployment's normal NATS retention or operator cleanup policy after
capturing evidence.

Record the following separately from deterministic acceptance:

- explicit operator authorization;
- UTC start time and command exit status;
- final success record, or the last authoritative phase/pending record plus returned error;
- whether discovery and the persisted Kit-hint route were proved;
- provider billing or usage evidence available to the authorized operator;
- teardown confirmation.

Never record the Gemini key, `.env` contents, prompt bodies, provider responses, or player bearer
credential.

## Recorded runs

- [2026-08-02 authorized Gemini 3.6 Flash baseline](../smoke-results/2026-08-02-bellweather-gemini.md)
