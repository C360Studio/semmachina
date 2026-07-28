package payload_test

import (
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func time0() time.Time { return time.Time{} }

func TestPlayerAction_ValidFixtureIsAccepted(t *testing.T) {
	if err := validPlayerAction().Validate(); err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}
}

func TestPlayerAction_RejectsActionsMissingTheIdentityContract(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*payload.PlayerAction)
		wantErr string
	}{
		{
			name:    "player identified by a connection instead of an entity",
			mutate:  func(a *payload.PlayerAction) { a.PlayerID = "ws-conn-7" },
			wantErr: "player_id",
		},
		{
			name:    "player id missing",
			mutate:  func(a *payload.PlayerAction) { a.PlayerID = "" },
			wantErr: "player_id",
		},
		{
			name:    "campaign is not a canonical entity id",
			mutate:  func(a *payload.PlayerAction) { a.CampaignID = "main" },
			wantErr: "campaign_id",
		},
		{
			name:    "scene is not a canonical entity id",
			mutate:  func(a *payload.PlayerAction) { a.SceneID = "gatehouse" },
			wantErr: "scene_id",
		},
		{
			name:    "action id missing",
			mutate:  func(a *payload.PlayerAction) { a.ActionID = "" },
			wantErr: "action_id",
		},
		{
			name:    "no action text",
			mutate:  func(a *payload.PlayerAction) { a.Text = "" },
			wantErr: "text",
		},
		{
			name:    "no arrival timestamp",
			mutate:  func(a *payload.PlayerAction) { a.ArrivedAt = time0() },
			wantErr: "arrived_at",
		},
		{
			name:    "adapter outside the registered set",
			mutate:  func(a *payload.PlayerAction) { a.Channel.Adapter = "slack" },
			wantErr: "channel_adapter",
		},
		{
			name:    "no reply address",
			mutate:  func(a *payload.PlayerAction) { a.Channel.ReplyTo = "" },
			wantErr: "reply_to",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := validPlayerAction()
			tc.mutate(action)

			decoded := decode(t, publishUnvalidated(t, action))
			err := decoded.Validate()
			if err == nil {
				t.Fatal("Validate accepted an action that breaks the identity contract")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// Identity is a graph entity, not a connection. The same player reconnecting
// on a different socket submits the same player_id with a different reply-to,
// and the engine must see one player.
func TestPlayerAction_IdentitySurvivesAReconnectWhileTheBindingChanges(t *testing.T) {
	first := validPlayerAction()
	first.Channel.ReplyTo = "ws-conn-7"

	second := validPlayerAction()
	second.ActionID = "act-2"
	second.Channel.ReplyTo = "ws-conn-99"

	decodedFirst, ok := decode(t, publish(t, first)).(*payload.PlayerAction)
	if !ok {
		t.Fatal("decoder produced the wrong type")
	}
	decodedSecond, ok := decode(t, publish(t, second)).(*payload.PlayerAction)
	if !ok {
		t.Fatal("decoder produced the wrong type")
	}

	if decodedFirst.PlayerID != decodedSecond.PlayerID {
		t.Fatalf("player identity changed across a reconnect: %q then %q",
			decodedFirst.PlayerID, decodedSecond.PlayerID)
	}
	if decodedFirst.Channel.ReplyTo == decodedSecond.Channel.ReplyTo {
		t.Fatal("the fixtures use the same binding; the reconnect case is vacuous")
	}
	if decodedFirst.ActionID == decodedSecond.ActionID {
		t.Fatal("two submissions must be two actions")
	}
}

// Turn identity is 1:1 with action identity and derived purely, which is what
// makes a redelivered action resolve to the same turn instead of a second one.
func TestTurnIDForAction_IsDeterministicAndOneToOne(t *testing.T) {
	if got, want := payload.TurnIDForAction(testActionID), payload.TurnIDForAction(testActionID); got != want {
		t.Fatalf("derivation is not deterministic: %q then %q", got, want)
	}
	if payload.TurnIDForAction("act-1") == payload.TurnIDForAction("act-2") {
		t.Fatal("two actions collided onto one turn id")
	}
	if payload.TurnIDForAction("act-1") == "" {
		t.Fatal("a present action must derive a present turn id")
	}
	if payload.TurnIDForAction("") != "" {
		t.Fatal("an absent action must not derive a turn id")
	}
	if got := payload.TurnIDForAction(testActionID); got != testTurnID {
		t.Fatalf("turn id for %q = %q, want %q", testActionID, got, testTurnID)
	}
}

// No turn-processing step may depend on elapsed wall-clock time. An action
// submitted a year after the previous turn is an ordinary action.
func TestPlayerAction_ArbitrarilyOldOrFutureArrivalIsStillValid(t *testing.T) {
	for _, arrival := range []time.Time{
		testTime.Add(-365 * 24 * time.Hour),
		testTime.Add(365 * 24 * time.Hour),
		time.Unix(1, 0).UTC(),
	} {
		action := validPlayerAction()
		action.ArrivedAt = arrival

		decoded, ok := decode(t, publish(t, action)).(*payload.PlayerAction)
		if !ok {
			t.Fatal("decoder produced the wrong type")
		}
		if err := decoded.Validate(); err != nil {
			t.Fatalf("an action arriving at %s was rejected: %v", arrival, err)
		}
		if !decoded.ArrivedAt.Equal(arrival) {
			t.Fatalf("arrival time changed on the wire: got %s want %s", decoded.ArrivedAt, arrival)
		}
	}
}

func TestChannelAdapter_OnlyRegisteredAdaptersAreAccepted(t *testing.T) {
	binding := payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "ws-conn-7"}
	if err := binding.Validate(); err != nil {
		t.Fatalf("the registered adapter was rejected: %v", err)
	}
	for _, adapter := range []string{"slack", "email", "sms", "WebSocket", ""} {
		binding.Adapter = vocabulary.ChannelAdapter(adapter)
		if err := binding.Validate(); err == nil {
			t.Fatalf("unregistered adapter %q was accepted; adapters arrive with their components", adapter)
		}
	}
}
