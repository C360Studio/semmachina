package caseflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type progressDurable struct {
	config  natsclient.StreamConsumerConfig
	handler func(context.Context, []byte) error
}

func (d *progressDurable) ConsumeDurable(_ context.Context, cfg natsclient.StreamConsumerConfig, _ time.Duration,
	handler func(context.Context, []byte) error) error {
	d.config, d.handler = cfg, handler
	return nil
}

type progressHandler struct {
	turnID, entityID string
	calls            int
	err              error
}

func (h *progressHandler) Process(_ context.Context, turnID, entityID string) (content.Ref, error) {
	h.turnID, h.entityID = turnID, entityID
	h.calls++
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/case-progress"}, h.err
}

type progressFailer struct {
	calls  int
	reason vocabulary.FailureReason
	err    error
}

func (f *progressFailer) Fail(_ context.Context, _, _ string, reason vocabulary.FailureReason,
	_ content.Ref) (turn.Transition, error) {
	f.calls++
	f.reason = reason
	return turn.Transition{}, f.err
}

func progressOwner() turn.Identity {
	return turn.Identity{Org: "acme", WorldNS: "bellweather", Template: "campaign"}
}

func TestProgressConsumerBindsSeparateDurableAndDispatchesOnlyItsSubject(t *testing.T) {
	durable, handler, failer := &progressDurable{}, &progressHandler{}, &progressFailer{}
	consumer, err := caseflow.NewProgressConsumer(durable, handler, failer, progressOwner())
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if durable.config.StreamName != rulepack.StageStream ||
		durable.config.ConsumerName != rulepack.CaseProgressConsumerName ||
		durable.config.FilterSubject != rulepack.SubjectCaseProgress || durable.config.AckPolicy != "explicit" {
		t.Fatalf("durable config = %+v", durable.config)
	}
	data, _ := json.Marshal(map[string]any{
		"entity_id": progressTurnEntity, "subject": rulepack.SubjectCaseProgress,
	})
	if err := durable.handler(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	if handler.calls != 1 || handler.turnID != progressTurnID || handler.entityID != progressTurnEntity {
		t.Fatalf("progress dispatch = %+v", handler)
	}
	wrong, _ := json.Marshal(map[string]any{
		"entity_id": progressTurnEntity, "subject": rulepack.SubjectKnowledge,
	})
	if err := durable.handler(t.Context(), wrong); err == nil {
		t.Fatal("progress consumer accepted another auxiliary subject")
	}
	if handler.calls != 1 {
		t.Fatalf("wrong subject invoked progress %d times", handler.calls)
	}
}

func TestProgressConsumerClassifiesPermanentMissingTransientAndForeignDeliveries(t *testing.T) {
	trigger := func(entityID string) []byte {
		data, _ := json.Marshal(map[string]any{"entity_id": entityID, "subject": rulepack.SubjectCaseProgress})
		return data
	}
	tests := map[string]struct {
		handlerErr  error
		failErr     error
		entityID    string
		wantNil     bool
		wantTerm    bool
		wantHandled int
		wantFailed  int
	}{
		"permanent fails and acks": {handlerErr: &caseflow.PermanentProgressError{Err: errors.New("invalid")},
			entityID: progressTurnEntity, wantNil: true, wantHandled: 1, wantFailed: 1},
		"failure write retries": {handlerErr: &caseflow.PermanentProgressError{Err: errors.New("invalid")},
			failErr: errors.New("write unavailable"), entityID: progressTurnEntity, wantHandled: 1, wantFailed: 1},
		"missing during failure write terminates": {
			handlerErr: &caseflow.PermanentProgressError{Err: errors.New("invalid")},
			failErr:    fmt.Errorf("failure write lost its turn: %w", graphio.ErrEntityNotFound),
			entityID:   progressTurnEntity, wantTerm: true, wantHandled: 1, wantFailed: 1},
		"transient retries without failure": {handlerErr: errors.New("timeout"),
			entityID: progressTurnEntity, wantHandled: 1},
		"missing terminates": {handlerErr: &caseflow.MissingTriggeredTurnError{
			TurnEntityID: progressTurnEntity, Err: errors.New("missing")},
			entityID: progressTurnEntity, wantTerm: true, wantHandled: 1},
		"foreign world terminates before handler": {
			entityID: "acme.semmachina.foreign.campaign.turn.turn-act-1", wantTerm: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			handler := &progressHandler{err: tc.handlerErr}
			failer := &progressFailer{err: tc.failErr}
			consumer, err := caseflow.NewProgressConsumer(&progressDurable{}, handler, failer, progressOwner())
			if err != nil {
				t.Fatal(err)
			}
			err = consumer.Handle(t.Context(), trigger(tc.entityID))
			if tc.wantNil && err != nil {
				t.Fatalf("Handle: %v", err)
			}
			var terminated *natsclient.PermanentDeliveryError
			if tc.wantTerm != errors.As(err, &terminated) {
				t.Fatalf("termination = %v, err=%v", errors.As(err, &terminated), err)
			}
			if !tc.wantNil && !tc.wantTerm && err == nil {
				t.Fatal("retryable delivery returned nil")
			}
			if handler.calls != tc.wantHandled || failer.calls != tc.wantFailed {
				t.Fatalf("handler/failer calls = %d/%d, want %d/%d",
					handler.calls, failer.calls, tc.wantHandled, tc.wantFailed)
			}
			if tc.wantFailed == 1 && failer.reason != vocabulary.FailureCaseProgressInvalid {
				t.Fatalf("failure reason = %s", failer.reason)
			}
		})
	}
}
