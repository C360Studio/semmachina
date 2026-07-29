package payload_test

import (
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	pointerPlayer = "acme.semmachina.starter-ns.starter.player.pat"
	pointerTurnID = "turn-ACTION1"
	pointerTurn   = "acme.semmachina.starter-ns.starter.turn.turn-ACTION1"
)

var pointerAt = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func validPointer() *payload.PlayerTurn {
	return &payload.PlayerTurn{
		PlayerID:     pointerPlayer,
		TurnID:       pointerTurnID,
		TurnEntityID: pointerTurn,
	}
}

func TestPlayerTurn_ProjectsOneSingleValuedPointerOntoThePlayer(t *testing.T) {
	triples, err := validPointer().Triples(pointerPlayer, "turn-recorder", pointerAt)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("projected %d triples, want exactly one; the pointer is the whole record", len(triples))
	}

	got := triples[0]
	if got.Subject != pointerPlayer {
		t.Errorf("subject = %q, want the PLAYER entity %q; a pointer on the turn answers nothing at ingress",
			got.Subject, pointerPlayer)
	}
	if got.Predicate != vocabulary.PlayerTurnCurrent.String() {
		t.Errorf("predicate = %q, want %q", got.Predicate, vocabulary.PlayerTurnCurrent)
	}
	if got.Object != pointerTurn {
		t.Errorf("object = %v, want the turn ENTITY id %q; the gateway's next act is to read it",
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

// The foreign-subject pairing is the whole reason this projection needed a new
// mode, so it gets the test the mode exists for: a pointer aimed at a player
// other than the one it is about is refused rather than written.
func TestPlayerTurn_RefusesAProjectionAimedAtAnotherPlayer(t *testing.T) {
	const bystander = "acme.semmachina.starter-ns.starter.player.alex"

	_, err := validPointer().Triples(bystander, "turn-recorder", pointerAt)
	if err == nil {
		t.Fatal("a pointer about one player was projected onto another; that grants a bystander a live turn " +
			"and silently takes it from its owner")
	}
	if !strings.Contains(err.Error(), bystander) || !strings.Contains(err.Error(), pointerPlayer) {
		t.Errorf("the refusal %q names neither the destination nor the claimed player", err)
	}
}

// The turn id and the turn entity id are one turn wearing two shapes. A pointer
// that disagreed with itself would have the gateway report one turn and read
// another's phase.
func TestPlayerTurn_RefusesAPointerThatDisagreesWithItself(t *testing.T) {
	pointer := validPointer()
	pointer.TurnEntityID = "acme.semmachina.starter-ns.starter.turn.turn-OTHER"

	if _, err := pointer.Triples(pointerPlayer, "turn-recorder", pointerAt); err == nil {
		t.Fatal("a pointer whose turn id and turn entity id name different turns was projected")
	}
}

func TestPlayerTurn_RefusesEveryMalformedIdentity(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*payload.PlayerTurn)
		wantSub string
	}{
		{
			name:    "no player",
			mutate:  func(p *payload.PlayerTurn) { p.PlayerID = "" },
			wantSub: "player_id",
		},
		{
			name:    "player is not a canonical entity id",
			mutate:  func(p *payload.PlayerTurn) { p.PlayerID = "pat" },
			wantSub: "player_id",
		},
		{
			name:    "player is a connection identifier",
			mutate:  func(p *payload.PlayerTurn) { p.PlayerID = "conn-7" },
			wantSub: "player_id",
		},
		{
			name:    "no turn id",
			mutate:  func(p *payload.PlayerTurn) { p.TurnID = "" },
			wantSub: "turn_id",
		},
		{
			name:    "turn entity id is not canonical",
			mutate:  func(p *payload.PlayerTurn) { p.TurnEntityID = "turn-ACTION1" },
			wantSub: "turn entity id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pointer := validPointer()
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

// A projection that forgets to declare its player subject must fall back to the
// STRICT turn pairing and fail loudly, never to an unchecked subject. The
// projection is unexported, so the property is proved where it is observable:
// the player entity id is not a turn entity id, so the default pairing refuses
// it — which is exactly what a forgotten declaration would run into.
func TestTripleProjection_DefaultSubjectPairingRefusesAPlayerEntity(t *testing.T) {
	state := &payload.TurnState{
		TurnID: pointerTurnID,
		Phase:  vocabulary.PhaseAdjudicating,
	}
	_, err := state.Triples(pointerPlayer, "turn-recorder", pointerAt)
	if err == nil {
		t.Fatal("a turn-state projection landed on a player entity; the default pairing is not enforced")
	}
	if !strings.Contains(err.Error(), "turn") {
		t.Errorf("the refusal %q does not explain that the subject is not this turn's entity", err)
	}
}

func TestPlayerTurn_SurvivesARoundTrip(t *testing.T) {
	pointer := validPointer()
	data, err := pointer.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var back payload.PlayerTurn
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

func TestPlayerTurn_SchemaNamesTheUnregisteredCategory(t *testing.T) {
	schema := validPointer().Schema()
	if schema.Category != payload.CategoryPlayerTurn {
		t.Errorf("Schema().Category = %q, want %q", schema.Category, payload.CategoryPlayerTurn)
	}
	if schema.Domain != payload.Domain || schema.Version != payload.SchemaVersion {
		t.Errorf("Schema() = %+v, want the SemMachina payload coordinates", schema)
	}
}

// The projection must still refuse a source or timestamp it cannot stamp; those
// gates are shared, and a new mode that skipped them would be a hole.
func TestPlayerTurn_RefusesAnUnstampableProjection(t *testing.T) {
	if _, err := validPointer().Triples(pointerPlayer, "", pointerAt); err == nil {
		t.Error("a projection with no source was accepted")
	}
	if _, err := validPointer().Triples(pointerPlayer, "turn-recorder", time.Time{}); err == nil {
		t.Error("a projection with no timestamp was accepted")
	}
	if _, err := (&payload.PlayerTurn{}).Triples(pointerPlayer, "turn-recorder", pointerAt); err == nil {
		t.Error("an empty pointer was projected")
	}
}
