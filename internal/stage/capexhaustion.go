package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go"

	"github.com/c360studio/semmachina/internal/persona"
)

// The agentic-loop's failure notification.
//
// Upstream publishes a LoopFailedEvent "for reactive workflows to observe" when
// a loop ends without succeeding, which makes this the intended seam rather than
// a back door: the alternative — matching rules on the loop entity's own state
// triples — would put the turn loop's recovery on predicates the agentic-loop
// owns and can renumber.
const (
	// LoopFailedSubject is the wildcard every loop failure arrives on. The
	// loop id is the trailing token.
	LoopFailedSubject = "agent.failed.>"
	// ReasonMaxIterations is upstream's uniform reason for a loop that used its
	// whole iteration budget without a terminal exit. It is a CONSTANT rather
	// than a string match on the error text — upstream introduced the typed
	// sentinel and this reason specifically so reactive consumers would stop
	// matching on prose.
	ReasonMaxIterations = "max_iterations"
)

// Subscriber is the core-NATS subscription surface the cap watcher needs.
//
// Core NATS rather than a durable consumer, deliberately. This is a
// NOTIFICATION about a fact the agentic loop has already recorded, not a request
// somebody has to work through: the loop's failure is durable in its own state,
// and the turn's recovery does not depend on this message arriving — a turn left
// mid-stage is picked up by the rule pack's on_recovery replay on the next boot.
// What the subscription buys is that the player does not wait for a restart.
type Subscriber interface {
	Subscribe(
		ctx context.Context,
		subject string,
		handler func(context.Context, *nats.Msg),
	) (*natsclient.Subscription, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Subscriber = (*natsclient.Client)(nil)

// CapWatcher turns an exhausted persona loop into an ended turn.
//
// This is the explicit half of bounded cognition (M5), and it is the half that
// is easy to leave out because nothing fails without it. The cap itself is
// upstream's — the loop counts its own iterations and stops — but a loop that
// stops having produced nothing leaves a turn sitting in `adjudicating` with a
// player waiting on it forever. "The cap prevents a runaway" and "the cap
// prevents a stall" are different claims, and only the first is true of the
// cap alone.
//
// It reads the turn's identity off the failed loop's METADATA, which is the same
// injection channel the terminal tools read: the spawner stamps it, the loop
// carries it onto its failure event, and the model can never write to it. A
// failure event carrying no engine identity is somebody else's loop and is
// ignored.
type CapWatcher struct {
	subscriber Subscriber
	decoder    *message.Decoder
	failer     persona.TurnFailer
	details    persona.DetailStore
	subject    string
	logger     *slog.Logger
}

// CapWatcherOption configures a CapWatcher.
type CapWatcherOption func(*CapWatcher)

// WithCapWatcherLogger sets the watcher's logger.
func WithCapWatcherLogger(logger *slog.Logger) CapWatcherOption {
	return func(w *CapWatcher) {
		if logger != nil {
			w.logger = logger
		}
	}
}

// WithCapWatcherSubject overrides the subject the watcher binds to.
func WithCapWatcherSubject(subject string) CapWatcherOption {
	return func(w *CapWatcher) {
		if subject != "" {
			w.subject = subject
		}
	}
}

// NewCapWatcher builds the loop-failure watcher.
func NewCapWatcher(
	subscriber Subscriber,
	decoder *message.Decoder,
	failer persona.TurnFailer,
	details persona.DetailStore,
	opts ...CapWatcherOption,
) (*CapWatcher, error) {
	switch {
	case subscriber == nil:
		return nil, errors.New("the cap watcher requires a subscriber")
	case decoder == nil:
		return nil, errors.New("the cap watcher requires a payload decoder")
	case failer == nil:
		return nil, errors.New("the cap watcher requires a turn recorder")
	case details == nil:
		return nil, errors.New("the cap watcher requires a detail store")
	}
	watcher := &CapWatcher{
		subscriber: subscriber, decoder: decoder, failer: failer, details: details,
		subject: LoopFailedSubject, logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(watcher)
	}
	return watcher, nil
}

// Start binds the subscription and returns once it is live.
//
// A handler error is LOGGED rather than returned, because there is nobody to
// return it to: core NATS has no ack. The turn it could not end stays in its
// stage and is picked up by the rule pack's recovery replay on the next boot,
// which is the property that makes it safe for this watcher to be
// best-effort — it is a shortcut past a restart, never the only path.
func (w *CapWatcher) Start(ctx context.Context) (*natsclient.Subscription, error) {
	subscription, err := w.subscriber.Subscribe(ctx, w.subject, func(msgCtx context.Context, msg *nats.Msg) {
		if err := w.Handle(msgCtx, msg.Data); err != nil {
			w.logger.Error("loop failure could not be recorded on its turn",
				"subject", msg.Subject, "error", err)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", w.subject, err)
	}
	return subscription, nil
}

// Handle records one loop failure against the turn it belonged to.
//
// Only cap exhaustion is recorded, and that boundary is deliberate rather than
// lazy: `persona-cap-exhausted` is the one closed failure reason the vocabulary
// declares for a persona, so a loop that failed for any other cause has no code
// to record and inventing one here would put an unclosed reason on rule-matching
// surface. Those failures are logged loudly instead — see the note in the
// package doc, and the follow-up it names.
func (w *CapWatcher) Handle(ctx context.Context, data []byte) error {
	event, err := w.decode(data)
	if err != nil {
		return err
	}
	if event == nil {
		return nil
	}

	identity, err := persona.IdentityFrom(event.Metadata)
	if err != nil {
		// Not one of ours. Every loop this engine spawns carries the engine's
		// injected identity, so a failure event without it belongs to some other
		// producer sharing the broker.
		w.logger.Debug("loop failure carries no turn identity; ignoring",
			"loop", event.LoopID, "role", event.Role, "reason", event.Reason)
		return nil
	}
	if event.Reason != ReasonMaxIterations {
		w.logger.Error("persona loop failed for a reason the turn vocabulary has no code for; the turn is "+
			"left in its stage",
			"loop", event.LoopID, "role", event.Role, "reason", event.Reason,
			"turn", identity.TurnEntityID, "error", event.Error)
		return nil
	}

	role, err := roleOf(event.Role)
	if err != nil {
		return err
	}
	exhaustion := persona.CapExhaustion{
		Role:       role,
		LoopID:     event.LoopID,
		Iterations: event.Iterations,
		LastError:  event.Error,
	}

	transition, err := persona.RecordCapExhausted(ctx, w.failer, w.details, identity, exhaustion)
	if err != nil {
		// A DetailLostError says the turn IS resolved and only its explanation
		// is missing. Reading `err != nil` as "still needs running" here would
		// re-bill a persona that already ran out of budget, which is precisely
		// the spend this whole path exists to stop.
		var lost *persona.DetailLostError
		if errors.As(err, &lost) {
			w.logger.Error("turn ended on cap exhaustion without its explanation",
				"turn", identity.TurnEntityID, "phase", lost.Transition.Phase, "error", err)
			return nil
		}
		return fmt.Errorf("record cap exhaustion for turn %s: %w", identity.TurnEntityID, err)
	}
	w.logger.Warn("persona exhausted its iteration budget; the turn ended explicitly",
		"turn", identity.TurnEntityID, "role", role, "loop", event.LoopID,
		"iterations", event.Iterations, "outcome", transition.Outcome, "phase", transition.Phase)
	return nil
}

// decode reads a loop-failure event off the wire, reporting anything else as a
// nil event rather than an error.
func (w *CapWatcher) decode(data []byte) (*agentic.LoopFailedEvent, error) {
	msg, err := w.decoder.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decode loop failure: %w", err)
	}
	event, ok := msg.Payload().(*agentic.LoopFailedEvent)
	if !ok {
		w.logger.Debug("message on the loop-failure subject is not a loop failure",
			"payload", fmt.Sprintf("%T", msg.Payload()))
		return nil, nil
	}
	return event, nil
}

// roleOf maps the loop's declared role back onto a persona this engine runs.
func roleOf(role string) (persona.Role, error) {
	spec, err := persona.SpecFor(persona.Role(role))
	if err != nil {
		return "", fmt.Errorf(
			"a loop carrying this engine's turn identity reported role %q: %w; the identity is injected by the "+
				"spawner, so a role outside the closed set means the two disagree about what was spawned",
			role, err)
	}
	return spec.Role, nil
}
