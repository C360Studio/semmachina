package turn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/payload"
)

// The player-action stream's coordinates.
//
// A player action is a REQUEST, so it lives on a JetStream stream and not in KV:
// a restart must resume from the last acknowledgment rather than re-deliver
// every action ever taken. Run the four tests and all four agree — re-processing
// an accepted action would be wrong, exactly one consumer should handle it, it
// has real side effects, and it is something the player asked for rather than a
// fact about the world. The turn it produces is the fact, and that goes to
// ENTITY_STATES.
const (
	// ActionStream is the durable home of submitted player actions.
	ActionStream = "PLAYER_ACTIONS"
	// ActionSubjectPrefix is the stream's subject space. Every ingress adapter —
	// WebSocket now; Slack, email, SMS later — publishes the same canonical
	// payload under it, which is what makes adding a channel a new component
	// rather than an engine change.
	ActionSubjectPrefix = "player.action.>"
	// ActionSubject is the subject an ingress adapter publishes on.
	ActionSubject = "player.action.submitted"
	// IntakeConsumer is the durable consumer name. It is durable and stable
	// because the ack floor IS the resume point.
	IntakeConsumer = "semmachina-turn-intake"
)

// Ack timings.
//
// AckWait bounds how long a delivery may be in flight before the server assumes
// the consumer died; the heartbeat resets that clock while a turn is genuinely
// being created, so a slow graph write does not manufacture a duplicate
// delivery. Upstream enforces heartbeat*2 <= AckWait and refuses a violating
// config at start, so these two are chosen together.
//
// Neither is a pacing assumption. They bound the processing of ONE delivery, not
// the gap between a player's turns: an idle consumer with nothing in flight has
// no clock running at all.
const (
	// DefaultAckWait is how long one delivery may be in flight.
	DefaultAckWait = 30 * time.Second
	// DefaultHeartbeat resets the ack clock while the handler is working.
	DefaultHeartbeat = 10 * time.Second
)

// Consumer is the durable at-least-once consumption surface intake needs.
//
// It is the framework's own, not a hand-rolled loop: ConsumeDurable owns the
// ack, the nak-with-delay, the terminate-on-permanent-error, and the in-progress
// heartbeat. Re-deriving those here would be re-deriving the exact semantics the
// turn's correctness depends on.
type Consumer interface {
	ConsumeDurable(
		ctx context.Context,
		cfg natsclient.StreamConsumerConfig,
		heartbeat time.Duration,
		handler func(context.Context, []byte) error,
	) error
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Consumer = (*natsclient.Client)(nil)

// Intake turns delivered player actions into turns.
type Intake struct {
	recorder  *Recorder
	decoder   *message.Decoder
	consumer  Consumer
	stream    string
	subject   string
	name      string
	ackWait   time.Duration
	heartbeat time.Duration
}

// IntakeOption configures an Intake.
type IntakeOption func(*Intake)

// WithAckTimings overrides the delivery timings. Tests use it to exercise an
// idle gap longer than the ack deadline in bounded wall-clock time.
func WithAckTimings(ackWait, heartbeat time.Duration) IntakeOption {
	return func(i *Intake) {
		i.ackWait = ackWait
		i.heartbeat = heartbeat
	}
}

// WithStream overrides the stream and subject filter the consumer binds to.
func WithStream(stream, subject string) IntakeOption {
	return func(i *Intake) {
		i.stream = stream
		i.subject = subject
	}
}

// WithConsumerName overrides the durable consumer name.
func WithConsumerName(name string) IntakeOption {
	return func(i *Intake) { i.name = name }
}

// NewIntake builds the intake over a durable consumer and a payload decoder.
//
// The decoder is required rather than optional because a registry-bound decoder
// is the only thing that turns wire bytes into a *payload.PlayerAction: without
// it a binary that forgot RegisterPayloads would consume actions, decode
// nothing, and silently accept no turns.
func NewIntake(
	consumer Consumer,
	recorder *Recorder,
	decoder *message.Decoder,
	opts ...IntakeOption,
) (*Intake, error) {
	if consumer == nil {
		return nil, errors.New("turn intake requires a stream consumer")
	}
	if recorder == nil {
		return nil, errors.New("turn intake requires a turn recorder")
	}
	if decoder == nil {
		return nil, errors.New("turn intake requires a payload decoder")
	}
	intake := &Intake{
		recorder:  recorder,
		decoder:   decoder,
		consumer:  consumer,
		stream:    ActionStream,
		subject:   ActionSubject,
		name:      IntakeConsumer,
		ackWait:   DefaultAckWait,
		heartbeat: DefaultHeartbeat,
	}
	for _, opt := range opts {
		opt(intake)
	}
	if intake.stream == "" || intake.name == "" {
		return nil, errors.New("turn intake requires a stream and a durable consumer name")
	}
	if intake.ackWait <= 0 || intake.heartbeat <= 0 {
		return nil, errors.New("turn intake requires positive ack timings")
	}
	return intake, nil
}

// ConsumerConfig is the durable consumer this intake binds.
//
// DeliverPolicy is "all", deliberately. With "new", an action published while
// the engine was down would be skipped on the next boot — silently, and
// permanently. Play at email cadence means actions arrive when the player is
// awake and not when the engine is; the durable consumer's ack floor is what
// prevents replay, and the deliver policy only decides where a BRAND NEW
// consumer starts.
//
// MaxDeliver is unlimited because a transport failure must not drop a player's
// move: the alternative is a delivery ceiling that silently discards an action
// after N attempts, and the Nth attempt is not a better moment to give up than
// the first.
//
// Termination covers ONE permanent class, not all of them. Handle terminates
// exactly on *RejectedActionError — an action that can never become a turn — and
// everything else naks and redelivers on a 30-second delay, forever. Two of
// those redeliver-forever cases are deterministic and will never succeed on
// their own:
//
//   - the turn already exists, and reading its phase fails as a RecordError:
//     the entity holds two phase values, none, or is a referential stub. The
//     record is corrupt and the next redelivery reads the same corrupt record.
//   - graph-ingest deterministically refuses the create — an unregistered
//     predicate, a rejected envelope. Accept's default branch wraps that as an
//     ordinary error, because from here a refusal is indistinguishable from the
//     transport failure this policy exists to survive.
//
// Redelivering those forever is the deliberate choice. Both are RECOVERABLE by
// repairing the thing that is broken — the entity, the registration, the
// deployment — and the next redelivery then lands the turn the player is still
// owed; a terminate would have thrown the move away to spare an operator a log
// line. Neither wedges the consumer: a nak'd delivery does not block the ones
// behind it, and each costs one ack-pending slot against the server's default
// ceiling (MaxAckPending is left unset here), so one stuck action does not stop
// the next player from acting. Both are observable without archaeology —
// NumRedelivered and NumAckPending on the consumer stay non-zero, and
// ConsumeDurable logs a WARN on every attempt.
func (i *Intake) ConsumerConfig() natsclient.StreamConsumerConfig {
	return natsclient.StreamConsumerConfig{
		StreamName:    i.stream,
		ConsumerName:  i.name,
		FilterSubject: i.subject,
		DeliverPolicy: "all",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       i.ackWait,
	}
}

// Start binds the durable consumer and returns once it is running.
//
// ConsumeDurable acknowledges a delivery exactly when the handler returns nil,
// which is what makes "acknowledge only after the turn entity exists" a property
// of Handle's return value rather than a comment.
func (i *Intake) Start(ctx context.Context) error {
	return i.consumer.ConsumeDurable(ctx, i.ConsumerConfig(), i.heartbeat, i.Handle)
}

// Handle processes one delivered action.
//
// The return value IS the ack decision, and there are exactly three of them:
//
//   - nil: the turn entity exists. Acknowledge. This covers both a fresh accept
//     and a duplicate delivery landing on a turn that already exists — the
//     second is a no-op, and acknowledging it is the point.
//   - a terminating error: this delivery can never succeed. The message is
//     TERMED, not retried, because redelivering an action that can never compose
//     a turn only reproduces the same refusal every 30 seconds forever.
//   - any other error: treated as transient. The message is nak'd and
//     redelivered, and the player's action is not lost. See ConsumerConfig for
//     which deterministic failures deliberately land here anyway.
//
// Nothing is published here. The turn entity landing in ENTITY_STATES is itself
// the trigger the rule pack matches on, which is the facts-vs-requests split
// doing its job: the request ends at the ack, and the fact carries the turn
// forward.
func (i *Intake) Handle(ctx context.Context, data []byte) error {
	action, err := i.decode(data)
	if err != nil {
		return natsclient.TerminateDelivery(err)
	}

	if _, err := i.recorder.Accept(ctx, action); err != nil {
		var rejected *RejectedActionError
		if errors.As(err, &rejected) {
			// A rejected action has no turn entity, so it has nowhere to record
			// its own failure — the failed phase is a fact ABOUT a turn, and
			// this one never became one. Terminating is the honest outcome here,
			// and it is also the QUIETEST outcome anywhere in this component.
			//
			// The observability runs the opposite way from the intuition. A
			// nak-forever — the class this branch is not — repeats a WARN every
			// 30 seconds and holds NumRedelivered and NumAckPending non-zero, so
			// it announces itself on the consumer's own counters. A TERM has no
			// counter at all: jetstream.ConsumerInfo carries Delivered, AckFloor,
			// NumAckPending, NumRedelivered, NumWaiting and NumPending, and
			// nothing that counts terminations. It surfaces as ONE WARN from
			// ConsumeDurable — the same line a nak logs, so even the shape does
			// not distinguish them — plus a server advisory nothing subscribes
			// to. So the branch that merely delays a turn is loud, and the branch
			// that permanently drops a player's move is silent.
			//
			// That is why refusal cannot END here. A structurally invalid action
			// has to fail at INGRESS, where a player is still connected to be
			// told: the adapter validates the canonical action before it
			// publishes, and this branch is the backstop for what gets past it —
			// a misrouted publisher, an envelope no adapter built.
			return natsclient.TerminateDelivery(err)
		}
		return err
	}
	return nil
}

// decode turns wire bytes into the canonical action.
//
// Both failures here are permanent by construction: bytes that do not decode
// today will not decode on the tenth redelivery, and a message of the wrong type
// on this subject is a misrouted publisher rather than a transient fault.
func (i *Intake) decode(data []byte) (*payload.PlayerAction, error) {
	msg, err := i.decoder.Decode(data)
	if err != nil {
		return nil, &RejectedActionError{Err: fmt.Errorf("decode player action: %w", err)}
	}
	action, ok := msg.Payload().(*payload.PlayerAction)
	if !ok {
		return nil, &RejectedActionError{Err: fmt.Errorf(
			"message on the player-action stream carries a %T, not a player action; only ingress adapters "+
				"publish here", msg.Payload())}
	}
	return action, nil
}
