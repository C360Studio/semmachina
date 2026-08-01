package world

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
)

const (
	// DefaultImportSubject is the JetStream subject world entities are
	// published on.
	//
	// It sits under `entity.` because that is the prefix graph-ingest's default
	// input port consumes (`entity.>` on the ENTITY stream). The importer is a
	// producer on the framework's ordinary fact lane, not a special case.
	DefaultImportSubject = "entity.world.import"

	// ImportSource identifies the importer as the producer of a published
	// message.
	ImportSource = "world-importer"

	// EntityStream is the JetStream stream graph-ingest's default input port
	// consumes, and the one the importer publishes onto.
	//
	// Neither the name nor the subjects are ours to choose: graph-ingest derives
	// the name from the `entity.` subject prefix (deriveStreamName) and binds a
	// consumer with AutoCreate FALSE, and semstreams' own stream provisioning
	// derives the subjects by lower-casing the stream name and appending `.>`.
	// They are declared here because a composition that runs graph-ingest has to
	// create it: without the stream, graph-ingest's consumer cannot bind and the
	// importer's own publish is refused — which is the loud half. The quiet half
	// is what a plain core publish onto that subject would do, which is nothing,
	// successfully.
	EntityStream = "ENTITY"
	// EntitySubjectFilter is the subject space that stream captures.
	EntitySubjectFilter = "entity.>"
)

// EntityStreamConfig is the fact lane every world entity travels.
//
// Limits-based with no age or size eviction, matching ENTITY_STATES' own
// ADR-068 guardrail: this stream carries the writes that BECOME authoritative
// state, and a message that expired before graph-ingest applied it is an entity
// that never existed, with no error anywhere.
func EntityStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:      EntityStream,
		Subjects:  []string{EntitySubjectFilter},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
	}
}

// Publisher is the narrow JetStream publish surface the importer needs.
//
// It is an interface so a test can drive the failure paths a real broker will
// not produce on demand — but the surface is the production method verbatim, so
// an importer that works against a fake is calling the same method it will call
// against NATS.
type Publisher interface {
	PublishToStreamWithAck(ctx context.Context, subject string, data []byte) (*jetstream.PubAck, error)
}

// The claim above, enforced by the compiler rather than by a doc comment: if
// the upstream signature moves, this build breaks here instead of at the one
// call site that only runs against a real broker.
var _ Publisher = (*natsclient.Client)(nil)

// Importer materializes a resolved plan into the graph.
//
// It publishes each entity as a Graphable payload and does not write
// ENTITY_STATES. That is not a stylistic preference: graph-ingest is the sole
// writer, and everything it does on the way in — the predicate contract, the
// indexing-profile stamp, hierarchy inference, referential stubs for
// cross-entity references, and the newer-wins merge that makes re-import
// converge — is invisible to a component that writes the KV bucket directly.
// An importer that took the shortcut would produce a world that looks right
// and behaves differently from every other write in the system.
type Importer struct {
	publisher Publisher
	subject   string
	source    string
	now       func() time.Time
}

// ImporterOption configures an Importer.
type ImporterOption func(*Importer)

// WithSubject overrides the publish subject. It must be routed to graph-ingest
// by the deployment's flow configuration.
func WithSubject(subject string) ImporterOption {
	return func(i *Importer) { i.subject = subject }
}

// WithClock overrides the import clock. Tests use it so a published triple's
// timestamp is an assertable value rather than whatever time it happened to be.
func WithClock(now func() time.Time) ImporterOption {
	return func(i *Importer) { i.now = now }
}

// NewImporter builds an importer over a JetStream publisher.
func NewImporter(publisher Publisher, opts ...ImporterOption) (*Importer, error) {
	if publisher == nil {
		return nil, errors.New("world importer requires a publisher")
	}
	importer := &Importer{
		publisher: publisher,
		subject:   DefaultImportSubject,
		source:    ImportSource,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(importer)
	}
	if importer.subject == "" {
		return nil, errors.New("world importer requires a publish subject")
	}
	if importer.now == nil {
		return nil, errors.New("world importer requires a clock")
	}
	return importer, nil
}

// ImportResult records what an import published.
type ImportResult struct {
	// Entities are the materialized entity IDs in plan order.
	Entities []string
	// Sequences are the stream sequences the broker assigned, positionally
	// matching Entities — one entry per published entity, zero where the
	// publisher returned no acknowledgment. They are the durable proof a
	// message was accepted, which is a different claim from "the entity
	// exists" — see Import.
	Sequences []uint64
	// RecordedAt is the timestamp stamped on every published triple.
	RecordedAt time.Time
}

// Import publishes every planned entity and returns once the broker has
// acknowledged all of them.
//
// The acknowledgment is a DURABILITY claim, not a materialization claim: the
// messages are in the stream, and graph-ingest applies them asynchronously on
// its own consumer. A caller that needs "the world is queryable" must poll the
// graph. Import does not do that polling itself because it has no read
// surface, and inventing one here would put a second graph client in the
// importer purely to make an asynchronous pipeline look synchronous.
//
// Such a poll MUST exclude referential stubs via graph.EntityState.IsStub().
// When one imported entity references another, graph-ingest materializes a
// referential-integrity STUB at the referenced ID the moment the REFERENCING
// entity lands — before the referenced entity's own message is applied. So an
// existence poll can succeed against an entity carrying only core.identity.*
// stub markers and none of its own facts, and report a world loaded when it is
// half-written. IsStub keys on the envelope, which the fact lane re-stamps at
// true birth, so it is the signal that actually flips.
//
// One timestamp is minted for the whole import and stamped on every triple, so
// an imported world has a single coherent birth time rather than a spread that
// depends on how long publishing took.
//
// A publish failure part-way through leaves a partial world, and that is safe
// rather than merely tolerated: identity is deterministic and merges replace,
// so re-running the same import converges on the same state. The error names
// the entity that failed and how many had already been published.
func (i *Importer) Import(ctx context.Context, plan *Plan) (ImportResult, error) {
	if plan == nil {
		return ImportResult{}, errors.New("world importer requires a plan")
	}
	if len(plan.Entities) == 0 {
		return ImportResult{}, errors.New("world plan materializes no entities")
	}
	if err := plan.requireResolvedSeal(); err != nil {
		return ImportResult{}, err
	}

	recordedAt := i.now().UTC()
	result := ImportResult{
		Entities:   make([]string, 0, len(plan.Entities)),
		Sequences:  make([]uint64, 0, len(plan.Entities)),
		RecordedAt: recordedAt,
	}

	// Preflight every payload before the first irreversible publish. Package
	// validation catches authoring mistakes, but this is the final production
	// boundary over the exact materialized WorldEntity bytes graph-ingest will
	// receive. Finding poison in entity N after publishing entities 1..N-1
	// would turn a locally decidable validation error into a partial world.
	type preparedEntity struct {
		id   string
		wire []byte
	}
	prepared := make([]preparedEntity, 0, len(plan.Entities))
	for index, planned := range plan.Entities {
		entity := planned.Payload(recordedAt)
		wire, err := i.encode(entity)
		if err != nil {
			return result, fmt.Errorf("entity %d of %d (%s): %w",
				index+1, len(plan.Entities), entity.ID, err)
		}
		prepared = append(prepared, preparedEntity{id: entity.ID, wire: wire})
	}

	for index, entity := range prepared {
		ack, err := i.publisher.PublishToStreamWithAck(ctx, i.subject, entity.wire)
		if err != nil {
			return result, fmt.Errorf("publish entity %d of %d (%s) to %s: %w",
				index+1, len(prepared), entity.id, i.subject, err)
		}

		result.Entities = append(result.Entities, entity.id)
		// Appended unconditionally, including the zero for a publisher that
		// returned no ack and no error: Sequences promises positional
		// correspondence with Entities, and a skipped append would silently
		// shift every later sequence onto the wrong entity. Zero is not a
		// sequence JetStream ever assigns, so it reads as "no proof" rather
		// than as somebody else's message.
		var sequence uint64
		if ack != nil {
			sequence = ack.Sequence
		}
		result.Sequences = append(result.Sequences, sequence)
	}
	return result, nil
}

// encode wraps the entity in the BaseMessage envelope the decoder expects.
//
// Validate is called explicitly even though BaseMessage.MarshalJSON validates
// too. Marshal's failure arrives as a JSON error wrapping the real reason, and
// the real reason — which entity, which fact — is the one an author needs.
func (i *Importer) encode(entity *payload.WorldEntity) ([]byte, error) {
	if err := entity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid world entity: %w", err)
	}
	msg := message.NewBaseMessage(entity.Schema(), entity, i.source,
		message.WithTime(entity.RecordedAt))
	wire, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode world entity: %w", err)
	}
	return wire, nil
}
