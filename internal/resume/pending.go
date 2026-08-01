package resume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Settle timings for the work queues.
//
// The pass reads a SET of queued work and then acts on what is missing from it,
// so it must not read a set that is still being written. At boot the only writer
// is the rule engine's bootstrap replay, and upstream exposes no signal for when
// that has drained — its timing relative to the processor's Start is a race,
// measured landing 1.02 seconds after Start returned and measured having already
// landed when Start returned. Sleeping "long enough" for a race is the same class
// of guess this whole pass exists to stop making, so the quiet window is observed
// instead.
const (
	// DefaultSettleQuiet is how long every work queue must hold still before the
	// queued set is treated as readable.
	DefaultSettleQuiet = 3 * time.Second
	// DefaultSettleTimeout bounds the wait. A queue that will not hold still is
	// one something is actively publishing to, which means this pass has been run
	// at the wrong point in the boot sequence — reporting that is worth more than
	// acting on a moving target.
	DefaultSettleTimeout = 60 * time.Second
)

// maxQueueScan bounds one subject's read.
//
// Neither queue evicts what it holds — the stage stream deliberately so, because
// a trigger that expired unread is a turn that stops mid-loop with no error
// anywhere — so both hold everything ever published. Only the unacknowledged tail
// is read (see WorkQueues.Pending), but the bound is still stated, because "read
// everything" is how a boot pass becomes O(campaign history) without anybody
// deciding it should be.
const maxQueueScan = 4096

// fetchWait bounds one subject's read when the tail is shorter than the batch.
const fetchWait = 500 * time.Millisecond

// pollInterval is how often the settle check re-reads the queue heads.
const pollInterval = 250 * time.Millisecond

// WorkQueues is the MEASURED view of what the substrate is still holding for the
// turn loop.
//
// It exists because the alternative was an inference, and the inference was wrong
// twice. Asking the rule pack whether a turn's hop had been published answers a
// question about RULES; what this pass needs is a fact about MESSAGES, and the
// two come apart wherever a rule fires and its publish does not — a circuit-broken
// client, a disconnected one, a refused PubAck, an action-level iteration cap. So
// this reads the queues.
//
// # Every queue that can deliver work for a turn, not one of them
//
// This is the general form, and it is the part to carry forward. "Is work still
// coming for this turn" must be answered across EVERY durable queue that can
// deliver work for it. Today that is two:
//
//   - TURN_STAGES, carrying stage triggers.
//   - AGENT, carrying persona tasks on agent.task.* — because Spawner.Run
//     acknowledges the stage trigger when it PUBLISHES the task, not when the
//     persona finishes. The real work then sits unacknowledged on AGENT, where
//     upstream redelivers it (AckWait 90s, MaxDeliver 2, a 60s heartbeat, acking
//     only on completion). A pass that read only TURN_STAGES would see nothing
//     queued for a turn whose adjudication is scheduled for redelivery in this
//     very process, re-trigger it with a fresh task id, and buy two to three
//     billed adjudications for one turn — two of them racing to write
//     turn.verdict.* through a last-write-wins merge.
//
// Instance-per-world does not cover that, and the reason is worth internalising:
// the thing still working on the turn is not another process, it is a redelivered
// task arriving in THIS one. The booting process is precisely what resumes it.
//
// A third queue would have to be added here the day one exists, and the question
// to ask of any new durable input is exactly that: can it deliver work for a turn?
//
// # Why AGENT_LOOPS cannot answer this
//
// The agentic loop keeps its own state and `state=running` looks like the answer.
// It is not: only a handler transitions a loop out of running
// (processor/agentic-loop/component.go), so a process that died mid-loop leaves a
// stale `running` entry indistinguishable from a live one. A liveness claim read
// off it would be the same inference this package was rebuilt to remove, wearing
// a KV bucket.
type WorkQueues struct {
	stages  jetstream.Stream
	agent   jetstream.Stream
	quiet   time.Duration
	timeout time.Duration
	now     func() time.Time
}

// WorkQueuesOption configures a WorkQueues.
type WorkQueuesOption func(*WorkQueues)

// WithSettleWindow overrides the quiet window and its bound. Tests use it so a
// pass over a broker nobody is publishing to does not wait out a window sized for
// a real boot.
func WithSettleWindow(quiet, timeout time.Duration) WorkQueuesOption {
	return func(q *WorkQueues) {
		q.quiet = quiet
		q.timeout = timeout
	}
}

// WithQueueClock overrides the settle clock, so the settle logic can be exercised
// without wall-clock waits. Nothing in production sets it.
func WithQueueClock(now func() time.Time) WorkQueuesOption {
	return func(q *WorkQueues) {
		if now != nil {
			q.now = now
		}
	}
}

// NewWorkQueues builds the measured view over both queues that carry turn work.
func NewWorkQueues(stages, agent jetstream.Stream, opts ...WorkQueuesOption) (*WorkQueues, error) {
	if stages == nil {
		return nil, errors.New("the stranded-turn pass requires the stage-trigger stream")
	}
	if agent == nil {
		return nil, errors.New(
			"the stranded-turn pass requires the agentic loop's stream; a persona task is acknowledged when the " +
				"persona FINISHES, so a turn whose adjudication is mid-flight is invisible without it and would " +
				"be re-triggered into a second billed spawn")
	}
	view := &WorkQueues{
		stages: stages, agent: agent,
		quiet: DefaultSettleQuiet, timeout: DefaultSettleTimeout, now: time.Now,
	}
	for _, opt := range opts {
		opt(view)
	}
	if view.quiet <= 0 || view.timeout <= 0 {
		return nil, errors.New("the stranded-turn pass requires a positive settle window")
	}
	if view.quiet > view.timeout {
		return nil, fmt.Errorf(
			"the settle window (%s) is longer than the time allowed to reach it (%s), so the pass could never "+
				"decide the queues had gone quiet", view.quiet, view.timeout)
	}
	return view, nil
}

// Settle blocks until no work has been published on either queue for the quiet
// window.
//
// It is the enforced half of an ordering constraint whose other half cannot be
// enforced from here. The composition MUST have started the rule processor before
// this runs — an unstarted processor makes the queues trivially quiet and its
// replay lands afterwards, on a turn this pass has already called stranded. Given
// that it has started, this is what says its replay has finished, without anybody
// choosing a sleep.
func (q *WorkQueues) Settle(ctx context.Context) error {
	deadline := q.now().Add(q.timeout)
	previous, err := q.heads(ctx)
	if err != nil {
		return err
	}
	quietSince := q.now()

	for {
		if q.now().Sub(quietSince) >= q.quiet {
			return nil
		}
		if q.now().After(deadline) {
			return fmt.Errorf(
				"the %s and %s queues were still being published to after %s, so the queued-work set could not "+
					"be read as a settled fact; the stranded-turn pass runs once the rule processor's bootstrap "+
					"replay has drained and before any player action is accepted, and something is publishing "+
					"outside that window", rulepack.StageStream, persona.TaskStream, q.timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.sleep(pollInterval):
		}

		current, err := q.heads(ctx)
		if err != nil {
			return err
		}
		if current != previous {
			previous = current
			quietSince = q.now()
		}
	}
}

// sleep is the settle loop's only wait, routed through one place so the injected
// clock governs the whole loop rather than half of it.
//
// A half-injectable clock is worse than none: a test that set `now` would control
// the deadline arithmetic and not the waiting, and would then believe it had
// exercised a settle window it had actually slept through in real time.
func (q *WorkQueues) sleep(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// heads is both queues' last sequences, as one comparable value.
func (q *WorkQueues) heads(ctx context.Context) ([2]uint64, error) {
	var heads [2]uint64
	for idx, stream := range [2]jetstream.Stream{q.stages, q.agent} {
		info, err := stream.Info(ctx)
		if err != nil {
			return heads, fmt.Errorf("read a work queue's head: %w", err)
		}
		heads[idx] = info.State.LastSeq
	}
	return heads, nil
}

// Pending returns, per turn entity ID, how many pieces of work are queued for it
// and not yet acknowledged, across every queue that can deliver work for a turn.
//
// # Why it cannot simply read a subject
//
// Neither stream evicts what it holds, so a read of a subject's whole contents
// answers "what was ever published" — which for a campaign with any history is
// every turn it has ever run. What "queued" means is a property of the CONSUMER:
// the messages it has not acknowledged. So each subject is read from its
// consumer's acknowledgement floor, the sequence below which that consumer has
// finished everything.
//
// # Keyed by TURN, deliberately, not by subject
//
// The result sums across subjects and streams, and that is what lets the pass
// leave a turn alone without knowing WHICH hop or WHICH task it is owed. Keying
// by subject would put the pass back in the business of deciding what a turn
// should be waiting on — the inference this package was rebuilt without.
//
// The ephemeral consumers this uses acknowledge NOTHING and carry their own
// cursors, so counting never removes work anybody else would receive.
func (q *WorkQueues) Pending(ctx context.Context) (map[string]int, error) {
	pending := make(map[string]int)

	for _, phase := range rulepack.StagePhases() {
		subject, err := rulepack.SubjectForPhase(phase)
		if err != nil {
			return nil, err
		}
		floor, err := q.stageAckFloor(ctx, phase)
		if err != nil {
			return nil, err
		}
		if err := readQueue(ctx, q.stages, subject, floor, triggerEntityID, pending); err != nil {
			return nil, fmt.Errorf("read the queued %s triggers: %w", phase, err)
		}
	}
	knowledgeFloor, err := q.consumerAckFloor(ctx, rulepack.KnowledgeConsumerName, "knowledge")
	if err != nil {
		return nil, err
	}
	if err := readQueue(ctx, q.stages, rulepack.SubjectKnowledge, knowledgeFloor, triggerEntityID, pending); err != nil {
		return nil, fmt.Errorf("read the queued knowledge triggers: %w", err)
	}
	accusationFloor, err := q.consumerAckFloor(ctx, rulepack.AccusationConsumerName, "accusation")
	if err != nil {
		return nil, err
	}
	if err := readQueue(ctx, q.stages, rulepack.SubjectAccusation, accusationFloor, triggerEntityID, pending); err != nil {
		return nil, fmt.Errorf("read the queued accusation triggers: %w", err)
	}

	floor, err := q.taskAckFloor(ctx)
	if err != nil {
		return nil, err
	}
	if err := readQueue(ctx, q.agent, persona.TaskSubjectFilter, floor, taskEntityID, pending); err != nil {
		return nil, fmt.Errorf("read the queued persona tasks: %w", err)
	}
	return pending, nil
}

// stageAckFloor returns the sequence a stage has finished everything below.
//
// An absent consumer means "read from the beginning", and here that is correct
// rather than convenient: this engine creates the stage consumers itself, with
// DeliverPolicy "all", so a trigger published while one was absent IS delivered
// once it binds. Everything on the subject is therefore genuinely queued.
func (q *WorkQueues) stageAckFloor(ctx context.Context, phase vocabulary.TurnPhase) (uint64, error) {
	return q.consumerAckFloor(ctx, rulepack.StageConsumerName(phase), string(phase)+" stage")
}

func (q *WorkQueues) consumerAckFloor(ctx context.Context, name, label string) (uint64, error) {
	consumer, err := q.stages.Consumer(ctx, name)
	if err != nil {
		if errors.Is(err, jetstream.ErrConsumerNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("read the %s consumer: %w", label, err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the %s consumer: %w", label, err)
	}
	// AckFloor rather than the delivered position, deliberately: a delivery that
	// is out and unacknowledged is still queued work, and a turn whose trigger is
	// being redelivered must not be re-triggered on top of it. Everything at or
	// below the floor is finished; see readQueue for the +1 that follows from
	// "at or below".
	return info.AckFloor.Stream, nil
}

// taskAckFloor returns the sequence the agentic loop has finished every persona
// task below.
//
// # Found by FILTER SUBJECT, not by name
//
// Upstream's consumer name is `agentic-loop-` plus an UNEXPORTED sanitisation of
// the subject, plus an optional operator-configured suffix
// (processor/agentic-loop/component.go). Reconstructing that would be a cross-repo
// coupling on a string this engine cannot see and an operator can change, and its
// failure mode is the worst available: a name nobody binds looks exactly like a
// deployment with no agentic loop, so a drifted guess reports an empty
// acknowledgement floor and confidently concludes nothing is in flight —
// inverting the fact being asked for. semstreams#733 asks for the name.
//
// This does not need it. A consumer is identified by WHAT IT FILTERS, which is
// upstream's declared port subject and an ordinary subject contract — the same
// one this engine's spawner already publishes onto. That is immune to the
// sanitisation, immune to the suffix, and immune to a rename.
//
// # An absent consumer REFUSES rather than reading as empty
//
// Nothing bound to agent.task.* means no persona can ever run: upstream's
// consumers default to DeliverPolicy "new", so a task published while none exists
// is not replayed later — it is simply never delivered. That is a deployment
// where the turn loop cannot work at all, which is the same condition
// stage.LoopFailureWatcher.Start already refuses to boot into. Answering "nothing
// queued" here would be the silent inverse of the fact, and would re-trigger every
// turn whose persona is mid-flight.
//
// The refusal is GLOBAL rather than per-turn on purpose: the condition is a
// property of the deployment, not of any turn, so reporting it once and declining
// to act beats repeating it per turn while still acting on the rest.
//
// # Only DURABLE consumers count, and leaving that out disarmed the pass on the
// one occasion it exists for
//
// readQueue reads a subject through an EPHEMERAL, AckNone consumer carrying THIS
// EXACT FILTER, with a thirty-second inactive threshold. Such a reader is left on
// the stream at the floor it started from, and this minimum was taken over every
// consumer on the filter — so a SECOND reading inside that window is taken against
// the FIRST reading's stale cursor. Work the loop finished in between is then
// reported as still queued, for the turns that own it.
//
// The direction of the error is the conservative one — a turn reads as queued
// rather than as stranded — which is why nothing ever failed. What it costs is the
// pass itself: a turn whose work really is gone reads as owed to somebody, is
// counted and left alone, and stays stranded with a player waiting. And the window
// is precisely a crash-restart: a process that dies and comes back inside half a
// minute runs its pass against the floor its own previous pass left behind.
//
// Restricting to durables is not a workaround but the definition made explicit:
// "work above ANY consumer's floor is still owed to somebody" is a statement about
// consumers that OWE something. An ephemeral, AckNone cursor owes nothing, cannot
// acknowledge, and disappears on its own.
// TestWorkQueues_AreNotDisarmedByTheirOwnPreviousReading is the measurement.
func (q *WorkQueues) taskAckFloor(ctx context.Context) (uint64, error) {
	lister := q.agent.ListConsumers(ctx)
	var (
		floor uint64
		found bool
	)
	for info := range lister.Info() {
		if info == nil || info.Config.Durable == "" || !consumerCarries(info.Config, persona.TaskSubjectFilter) {
			continue
		}
		// The MINIMUM across every consumer on the filter. More than one means
		// more than one loop is bound, and work above any of their floors is
		// still owed to somebody.
		if !found || info.AckFloor.Stream < floor {
			floor = info.AckFloor.Stream
		}
		found = true
	}
	if err := lister.Err(); err != nil {
		return 0, fmt.Errorf("list the %s stream's consumers: %w", persona.TaskStream, err)
	}
	if !found {
		return 0, fmt.Errorf(
			"no DURABLE consumer on the %s stream filters %q, so this pass cannot tell whether a persona task "+
				"is still in flight. Upstream's task consumers deliver only NEW messages, so nothing bound to "+
				"that subject means no persona can ever run — and answering 'nothing queued' would re-trigger "+
				"every turn whose persona is mid-flight into a second billed spawn",
			persona.TaskStream, persona.TaskSubjectFilter)
	}
	return floor, nil
}

// consumerCarries reports whether a consumer's filter is the subject space asked
// about.
//
// Both filter shapes are checked because a consumer may declare either — they are
// mutually exclusive on the wire, and which one a component uses is not this
// engine's business.
func consumerCarries(cfg jetstream.ConsumerConfig, want string) bool {
	if cfg.FilterSubject == want {
		return true
	}
	for _, subject := range cfg.FilterSubjects {
		if subject == want {
			return true
		}
	}
	return false
}

// readQueue adds one subject's unacknowledged messages to the set, keyed by the
// turn each names.
func readQueue(
	ctx context.Context,
	stream jetstream.Stream,
	subject string,
	floor uint64,
	turnOf func([]byte) (string, error),
	pending map[string]int,
) error {
	cfg := jetstream.ConsumerConfig{
		FilterSubject: subject,
		// EPHEMERAL is what makes this safe: no Durable name, so this reader has
		// its own cursor and cannot advance anybody else's. Binding a component's
		// own consumer to count would consume the work it was counting.
		//
		// AckNone on top of that is hygiene rather than the guarantee — it keeps
		// the read from parking N messages as ack-pending on streams other
		// components watch, and from ever redelivering to itself.
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: 30 * time.Second,
	}
	if floor == 0 {
		cfg.DeliverPolicy = jetstream.DeliverAllPolicy
	} else {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		// floor + 1, and the +1 is the whole meaning of "floor": AckFloor.Stream
		// is the last sequence that WAS acknowledged, so a read that begins at it
		// re-reads finished work. One message per subject, which is exactly
		// enough to report the most recently completed turn as still waiting —
		// and a turn reported as waiting is a turn this pass leaves alone or
		// ends, so the off-by-one is silent in the direction that matters.
		cfg.OptStartSeq = floor + 1
	}

	consumer, err := stream.CreateConsumer(ctx, cfg)
	if err != nil {
		return err
	}
	batch, err := consumer.Fetch(maxQueueScan, jetstream.FetchMaxWait(fetchWait))
	if err != nil {
		return err
	}
	scanned := 0
	for msg := range batch.Messages() {
		scanned++
		entityID, err := turnOf(msg.Data())
		if err != nil {
			// A message naming no turn holds no turn open. The consumer that owns
			// it decides what to do with it; here it is simply not evidence about
			// anybody.
			continue
		}
		pending[entityID]++
	}
	if err := batch.Error(); err != nil {
		return err
	}
	if scanned >= maxQueueScan {
		return fmt.Errorf(
			"the queued messages on %s filled a %d-message read, so the set may be short; a turn missing from a "+
				"short set reads as stranded and would be re-triggered on top of work it already has",
			subject, maxQueueScan)
	}
	return nil
}

// triggerEntityID reads the turn a stage trigger names.
//
// The rule engine's own publication shape, decoded structurally rather than
// through the payload registry, because the rule engine composes this map itself
// and stamps no BaseMessage envelope on it.
func triggerEntityID(data []byte) (string, error) {
	var published struct {
		EntityID string `json:"entity_id"`
	}
	if err := json.Unmarshal(data, &published); err != nil {
		return "", err
	}
	if published.EntityID == "" {
		return "", errors.New("stage trigger names no entity")
	}
	return published.EntityID, nil
}

// taskEntityID reads the turn a persona task is about.
//
// Off the task's METADATA, which is the engine's own injection channel: the
// spawner stamps it, the loop carries it through, and the model can never write
// to it. It is the same field LoopFailureWatcher reads a failure's turn from, so
// a task and its failure name the turn the same way or neither does.
//
// Decoded structurally rather than through the payload registry so this pass
// links no agentic machinery to read one map key.
func taskEntityID(data []byte) (string, error) {
	var envelope struct {
		Payload struct {
			Metadata map[string]any `json:"metadata"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", err
	}
	value, ok := envelope.Payload.Metadata[persona.MetadataKeyTurnEntityID]
	if !ok {
		return "", fmt.Errorf("persona task carries no %s", persona.MetadataKeyTurnEntityID)
	}
	entityID, ok := value.(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("persona task carries a %T for %s", value, persona.MetadataKeyTurnEntityID)
	}
	return entityID, nil
}
