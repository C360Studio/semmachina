package gateway

import (
	"fmt"
	"sync"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The delivery index's own hygiene, which is invisible from outside the package
// and is exactly the kind of leak an external test cannot see.
//
// forPlayer cross-checks byConnID before it returns anything, so a stale entry in
// the player index produces the RIGHT answer and grows the table forever. That
// makes the leak a pure resource defect with no behavioural symptom — the shape
// that survives every black-box test and is found in production as memory.
//
// It matters more here than it looks: the per-player cap bounds how many
// connections one player may hold AT ONCE, and says nothing about how many the
// table remembers after they leave. A leak in the player index would grow past
// that cap forever with the cap still reporting itself satisfied.
func TestSessions_TheDeliveryIndexDoesNotOutliveItsConnections(t *testing.T) {
	const player = "c360.semmachina.world1.starter.player.pat"
	table := newSessions(DefaultMaxConnectionsPerPlayer)

	for i := range 100 {
		connID := "conn-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		if err := table.put(&Session{
			PlayerID:   player,
			Connection: Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID},
		}); err != nil {
			t.Fatalf("put %s: %v", connID, err)
		}
		table.remove(connID)
	}

	if len(table.byConnID) != 0 {
		t.Fatalf("the connection index holds %d entries after every socket left", len(table.byConnID))
	}
	if len(table.byPlayerID) != 0 {
		t.Fatalf("the delivery index holds %d player entries after every socket left; forPlayer cross-checks "+
			"byConnID, so the stale entries answer correctly and accumulate silently", len(table.byPlayerID))
	}
}

// The table is now read by a DIFFERENT goroutine than the one that writes it:
// the transport authenticates and disconnects while the egress path resolves
// delivery targets off a durable consumer's callback. That crossing is new with
// the player index, so it gets a test rather than a claim — run under -race, it
// is what says the two-map update is atomic against a concurrent lookup.
func TestSessions_TheDeliveryIndexIsSafeAcrossTheTransportAndEgressGoroutines(t *testing.T) {
	const player = "c360.semmachina.world1.starter.player.pat"
	// Seven connection ids against a cap of seven: the writer exercises every
	// branch of put, including the refusal, while the reader is in the table.
	table := newSessions(7)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 500 {
			connID := fmt.Sprintf("conn-%d", i%7)
			_ = table.put(&Session{
				PlayerID:   player,
				Connection: Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID},
			})
			if i%3 == 0 {
				table.remove(connID)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 500 {
			for _, session := range table.forPlayer(player) {
				// Read what a delivery reads. A lookup that returned a pointer
				// into the live table would race here rather than merely look
				// unsafe.
				if session.PlayerID != player {
					t.Errorf("a lookup answered with player %q", session.PlayerID)
					return
				}
			}
		}
	}()
	wg.Wait()
}

// The same hygiene on the rebind path: a connection that re-authenticates as a
// different player must not leave a residue on the first one.
func TestSessions_RebindingLeavesNoResidueOnTheFormerPlayer(t *testing.T) {
	const (
		first  = "c360.semmachina.world1.starter.player.pat"
		second = "c360.semmachina.world1.starter.player.alex"
		connID = "conn-7"
	)
	table := newSessions(DefaultMaxConnectionsPerPlayer)
	conn := Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID}

	if err := table.put(&Session{PlayerID: first, Connection: conn}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := table.put(&Session{PlayerID: second, Connection: conn}); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	if _, present := table.byPlayerID[first]; present {
		t.Fatalf("the former player still has an index entry: %v", table.byPlayerID[first])
	}
	if got := len(table.byPlayerID[second]); got != 1 {
		t.Fatalf("the new player holds %d connections, want 1", got)
	}
}

// The table's whole bound, stated as an observation rather than as a doc claim:
// (roster size × cap), holding under a client that never stops connecting.
//
// This is what stands in for the session TTL this design refuses to have. Nothing
// here expires for silence — a player at email cadence keeps their seat for as
// long as their socket lives — and the table still cannot grow, because only a
// roster player can be bound and each of them has a fixed number of seats.
//
// A refusal that left the connection index unbound-but-present would fail this
// while every black-box test still passed: forPlayer cross-checks byConnID, so
// the residue answers correctly and accumulates.
func TestSessions_CannotGrowPastTheRosterTimesTheCap(t *testing.T) {
	const cap_ = 2
	players := []string{
		"c360.semmachina.world1.starter.player.pat",
		"c360.semmachina.world1.starter.player.alex",
		"c360.semmachina.world1.starter.player.robin",
	}
	table := newSessions(cap_)

	refused := 0
	for attempt := range 200 {
		player := players[attempt%len(players)]
		connID := fmt.Sprintf("conn-%d", attempt)
		if err := table.put(&Session{
			PlayerID:   player,
			Connection: Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID},
		}); err != nil {
			refused++
		}
	}

	if refused == 0 {
		t.Fatal("200 connections for three players were all admitted, so the bound below proves nothing")
	}
	if want := len(players) * cap_; len(table.byConnID) != want {
		t.Fatalf("the connection index holds %d entries after 200 attempts, want at most %d — the table's "+
			"bound is (roster size × cap) and it is what stands in for a session TTL", len(table.byConnID), want)
	}
	if len(table.byPlayerID) != len(players) {
		t.Fatalf("the delivery index holds %d player entries, want %d", len(table.byPlayerID), len(players))
	}
	for player, connections := range table.byPlayerID {
		if len(connections) > cap_ {
			t.Fatalf("player %s holds %d connections, past the cap of %d", player, len(connections), cap_)
		}
	}
}
