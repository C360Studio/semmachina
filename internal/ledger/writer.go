package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/types"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
)

// TurnTypeSegment is the turn entity's position in the six-part ID.
//
// Restated here rather than imported from internal/stage because the dependency
// would drag the whole turn loop — personas, the effect applier, the scene
// assembler — into a package whose entire job is to read a graph entity and
// append a record. That weight is not hypothetical: the replay reader must be
// provably unable to run a persona, and the cheapest way to be unable to is to
// not link one. TestResolvedEvent_ReadsTheSameWireShapeAsTheStageTriggerParser
// pins the two decoders together from the TEST binary, where importing
// internal/stage costs nothing.
const TurnTypeSegment = "turn"

// TurnStore is the graph read surface the writer needs.
//
// Two methods, because the ledger has two ways of learning that a turn ended
// and only one of them is a notification. GetEntity serves the fast path — a
// resolved-turn event names one turn and the writer reads it. EntitiesWithPrefix
// serves the reconciliation, which has to find terminal turns NOBODY told it
// about; see Writer.Reconcile for why that is not paranoia.
type TurnStore interface {
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
	EntitiesWithPrefix(ctx context.Context, prefix, cursor string, limit int) (graphio.PrefixPage, error)
}

// StreamEnsurer creates the archive stream if it does not exist.
type StreamEnsurer interface {
	EnsureStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

// Publisher appends one manifest, stamping a de-duplication id.
type Publisher interface {
	PublishToStreamWithMsgID(ctx context.Context, subject string, data []byte, msgID string) error
}

// Consumer is the durable at-least-once consumption surface the writer binds to
// learn that a turn resolved. It is the framework's own: ConsumeDurable owns the
// ack, the nak-with-delay, the terminate-on-permanent-error and the heartbeat.
type Consumer interface {
	ConsumeDurable(
		ctx context.Context,
		cfg natsclient.StreamConsumerConfig,
		heartbeat time.Duration,
		handler func(context.Context, []byte) error,
	) error
}

// archive is the subject-scoped read the durable duplicate guard needs.
//
// GetLastMsgForSubject rather than a consumer or a scan: with a subject per
// turn it answers "is this turn already archived?" in one round trip, and it
// answers it from the STREAM rather than from a de-duplication window, so a
// redelivery an hour later gets the same answer as one a millisecond later.
type archive interface {
	GetLastMsgForSubject(ctx context.Context, subject string) (*jetstream.RawStreamMsg, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ TurnStore     = (*graphio.Store)(nil)
	_ StreamEnsurer = (*natsclient.Client)(nil)
	_ Publisher     = (*natsclient.Client)(nil)
	_ Consumer      = (*natsclient.Client)(nil)
)

// Writer appends one manifest per resolved turn.
type Writer struct {
	turns      TurnStore
	streams    StreamEnsurer
	publisher  Publisher
	consumer   Consumer
	decoder    *message.Decoder
	campaignID string
	turnPrefix string
	logger     *slog.Logger
	heartbeat  time.Duration
	pageLimit  int

	mu     sync.Mutex
	stream archive
}

// WriterOption configures a Writer.
type WriterOption func(*Writer)

// WithLogger sets the writer's logger.
func WithLogger(logger *slog.Logger) WriterOption {
	return func(w *Writer) {
		if logger != nil {
			w.logger = logger
		}
	}
}

// WithHeartbeat overrides the in-flight heartbeat.
func WithHeartbeat(d time.Duration) WriterOption {
	return func(w *Writer) { w.heartbeat = d }
}

// WithPageLimit overrides how many turn entities one reconciliation page asks
// for. Tests use it to exercise the paging loop against a handful of turns
// rather than the server default of a thousand.
func WithPageLimit(limit int) WriterOption {
	return func(w *Writer) { w.pageLimit = limit }
}

// NewWriter builds the campaign ledger's writer for one campaign.
//
// The campaign id is configuration rather than something read off the turn,
// which is honest about the MVP: instance-per-world means one process serves one
// campaign, and the turn entity does not carry a campaign reference to read
// anyway. It is validated here so a misconfigured deployment fails at boot
// rather than at the first archived turn.
//
// The decoder is required because the duplicate guard READS a manifest back off
// the stream, and reading a payload means the registry: a binary that forgot
// RegisterPayloads would decode nothing, conclude the archive was empty, and
// append a second manifest for every redelivered turn.
func NewWriter(
	turns TurnStore,
	streams StreamEnsurer,
	publisher Publisher,
	consumer Consumer,
	decoder *message.Decoder,
	campaignID string,
	opts ...WriterOption,
) (*Writer, error) {
	switch {
	case turns == nil:
		return nil, errors.New("the ledger writer requires a turn store")
	case streams == nil:
		return nil, errors.New("the ledger writer requires a stream creator")
	case publisher == nil:
		return nil, errors.New("the ledger writer requires a publisher")
	case consumer == nil:
		return nil, errors.New("the ledger writer requires a stream consumer")
	case decoder == nil:
		return nil, errors.New(
			"the ledger writer requires a payload decoder; the duplicate guard reads a manifest back off the " +
				"stream, and a writer that cannot decode one would archive every redelivery a second time")
	}
	if err := types.ValidateEntityID(campaignID); err != nil {
		return nil, fmt.Errorf("the ledger writer requires a canonical campaign entity id: %w", err)
	}
	turnPrefix, err := TurnPrefixForCampaign(campaignID)
	if err != nil {
		return nil, err
	}
	writer := &Writer{
		turns: turns, streams: streams, publisher: publisher, consumer: consumer,
		decoder: decoder, campaignID: campaignID, turnPrefix: turnPrefix,
		logger: slog.Default(), heartbeat: DefaultHeartbeat, pageLimit: DefaultPageLimit,
	}
	for _, opt := range opts {
		opt(writer)
	}
	if writer.heartbeat <= 0 {
		return nil, errors.New("the ledger writer requires a positive heartbeat")
	}
	if writer.pageLimit <= 0 {
		return nil, errors.New("the ledger writer requires a positive reconciliation page limit")
	}
	return writer, nil
}

// Start creates the archive stream, reconciles the archive against
// ENTITY_STATES, and binds the resolved-turn consumer — in that order.
//
// The order is the whole content of this method.
//
// The stream is ensured FIRST for the reason the stage runner ensures its own:
// a turn that resolves while the archive stream does not exist has its manifest
// published into nothing, and a turn that was never archived looks exactly like
// one that was.
//
// Reconciliation runs BEFORE the consumer binds, so the archive is whole before
// the fast path starts adding to it — which also means a boot sequence that
// gates ingress on this call has gated it on a complete archive rather than on
// a stream that exists.
//
// A reconciliation FAILURE does not stop the bind, and that is deliberate. The
// failures it reports are per-turn — a corrupt historical turn record, one
// manifest that could not be composed — and refusing to bind would let one bad
// record from last week stop every turn from being archived today. Each failure
// is logged at ERROR and the summary too; a caller that wants to refuse to boot
// calls Reconcile itself and reads the report.
func (w *Writer) Start(ctx context.Context) error {
	if _, err := w.archive(ctx); err != nil {
		return err
	}
	if report, err := w.Reconcile(ctx); err != nil {
		w.logger.Error("the campaign ledger could not fully reconcile with ENTITY_STATES",
			"scanned", report.Scanned, "archived", report.Archived, "already_archived", report.AlreadyArchived,
			"in_flight", report.InFlight, "failed", len(report.Failures), "error", err)
	}
	if err := w.consumer.ConsumeDurable(ctx, ConsumerConfig(), w.heartbeat, w.Handle); err != nil {
		return fmt.Errorf("bind the campaign-ledger consumer: %w", err)
	}
	return nil
}

// archive returns the stream handle, creating the stream on first use.
func (w *Writer) archive(ctx context.Context) (archive, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream != nil {
		return w.stream, nil
	}
	stream, err := w.streams.EnsureStream(ctx, StreamConfig())
	if err != nil {
		return nil, fmt.Errorf("ensure the %s stream: %w", Stream, err)
	}
	w.stream = stream
	return stream, nil
}

// Outcome is what one archive attempt did.
type Outcome struct {
	// Manifest is the record for this turn — the one just appended, or the one
	// already in the archive.
	Manifest *payload.TurnManifest
	// Appended reports that THIS call wrote it. False means an identical
	// manifest was already archived and nothing was written.
	Appended bool
}

// Handle processes one resolved-turn notification. Its return value IS the ack
// decision.
//
//   - nil: the turn is archived, by this call or by an earlier one. Acknowledge.
//   - a terminating error: this MESSAGE can never archive anything — it is not
//     a rule publication, or it names something that is not a turn. Redelivering
//     it reproduces the same refusal forever and nothing a repair could rescue
//     is inside it.
//   - anything else: nak and redeliver, forever.
//
// That last class deliberately includes failures that will never succeed on
// their own — a turn record holding two phases, a manifest already in the
// archive that disagrees with the turn. The choice follows intake's: both are
// RECOVERABLE by repairing the thing that is broken, and the redelivery then
// archives the turn, whereas terminating would drop a campaign's only record of
// it to spare an operator a log line. A nak is also the loud option — it holds
// NumAckPending and NumRedelivered non-zero and logs a WARN every attempt —
// while a terminate has no counter anywhere.
func (w *Writer) Handle(ctx context.Context, data []byte) error {
	event, err := ParseResolvedEvent(data)
	if err != nil {
		w.logger.Error("resolved-turn notification refused permanently", "error", err)
		return natsclient.TerminateDelivery(err)
	}

	outcome, err := w.Append(ctx, event.TurnEntityID)
	if err != nil {
		return fmt.Errorf("archive turn %s: %w", event.TurnEntityID, err)
	}
	if !outcome.Appended {
		w.logger.Info("turn was already archived; the duplicate notification wrote nothing",
			"turn", event.TurnEntityID)
	}
	return nil
}

// Append archives one resolved turn, exactly once.
//
// The duplicate guard is a READ of the turn's own subject rather than a
// de-duplication window, because the two cover different failures and only one
// of them is durable. A redelivery that arrives after the window — a consumer
// rebound with DeliverPolicy "all", a writer restarted an hour later — is
// invisible to Nats-Msg-Id and perfectly visible here. The msg-id is still
// stamped, as the narrow guard for the case this read cannot cover: two appends
// racing inside the same instant, each having read an empty subject.
//
// A manifest already present and IDENTICAL is dropped silently, which is the
// spec's requirement. A manifest already present and DIFFERENT is an error, not
// a second append and not an overwrite: the archive claims a turn resolved
// exactly once, and two conflicting accounts of one turn is the one thing that
// makes the whole record untrustworthy. Since Compose is pure, the comparison is
// exact rather than modulo a timestamp.
func (w *Writer) Append(ctx context.Context, turnEntityID string) (Outcome, error) {
	turnID, err := turnIDOf(turnEntityID)
	if err != nil {
		return Outcome{}, err
	}
	state, err := w.turns.GetEntity(ctx, turnEntityID)
	if err != nil {
		return Outcome{}, fmt.Errorf("read turn entity %s: %w", turnEntityID, err)
	}
	return w.archiveTurn(ctx, turnID, state)
}

// archiveTurn is the one append path, shared by the notification and the
// reconciliation.
//
// Shared deliberately rather than re-derived per caller: the duplicate guard is
// what makes the fast path and the reconciliation converge instead of doubling
// up, and a second implementation of it would be a second chance to get the
// convergence wrong. It takes the entity rather than reading one, so the
// reconciliation — which was handed whole entities by the prefix query — does
// not pay a second round trip per turn to read what it already has.
func (w *Writer) archiveTurn(
	ctx context.Context,
	turnID string,
	state *graph.EntityState,
) (Outcome, error) {
	subject, err := SubjectFor(turnID)
	if err != nil {
		return Outcome{}, err
	}
	manifest, err := Compose(turnID, state, w.campaignID)
	if err != nil {
		return Outcome{}, err
	}

	stream, err := w.archive(ctx)
	if err != nil {
		return Outcome{}, err
	}
	existing, err := readManifest(ctx, stream, w.decoder, subject)
	switch {
	case err == nil:
		if diff := manifestDiff(existing, manifest); diff != "" {
			return Outcome{}, fmt.Errorf(
				"turn %s is already archived with a DIFFERENT manifest, and the ledger is append-only, so "+
					"neither account can be corrected: %s", state.ID, diff)
		}
		return Outcome{Manifest: existing, Appended: false}, nil
	case errors.Is(err, ErrManifestNotFound):
	default:
		return Outcome{}, err
	}

	body, err := json.Marshal(message.NewBaseMessage(manifest.Schema(), manifest, Source))
	if err != nil {
		return Outcome{}, fmt.Errorf("encode the manifest for turn %s: %w", state.ID, err)
	}
	if err := w.publisher.PublishToStreamWithMsgID(ctx, subject, body, turnID); err != nil {
		return Outcome{}, fmt.Errorf("append the manifest for turn %s: %w", state.ID, err)
	}
	return Outcome{Manifest: manifest, Appended: true}, nil
}

// manifestDiff renders the difference between two manifests, or "" when they
// are the same record.
//
// Compared as canonical payload JSON rather than field by field, so a field
// added to TurnManifest is covered the day it is added rather than the day
// somebody remembers to extend a comparison. The BaseMessage envelope is
// excluded on purpose: it carries a fresh UUID and publish time per call, so
// comparing raw stream bytes would report every duplicate as a conflict.
func manifestDiff(existing, composed *payload.TurnManifest) string {
	before, err := json.Marshal(existing)
	if err != nil {
		return fmt.Sprintf("the archived manifest could not be re-encoded: %v", err)
	}
	after, err := json.Marshal(composed)
	if err != nil {
		return fmt.Sprintf("the composed manifest could not be encoded: %v", err)
	}
	if bytes.Equal(before, after) {
		return ""
	}
	return fmt.Sprintf("archived %s, turn now composes %s", before, after)
}

// readManifest reads one turn's archived manifest through the production
// decoder.
func readManifest(
	ctx context.Context,
	stream archive,
	decoder *message.Decoder,
	subject string,
) (*payload.TurnManifest, error) {
	raw, err := stream.GetLastMsgForSubject(ctx, subject)
	if err != nil {
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			return nil, fmt.Errorf("%s: %w", subject, ErrManifestNotFound)
		}
		return nil, fmt.Errorf("read the archived manifest at %s: %w", subject, err)
	}

	msg, err := decoder.Decode(raw.Data)
	if err != nil {
		return nil, fmt.Errorf("decode the archived manifest at %s: %w", subject, err)
	}
	manifest, ok := msg.Payload().(*payload.TurnManifest)
	if !ok {
		return nil, fmt.Errorf(
			"the archived record at %s is a %T, not a turn manifest; only the ledger writer publishes here",
			subject, msg.Payload())
	}
	// Stored bytes are untrusted input like any other: a manifest written by an
	// older schema decodes into a half-populated struct that a replay would
	// read as fact.
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("the archived manifest at %s does not satisfy its own contract: %w", subject, err)
	}
	return manifest, nil
}

// ResolvedEvent is one decoded `semmachina.turn.resolved` notification.
//
// Exported because that subject now has TWO consumers — this archive and the
// player-egress path that owes the player an answer — and the notification's wire
// shape is upstream's rule-publication map rather than one of ours. A second
// decoding of somebody else's shape is a second thing to get wrong when it moves,
// and the two consumers would disagree about which entity a rule fired for.
type ResolvedEvent struct {
	// TurnEntityID is the turn's six-part entity ID.
	TurnEntityID string
	// TurnID is the turn identity — the instance segment of TurnEntityID.
	TurnID string
}

// rulePublication is the rule engine's own `publish` action payload.
//
// Upstream's wire shape rather than one of ours, which is why it is decoded
// structurally instead of through the payload registry: the rule engine composes
// this map itself and stamps no BaseMessage envelope on it. The only field the
// ledger reads is the entity the rule fired for — the notification carries a
// reference and nothing else, and everything about the turn is read from the
// graph at archive time.
type rulePublication struct {
	EntityID string `json:"entity_id"`
	Subject  string `json:"subject"`
}

// ParseResolvedEvent decodes a resolved-turn notification and holds it to the
// turn's identity contract.
//
// Everything it rejects is permanent for that message: bytes that are not the
// rule engine's publication shape, an entity ID that is not canonical, and an
// entity that is not a TURN. The last is the interesting refusal — a rule whose
// pattern was widened to characters or scenes would otherwise hand a consumer a
// perfectly valid entity ID, and a record would be composed for something that
// has no phase.
//
// Exported for the second consumer of this subject, the player-egress path. It
// lives here rather than in a shared package because this is where the shape was
// first read and where the test that pins it against the stage-trigger parser
// already lives; moving it would buy a package and lose that pairing.
func ParseResolvedEvent(data []byte) (ResolvedEvent, error) {
	var published rulePublication
	if err := json.Unmarshal(data, &published); err != nil {
		return ResolvedEvent{}, fmt.Errorf("decode resolved-turn notification: %w", err)
	}
	if published.EntityID == "" {
		return ResolvedEvent{}, fmt.Errorf(
			"resolved-turn notification on %q names no entity; a rule payload carries the reference and "+
				"nothing else, so without it there is no turn to act on", published.Subject)
	}
	turnID, err := turnIDOf(published.EntityID)
	if err != nil {
		return ResolvedEvent{}, err
	}
	return ResolvedEvent{TurnEntityID: published.EntityID, TurnID: turnID}, nil
}

// turnIDOf reads the turn identity out of a turn entity's six-part ID.
func turnIDOf(turnEntityID string) (string, error) {
	if err := types.ValidateEntityID(turnEntityID); err != nil {
		return "", fmt.Errorf("%q is not a canonical six-part entity ID: %w", turnEntityID, err)
	}
	parts := strings.Split(turnEntityID, ".")
	if segment := parts[len(parts)-2]; segment != TurnTypeSegment {
		return "", fmt.Errorf(
			"%q has type segment %q rather than %q; the ledger archives TURNS, and nothing else in the world "+
				"has a phase to resolve", turnEntityID, segment, TurnTypeSegment)
	}
	turnID := parts[len(parts)-1]
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return "", err
	}
	return turnID, nil
}
