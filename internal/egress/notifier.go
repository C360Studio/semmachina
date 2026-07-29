package egress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/rulepack"
)

// The egress consumer's coordinates.
const (
	// ConsumerName is the durable consumer this path binds on the stage stream.
	//
	// Its own name, distinct from the ledger's, because two independent consumers
	// of one subject is the point: the archive must record every resolved turn
	// whether or not anybody was listening, and the player must be told whether or
	// not the archive is keeping up. Sharing a durable would make each one's
	// acknowledgment the other's data loss.
	ConsumerName = "semmachina-player-egress"

	// DefaultAckWait bounds one delivery in flight: a graph read, at most two
	// artifact reads, and a write to whatever sockets are open.
	DefaultAckWait = 30 * time.Second
	// DefaultHeartbeat resets the ack clock while a delivery is in progress.
	DefaultHeartbeat = 10 * time.Second
)

// ConsumerConfig is the durable consumer that learns a turn resolved.
//
// It binds the STAGE stream filtered on `semmachina.turn.resolved`, which the
// rule pack publishes on the transition into `complete` and into `failed` alike —
// which is what makes a failed turn deliverable rather than a silence the player
// has to interpret.
//
// # DeliverPolicy is "new", and it is the one place this path differs from every
// other consumer in the engine
//
// The others are "all", and intake states the reason plainly: an action published
// while the engine was down has no other copy, so skipping it loses a player's
// move permanently. THIS path has another copy. Push is the non-durable half by
// design — the durable answer is Results, which reads the same turn out of the
// graph by turn id, by action id, or as the player's latest, whenever they ask.
//
// So "all" buys nothing here, including the case it looks like it buys. A turn
// that resolved while this process was down IS still delivered on return, and
// that is the ACKNOWLEDGMENT FLOOR's doing, not the policy's: a durable consumer
// resumes from its floor, and DeliverPolicy never applies again after creation.
//
// What "all" would cost is unbounded and structural. `TURN_STAGES` is
// limits-based with no MaxAge and no MaxBytes (stage.StreamConfig), so it holds
// every resolved-turn notification the campaign has ever published — and first
// creation is not only a fresh deployment. It is also this path shipping
// mid-campaign, and a durable deleted and recreated. In either case the consumer
// would replay the campaign's entire resolved history: one graph read plus up to
// two artifact reads per historical turn, and any connected player handed their
// whole back-catalogue as live results. Nobody is watching for a turn that
// resolved last month.
//
// MaxDeliver is unlimited, and the poison case is handled by TERMINATING rather
// than by a cap — see Handle. A cap would drop a delivery silently at attempt N;
// terminating drops it loudly at attempt one, and a permanent failure that naks
// forever would hold the consumer on one message while every later player waits
// behind it.
func ConsumerConfig() natsclient.StreamConsumerConfig {
	return natsclient.StreamConsumerConfig{
		StreamName:    rulepack.StageStream,
		ConsumerName:  ConsumerName,
		FilterSubject: rulepack.SubjectResolved,
		DeliverPolicy: "new",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       DefaultAckWait,
	}
}

// Consumer is the durable at-least-once consumption surface this path binds. It
// is the framework's own: ConsumeDurable owns the ack, the nak-with-delay, the
// terminate-on-permanent-error and the heartbeat.
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

// Notifier pushes each resolved turn's result to the player it belongs to.
//
// It is the PUSH half only, and that is load-bearing rather than modest. The
// durable guarantee is Results, which answers the same question from the graph
// whenever a player asks; this exists so a player who is watching does not have
// to. Every decision here leans on that — an absent player is acknowledged rather
// than queued, a turn that can never be delivered is terminated rather than
// retried forever, and the consumer starts at "new" rather than replaying
// history — because none of them can lose an answer that is still sitting in
// ENTITY_STATES.
type Notifier struct {
	results   *Results
	router    *Router
	consumer  Consumer
	logger    *slog.Logger
	heartbeat time.Duration
}

// NotifierOption configures a Notifier.
type NotifierOption func(*Notifier)

// WithNotifierLogger sets the notifier's logger.
func WithNotifierLogger(logger *slog.Logger) NotifierOption {
	return func(n *Notifier) {
		if logger != nil {
			n.logger = logger
		}
	}
}

// WithNotifierHeartbeat overrides the in-flight heartbeat.
func WithNotifierHeartbeat(d time.Duration) NotifierOption {
	return func(n *Notifier) { n.heartbeat = d }
}

// NewNotifier builds the push path.
func NewNotifier(results *Results, router *Router, consumer Consumer, opts ...NotifierOption) (*Notifier, error) {
	switch {
	case results == nil:
		return nil, errors.New("the egress notifier requires a result surface")
	case router == nil:
		return nil, errors.New("the egress notifier requires a router")
	case consumer == nil:
		return nil, errors.New("the egress notifier requires a stream consumer")
	}
	notifier := &Notifier{
		results: results, router: router, consumer: consumer,
		logger: slog.Default(), heartbeat: DefaultHeartbeat,
	}
	for _, opt := range opts {
		opt(notifier)
	}
	if notifier.heartbeat <= 0 {
		return nil, errors.New("the egress notifier requires a positive heartbeat")
	}
	return notifier, nil
}

// Start binds the resolved-turn consumer.
//
// It creates no stream. The stage stream is the rule pack's and is ensured by the
// stage runner at boot; a component that created a stream it does not own would
// be deciding that stream's limits by winning a boot race.
func (n *Notifier) Start(ctx context.Context) error {
	if err := n.consumer.ConsumeDurable(ctx, ConsumerConfig(), n.heartbeat, n.Handle); err != nil {
		return fmt.Errorf("bind the player-egress consumer: %w", err)
	}
	return nil
}

// Handle delivers one resolved turn. Its return value IS the ack decision.
//
//   - nil: the result reached every connection the player had, or they had none.
//     Acknowledge either way — see Router.Deliver for why an absent player is not
//     a failure.
//   - a terminating error: this MESSAGE can never deliver anything. It is not a
//     rule publication, it names something that is not a turn, or the turn's own
//     record is one no result composes from. Redelivering reproduces the refusal
//     forever, and holding the consumer on it would make one broken turn into a
//     delivery outage for every player behind it.
//   - anything else: nak and redeliver.
//
// The split matters more here than in the archive, and in the opposite direction.
// The ledger naks a corrupt turn forever because a manifest is that turn's only
// permanent record and losing it is unrecoverable; a missed PUSH is recoverable by
// definition — the player retrieves the same result from the same graph — so the
// cost of terminating is a notification, and the cost of not terminating is every
// later player's notification.
func (n *Notifier) Handle(ctx context.Context, data []byte) error {
	event, err := ledger.ParseResolvedEvent(data)
	if err != nil {
		n.logger.Error("resolved-turn notification refused permanently", "error", err)
		return natsclient.TerminateDelivery(err)
	}

	delivery, err := n.results.ByTurn(ctx, event.TurnID)
	switch {
	case errors.Is(err, ErrNoResult):
		// The notification arrived before this reader saw the terminal phase.
		// ENTITY_STATES is read through graph-ingest's query surface and the rule
		// fired off a KV watch, so the two can disagree for an instant. Transient,
		// and the redelivery is the whole answer.
		return fmt.Errorf("turn %s has not resolved yet from this reader's view: %w", event.TurnID, err)
	case errors.Is(err, ErrTurnNotFound):
		n.logger.Error("a resolved-turn notification names a turn that does not exist",
			"turn", event.TurnEntityID, "error", err)
		return natsclient.TerminateDelivery(err)
	case errors.Is(err, ErrNarrationMissing):
		// A reference to prose nobody stored. The write ordering makes this a
		// correctness bug rather than a race, and no redelivery invents the
		// object — so it is loud and terminal rather than an endless retry that
		// blocks every later delivery.
		n.logger.Error("a resolved turn references narration that is not in the store; it cannot be delivered",
			"turn", event.TurnEntityID, "error", err)
		return natsclient.TerminateDelivery(err)
	case err != nil:
		return fmt.Errorf("compose the result for turn %s: %w", event.TurnEntityID, err)
	}

	result, err := n.router.Deliver(ctx, delivery)
	if err != nil {
		return fmt.Errorf("deliver turn %s: %w", event.TurnEntityID, err)
	}
	n.logger.Info("turn result delivered",
		"turn", event.TurnEntityID, "player", result.PlayerID, "recipients", result.Recipients)
	return nil
}
