# Minimal Creator Surface Operations

This runbook covers the local SvelteKit creator surface, its bounded same-origin player bridge,
and its server-only SemStreams GraphQL projection. One deployed surface represents one configured
world and player. The browser is presentation only: it receives neither the upstream player Bearer
nor general GraphQL or NATS authority.

Use the action-free preflight before any paid acceptance. The paid browser journey is an
operator-authorized production smoke and uses Gemini 3.5 Flash-Lite.

## Prerequisites

- Run from the repository root on a POSIX host with the supported Go, Node, npm, and Task versions.
- Install the lockfile-pinned web dependencies with `task frontend:install`.
- Install the Playwright browser required by the repository if it is not already present.
- Keep Docker available for the fresh local NATS broker used by the real-stack harness.
- Keep ports `4173`, `4181`, `43101`, `43102`, and `43103` free for the managed local journey.
- Do not expose the loopback player, GraphQL, diagnostic, or surface HTTP listeners to the browser.

The managed runner builds the web application, starts a fresh Bellweather engine and NATS state,
starts the beta.159 GraphQL read surface, starts the loopback Node server, adds the test HTTPS/WSS
proxy, runs Playwright, and tears down every owned process group.

## Safe local startup

Run the action-free real-stack preflight first:

```sh
task smoke:surface:preflight
```

This path supplies no real provider key and submits no player action. It proves startup, denied
unauthenticated projection, creator login, the same-origin WSS bridge, latest-result retrieval,
the Bellweather schematic map, the explicit unconfigured clock state, and browser authority
confinement. Passing preflight does not authorize the paid journey.

For normal deployment or manual local integration, build and start the custom adapter-node server:

```sh
npm --prefix web run build
npm --prefix web start
```

Supply the server environment described below through a secret-capable process supervisor. The
Node server must bind to loopback and must sit behind an HTTPS proxy. A direct browser connection to
the plain loopback server is deliberately refused because it has no trusted transport attestation.
The repository's `web/tests/loopback-https-proxy.mjs` is a self-signed acceptance fixture, not a
production TLS configuration.

## TLS and proxy boundary

The only supported surface posture is `trusted_loopback_proxy`:

- Set `HOST` to a literal loopback address and `PORT` to the internal Node listener.
- Set `ORIGIN` and `SEMMACHINA_PUBLIC_ORIGIN` to the same externally visible HTTPS origin.
- Set `SEMMACHINA_TLS_POSTURE=trusted_loopback_proxy`.
- Terminate TLS at a proxy that alone can reach the loopback Node listener.
- Forward exactly one `Host` matching the public origin and one `X-Forwarded-Proto: https` value.
- Proxy `/api/player` WebSocket upgrades without changing the path or query.
- Refuse browser or sibling-workload access to the internal HTTP, player WS, GraphQL, NATS, and
  diagnostic listeners.

The surface strips any browser-supplied internal transport header and creates its own process-local
attestation only after the raw request arrived from loopback with the exact proxy headers. Do not
configure `ADDRESS_HEADER` or `XFF_DEPTH`; startup rejects both. A successful startup check does not
prove firewall or network-policy isolation, which remains a deployment acceptance requirement.

## Deployment isolation and acceptance

The approved minimal-surface deployment assumes one exclusive `surface-runtime` network namespace:

- The real Svelte Node server, GraphQL endpoint, and upstream player socket bind only to loopback
  inside that namespace.
- Only the TLS proxy binds to a container or host interface reachable from another namespace.
- The browser-facing workload and every untrusted sibling workload run in distinct network
  namespaces from `surface-runtime` and from each other.
- No untrusted sidecar or process may share the `surface-runtime` network namespace. Such a process
  can reach loopback and invalidates this security boundary, so its presence blocks deployment.
- GraphQL, Node HTTP, and player ports are neither interface-bound nor published. The public proxy
  is the only ingress.

The reference acceptance uses three workload identities: the exclusive surface runtime, an
`edge-probe` attached only to the browser-facing network, and a `sibling-probe` attached only to a
separate application network. Neither probe receives secrets, mounts, the Docker socket, or a
shared namespace. Both probes must resolve the same surface-runtime service identity used for the
allow and deny checks; a DNS failure is not proof of isolation.

With Docker available, run the deterministic no-provider gate:

```sh
task smoke:surface:isolation
```

The task builds the actual surface before running the containerized proof. It requires neither
`GEMINI_API_KEY` nor `SEMMACHINA_PAID_SMOKE` and must not invoke a model provider.

Run deployment acceptance in this order:

1. Start a live GraphQL fixture inside `surface-runtime` whose valid location label contains a
   per-run nonce. Start the actual built Svelte server against that loopback GraphQL endpoint.
2. From `edge-probe`, reach the public HTTPS proxy, complete real preauthentication and creator
   login, and read `/api/world`. The closed world DTO must contain the nonce. This proves GraphQL is
   live and the actual surface can read it before any deny result is accepted.
3. From `sibling-probe`, reach the same public proxy and receive the expected unauthenticated
   response. This proves that probe has a working network path and service identity.
4. From each probe, attempt a bounded TCP connection and an exact GraphQL POST to the runtime
   service identity on the internal GraphQL port. The expected result is connection refusal or
   timeout with no HTTP response and no nonce.
5. Treat any successful TCP connection as failure, even if the later HTTP response is `401`, `403`,
   `404`, or empty. An application-layer denial means the internal endpoint was reachable.
6. Inspect runtime topology: the GraphQL, Node, and player listeners are loopback-only; the GraphQL
   port is not published; and neither probe shares the runtime network namespace.
7. Tear down uniquely named containers and networks on success, failure, or interruption, then
   verify that none remain.

Use short explicit probe deadlines and emit bounded pass/fail labels only. Do not print credentials,
cookies, environment values, raw GraphQL documents or responses, or the nonce-bearing DTO.

The following are supporting checks, not deployment-isolation proof:

- startup configuration and loopback-address validation;
- CORS or content-security policy;
- a browser audit showing that one tested page made no GraphQL request;
- a host-loopback test where the surface and hostile probes share the host network namespace;
- absence of a published port without a live-read proof and hostile-source TCP probes.

Firewall rules, separate network namespaces, or an equivalently enforced network policy create the
boundary. Startup checks, browser controls, and no-request assertions cannot substitute for them.
Task 2.2 remains open until this live-read-first acceptance passes in the supported deployment and
its automated gate.

## Same-origin authentication and player bridge

Configure these server-only values:

| Variable | Contract |
| --- | --- |
| `SEMMACHINA_PUBLIC_ORIGIN` | Exact HTTPS origin with no path, query, fragment, or URL credentials. |
| `SEMMACHINA_TLS_POSTURE` | Must be `trusted_loopback_proxy`. |
| `SEMMACHINA_CREATOR_CREDENTIAL` | Separate 16–4096 byte printable server secret used only for creator login. |
| `SEMMACHINA_PLAYER_BEARER` | Existing upstream player Bearer; must differ from the creator credential. |
| `SEMMACHINA_PLAYER_WS_URL` | Fixed `ws://` literal-loopback `/play` endpoint with no query or credentials. |
| `SEMMACHINA_PLAYER_ID` | Exact canonical player entity inside the configured world scope. |
| `SEMMACHINA_SESSION_TTL_SECONDS` | Optional `60`–`3600`; defaults to `300`. |

The browser first obtains a bounded preauthentication CSRF proof, then sends the creator credential
once to the same-origin HTTPS login route. Success rotates any prior session and returns an opaque,
secure, HTTP-only `__Host-` session cookie plus a session-bound CSRF proof. Expiry, logout, or a later
successful login invalidates the old session for both projection reads and WebSocket upgrades.

The browser opens only same-origin `wss://<public-host>/api/player` using the player protocol and
session CSRF subprotocol. The server selects the configured player ID, upstream loopback `/play`
endpoint, and Bearer. Browser input cannot override them. The bridge relays bounded valid text bytes;
it does not add sequencing, replay, adjudication, correlation, or game state.

## World and GraphQL configuration

The real Bellweather harness starts from
`configs/instance.gemini35-flash-lite.bellweather.example.json`. That dedicated instance binds the
Bellweather package, Rowan, Kit, and the tool-capable `gemini-3.5-flash-lite` default. The runner
adds a fresh timestamped world namespace and publishes the exact player WS, GraphQL, diagnostic,
world-prefix, and campaign identities only to the child processes that need them. Upstream endpoints
and credentials remain server-side; the browser receives closed scoped projection and player data.

Configure the projection server with:

| Variable | Contract |
| --- | --- |
| `SEMMACHINA_WORLD_ORG` | Canonical organization component. |
| `SEMMACHINA_WORLD_NAMESPACE` | Canonical namespace for this deployment's one active world. |
| `SEMMACHINA_WORLD_TEMPLATE` | Canonical template component. |
| `SEMMACHINA_GRAPHQL_URL` | Fixed HTTP(S) URL whose exact path is `/graphql`, with no query or credentials. |
| `SEMMACHINA_GRAPHQL_POSTURE` | One of `loopback`, `network_policy`, or `auth_proxy`. |
| `SEMMACHINA_GRAPHQL_AUTH_TOKEN` | Required only for `auth_proxy`; a server-only Bearer sent to its HTTPS endpoint. |
| `SEMMACHINA_GRAPHQL_PROJECTION_DEADLINE_MS` | Optional `1000`–`30000`; defaults to `5000`. |

`loopback` requires a literal loopback GraphQL endpoint. `network_policy` requires deployment-proven
surface-only reachability. `auth_proxy` requires HTTPS and its dedicated token. The token is invalid
under either other posture. Redirects are not followed, raw GraphQL documents are not accepted from
the browser, and complete upstream responses are validated before closed browser DTOs are created.

The configured organization, namespace, and template derive one exact scope prefix. Player identity,
locations, relationships, and an optional clock entity must remain inside it. The UI exposes no
world switcher, arbitrary prefix, or endpoint override. Deploy another process with another fixed
configuration to serve another world.

## Authored and schematic map behavior

The projection reads labelled location entities and directed `connects-to` topology without writing
anything back to the graph.

- A location with canonical authored latitude and longitude keeps those exact values.
- A location without coordinates receives a deterministic labelled schematic position derived from
  topology and stable entity ordering.
- In a mixed world, authored nodes remain fixed and only unpositioned nodes use the fallback.
- Directed edges remain directed; author the reverse relationship when travel is bidirectional.
- Disconnected and topology-only locations remain visible.
- If any fallback position is present, the UI says that schematic mode is in use.

The schematic is an orientation aid, not inferred geography. Refreshing reprojects authoritative
graph state; neither the browser nor the server persists fallback positions or map artifacts.

## Campaign clock states

Omit all five clock variables to return the explicit `not_configured` state:

- `SEMMACHINA_CLOCK_ENTITY_ID`
- `SEMMACHINA_CLOCK_PREDICATE`
- `SEMMACHINA_CLOCK_LABEL`
- `SEMMACHINA_CLOCK_UNIT`
- `SEMMACHINA_CLOCK_VALUE_TYPE`

To configure a clock, set all five. The entity must be inside the active world, the predicate must
be canonical, and `SEMMACHINA_CLOCK_VALUE_TYPE` must be `number`. The browser then displays the
configured label, authoritative finite numeric value, and unit.

A partial configuration blocks startup. A missing, ambiguous, malformed, or out-of-scope configured
fact is a typed projection failure; it is not disguised as `not_configured`. The readout never ticks,
interpolates, chooses pacing policy, advances a deadline, or mutates the graph. It changes only when
the authoritative fact changes and the projection is refreshed.

## Paid Flash-Lite browser acceptance

The paid journey uses the same managed stack as preflight, then submits the two fixed Bellweather
actions through the real browser surface. Review
[Bellweather Gemini Smoke](bellweather-gemini-smoke.md) for provider-key handling, paid authorization,
observation budgets, correction-round limitations, and failure classification.

Create the ignored `.env` from `.env.example`, set the authorized `GEMINI_API_KEY`, and leave
`SEMMACHINA_PAID_SMOKE=0` until the operator approves the spend. After approval, run:

```sh
SEMMACHINA_PAID_SMOKE=1 task smoke:gemini:surface
```

Success requires the final `browser_tests_complete` checkpoint after both unique turns complete,
the first turn advances the case to `discovery`, the second proves the persisted Kit hint route,
the browser stays on same-origin HTTPS/WSS, and teardown removes every owned listener and process.
Do not infer success from quiet logs or from preflight alone.

The successful reference run is recorded at
[2026-08-03 Bellweather Flash-Lite surface acceptance](../smoke-results/2026-08-03-bellweather-flash-lite-surface.md).

## Secret-safe troubleshooting

Do not use `env`, `printenv`, shell tracing, command-line secret arguments, browser storage, or copied
request bodies to diagnose this surface. Do not log GraphQL response bodies, provider prompts or
responses, cookies, CSRF proofs, the creator credential, the player Bearer, proxy tokens, or the
Gemini key. The custom server intentionally reports only that startup failed before listen.

Use closed state and status evidence:

- **Startup fails before listen:** Check that required variable names are present, values have the
  documented shape, credentials are distinct, `HOST` is loopback, and player/world scope matches.
  Do not print values.
- **Direct HTTP returns `401` or an upgrade is refused:** Confirm the request came through the HTTPS
  proxy with the exact public Host and one `X-Forwarded-Proto: https`. Do not bypass attestation.
- **`/api/world` returns `401` through HTTPS:** Reauthenticate through the UI; verify the opaque
  session is live and has not expired, rotated, or logged out.
- **Projection returns a closed `502` error:** Check GraphQL reachability, posture, deadline, exact
  scope, and upstream schema health without recording the raw response.
- **Player WSS upgrade is refused:** Check the exact public origin, `/api/player` path, session
  cookie and CSRF flow, and fixed upstream loopback `/play` listener.
- **Map says schematic:** This is expected when one or more locations lack authored coordinates.
  Inspect the [world-authoring guide](../guides/world-authoring.md) rather than treating fallback
  placement as stored geography.
- **Clock says not configured:** Either intentionally leave all five variables absent or configure
  all five. Never patch around a partial or malformed fact.
- **Runner exits or is interrupted:** Verify ports `4173`, `4181`, and `43101`–`43103` have no
  listeners before retrying. A paid retry requires new authorization.

For the managed journey, retain only the exit status, closed checkpoints, safe diagnostic phase and
case fields, assertion summary, and teardown proof. Revoke any secret that appears outside its
intended server environment.
