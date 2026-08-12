// Package testinfra is the shared real-NATS + real-graph-ingest harness.
//
// It exists so the tests that prove a seam against real infrastructure — the
// world importer, the instantiation gate, the dice component's roll triples —
// share ONE harness and ONE opt-out policy. The policy in particular must not
// be restated per package: its whole point is that a run without Docker is
// LOUD, and a second copy is a second chance to get the polarity backwards.
//
// It is imported only from _test files, so nothing here reaches a production
// binary.
package testinfra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/component"
	ssconfig "github.com/c360studio/semstreams/config"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	graphindex "github.com/c360studio/semstreams/processor/graph-index"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/c360studio/semstreams/service"
	"github.com/c360studio/semstreams/types"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"

	"github.com/c360studio/semmachina/internal/payload"
)

// SkipEnv opts OUT of the real-infrastructure tests.
//
// The polarity is deliberate and was chosen after watching the alternative
// fail. With an opt-IN flag, a run without Docker prints `ok` and nothing else:
// `go test` discards a passing package's output, so neither a stderr banner nor
// a t.Skip reason is visible unless someone passes -v. "The seam is covered"
// and "the seam was never exercised" would look identical, which is precisely
// the coverage illusion these tests exist to avoid.
//
// So the strict path is the DEFAULT: no Docker means these tests fail. CI needs
// no special configuration to require the real run. A developer who genuinely
// cannot run Docker sets this variable, which is an explicit, greppable
// statement that they turned the proof off — and even then the tests skip
// rather than pass.
const SkipEnv = "SEMMACHINA_SKIP_INTEGRATION"

// EntityStream is the JetStream stream graph-ingest's default input port
// consumes, and entityStreamSubject its subject filter. The name is derived by
// graph-ingest itself from the `entity.` subject prefix (deriveStreamName), so
// it is not ours to choose. Exported because a test that waits for the fact
// lane to drain has to name it.
const (
	EntityStream        = "ENTITY"
	entityStreamSubject = "entity.>"
)

// SubjectQueryEntity is graph-ingest's single-entity read subject.
const SubjectQueryEntity = "graph.ingest.query.entity"

// Harness is one NATS container with a running graph-ingest component.
//
// It is shared across a package's integration tests rather than rebuilt per
// test: container startup dominates the runtime, and isolation comes from
// per-test entity IDs — disjoint world namespaces — instead.
type Harness struct {
	// Client is the connected NATS client.
	Client *natsclient.Client
	// Registry is the payload registry graph-ingest decodes with, populated by
	// the same RegisterPayloads a binary bootstrap calls.
	Registry *payloadregistry.Registry

	graphClient *natsclient.Client
	ingest      *service.ComponentManager
	indexOnce   sync.Once
	index       *service.ComponentManager
	indexErr    error

	stop func()
}

// EnsureArchivalStream provisions a stream whose contract is permanence through
// beta.160's declarative seam and returns the live stream. Programmatic
// EnsureStream intentionally cannot classify an archive and therefore refuses
// to create one without finite bounds.
func (h *Harness) EnsureArchivalStream(
	t *testing.T,
	name string,
	subjects []string,
	duplicates string,
) jetstream.Stream {
	t.Helper()
	cfg := &ssconfig.Config{
		Version:  "1.0.0",
		Platform: ssconfig.PlatformConfig{Org: "c360", ID: "semmachina-tests"},
		Streams: ssconfig.StreamConfigs{
			name: {
				Subjects: subjects, Storage: "file", Retention: "limits", Duplicates: duplicates,
			},
		},
		ArchivalStreams: ssconfig.ArchivalStreams{
			name: {Owner: "semmachina", Reason: "the production stream contract requires permanent test evidence"},
		},
	}
	if err := ssconfig.NewStreamsManager(h.Client, slog.Default()).EnsureStreams(t.Context(), cfg); err != nil {
		t.Fatalf("provision archival stream %s: %v", name, err)
	}
	stream, err := h.Client.GetStream(t.Context(), name)
	if err != nil {
		t.Fatalf("read archival stream %s: %v", name, err)
	}
	return stream
}

var (
	shared    *Harness
	sharedErr error
)

// RunTests starts the shared harness, runs the package's tests, and tears the
// container down. Call it from TestMain: os.Exit(testinfra.RunTests(m)).
func RunTests(m *testing.M) int {
	shared, sharedErr = start()
	if sharedErr != nil && Skipped() {
		fmt.Fprintf(os.Stderr,
			"\n"+
				"================================================================\n"+
				" REAL-INFRASTRUCTURE TESTS SKIPPED BY %s\n"+
				" reason: %v\n"+
				" The seams they cover are NOT exercised by this run.\n"+
				"================================================================\n\n",
			SkipEnv, sharedErr)
	}
	if shared != nil {
		defer shared.stop()
	}
	return m.Run()
}

// Skipped reports whether the run explicitly opted out.
func Skipped() bool { return os.Getenv(SkipEnv) != "" }

// Require returns the shared harness, FAILING when real infrastructure is
// unavailable unless the run explicitly opted out.
func Require(t *testing.T) *Harness {
	t.Helper()
	if shared != nil {
		return shared
	}
	if Skipped() {
		t.Skipf("SKIPPED by %s — this test proved nothing in this run", SkipEnv)
		return nil
	}
	t.Fatalf("real NATS + graph-ingest are required and unavailable: %v\n"+
		"This test exercises the production path; there is no substitute for it.\n"+
		"Start Docker, or set %s=1 to run the rest of the suite without this proof.",
		sharedErr, SkipEnv)
	return nil
}

func start() (*Harness, error) {
	if Skipped() {
		return nil, fmt.Errorf("%s is set", SkipEnv)
	}
	ctx := context.Background()

	// Probe Docker before testcontainers touches it, so "no Docker" is a clear
	// sentence rather than a wall of container-runtime output.
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return nil, fmt.Errorf("docker provider unavailable: %w", err)
	}
	if err := provider.Health(ctx); err != nil {
		return nil, fmt.Errorf("docker daemon is not reachable: %w", err)
	}

	client, err := natsclient.NewSharedTestClient(
		natsclient.WithJetStream(),
		natsclient.WithNATSVersion("2.14.4"),
		natsclient.WithStreams(natsclient.TestStreamConfig{
			Name:     EntityStream,
			Subjects: []string{entityStreamSubject},
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start NATS test container: %w", err)
	}

	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		client.Terminate() //nolint:errcheck // best effort on the error path
		return nil, fmt.Errorf("register framework payloads: %w", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		client.Terminate() //nolint:errcheck
		return nil, fmt.Errorf("register semmachina payloads: %w", err)
	}

	// Graph components own a dedicated connection. Package tests deliberately
	// stop their own consumers between worlds; sharing the client would also
	// erase graph-ingest's beta.160 consumer binding while leaving its component
	// marked running and permanently unready.
	graphClient, err := natsclient.NewClient(client.URL)
	if err != nil {
		client.Terminate() //nolint:errcheck
		return nil, fmt.Errorf("build graph substrate NATS client: %w", err)
	}
	if err := graphClient.Connect(ctx); err != nil {
		client.Terminate() //nolint:errcheck
		return nil, fmt.Errorf("connect graph substrate NATS client: %w", err)
	}

	ingest, err := startGraphIngest(ctx, graphClient, registry)
	if err != nil {
		_ = graphClient.Close(context.Background())
		client.Terminate() //nolint:errcheck
		return nil, err
	}

	harness := &Harness{
		Client: client.Client, Registry: registry,
		graphClient: graphClient, ingest: ingest,
	}
	harness.stop = func() {
		if harness.index != nil {
			_ = harness.index.Stop(5 * time.Second)
		}
		_ = harness.ingest.Stop(5 * time.Second)
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = harness.graphClient.Close(closeCtx)
		cancel()
		_ = client.Terminate()
	}
	return harness, nil
}

// RequireIndex starts the real graph-index component and returns once it is
// running.
//
// It is LAZY rather than part of the shared startup because most packages never
// ask a reverse-edge question, and a component that watches ENTITY_STATES and
// maintains six KV buckets is not free to run beside tests that do not need it.
// The packages that do need it — anything answering "who is in this scene",
// which the graph cannot answer from the scene's own triples — call this.
//
// It is the REAL component through its production factory and lifecycle, for the
// same reason graph-ingest is: the index is eventually consistent off a KV
// watch, and a fake that answered instantly would hide the one property a caller
// has to design around.
func (h *Harness) RequireIndex(t *testing.T) {
	t.Helper()
	h.indexOnce.Do(func() {
		h.index, h.indexErr = startGraphIndex(context.Background(), h.graphClient, h.Registry)
	})
	if h.indexErr != nil {
		t.Fatalf("real graph-index is required and did not start: %v", h.indexErr)
	}
}

// startGraphIndex boots the real graph-index component against the real broker.
func startGraphIndex(
	ctx context.Context,
	client *natsclient.Client,
	registry *payloadregistry.Registry,
) (*service.ComponentManager, error) {
	return startGraphManager(ctx, client, registry, "graph-index", graphindex.DefaultConfig(), graphindex.Register)
}

// startGraphIngest boots the real graph-ingest component against the real
// broker, through its production factory and lifecycle.
//
// Nothing here is a substitute for graph-ingest: it is graph-ingest, with its
// own default port configuration, creating its own ENTITY_STATES bucket and
// binding its own consumer and mutation handlers. That is what makes "every
// write traveled through graph-ingest" an observation rather than an assertion.
func startGraphIngest(
	ctx context.Context,
	client *natsclient.Client,
	registry *payloadregistry.Registry,
) (*service.ComponentManager, error) {
	return startGraphManager(ctx, client, registry, "graph-ingest", graphingest.DefaultConfig(), graphingest.Register)
}

func startGraphManager(
	ctx context.Context,
	client *natsclient.Client,
	registry *payloadregistry.Registry,
	name string,
	defaults any,
	register func(*component.Registry) error,
) (*service.ComponentManager, error) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	raw, err := json.Marshal(defaults)
	if err != nil {
		return nil, fmt.Errorf("encode %s default configuration: %w", name, err)
	}
	componentRegistry := component.NewRegistry(component.WithLogger(logger))
	if err := register(componentRegistry); err != nil {
		return nil, fmt.Errorf("register %s: %w", name, err)
	}
	managerConfig, err := ssconfig.NewConfigManager(&ssconfig.Config{
		Version: "1.1.0",
		Platform: ssconfig.PlatformConfig{
			Org: "c360", ID: "semmachina-testinfra-" + name, Type: "application", Environment: "test",
		},
		Components: ssconfig.ComponentConfigs{
			name: {Type: types.ComponentTypeProcessor, Name: name, Enabled: true, Config: raw},
		},
	}, client, logger)
	if err != nil {
		return nil, fmt.Errorf("build %s config manager: %w", name, err)
	}
	constructed, err := service.NewComponentManager(json.RawMessage(`{"watch_config":false}`), &service.Dependencies{
		NATSClient: client, Logger: logger,
		Platform: types.PlatformMeta{Org: "c360", Platform: "semmachina-testinfra-" + name},
		Manager:  managerConfig, ComponentRegistry: componentRegistry, PayloadRegistry: registry,
	})
	if err != nil {
		return nil, fmt.Errorf("build %s component manager: %w", name, err)
	}
	manager, ok := constructed.(*service.ComponentManager)
	if !ok {
		return nil, fmt.Errorf("build %s component manager: constructor returned %T", name, constructed)
	}
	managed := manager.GetManagedComponents()[name]
	if managed == nil || managed.State != component.StateInitialized {
		return nil, fmt.Errorf("build %s component manager: component was not initialized", name)
	}
	if err := manager.Start(ctx); err != nil {
		_ = manager.Stop(5 * time.Second)
		return nil, fmt.Errorf("start %s component manager: %w", name, err)
	}
	managed = manager.GetManagedComponents()[name]
	if managed == nil || managed.State != component.StateStarted {
		_ = manager.Stop(5 * time.Second)
		return nil, fmt.Errorf("start %s component manager: component did not reach started", name)
	}
	return manager, nil
}

// QueryEntity reads one entity through graph-ingest's NATS query surface.
//
// The read goes over the wire to the component rather than into the KV bucket
// so the test observes what a consumer observes. It is a READ, so it does not
// weaken the sole-writer claim.
func (h *Harness) QueryEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	request, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		return nil, err
	}
	response, err := h.Client.RequestClassified(ctx, SubjectQueryEntity, request, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var exact graph.ExactEntity
	if err := json.Unmarshal(response, &exact); err != nil {
		return nil, fmt.Errorf("decode exact entity state: %w", err)
	}
	if exact.Entity == nil || exact.KVRevision == 0 {
		return nil, errors.New("exact entity response has no entity or revision")
	}
	return exact.Entity.Clone(), nil
}

// AwaitEntity polls until the entity has an exact authoritative value. Missing
// relationship targets remain absent in beta.160; no stub filtering is needed.
func (h *Harness) AwaitEntity(t *testing.T, id string) *graph.EntityState {
	t.Helper()
	ctx := t.Context()

	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		state, err := h.QueryEntity(ctx, id)
		switch {
		case err != nil:
			lastErr = err
		default:
			return state
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for entity %s: %v", id, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("entity %s never materialized in ENTITY_STATES: %v", id, lastErr)
	return nil
}

// ObjectsFor returns every object recorded for a predicate on an entity.
//
// A slice rather than a single value on purpose: "how many values does this
// predicate hold?" is the question that distinguishes a reconcile-group write from
// an append-lane one, and a helper that returned only the first would answer it
// wrong every time.
func ObjectsFor(state *graph.EntityState, predicate string) []any {
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate {
			out = append(out, triple.Object)
		}
	}
	return out
}

// FirstObject returns the single object for a predicate, or nil.
func FirstObject(state *graph.EntityState, predicate string) any {
	objects := ObjectsFor(state, predicate)
	if len(objects) == 0 {
		return nil
	}
	return objects[0]
}

// TripleKey identifies one recorded fact for set comparison.
type TripleKey struct {
	Predicate string
	Object    string
}

// TripleSet reduces an entity to the facts it asserts, dropping the metadata
// that legitimately changes between writes (timestamps, KV revision).
func TripleSet(state *graph.EntityState) map[TripleKey]int {
	out := make(map[TripleKey]int, len(state.Triples))
	for _, triple := range state.Triples {
		out[TripleKey{Predicate: triple.Predicate, Object: fmt.Sprint(triple.Object)}]++
	}
	return out
}
