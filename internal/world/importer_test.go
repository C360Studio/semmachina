package world_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/payloadregistry"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// recordingPublisher captures what the importer put on the wire.
type recordingPublisher struct {
	subjects []string
	messages [][]byte
}

func (p *recordingPublisher) PublishToStreamWithAck(
	_ context.Context, subject string, data []byte,
) (*jetstream.PubAck, error) {
	p.subjects = append(p.subjects, subject)
	p.messages = append(p.messages, data)
	return &jetstream.PubAck{Sequence: uint64(len(p.messages))}, nil
}

func importedPlan(t *testing.T) *world.Plan {
	t.Helper()
	plan, err := testPackage(t).Resolve(testInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return plan
}

// The published bytes are decoded with the PRODUCTION decoder over the
// production registry. A payload the decoder cannot reconstruct is a payload
// graph-ingest drops with "payload does not implement Graphable", and it drops
// it silently — the message is acked and counted as an error.
func TestImporter_PublishesDecodableGraphablePayloads(t *testing.T) {
	plan := importedPlan(t)
	publisher := &recordingPublisher{}

	stamp := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	importer, err := world.NewImporter(publisher, world.WithClock(func() time.Time { return stamp }))
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	result, err := importer.Import(context.Background(), plan)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(publisher.messages) != len(plan.Entities) {
		t.Fatalf("published %d messages for %d entities", len(publisher.messages), len(plan.Entities))
	}
	if !result.RecordedAt.Equal(stamp) {
		t.Fatalf("result stamped %v, want %v", result.RecordedAt, stamp)
	}

	registry := payloadregistry.New()
	if err := payload.RegisterPayloads(registry); err != nil {
		t.Fatalf("RegisterPayloads: %v", err)
	}
	decoder := message.NewDecoder(registry)

	for index, wire := range publisher.messages {
		msg, decodeErr := decoder.Decode(wire)
		if decodeErr != nil {
			t.Fatalf("message %d does not decode through the production decoder: %v", index, decodeErr)
		}
		entity, ok := msg.Payload().(*payload.WorldEntity)
		if !ok {
			t.Fatalf("message %d decoded as %T, want *payload.WorldEntity", index, msg.Payload())
		}
		if entity.ID != plan.Entities[index].ID {
			t.Fatalf("message %d carries %s, plan position %d is %s",
				index, entity.ID, index, plan.Entities[index].ID)
		}
		if err := entity.Validate(); err != nil {
			t.Fatalf("message %d carries an invalid entity: %v", index, err)
		}
		if triples := entity.Triples(); len(triples) != len(entity.Facts)+1 {
			t.Fatalf("message %d projects %d triples for %d facts", index, len(triples), len(entity.Facts))
		}
	}
}

// One import, one birth time. A per-entity clock read would give an imported
// world a spread of timestamps that depends on how long publishing took.
func TestImporter_StampsOneTimestampAcrossTheWholeImport(t *testing.T) {
	publisher := &recordingPublisher{}

	var calls int
	importer, err := world.NewImporter(publisher, world.WithClock(func() time.Time {
		calls++
		return time.Date(2026, 7, 28, 12, 0, calls, 0, time.UTC)
	}))
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	if _, err := importer.Import(context.Background(), importedPlan(t)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the importer read the clock %d times; an import has one birth time", calls)
	}
}

// The subject is what routes an entity to graph-ingest. Publishing to the wrong
// one is a silent no-op: the message lands durably in some stream and nothing
// ever ingests it.
func TestImporter_PublishesOnTheConfiguredSubject(t *testing.T) {
	plan := importedPlan(t)

	t.Run("default", func(t *testing.T) {
		publisher := &recordingPublisher{}
		importer, err := world.NewImporter(publisher)
		if err != nil {
			t.Fatalf("NewImporter: %v", err)
		}
		if _, err := importer.Import(context.Background(), plan); err != nil {
			t.Fatalf("Import: %v", err)
		}
		for _, subject := range publisher.subjects {
			if subject != world.DefaultImportSubject {
				t.Fatalf("published on %q, want %q", subject, world.DefaultImportSubject)
			}
		}
		// graph-ingest's default input port consumes `entity.>`; a default
		// subject outside that prefix would never reach it.
		if !strings.HasPrefix(world.DefaultImportSubject, "entity.") {
			t.Fatalf("default subject %q is outside graph-ingest's default `entity.>` port",
				world.DefaultImportSubject)
		}
	})

	t.Run("override", func(t *testing.T) {
		publisher := &recordingPublisher{}
		importer, err := world.NewImporter(publisher, world.WithSubject("entity.custom"))
		if err != nil {
			t.Fatalf("NewImporter: %v", err)
		}
		if _, err := importer.Import(context.Background(), plan); err != nil {
			t.Fatalf("Import: %v", err)
		}
		for _, subject := range publisher.subjects {
			if subject != "entity.custom" {
				t.Fatalf("published on %q, want the override", subject)
			}
		}
	})
}

// An entity the graph would reject must not be published at all. Publishing it
// would put a poison message in a stream that graph-ingest redelivers, fails to
// apply, and ack-drops with a warning nobody is watching.
func TestImporter_RefusesToPublishAnInvalidEntity(t *testing.T) {
	plan := importedPlan(t)
	plan.Entities[0].Facts = append(plan.Entities[0].Facts, payload.WorldFact{
		Predicate: vocabulary.CharacterAttributeHealth,
		Object:    "not a number",
	})

	publisher := &recordingPublisher{}
	importer, err := world.NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	if _, err := importer.Import(context.Background(), plan); err == nil {
		t.Fatal("Import published an entity the graph would reject")
	}
	if len(publisher.messages) != 0 {
		t.Fatalf("the invalid entity was published anyway (%d messages)", len(publisher.messages))
	}
}

// A publish failure part-way through must be reported with the entity that
// failed, not swallowed into a partial success.
//
// The failure is injected at the production publish surface because a real
// broker will not fail on demand — which also means this test needs no
// infrastructure, so it lives with the other in-process importer tests rather
// than behind the Docker gate.
func TestImport_ReportsAPartialPublishFailure(t *testing.T) {
	plan := importedPlan(t)
	if len(plan.Entities) < 3 {
		t.Fatalf("the fixture plans %d entities; this test needs a failure after at least two",
			len(plan.Entities))
	}

	publisher := &failingPublisher{failAfter: 2, err: errors.New("broker said no")}
	importer, err := world.NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	result, err := importer.Import(t.Context(), plan)
	if err == nil {
		t.Fatal("Import reported success after a publish failure")
	}
	if len(result.Entities) != 2 {
		t.Fatalf("result reported %d published entities, want the 2 that succeeded", len(result.Entities))
	}
	// The partial result stays positionally usable: one sequence per reported
	// entity, or a caller cannot tell which message the broker accepted.
	if len(result.Sequences) != len(result.Entities) {
		t.Fatalf("result reported %d entities and %d sequences",
			len(result.Entities), len(result.Sequences))
	}
	if !strings.Contains(err.Error(), plan.Entities[2].ID) {
		t.Fatalf("error %q does not name the entity that failed", err)
	}
	if !strings.Contains(err.Error(), "broker said no") {
		t.Fatalf("error %q loses the broker's reason", err)
	}
}

// ImportResult.Sequences promises positional correspondence with Entities, so
// it must stay a per-entity slot even when a publisher hands back no
// acknowledgment. A skipped append would not shorten the slice visibly — it
// would slide every later sequence one entity to the left, which reads as a
// successful import against the wrong messages.
func TestImport_KeepsSequencesPositionalWhenAPublisherReturnsNoAck(t *testing.T) {
	plan := importedPlan(t)

	result, err := mustImport(t, &ackLessPublisher{}, plan)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Sequences) != len(result.Entities) {
		t.Fatalf("result reported %d entities and %d sequences; the two are positional",
			len(result.Entities), len(result.Sequences))
	}
	for index, sequence := range result.Sequences {
		if sequence != 0 {
			t.Fatalf("entity %d reports sequence %d without an acknowledgment", index, sequence)
		}
	}
}

func mustImport(t *testing.T, publisher world.Publisher, plan *world.Plan) (world.ImportResult, error) {
	t.Helper()
	importer, err := world.NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	return importer.Import(t.Context(), plan)
}

// ackLessPublisher accepts every message and acknowledges nothing.
type ackLessPublisher struct{}

func (p *ackLessPublisher) PublishToStreamWithAck(
	_ context.Context, _ string, _ []byte,
) (*jetstream.PubAck, error) {
	return nil, nil
}

type failingPublisher struct {
	failAfter int
	err       error
	calls     int
	subjects  []string
}

func (p *failingPublisher) PublishToStreamWithAck(
	_ context.Context, subject string, _ []byte,
) (*jetstream.PubAck, error) {
	p.calls++
	p.subjects = append(p.subjects, subject)
	if p.calls > p.failAfter {
		return nil, p.err
	}
	return &jetstream.PubAck{Sequence: uint64(p.calls)}, nil
}

func TestNewImporter_RejectsAnUnusableConfiguration(t *testing.T) {
	if _, err := world.NewImporter(nil); err == nil {
		t.Fatal("NewImporter accepted a nil publisher")
	}
	if _, err := world.NewImporter(&recordingPublisher{}, world.WithSubject("")); err == nil {
		t.Fatal("NewImporter accepted an empty subject")
	}
	if _, err := world.NewImporter(&recordingPublisher{}, world.WithClock(nil)); err == nil {
		t.Fatal("NewImporter accepted a nil clock")
	}
}

func TestImport_RejectsAnEmptyPlan(t *testing.T) {
	importer, err := world.NewImporter(&recordingPublisher{})
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	if _, err := importer.Import(context.Background(), nil); err == nil {
		t.Fatal("Import accepted a nil plan")
	}
	if _, err := importer.Import(context.Background(), &world.Plan{}); err == nil {
		t.Fatal("Import accepted a plan that materializes nothing")
	}
}

func TestImport_RejectsAnUnvalidatedOrAlteredSeedPlan(t *testing.T) {
	publisher := &recordingPublisher{}
	importer, err := world.NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	fabricated := &world.Plan{Entities: []world.PlannedEntity{{
		ID:   "c360.semmachina.world1.mystery.case.case1",
		Kind: vocabulary.EntityKindCase,
		Facts: []payload.WorldFact{{
			Predicate: vocabulary.CaseSolutionCulprit,
			Object:    "c360.semmachina.world1.mystery.character.suspect1",
			Reference: true,
		}},
	}}}
	if _, err := importer.Import(t.Context(), fabricated); err == nil {
		t.Fatal("Import accepted fabricated protected seed data")
	} else if !strings.Contains(err.Error(), "Package.Resolve") {
		t.Fatalf("fabricated plan refusal %q does not name the validated path", err)
	}

	altered := importedPlan(t)
	altered.Entities[0].Facts = append(altered.Entities[0].Facts, payload.WorldFact{
		Predicate: vocabulary.WorldEntityDescription,
		Object:    "changed after validation",
	})
	if _, err := importer.Import(t.Context(), altered); err == nil {
		t.Fatal("Import accepted a plan altered after package validation")
	}
	selectionAltered := importedPlan(t)
	selectionAltered.Experience.PersonaPack = "changed-after-resolution"
	if _, err := importer.Import(t.Context(), selectionAltered); err == nil {
		t.Fatal("Import accepted an experience selection altered after Package.Resolve sealed it")
	}
	if len(publisher.messages) != 0 {
		t.Fatalf("import published %d messages before refusing unvalidated seed data", len(publisher.messages))
	}
}
