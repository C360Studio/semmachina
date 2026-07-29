package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Ack timings for a stage delivery.
//
// AckWait bounds one delivery in flight; the heartbeat resets that clock while a
// stage is genuinely working, which matters here far more than at intake — a
// persona spawn assembles a scene and a model call is what follows it. Upstream
// enforces heartbeat*2 <= AckWait, so these two are chosen together.
//
// Neither is a pacing assumption about the PLAYER. They bound one stage's work,
// and a turn waiting on a player's next action is not in flight at all.
const (
	// DefaultAckWait is how long one stage delivery may be in flight.
	DefaultAckWait = 2 * time.Minute
	// DefaultHeartbeat resets the ack clock while a stage is working.
	DefaultHeartbeat = 30 * time.Second
)

// Stage is one hop of the turn loop.
//
// A stage OWNS EXACTLY ONE PHASE — the one it enters — and every stage enters
// its phase before it does anything else. That is what makes the phase a real
// guard rather than a label: a duplicate trigger for a stage the turn has left
// is declined by the recorder before the stage can spend anything.
type Stage interface {
	// Phase is the phase this stage enters.
	Phase() vocabulary.TurnPhase
	// Run executes the stage for one trigger. A nil return acknowledges the
	// trigger.
	Run(ctx context.Context, trigger Trigger) error
}

// Consumer is the durable at-least-once consumption surface a stage runner
// needs. It is the framework's own: ConsumeDurable owns the ack, the
// nak-with-delay, the terminate-on-permanent-error, and the in-flight heartbeat.
type Consumer interface {
	ConsumeDurable(
		ctx context.Context,
		cfg natsclient.StreamConsumerConfig,
		heartbeat time.Duration,
		handler func(context.Context, []byte) error,
	) error
}

// StreamEnsurer creates the stage-trigger stream if it does not exist.
type StreamEnsurer interface {
	EnsureStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ Consumer      = (*natsclient.Client)(nil)
	_ StreamEnsurer = (*natsclient.Client)(nil)
)

// Runner binds one durable consumer per stage to the stage-trigger stream.
type Runner struct {
	consumer  Consumer
	streams   StreamEnsurer
	stages    []Stage
	logger    *slog.Logger
	ackWait   time.Duration
	heartbeat time.Duration
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithLogger sets the runner's logger.
func WithLogger(logger *slog.Logger) RunnerOption {
	return func(r *Runner) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// WithAckTimings overrides the delivery timings.
func WithAckTimings(ackWait, heartbeat time.Duration) RunnerOption {
	return func(r *Runner) {
		r.ackWait = ackWait
		r.heartbeat = heartbeat
	}
}

// NewRunner builds a stage runner over the stage-trigger stream.
//
// It refuses duplicate phases and a stage whose phase no trigger drives, because
// both are wiring bugs that present as a turn that simply stops: two runners
// bound to one subject would each see half the triggers, and a stage listening
// on a subject nothing publishes would never run at all.
func NewRunner(consumer Consumer, streams StreamEnsurer, stages []Stage, opts ...RunnerOption) (*Runner, error) {
	if consumer == nil {
		return nil, errors.New("a stage runner requires a stream consumer")
	}
	if streams == nil {
		return nil, errors.New("a stage runner requires a stream creator")
	}
	if len(stages) == 0 {
		return nil, errors.New("a stage runner requires at least one stage")
	}

	seen := make(map[vocabulary.TurnPhase]bool, len(stages))
	for _, stage := range stages {
		if stage == nil {
			return nil, errors.New("a stage runner was given a nil stage")
		}
		phase := stage.Phase()
		if seen[phase] {
			return nil, fmt.Errorf(
				"two stages claim phase %s; they would bind the same durable consumer and each see half the "+
					"triggers", phase)
		}
		if _, err := rulepack.SubjectForPhase(phase); err != nil {
			return nil, err
		}
		seen[phase] = true
	}

	runner := &Runner{
		consumer:  consumer,
		streams:   streams,
		stages:    stages,
		logger:    slog.Default(),
		ackWait:   DefaultAckWait,
		heartbeat: DefaultHeartbeat,
	}
	for _, opt := range opts {
		opt(runner)
	}
	if runner.ackWait <= 0 || runner.heartbeat <= 0 {
		return nil, errors.New("a stage runner requires positive ack timings")
	}
	return runner, nil
}

// StreamConfig is the stage-trigger stream this runner binds to.
//
// Declared explicitly rather than left to the consumer's auto-create, which
// would apply a seven-day MaxAge nobody chose. Retention is limits-based with no
// age or size eviction: a stage trigger that expired unread is a turn that stops
// mid-loop with no error anywhere, which is the failure this whole stream exists
// to prevent.
func StreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:      rulepack.StageStream,
		Subjects:  []string{rulepack.StageSubjectFilter},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
	}
}

// ConsumerConfig is the durable consumer one stage binds.
//
// MaxDeliver is unlimited for the same reason intake's is: a transport failure
// must not silently drop a hop of a turn already in flight. The permanent class
// that would otherwise redeliver forever is terminated in the handler instead —
// see handle.
func (r *Runner) ConsumerConfig(phase vocabulary.TurnPhase) (natsclient.StreamConsumerConfig, error) {
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		return natsclient.StreamConsumerConfig{}, err
	}
	return natsclient.StreamConsumerConfig{
		StreamName:    rulepack.StageStream,
		ConsumerName:  "semmachina-stage-" + string(phase),
		FilterSubject: subject,
		DeliverPolicy: "all",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       r.ackWait,
	}, nil
}

// Start creates the stage stream and binds every stage's consumer.
//
// The stream is ensured BEFORE any consumer binds, because a rule that fires
// while the stream does not exist publishes into nothing: the rule engine's
// publish succeeds against a JetStream subject with no stream only by failing,
// and a stage that was never triggered looks exactly like a stage that ran and
// did nothing.
func (r *Runner) Start(ctx context.Context) error {
	if _, err := r.streams.EnsureStream(ctx, StreamConfig()); err != nil {
		return fmt.Errorf("ensure the %s stream: %w", rulepack.StageStream, err)
	}
	for _, stage := range r.stages {
		cfg, err := r.ConsumerConfig(stage.Phase())
		if err != nil {
			return err
		}
		if err := r.consumer.ConsumeDurable(ctx, cfg, r.heartbeat, r.handler(stage)); err != nil {
			return fmt.Errorf("bind the %s stage consumer: %w", stage.Phase(), err)
		}
	}
	return nil
}

// handler adapts one stage to the durable consumer's handler contract.
func (r *Runner) handler(stage Stage) func(context.Context, []byte) error {
	return func(ctx context.Context, data []byte) error {
		return r.handle(ctx, stage, data)
	}
}

// handle processes one stage trigger. Its return value IS the ack decision.
//
// Three outcomes, and the split follows intake's:
//
//   - nil: the stage ran, resumed, or correctly declined. Acknowledge.
//   - a terminating error: this MESSAGE can never drive a stage — it is not a
//     rule publication, or it names something that is not a turn. Redelivering
//     it reproduces the same refusal forever and it carries nothing a repair
//     could rescue.
//   - anything else: nak and redeliver. That deliberately includes an illegal
//     transition, which is a wiring or authoring bug rather than a transient
//     fault: the turn's state is still on the graph, so fixing the pack and
//     restarting lets the redelivery land the hop the player is still owed.
//     Terminating would throw the turn away to spare an operator a log line.
func (r *Runner) handle(ctx context.Context, stage Stage, data []byte) error {
	trigger, err := ParseTrigger(data)
	if err != nil {
		r.logger.Error("stage trigger refused permanently",
			"phase", stage.Phase(), "error", err)
		return natsclient.TerminateDelivery(err)
	}

	if err := stage.Run(ctx, trigger); err != nil {
		var illegal *turn.IllegalTransitionError
		if errors.As(err, &illegal) {
			r.logger.Error("stage trigger describes a transition the turn FSM refuses",
				"phase", stage.Phase(), "turn", trigger.TurnEntityID, "error", err)
		}
		return fmt.Errorf("run the %s stage for turn %s: %w", stage.Phase(), trigger.TurnEntityID, err)
	}
	return nil
}
