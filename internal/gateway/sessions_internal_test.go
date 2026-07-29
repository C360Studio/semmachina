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
// It matters more here than it looks: the session table already has no bound, no
// expiry and no way to notice a connection that left (task 9.3 owns that), and a
// second structure with the same property would be a second thing to fix.
func TestSessions_TheDeliveryIndexDoesNotOutliveItsConnections(t *testing.T) {
	const player = "c360.semmachina.world1.starter.player.pat"
	table := newSessions()

	for i := range 100 {
		connID := "conn-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		table.put(&Session{
			PlayerID:   player,
			Connection: Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID},
		})
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
	table := newSessions()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 500 {
			connID := fmt.Sprintf("conn-%d", i%7)
			table.put(&Session{
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
	table := newSessions()
	conn := Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID}

	table.put(&Session{PlayerID: first, Connection: conn})
	table.put(&Session{PlayerID: second, Connection: conn})

	if _, present := table.byPlayerID[first]; present {
		t.Fatalf("the former player still has an index entry: %v", table.byPlayerID[first])
	}
	if got := len(table.byPlayerID[second]); got != 1 {
		t.Fatalf("the new player holds %d connections, want 1", got)
	}
}
