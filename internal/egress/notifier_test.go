package egress_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The ack decision is the whole contract of Handle, and it is the one thing a
// green delivery test cannot see: a handler that does the right work and returns
// the wrong error class leaves JetStream retrying forever or dropping silently,
// and the delivery looks correct either way.

type stubConsumer struct{}

func (stubConsumer) ConsumeDurable(
	context.Context,
	natsclient.StreamConsumerConfig,
	time.Duration,
	func(context.Context, []byte) error,
) error {
	return nil
}

func newNotifier(t *testing.T, h *harness) *egress.Notifier {
	t.Helper()
	notifier, err := egress.NewNotifier(
		h.results, h.router, stubConsumer{}, egress.WithNotifierLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewNotifier: %v", err)
	}
	return notifier
}

func notification(t *testing.T, entityID string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"entity_id": entityID,
		"subject":   rulepack.SubjectResolved,
		"source":    rulepack.PackID,
	})
	if err != nil {
		t.Fatalf("encode the notification: %v", err)
	}
	return data
}

func isPermanent(err error) bool {
	var permanent *natsclient.PermanentDeliveryError
	return errors.As(err, &permanent)
}

func TestHandle_AcknowledgesADeliveredTurn(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)
	h.directory.connect(testPlayerID, "conn-pat")

	if err := newNotifier(t, h).Handle(t.Context(), notification(t, testTurnEntityID)); err != nil {
		t.Fatalf("Handle on a delivered turn = %v, want an acknowledgment", err)
	}
	if got := len(h.sink.to("conn-pat")); got != 1 {
		t.Fatalf("the player received %d documents", got)
	}
}

// Nobody connected acknowledges. A nak here would redeliver the same push at the
// framework's cadence for as long as the player stayed away, which at email
// cadence is a retry loop measured in days — and the durable answer was always
// retrieval.
func TestHandle_AcknowledgesWhenNobodyIsConnected(t *testing.T) {
	h := newHarness(t)
	h.completedTurn(t, testTurnID, testPlayerID, testTime)

	if err := newNotifier(t, h).Handle(t.Context(), notification(t, testTurnEntityID)); err != nil {
		t.Fatalf("Handle with nobody connected = %v, want an acknowledgment", err)
	}
}

func TestHandle_ClassifiesEveryRefusal(t *testing.T) {
	tests := []struct {
		name      string
		arrange   func(*testing.T, *harness) []byte
		permanent bool
		why       string
	}{
		{
			name:      "not a rule publication",
			arrange:   func(*testing.T, *harness) []byte { return []byte(`{"nope":`) },
			permanent: true,
			why:       "no redelivery makes malformed bytes parse",
		},
		{
			name: "names something that is not a turn",
			arrange: func(t *testing.T, _ *harness) []byte {
				return notification(t, testPrefix+".scene.gatehouse")
			},
			permanent: true,
			why:       "only turns have a phase, so this can never become a result",
		},
		{
			name: "names a turn that does not exist",
			arrange: func(t *testing.T, _ *harness) []byte {
				return notification(t, testPrefix+".turn.turn-act-ghost")
			},
			permanent: true,
			why:       "a turn nothing created is not one a redelivery finds",
		},
		{
			name: "the turn has not resolved from this reader's view",
			arrange: func(t *testing.T, h *harness) []byte {
				h.graph.putTurn(newTurn(t, testTurnID).
					accepted(testPlayerID).
					phase(vocabulary.PhaseNarrating, testTime).
					build())
				return notification(t, testTurnEntityID)
			},
			permanent: false,
			why: "the rule fired off a KV watch and this reads through the query surface, so the two " +
				"disagree for an instant and the redelivery is the whole answer",
		},
		{
			name: "the prose it references is not in the store",
			arrange: func(t *testing.T, h *harness) []byte {
				h.completedTurn(t, testTurnID, testPlayerID, testTime)
				delete(h.artifacts.narrations, refFor(vocabulary.TurnNarrationRef, testTurnID))
				return notification(t, testTurnEntityID)
			},
			permanent: true,
			why: "the write ordering makes this a correctness bug rather than a race, and no redelivery " +
				"invents the object — while naking would hold every later player behind it",
		},
		{
			name: "the graph is unreachable",
			arrange: func(t *testing.T, h *harness) []byte {
				h.graph.err[testTurnEntityID] = errors.New("nats: timeout")
				return notification(t, testTurnEntityID)
			},
			permanent: false,
			why:       "a transport fault is exactly what redelivery is for",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			data := test.arrange(t, h)

			err := newNotifier(t, h).Handle(t.Context(), data)
			if err == nil {
				t.Fatal("Handle acknowledged it")
			}
			switch {
			case test.permanent && !isPermanent(err):
				t.Fatalf("Handle nak'd %q; it must TERMINATE, because %s\n  error: %v", test.name, test.why, err)
			case !test.permanent && isPermanent(err):
				t.Fatalf("Handle terminated %q; it must NAK, because %s\n  error: %v", test.name, test.why, err)
			}
			if len(h.sink.sent) != 0 {
				t.Fatalf("a refused notification still delivered something: %+v", h.sink.sent)
			}
		})
	}
}

func TestConsumerConfig_BindsTheResolvedSubjectUnderItsOwnDurableName(t *testing.T) {
	cfg := egress.ConsumerConfig()
	if cfg.StreamName != rulepack.StageStream {
		t.Errorf("StreamName = %q, want %q", cfg.StreamName, rulepack.StageStream)
	}
	if cfg.FilterSubject != rulepack.SubjectResolved {
		t.Errorf("FilterSubject = %q, want %q", cfg.FilterSubject, rulepack.SubjectResolved)
	}
	// The load-bearing negative: sharing the archive's durable would make each
	// one's acknowledgment the other's data loss.
	if cfg.ConsumerName == "semmachina-campaign-ledger" {
		t.Fatal("the egress path binds the ledger's durable consumer; two consumers of one subject need " +
			"two durables, or archiving a turn consumes the notification that would have delivered it")
	}
	// "new", and it is the one consumer in the engine that is not "all". The
	// downtime case the others need "all" for is covered here by the
	// acknowledgment floor, which applies regardless of policy — while "all"
	// would make a first creation against a live campaign replay every resolved
	// turn it ever had, because the stage stream never evicts.
	if cfg.DeliverPolicy != "new" {
		t.Errorf("DeliverPolicy = %q, want \"new\"; push is the non-durable half and every historical result "+
			"is still retrievable, so replaying the campaign's history buys nothing and costs a graph read "+
			"plus two artifact reads per turn ever resolved", cfg.DeliverPolicy)
	}
	if cfg.AckPolicy != "explicit" {
		t.Errorf("AckPolicy = %q, want explicit", cfg.AckPolicy)
	}
}

func TestNewNotifier_RefusesAHalfWiredPushPath(t *testing.T) {
	h := newHarness(t)
	if _, err := egress.NewNotifier(nil, h.router, stubConsumer{}); err == nil {
		t.Error("a notifier with no result surface was accepted")
	}
	if _, err := egress.NewNotifier(h.results, nil, stubConsumer{}); err == nil {
		t.Error("a notifier with no router was accepted")
	}
	if _, err := egress.NewNotifier(h.results, h.router, nil); err == nil {
		t.Error("a notifier with no consumer was accepted")
	}
}
