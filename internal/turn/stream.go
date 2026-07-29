package turn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The action stream's limits.
//
// Stated rather than defaulted, and that is the whole reason this file exists.
// Until now nothing in production declared PLAYER_ACTIONS at all: the test
// harness created it, and in a real deployment the first thing to touch it would
// have been intake's own consumer bind, which auto-creates a missing stream from
// natsclient.DefaultStreamConfig — file storage, limits retention, and a SEVEN
// DAY MaxAge nobody chose. A player action that expired unread is a move that
// silently never happened, and the seven days would have been invisible in every
// review of every file in this repo.
const (
	// ActionMaxAge bounds how long an UNCONSUMED action may sit on the stream.
	//
	// It is finite on purpose — an ingress queue is time-shaped, unlike the
	// campaign ledger, which is an archive and has no age limit — and it is
	// generous on purpose, because the only message it can ever destroy is one
	// the engine never consumed. Consumption is not the same as play: the
	// durable copy of an accepted action is the ObjectStore object the turn
	// references, and expiry cannot reach that.
	//
	// Thirty days rather than seven, because DeliverPolicy is "all" precisely so
	// an action published while the engine was down is processed on the next
	// boot, and a limit shorter than a plausible outage would quietly undo that.
	// This is the dial for a deployment that can be down longer.
	//
	// It is NOT a pacing assumption. Nothing measures the gap between a player's
	// turns; this measures how long an action waits for an engine that is not
	// reading it, and an idle stream with nothing on it has no clock at all.
	ActionMaxAge = 30 * 24 * time.Hour

	// ActionMaxBytes bounds the stream's disk footprint. Finite for the reason
	// MaxAge is: an unbounded ingress queue is an unbounded resource on a
	// single-box deployment. At the 4 KiB action-text budget this is room for
	// far more unprocessed actions than an instance-per-world campaign can
	// generate before somebody notices nothing is being played.
	ActionMaxBytes = 1 << 30
)

// ActionStreamConfig is the player-action stream.
//
// Retention is limits-based rather than work-queue, and the difference is not
// academic. A work-queue stream deletes a message once a consumer acknowledges
// it, which would make the stream unreadable by anything but intake — and the
// ledger's reconciliation, the stranded-turn pass, and any future audit all read
// state that was derived from these actions. Limits retention keeps the request
// visible for as long as its limits allow, and the ACK FLOOR, not deletion, is
// what stops it being reprocessed.
//
// Discard is DiscardNew, and here it is load-bearing rather than defensive. When
// a limit is reached, DiscardOld drops the OLDEST message — an action already
// queued, possibly already the subject of a player waiting for an answer — with
// no error anywhere. DiscardNew refuses the newest publish instead, which the
// gateway sees as a failed publish and turns into a refusal the player reads.
// One of those loses a move silently; the other tells somebody.
func ActionStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:      ActionStream,
		Subjects:  []string{ActionSubjectPrefix},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    ActionMaxAge,
		MaxBytes:  ActionMaxBytes,
		Discard:   jetstream.DiscardNew,
		// Message and per-subject counts are left unlimited: bytes is the
		// resource that actually runs out on a single box, and a second ceiling
		// expressed in a different unit is a second thing to get wrong.
		MaxMsgs:           -1,
		MaxMsgsPerSubject: -1,
	}
}

// StreamEnsurer creates the action stream if it does not exist.
type StreamEnsurer interface {
	EnsureStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

// EnsureActionStream creates the player-action stream and PROVES the stream it
// got is the one this file describes.
//
// The readback is the point, and it is not paranoia. natsclient.EnsureStream
// creates a missing stream and otherwise returns the existing one UNCHANGED — it
// does not update configuration — so a stream someone else auto-created with
// defaults comes back from this call looking like a success. The failure that
// makes real is exactly the one the limits above exist to prevent: an engine
// running on a seven-day-MaxAge, DiscardOld stream, silently dropping a player's
// queued move, with every test in this repo green because they all create the
// stream correctly first.
//
// So the mismatch is an ERROR naming the field, rather than a repair. Widening a
// live stream's limits is an operator decision with data consequences (a
// narrowing update deletes messages), and a boot path that silently rewrote the
// deployment's retention would be a worse surprise than the one it fixed.
func EnsureActionStream(ctx context.Context, streams StreamEnsurer) error {
	if streams == nil {
		return fmt.Errorf("ensuring the %s stream requires a stream ensurer", ActionStream)
	}
	want := ActionStreamConfig()

	stream, err := streams.EnsureStream(ctx, want)
	if err != nil {
		return fmt.Errorf("ensure the %s stream: %w", ActionStream, err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("read back the %s stream configuration: %w", ActionStream, err)
	}
	return checkActionStreamConfig(want, info.Config)
}

// checkActionStreamConfig compares the limits that can silently lose a move.
//
// Not a whole-struct comparison: replicas, placement, sources and mirrors are
// operator decisions this file has no opinion about, and demanding equality on
// them would make a legitimately configured cluster fail to boot. The fields
// here are the ones whose wrong value destroys a player action without an error.
func checkActionStreamConfig(want, got jetstream.StreamConfig) error {
	mismatches := []struct {
		field string
		want  any
		got   any
		harm  string
	}{
		{"MaxAge", want.MaxAge, got.MaxAge,
			"an action that expires before the engine reads it is a move that silently never happened"},
		{"MaxBytes", want.MaxBytes, got.MaxBytes,
			"an unbounded ingress queue is an unbounded resource on a single-box deployment"},
		{"Discard", want.Discard, got.Discard,
			"DiscardOld drops an already-queued action with no error; DiscardNew refuses the publish, " +
				"which the gateway turns into a refusal the player reads"},
		{"Retention", want.Retention, got.Retention,
			"work-queue or interest retention deletes an acknowledged action, and the ack floor — not " +
				"deletion — is what stops it being reprocessed"},
	}
	for _, m := range mismatches {
		if m.want != m.got {
			return fmt.Errorf(
				"the %s stream already exists with %s = %v, but this engine requires %v: %s. "+
					"EnsureStream does not update an existing stream, so this must be fixed by an operator",
				ActionStream, m.field, m.got, m.want, m.harm)
		}
	}
	if !subjectCovered(got.Subjects, ActionSubject) {
		return fmt.Errorf(
			"the %s stream already exists with subjects %v, which do not cover the canonical publish subject "+
				"%q; every published action would be refused by the broker",
			ActionStream, got.Subjects, ActionSubject)
	}
	return nil
}

// subjectCovered reports whether a published subject falls under any of a
// stream's subject filters, for the two wildcard forms NATS defines.
func subjectCovered(filters []string, subject string) bool {
	for _, filter := range filters {
		if filter == subject {
			return true
		}
		if prefix, found := strings.CutSuffix(filter, ">"); found &&
			strings.HasPrefix(subject, prefix) && len(subject) > len(prefix) {
			return true
		}
		if prefix, found := strings.CutSuffix(filter, "*"); found {
			if rest, ok := strings.CutPrefix(subject, prefix); ok &&
				rest != "" && !strings.Contains(rest, ".") {
				return true
			}
		}
	}
	return false
}
