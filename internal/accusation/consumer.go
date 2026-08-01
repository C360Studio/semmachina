package accusation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// DefaultAckWait bounds one accusation delivery attempt.
	DefaultAckWait = 30 * time.Second
	// DefaultHeartbeat protects multi-read verification from early redelivery.
	DefaultHeartbeat = 10 * time.Second
)

// DurableConsumer is the TURN_STAGES durable binding surface.
type DurableConsumer interface {
	ConsumeDurable(context.Context, natsclient.StreamConsumerConfig, time.Duration,
		func(context.Context, []byte) error) error
}

// PreflightLoader resolves the private decision and verifier-only solution.
type PreflightLoader interface {
	Load(context.Context, string) (Preflight, error)
}

// ResultCommitter owns result object-first/ref-last journaling.
type ResultCommitter interface {
	CommitResult(context.Context, string, *payload.AccusationResult) (content.Ref, error)
	CommitNotApplicable(context.Context, string, string) (content.Ref, error)
}

// LifecycleRecorder submits structural case lifecycle receipts.
type LifecycleRecorder interface {
	Record(context.Context, caseflow.TransitionRequest) (caseflow.ReceiptOutcome, error)
}

// TurnFailer closes permanently invalid work with one non-disclosing code.
type TurnFailer interface {
	Fail(context.Context, string, string, vocabulary.FailureReason, content.Ref) (turn.Transition, error)
}

// Consumer binds the verifier's separate durable on TURN_STAGES.
type Consumer struct {
	durable   DurableConsumer
	loader    PreflightLoader
	committer ResultCommitter
	lifecycle LifecycleRecorder
	failer    TurnFailer
}

// NewConsumer composes durable delivery and deterministic accusation handling.
func NewConsumer(durable DurableConsumer, loader PreflightLoader, committer ResultCommitter,
	lifecycle LifecycleRecorder, failer TurnFailer) (*Consumer, error) {
	if durable == nil || loader == nil || committer == nil || lifecycle == nil || failer == nil {
		return nil, errors.New("accusation consumer requires durable, loader, committer, lifecycle, and turn failer")
	}
	return &Consumer{durable: durable, loader: loader, committer: committer, lifecycle: lifecycle, failer: failer}, nil
}

// Start binds the accusation durable before rules can publish work.
func (c *Consumer) Start(ctx context.Context) error {
	return c.durable.ConsumeDurable(ctx, natsclient.StreamConsumerConfig{
		StreamName: rulepack.StageStream, ConsumerName: rulepack.AccusationConsumerName,
		FilterSubject: rulepack.SubjectAccusation, DeliverPolicy: "all", AckPolicy: "explicit",
		MaxDeliver: 0, AckWait: DefaultAckWait,
	}, DefaultHeartbeat, c.Handle)
}

// Handle terminates malformed triggers before reads, retries transport and
// lifecycle timing, and closes permanent identity failures without disclosure.
func (c *Consumer) Handle(ctx context.Context, data []byte) error {
	trigger, err := stage.ParseTrigger(data)
	if err != nil {
		return natsclient.TerminateDelivery(err)
	}
	if trigger.Subject != rulepack.SubjectAccusation {
		return natsclient.TerminateDelivery(fmt.Errorf("accusation durable received subject %q", trigger.Subject))
	}
	loaded, err := c.loader.Load(ctx, trigger.TurnEntityID)
	if err != nil {
		var missing *MissingTriggeredTurnError
		if errors.As(err, &missing) {
			return natsclient.TerminateDelivery(err)
		}
		var permanent *IntegrityError
		if errors.As(err, &permanent) {
			return c.failInvalid(ctx, trigger)
		}
		return fmt.Errorf("load accusation preflight: %w", err)
	}
	if !loaded.Applicable {
		if _, err := c.committer.CommitNotApplicable(ctx, loaded.TurnID, trigger.TurnEntityID); err != nil {
			var permanent *IntegrityError
			if errors.As(err, &permanent) {
				return c.failInvalid(ctx, trigger)
			}
			return fmt.Errorf("commit non-applicable accusation barrier: %w", err)
		}
		return nil
	}
	if _, err := c.lifecycle.Record(ctx, caseflow.TransitionRequest{
		CaseEntityID: loaded.CaseID, EventID: loaded.Decision.DecisionID,
		Kind: vocabulary.CaseEventAccusationSubmitted,
	}); err != nil {
		return fmt.Errorf("record accusation submitted: %w", err)
	}
	if loaded.CasePhase == vocabulary.CasePhaseInvestigation {
		return errors.New("await accusation lifecycle transition")
	}
	if loaded.CasePhase != vocabulary.CasePhaseAccusation && loaded.CasePhase != vocabulary.CasePhaseDenouement {
		return c.failInvalid(ctx, trigger)
	}
	result, err := Verify(loaded.Decision, loaded.Solution)
	if err != nil {
		return c.failInvalid(ctx, trigger)
	}
	if _, err := c.committer.CommitResult(ctx, trigger.TurnEntityID, result); err != nil {
		var permanent *IntegrityError
		if errors.As(err, &permanent) {
			return c.failInvalid(ctx, trigger)
		}
		return fmt.Errorf("commit accusation result: %w", err)
	}
	if result.Outcome == payload.AccusationIncorrect {
		return nil
	}
	if _, err := c.lifecycle.Record(ctx, caseflow.TransitionRequest{
		CaseEntityID: loaded.CaseID, EventID: result.ResultID,
		Kind: vocabulary.CaseEventAccusationCorrect,
	}); err != nil {
		return fmt.Errorf("record correct accusation: %w", err)
	}
	return nil
}

func (c *Consumer) failInvalid(ctx context.Context, trigger stage.Trigger) error {
	if _, err := c.failer.Fail(ctx, trigger.TurnID, trigger.TurnEntityID,
		vocabulary.FailureAccusationInvalid, content.Ref{}); err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return natsclient.TerminateDelivery(err)
		}
		return fmt.Errorf("durably fail invalid accusation turn: %w", err)
	}
	return nil
}
