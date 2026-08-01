package content_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/storage/objectstore"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Durability is the entire product of this package, and a map cannot lose data
// in any of the ways it exists to survive. Both claims below are claims about
// NATS ObjectStore — that a derived key makes a re-put land on the same object
// rather than beside it, and that bytes written by a process that then died are
// readable by one that never saw them — so both are made against a real one.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

// Each test gets its own bucket, so tests sharing one broker cannot see each
// other's objects and a stale object cannot make a later run pass.
var integrationCounter atomic.Int64

func integrationCompanionDecision(t *testing.T, kind payload.CompanionDecisionKind) *payload.CompanionDecision {
	t.Helper()
	decision := &payload.CompanionDecision{
		TurnID: "turn-exact", ContextRef: compose(t, "scene", "gatehouse"),
		PlayerID: compose(t, "player", "p1"), CompanionID: compose(t, "character", "wren"),
		Kind: kind, EvidenceRefs: []string{},
	}
	decision.DecisionID = payload.CompanionDecisionID(
		decision.TurnID, decision.ContextRef, decision.PlayerID, decision.CompanionID)
	return decision
}

func liveStore(t *testing.T, bucket string) (*content.Store, *content.ObjectBackend) {
	t.Helper()
	harness := testinfra.Require(t)

	backend, err := content.NewObjectStore(t.Context(), harness.Client, content.WithBucket(bucket))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown

	store, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, backend
}

func testBucket(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("CONTENT_T%d", integrationCounter.Add(1))
}

func TestIntegration_CompanionDecisionClaimLinearizesIndependentHandles(t *testing.T) {
	bucket := testBucket(t)
	firstStore, _ := liveStore(t, bucket)
	secondStore, _ := liveStore(t, bucket)
	first := integrationCompanionDecision(t, payload.CompanionDecisionSilent)
	second := integrationCompanionDecision(t, payload.CompanionDecisionQuip)
	turnEntityID := compose(t, "turn", first.TurnID)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for _, contender := range []struct {
		store    *content.Store
		decision *payload.CompanionDecision
	}{{firstStore, first}, {secondStore, second}} {
		workers.Add(1)
		go func(store *content.Store, decision *payload.CompanionDecision) {
			defer workers.Done()
			<-start
			_, err := store.PutCompanionDecision(t.Context(), turnEntityID, decision)
			errs <- err
		}(contender.store, contender.decision)
	}
	close(start)
	workers.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, content.ErrArtifactConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent exact-resident write: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want one linearized winner", successes, conflicts)
	}
	fresh, _ := liveStore(t, bucket)
	key, _ := content.KeyFor(vocabulary.TurnCompanionDecisionRef, content.SubjectTurn, first.TurnID)
	got, err := fresh.GetCompanionDecision(t.Context(), content.Ref{Instance: bucket, Key: key})
	if err != nil || (got.Kind != first.Kind && got.Kind != second.Kind) {
		t.Fatalf("fresh handle winner=%+v err=%v", got, err)
	}
}

func TestIntegration_CompanionDecisionEqualConcurrentHandlesBothSucceed(t *testing.T) {
	bucket := testBucket(t)
	firstStore, _ := liveStore(t, bucket)
	secondStore, _ := liveStore(t, bucket)
	decision := integrationCompanionDecision(t, payload.CompanionDecisionSilent)
	turnEntityID := compose(t, "turn", decision.TurnID)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, store := range []*content.Store{firstStore, secondStore} {
		go func(store *content.Store) {
			<-start
			_, err := store.PutCompanionDecision(t.Context(), turnEntityID, decision)
			errs <- err
		}(store)
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("equal concurrent retry: %v", err)
		}
	}
}

func TestIntegration_CompanionDecisionClaimRepairsMissingObjectBeforeConflict(t *testing.T) {
	bucket := testBucket(t)
	store, _ := liveStore(t, bucket)
	winner := integrationCompanionDecision(t, payload.CompanionDecisionSilent)
	loser := integrationCompanionDecision(t, payload.CompanionDecisionQuip)
	turnEntityID := compose(t, "turn", winner.TurnID)
	ref, err := store.PutCompanionDecision(t.Context(), turnEntityID, winner)
	if err != nil {
		t.Fatal(err)
	}
	harness := testinfra.Require(t)
	js, _ := harness.Client.JetStream()
	objects, _ := js.ObjectStore(t.Context(), bucket)
	if err := objects.Delete(t.Context(), ref.Key); err != nil {
		t.Fatal(err)
	}
	fresh, _ := liveStore(t, bucket)
	if _, err := fresh.PutCompanionDecision(t.Context(), turnEntityID, loser); !errors.Is(err, content.ErrArtifactConflict) {
		t.Fatalf("differing retry after missing object = %v", err)
	}
	got, err := fresh.GetCompanionDecision(t.Context(), ref)
	if err != nil || got.Kind != winner.Kind {
		t.Fatalf("winning claim was not repaired before conflict: got=%+v err=%v", got, err)
	}
}

func TestIntegration_CompanionDecisionClaimObjectMismatchFailsClosed(t *testing.T) {
	bucket := testBucket(t)
	store, _ := liveStore(t, bucket)
	winner := integrationCompanionDecision(t, payload.CompanionDecisionSilent)
	corrupt := integrationCompanionDecision(t, payload.CompanionDecisionQuip)
	turnEntityID := compose(t, "turn", winner.TurnID)
	ref, err := store.PutCompanionDecision(t.Context(), turnEntityID, winner)
	if err != nil {
		t.Fatal(err)
	}
	harness := testinfra.Require(t)
	js, _ := harness.Client.JetStream()
	objects, _ := js.ObjectStore(t.Context(), bucket)
	corruptBytes, _ := json.Marshal(corrupt)
	if _, err := objects.PutBytes(t.Context(), ref.Key, corruptBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCompanionDecision(t.Context(), turnEntityID, winner); !errors.Is(err, content.ErrArtifactCorrupt) {
		t.Fatalf("claim/object mismatch error = %v", err)
	}
	resident, _ := objects.GetBytes(t.Context(), ref.Key)
	if !bytes.Equal(resident, corruptBytes) {
		t.Fatal("fail-closed mismatch path overwrote the resident object")
	}
}

func TestIntegration_IncompatibleExactClaimBucketBlocksContentStartup(t *testing.T) {
	for name, mutate := range map[string]func(*jetstream.KeyValueConfig){
		"ttl":        func(cfg *jetstream.KeyValueConfig) { cfg.TTL = time.Minute },
		"durability": func(cfg *jetstream.KeyValueConfig) { cfg.Storage = jetstream.MemoryStorage },
	} {
		t.Run(name, func(t *testing.T) {
			bucket := testBucket(t)
			harness := testinfra.Require(t)
			backend, err := objectstore.NewStoreWithConfig(t.Context(), harness.Client,
				objectstore.Config{BucketName: bucket, InstanceName: bucket})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			js, _ := harness.Client.JetStream()
			cfg := jetstream.KeyValueConfig{Bucket: bucket + "_IMMUTABLE", History: 1,
				MaxValueSize: content.MaxCompanionDecisionBytes, Storage: jetstream.FileStorage, Replicas: 1}
			mutate(&cfg)
			if _, err := js.CreateKeyValue(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := content.NewObjectStore(t.Context(), harness.Client,
				content.WithBucket(bucket)); err == nil {
				t.Fatal("content startup adopted an incompatible exact-resident claim bucket")
			}
		})
	}
}

// The failure this task closes: the process dies after the action is stored and
// before the turn entity exists. The action has to be there when a process that
// remembers nothing comes back and follows the reference — otherwise "the turn
// durably exists" is true of the paperwork and false of the player's words.
//
// The second store is a genuinely fresh handle with a cold cache, which is why
// the engine's store runs with caching off: a cached read would have proved
// only that this process remembers what it just wrote.
func TestIntegration_AStoredActionIsReadableByAProcessThatNeverWroteIt(t *testing.T) {
	bucket := testBucket(t)
	writer, _ := liveStore(t, bucket)

	action := testAction(t, "act-crash")
	turnEntityID := compose(t, "turn", payload.TurnIDForAction(action.ActionID))

	ref, err := writer.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("PutAction: %v", err)
	}

	// The crash. Everything in memory is gone; only the reference survives, and
	// in production it survives on the turn entity.
	reader, _ := liveStore(t, bucket)
	got, err := reader.GetAction(t.Context(), ref)
	if err != nil {
		t.Fatalf("resolving %s after a restart: %v", ref, err)
	}

	if got.Text != action.Text {
		t.Fatalf("the recovered action reads %q, want %q; the adjudicator would be re-prompted with the "+
			"wrong words", got.Text, action.Text)
	}
	if got.Channel.ReplyTo != action.Channel.ReplyTo {
		t.Fatalf("the recovered action's reply address is %q, want %q", got.Channel.ReplyTo, action.Channel.ReplyTo)
	}
	if !got.ArrivedAt.Equal(action.ArrivedAt) {
		t.Fatalf("the recovered action arrived at %v, want %v", got.ArrivedAt, action.ArrivedAt)
	}
}

// At-least-once delivery WILL re-put. Against a real ObjectStore that is a
// versioned overwrite of one object rather than a second one, which is the
// property the derived key buys — and the only reason the write can be issued
// unconditionally, before the atomic create that arbitrates the turn.
func TestIntegration_ARedeliveryRePutsOntoOneObject(t *testing.T) {
	bucket := testBucket(t)
	store, backend := liveStore(t, bucket)

	action := testAction(t, "act-redeliver")
	turnEntityID := compose(t, "turn", payload.TurnIDForAction(action.ActionID))

	first, err := store.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("first PutAction: %v", err)
	}
	firstBytes, err := backend.Get(t.Context(), first.Key)
	if err != nil {
		t.Fatalf("read back the first object: %v", err)
	}

	second, err := store.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("second PutAction: %v", err)
	}
	if second != first {
		t.Fatalf("a redelivery produced %s, the first produced %s", second, first)
	}

	keys, err := backend.List(t.Context(), "")
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("two deliveries left %d objects in the bucket: %v", len(keys), keys)
	}
	if keys[0] != first.Key {
		t.Fatalf("the stored object is at %q, the reference addresses %q", keys[0], first.Key)
	}

	secondBytes, err := backend.Get(t.Context(), second.Key)
	if err != nil {
		t.Fatalf("read back the second object: %v", err)
	}
	if string(secondBytes) != string(firstBytes) {
		t.Fatalf("the re-put wrote different bytes to the same key:\n first: %s\nsecond: %s",
			firstBytes, secondBytes)
	}
}

// The failure detail's reference is a real one written to a real store, because
// the alternative — a placeholder string a test invents — proves the recorder
// can write a string and nothing about whether anything is behind it.
func TestIntegration_AFailureDetailIsResolvableFromItsReference(t *testing.T) {
	store, _ := liveStore(t, testBucket(t))

	detail := &content.FailureDetail{
		TurnID:    "turn-act-partial",
		Reason:    vocabulary.FailureEffectCommitIncomplete,
		Message:   "merge into the sentry failed after the courier committed",
		Target:    compose(t, "character", "hollis"),
		Committed: []string{compose(t, "character", "rook")},
	}
	turnEntityID := compose(t, "turn", detail.TurnID)

	ref, err := store.PutFailureDetail(t.Context(), turnEntityID, detail)
	if err != nil {
		t.Fatalf("PutFailureDetail: %v", err)
	}

	// And the reference round-trips through the spelling a triple carries,
	// because that is the only form the graph will ever hand back.
	parsed, err := content.ParseRef(ref.String())
	if err != nil {
		t.Fatalf("ParseRef(%q): %v", ref, err)
	}
	got, err := store.GetFailureDetail(t.Context(), parsed)
	if err != nil {
		t.Fatalf("GetFailureDetail: %v", err)
	}
	if got.Target != detail.Target || len(got.Committed) != 1 {
		t.Fatalf("the resolved detail reads %+v", got)
	}
}
