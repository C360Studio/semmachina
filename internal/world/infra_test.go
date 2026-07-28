package world_test

import (
	"os"
	"testing"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/testinfra"
)

// The real-infrastructure harness — NATS container, real graph-ingest started
// through its production lifecycle, and the loud opt-out policy — lives in
// internal/testinfra, shared with the instantiation-gate and dice tests. The
// aliases below keep this package's test bodies reading in their own terms.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

type testInfra = testinfra.Harness

// entityStream is the fact lane the importer publishes on; a drain check names it.
const entityStream = testinfra.EntityStream

func requireInfra(t *testing.T) *testInfra {
	t.Helper()
	return testinfra.Require(t)
}

func objectsFor(state *graph.EntityState, predicate string) []any {
	return testinfra.ObjectsFor(state, predicate)
}

func firstObject(state *graph.EntityState, predicate string) any {
	return testinfra.FirstObject(state, predicate)
}

type tripleKey = testinfra.TripleKey

func tripleSet(state *graph.EntityState) map[tripleKey]int {
	return testinfra.TripleSet(state)
}
