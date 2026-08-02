package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// These are the two gates that decide whether this engine serves play from a
// whole world, and both of their failure modes are SILENT against a real broker:
// a referential stub is queryable and factless, and a mid-build index answers
// with a SHORTER list rather than an error. Neither state can be produced on
// demand — they are windows a broker passes through on its own schedule — so the
// gates are driven here against a graph that can be asked to sit in one.

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

// stubbed returns a batch whose entity carries the referential-stub envelope:
// queryable, and none of its own facts.
func stubbed(id string) graphio.BatchResult {
	return graphio.BatchResult{
		Entities: []graph.EntityState{{ID: id, MessageType: graph.StubMessageType}},
	}
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

// The half that gets forgotten. graph-ingest materialises a referenced entity as
// a STUB the moment the REFERENCING entity lands, so an existence poll alone
// succeeds against a half-world — and the context assembler then reads a scene
// that is quietly smaller rather than an error.
func TestAwaitEntitiesBorn_RefusesAnEntityThatIsStillAReferentialStub(t *testing.T) {
	g := &scriptedGraph{batches: []graphio.BatchResult{stubbed("a")}}

	err := awaitEntitiesBorn(t.Context(), g, []string{"a"}, testWindow())
	if err == nil {
		t.Fatal("the readiness gate accepted a referential stub as a loaded entity; \"the id resolves\" is not " +
			"\"the entity is loaded\", and the difference is a world the assembler reads as smaller")
	}
	if !strings.Contains(err.Error(), "referential stub") {
		t.Errorf("the refusal does not name the stub: %v", err)
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
	if !strings.Contains(err.Error(), "unborn") {
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

// The rule-processor started check, driven against every shape its reader can
// return.
//
// The important case is the STALE one. The status key survives a restart, so a
// check that merely found a status passes on every boot after the first — for a
// processor this process never started, which is the state that would let the
// stranded-turn pass end live turns. The engine closes it by DELETING the key
// before starting the processor, so what this exercises is the predicate that
// deletion makes meaningful: a key that is absent stays a refusal for as long as
// it is absent, and no timestamp is consulted at all.
func TestAwaitReportedStatus_RefusesUntilTheStatusReappears(t *testing.T) {
	status := func(stage string, at time.Time) []byte {
		data, err := json.Marshal(component.Status{
			Component: "rule-processor", Stage: stage, StageStartedAt: at,
		})
		if err != nil {
			t.Fatalf("encode status: %v", err)
		}
		return data
	}

	for _, tc := range []struct {
		name  string
		read  func(context.Context) ([]byte, error)
		wants string
	}{
		{
			name:  "the key never reappears",
			read:  func(context.Context) ([]byte, error) { return nil, errors.New("nats: key not found") },
			wants: "key not found",
		},
		{
			name:  "a status carrying no stage",
			read:  func(context.Context) ([]byte, error) { return status("", time.Now()), nil },
			wants: "no stage at all",
		},
		{
			name:  "an undecodable status",
			read:  func(context.Context) ([]byte, error) { return []byte("{"), nil },
			wants: "undecodable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := awaitReportedStatus(t.Context(), tc.read, testWindow())
			if err == nil {
				t.Fatal("the check accepted a status the processor had not written; the stranded-turn pass would " +
					"then run against a processor nobody confirmed started")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("the refusal does not say %q: %v", tc.wants, err)
			}
		})
	}

	// The positive controls, without which every case above is satisfied by a
	// check that always fails. BOTH stages the processor can be in when this
	// reads are accepted: `idle` is what Start writes last, and `evaluating` is
	// what its already-live entity watcher can overwrite it with — the processor
	// has no path back to idle, so pinning the value would hang the boot.
	for _, stage := range []string{"idle", "evaluating"} {
		read := func(context.Context) ([]byte, error) { return status(stage, time.Now()), nil }
		if err := awaitReportedStatus(t.Context(), read, testWindow()); err != nil {
			t.Fatalf("the check refused a processor reporting %q: %v", stage, err)
		}
	}
}

// A clock that stepped BACKWARDS must not turn a refusal into a pass.
//
// This is the hole the delete-then-await shape closes. The reported stamp
// round-trips through JSON without its monotonic reading, so any comparison
// against it is wall-clock on both sides — and a status written an hour in the
// PAST is exactly what an NTP correction, a VM snapshot restore or a container
// host resync leaves behind. Existence after deletion cannot be forged that way,
// and the assertion here is that the stamp is not consulted at all.
func TestAwaitReportedStatus_DoesNotConsultTheReportedClock(t *testing.T) {
	ancient, err := json.Marshal(component.Status{
		Component:      "rule-processor",
		Stage:          "idle",
		StageStartedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("encode status: %v", err)
	}
	read := func(context.Context) ([]byte, error) { return ancient, nil }

	if err := awaitReportedStatus(t.Context(), read, testWindow()); err != nil {
		t.Fatalf("the check refused a status stamped in the past: %v. The stamp is not the predicate — the key's "+
			"reappearance after this boot deleted it is, precisely so a backwards clock step cannot decide it", err)
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
