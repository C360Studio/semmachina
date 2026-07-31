package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

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
	// LoopFailedSubject is the subject every loop failure arrives on. The loop
	// id is the trailing token, and the filter is a single-token wildcard
	// because that is exactly how upstream declares the port (agentic-loop's
	// `agent.failed` output, subject `agent.failed.*`).
	LoopFailedSubject = "agent.failed.*"
	// LoopFailureConsumer is the durable consumer this engine binds to learn
	// that a persona loop ended without exiting.
	LoopFailureConsumer = "semmachina-loop-failures"
	// ReasonMaxIterations is upstream's uniform reason for a loop that used its
	// whole iteration budget without a terminal exit. It is a CONSTANT rather
	// than a string match on the error text — upstream introduced the typed
	// sentinel and this reason specifically so reactive consumers would stop
	// matching on prose.
	ReasonMaxIterations = "max_iterations"
)

// Ack timings for one loop-failure delivery. The watcher's work is a content
// write and a guarded graph write; the heartbeat resets the clock while that is
// genuinely happening.
const (
	// DefaultFailureAckWait is how long one loop-failure delivery may be in
	// flight.
	DefaultFailureAckWait = 30 * time.Second
	// DefaultFailureHeartbeat resets the ack clock while the watcher is
	// working.
	DefaultFailureHeartbeat = 10 * time.Second
	// failureCatchUpPoll is the broker-state polling cadence used only during
	// boot. Completion is authoritative consumer state, not elapsed time.
	failureCatchUpPoll = 10 * time.Millisecond
)

// The agentic stream's eviction limits.
//
// Both are FINITE, and that is the deliberate contrast with the campaign ledger,
// where every eviction limit is disabled because an archive that forgets a turn
// is not an archive. Nothing on this stream is a record of what happened: a task
// is a request that has been served, and a loop failure older than a week cannot
// rescue a turn anybody is still waiting for. The durable record of a failed
// persona is the turn's own phase and reason on the graph, which never evicts,
// and the stranded-turn pass (internal/resume) is what reads it.
//
// So this stream is a fast path with a finite horizon. Saying so explicitly
// costs nothing today and avoids being the one undeclared stream when upstream
// lands its "every ordinary stream states its age, size, and discard policy"
// requirement.
const (
	// AgentStreamMaxAge is how long agentic traffic is retained.
	AgentStreamMaxAge = 7 * 24 * time.Hour
	// AgentStreamMaxBytes caps the stream's on-disk size. Prompts and
	// completions ride here, so the cap is generous rather than tight; what it
	// buys is that a runaway deployment fills a bounded amount of an
	// instance-per-world box rather than the box.
	AgentStreamMaxBytes = 4 << 30
)

// AgentStreamConfig is the agentic loop's stream as this engine would declare
// it — and this engine does NOT declare it.
//
// The stream belongs to whoever runs the agentic loop. Upstream's own
// milestone subscriber makes the same choice for the same subjects and says so
// plainly: it calls GetStream on AGENT and, finding nothing, logs and no-ops,
// commented "they create the AGENT stream" (agentic/agentrun). Following that
// precedent is not deference — creating it here would be actively wrong, because
// EnsureStream is get-or-create with NO reconcile and upstream's creation path
// defaults MaxBytes to 0, which means unlimited. So "whoever gets there first
// wins" would hand this deployment a size limit or not depending on boot order,
// and the limit is the whole reason this config exists.
//
// It is still exported and still states all three limits, because a deployment
// that owns the stream needs a correct declaration to hand over: the test bridge
// that stands in for the agentic loop creates it from here, which keeps the
// stand-in and the real component describing one stream.
//
// A private stream of ours over `agent.failed.>` is not the alternative. NATS
// rejects a stream whose subjects overlap an existing one — measured:
// `code=400 err_code=10065 subjects overlap with an existing stream` — so it
// would boot cleanly on a machine that has never run an agentic loop and refuse
// to boot on every machine that has.
//
// The name is upstream's shape, not ours: the agentic-loop and agentic-tools
// components declare every port with `stream_name: AGENT`. The subjects are
// upstream's derivation (`agent.>`, from the lower-cased stream name) PLUS the
// tool-dispatch lane (`tool.execute.>` and `tool.result.>`), which that
// derivation does not produce and which both components nevertheless declare on
// this stream. Composing without them is a deployment that starts cleanly and
// cannot run a tool: the loop publishes every tool call onto `tool.execute.*`
// rather than executing it in process, and a subject the stream does not capture
// reaches no consumer. See persona.AgentStreamSubjects for the measurement.
func AgentStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:      TaskStream,
		Subjects:  persona.AgentStreamSubjects(),
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    AgentStreamMaxAge,
		MaxBytes:  AgentStreamMaxBytes,
		// A task's TaskID is also its Nats-Msg-Id. Holding duplicate history for
		// the complete retention horizon closes the publish-ack crash window for
		// every task that can still be present on an ordinary running stream.
		// This is deliberately a bounded local guarantee: DiscardOld under
		// MaxBytes, an operator purge, or a NATS restart after the oldest bytes
		// were evicted can remove the message and its dedup evidence. It is not a
		// universal at-most-once billing claim.
		Duplicates: AgentStreamMaxAge,
		// DiscardOld rather than the ledger's DiscardNew. These are time-shaped
		// events whose value decays: when the cap is reached the oldest agentic
		// traffic is the least useful thing on the stream, while refusing NEW
		// publishes would stop the engine spawning personas at all.
		Discard: jetstream.DiscardOld,
	}
}

// LoopFailureWatcher turns a persona loop that ended without exiting into an
// ended turn.
//
// This is the explicit half of bounded cognition (M5), and it is the half that
// is easy to leave out because nothing fails without it. The cap itself is
// upstream's — the loop counts its own iterations and stops — but a loop that
// stops having produced nothing leaves a turn sitting in `adjudicating` with a
// player waiting on it forever. "The cap prevents a runaway" and "the cap
// prevents a stall" are different claims, and only the first is true of the cap
// alone.
//
// It is not cap-specific any more, and the name is the honest half of that
// change: a loop that fails for ANY reason leaves the turn in exactly the same
// place, and the far commoner reason is a model error rather than an exhausted
// budget. Both endings are recorded, with different closed codes.
//
// # Why a durable consumer and not a core-NATS subscription
//
// The argument for core NATS was false. It read: this is only a shortcut past a
// restart, because a turn left mid-stage is picked up by the rule pack's
// on_recovery replay on the next boot. That replay does not happen. Bootstrap
// replay re-fires a rule that is currently matching AND whose entity has been
// written since the rule last evaluated it, and a turn parked mid-stage
// satisfies neither — measured against a live broker, not inferred (see
// internal/resume's package doc for the probe).
//
// So this notification is not a shortcut past anything: it is the only thing
// that ends the turn before the next boot's stranded-turn pass. There is exactly
// one consumer and a billed consequence behind it, which makes it a request
// rather than a broadcast — and a request that vanishes because its handler was
// restarting is a player waiting for no reason.
//
// It reads the turn's identity off the failed loop's METADATA, which is the same
// injection channel the terminal tools read: the spawner stamps it, the loop
// carries it onto its failure event, and the model can never write to it. A
// failure event carrying no engine identity is somebody else's loop and is
// ignored.
type LoopFailureWatcher struct {
	consumer     Consumer
	streams      StreamReader
	decoder      *message.Decoder
	failer       persona.TurnFailer
	details      persona.DetailStore
	subject      string
	consumerName string
	logger       *slog.Logger
	heartbeat    time.Duration
	ackWait      time.Duration
}

// LoopFailureOption configures a LoopFailureWatcher.
type LoopFailureOption func(*LoopFailureWatcher)

// WithLoopFailureLogger sets the watcher's logger.
func WithLoopFailureLogger(logger *slog.Logger) LoopFailureOption {
	return func(w *LoopFailureWatcher) {
		if logger != nil {
			w.logger = logger
		}
	}
}

// WithLoopFailureSubject overrides the subject filter the watcher binds to.
func WithLoopFailureSubject(subject string) LoopFailureOption {
	return func(w *LoopFailureWatcher) {
		if subject != "" {
			w.subject = subject
		}
	}
}

// WithLoopFailureConsumerName overrides the durable consumer's name. Tests use
// it so parallel worlds sharing one broker do not share one cursor.
func WithLoopFailureConsumerName(name string) LoopFailureOption {
	return func(w *LoopFailureWatcher) {
		if name != "" {
			w.consumerName = name
		}
	}
}

// WithLoopFailureTimings overrides the delivery timings.
func WithLoopFailureTimings(ackWait, heartbeat time.Duration) LoopFailureOption {
	return func(w *LoopFailureWatcher) {
		w.ackWait = ackWait
		w.heartbeat = heartbeat
	}
}

// NewLoopFailureWatcher builds the loop-failure watcher.
func NewLoopFailureWatcher(
	consumer Consumer,
	streams StreamReader,
	decoder *message.Decoder,
	failer persona.TurnFailer,
	details persona.DetailStore,
	opts ...LoopFailureOption,
) (*LoopFailureWatcher, error) {
	switch {
	case consumer == nil:
		return nil, errors.New("the loop-failure watcher requires a stream consumer")
	case streams == nil:
		return nil, errors.New("the loop-failure watcher requires a stream reader")
	case decoder == nil:
		return nil, errors.New("the loop-failure watcher requires a payload decoder")
	case failer == nil:
		return nil, errors.New("the loop-failure watcher requires a turn recorder")
	case details == nil:
		return nil, errors.New("the loop-failure watcher requires a detail store")
	}
	watcher := &LoopFailureWatcher{
		consumer: consumer, streams: streams, decoder: decoder, failer: failer, details: details,
		subject: LoopFailedSubject, consumerName: LoopFailureConsumer, logger: slog.Default(),
		heartbeat: DefaultFailureHeartbeat, ackWait: DefaultFailureAckWait,
	}
	for _, opt := range opts {
		opt(watcher)
	}
	if watcher.heartbeat <= 0 || watcher.ackWait <= 0 {
		return nil, errors.New("the loop-failure watcher requires positive ack timings")
	}
	return watcher, nil
}

// ConsumerConfig is the durable consumer the watcher binds.
//
// DeliverPolicy is "all", deliberately, and it is the property that makes this
// worth moving off core NATS at all: a loop that failed while this engine was
// down must still end its turn, and re-processing is safe by construction — the
// turn recorder DECLINES a terminal turn, so a replayed failure for a turn that
// has since ended writes nothing.
//
// MaxDeliver is unlimited for the reason intake's is: a transport failure must
// not silently drop the one message that ends a turn a player is waiting on, and
// the Nth attempt is not a better moment to give up than the first.
func (w *LoopFailureWatcher) ConsumerConfig() natsclient.StreamConsumerConfig {
	return natsclient.StreamConsumerConfig{
		StreamName:    TaskStream,
		ConsumerName:  w.consumerName,
		FilterSubject: w.subject,
		DeliverPolicy: "all",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       w.ackWait,
	}
}

// Start reads the agentic stream and binds the durable consumer.
//
// It READS rather than creates, following upstream's own subscriber over the
// same subjects; see AgentStreamConfig for why creating it here would be worse
// than merely unnecessary.
//
// It diverges from upstream in what it does when the stream is ABSENT. Upstream
// logs and no-ops, because its milestone subscriber is optional garnish on
// deployments that may legitimately run no agentic component at all. This engine
// has no such shape: the personas ARE the loop, the spawner publishes its tasks
// onto this same stream, and a missing AGENT stream is a deployment where the
// turn loop cannot run at all. Booting quietly into that would trade one loud
// error for a player who waits and an operator who has nothing to read.
//
// The capture check is the second half, and it is not belt-and-braces. An AGENT
// stream created by something else with narrower subjects is returned exactly as
// it is, and every loop failure would then be published into nothing. Failing
// here is loud and at boot; the alternative is a watcher that runs forever and
// never fires.
func (w *LoopFailureWatcher) Start(ctx context.Context) error {
	stream, err := w.streams.GetStream(ctx, TaskStream)
	if err != nil {
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			return fmt.Errorf(
				"the %s stream does not exist, so no persona loop's failure can reach this engine and no persona "+
					"task can leave it; the agentic loop owns that stream and this deployment is not running one: %w",
				TaskStream, err)
		}
		return fmt.Errorf("read the %s stream: %w", TaskStream, err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("read the %s stream's configuration: %w", TaskStream, err)
	}
	if !subjectCovered(w.subject, info.Config.Subjects) {
		return fmt.Errorf(
			"the %s stream exists but its subjects %v do not capture %q, so every persona loop failure would be "+
				"published into nothing and every turn whose loop failed would wait until the next boot's "+
				"stranded-turn pass",
			TaskStream, info.Config.Subjects, w.subject)
	}
	if err := w.consumer.ConsumeDurable(ctx, w.ConsumerConfig(), w.heartbeat, w.Handle); err != nil {
		return fmt.Errorf("bind the loop-failure consumer: %w", err)
	}
	return nil
}

// CatchUp waits until the durable consumer has acknowledged every loop failure
// that preceded this boot. Start must be called first.
//
// Boot uses this barrier before it binds stage-trigger consumers. A queued loop
// failure can therefore terminally record its turn before an older redelivered
// stage trigger gets a chance to spawn the same persona again. NumPending alone
// is insufficient: it reaches zero while the handler is still running, so the
// acknowledgement-pending count is part of the barrier.
func (w *LoopFailureWatcher) CatchUp(ctx context.Context) error {
	stream, err := w.streams.GetStream(ctx, TaskStream)
	if err != nil {
		return fmt.Errorf("read the %s stream while catching up loop failures: %w", TaskStream, err)
	}
	consumer, err := stream.Consumer(ctx, w.consumerName)
	if err != nil {
		return fmt.Errorf("read loop-failure consumer %s: %w", w.consumerName, err)
	}
	ticker := time.NewTicker(failureCatchUpPoll)
	defer ticker.Stop()
	for {
		info, infoErr := consumer.Info(ctx)
		if infoErr != nil {
			return fmt.Errorf("read loop-failure consumer %s state: %w", w.consumerName, infoErr)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("catch up loop failures: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Handle records one loop failure against the turn it belonged to. Its return
// value IS the ack decision.
//
//   - nil: the turn is ended, was already ended, or the message was never ours.
//     Acknowledge.
//   - a terminating error: these BYTES can never end a turn — they are not a
//     decodable payload. Redelivering reproduces the same refusal forever.
//   - anything else: nak and redeliver, forever. That deliberately includes a
//     graph write that failed, because the turn is still waiting and the Nth
//     attempt is not a worse moment to end it than the first.
//
// Both persona endings are recorded, with different closed codes. The non-cap
// branch used to log and return, on the stated grounds that the turn would be
// picked up by the next boot's recovery replay; that replay does not happen, so
// the branch described a rescue nothing performed and the turn waited on a
// player who had no way to know.
func (w *LoopFailureWatcher) Handle(ctx context.Context, data []byte) error {
	event, err := w.decode(data)
	if err != nil {
		w.logger.Error("loop-failure notification refused permanently", "error", err)
		return natsclient.TerminateDelivery(err)
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
	role, err := roleOf(event.Role)
	if err != nil {
		// A loop carrying THIS engine's identity and a role it does not run is a
		// disagreement between the spawner and the loop, not a poison message:
		// the turn is real and still waiting, so the delivery is naked rather
		// than terminated and the mismatch is loud on every attempt.
		return err
	}

	if event.Reason == ReasonMaxIterations {
		return w.recordCapExhaustion(ctx, identity, role, event)
	}
	return w.recordLoopFailure(ctx, identity, role, event)
}

// recordCapExhaustion ends a turn whose persona used its whole budget.
func (w *LoopFailureWatcher) recordCapExhaustion(
	ctx context.Context,
	identity persona.Identity,
	role persona.Role,
	event *agentic.LoopFailedEvent,
) error {
	transition, err := persona.RecordCapExhausted(ctx, w.failer, w.details, identity, persona.CapExhaustion{
		Role:       role,
		LoopID:     event.LoopID,
		Iterations: event.Iterations,
		LastError:  event.Error,
	})
	if err != nil {
		return w.reportRecordError(err, identity, "cap exhaustion")
	}
	w.logger.Warn("persona exhausted its iteration budget; the turn ended explicitly",
		"turn", identity.TurnEntityID, "role", role, "loop", event.LoopID,
		"iterations", event.Iterations, "outcome", transition.Outcome, "phase", transition.Phase)
	return nil
}

// recordLoopFailure ends a turn whose persona loop failed for any other reason.
func (w *LoopFailureWatcher) recordLoopFailure(
	ctx context.Context,
	identity persona.Identity,
	role persona.Role,
	event *agentic.LoopFailedEvent,
) error {
	transition, err := persona.RecordLoopFailed(ctx, w.failer, w.details, identity, persona.LoopFailure{
		Role:       role,
		LoopID:     event.LoopID,
		Reason:     event.Reason,
		Iterations: event.Iterations,
		LastError:  event.Error,
	})
	if err != nil {
		return w.reportRecordError(err, identity, "loop failure")
	}
	w.logger.Error("persona loop failed without exiting; the turn ended explicitly",
		"turn", identity.TurnEntityID, "role", role, "loop", event.LoopID,
		"reason", event.Reason, "iterations", event.Iterations,
		"outcome", transition.Outcome, "phase", transition.Phase, "error", event.Error)
	return nil
}

// reportRecordError decides whether a failed recording still acknowledges.
//
// A DetailLostError says the turn IS resolved and only its explanation is
// missing. Reading `err != nil` as "still needs handling" here would redeliver
// forever over a content-store fault that has nothing to do with the turn, and
// on the cap path it would re-bill a loop that already ran out of budget.
func (w *LoopFailureWatcher) reportRecordError(err error, identity persona.Identity, kind string) error {
	var lost *persona.DetailLostError
	if errors.As(err, &lost) {
		w.logger.Error("turn ended without its explanation",
			"turn", identity.TurnEntityID, "kind", kind, "phase", lost.Transition.Phase, "error", err)
		return nil
	}
	return fmt.Errorf("record %s for turn %s: %w", kind, identity.TurnEntityID, err)
}

// decode reads a loop-failure event off the wire, reporting anything else as a
// nil event rather than an error.
func (w *LoopFailureWatcher) decode(data []byte) (*agentic.LoopFailedEvent, error) {
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

// subjectCovered reports whether a stream's configured subjects capture the
// subject filter this watcher binds.
//
// Wildcard-aware, because the subject this engine must reach is captured by
// upstream's `agent.>` and by an exact `agent.failed.*` and by nothing narrower.
// An exact-string check would refuse the ordinary production shape — an AGENT
// stream created by semstreams' own provisioning — which is the deployment this
// most has to work in.
func subjectCovered(want string, subjects []string) bool {
	wantTokens := strings.Split(want, ".")
	for _, subject := range subjects {
		if covers(strings.Split(subject, "."), wantTokens) {
			return true
		}
	}
	return false
}

// covers reports whether a NATS subject pattern captures every subject another
// pattern can match.
func covers(pattern, want []string) bool {
	for idx, token := range pattern {
		if token == ">" {
			// A full wildcard swallows the remainder.
			return true
		}
		if idx >= len(want) {
			return false
		}
		// `*` matches exactly one token, whatever it is; anything else must be
		// that token literally. A literal pattern token cannot capture a `*` in
		// the wanted subject, because the wildcard admits values the literal
		// does not.
		if token != "*" && token != want[idx] {
			return false
		}
	}
	return len(pattern) == len(want)
}
