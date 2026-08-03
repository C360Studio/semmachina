## Why

Stage 3 proved that one world package can support selectable experiences and a first-class place
ontology, but a solo creator still has no product surface for playing a scene, understanding its
resolution, or orienting themselves in place and time. This change makes that proof legible through
the existing player and graph contracts without moving game authority into a browser.

## What Changes

- Add a minimal SvelteKit session surface through a bounded same-origin WebSocket bridge to the
  existing authenticated `player/v1` endpoint: submit one action, show typed
  progress/error/reconnect state, and receive or retrieve its result.
- Render narration and a resolution card directly from the delivered plausibility, risk,
  consequence, outcome band, modifiers, and roll; the client performs no adjudication or inference.
- Add a pure server-side graph adapter behind an authenticated-principal boundary. It uses one fixed
  trusted endpoint, derives exact component-aware world scope from deployment/session state, fully
  validates upstream responses, and returns closed view models rather than arbitrary GraphQL data.
- Project locations, authored coordinates, and directed topology into a map. Authored geometry wins;
  worlds without geometry receive a deterministic labelled schematic layout that is never written
  back as world state.
- Project a configured typed campaign-clock fact, or the typed state `not_configured`, without
  inventing clock vocabulary, policies, or ticking behavior.
- Preserve the MVP boundary of one active world per broker/process and expose no world switcher.
- Keep the upstream Bearer credential server-only behind an opaque same-origin session; the bridge
  relays protocol bytes without taking sequencing, replay, or result-correlation authority.
- Authenticate the creator over HTTPS with a separately configured creator credential before
  minting or rotating that bounded session; the upstream player Bearer is never a browser login
  credential.
- Treat any missing upstream authentication or query primitive as a SemStreams engine ask to file,
  not a local query service or substrate workaround.
- Bound the beta.159 exhaustive map below its unpageable 1,000-entity prefix cap. Spatial search is
  not an exhaustive-map fallback and remains blocked pending scoped pagination support upstream.

## Non-goals

- A world/package authoring editor, human-GM editing, or mutation of world state from the map.
- New companion orchestration or changes to the established companion decision/voice contract.
- Campaign-clock vocabulary, clock-policy selection, deadline behavior, or any ticking scheduler.
- Multiple active worlds, multi-tenant campaign scoping, federation, or a world switcher.
- Client-side adjudication, turn sequencing, graph mutation, direct NATS access, or an authoritative
  client cache.
- A new query service, unrestricted GraphQL proxy, or local replacement for missing SemStreams
  authentication/query behavior.

## Capabilities

### New Capabilities

- `player-session-surface`: The authenticated action, progress, failure, reconnect, and result UX.
- `turn-resolution-projection`: Faithful narration and resolution-card rendering from delivered data.
- `world-graph-projection`: A scoped graph adapter and read-only authored/schematic map projection.
- `campaign-clock-projection`: A factual clock readout with an explicit not-configured state.

### Modified Capabilities

None. The existing `player/v1`, turn-result, graph, world, and companion contracts remain unchanged.

## Impact

This is game-repository presentation and adapter work: a new SvelteKit surface, custom Node server
with a bounded same-origin WebSocket bridge, strict TypeScript wire/view-model boundaries,
server-only SemStreams GraphQL access, deterministic projection logic, accessible components, and
mock/real-stack acceptance coverage. The bridge is a local SemMachina security responsibility, not
a SemStreams engine ask; it consumes the existing authenticated player WebSocket for action
submission, delivery, and retrieval without changing protocol bytes. Read projections use the
SemStreams `v1.0.0-beta.159` GraphQL `entity`, `entitiesByPrefix`, and `relationships` operations;
the exhaustive map does not call `spatialSearch`. No engine payload,
player protocol, graph predicate, rule pack, component, or lifecycle contract changes. The change
tracks upstream asks for authentication/scope, selection minimization, prefix pagination, spatial
scope/pagination, and relationship-schema consistency, tracked rather than hand-rolled here:
SemStreams
[#882](https://github.com/C360Studio/semstreams/issues/882),
[#883](https://github.com/C360Studio/semstreams/issues/883),
[#884](https://github.com/C360Studio/semstreams/issues/884),
[#885](https://github.com/C360Studio/semstreams/issues/885), and
[#886](https://github.com/C360Studio/semstreams/issues/886). A loopback/Unix-local endpoint,
deployment-enforced surface-only network policy, or authenticated proxy—plus the closed DTO,
sub-1,000 starter world, no-spatial path, and dual-form relationship normalization—forms the local
starter envelope. A bare RFC1918/private address is not authorization; public/scale and stable-schema
claims remain gated by the corresponding upstream and deployment controls.
