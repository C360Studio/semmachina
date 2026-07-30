package campaign_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// markerObjects returns every value the campaign entity records for the
// import-completion marker.
//
// A slice rather than a single value, deliberately: "how many values does this
// predicate hold?" is the question that distinguishes the replacing lane from
// the appending one, and a helper returning the first would answer it wrong
// every time.
func markerObjects(t *testing.T, store *fakeStore) []any {
	t.Helper()
	state, err := store.GetEntity(context.Background(), testCampaignID)
	if err != nil {
		t.Fatalf("read campaign entity: %v", err)
	}
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.CampaignImportCompleted.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

// The whole point of taking the claim as an argument: a boot that did not create
// the campaign cannot certify somebody else's import.
func TestMarkImported_RefusesAClaimThatDidNotCreateTheCampaign(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	if _, err := gate.Claim(t.Context()); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	loser, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if loser.Fresh {
		t.Fatal("the second claim reported a fresh world")
	}

	if _, err := gate.MarkImported(t.Context(), loser); err == nil {
		t.Fatal("a claimant that lost the create was allowed to mark the import complete; that is how a late " +
			"loser manufactures the marked-but-partial world the marker exists to prevent")
	}
	if objects := markerObjects(t, store); len(objects) != 0 {
		t.Fatalf("the refused mark still wrote %v", objects)
	}
}

func TestMarkImported_RefusesAClaimOnADifferentCampaign(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	claim.CampaignID = "c360.semmachina.other.starter.campaign.main"

	if _, err := gate.MarkImported(t.Context(), claim); err == nil {
		t.Fatal("a fresh claim on another campaign was allowed to mark THIS one imported")
	}
}

// The marker records the instant, is readable, and — the part that needs the
// lane — replaces itself rather than accumulating.
func TestMarkImported_RecordsOneInstantAndReplacesItOnASecondWrite(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	at, err := gate.MarkImported(t.Context(), claim)
	if err != nil {
		t.Fatalf("MarkImported: %v", err)
	}
	if !at.Equal(testClock()) {
		t.Errorf("marker instant = %s, want the gate's clock %s", at, testClock())
	}

	read, marked, err := gate.ImportCompletion(t.Context())
	if err != nil {
		t.Fatalf("ImportCompletion: %v", err)
	}
	if !marked || !read.Equal(at) {
		t.Fatalf("ImportCompletion = (%s, %v), want (%s, true)", read, marked, at)
	}

	if _, err := gate.MarkImported(t.Context(), claim); err != nil {
		t.Fatalf("second MarkImported: %v", err)
	}
	if objects := markerObjects(t, store); len(objects) != 1 {
		t.Fatalf("the campaign holds %d completion instants after two marks, want one; a marker on an "+
			"appending lane cannot say which import finished", len(objects))
	}
}

// The negative control for the assertion above. Against a store that APPENDS —
// which is what graph.mutation.triple.add_batch does — the same two writes leave
// two markers, so the one-value assertion is a fact about the lane rather than
// about the fake.
func TestMarkImported_TheAppendingLaneWouldLeaveTwoMarkers(t *testing.T) {
	base := newFakeStore()
	store := &appendingStore{fakeStore: base}
	gate, err := campaign.NewGate(store, testIdentity,
		campaign.WithClock(testClock), campaign.WithSeedSource(countingSeeds()))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	claim, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for range 2 {
		if _, err := gate.MarkImported(t.Context(), claim); err != nil {
			t.Fatalf("MarkImported: %v", err)
		}
	}
	if objects := markerObjects(t, base); len(objects) != 2 {
		t.Fatalf("the appending lane left %d markers, want 2; this control is what makes the merge-lane "+
			"assertion mean something", len(objects))
	}
	if _, _, err := gate.ImportCompletion(t.Context()); err == nil {
		t.Fatal("two completion instants were read as an answer rather than as a corrupted record")
	}
}

// The other half of the lane choice, and the one with the sharpest consequence:
// the seed shares this entity, and a boot that rewrote it would make every roll
// already in the ledger unreproducible.
func TestMarkImported_LeavesTheCampaignSeedIntact(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := gate.MarkImported(t.Context(), claim); err != nil {
		t.Fatalf("MarkImported: %v", err)
	}

	stored, err := gate.LoadSeed(t.Context())
	if err != nil {
		t.Fatalf("LoadSeed after marking: %v", err)
	}
	if stored != claim.Seed {
		t.Errorf("seed after marking = %s, want %s", stored, claim.Seed)
	}
}

// The three states the marker has to distinguish, each with a different remedy.
func TestImportCompletion_DistinguishesAbsentFromUnmarked(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	if _, _, err := gate.ImportCompletion(t.Context()); !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("ImportCompletion on an uninstantiated world = %v, want ErrEntityNotFound", err)
	}

	if _, err := gate.Claim(t.Context()); err != nil {
		t.Fatalf("claim: %v", err)
	}
	at, marked, err := gate.ImportCompletion(t.Context())
	if err != nil {
		t.Fatalf("ImportCompletion on a claimed world: %v", err)
	}
	if marked || !at.IsZero() {
		t.Fatalf("a claimed world with no import reported (%s, %v), want the unmarked answer", at, marked)
	}
}

func TestImportCompletion_RefusesAStubAndAnUnreadableInstant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		entity *graph.EntityState
	}{
		{
			name:   "referential stub",
			entity: &graph.EntityState{ID: testCampaignID, MessageType: graph.StubMessageType},
		},
		{
			name: "non-string object",
			entity: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: campaign.EntityMessageType,
				Triples: []message.Triple{{
					Subject:   testCampaignID,
					Predicate: vocabulary.CampaignImportCompleted.String(),
					Object:    42,
				}},
			},
		},
		{
			name: "unparseable instant",
			entity: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: campaign.EntityMessageType,
				Triples: []message.Triple{{
					Subject:   testCampaignID,
					Predicate: vocabulary.CampaignImportCompleted.String(),
					Object:    "yesterday",
				}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			store.put(tc.entity)
			gate := newTestGate(t, store)

			if _, _, err := gate.ImportCompletion(t.Context()); err == nil {
				t.Fatal("an unreadable campaign record was reported as an answer")
			}
		})
	}
}

// A boot that finds an unmarked campaign waits for the real importer, which is
// the only one of the three answers that has to be BOUNDED.
func TestAwaitImportCompletion_ReturnsOnceAnotherClaimantMarksIt(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// The marker lands on the third read, so the wait genuinely polls rather
	// than answering from the first look. The gate's clock is frozen, so the
	// bound below can never fire and the test cannot pass by timing out.
	var reads atomic.Int64
	late := &lateMarkingStore{fakeStore: store, gate: gate, claim: claim, markOn: 3, reads: &reads}
	waiting, err := campaign.NewGate(late, testIdentity,
		campaign.WithClock(testClock), campaign.WithSeedSource(countingSeeds()))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	at, err := waiting.AwaitImportCompletion(t.Context(), time.Minute, time.Millisecond)
	if err != nil {
		t.Fatalf("AwaitImportCompletion: %v", err)
	}
	if at.IsZero() {
		t.Error("the wait returned the zero instant")
	}
	if got := reads.Load(); got < 3 {
		t.Errorf("the wait read the campaign %d times; it answered before the marker could have landed", got)
	}
}

// The bound is a real bound, and the read count is what says so.
//
// Asserting only that the wait eventually returned ErrImportInterrupted proves
// far less than it looks: with the clock stepping one `wait` per call, a
// correct implementation reads the campaign exactly ONCE and then gives up, while
// an implementation whose deadline arithmetic is off by an hour returns the same
// error thousands of reads later and passes the same assertion. A boot that
// stalls for an hour before saying what is wrong is the failure this bound
// exists to prevent, so the count is the assertion.
func TestAwaitImportCompletion_FailsLoudlyRatherThanImportingOrMarking(t *testing.T) {
	base := newFakeStore()
	var reads atomic.Int64
	store := &countingStore{fakeStore: base, reads: &reads}
	gate, err := campaign.NewGate(store, testIdentity,
		campaign.WithClock(steppingClock(time.Second)), campaign.WithSeedSource(countingSeeds()))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if _, err := gate.Claim(t.Context()); err != nil {
		t.Fatalf("claim: %v", err)
	}
	reads.Store(0)

	_, err = gate.AwaitImportCompletion(t.Context(), time.Second, time.Millisecond)
	if !errors.Is(err, campaign.ErrImportInterrupted) {
		t.Fatalf("AwaitImportCompletion on a claimed, never-marked campaign = %v, want ErrImportInterrupted", err)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("the wait read the campaign %d times before giving up; with the clock stepping one whole "+
			"bound per call it must give up after one, and anything more means the deadline arithmetic does "+
			"not hold the bound the caller asked for", got)
	}
	if objects := markerObjects(t, base); len(objects) != 0 {
		t.Fatalf("the wait wrote %v; a boot that imported nothing must not certify an import", objects)
	}
}

// countingStore counts reads without changing any of them.
type countingStore struct {
	*fakeStore
	reads *atomic.Int64
}

func (s *countingStore) GetEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	s.reads.Add(1)
	return s.fakeStore.GetEntity(ctx, id)
}

// steppingClock advances by step on every call, so a bounded wait reaches its
// deadline deterministically instead of by sleeping through one.
func steppingClock(step time.Duration) func() time.Time {
	var calls atomic.Int64
	base := testClock()
	return func() time.Time {
		return base.Add(time.Duration(calls.Add(1)-1) * step)
	}
}

// lateMarkingStore marks the import on the Nth read, standing in for the other
// process whose import finishes while this boot is waiting.
type lateMarkingStore struct {
	*fakeStore
	gate   *campaign.Gate
	claim  campaign.Instantiation
	markOn int64
	reads  *atomic.Int64
}

func (s *lateMarkingStore) GetEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	if s.reads.Add(1) == s.markOn {
		if _, err := s.gate.MarkImported(ctx, s.claim); err != nil {
			return nil, err
		}
	}
	return s.fakeStore.GetEntity(ctx, id)
}

// A hand-built Instantiation is not proof of anything.
//
// MarkImported takes the claim as EVIDENCE that this boot won the atomic create,
// and Instantiation is an exported struct with exported fields — so without an
// unexported provenance marker, `campaign.Instantiation{CampaignID: x, Fresh:
// true}` is an ordinary composite literal that compiles and marks a campaign this
// boot never imported. The guard is only worth having if the value it inspects
// can only have come from Claim.
func TestMarkImported_RefusesAnInstantiationThatDidNotComeFromClaim(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	if _, err := gate.Claim(t.Context()); err != nil {
		t.Fatalf("claim: %v", err)
	}

	forged := campaign.Instantiation{CampaignID: testCampaignID, Fresh: true}
	if _, err := gate.MarkImported(t.Context(), forged); err == nil {
		t.Fatal("a hand-built Instantiation marked the import complete; the argument is supposed to be proof " +
			"that this boot won the create, and a composite literal proves nothing")
	}
	if objects := markerObjects(t, store); len(objects) != 0 {
		t.Fatalf("the refused mark still wrote %v", objects)
	}

	// The positive control: a claim that DID come from Claim still marks, so the
	// refusal above is about provenance rather than about MarkImported having
	// stopped working.
	real, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	real.Fresh = true // the second claim lost, as it must; the provenance is what is under test
	if _, err := gate.MarkImported(t.Context(), real); err != nil {
		t.Fatalf("a claim that came from Claim was refused: %v", err)
	}
}
