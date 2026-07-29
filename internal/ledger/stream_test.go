package ledger_test

import (
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The archive must not be able to forget a turn. Every limit that could evict a
// manifest is checked here rather than left to the defaults, because the default
// is the failure: an auto-created stream carries a seven-day MaxAge, and a
// campaign whose first week silently disappears is a cache with an archive's
// name.
func TestStreamConfig_HasNoWayToEvictAManifest(t *testing.T) {
	cfg := ledger.StreamConfig()

	if cfg.MaxAge != 0 {
		t.Errorf("MaxAge = %s; a manifest that expired is a turn the campaign can no longer account for", cfg.MaxAge)
	}
	for _, limit := range []struct {
		name  string
		value int64
	}{
		{"MaxBytes", cfg.MaxBytes},
		{"MaxMsgs", cfg.MaxMsgs},
		{"MaxMsgsPerSubject", cfg.MaxMsgsPerSubject},
	} {
		if limit.value != -1 {
			t.Errorf("%s = %d, want -1 (unlimited)", limit.name, limit.value)
		}
	}
	if cfg.Retention != jetstream.LimitsPolicy {
		t.Errorf("Retention = %v; a manifest must survive being read, and survive having no reader at all",
			cfg.Retention)
	}
	if cfg.Storage != jetstream.FileStorage {
		t.Errorf("Storage = %v; an archive that does not survive a restart is not an archive", cfg.Storage)
	}
	if cfg.Discard != jetstream.DiscardNew {
		t.Errorf("Discard = %v; if a limit is ever introduced by accident, refusing the append is loud at the "+
			"writer and dropping the oldest manifest is silent forever", cfg.Discard)
	}
	if cfg.Name != ledger.Stream {
		t.Errorf("Name = %q, want %q", cfg.Name, ledger.Stream)
	}
	if len(cfg.Subjects) != 1 || cfg.Subjects[0] != ledger.SubjectFilter {
		t.Errorf("Subjects = %v, want [%s]", cfg.Subjects, ledger.SubjectFilter)
	}
}

// The subject filter has to actually admit the subjects SubjectFor derives, or
// every append lands on a subject the stream does not carry.
func TestSubjectFor_ProducesSubjectsTheStreamFilterAdmits(t *testing.T) {
	prefix, ok := strings.CutSuffix(ledger.SubjectFilter, "*")
	if !ok {
		t.Fatalf("subject filter %q is not a single-token wildcard", ledger.SubjectFilter)
	}

	for _, turnID := range []string{"turn-act-1", "turn-a", "turn-A_1-b", strings.Repeat("t", 60)} {
		subject, err := ledger.SubjectFor(turnID)
		if err != nil {
			t.Fatalf("SubjectFor(%q): %v", turnID, err)
		}
		token, matched := strings.CutPrefix(subject, prefix)
		if !matched {
			t.Fatalf("subject %q does not sit under the stream filter %q", subject, ledger.SubjectFilter)
		}
		if token != turnID {
			t.Fatalf("subject token %q is not the turn id %q", token, turnID)
		}
		if strings.ContainsAny(token, ".*> \t") {
			t.Fatalf("turn id %q composed a multi-token or wildcard subject; a manifest would be filed where "+
				"no reader looks, or on a subject that matches other turns", turnID)
		}
	}
}

// A turn id that is not an entity-ID segment is refused before it can widen a
// subject. This is the anti-vacuity half of the test above: the alphabet
// guarantee only holds because the gate is real.
func TestSubjectFor_RefusesATurnIDThatWouldWidenTheSubject(t *testing.T) {
	for _, turnID := range []string{"", "turn.act", "turn act", "*", ">", "turn/1", strings.Repeat("t", 200)} {
		subject, err := ledger.SubjectFor(turnID)
		if err == nil {
			t.Fatalf("SubjectFor(%q) composed %q instead of refusing", turnID, subject)
		}
	}
}

// The writer learns that a turn resolved from the rule pack's own notification,
// so the consumer must bind the stream and subject the pack actually publishes
// on. A consumer filtered on a subject nobody publishes archives nothing, and
// looks exactly like a campaign in which nothing happened.
func TestConsumerConfig_BindsTheSubjectTheRulePackPublishes(t *testing.T) {
	cfg := ledger.ConsumerConfig()

	if cfg.StreamName != rulepack.StageStream {
		t.Errorf("StreamName = %q, want %q", cfg.StreamName, rulepack.StageStream)
	}
	if cfg.FilterSubject != rulepack.SubjectResolved {
		t.Errorf("FilterSubject = %q, want %q", cfg.FilterSubject, rulepack.SubjectResolved)
	}
	if cfg.DeliverPolicy != "all" {
		t.Errorf("DeliverPolicy = %q; a turn that resolved while the writer was down must still be archived",
			cfg.DeliverPolicy)
	}
	if cfg.AckPolicy != "explicit" {
		t.Errorf("AckPolicy = %q; the ack is the archive's own resume point", cfg.AckPolicy)
	}
	if cfg.MaxDeliver != 0 {
		t.Errorf("MaxDeliver = %d; a delivery ceiling silently discards a turn's only archive record",
			cfg.MaxDeliver)
	}
	// The subject is one the pack knows about, checked against the pack rather
	// than against a literal here.
	if !strings.HasPrefix(rulepack.SubjectResolved, rulepack.StageSubjectPrefix) {
		t.Errorf("the resolved subject %q is outside the stage subject space %q",
			rulepack.SubjectResolved, rulepack.StageSubjectPrefix)
	}
}

// The reconciliation scan must cover exactly the world instance the writer
// archives for. Deriving the prefix from the campaign entity is what makes
// "another world's turns" unreachable rather than merely unlikely.
func TestTurnPrefixForCampaign_ScansTheSameWorldInstanceItArchivesFor(t *testing.T) {
	prefix, err := ledger.TurnPrefixForCampaign(testCampaignID)
	if err != nil {
		t.Fatalf("TurnPrefixForCampaign: %v", err)
	}
	if prefix != "c360.semmachina.world1.starter.turn" {
		t.Fatalf("prefix = %q", prefix)
	}
	// The turn entity of this world sits under it; another world's does not.
	if !strings.HasPrefix(testTurnEntity, prefix+".") {
		t.Fatalf("this world's turn %q is not under its own prefix %q", testTurnEntity, prefix)
	}
	other, err := vocabulary.ComposeEntityID("c360", "world2", "starter", "turn", "turn-act-1")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if strings.HasPrefix(other, prefix+".") {
		t.Fatalf("another world's turn %q sits under this world's prefix %q", other, prefix)
	}
	// And the campaign entity itself does not, so the scan cannot try to
	// archive the sentinel it derived its own prefix from.
	if strings.HasPrefix(testCampaignID, prefix+".") {
		t.Fatalf("the campaign entity %q sits under the turn prefix %q", testCampaignID, prefix)
	}
}

func TestTurnPrefixForCampaign_RefusesANonCanonicalCampaignID(t *testing.T) {
	for _, id := range []string{"", "campaign", "c360.semmachina.world1.starter.campaign"} {
		if prefix, err := ledger.TurnPrefixForCampaign(id); err == nil {
			t.Fatalf("TurnPrefixForCampaign(%q) produced %q instead of refusing", id, prefix)
		}
	}
}
