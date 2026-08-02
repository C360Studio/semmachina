package campaign_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

var testExperience = campaign.Experience{PersonaPack: "voices", MechanicsPack: "core-rules"}

func TestExperienceValidate_RequiresCanonicalPackIDs(t *testing.T) {
	for name, experience := range map[string]campaign.Experience{
		"missing persona pack":   {MechanicsPack: "core-rules"},
		"missing mechanics pack": {PersonaPack: "voices"},
		"dotted persona pack":    {PersonaPack: "voice.pack", MechanicsPack: "core-rules"},
		"invalid mechanics pack": {PersonaPack: "voices", MechanicsPack: "core rules"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := experience.Validate(); err == nil {
				t.Fatalf("Validate accepted %+v", experience)
			}
		})
	}
	if err := testExperience.Validate(); err != nil {
		t.Fatalf("Validate rejected canonical experience: %v", err)
	}
}

func TestGateClaim_FreshCreateAtomicallyStoresSeedAndExperience(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)

	claim, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claim.Fresh || claim.Experience != testExperience {
		t.Fatalf("fresh claim = %+v, want requested experience", claim)
	}
	state, err := store.GetEntity(t.Context(), testCampaignID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	want := map[string]string{
		vocabulary.CampaignSeedValue.String():               claim.Seed.String(),
		vocabulary.CampaignExperiencePersonaPack.String():   testExperience.PersonaPack,
		vocabulary.CampaignExperienceMechanicsPack.String(): testExperience.MechanicsPack,
	}
	if len(state.Triples) != len(want) {
		t.Fatalf("fresh campaign triples = %d, want exactly seed + two provenance triples: %+v", len(state.Triples), state.Triples)
	}
	for predicate, object := range want {
		triples := triplesFor(state, predicate)
		if len(triples) != 1 || triples[0].Object != object {
			t.Fatalf("%s triples = %+v, want one string %q", predicate, triples, object)
		}
		triple := triples[0]
		if triple.Source != campaign.InstantiationSource || triple.Context != testCampaignID ||
			!triple.Timestamp.Equal(testClock()) {
			t.Fatalf("%s provenance = source %q context %q at %v", predicate,
				triple.Source, triple.Context, triple.Timestamp)
		}
	}
}

func TestGateClaim_ExactRestartReturnsStoredSeedAndExperience(t *testing.T) {
	store := newFakeStore()
	gate := newTestGate(t, store)
	first, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("fresh Claim: %v", err)
	}
	restarted, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("restart Claim: %v", err)
	}
	if restarted.Fresh || restarted.Seed != first.Seed || restarted.Experience != testExperience {
		t.Fatalf("restart claim = %+v, first = %+v", restarted, first)
	}
	if store.getCall != 1 {
		t.Fatalf("restart used %d GetEntity snapshots, want exactly one", store.getCall)
	}
}

func TestGateClaim_RefusesStoredExperienceMismatch(t *testing.T) {
	gate := newTestGate(t, newFakeStore())
	if _, err := gate.Claim(t.Context(), testExperience); err != nil {
		t.Fatalf("fresh Claim: %v", err)
	}
	requested := campaign.Experience{PersonaPack: "other-voices", MechanicsPack: "other-rules"}
	_, err := gate.Claim(t.Context(), requested)
	if !errors.Is(err, campaign.ErrExperienceMismatch) {
		t.Fatalf("mismatch error = %v, want ErrExperienceMismatch", err)
	}
	for _, want := range []string{
		testExperience.PersonaPack, testExperience.MechanicsPack,
		requested.PersonaPack, requested.MechanicsPack,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("mismatch error %q does not name %q", err, want)
		}
	}
}

func TestGateClaim_RefusesMissingPartialAmbiguousAndMalformedExperienceProvenance(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*graph.EntityState)
		wantError error
		wantText  string
	}{
		{
			name: "pre-provenance campaign",
			mutate: func(state *graph.EntityState) {
				state.Triples = removeExperienceTriples(state.Triples)
			},
			wantError: campaign.ErrExperienceMigrationRequired,
			wantText:  "migration",
		},
		{
			name: "partial provenance",
			mutate: func(state *graph.EntityState) {
				state.Triples = removePredicate(state.Triples, vocabulary.CampaignExperienceMechanicsPack.String())
			},
			wantText: "partial",
		},
		{
			name: "ambiguous provenance",
			mutate: func(state *graph.EntityState) {
				state.Triples = append(state.Triples, message.Triple{
					Subject: testCampaignID, Predicate: vocabulary.CampaignExperiencePersonaPack.String(), Object: "other",
				})
			},
			wantText: "2 values",
		},
		{
			name: "wrong object type",
			mutate: func(state *graph.EntityState) {
				setPredicateObject(state, vocabulary.CampaignExperiencePersonaPack.String(), 42)
			},
			wantText: "want a string",
		},
		{
			name: "empty pack",
			mutate: func(state *graph.EntityState) {
				setPredicateObject(state, vocabulary.CampaignExperiencePersonaPack.String(), "")
			},
			wantText: "required",
		},
		{
			name: "invalid pack",
			mutate: func(state *graph.EntityState) {
				setPredicateObject(state, vocabulary.CampaignExperienceMechanicsPack.String(), "bad.pack")
			},
			wantText: "dot",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			gate := newTestGate(t, store)
			if _, err := gate.Claim(t.Context(), testExperience); err != nil {
				t.Fatalf("fresh Claim: %v", err)
			}
			state := store.entities[testCampaignID]
			tc.mutate(state)

			_, err := gate.Claim(t.Context(), testExperience)
			if err == nil {
				t.Fatal("Claim accepted corrupt experience provenance")
			}
			if tc.wantError != nil && !errors.Is(err, tc.wantError) {
				t.Fatalf("error = %v, want sentinel %v", err, tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error %q does not contain %q", err, tc.wantText)
			}
		})
	}
}

func TestGateClaim_ConcurrentClaimsRespectExperienceProvenance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		requested  [2]campaign.Experience
		wantErrors int
	}{
		{name: "same selection", requested: [2]campaign.Experience{testExperience, testExperience}},
		{
			name: "different selection",
			requested: [2]campaign.Experience{
				testExperience,
				{PersonaPack: "other-voices", MechanicsPack: "other-rules"},
			},
			wantErrors: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			gates := [2]*campaign.Gate{newTestGate(t, store), newTestGate(t, store)}
			claims := make([]campaign.Instantiation, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			start := make(chan struct{})
			ctx := t.Context()
			for i := range gates {
				wg.Add(1)
				go func(index int) {
					defer wg.Done()
					<-start
					claims[index], errs[index] = gates[index].Claim(ctx, tc.requested[index])
				}(i)
			}
			close(start)
			wg.Wait()

			errorCount := 0
			freshCount := 0
			for i, err := range errs {
				if err != nil {
					errorCount++
					if tc.wantErrors == 1 && !errors.Is(err, campaign.ErrExperienceMismatch) {
						t.Fatalf("claim %d error = %v, want mismatch", i, err)
					}
					continue
				}
				if claims[i].Fresh {
					freshCount++
				}
				if claims[i].Experience != tc.requested[i] {
					t.Fatalf("claim %d returned experience %+v, requested %+v", i, claims[i].Experience, tc.requested[i])
				}
			}
			if errorCount != tc.wantErrors || freshCount != 1 {
				t.Fatalf("errors=%d fresh=%d claims=%+v errs=%v", errorCount, freshCount, claims, errs)
			}
		})
	}
}

func TestGateClaim_DegradedCreateReturnsRequestedExperienceWithoutRetry(t *testing.T) {
	store := newFakeStore()
	store.degrade = true
	gate := newTestGate(t, store)
	claim, err := gate.Claim(t.Context(), testExperience)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claim.Fresh || claim.Experience != testExperience || store.createCall != 1 {
		t.Fatalf("degraded claim = %+v, creates=%d", claim, store.createCall)
	}
}

func triplesFor(state *graph.EntityState, predicate string) []message.Triple {
	var triples []message.Triple
	for _, triple := range state.Triples {
		if triple.Predicate == predicate {
			triples = append(triples, triple)
		}
	}
	return triples
}

func removeExperienceTriples(triples []message.Triple) []message.Triple {
	return removePredicate(
		removePredicate(triples, vocabulary.CampaignExperiencePersonaPack.String()),
		vocabulary.CampaignExperienceMechanicsPack.String(),
	)
}

func removePredicate(triples []message.Triple, predicate string) []message.Triple {
	var kept []message.Triple
	for _, triple := range triples {
		if triple.Predicate != predicate {
			kept = append(kept, triple)
		}
	}
	return kept
}

func setPredicateObject(state *graph.EntityState, predicate string, object any) {
	for i := range state.Triples {
		if state.Triples[i].Predicate == predicate {
			state.Triples[i].Object = object
			return
		}
	}
}
