package ledger

import (
	"fmt"
	"time"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The campaign archive's coordinates.
const (
	// Stream is the durable home of turn manifests.
	Stream = "CAMPAIGN_LEDGER"
	// SubjectPrefix is the subject space every manifest lives under.
	SubjectPrefix = "semmachina.ledger.turn."
	// SubjectFilter is the stream's subject filter: one token per turn.
	SubjectFilter = SubjectPrefix + "*"
	// ConsumerName is the durable consumer the writer binds on the stage stream
	// to learn that a turn resolved.
	ConsumerName = "semmachina-campaign-ledger"
	// Source names the ledger writer as the producer of the manifests it
	// appends.
	Source = "campaign-ledger"
)

// DuplicateWindow is the server-side Nats-Msg-Id de-duplication window.
//
// It is a NARROW second guard and explicitly not the duplicate guarantee. The
// window is time-bounded, so it covers the case the read-before-append cannot —
// two appends racing inside the same instant, each having read an empty
// subject — and covers nothing at all for a redelivery that arrives an hour
// later. That case belongs to the durable check in Writer.Append, which reads
// the subject rather than trusting a clock.
const DuplicateWindow = 2 * time.Minute

// AckWait bounds one resolved-turn delivery in flight. The writer's work is a
// graph read, a subject read, and an append; the heartbeat resets the clock
// while that is genuinely happening.
const (
	// DefaultAckWait is how long one delivery may be in flight.
	DefaultAckWait = 30 * time.Second
	// DefaultHeartbeat resets the ack clock while the writer is working.
	DefaultHeartbeat = 10 * time.Second
)

// SubjectFor returns the subject one turn's manifest is appended to.
//
// A subject PER TURN rather than one subject for the whole archive, and that
// choice is the durable duplicate guard: "has this turn already been archived?"
// becomes GetLastMsgForSubject — one round trip, no scan, and no dependence on
// a de-duplication window that expires. A single shared subject would make the
// same question a walk of the campaign's whole history.
//
// The turn id is validated as an entity-ID segment, which is also exactly what
// makes it a safe subject token: that alphabet is alphanumerics, `_` and `-`,
// so it can carry no dot, no space, and neither wildcard. A turn id that could
// contain a `.` would silently widen the subject and file a manifest where no
// reader looks; one containing `*` or `>` would be a subject that matches other
// turns' manifests.
func SubjectFor(turnID string) (string, error) {
	if err := vocabulary.ValidateIDSegment(turnID); err != nil {
		return "", fmt.Errorf("ledger subject needs a turn id: %w", err)
	}
	return SubjectPrefix + turnID, nil
}

// StreamConfig is the archive stream.
//
// Every limit is stated rather than defaulted, because the defaults are wrong
// here in a way that is invisible: an auto-created stream carries a seven-day
// MaxAge, and a ledger that quietly forgets a turn after a week is not an
// archive — it is a cache with an archive's name. The spec's requirement is
// literally that no age- or size-based eviction may drop a manifest, and this
// is where that is true or false.
//
// Retention is limits-based rather than work-queue or interest: a manifest must
// survive being read, must survive having no reader at all, and must be
// readable again by the next consumer that wants the campaign's history.
func StreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:      Stream,
		Subjects:  []string{SubjectFilter},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		// The four ways a manifest could be evicted, all disabled. -1 is
		// JetStream's "unlimited"; 0 on MaxAge means no age limit.
		MaxAge:            0,
		MaxBytes:          -1,
		MaxMsgs:           -1,
		MaxMsgsPerSubject: -1,
		// DiscardNew rather than DiscardOld: with the limits above nothing can
		// be discarded either way, but if a limit is ever introduced by
		// accident, refusing the new append is loud at the writer while
		// dropping the oldest manifest is silent forever.
		Discard:    jetstream.DiscardNew,
		Duplicates: DuplicateWindow,
	}
}

// ConsumerConfig is the durable consumer the writer binds to learn that a turn
// resolved.
//
// It binds the STAGE stream filtered on the resolved subject, because that is
// where the rule pack publishes: `semmachina.turn.resolved` is one of its
// declared JetStream output ports and fires on the transition into `complete`
// and into `failed` alike.
//
// DeliverPolicy is "all", deliberately. A turn that resolved while the ledger
// writer was down must still be archived, and re-processing is safe here by
// construction rather than by luck — Append reads the turn's own subject before
// it writes, so a redelivery of any age converges on one manifest.
//
// MaxDeliver is unlimited for the reason intake's is: a transport failure must
// not silently drop a turn's only archive record, and the Nth attempt is not a
// better moment to give up than the first.
func ConsumerConfig() natsclient.StreamConsumerConfig {
	return natsclient.StreamConsumerConfig{
		StreamName:    rulepack.StageStream,
		ConsumerName:  ConsumerName,
		FilterSubject: rulepack.SubjectResolved,
		DeliverPolicy: "all",
		AckPolicy:     "explicit",
		MaxDeliver:    0,
		AckWait:       DefaultAckWait,
	}
}
