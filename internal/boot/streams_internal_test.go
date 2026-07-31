package boot

import (
	"errors"
	"slices"
	"testing"

	ssconfig "github.com/c360studio/semstreams/config"

	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/world"
)

func TestStreamDeclarations_BoundWorkQueuesAndNamePermanentArchives(t *testing.T) {
	cfg := streamDeclarations("c360")

	if cfg.Version != "1.0.0" || cfg.Platform.Org != "c360" || cfg.Platform.ID != "semmachina" {
		t.Fatalf("platform declaration = version %q org %q id %q", cfg.Version, cfg.Platform.Org, cfg.Platform.ID)
	}
	if _, err := ssconfig.ValidateStreamDeclarations(cfg); err != nil {
		t.Fatalf("SemMachina stream declarations do not satisfy beta.159: %v", err)
	}

	for name, wantSubjects := range map[string][]string{
		world.EntityStream:   {world.EntitySubjectFilter},
		rulepack.StageStream: {rulepack.StageSubjectFilter},
		ledger.Stream:        {ledger.SubjectFilter},
	} {
		decl, ok := cfg.Streams[name]
		if !ok {
			t.Errorf("archive %s has no ordinary stream declaration", name)
			continue
		}
		if !slices.Equal(decl.Subjects, wantSubjects) || decl.Storage != "file" || decl.Retention != "limits" {
			t.Errorf("archive %s = %+v, want subjects %v file/limits", name, decl, wantSubjects)
		}
		archive, ok := cfg.ArchivalStreams[name]
		if !ok || archive.Owner != "semmachina" || archive.Reason == "" {
			t.Errorf("archive declaration %s = %+v, want owned, reasoned permanence", name, archive)
		}
	}
	if got := cfg.Streams[ledger.Stream].Duplicates; got != "2m" {
		t.Errorf("ledger duplicate window = %q, want 2m", got)
	}

	for _, name := range []string{persona.TaskStream, turn.ActionStream} {
		decl := cfg.Streams[name]
		if decl.MaxAge == "" || decl.MaxBytes <= 0 || decl.Discard == "" {
			t.Errorf("work stream %s is not finitely bounded: %+v", name, decl)
		}
	}
	if got := cfg.Streams[persona.TaskStream].Duplicates; got != stage.AgentStreamMaxAge.String() {
		t.Errorf("AGENT duplicate window = %q, want the complete retained-task horizon %q",
			got, stage.AgentStreamMaxAge.String())
	}
}

func TestStreamDeclarations_ArchivalClassificationIsLoadBearing(t *testing.T) {
	cfg := streamDeclarations("c360")
	delete(cfg.ArchivalStreams, world.EntityStream)

	_, err := ssconfig.ValidateStreamDeclarations(cfg)
	if !errors.Is(err, ssconfig.ErrStreamBoundsUndeclared) {
		t.Fatalf("unclassified ENTITY stream returned %v, want ErrStreamBoundsUndeclared", err)
	}
}
