package turn_test

import (
	"errors"
	"testing"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/turn"
)

// beta.159 has no auto-create retention default to compare against. The seam
// now refuses an ordinary stream whose author did not state BOTH bounds, and
// the action stream must pass that same production check.
func TestActionStreamConfig_DeclaresTheBoundsBeta159Requires(t *testing.T) {
	undeclared := jetstream.StreamConfig{Name: turn.ActionStream}
	if err := natsclient.CheckStreamBounds(undeclared, "test negative control"); !errors.Is(err, natsclient.ErrStreamBoundsUndeclared) {
		t.Fatalf("an unbounded ordinary stream returned %v, want ErrStreamBoundsUndeclared", err)
	}

	if err := natsclient.CheckStreamBounds(turn.ActionStreamConfig(), "turn.ActionStreamConfig"); err != nil {
		t.Fatalf("ActionStreamConfig does not satisfy beta.159's finite-bounds contract: %v", err)
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
