package world

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type preflightPublisher struct {
	messages int
}

func (p *preflightPublisher) PublishToStreamWithAck(
	_ context.Context, _ string, _ []byte,
) (*jetstream.PubAck, error) {
	p.messages++
	return &jetstream.PubAck{Sequence: uint64(p.messages)}, nil
}

var _ Publisher = (*preflightPublisher)(nil)

func TestImporter_PreflightsEveryEntityBeforePublishingTheFirst(t *testing.T) {
	template := func(localID string) payload.TemplateRef {
		return payload.TemplateRef{ID: "starter", Version: "0.1.0", LocalID: localID}
	}
	plan := &Plan{
		Org: "c360", WorldNS: "world1", TemplateID: "starter", TemplateVersion: "0.1.0",
		Entities: []PlannedEntity{
			{
				ID: "c360.semmachina.world1.starter.scene.first", Kind: vocabulary.EntityKindScene,
				Template: template("first"),
				Facts:    []payload.WorldFact{{Predicate: vocabulary.WorldEntityName, Object: "First"}},
			},
			{
				ID: "c360.semmachina.world1.starter.character.kit", Kind: vocabulary.EntityKindCharacter,
				Template: template("kit"),
				Facts: []payload.WorldFact{
					{Predicate: vocabulary.WorldEntityName, Object: "Kit"},
					{Predicate: vocabulary.CompanionCandidatePolicy, Object: "bounded-initiative"},
					{Predicate: vocabulary.CompanionCandidatePolicy, Object: "bounded-initiative"},
				},
			},
		},
	}
	if err := plan.sealResolved(); err != nil {
		t.Fatalf("seal invalid regression plan: %v", err)
	}
	publisher := &preflightPublisher{}
	importer, err := NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	_, err = importer.Import(t.Context(), plan)
	if err == nil {
		t.Fatal("Import accepted a later entity with duplicate companion policy")
	}
	for _, want := range []string{"entity 2 of 2", vocabulary.CompanionCandidatePolicy.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("preflight error %q does not name %q", err, want)
		}
	}
	if publisher.messages != 0 {
		t.Fatalf("preflight published %d earlier entities before finding the later invalid one", publisher.messages)
	}
}
