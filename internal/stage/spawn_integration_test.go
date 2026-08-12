//go:build integration

package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
)

// This is the exact publish crash window: JetStream accepts the first task and
// returns a PubAck, but the process loses that success and the stage delivery is
// retried. The deterministic TaskID is also the Nats-Msg-Id, so the second
// publish is acknowledged as a duplicate and only one task is stored/delivered.
func TestSpawner_AckLostAfterStoreAndRedeliveryProducesOneTask(t *testing.T) {
	harness := testinfra.Require(t)
	stream, err := harness.Client.EnsureStream(t.Context(), stage.AgentStreamConfig())
	if err != nil {
		t.Fatalf("ensure AGENT: %v", err)
	}
	before, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read AGENT before publish: %v", err)
	}

	journal := &journal{}
	publisher := &acknowledgedThenFailedPublisher{client: harness.Client}
	uniqueActionID := "crash-window-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	uniqueTurnID := "turn-" + uniqueActionID
	spawner, err := stage.NewSpawner(
		persona.Adjudicator(),
		&fakeRecorder{journal: journal, transition: turn.Transition{Outcome: turn.OutcomeAdvanced}},
		&fakeGuard{journal: journal, resumption: persona.Resumption{Decision: persona.DecisionRun}},
		&fakeProjector{journal: journal},
		&fakePrompter{journal: journal, actionID: uniqueActionID},
		publisher,
	)
	if err != nil {
		t.Fatalf("NewSpawner: %v", err)
	}
	trigger := stage.Trigger{
		TurnID:       uniqueTurnID,
		TurnEntityID: strings.TrimSuffix(testTurnEntityID, testTurnID) + uniqueTurnID,
		Subject:      "semmachina.turn.adjudicating",
	}
	if err := spawner.Run(t.Context(), trigger); err == nil {
		t.Fatal("first delivery saw success even though its PubAck was deliberately lost")
	}
	if err := spawner.Run(t.Context(), trigger); err != nil {
		t.Fatalf("redelivered stage: %v", err)
	}

	after, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read AGENT after publish: %v", err)
	}
	wantTaskID := string(persona.RoleAdjudicator) + "-" + uniqueTurnID
	stored := 0
	for seq := before.State.LastSeq + 1; seq <= after.State.LastSeq; seq++ {
		raw, getErr := stream.GetMsg(t.Context(), seq)
		if getErr != nil {
			if errors.Is(getErr, jetstream.ErrMsgNotFound) {
				continue
			}
			t.Fatalf("read AGENT sequence %d: %v", seq, getErr)
		}
		var envelope struct {
			Payload agentic.TaskMessage `json:"payload"`
		}
		if json.Unmarshal(raw.Data, &envelope) == nil && envelope.Payload.TaskID == wantTaskID {
			stored++
		}
	}
	if stored != 1 {
		t.Fatalf("stored tasks with TaskID %q = %d, want exactly 1", wantTaskID, stored)
	}

	consumerName := "crash-window-" + strings.TrimPrefix(uniqueActionID, "crash-window-")
	consumer, err := stream.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   before.State.LastSeq + 1,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: stage.TaskSubjectFor(persona.RoleAdjudicator),
	})
	if err != nil {
		t.Fatalf("create proof consumer: %v", err)
	}
	t.Cleanup(func() { _ = stream.DeleteConsumer(context.Background(), consumerName) })
	batch, err := consumer.Fetch(256, jetstream.FetchMaxWait(250*time.Millisecond))
	if err != nil {
		t.Fatalf("fetch stored tasks: %v", err)
	}
	delivered := 0
	for msg := range batch.Messages() {
		var envelope struct {
			Payload agentic.TaskMessage `json:"payload"`
		}
		if json.Unmarshal(msg.Data(), &envelope) == nil && envelope.Payload.TaskID == wantTaskID {
			delivered++
		}
		if err := msg.Ack(); err != nil {
			t.Fatalf("ack proof delivery: %v", err)
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered tasks with TaskID %q = %d, want exactly 1", wantTaskID, delivered)
	}
}
