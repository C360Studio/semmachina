package caseflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// DefaultProgressAckWait bounds one progress delivery attempt.
	DefaultProgressAckWait = 30 * time.Second
	// DefaultProgressHeartbeat protects processing from premature redelivery.
	DefaultProgressHeartbeat = 10 * time.Second
)

// ProgressDurable is the TURN_STAGES durable binding surface.
type ProgressDurable interface {
	ConsumeDurable(context.Context, natsclient.StreamConsumerConfig, time.Duration,
		func(context.Context, []byte) error) error
}

// ProgressHandler executes one deterministic progress operation.
type ProgressHandler interface {
	Process(context.Context, string, string) (content.Ref, error)
}

// ProgressTurnFailer closes permanently invalid owning-world turns.
type ProgressTurnFailer interface {
	Fail(context.Context, string, string, vocabulary.FailureReason, content.Ref) (turn.Transition, error)
}

// ProgressConsumer owns durable delivery, not case lifecycle phase.
type ProgressConsumer struct {
	durable ProgressDurable
	handler ProgressHandler
	failer  ProgressTurnFailer
	owner   turn.Identity
}

// NewProgressConsumer composes the separate durable and deterministic handler.
func NewProgressConsumer(
	durable ProgressDurable, handler ProgressHandler, failer ProgressTurnFailer, owner turn.Identity,
) (*ProgressConsumer, error) {
	if durable == nil || handler == nil || failer == nil {
		return nil, errors.New("case progress consumer requires durable, handler, and turn failer")
	}
	if err := owner.Validate(); err != nil {
		return nil, fmt.Errorf("case progress consumer owner: %w", err)
	}
	return &ProgressConsumer{durable: durable, handler: handler, failer: failer, owner: owner}, nil
}

// Start binds before rule replay can publish progress work.
func (c *ProgressConsumer) Start(ctx context.Context) error {
	return c.durable.ConsumeDurable(ctx, natsclient.StreamConsumerConfig{
		StreamName: rulepack.StageStream, ConsumerName: rulepack.CaseProgressConsumerName,
		FilterSubject: rulepack.SubjectCaseProgress, DeliverPolicy: "all", AckPolicy: "explicit",
		MaxDeliver: 0, AckWait: DefaultProgressAckWait,
	}, DefaultProgressHeartbeat, c.Handle)
}

// Handle terminates malformed/misrouted triggers and retries processing errors.
func (c *ProgressConsumer) Handle(ctx context.Context, data []byte) error {
	trigger, err := stage.ParseTrigger(data)
	if err != nil {
		return natsclient.TerminateDelivery(err)
	}
	if trigger.Subject != rulepack.SubjectCaseProgress {
		return natsclient.TerminateDelivery(fmt.Errorf("case progress durable received subject %q", trigger.Subject))
	}
	ownedEntityID, err := c.owner.EntityID(trigger.TurnID)
	if err != nil {
		return natsclient.TerminateDelivery(fmt.Errorf("case progress trigger identity: %w", err))
	}
	if trigger.TurnEntityID != ownedEntityID {
		return natsclient.TerminateDelivery(fmt.Errorf(
			"case progress trigger %s belongs outside owning world %s", trigger.TurnEntityID, ownedEntityID))
	}
	if _, err := c.handler.Process(ctx, trigger.TurnID, trigger.TurnEntityID); err != nil {
		var missing *MissingTriggeredTurnError
		if errors.As(err, &missing) {
			return natsclient.TerminateDelivery(err)
		}
		var permanent *PermanentProgressError
		if errors.As(err, &permanent) {
			if _, failErr := c.failer.Fail(ctx, trigger.TurnID, trigger.TurnEntityID,
				vocabulary.FailureCaseProgressInvalid, content.Ref{}); failErr != nil {
				if errors.Is(failErr, graphio.ErrEntityNotFound) {
					return natsclient.TerminateDelivery(fmt.Errorf(
						"invalid case progress turn disappeared before failure recording: %w", failErr))
				}
				return fmt.Errorf("durably fail invalid case progress turn: %w", failErr)
			}
			return nil
		}
		return fmt.Errorf("process deterministic case progress: %w", err)
	}
	return nil
}
