package ledger_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// statusVerdict calls for the dice and declares the SAME effect in all three
// bands.
//
// The band is chosen by the seed and cannot be scripted, so a test that needs a
// known world change after a turn either searches for a seed or declares one
// outcome per band. Declaring one outcome is the honest version: it makes the
// world change deterministic without pretending the roll was.
func (w *archiveWorld) statusVerdict(status vocabulary.Status) map[string]any {
	return map[string]any{
		"scalars": map[string]any{
			"plausibility":  string(vocabulary.PlausibilityPlausible),
			"risk":          string(vocabulary.RiskModerate),
			"consequence":   string(vocabulary.ConsequenceHarm),
			"requires_roll": true,
		},
		"modifiers": []any{},
		"bands": map[string]any{
			string(vocabulary.BandMiss):    w.statusIntent(w.characterID, status),
			string(vocabulary.BandPartial): w.statusIntent(w.characterID, status),
			string(vocabulary.BandFull):    w.statusIntent(w.characterID, status),
		},
		"rationale": "One outcome, whichever way the dice fall.",
	}
}

// THE FINDING THIS TEST EXISTS FOR.
//
// The rules that publish `semmachina.turn.resolved` use the rule engine's
// `transition` operator, whose previous value is read from MatchState.FieldValues
// in RULE_STATE — not from the graph. With no recorded previous value a
// transition condition returns false unconditionally, so a turn that reaches
// `complete` against a fresh or cleared RULE_STATE never publishes the
// notification, and bootstrap replay does not rescue it either — the durable
// stale-replay guard skips an evaluation whose source revision it has already
// recorded, which is exactly a turn nobody has written to since (measured in
// internal/resume, not inferred). Without reconciliation that turn is simply
// absent from the campaign's history and nothing anywhere reports it.
//
// So: a turn that resolved with NO notification at all is archived by the boot
// pass, which is exactly the shape of the lost-notification failure.
func TestReconcile_ArchivesAResolvedTurnWhoseNotificationNeverArrived(t *testing.T) {
	world := startArchive(t)
	turnID, entityID := world.resolvedTurn(t, "act-lost", world.rollingVerdict())

	// Nothing has told the ledger about this turn.
	if _, err := world.reader.Manifest(t.Context(), turnID); err == nil {
		t.Fatal("the turn was archived before reconciliation ran; this test proves nothing")
	}

	report, err := world.writer.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Archived != 1 {
		t.Fatalf("reconciliation archived %d turns, want 1 (report %+v)", report.Archived, report)
	}
	if report.Scanned < 1 {
		t.Fatalf("reconciliation scanned %d turns", report.Scanned)
	}

	manifest, err := world.reader.Manifest(t.Context(), turnID)
	if err != nil {
		t.Fatalf("the reconciled turn is not in the archive: %v", err)
	}
	if manifest.Phase != vocabulary.PhaseComplete {
		t.Fatalf("archived phase = %q", manifest.Phase)
	}
	if manifest.TurnID != turnID {
		t.Fatalf("archived turn = %q, want %q", manifest.TurnID, turnID)
	}
	// It is a real manifest, not a placeholder: the references resolve.
	if _, err := world.content.GetNarration(t.Context(), parseRef(t, manifest.NarrationRef)); err != nil {
		t.Fatalf("the reconciled manifest's narration reference does not resolve: %v", err)
	}
	// And it is THIS turn's record, not one composed from whatever the scan
	// happened to reach first.
	if !strings.HasSuffix(entityID, "."+manifest.TurnID) {
		t.Fatalf("the archived manifest names turn %q, the resolved entity is %q", manifest.TurnID, entityID)
	}
}

// Reconciliation and the notification converge instead of doubling up, and they
// converge through ONE mechanism: Append's durable duplicate guard. A second
// pass must find nothing to do.
func TestReconcile_IsIdempotentAcrossRepeatedBoots(t *testing.T) {
	world := startArchive(t)
	_, entityID := world.resolvedTurn(t, "act-boot", world.rollingVerdict())

	first, err := world.writer.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if first.Archived != 1 {
		t.Fatalf("first pass archived %d, want 1", first.Archived)
	}

	second, err := world.writer.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second.Archived != 0 {
		t.Fatalf("the second boot archived %d turns again", second.Archived)
	}
	if second.AlreadyArchived != 1 {
		t.Fatalf("the second boot found %d turns already archived, want 1 (report %+v)",
			second.AlreadyArchived, second)
	}

	turnID, err := payloadTurnID(entityID)
	if err != nil {
		t.Fatalf("derive turn id: %v", err)
	}
	if count := world.manifestCount(t, turnID); count != 1 {
		t.Fatalf("the ledger holds %d manifests after two boots, want 1", count)
	}
}

// An in-flight turn is not the ledger's business. Archiving one would record an
// outcome that has not happened, into an append-only stream that could never
// correct it.
func TestReconcile_LeavesATurnStillInFlightAlone(t *testing.T) {
	world := startArchive(t)

	// A turn that stops at adjudication: accepted, phase advanced, no verdict.
	action := &payload.PlayerAction{
		ActionID:   "act-inflight",
		PlayerID:   world.playerID,
		CampaignID: world.campaignID,
		SceneID:    world.sceneID,
		Text:       "I hesitate in the doorway.",
		ArrivedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "ws://local/1"},
	}
	acceptance, err := world.recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	world.advance(t, acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseInterpreting)
	world.advance(t, acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)

	report, err := world.writer.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Archived != 0 {
		t.Fatalf("reconciliation archived %d in-flight turns", report.Archived)
	}
	if report.InFlight != 1 {
		t.Fatalf("reconciliation counted %d in-flight turns, want 1 (report %+v)", report.InFlight, report)
	}
	if _, err := world.reader.Manifest(t.Context(), acceptance.TurnID); err == nil {
		t.Fatal("an unfinished turn was archived")
	}
}

// The scan must page. With a page limit of two and five turns, a reconciliation
// that read one page and stopped would archive two turns and silently call the
// campaign complete.
func TestReconcile_PagesThroughEveryTurnInTheWorld(t *testing.T) {
	world := startArchive(t)

	const turns = 5
	ids := make([]string, 0, turns)
	for i := range turns {
		turnID, _ := world.resolvedTurn(t, "act-page-"+string(rune('a'+i)), world.rollingVerdict())
		ids = append(ids, turnID)
	}

	report, err := world.writer.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Archived != turns {
		t.Fatalf("reconciliation archived %d of %d turns; the page limit is 2, so a scan that stopped at the "+
			"first page would look exactly like this (report %+v)", report.Archived, turns, report)
	}
	for _, turnID := range ids {
		if _, err := world.reader.Manifest(t.Context(), turnID); err != nil {
			t.Fatalf("turn %s is missing from the archive after reconciliation: %v", turnID, err)
		}
	}
}

// The rule pack's notification is the fast path, and this is the wiring proof:
// the writer's own consumer, bound to the stage stream on the subject the pack
// publishes, archives a turn with nobody calling Append.
//
// Reconciliation runs inside Start, so the writer is started BEFORE the turn
// exists — otherwise the boot pass would archive it and this test would prove
// the reconciliation twice and the consumer never.
func TestWriter_ArchivesOnTheRulePacksResolvedNotification(t *testing.T) {
	world := startArchive(t)

	// The stage stream is the rule pack's output home; in production the stage
	// runner ensures it before ingress opens.
	world.harness.EnsureArchivalStream(t, rulepack.StageStream,
		[]string{rulepack.StageSubjectFilter}, "")
	if err := world.writer.Start(context.Background()); err != nil {
		t.Fatalf("start the ledger writer: %v", err)
	}
	t.Cleanup(world.harness.Client.StopAllConsumers)

	turnID, entityID := world.resolvedTurn(t, "act-notified", world.rollingVerdict())

	// Exactly the bytes the rule engine's publish action puts on the wire.
	notification, err := json.Marshal(map[string]any{
		"entity_id": entityID,
		"subject":   rulepack.SubjectResolved,
		"source":    rulepack.PackID,
	})
	if err != nil {
		t.Fatalf("encode the notification: %v", err)
	}
	if err := world.harness.Client.PublishToStream(t.Context(), rulepack.SubjectResolved, notification); err != nil {
		t.Fatalf("publish the resolved notification: %v", err)
	}

	manifest := world.awaitManifest(t, turnID)
	if manifest.Phase != vocabulary.PhaseComplete {
		t.Fatalf("archived phase = %q", manifest.Phase)
	}

	// And the delivery was acknowledged — a handler that failed after doing the
	// work leaves a delivery JetStream hands back forever, and the manifest
	// would be there either way.
	world.requireLedgerConsumerDrained(t)
}

// awaitManifest polls the archive, because the consumer archives asynchronously.
func (w *archiveWorld) awaitManifest(t *testing.T, turnID string) *payload.TurnManifest {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		manifest, err := w.reader.Manifest(t.Context(), turnID)
		if err == nil {
			return manifest
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("turn %s was never archived: %v", turnID, lastErr)
	return nil
}

func (w *archiveWorld) requireLedgerConsumerDrained(t *testing.T) {
	t.Helper()
	js, err := w.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	consumer, err := js.Consumer(t.Context(), rulepack.StageStream, ledger.ConsumerName)
	if err != nil {
		t.Fatalf("read the ledger consumer: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err := consumer.Info(t.Context())
		if err != nil {
			t.Fatalf("read the ledger consumer info: %v", err)
		}
		if info.NumAckPending == 0 && info.NumPending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ledger consumer left %d unacknowledged and %d unread notification(s); the manifest "+
				"landed anyway, which is how a failing handler hides", info.NumAckPending, info.NumPending)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// The archive is append-only, so two conflicting accounts of one turn is the one
// thing that makes the whole record untrustworthy: neither can be corrected. It
// must be an error rather than a second append or a silent overwrite.
func TestLedger_RefusesToArchiveASecondDifferentAccountOfOneTurn(t *testing.T) {
	world := startArchive(t)
	turnID, entityID := world.resolvedTurn(t, "act-conflict", world.rollingVerdict())
	world.append(t, entityID)

	// Change the turn's record after it was archived, the way a rogue writer
	// or a bad recovery would: a different narration reference on the turn.
	ref, err := world.content.PutNarration(t.Context(), entityID, &content.Narration{
		TurnID: turnID, Band: vocabulary.BandAuto, Prose: "Someone else's account of the same turn.",
	})
	if err != nil {
		t.Fatalf("store a second narration: %v", err)
	}
	if _, err := world.graph.MergeTriples(t.Context(), entityID, []message.Triple{{
		Subject:   entityID,
		Predicate: vocabulary.TurnEffectsRef.String(),
		Object:    ref.String(),
		Source:    "test-conflict",
		// A different value under a predicate the manifest carries; the
		// narration key is derived per turn, so this is the cheapest way to
		// make the turn compose differently without breaking its contract.
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}}); err != nil {
		t.Fatalf("rewrite the turn's effect reference: %v", err)
	}

	_, err = world.writer.Append(t.Context(), entityID)
	if err == nil {
		t.Fatal("the ledger accepted a second, different account of one turn")
	}
	if !strings.Contains(err.Error(), "DIFFERENT manifest") {
		t.Fatalf("rejection reason %q does not name the conflict", err.Error())
	}
	if count := world.manifestCount(t, turnID); count != 1 {
		t.Fatalf("the ledger holds %d manifests after a refused conflicting append, want 1", count)
	}
}

// The spec's reconstruction scenario, made concrete: ENTITY_STATES holds only
// the LATEST value of an attribute, and the earlier turn's context survives in
// the ledger plus the artifacts it points at.
func TestLedger_HistoryOutlivesGraphChurn(t *testing.T) {
	world := startArchive(t)

	firstTurn, firstEntity := world.resolvedTurn(t, "act-first", world.statusVerdict(vocabulary.StatusWounded))
	world.append(t, firstEntity)
	secondTurn, secondEntity := world.resolvedTurn(t, "act-second", world.statusVerdict(vocabulary.StatusHidden))
	world.append(t, secondEntity)

	// The graph has forgotten the first outcome: the status is single-valued
	// and the second turn replaced it.
	character := world.harness.AwaitEntity(t, world.characterID)
	statuses := testinfraObjects(character.Triples, vocabulary.CharacterStatusCurrent)
	if len(statuses) != 1 {
		t.Fatalf("the character holds %d statuses, want exactly 1 (single-valued facts replace)", len(statuses))
	}
	if statuses[0] != string(vocabulary.StatusHidden) {
		t.Fatalf("current status = %q, want %q", statuses[0], vocabulary.StatusHidden)
	}

	// The ledger still accounts for both turns, and the first turn's committed
	// batch still names the value the world no longer holds.
	first, err := world.reader.Manifest(t.Context(), firstTurn)
	if err != nil {
		t.Fatalf("the earlier turn is missing from the archive: %v", err)
	}
	second, err := world.reader.Manifest(t.Context(), secondTurn)
	if err != nil {
		t.Fatalf("the later turn is missing from the archive: %v", err)
	}
	if !first.RecordedAt.Before(second.RecordedAt) && !first.RecordedAt.Equal(second.RecordedAt) {
		t.Fatalf("the archive orders the turns backwards: %s then %s", first.RecordedAt, second.RecordedAt)
	}

	batch, err := world.content.GetEffectBatch(t.Context(), parseRef(t, first.EffectBatchRef))
	if err != nil {
		t.Fatalf("the earlier turn's effect batch does not resolve: %v", err)
	}
	if len(batch.Intents) != 1 {
		t.Fatalf("the earlier batch committed %d intents, want 1", len(batch.Intents))
	}
	if batch.Intents[0].Status != vocabulary.StatusWounded {
		t.Fatalf("the earlier turn's committed intent reads %q; the value the world no longer holds is not "+
			"reconstructable", batch.Intents[0].Status)
	}

	// And the prose that explained it is still readable, from the reference the
	// manifest preserved.
	narration, err := world.content.GetNarration(t.Context(), parseRef(t, first.NarrationRef))
	if err != nil {
		t.Fatalf("the earlier turn's narration does not resolve: %v", err)
	}
	if narration.TurnID != firstTurn {
		t.Fatalf("the earlier turn's narration belongs to %q", narration.TurnID)
	}
}

func testinfraObjects(triples []message.Triple, predicate vocabulary.Predicate) []string {
	var out []string
	for _, triple := range triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object.(string)) //nolint:errcheck // a non-string here fails the test loudly
		}
	}
	return out
}

func payloadTurnID(turnEntityID string) (string, error) {
	parts := strings.Split(turnEntityID, ".")
	turnID := parts[len(parts)-1]
	return turnID, payload.RequireTurnEntityID(turnID, turnEntityID)
}
