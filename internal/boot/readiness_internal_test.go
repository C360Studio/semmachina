package boot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/graph/readiness"
	"github.com/c360studio/semstreams/message"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// These are the two gates that decide whether this engine serves play from a
// whole world, and both of their failure modes are SILENT against a real broker:
// an import can still have missing authority entries after its publishes are
// acknowledged, and a mid-build index answers with a SHORTER list rather than
// an error. Neither state can be produced on demand — they are windows a broker
// passes through on its own schedule — so the gates are driven here against a
// graph that can be asked to sit in one.

// testWindow polls fast and gives up fast; nothing here waits on real work.
func testWindow() readinessWindow {
	return readinessWindow{
		timeout: 50 * time.Millisecond,
		poll:    time.Millisecond,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
}

// scriptedGraph answers each read from a script, so a test can stand the gate in
// front of a half-built world for exactly as long as it wants.
type scriptedGraph struct {
	batches   []graphio.BatchResult
	batchErr  error
	incoming  map[string][]graph.IncomingEntry
	incomeErr error

	batchCalls  atomic.Int64
	incomeCalls atomic.Int64
}

func (g *scriptedGraph) GetEntities(context.Context, []string) (graphio.BatchResult, error) {
	if g.batchErr != nil {
		return graphio.BatchResult{}, g.batchErr
	}
	idx := int(g.batchCalls.Add(1)) - 1
	if idx >= len(g.batches) {
		idx = len(g.batches) - 1
	}
	return g.batches[idx], nil
}

func (g *scriptedGraph) IncomingRelationships(_ context.Context, id string) ([]graph.IncomingEntry, error) {
	g.incomeCalls.Add(1)
	if g.incomeErr != nil {
		return nil, g.incomeErr
	}
	return g.incoming[id], nil
}

func born(ids ...string) graphio.BatchResult {
	out := graphio.BatchResult{}
	for _, id := range ids {
		out.Entities = append(out.Entities, graph.EntityState{
			ID: id,
			MessageType: message.Type{
				Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
			},
			Version: 1,
		})
	}
	return out
}

func TestAwaitEntitiesBorn_ReturnsOnceEveryEntityIsBorn(t *testing.T) {
	g := &scriptedGraph{batches: []graphio.BatchResult{
		{Missing: []graph.MissingEntity{{ID: "a", Reason: graph.MissingNotFound}}},
		born("a", "b"),
	}}
	if err := awaitEntitiesBorn(t.Context(), g, []string{"a", "b"}, testWindow()); err != nil {
		t.Fatalf("awaitEntitiesBorn: %v", err)
	}
	if g.batchCalls.Load() < 2 {
		t.Errorf("the gate read %d times; it answered before the world could have landed", g.batchCalls.Load())
	}
}

func TestAwaitEntitiesBorn_RefusesAnEntityThatNeverArrives(t *testing.T) {
	g := &scriptedGraph{batches: []graphio.BatchResult{
		{Missing: []graph.MissingEntity{{ID: "a", Reason: graph.MissingNotFound}}},
	}}
	err := awaitEntitiesBorn(t.Context(), g, []string{"a"}, testWindow())
	if err == nil {
		t.Fatal("the readiness gate accepted a world with a missing entity")
	}
	if !strings.Contains(err.Error(), "not queryable") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

func TestAwaitMembershipIndexed_ReturnsOnceEveryEdgeAppears(t *testing.T) {
	g := &scriptedGraph{incoming: map[string][]graph.IncomingEntry{
		"scene": {
			{FromEntityID: "rook", Predicate: vocabulary.WorldLocationCurrent.String()},
			{FromEntityID: "wren", Predicate: vocabulary.WorldLocationCurrent.String()},
		},
	}}
	expected := map[string][]string{"scene": {"rook", "wren"}}
	if err := awaitMembershipIndexed(t.Context(), g, expected, testWindow()); err != nil {
		t.Fatalf("awaitMembershipIndexed: %v", err)
	}
}

func TestMembershipEdges_KeyOccupancyByResolvedLocation(t *testing.T) {
	locationID := "c360.semmachina.world1.starter.location.gatehouse-place"
	sceneID := "c360.semmachina.world1.starter.scene.gatehouse"
	rookID := "c360.semmachina.world1.starter.character.rook"
	plan := &world.Plan{Entities: []world.PlannedEntity{
		{ID: sceneID, Kind: vocabulary.EntityKindScene, Facts: []payload.WorldFact{{
			Predicate: vocabulary.SceneLocationCurrent, Object: locationID, Reference: true,
		}}},
		{ID: rookID, Kind: vocabulary.EntityKindCharacter, Facts: []payload.WorldFact{{
			Predicate: vocabulary.WorldLocationCurrent, Object: locationID, Reference: true,
		}}},
	}}

	got := membershipEdges(plan)
	if len(got) != 1 || len(got[locationID]) != 1 || got[locationID][0] != rookID {
		t.Fatalf("membership edges = %v, want Rook keyed by the resolved location", got)
	}
	if _, sceneAsPlace := got[sceneID]; sceneAsPlace {
		t.Fatalf("scene placement was mistaken for occupancy: %v", got)
	}
}

// THE failure the whole gate exists for. A mid-build index returns a PARTIAL
// keyset, which reads as a smaller scene rather than as an error — so a gate that
// accepted any successful answer would pass here and hand a persona a room with
// somebody missing from it.
func TestAwaitMembershipIndexed_RefusesAShortAnswerRatherThanReadingItAsASmallerScene(t *testing.T) {
	g := &scriptedGraph{incoming: map[string][]graph.IncomingEntry{
		"scene": {{FromEntityID: "rook", Predicate: vocabulary.WorldLocationCurrent.String()}},
	}}
	expected := map[string][]string{"scene": {"rook", "wren"}}

	err := awaitMembershipIndexed(t.Context(), g, expected, testWindow())
	if err == nil {
		t.Fatal("the readiness gate accepted a SHORT membership answer; a partial keyset reads as a smaller " +
			"scene, and a persona handed part of a room narrates a room that is not there")
	}
	if !strings.Contains(err.Error(), "wren -> scene") {
		t.Errorf("the refusal does not name the missing edge: %v", err)
	}
	if strings.Contains(err.Error(), "rook -> scene") {
		t.Errorf("the refusal names an edge that WAS present: %v", err)
	}
}

// An index that says NOT READY is not an answer, and must never be read as one.
// The sentinel is inert in exactly the window that matters — the index latches
// ready when its target count is zero, so a fresh boot reports ready before the
// import writes anything — which is why the gate is a positive readback and this
// case is "not yet" rather than "fine".
func TestAwaitMembershipIndexed_TreatsNotReadyAsNotYetAndNeverAsAnAnswer(t *testing.T) {
	g := &scriptedGraph{incomeErr: fmt.Errorf("incoming edges of scene: %w", graphio.ErrIndexNotReady)}
	expected := map[string][]string{"scene": {"rook"}}

	err := awaitMembershipIndexed(t.Context(), g, expected, testWindow())
	if err == nil {
		t.Fatal("the readiness gate treated an index that is still building as a completed answer")
	}
	if !strings.Contains(err.Error(), "rook -> scene") {
		t.Errorf("a not-ready index must read as every edge still missing: %v", err)
	}
	if g.incomeCalls.Load() < 2 {
		t.Errorf("the gate asked the index %d time(s); not-ready is a reason to ask again", g.incomeCalls.Load())
	}
}

// Anything that is NOT the not-ready sentinel is a failure rather than a retry:
// polling forever over a broken query would turn a boot into a hang, and "the
// boot hung" is the worst diagnosis in the catalogue.
func TestAwaitMembershipIndexed_ReportsAnIndexErrorThatIsNotAWaitSignal(t *testing.T) {
	g := &scriptedGraph{incomeErr: errors.New("the index refused the request")}
	err := awaitMembershipIndexed(t.Context(), g, map[string][]string{"scene": {"rook"}}, testWindow())
	if err == nil {
		t.Fatal("a failing index read was swallowed")
	}
	if !strings.Contains(err.Error(), "the index refused the request") {
		t.Errorf("the failure does not carry the index's own reason: %v", err)
	}
	if g.incomeCalls.Load() != 1 {
		t.Errorf("the gate retried a failure that is not a wait signal %d times", g.incomeCalls.Load())
	}
}

// A gate with nothing to check is the one that reports green on the day the
// import produced nothing.
func TestAwaitMembershipIndexed_RefusesAWorldWithNoMembershipToGateOn(t *testing.T) {
	g := &scriptedGraph{}
	err := awaitMembershipIndexed(t.Context(), g, nil, testWindow())
	if err == nil {
		t.Fatal("the readiness gate passed with no membership edge to read; an empty gate is satisfied by an " +
			"empty index")
	}
	if g.incomeCalls.Load() != 0 {
		t.Error("the gate queried the index for a world that declares no membership")
	}
}

// A predicate that is not membership must not satisfy the gate. Without this,
// an index carrying only `world.relation.carries` edges would look like a room
// full of people.
func TestAwaitMembershipIndexed_IgnoresEdgesThatAreNotMembership(t *testing.T) {
	g := &scriptedGraph{incoming: map[string][]graph.IncomingEntry{
		"scene": {{FromEntityID: "rook", Predicate: vocabulary.WorldRelationCarries.String()}},
	}}
	err := awaitMembershipIndexed(t.Context(), g, map[string][]string{"scene": {"rook"}}, testWindow())
	if err == nil {
		t.Fatal("a non-membership edge satisfied the membership gate")
	}
}

func TestAwaitRuleReadiness_FailsClosedOnEveryIncompleteShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reading readiness.Reading
		want    string
	}{
		{name: "unknown", reading: readiness.Reading{}, want: "unknown"},
		{name: "watch error", reading: readiness.Reading{Known: true, Fresh: true, Err: errors.New("watch lost")}, want: "watch lost"},
		{name: "stale", reading: readiness.Reading{Known: true, Age: time.Minute}, want: "stale"},
		{name: "building", reading: readiness.Reading{Known: true, Fresh: true,
			Status: graph.IndexStatusResponse{State: graph.IndexStateBuilding}}, want: "building"},
		{name: "not ready", reading: readiness.Reading{Known: true, Fresh: true,
			Status: graph.IndexStatusResponse{State: graph.IndexStateReady, BootstrapComplete: true}}, want: "ready is false"},
		{name: "bootstrap incomplete", reading: readiness.Reading{Known: true, Fresh: true,
			Status: graph.IndexStatusResponse{State: graph.IndexStateReady, Ready: true}}, want: "bootstrap is incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := awaitRuleReadiness(t.Context(), 0, func() ruleReadinessGeneration {
				return ruleReadinessGeneration{revision: 1, reading: tc.reading}
			}, testWindow())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("readiness refusal = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAwaitRuleReadiness_WaitsForFreshReadyBootstrap(t *testing.T) {
	var calls atomic.Int64
	read := func() ruleReadinessGeneration {
		if calls.Add(1) == 1 {
			return ruleReadinessGeneration{revision: 1, reading: readiness.Reading{Known: true, Fresh: true,
				Status: graph.IndexStatusResponse{State: graph.IndexStateBuilding}}}
		}
		return ruleReadinessGeneration{revision: 2, reading: readiness.Reading{Known: true, Fresh: true, Status: graph.IndexStatusResponse{
			State: graph.IndexStateReady, Ready: true, BootstrapComplete: true,
		}}}
	}
	if err := awaitRuleReadiness(t.Context(), 0, read, testWindow()); err != nil {
		t.Fatalf("awaitRuleReadiness: %v", err)
	}
	if calls.Load() < 2 {
		t.Fatal("readiness gate did not observe the transition")
	}
}

func TestAwaitRuleReadiness_RejectsFreshReadyStatusFromPriorActivation(t *testing.T) {
	const baseline = 41
	var calls atomic.Int64
	read := func() ruleReadinessGeneration {
		if calls.Add(1) == 1 {
			return ruleReadinessGeneration{revision: baseline, reading: readiness.Reading{
				Known: true, Fresh: true, Status: graph.IndexStatusResponse{
					State: graph.IndexStateReady, Ready: true, BootstrapComplete: true,
				},
			}}
		}
		return ruleReadinessGeneration{revision: baseline + 1, reading: readiness.Reading{
			Known: true, Fresh: true, Status: graph.IndexStatusResponse{
				State: graph.IndexStateReady, Ready: true, BootstrapComplete: true,
			},
		}}
	}
	if err := awaitRuleReadiness(t.Context(), baseline, read, testWindow()); err != nil {
		t.Fatalf("awaitRuleReadiness: %v", err)
	}
	if calls.Load() < 2 {
		t.Fatal("fresh ready status from the prior activation satisfied the restart gate")
	}
}

// checkStreamCaptures is what stands between this engine and a stream somebody
// else created with narrower subjects. EnsureStream is get-or-create with no
// reconcile, so "whoever got there first wins" — and everything published onto
// an uncaptured subject is a core publish that reaches no durable consumer and
// is reported as a success.
func TestCheckStreamCaptures_RefusesAStreamThatDoesNotCoverTheSubject(t *testing.T) {
	narrow := &fakeStreams{subjects: []string{"agent.>"}}
	err := checkStreamCaptures(t.Context(), narrow, "AGENT", "agent.task.*", "tool.result.>")
	if err == nil {
		t.Fatal("the check accepted a stream that does not capture tool.result.>; the server ACCEPTS a consumer " +
			"filtered outside its stream's subjects, so nothing else would notice — the lane is simply never " +
			"delivered anything and the persona waiting on it burns its whole budget")
	}
	if !strings.Contains(err.Error(), "tool.result.>") {
		t.Errorf("the refusal does not name the uncaptured subject: %v", err)
	}

	wide := &fakeStreams{subjects: []string{"agent.>", "tool.result.>"}}
	if err := checkStreamCaptures(t.Context(), wide, "AGENT", "agent.task.*", "tool.result.>"); err != nil {
		t.Fatalf("the check refused a stream that captures both subjects: %v", err)
	}
}

func TestCheckStreamCaptures_RefusesAnAbsentStream(t *testing.T) {
	err := checkStreamCaptures(t.Context(), &fakeStreams{err: jetstream.ErrStreamNotFound}, "AGENT", "agent.>")
	if err == nil {
		t.Fatal("the check accepted a stream that does not exist")
	}
	if !strings.Contains(err.Error(), "CORE publish") {
		t.Errorf("the refusal does not say what a publish into no stream actually does: %v", err)
	}
}

// fakeStreams stands in for the broker's stream lookup, so a stream with the
// wrong subjects is a state a test can hold rather than a race it has to win.
type fakeStreams struct {
	subjects []string
	err      error
}

func (f *fakeStreams) GetStream(context.Context, string) (jetstream.Stream, error) {
	if f.err != nil {
		return nil, f.err
	}
	return fakeStream{subjects: f.subjects}, nil
}

type fakeStream struct {
	jetstream.Stream
	subjects []string
}

func (s fakeStream) Info(context.Context, ...jetstream.StreamInfoOpt) (*jetstream.StreamInfo, error) {
	return &jetstream.StreamInfo{Config: jetstream.StreamConfig{Subjects: s.subjects}}, nil
}
