package campaign_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

var testIdentity = campaign.Identity{Org: "c360", WorldNS: "world1", Template: "starter"}

const testCampaignID = "c360.semmachina.world1.starter.campaign.main"

var testClock = func() time.Time { return time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC) }

// fakeStore is an in-memory create-or-fail store with the same atomicity the
// real one has. It exists for the failure shapes a broker will not produce on
// demand (degraded write, stub resident, missing seed triple); the atomic and
// concurrent paths are ALSO proven against real NATS in gate_integration_test.go,
// because a fake that agreed with itself would prove nothing about the contract
// this design rests on.
type fakeStore struct {
	mu       sync.Mutex
	entities map[string]*graph.EntityState

	degrade    bool
	createErr  error
	getErr     error
	mergeErr   error
	createCall int
	getCall    int
}

func newFakeStore() *fakeStore {
	return &fakeStore{entities: make(map[string]*graph.EntityState)}
}

func (s *fakeStore) CreateEntity(_ context.Context, entity *graph.EntityState) (graphio.CreateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCall++
	if s.createErr != nil {
		return graphio.CreateResult{}, s.createErr
	}
	if _, taken := s.entities[entity.ID]; taken {
		return graphio.CreateResult{}, fmt.Errorf("create entity %s: %w", entity.ID, graphio.ErrEntityExists)
	}
	s.entities[entity.ID] = entity
	if s.degrade {
		return graphio.CreateResult{Degraded: true, DegradedReason: "read-back failed"}, nil
	}
	return graphio.CreateResult{Entity: entity, Revision: uint64(len(s.entities))}, nil
}

func (s *fakeStore) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCall++
	if s.getErr != nil {
		return nil, s.getErr
	}
	entity, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	return entity, nil
}

// MergeTriples replaces by (subject, predicate), which is the property every
// caller of the real merge lane depends on. A fake that appended would agree
// with the WRONG lane and would make the marker tests pass while the production
// write left two completion instants on one campaign.
func (s *fakeStore) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	_ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mergeErr != nil {
		return nil, s.mergeErr
	}
	entity, ok := s.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("merge into %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	for _, triple := range triples {
		if triple.Subject != entityID {
			return nil, fmt.Errorf("triple targets %q, not %q", triple.Subject, entityID)
		}
	}
	merged := *entity
	merged.Triples = slices.Clone(entity.Triples)
	for _, triple := range triples {
		merged.Triples = slices.DeleteFunc(merged.Triples, func(existing message.Triple) bool {
			return existing.Predicate == triple.Predicate
		})
		merged.Triples = append(merged.Triples, triple)
	}
	s.entities[entityID] = &merged
	return &merged, nil
}

// appendingStore is the negative control for the lane choice: it APPENDS where
// the merge lane replaces, exactly as graph.mutation.triple.add_batch does.
//
// It exists because "the marker replaces" is otherwise a claim about a fake
// nobody perturbed. A test asserting one marker after two writes passes against
// a replacing fake whether the production code chose a lane deliberately or by
// accident; the same assertion against this one fails, which is what makes the
// first assertion mean something.
type appendingStore struct{ *fakeStore }

func (s *appendingStore) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	_ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entity, ok := s.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("merge into %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	merged := *entity
	merged.Triples = append(slices.Clone(entity.Triples), triples...)
	s.entities[entityID] = &merged
	return &merged, nil
}

func (s *fakeStore) put(entity *graph.EntityState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entities[entity.ID] = entity
}

// countingSeeds hands out a different, predictable seed on every call, so a
// test can tell WHICH generation a returned seed came from.
func countingSeeds() func() (campaign.Seed, error) {
	var mu sync.Mutex
	n := byte(0)
	return func() (campaign.Seed, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		var seed campaign.Seed
		for idx := range seed {
			seed[idx] = n
		}
		return seed, nil
	}
}

func newTestGate(t *testing.T, store campaign.EntityStore, opts ...campaign.GateOption) *campaign.Gate {
	t.Helper()
	opts = append([]campaign.GateOption{
		campaign.WithClock(testClock),
		campaign.WithSeedSource(countingSeeds()),
	}, opts...)
	gate, err := campaign.NewGate(store, testIdentity, opts...)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return gate
}

func TestGate_CampaignIDIsTheComposedSixPartIdentity(t *testing.T) {
	gate := newTestGate(t, newFakeStore())
	if gate.CampaignID() != testCampaignID {
		t.Fatalf("campaign id = %q, want %q", gate.CampaignID(), testCampaignID)
	}
}

// The gate's whole job: created means fresh, taken means already instantiated.
func TestGateClaim_FirstCallIsFreshAndSecondIsNot(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	first, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if !first.Fresh {
		t.Fatal("the first claim on an empty world reported an existing campaign")
	}

	second, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if second.Fresh {
		t.Fatal("the second claim reported a fresh world; the importer would reset a live campaign")
	}
	if second.CampaignID != first.CampaignID {
		t.Fatalf("claims disagree on the campaign entity: %q vs %q", first.CampaignID, second.CampaignID)
	}
}

// The seed is generated ONCE for a campaign's whole life. The seed source hands
// out a different value on every call, so a Claim that returned seed #2 would be
// a Claim that had just re-seeded a campaign with recorded history — every roll
// in its ledger would still validate and none would reproduce.
func TestGateClaim_NeverRegeneratesTheSeedOfAnExistingCampaign(t *testing.T) {
	seeds := countingSeeds()
	gate := newTestGate(t, newFakeStore(), campaign.WithSeedSource(seeds))

	first, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}

	for attempt := range 3 {
		again, err := gate.Claim(t.Context(), testExperience)
		if err != nil {
			t.Fatalf("Claim %d: %v", attempt+2, err)
		}
		if again.Seed != first.Seed {
			t.Fatalf("claim %d returned a different seed than the campaign was created with", attempt+2)
		}
	}

	// Guard: the SHARED source must have moved on, or the assertion above
	// would hold for a gate that re-seeded on every call.
	next, err := seeds()
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if next == first.Seed {
		t.Fatal("the seed source never advanced; this test would pass against a gate that re-seeded")
	}
}

// A campaign that a second process created between our mint and our create must
// still yield ITS seed, not the one we minted and lost with.
func TestGateClaim_LosingTheRaceReturnsTheStoredSeed(t *testing.T) {
	store := newFakeStore()
	winner := newTestGate(t, store)
	loser := newTestGate(t, store)

	won, err := winner.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("winner Claim: %v", err)
	}

	lost, err := loser.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("loser Claim: %v", err)
	}
	if lost.Fresh {
		t.Fatal("the loser reported a fresh world")
	}
	if lost.Seed != won.Seed {
		t.Fatal("the loser is holding its own discarded seed; its rolls would not match the campaign's ledger")
	}
}

// A degraded create COMMITTED. Retrying it would come back as
// entity_already_exists and a caller reading that as "someone beat me to it"
// would skip the import of a world nobody imported.
func TestGateClaim_TreatsADegradedCreateAsACommittedFreshWorld(t *testing.T) {
	store := newFakeStore()
	store.degrade = true
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claim.Fresh {
		t.Fatal("a committed-but-unechoed create was reported as an existing campaign")
	}
	if claim.Seed.IsZero() {
		t.Fatal("a degraded create returned no seed; the dice would have nothing to derive from")
	}
	if store.createCall != 1 {
		t.Fatalf("the gate issued %d creates; a degraded write must not be retried", store.createCall)
	}
}

// Every way the stored seed can be unreadable is a STOP. The tempting recovery
// — mint a fresh seed and carry on — would leave a campaign whose recorded
// rolls all validate and none reproduce.
func TestGateClaim_RefusesToProceedWhenTheStoredSeedIsUnreadable(t *testing.T) {
	cases := []struct {
		name     string
		resident *graph.EntityState
		wantErr  string
	}{
		{
			name: "a referential stub occupies the key",
			resident: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: graph.StubMessageType,
				Triples: []message.Triple{{
					Subject: testCampaignID, Predicate: graph.PredStubMarker, Object: "true", Confidence: 1.0,
				}},
			},
			wantErr: "referential stub",
		},
		{
			name: "the campaign carries no seed triple",
			resident: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: campaign.EntityMessageType,
				Triples: []message.Triple{{
					Subject: testCampaignID, Predicate: vocabulary.WorldEntityName.String(),
					Object: "Main Campaign", Confidence: 1.0,
				}},
			},
			wantErr: "carries no campaign.seed.value",
		},
		{
			name: "the seed value is not a string",
			resident: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: campaign.EntityMessageType,
				Triples: []message.Triple{{
					Subject: testCampaignID, Predicate: vocabulary.CampaignSeedValue.String(),
					Object: 42, Confidence: 1.0,
				}},
			},
			wantErr: "want a hex string",
		},
		{
			name: "the seed value is not a seed",
			resident: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: campaign.EntityMessageType,
				Triples: []message.Triple{{
					Subject: testCampaignID, Predicate: vocabulary.CampaignSeedValue.String(),
					Object: "deadbeef", Confidence: 1.0,
				}},
			},
			wantErr: "bytes",
		},
		{
			name: "the seed is ambiguous",
			resident: &graph.EntityState{
				ID:          testCampaignID,
				MessageType: campaign.EntityMessageType,
				Triples: []message.Triple{
					{Subject: testCampaignID, Predicate: vocabulary.CampaignSeedValue.String(), Object: strings.Repeat("01", campaign.SeedBytes)},
					{Subject: testCampaignID, Predicate: vocabulary.CampaignSeedValue.String(), Object: strings.Repeat("02", campaign.SeedBytes)},
				},
			},
			wantErr: "ambiguous",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			store.put(tc.resident)
			gate := newTestGate(t, store)

			claim, err := gate.Claim(t.Context(), testExperience)
			if err == nil {
				t.Fatalf("Claim proceeded past an unreadable seed: %+v", claim)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("failure reason %q does not mention %q", err.Error(), tc.wantErr)
			}
			if !strings.Contains(err.Error(), "never regenerated") {
				t.Fatalf("failure reason %q does not say why this is a stop rather than a re-seed", err.Error())
			}
			if !claim.Seed.IsZero() || claim.Fresh {
				t.Fatalf("a refused claim returned usable state: %+v", claim)
			}
		})
	}
}

// A create that succeeds while the seed fails to store leaves a campaign whose
// stored seed differs from the one this process is about to roll with:
// reproducible in memory, unreproducible after restart.
func TestGateClaim_RefusesACreateWhoseSeedDidNotStore(t *testing.T) {
	store := &seedDroppingStore{fakeStore: newFakeStore()}
	gate := newTestGate(t, store)

	if _, err := gate.Claim(t.Context(), testExperience); err == nil {
		t.Fatal("a create whose seed triple was dropped was accepted")
	} else if !strings.Contains(err.Error(), "seed did not store") {
		t.Fatalf("failure reason %q does not name the dropped seed", err.Error())
	}
}

type seedDroppingStore struct{ *fakeStore }

func (s *seedDroppingStore) CreateEntity(
	ctx context.Context,
	entity *graph.EntityState,
) (graphio.CreateResult, error) {
	result, err := s.fakeStore.CreateEntity(ctx, entity)
	if err != nil || result.Entity == nil {
		return result, err
	}
	stripped := result.Entity.Clone()
	stripped.Triples = nil
	return graphio.CreateResult{Entity: stripped, Revision: result.Revision}, nil
}

// A transport failure is neither "fresh" nor "already instantiated"; it must
// not be resolved into either.
func TestGateClaim_PropagatesAnUnclassifiedCreateFailure(t *testing.T) {
	store := newFakeStore()
	store.createErr = errors.New("nats: no responders")
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context(), testExperience)
	if err == nil {
		t.Fatal("a failed create was resolved into an instantiation decision")
	}
	if claim.Fresh {
		t.Fatal("a failed create reported a fresh world")
	}
}

func TestGateLoadSeed_ReportsAnAbsentCampaign(t *testing.T) {
	gate := newTestGate(t, newFakeStore())

	if _, err := gate.LoadSeed(t.Context()); !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("LoadSeed on an absent campaign returned %v, want ErrEntityNotFound", err)
	}
}

func TestGateLoadSeed_ReadsTheSeedAClaimStored(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	loaded, err := gate.LoadSeed(t.Context())
	if err != nil {
		t.Fatalf("LoadSeed: %v", err)
	}
	if loaded != claim.Seed {
		t.Fatal("LoadSeed returned a different seed than Claim stored")
	}
}

func TestNewGate_RejectsAnIdentityThatCannotCompose(t *testing.T) {
	cases := []struct {
		name     string
		identity campaign.Identity
		wantErr  string
	}{
		{"no org", campaign.Identity{WorldNS: "world1", Template: "starter"}, "org"},
		{"no namespace", campaign.Identity{Org: "c360", Template: "starter"}, "namespace"},
		{"no template", campaign.Identity{Org: "c360", WorldNS: "world1"}, "template"},
		{
			"a dotted namespace would add ID positions",
			campaign.Identity{Org: "c360", WorldNS: "world.1", Template: "starter"},
			"dot",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := campaign.NewGate(newFakeStore(), tc.identity)
			if err == nil {
				t.Fatal("an identity that cannot compose a canonical ID was accepted")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewGate_RequiresAStore(t *testing.T) {
	if _, err := campaign.NewGate(nil, testIdentity); err == nil {
		t.Fatal("a gate was built with no store")
	}
}

// The campaign entity must not read as a referential stub: boot-readiness and
// the gate's own recovery path both key on the envelope, and a zero envelope is
// indistinguishable from a stub.
func TestGateClaim_StampsARealEnvelopeOnTheCampaignEntity(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	if _, err := gate.Claim(t.Context(), testExperience); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	stored, err := store.GetEntity(t.Context(), testCampaignID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if stored.IsStub() {
		t.Fatal("the created campaign entity reads as a referential stub")
	}
	if !stored.MessageType.IsValid() {
		t.Fatalf("the campaign entity carries an invalid envelope %v", stored.MessageType)
	}
	if !stored.UpdatedAt.Equal(testClock()) {
		t.Fatalf("the campaign entity is stamped %v, want the injected clock %v", stored.UpdatedAt, testClock())
	}
}
