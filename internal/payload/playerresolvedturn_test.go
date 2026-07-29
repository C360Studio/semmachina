package payload_test

import (
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func validResolvedPointer() *payload.PlayerResolvedTurn {
	return &payload.PlayerResolvedTurn{
		PlayerID:     pointerPlayer,
		TurnID:       pointerTurnID,
		TurnEntityID: pointerTurn,
	}
}

func TestPlayerResolvedTurn_ProjectsOntoTheResolvedPredicateAndNotTheCurrentOne(t *testing.T) {
	triples, err := validResolvedPointer().Triples(pointerPlayer, "turn-recorder", pointerAt)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("projected %d triples, want exactly one; the pointer is the whole record", len(triples))
	}

	got := triples[0]
	if got.Predicate != vocabulary.PlayerTurnResolved.String() {
		t.Errorf("predicate = %q, want %q", got.Predicate, vocabulary.PlayerTurnResolved)
	}
	// The load-bearing negative. These two pointers answer different questions on
	// one entity, and a resolved-turn write that landed on the CURRENT predicate
	// would drag a player off a turn they are still running — the admission gate
	// would then read a terminal turn and admit a second action.
	if got.Predicate == vocabulary.PlayerTurnCurrent.String() {
		t.Fatal("the resolved pointer projected onto player.turn.current; that moves a player off a live turn")
	}
	if got.Subject != pointerPlayer {
		t.Errorf("subject = %q, want the PLAYER entity %q", got.Subject, pointerPlayer)
	}
	if got.Object != pointerTurn {
		t.Errorf("object = %v, want the turn ENTITY id %q; retrieval's next act is to read it",
			got.Object, pointerTurn)
	}
	if got.Context != pointerTurnID {
		t.Errorf("context = %q, want the turn id %q", got.Context, pointerTurnID)
	}
	if !got.Timestamp.Equal(pointerAt) {
		t.Errorf("timestamp = %s, want the supplied %s", got.Timestamp, pointerAt)
	}
	if got.Source != "turn-recorder" {
		t.Errorf("source = %q, want the producing component", got.Source)
	}
}

// The foreign-subject pairing matters more here than on the current pointer, not
// less: a resolved pointer landed on a bystander tells that player their last
// turn was somebody else's, and retrieval would then serve them another player's
// narration.
func TestPlayerResolvedTurn_RefusesAProjectionAimedAtAnotherPlayer(t *testing.T) {
	const bystander = "acme.semmachina.starter-ns.starter.player.alex"

	_, err := validResolvedPointer().Triples(bystander, "turn-recorder", pointerAt)
	if err == nil {
		t.Fatal("a resolved pointer about one player was projected onto another; retrieval would then serve " +
			"that player somebody else's turn")
	}
	if !strings.Contains(err.Error(), bystander) || !strings.Contains(err.Error(), pointerPlayer) {
		t.Errorf("the refusal %q names neither the destination nor the claimed player", err)
	}
}

func TestPlayerResolvedTurn_RefusesAPointerThatDisagreesWithItself(t *testing.T) {
	pointer := validResolvedPointer()
	pointer.TurnEntityID = "acme.semmachina.starter-ns.starter.turn.turn-OTHER"

	if _, err := pointer.Triples(pointerPlayer, "turn-recorder", pointerAt); err == nil {
		t.Fatal("a pointer whose turn id and turn entity id name different turns was projected")
	}
}

func TestPlayerResolvedTurn_RefusesEveryMalformedIdentity(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*payload.PlayerResolvedTurn)
		wantSub string
	}{
		{
			name:    "no player",
			mutate:  func(p *payload.PlayerResolvedTurn) { p.PlayerID = "" },
			wantSub: "player_id",
		},
		{
			name:    "player is a connection identifier",
			mutate:  func(p *payload.PlayerResolvedTurn) { p.PlayerID = "conn-7" },
			wantSub: "player_id",
		},
		{
			name:    "no turn id",
			mutate:  func(p *payload.PlayerResolvedTurn) { p.TurnID = "" },
			wantSub: "turn_id",
		},
		{
			name:    "turn entity id is not canonical",
			mutate:  func(p *payload.PlayerResolvedTurn) { p.TurnEntityID = "turn-ACTION1" },
			wantSub: "turn entity id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pointer := validResolvedPointer()
			test.mutate(pointer)

			if err := pointer.Validate(); err == nil {
				t.Fatal("Validate accepted it")
			} else if !strings.Contains(err.Error(), test.wantSub) {
				t.Errorf("Validate said %q, which does not name %q", err, test.wantSub)
			}
			if _, err := pointer.Triples(pointer.PlayerID, "turn-recorder", pointerAt); err == nil {
				t.Fatal("Triples projected it; validation must run before anything reaches the graph")
			}
		})
	}
}

func TestPlayerResolvedTurn_SurvivesARoundTrip(t *testing.T) {
	pointer := validResolvedPointer()
	data, err := pointer.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var back payload.PlayerResolvedTurn
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if back != *pointer {
		t.Errorf("round trip produced %+v, want %+v", back, *pointer)
	}
	if err := back.Validate(); err != nil {
		t.Errorf("a round-tripped pointer no longer validates: %v", err)
	}
}

func TestPlayerResolvedTurn_SchemaNamesItsOwnUnregisteredCategory(t *testing.T) {
	schema := validResolvedPointer().Schema()
	if schema.Category != payload.CategoryPlayerResolvedTurn {
		t.Errorf("Schema().Category = %q, want %q", schema.Category, payload.CategoryPlayerResolvedTurn)
	}
	// Two pointers on one entity written by one component are exactly the pair
	// most likely to be given one set of coordinates by accident.
	if schema.Category == payload.CategoryPlayerTurn {
		t.Error("the resolved pointer shares the current pointer's category; two payloads under one " +
			"(domain, category, version) is a collision the registry cannot report")
	}
	if schema.Domain != payload.Domain || schema.Version != payload.SchemaVersion {
		t.Errorf("Schema() = %+v, want the SemMachina payload coordinates", schema)
	}
}

func TestPlayerResolvedTurn_RefusesAnUnstampableProjection(t *testing.T) {
	if _, err := validResolvedPointer().Triples(pointerPlayer, "", pointerAt); err == nil {
		t.Error("a projection with no source was accepted")
	}
	if _, err := validResolvedPointer().Triples(pointerPlayer, "turn-recorder", time.Time{}); err == nil {
		t.Error("a projection with no timestamp was accepted")
	}
	if _, err := (&payload.PlayerResolvedTurn{}).Triples(pointerPlayer, "turn-recorder", pointerAt); err == nil {
		t.Error("an empty pointer was projected")
	}
}
