package knowledge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// DefaultAckWait is the durable redelivery window for knowledge work.
	DefaultAckWait = 30 * time.Second
	// DefaultHeartbeat keeps long preflight and commit work from premature redelivery.
	DefaultHeartbeat = 10 * time.Second
)

// DurableConsumer is the TURN_STAGES durable binding surface.
type DurableConsumer interface {
	ConsumeDurable(context.Context, natsclient.StreamConsumerConfig, time.Duration,
		func(context.Context, []byte) error) error
}

// TurnFailer durably terminates permanently unauthorized turns.
type TurnFailer interface {
	Fail(context.Context, string, string, vocabulary.FailureReason, content.Ref) (turn.Transition, error)
}

// PreflightLoader resolves every authorization input before the component is
// allowed to write. The narrow surface also keeps delivery semantics testable
// without pretending a graph read is a durable consumer.
type PreflightLoader interface {
	Load(context.Context, string) (LoadResult, error)
}

// Committer owns the idempotent knowledge/revelation write journal.
type Committer interface {
	Grant(context.Context, string, Preflight, ShareAuthorizer) (content.Ref, error)
	GrantNotApplicable(context.Context, string, string) (content.Ref, error)
}

// Consumer binds KnowledgeGranter's separate durable on TURN_STAGES.
type Consumer struct {
	consumer DurableConsumer
	loader   PreflightLoader
	granter  Committer
	failer   TurnFailer
	shares   ShareAuthorizer
}

// NewConsumer composes durable delivery, complete preflight, idempotent commit,
// and permanent turn failure.
func NewConsumer(
	consumer DurableConsumer, loader PreflightLoader, granter Committer, failer TurnFailer, shares ShareAuthorizer,
) (*Consumer, error) {
	if consumer == nil || loader == nil || granter == nil || failer == nil {
		return nil, errors.New("knowledge consumer requires durable, loader, granter, and turn failer")
	}
	return &Consumer{consumer: consumer, loader: loader, granter: granter, failer: failer, shares: shares}, nil
}

// Start binds the separate knowledge durable before rule replay can publish work.
func (c *Consumer) Start(ctx context.Context) error {
	return c.consumer.ConsumeDurable(ctx, natsclient.StreamConsumerConfig{
		StreamName: rulepack.StageStream, ConsumerName: rulepack.KnowledgeConsumerName,
		FilterSubject: rulepack.SubjectKnowledge, DeliverPolicy: "all", AckPolicy: "explicit",
		MaxDeliver: 0, AckWait: DefaultAckWait,
	}, DefaultHeartbeat, c.Handle)
}

// Handle returns nil for committed work and permanent authorization failures;
// every transient read/write error is returned for redelivery.
func (c *Consumer) Handle(ctx context.Context, data []byte) error {
	trigger, err := stage.ParseTrigger(data)
	if err != nil {
		return natsclient.TerminateDelivery(err)
	}
	loaded, err := c.loader.Load(ctx, trigger.TurnEntityID)
	if err != nil {
		var rejection *AuthorizationError
		if errors.As(err, &rejection) {
			return c.failUnauthorized(ctx, trigger)
		}
		return fmt.Errorf("load knowledge preflight: %w", err)
	}
	if !loaded.Applicable {
		_, err = c.granter.GrantNotApplicable(ctx, loaded.TurnID, trigger.TurnEntityID)
		return err
	}
	_, err = c.granter.Grant(ctx, trigger.TurnEntityID, loaded.Preflight, c.shares)
	if err == nil {
		return nil
	}
	var rejection *AuthorizationError
	if !errors.As(err, &rejection) {
		return fmt.Errorf("commit knowledge grant: %w", err)
	}
	return c.failUnauthorized(ctx, trigger)
}

func (c *Consumer) failUnauthorized(ctx context.Context, trigger stage.Trigger) error {
	if _, failErr := c.failer.Fail(ctx, trigger.TurnID, trigger.TurnEntityID,
		vocabulary.FailureKnowledgeUnauthorized, content.Ref{}); failErr != nil {
		return fmt.Errorf("durably fail unauthorized knowledge turn: %w", failErr)
	}
	return nil
}
