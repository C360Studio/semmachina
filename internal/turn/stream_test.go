package turn_test

import (
	"testing"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/turn"
)

// The failure this file exists to prevent, pinned against the DEFAULT it exists
// to prevent — derived from natsclient rather than restated, so an upstream
// change to the auto-create policy moves the comparison with it.
//
// Comparing the created stream to turn.ActionMaxAge, which is what the
// integration readback does, proves the broker got what this package asked for
// and says nothing about whether what it asked for is right: editing the
// constant moves both sides of that assertion together. This one cannot be
// satisfied by editing the constant to the wrong value, because the wrong value
// is named on the other side.
func TestActionStreamConfig_IsNotTheAutoCreateDefaultNobodyChose(t *testing.T) {
	defaults := natsclient.DefaultStreamConfig()
	if defaults == nil {
		t.Fatal("natsclient no longer publishes an auto-create default; this comparison sees nothing")
	}
	if defaults.MaxAge == 0 {
		t.Fatal("the auto-create default carries no MaxAge, so the hazard this constant guards against " +
			"no longer exists and the comparison below is vacuous")
	}

	cfg := turn.ActionStreamConfig()
	if cfg.MaxAge == defaults.MaxAge {
		t.Errorf("MaxAge = %s, which is exactly natsclient's auto-create default; an action that expires "+
			"before the engine reads it is a move that silently never happened, and a limit nobody chose "+
			"is not a decision", cfg.MaxAge)
	}
	if cfg.MaxAge <= 0 {
		t.Errorf("MaxAge = %s; the ingress queue is time-shaped and its bound is finite and stated",
			cfg.MaxAge)
	}
	if cfg.MaxBytes <= 0 {
		t.Errorf("MaxBytes = %d; an unbounded ingress queue is an unbounded resource on a single box",
			cfg.MaxBytes)
	}
}

// A generous MaxAge is the whole reason DeliverPolicy "all" is safe: an action
// published while the engine was down is processed on the next boot, and a
// limit shorter than a plausible outage would quietly undo that.
func TestActionStreamConfig_SurvivesAPlausibleOutage(t *testing.T) {
	const aWeek = 7 * 24 * 60 * 60
	if turn.ActionStreamConfig().MaxAge.Seconds() <= aWeek {
		t.Errorf("MaxAge = %s, which is not longer than a week; DeliverPolicy \"all\" exists so an action "+
			"published while the engine was down is processed on the next boot, and this is the limit that "+
			"decides whether it still can be", turn.ActionStreamConfig().MaxAge)
	}
}

// DiscardOld drops an already-queued action with no error anywhere. DiscardNew
// refuses the newest publish, which the gateway turns into a refusal the player
// reads. One loses a move silently; the other tells somebody.
func TestActionStreamConfig_RefusesTheNewPublishRatherThanDroppingAQueuedMove(t *testing.T) {
	cfg := turn.ActionStreamConfig()
	if cfg.Discard != jetstream.DiscardNew {
		t.Errorf("Discard = %v, want DiscardNew", cfg.Discard)
	}
	if cfg.Retention != jetstream.LimitsPolicy {
		t.Errorf("Retention = %v; work-queue retention deletes an acknowledged action, and the ack floor — "+
			"not deletion — is what stops it being reprocessed", cfg.Retention)
	}
	if cfg.Storage != jetstream.FileStorage {
		t.Errorf("Storage = %v; a memory-backed request queue loses every unprocessed move on restart",
			cfg.Storage)
	}
}

// The compiler cannot check that the publish subject falls under the stream's
// filter, and a mismatch is every published action refused by the broker.
func TestActionStreamConfig_CoversTheCanonicalPublishSubject(t *testing.T) {
	cfg := turn.ActionStreamConfig()
	if len(cfg.Subjects) != 1 || cfg.Subjects[0] != turn.ActionSubjectPrefix {
		t.Fatalf("stream subjects = %v, want [%s]", cfg.Subjects, turn.ActionSubjectPrefix)
	}
	if cfg.Name != turn.ActionStream {
		t.Fatalf("stream name = %q, want %q", cfg.Name, turn.ActionStream)
	}
}

func TestEnsureActionStream_RequiresAnEnsurer(t *testing.T) {
	if err := turn.EnsureActionStream(t.Context(), nil); err == nil {
		t.Fatal("EnsureActionStream accepted a nil ensurer")
	}
}
