# world-graph-projection Specification

## Purpose
Defines a least-authority, read-only projection of the active world's public place graph into an
honest map with authored geometry and deterministic topology fallback.
## Requirements
### Requirement: Graph transport has one trusted server-only destination
The GraphQL endpoint SHALL be fixed by server deployment configuration, disable redirects, and use
normal TLS certificate and hostname validation. Startup SHALL block unless the endpoint resolves to
loopback or a Unix-local socket, is protected by enforced network policy/firewall that permits only
the surface-server workload, or is reached through an explicitly configured authenticated proxy.
RFC1918, ULA, or other private-address classification alone SHALL NOT satisfy this boundary. The
browser SHALL NOT supply or override endpoint scheme, host, port, path, credentials, access posture,
or TLS policy.

Startup SHALL validate the configured posture, but successful startup connectivity SHALL NOT count
as proof of external access control. Deployment acceptance SHALL prove that a browser and an
untrusted sibling workload cannot directly connect to or read the graph endpoint. Missing policy,
firewall, proxy, or deployment evidence SHALL block release.

#### Scenario: An unsafe endpoint blocks startup
- **WHEN** the endpoint redirects, cannot pass normal TLS validation, or relies only on a private
  address without loopback/Unix locality, enforced surface-only network policy, or an authenticated
  proxy
- **THEN** the surface fails startup rather than weakening TLS, following a redirect, or serving a
  projection route

#### Scenario: Browser input cannot redirect graph traffic
- **WHEN** a projection request supplies an endpoint or upstream credential
- **THEN** the server refuses the input and sends no request outside the fixed deployment endpoint

#### Scenario: Direct graph access is unavailable outside the surface server
- **WHEN** deployment acceptance probes from the browser-facing network and an untrusted sibling
  workload
- **THEN** neither can connect to or read the graph endpoint, while the authorized surface server
  reaches it through the declared local, policy-enforced, or authenticated-proxy posture

#### Scenario: Startup checks do not substitute for network authorization
- **WHEN** endpoint configuration passes process startup checks but the deployment cannot prove its
  firewall, network policy, or proxy excludes untrusted workloads
- **THEN** release remains blocked even though the surface process itself can connect

### Requirement: Graph reads require an authenticated principal and exact component scope
The pure graph adapter SHALL accept an authenticated principal interface carrying immutable
deployment identity and exact canonical world components. Its production provider SHALL default to
deny, and no upstream request SHALL occur until the same-origin session boundary supplies a live
principal. The adapter SHALL parse canonical entity IDs and compare their components rather than
using raw string-prefix membership. It SHALL fully validate every requested and returned entity and
relationship endpoint before returning any data; one out-of-scope value SHALL fail the whole
response with no silent filtering.

#### Scenario: A valid map request stays inside the active world
- **WHEN** an authenticated session requests its map projection
- **THEN** the principal supplies the exact deployment scope and the server returns a typed map view
  model only after full response validation

#### Scenario: A forged prefix is not a query parameter
- **WHEN** a browser supplies a world namespace, entity prefix, raw GraphQL document, or out-of-scope
  entity identifier
- **THEN** the adapter refuses the input without forwarding it upstream or disclosing graph data

#### Scenario: Similar textual prefixes are different worlds
- **WHEN** the active canonical world component is `world1` and a response contains a valid entity
  whose world component is `world10`
- **THEN** component-aware validation fails the whole projection instead of accepting the textual
  prefix overlap

#### Scenario: An out-of-scope result fails closed
- **WHEN** an upstream response contains an entity or relationship endpoint outside the derived
  active-world prefix
- **THEN** the adapter returns a typed projection error and returns no partial map

#### Scenario: No session means no graph request
- **WHEN** the production principal is absent, expired, logged out, or otherwise unauthenticated
- **THEN** the adapter refuses by default before dialing the fixed GraphQL endpoint

### Requirement: Raw GraphQL responses become closed DTOs only after full validation
The server SHALL decode and validate the complete upstream response, including GraphQL errors,
known and unknown fields, bounds, canonical IDs, relationships, and typed values, before constructing
a closed browser DTO. Exact entity reads SHALL consume `ExactEntity { entity, kvRevision }`, prefix
reads SHALL consume `EntityPage { entities, next_cursor }`, and relationships SHALL consume only
canonical `from`, `to`, and `predicate`. Retired aliases and unknown envelope fields SHALL fail the
whole response. Raw response bodies, upstream error details, revision values, cursors, arbitrary
triples, and secret values SHALL NOT be logged, cached, embedded in errors, or returned to the
browser.

#### Scenario: Raw secret fields are stripped at the boundary
- **WHEN** a fully valid in-scope entity response contains fields or triples outside the closed place
  DTO, including a secret value
- **THEN** the adapter uses only allowlisted facts needed for the DTO and neither logs, caches,
  returns, nor embeds the raw or secret content in an error

#### Scenario: A retired relationship alias is refused
- **WHEN** a relationship response uses a beta.159 alias instead of canonical `from`, `to`, and
  `predicate`
- **THEN** the adapter fails the entire response and returns no partial topology

### Requirement: Exhaustive map discovery follows bounded beta.160 pages
The exhaustive map SHALL use only `entity`, `entitiesByPrefix`, and `relationships`; it SHALL NOT
call `spatialSearch`. It SHALL follow opaque `EntityPage.next_cursor` values until completion while
enforcing a page size of 200, at most 10 pages, fewer than 1,000 aggregate entities, 2,048 triples
per entity, and a 1 MiB cap per GraphQL response. A repeated cursor, empty page with a continuation,
malformed page, out-of-scope entity on any page, or aggregate-cap breach SHALL return typed failure
and no map.

For a supported location set, the adapter SHALL make at most 999 relationship calls, accept at most
999 relationships per location and 998001 total, and require every raw edge to be well-formed and in
exact scope before predicate selection. Every `location.relation.connects-to` edge SHALL connect two
locations in the validated set. Other valid in-scope predicates MAY be omitted only after complete
validation because they are outside the closed map DTO. Any exceeded bound, dangling topology
endpoint, malformed relationship, or out-of-scope edge SHALL fail the whole projection.

#### Scenario: A multi-page map is supported
- **WHEN** `entitiesByPrefix` returns fully valid in-scope pages whose opaque cursors terminate
  within every aggregate bound
- **THEN** the adapter completes bounded location and relationship validation before returning the
  exhaustive map DTO

#### Scenario: Pagination cannot claim partial completeness
- **WHEN** a cursor repeats, a continuation follows an empty page, a later page is malformed or out
  of scope, or a configured aggregate bound is reached
- **THEN** the adapter returns no map and makes no spatial query to guess at omitted state

#### Scenario: Spatial search is never an exhaustive fallback
- **WHEN** the exhaustive map is requested at any supported or exceeded size
- **THEN** no `spatialSearch` operation is issued

#### Scenario: A relationship response is unsafe
- **WHEN** relationship bounds are exceeded or any edge is malformed, dangling, or outside exact
  component scope
- **THEN** the complete map projection fails without returning a filtered or partial topology

### Requirement: Transient index readiness has an accessible explicit retry
An upstream `index_not_ready` error SHALL remain distinguishable from permanent projection errors.
The creator surface SHALL announce the transient failure in a live status region, expose an
explicit retry control, and restore usable controls and focus for the next attempt. It SHALL NOT
permanently latch the first error or retry indefinitely without the user.

#### Scenario: The index becomes ready after the first projection attempt
- **WHEN** the first projection returns `index_not_ready` and the user activates retry after the
  index becomes ready
- **THEN** the surface requests a fresh complete projection, restores the world view, and leaves no
  stale error state or disabled controls

#### Scenario: A permanent projection error is not disguised as transient
- **WHEN** projection fails because an envelope is malformed, out of scope, or over a bound
- **THEN** the surface reports a closed failure and does not label it as `index_not_ready`

### Requirement: Upstream query gaps remain explicit deployment gates
The starter SHALL remain inside the local safety envelope: loopback/Unix-local reachability,
deployment-proven surface-only network policy, or authenticated proxy; full raw validation followed
by a closed DTO; bounded beta.160 pagination; no spatial query; and canonical relationship fields.
Production or scale claims SHALL remain gated by the focused SemStreams issues for
authentication/scope ([#882](https://github.com/C360Studio/semstreams/issues/882)), response
selection minimization ([#883](https://github.com/C360Studio/semstreams/issues/883)), prefix
pagination at larger scale ([#884](https://github.com/C360Studio/semstreams/issues/884)), spatial scoping/pagination
([#885](https://github.com/C360Studio/semstreams/issues/885)), and relationship schema/runtime
consistency ([#886](https://github.com/C360Studio/semstreams/issues/886)).

#### Scenario: Starter constraints do not become production claims
- **WHEN** the starter passes inside the authorized-network, bounded, non-spatial, canonical adapter
  envelope
- **THEN** acceptance proves that bounded local journey only and does not declare the five upstream
  production/scale gaps resolved

### Requirement: The map is a projection of authoritative place facts
The map SHALL contain typed location nodes, player-safe labels, directed
`location.relation.connects-to` edges, and optional canonical authored coordinates from the active
world. It SHALL not expose arbitrary entity triples or treat nearby coordinates as connectivity.
Refreshing the map SHALL re-project authoritative graph state; neither browser nor server SHALL
write map positions or derived connectivity back into the world.

#### Scenario: Authored geometry controls placement
- **WHEN** a location carries a valid authored latitude/longitude pair
- **THEN** its map position comes from that pair and automatic layout does not move or replace it

#### Scenario: Connectivity remains directed graph data
- **WHEN** location A connects to location B and no reverse edge is authored
- **THEN** the map shows the A-to-B connection and does not infer B-to-A from distance or appearance

#### Scenario: Rendering does not create world facts
- **WHEN** the map lays out, pans, zooms, or refreshes
- **THEN** it performs no graph mutation and persists no derived map artifact as authoritative state

### Requirement: Topology-only worlds receive a deterministic schematic
Locations without authored coordinates SHALL receive deterministic labelled schematic positions
computed from the directed topology and stable location identity. The same scoped graph SHALL yield
the same positions regardless of query return order. When authored and topology-only locations are
mixed, authored positions SHALL remain fixed and fallback placement SHALL apply only to unpositioned
nodes. A disconnected location SHALL remain visible and labelled.

#### Scenario: A topology-only map is stable
- **WHEN** the same unmodified place graph is projected from differently ordered query responses
- **THEN** every location receives the same label and schematic position in both projections

#### Scenario: Partial geometry preserves authored intent
- **WHEN** some locations have authored coordinates and others have only topology
- **THEN** authored locations keep their exact positions and only the remaining locations use the
  deterministic fallback

#### Scenario: A disconnected location is not discarded
- **WHEN** a scoped location has neither coordinates nor a connective edge
- **THEN** the schematic still displays it with a deterministic position and label
