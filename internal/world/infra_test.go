package world_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/testcontainers/testcontainers-go"

	"github.com/c360studio/semmachina/internal/payload"
)

// SkipIntegrationEnv opts OUT of the real-infrastructure tests.
//
// The polarity is deliberate and was chosen after watching the alternative
// fail. With an opt-IN flag, a run without Docker prints `ok` and nothing else:
// `go test` discards a passing package's output, so neither a stderr banner nor
// a t.Skip reason is visible unless someone passes -v. "The importer is
// covered" and "the importer was never exercised" would look identical, which
// is precisely the coverage illusion this test exists to avoid.
//
// So the strict path is the DEFAULT: no Docker means these tests fail. CI needs
// no special configuration to require the real run. A developer who genuinely
// cannot run Docker sets this variable, which is an explicit, greppable
// statement that they turned the proof off — and even then the tests skip
// rather than pass.
const SkipIntegrationEnv = "SEMMACHINA_SKIP_INTEGRATION"

// entityStream is the JetStream stream graph-ingest's default input port
// consumes. The name is derived by graph-ingest itself from the `entity.`
// subject prefix (deriveStreamName), so it is not ours to choose.
const (
	entityStream        = "ENTITY"
	entityStreamSubject = "entity.>"
)

// infra is the shared real-NATS + real-graph-ingest harness, or nil when
// Docker is unavailable.
var (
	infra    *testInfra
	infraErr error
)

// testInfra is one NATS container with a running graph-ingest component.
//
// It is shared across the package's integration tests rather than rebuilt per
// test: container startup dominates the runtime, and isolation comes from
// per-test WORLD NAMESPACES instead. That is not a compromise — disjoint
// namespaces are the very property the two-worlds requirement asserts, so the
// tests get their isolation from the thing under test.
type testInfra struct {
	client *natsclient.Client
	stop   func()
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests wraps m.Run so container teardown runs; os.Exit skips defers.
func runTests(m *testing.M) int {
	infra, infraErr = startInfra()
	if infraErr != nil && integrationSkipped() {
		fmt.Fprintf(os.Stderr,
			"\n"+
				"================================================================\n"+
				" REAL-INFRASTRUCTURE TESTS SKIPPED BY %s\n"+
				" reason: %v\n"+
				" The world importer is NOT covered by this run.\n"+
				"================================================================\n\n",
			SkipIntegrationEnv, infraErr)
	}
	if infra != nil {
		defer infra.stop()
	}
	return m.Run()
}

func integrationSkipped() bool { return os.Getenv(SkipIntegrationEnv) != "" }

func startInfra() (*testInfra, error) {
	if integrationSkipped() {
		return nil, fmt.Errorf("%s is set", SkipIntegrationEnv)
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
		natsclient.WithStreams(natsclient.TestStreamConfig{
			Name:     entityStream,
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

	ingest, err := startGraphIngest(ctx, client.Client, registry)
	if err != nil {
		client.Terminate() //nolint:errcheck
		return nil, err
	}

	return &testInfra{
		client: client.Client,
		stop: func() {
			_ = ingest.Stop(5 * time.Second)
			_ = client.Terminate()
		},
	}, nil
}

// startGraphIngest boots the real graph-ingest component against the real
// broker, through its production factory and lifecycle.
//
// Nothing here is a substitute for graph-ingest: it is graph-ingest, with its
// own default port configuration, creating its own ENTITY_STATES bucket and
// binding its own consumer. That is what makes "every write traveled through
// graph-ingest" an observation rather than an assertion.
func startGraphIngest(
	ctx context.Context,
	client *natsclient.Client,
	registry *payloadregistry.Registry,
) (component.LifecycleComponent, error) {
	deps := component.Dependencies{
		NATSClient:      client,
		PayloadRegistry: registry,
		// Warn level: graph-ingest is chatty at info, and a test that drowns
		// its own failure message in setup logs is a test nobody reads.
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	created, err := graphingest.CreateGraphIngest(nil, deps)
	if err != nil {
		return nil, fmt.Errorf("create graph-ingest: %w", err)
	}
	ingest, ok := created.(component.LifecycleComponent)
	if !ok {
		return nil, fmt.Errorf("graph-ingest is a %T, not a LifecycleComponent", created)
	}
	if err := ingest.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize graph-ingest: %w", err)
	}
	if err := ingest.Start(ctx); err != nil {
		return nil, fmt.Errorf("start graph-ingest: %w", err)
	}
	return ingest, nil
}

// requireInfra returns the shared harness, FAILING when real infrastructure is
// unavailable unless the run explicitly opted out.
func requireInfra(t *testing.T) *testInfra {
	t.Helper()
	if infra != nil {
		return infra
	}
	if integrationSkipped() {
		t.Skipf("SKIPPED by %s — this test proved nothing in this run", SkipIntegrationEnv)
		return nil
	}
	t.Fatalf("real NATS + graph-ingest are required and unavailable: %v\n"+
		"This test materializes a world through the production path; there is no substitute for it.\n"+
		"Start Docker, or set %s=1 to run the rest of the suite without this proof.",
		infraErr, SkipIntegrationEnv)
	return nil
}

// queryEntity reads one entity through graph-ingest's NATS query surface.
//
// The read goes over the wire to the component rather than into the KV bucket
// so the test observes what a consumer observes. It is a READ, so it does not
// weaken the sole-writer claim.
func (i *testInfra) queryEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	request, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		return nil, err
	}
	response, err := i.client.RequestClassified(ctx, "graph.ingest.query.entity", request, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var state graph.EntityState
	if err := json.Unmarshal(response, &state); err != nil {
		return nil, fmt.Errorf("decode entity state: %w", err)
	}
	return &state, nil
}

// awaitEntity polls until the entity is queryable AND is no longer a bare
// referential stub.
//
// Both conditions are load-bearing, and the second was found the hard way: an
// existence poll alone succeeds against a referential stub carrying none of the
// entity's own facts. The contract is stated where the next caller will read
// it — Import's doc comment — because anything that treats "the ID resolves" as
// "the world is loaded" is exposed to the same half-entity, including a future
// boot-readiness check that will never open this file.
func (i *testInfra) awaitEntity(t *testing.T, id string) *graph.EntityState {
	t.Helper()
	ctx := t.Context()

	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		state, err := i.queryEntity(ctx, id)
		switch {
		case err != nil:
			lastErr = err
		case state.IsStub():
			lastErr = fmt.Errorf("entity %s is still a referential stub (envelope %v)", id, state.MessageType)
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

// objectsFor returns every object recorded for a predicate on an entity.
func objectsFor(state *graph.EntityState, predicate string) []any {
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate {
			out = append(out, triple.Object)
		}
	}
	return out
}

// firstObject returns the single object for a predicate, or nil.
func firstObject(state *graph.EntityState, predicate string) any {
	objects := objectsFor(state, predicate)
	if len(objects) == 0 {
		return nil
	}
	return objects[0]
}

// tripleKey identifies one recorded fact for set comparison.
type tripleKey struct {
	Predicate string
	Object    string
}

// tripleSet reduces an entity to the facts it asserts, dropping the metadata
// that legitimately changes between writes (timestamps, KV revision).
func tripleSet(state *graph.EntityState) map[tripleKey]int {
	out := make(map[tripleKey]int, len(state.Triples))
	for _, triple := range state.Triples {
		out[tripleKey{Predicate: triple.Predicate, Object: fmt.Sprint(triple.Object)}]++
	}
	return out
}
