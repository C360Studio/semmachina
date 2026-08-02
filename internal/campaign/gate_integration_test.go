package campaign_test

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The instantiation gate is the one design in this slice whose correctness is
// ENTIRELY a property of the substrate: "created means fresh, taken means
// already instantiated" is true only because graph-ingest's entity.create is an
// atomic KV Create. A fake store that implements create-or-fail agrees with
// itself and proves nothing about that. So the gate is exercised against a real
// broker and a real graph-ingest, including the concurrent case the atomicity
// exists for.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

// realGate builds a gate over the real graph client in its own world namespace,
// so tests are isolated by the same mechanism that isolates two campaigns.
func realGate(t *testing.T, worldNS string, opts ...campaign.GateOption) (*campaign.Gate, *testinfra.Harness) {
	t.Helper()
	harness := testinfra.Require(t)

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	identity := campaign.Identity{Org: "c360", WorldNS: worldNS, Template: "starter"}
	gate, err := campaign.NewGate(store, identity, opts...)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return gate, harness
}

func TestIntegration_ClaimCreatesTheCampaignEntityCarryingItsSeed(t *testing.T) {
	gate, harness := realGate(t, "gateworld1")

	claim, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claim.Fresh {
		t.Fatal("the first claim in an empty graph reported an existing campaign")
	}
	if claim.Seed.IsZero() {
		t.Fatal("a fresh campaign was created with no seed")
	}

	// AwaitEntity refuses a referential stub, so this also proves the campaign
	// entity was born with a real envelope — the signal boot-readiness keys on.
	state := harness.AwaitEntity(t, claim.CampaignID)
	stored := testinfra.ObjectsFor(state, vocabulary.CampaignSeedValue.String())
	if len(stored) != 1 {
		t.Fatalf("campaign entity holds %d seed values, want exactly one: %v", len(stored), stored)
	}
	if stored[0] != claim.Seed.String() {
		t.Fatalf("the graph holds seed %v, the claim returned %v", stored[0], claim.Seed)
	}
	for predicate, want := range map[string]string{
		vocabulary.CampaignExperiencePersonaPack.String():   testExperience.PersonaPack,
		vocabulary.CampaignExperienceMechanicsPack.String(): testExperience.MechanicsPack,
	} {
		objects := testinfra.ObjectsFor(state, predicate)
		if len(objects) != 1 || objects[0] != want {
			t.Fatalf("campaign %s values = %v, want exactly one %q", predicate, objects, want)
		}
	}
}

func TestIntegration_SecondClaimSeesTheExistingCampaignAndItsOriginalSeed(t *testing.T) {
	seeds := countingSeeds()
	gate, _ := realGate(t, "gateworld2", campaign.WithSeedSource(seeds))

	first, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	second, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}

	if !first.Fresh {
		t.Fatal("the first claim did not report a fresh world")
	}
	if second.Fresh {
		t.Fatal("the second claim reported a fresh world; a boot would re-import over a live campaign")
	}
	if second.Seed != first.Seed {
		t.Fatal("the second claim returned a re-minted seed; every roll already in the ledger would stop reproducing")
	}
	if second.Experience != testExperience {
		t.Fatalf("the second claim returned experience %+v, want %+v", second.Experience, testExperience)
	}

	// Guard: the source advanced, so the equality above is a real claim about
	// the gate rather than an artifact of a constant seed source.
	next, err := seeds()
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if next == first.Seed {
		t.Fatal("the seed source never advanced; this test would pass against a gate that re-seeded")
	}
}

// The reason the gate is one atomic create rather than an exists-check followed
// by a write. Under the check-then-write shape, concurrent boots both observe
// an absent world and both proceed to import; under create-or-fail there is
// exactly one winner, and every loser reads back the winner's seed.
func TestIntegration_ConcurrentClaimsHaveExactlyOneWinnerAndOneSeed(t *testing.T) {
	const claimants = 8
	gate, _ := realGate(t, "gateworld3")

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []campaign.Instantiation
		errs    []error
	)
	start := make(chan struct{})

	for range claimants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := gate.Claim(t.Context(), testExperience)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			results = append(results, claim)
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("%d concurrent claims failed: %v", len(errs), errs)
	}
	if len(results) != claimants {
		t.Fatalf("got %d results from %d claimants", len(results), claimants)
	}

	fresh := 0
	for _, claim := range results {
		if claim.Fresh {
			fresh++
		}
		if claim.Seed != results[0].Seed {
			t.Fatal("concurrent claimants disagree on the campaign seed; they would roll different dice for one campaign")
		}
		if claim.CampaignID != results[0].CampaignID {
			t.Fatal("concurrent claimants disagree on the campaign entity")
		}
	}
	if fresh != 1 {
		t.Fatalf("%d of %d concurrent claims reported a fresh world; exactly one may import", fresh, claimants)
	}
}

// F13 argues the campaign entity is immune to the referential-stub problem
// because nothing references a campaign today. "Today" is doing real work in
// that sentence: the moment some entity carries an entity reference to the
// campaign's ID, graph-ingest mints a STUB at that key, and the gate's create
// then loses to a resident that holds no seed.
//
// The failure that must not happen is the quiet one — reading "the key is
// taken" as "the world is already instantiated" and skipping the import of a
// world that was never imported, then rolling dice from a seed nobody minted.
// So the guard is proven against real infrastructure rather than assumed from
// the F13 argument.
//
// The referencing predicate is immaterial; what makes graph-ingest mint the
// stub is an object that resolves as an entity reference.
func TestIntegration_ClaimRefusesACampaignKeyOccupiedByAReferentialStub(t *testing.T) {
	gate, harness := realGate(t, "gateworld6")

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	const referrer = "c360.semmachina.gateworld6.starter.character.rook"
	stamp := time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)
	if _, err := store.CreateEntity(t.Context(), &graph.EntityState{
		ID:          referrer,
		MessageType: campaign.EntityMessageType,
		Version:     1,
		UpdatedAt:   stamp,
		Triples: []message.Triple{{
			Subject:    referrer,
			Predicate:  vocabulary.WorldRelationKnows.String(),
			Object:     gate.CampaignID(),
			Datatype:   message.EntityReferenceDatatype,
			Source:     "stub-collision-test",
			Timestamp:  stamp,
			Confidence: 1.0,
		}},
	}); err != nil {
		t.Fatalf("create the referring entity: %v", err)
	}

	// Confirm the premise: the campaign key is occupied by a stub. Without
	// this the test could pass because nothing was ever there.
	//
	// A failed premise FAILS rather than skips, for the two reasons testinfra's
	// opt-out policy is written around. `go test` discards a passing package's
	// output, so a skip here is invisible without -v: "the guard is covered" and
	// "the guard was never exercised" would look identical. And the premise not
	// holding is not a local inconvenience — it means graph-ingest stopped
	// minting referential stubs, an upstream behavior change that invalidates
	// F11's reasoning and belongs in front of whoever is reading the build.
	var occupied bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, err := harness.QueryEntity(t.Context(), gate.CampaignID())
		if err == nil && state.IsStub() {
			occupied = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !occupied {
		t.Fatal("graph-ingest did not mint a referential stub at the campaign key; the premise no longer holds")
	}

	claim, err := gate.Claim(t.Context(), testExperience)
	if err == nil {
		t.Fatalf("the gate reported an instantiation decision over a factless stub: %+v", claim)
	}
	if !strings.Contains(err.Error(), "referential stub") {
		t.Fatalf("failure reason %q does not name the stub", err.Error())
	}
	if claim.Fresh || !claim.Seed.IsZero() {
		t.Fatalf("a refused claim returned usable state: %+v", claim)
	}
}

func TestIntegration_LoadSeedReportsAnAbsentCampaignRatherThanMintingOne(t *testing.T) {
	gate, _ := realGate(t, "gateworld4")

	seed, err := gate.LoadSeed(t.Context())
	if !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("LoadSeed on an absent campaign returned %v, want ErrEntityNotFound", err)
	}
	if !seed.IsZero() {
		t.Fatal("a failed load returned seed material")
	}
}

// The seed survives a fresh client: replay determinism has to outlive the
// process that minted the seed, which means it has to come out of the graph and
// not out of a field somebody remembered to keep.
func TestIntegration_SeedSurvivesANewClientOverTheSameGraph(t *testing.T) {
	gate, harness := realGate(t, "gateworld5")

	claim, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reboot, err := campaign.NewGate(store, campaign.Identity{Org: "c360", WorldNS: "gateworld5", Template: "starter"})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	loaded, err := reboot.LoadSeed(t.Context())
	if err != nil {
		t.Fatalf("LoadSeed after reboot: %v", err)
	}
	if loaded != claim.Seed {
		t.Fatal("a restarted process read a different campaign seed; every recorded roll would stop reproducing")
	}
}
