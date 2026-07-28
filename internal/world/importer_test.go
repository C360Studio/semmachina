package world_test

import (
	"context"
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
