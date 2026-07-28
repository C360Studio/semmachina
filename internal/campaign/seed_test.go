package campaign_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/campaign"
)

func TestNewSeed_ProducesDistinctNonZeroSeeds(t *testing.T) {
	const draws = 64
	seen := make(map[campaign.Seed]bool, draws)
	for range draws {
		seed, err := campaign.NewSeed()
		if err != nil {
			t.Fatalf("NewSeed: %v", err)
		}
		if seed.IsZero() {
			t.Fatal("NewSeed produced the zero seed")
		}
		if seen[seed] {
			t.Fatalf("NewSeed repeated a seed after %d draws; campaign seeds must not collide", len(seen))
		}
		seen[seed] = true
	}
}

// The seed round-trips through the exact form it is stored in on the campaign
// entity — if hex encoding and parsing disagreed, a restart would read back a
// different seed and every recorded roll would become unreproducible.
func TestSeed_RoundTripsThroughItsStoredForm(t *testing.T) {
	seed, err := campaign.NewSeed()
	if err != nil {
		t.Fatalf("NewSeed: %v", err)
	}

	stored := seed.String()
	if len(stored) != campaign.SeedBytes*2 {
		t.Fatalf("stored seed is %d hex characters, want %d", len(stored), campaign.SeedBytes*2)
	}

	restored, err := campaign.ParseSeed(stored)
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}
	if restored != seed {
		t.Fatal("the seed did not survive the round trip through its stored form")
	}
}

func TestParseSeed_RejectsAnythingThatIsNotASeed(t *testing.T) {
	valid, err := campaign.NewSeed()
	if err != nil {
		t.Fatalf("NewSeed: %v", err)
	}

	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "empty", value: "", wantErr: "empty"},
		{name: "not hex", value: strings.Repeat("z", campaign.SeedBytes*2), wantErr: "not hex"},
		{name: "too short", value: valid.String()[:campaign.SeedBytes], wantErr: "bytes"},
		{name: "too long", value: valid.String() + "ab", wantErr: "bytes"},
		{
			// The value a forgotten generation leaves behind. Rolling from it
			// is perfectly deterministic and completely wrong.
			name:    "all zeroes",
			value:   strings.Repeat("0", campaign.SeedBytes*2),
			wantErr: "zeroes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seed, err := campaign.ParseSeed(tc.value)
			if err == nil {
				t.Fatalf("ParseSeed accepted %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
			if !seed.IsZero() {
				t.Fatal("a refused parse returned seed material")
			}
		})
	}
}

// Bytes hands out a copy: a caller that mutated the returned slice would
// otherwise silently re-seed the campaign a Gate is holding.
func TestSeed_BytesIsACopy(t *testing.T) {
	seed, err := campaign.NewSeed()
	if err != nil {
		t.Fatalf("NewSeed: %v", err)
	}

	raw := seed.Bytes()
	for idx := range raw {
		raw[idx] = 0
	}
	if seed.IsZero() {
		t.Fatal("mutating the returned slice zeroed the seed itself")
	}
	if seed.String() == strings.Repeat("00", campaign.SeedBytes) {
		t.Fatal("the seed was mutated through its byte view")
	}
}
