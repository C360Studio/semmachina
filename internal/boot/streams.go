package boot

import (
	"context"

	ssconfig "github.com/c360studio/semstreams/config"

	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/world"
)

const (
	streamOwner = "semmachina"

	entityArchiveReason = "the canonical graph-ingest fact lane must not lose unapplied or replay input"
	stageArchiveReason  = "an unread stage trigger permanently strands its turn"
	ledgerArchiveReason = "the campaign ledger is the permanent record from which a campaign is replayed"
)

// streamDeclarations is the one declarative inventory for every ordinary
// stream this composition owns. beta.159 requires ordinary work streams to
// state finite age and byte bounds, and requires a named archival declaration
// when permanence is the contract.
//
// Direct EnsureStream calls remain at each component boundary as bind/readback
// guards. This inventory runs first and owns creation, so those guards never
// attempt to create an archival stream through the programmatic seam, where an
// archive intentionally cannot be declared.
func streamDeclarations(org string) *ssconfig.Config {
	action := turn.ActionStreamConfig()
	agent := stage.AgentStreamConfig()

	return &ssconfig.Config{
		Version:  "1.0.0",
		Platform: ssconfig.PlatformConfig{Org: org, ID: streamOwner},
		Streams: ssconfig.StreamConfigs{
			world.EntityStream: {
				Subjects:  []string{world.EntitySubjectFilter},
				Storage:   "file",
				Retention: "limits",
			},
			rulepack.StageStream: {
				Subjects:  []string{rulepack.StageSubjectFilter},
				Storage:   "file",
				Retention: "limits",
			},
			ledger.Stream: {
				Subjects:   []string{ledger.SubjectFilter},
				Storage:    "file",
				Retention:  "limits",
				Duplicates: "2m",
			},
			agent.Name: {
				Subjects:   agent.Subjects,
				Storage:    "file",
				Retention:  "limits",
				MaxAge:     agent.MaxAge.String(),
				MaxBytes:   agent.MaxBytes,
				Duplicates: agent.Duplicates.String(),
				Discard:    ssconfig.StreamDiscardOld,
			},
			action.Name: {
				Subjects:  action.Subjects,
				Storage:   "file",
				Retention: "limits",
				MaxAge:    action.MaxAge.String(),
				MaxBytes:  action.MaxBytes,
				Discard:   ssconfig.StreamDiscardNew,
			},
		},
		ArchivalStreams: ssconfig.ArchivalStreams{
			world.EntityStream: {
				Owner: streamOwner, Reason: entityArchiveReason,
			},
			rulepack.StageStream: {
				Owner: streamOwner, Reason: stageArchiveReason,
			},
			ledger.Stream: {
				Owner: streamOwner, Reason: ledgerArchiveReason,
			},
		},
	}
}

// ensureStreams provisions the complete stream inventory before any component
// binds. The manager validates every declaration before issuing the first NATS
// call, so a missing bound cannot leave a half-provisioned deployment.
func (e *Engine) ensureStreams(ctx context.Context) error {
	manager := ssconfig.NewStreamsManager(e.client, e.log())
	return manager.EnsureStreams(ctx, streamDeclarations(e.cfg.Org))
}
