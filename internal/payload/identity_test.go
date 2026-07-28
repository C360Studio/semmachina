package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// testTurnEntityPrefix is the five leading segments of the turn entity ID the
// starter world composes; the sixth is the turn id. The order is
// org.platform.domain.system.type.instance, so the WORLD NAMESPACE (world1)
// sits in the domain slot and the TEMPLATE (starter) in the system slot.
const testTurnEntityPrefix = "c360.semmachina.world1.starter.turn."

// action_id arrives from an ingress adapter — a Slack ts, an email
// Message-ID — and TurnIDForAction turns it into the instance segment of the
// turn entity's six-part ID. These are the values that would compose an ID the
// graph refuses, deterministically, after intake already committed to the
// message: a poison-message redelivery loop on the durable consumer.
func TestPlayerAction_RejectsActionIDsThatCannotBecomeAnEntityIDSegment(t *testing.T) {
	// Each of these composes a turn entity ID the graph refuses, so every
	// case is checked twice: the payload rejects it, AND the rejection is
	// justified by the failure it prevents.
	composesAnIllegalEntityID := []struct {
		name     string
		actionID string
	}{
		{name: "a dot adds entity-ID segments", actionID: "a.b"},
		{name: "a trailing dot adds an empty segment", actionID: "act1."},
		{name: "a slash is outside the segment alphabet", actionID: "act/1"},
		{name: "a space is outside the segment alphabet", actionID: "act 1"},
		{name: "a colon is outside the segment alphabet", actionID: "slack:1234"},
		{name: "an at sign is outside the segment alphabet", actionID: "msg@example.com"},
		{name: "a wildcard would match an ID pattern", actionID: "*"},
		{name: "300 bytes blows the whole entity-ID limit", actionID: strings.Repeat("a", 300)},
	}

	for _, tc := range composesAnIllegalEntityID {
		t.Run(tc.name, func(t *testing.T) {
			action := validPlayerAction()
			action.ActionID = tc.actionID

			decoded := decode(t, publishUnvalidated(t, action))
			err := decoded.Validate()
			if err == nil {
				t.Fatalf("action_id %q was accepted; it composes the turn entity ID %q",
					tc.actionID, testTurnEntityPrefix+payload.TurnIDForAction(tc.actionID))
			}
			if !strings.Contains(err.Error(), "action_id") {
				t.Fatalf("rejection reason %q does not name action_id", err.Error())
			}

			composed := testTurnEntityPrefix + payload.TurnIDForAction(tc.actionID)
			if err := types.ValidateEntityID(composed); err == nil {
				t.Fatalf("action_id %q was rejected but composes a legal entity ID %q", tc.actionID, composed)
			}
		})
	}

	// These two do NOT compose an illegal ID today, because TurnIDForAction's
	// "turn-" prefix happens to supply the alphanumeric first byte the segment
	// alphabet requires. They are still rejected: action_id is validated as a
	// segment on its own terms, not on the accident of what a derivation
	// prepends. A future composition that used action_id directly — a
	// per-action entity, a subject built from it — would otherwise inherit an
	// illegal segment from a value the boundary had already blessed.
	maskedByTheDerivedPrefix := []struct {
		name     string
		actionID string
	}{
		{name: "a leading dash is not a legal first byte", actionID: "-act1"},
		{name: "a leading underscore is not a legal first byte", actionID: "_act1"},
	}

	for _, tc := range maskedByTheDerivedPrefix {
		t.Run(tc.name, func(t *testing.T) {
			if err := types.ValidateEntityID(testTurnEntityPrefix + payload.TurnIDForAction(tc.actionID)); err != nil {
				t.Fatalf("case %q is no longer masked by the derived prefix; move it to the composed table", tc.name)
			}
			if err := types.ValidateEntityIDPrefix(tc.actionID); err == nil {
				t.Fatalf("action_id %q is a legal segment after all; this case proves nothing", tc.actionID)
			}

			action := validPlayerAction()
			action.ActionID = tc.actionID
			if err := action.Validate(); err == nil {
				t.Fatalf("action_id %q was accepted; it is not a legal entity-ID segment", tc.actionID)
			}
		})
	}

	// The length bound is a conservative RESERVE, not the exact upstream
	// failure point: the payload boundary cannot see the world package's
	// five leading segments, so it reserves half the budget for them. An id
	// just past the reserve composes a legal ID under the short starter
	// prefix and an illegal one under a long world prefix — which is exactly
	// why the bound is drawn where the information is missing.
	t.Run("one byte past the reserved segment budget", func(t *testing.T) {
		justOver := strings.Repeat("a", payload.MaxActionIDBytes+1)

		action := validPlayerAction()
		action.ActionID = justOver
		err := action.Validate()
		if err == nil {
			t.Fatal("an action_id past the reserved budget was accepted")
		}
		if !strings.Contains(err.Error(), "action_id") {
			t.Fatalf("rejection reason %q does not name action_id", err.Error())
		}
	})
}

// Segment-legal ids stay accepted: the alphabet permits uppercase and
// underscores that predicates do not (F9), and an adapter deriving an id from
// a channel message must not be rejected for using them.
func TestPlayerAction_AcceptsEveryEntityIDSegmentLegalActionID(t *testing.T) {
	for _, actionID := range []string{
		"act-1",
		"1",
		"a",
		"A1",
		"slack_1727291234_000200",
		"WS-Conn7_msg-42",
		strings.Repeat("a", payload.MaxActionIDBytes),
	} {
		t.Run(actionID, func(t *testing.T) {
			action := validPlayerAction()
			action.ActionID = actionID

			if err := action.Validate(); err != nil {
				t.Fatalf("a segment-legal action_id was rejected: %v", err)
			}
			composed := testTurnEntityPrefix + payload.TurnIDForAction(actionID)
			if err := types.ValidateEntityID(composed); err != nil {
				t.Fatalf("accepted action_id %q composes an entity ID the graph refuses: %v", actionID, err)
			}
		})
	}
}

// The length bound is stated against the upstream contract rather than
// against itself: the longest accepted action id, under a five-segment prefix
// that exactly fills the reserved budget, must compose an ID of exactly
// types.MaxEntityIDBytes that upstream accepts.
func TestActionIDBound_LeavesTheComposedEntityIDInsideTheUpstreamLimit(t *testing.T) {
	prefixBudget := types.MaxEntityIDBytes - vocabulary.MaxIDSegmentBytes
	if prefixBudget < 12 {
		t.Fatalf("the reserved prefix budget of %d bytes cannot hold five segments", prefixBudget)
	}

	// Five segments plus their four separators plus the one joining the
	// instance segment fill the reserved budget exactly.
	const segments = 6
	body := prefixBudget - 1 - (segments - 2)
	prefix := make([]string, 0, segments-1)
	for i := range segments - 1 {
		size := body / (segments - 1)
		if i == 0 {
			size += body % (segments - 1)
		}
		prefix = append(prefix, strings.Repeat("w", size))
	}

	longest := strings.Repeat("a", payload.MaxActionIDBytes)
	composed := strings.Join(prefix, ".") + "." + payload.TurnIDForAction(longest)

	if len(composed) != types.MaxEntityIDBytes {
		t.Fatalf("the worst-case composed ID is %d bytes, want exactly %d (the test is not testing the boundary)",
			len(composed), types.MaxEntityIDBytes)
	}
	if err := types.ValidateEntityID(composed); err != nil {
		t.Fatalf("the longest accepted action_id composes an entity ID the graph refuses: %v", err)
	}

	action := validPlayerAction()
	action.ActionID = longest
	if err := action.Validate(); err != nil {
		t.Fatalf("the longest action_id inside the budget was rejected: %v", err)
	}

	action.ActionID = longest + "a"
	if err := action.Validate(); err == nil {
		t.Fatal("one byte past the budget was accepted")
	}
}

// action_id is bounded tighter than turn_id by exactly the derived prefix, so
// an id cannot pass intake and then fail on the very next payload — that would
// move the poison message one hop downstream instead of removing it.
func TestActionIDBound_LeavesRoomForTheDerivedTurnID(t *testing.T) {
	longest := strings.Repeat("a", payload.MaxActionIDBytes)
	derived := payload.TurnIDForAction(longest)

	if len(derived) != vocabulary.MaxIDSegmentBytes {
		t.Fatalf("the derived turn id is %d bytes, want exactly the %d-byte segment budget",
			len(derived), vocabulary.MaxIDSegmentBytes)
	}

	action := validPlayerAction()
	action.ActionID = longest
	if err := action.Validate(); err != nil {
		t.Fatalf("intake rejected the longest action_id: %v", err)
	}

	// The verdict is the next payload to carry the derived turn id.
	verdict := validVerdict()
	verdict.ActionID = longest
	verdict.TurnID = derived
	if err := verdict.Validate(); err != nil {
		t.Fatalf("a turn id derived from an accepted action_id was rejected downstream: %v", err)
	}
}

// Every payload carrying a turn or action id must gate it, because any of them
// can be the first to reach the graph with it. A hole in one is a hole in the
// contract.
func TestPayloads_GateEveryTurnAndActionIdentity(t *testing.T) {
	const dotted = "a.b"
	overlong := strings.Repeat("a", vocabulary.MaxIDSegmentBytes+1)

	cases := []struct {
		name    string
		build   func(id string) message.Payload
		wantErr string
		// ids the case is exercised with; turn ids get the segment budget,
		// action ids the tighter derived-turn budget.
		overlong string
	}{
		{
			name: "player_action action_id",
			build: func(id string) message.Payload {
				p := validPlayerAction()
				p.ActionID = id
				return p
			},
			wantErr:  "action_id",
			overlong: strings.Repeat("a", payload.MaxActionIDBytes+1),
		},
		{
			name: "verdict turn_id",
			build: func(id string) message.Payload {
				v := validVerdict()
				v.TurnID = id
				return v
			},
			wantErr:  "turn_id",
			overlong: overlong,
		},
		{
			name: "verdict action_id",
			build: func(id string) message.Payload {
				v := validVerdict()
				v.ActionID = id
				return v
			},
			wantErr:  "action_id",
			overlong: strings.Repeat("a", payload.MaxActionIDBytes+1),
		},
		{
			name: "roll_result turn_id",
			build: func(id string) message.Payload {
				r := validRollResult()
				r.TurnID = id
				r.Seed.TurnID = id
				return r
			},
			wantErr:  "turn_id",
			overlong: overlong,
		},
		{
			name: "effect_batch turn_id",
			build: func(id string) message.Payload {
				return payload.NewEffectBatch(id, vocabulary.BandPartial, nil)
			},
			wantErr:  "turn_id",
			overlong: overlong,
		},
		{
			name: "turn_manifest turn_id",
			build: func(id string) message.Payload {
				m := validTurnManifest()
				m.TurnID = id
				return m
			},
			wantErr:  "turn_id",
			overlong: overlong,
		},
		{
			name: "turn_manifest action_id",
			build: func(id string) message.Payload {
				m := validTurnManifest()
				m.ActionID = id
				return m
			},
			wantErr:  "action_id",
			overlong: strings.Repeat("a", payload.MaxActionIDBytes+1),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			malformed := []struct{ label, id string }{
				{label: "dotted", id: dotted},
				{label: "overlong", id: tc.overlong},
			}
			for _, m := range malformed {
				label, id := m.label, m.id
				t.Run(label, func(t *testing.T) {
					decoded := decode(t, publishUnvalidated(t, tc.build(id)))
					err := decoded.Validate()
					if err == nil {
						t.Fatalf("a %s identifier reached the graph through %s", label, tc.name)
					}
					if !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("rejection reason %q does not name %s", err.Error(), tc.wantErr)
					}
				})
			}
		})
	}
}

// The action text feeds the adjudicator prompt, so its bound is a spend bound.
// Unbounded, the first thing to stop a pasted novel is NATS's 1MB max payload:
// a transport failure, far from the boundary that owns the contract.
func TestPlayerAction_BoundsActionTextAtTheContractBoundary(t *testing.T) {
	action := validPlayerAction()
	action.Text = strings.Repeat("x", payload.MaxActionTextBytes)
	if err := action.Validate(); err != nil {
		t.Fatalf("action text at the bound was rejected: %v", err)
	}

	action.Text = strings.Repeat("x", payload.MaxActionTextBytes+1)
	decoded := decode(t, publishUnvalidated(t, action))
	err := decoded.Validate()
	if err == nil {
		t.Fatal("unbounded action text was accepted; unbounded text is unbounded adjudicator spend")
	}
	if !strings.Contains(err.Error(), "text") {
		t.Fatalf("rejection reason %q does not name text", err.Error())
	}

	// The bound is stated in bytes because spend and the transport limit both
	// are; a multi-byte-rune action is measured the same way.
	action.Text = strings.Repeat("é", payload.MaxActionTextBytes)
	if err := action.Validate(); err == nil {
		t.Fatal("a text of MaxActionTextBytes multi-byte runes was accepted; the bound is not counting bytes")
	}
}

// turn_id IS the instance segment of the turn entity's six-part ID. Any stage
// handed both as independent arguments can be wired to pair turn A's entity
// with turn B's payload, and every guard downstream reads exactly one of them:
// the applier's idempotency marker lands on the entity while its identity is
// derived from the payload, so a mismatch commits real world changes, poisons
// the entity it wrote to (a later legitimate batch derives a different id and
// is refused as "already records batch X"), and leaves the other turn unmarked.
func TestRequireTurnEntityID_BindsTheEntityToTheTurnItAddresses(t *testing.T) {
	const turnID = "turn-act-1"
	entityID := testTurnEntityPrefix + turnID

	if err := payload.RequireTurnEntityID(turnID, entityID); err != nil {
		t.Fatalf("the entity addressing its own turn was refused: %v", err)
	}

	cases := map[string]struct {
		turnID   string
		entityID string
		names    string
	}{
		"another turn's entity": {
			turnID:   turnID,
			entityID: testTurnEntityPrefix + "turn-act-9",
			names:    "turn-act-9",
		},
		"a turn entity from another world": {
			// Same instance segment, different namespace: still the wrong
			// entity, and the segment comparison alone would pass it.
			turnID:   turnID,
			entityID: "c360.semmachina.world2.starter.turn." + turnID,
			names:    "",
		},
		"an id that is not six parts": {
			turnID:   turnID,
			entityID: "not-an-entity",
			names:    "canonical",
		},
		"an empty turn id": {
			turnID:   "",
			entityID: entityID,
			names:    "turn_id",
		},
		"a dotted turn id": {
			turnID:   "a.b",
			entityID: entityID,
			names:    "turn_id",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := payload.RequireTurnEntityID(tc.turnID, tc.entityID)
			if name == "a turn entity from another world" {
				// Documented limitation, asserted rather than assumed: the
				// binding is over the instance segment, so a same-named turn in
				// another world namespace is indistinguishable here. Campaign
				// scoping is resolved at the process boundary (instance-per-
				// world), which is what makes that acceptable.
				if err != nil {
					t.Fatalf("the segment binding rejected a same-turn id from another namespace: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a turn entity that does not address this turn was accepted")
			}
			if tc.names != "" && !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection reason %q does not name %q", err, tc.names)
			}
		})
	}
}

// The binding is the derivation, so it must hold for every action id the
// intake boundary accepts — not just for the shapes a test happens to pick.
func TestRequireTurnEntityID_HoldsForEveryDerivedTurnID(t *testing.T) {
	for _, actionID := range []string{"a", "1699999999-000100", "CAFE_beef", "msg-42"} {
		turnID := payload.TurnIDForAction(actionID)
		if err := payload.RequireTurnEntityID(turnID, testTurnEntityPrefix+turnID); err != nil {
			t.Fatalf("action %q derives turn %q, whose own entity id was refused: %v", actionID, turnID, err)
		}
	}
}
