package ledger_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The archive is a claim about durable infrastructure, and every substitute for
// that infrastructure would hide the failure it is meant to catch.
//
// A fake stream cannot have a retention policy, so "no manifest can be evicted"
// would be asserted against a map. A fake ObjectStore would make "the references
// resolve" a test of the test's own bookkeeping. And the roll a replay
// reproduces has to be one the dice component actually produced, from a seed
// actually stored on a campaign entity in ENTITY_STATES — a replay compared
// against a fixture proves the fixture.
//
// So the turn artifacts below are made by the PRODUCTION components: the real
// terminal-tool executors, the real dice stage, the real effect applier, the
// real turn recorder, against real graph-ingest. What is stood in for is the
// agentic loop — a model call and a tool dispatch, which belong to the E2E task
// that owns the mock endpoint — so the scripted exit is fed to the real
// executor directly.

func TestMain(m *testing.M) {
	// The rule processor rejects a canonical-but-undeclared predicate, so this
	// must precede any rule config load in every binary and test.
	if err := vocabulary.RegisterPredicates(); err != nil {
		fmt.Fprintf(os.Stderr, "register semmachina predicates: %v\n", err)
		os.Exit(1)
	}
	os.Exit(testinfra.RunTests(m))
}

var worldCounter atomic.Int64

const (
	testOrg      = "c360"
	testTemplate = "starter"
)

// archiveWorld is one campaign's whole ledger path over a real broker: real
// graph-ingest, a real ObjectStore, a real campaign seed, and the ledger's own
// stream.
type archiveWorld struct {
	harness   *testinfra.Harness
	graph     *graphio.Store
	content   *content.Store
	recorder  *turn.Recorder
	gate      *campaign.Gate
	writer    *ledger.Writer
	reader    *ledger.Reader
	namespace string

	campaignID  string
	sceneID     string
	characterID string
	playerID    string

	verdictTool   *persona.VerdictExecutor
	narrationTool *persona.NarrationExecutor
	diceStage     *stage.Resolver
	effectStage   *stage.Effector
	completeStage *stage.Completer
}

func startArchive(t *testing.T) *archiveWorld {
	t.Helper()
	harness := testinfra.Require(t)
	harness.EnsureArchivalStream(t, ledger.Stream, []string{ledger.SubjectFilter},
		ledger.DuplicateWindow.String())
	namespace := fmt.Sprintf("w%d", worldCounter.Add(1))

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("graphio.NewStore: %v", err)
	}
	backend, err := content.NewObjectStore(t.Context(), harness.Client, content.WithBucket("LEDGER_"+namespace))
	if err != nil {
		t.Fatalf("content.NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	identity := campaign.Identity{Org: testOrg, WorldNS: namespace, Template: testTemplate}
	gate, err := campaign.NewGate(store, identity)
	if err != nil {
		t.Fatalf("campaign.NewGate: %v", err)
	}
	instantiation, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim the campaign: %v", err)
	}
	if !instantiation.Fresh {
		t.Fatalf("campaign %s already existed; each test owns its own world namespace", instantiation.CampaignID)
	}

	recorder, err := turn.NewRecorder(
		store, artifacts, turn.Identity{Org: testOrg, WorldNS: namespace, Template: testTemplate})
	if err != nil {
		t.Fatalf("turn.NewRecorder: %v", err)
	}

	world := &archiveWorld{
		harness: harness, graph: store, content: artifacts, recorder: recorder, gate: gate,
		namespace:   namespace,
		campaignID:  instantiation.CampaignID,
		sceneID:     composeID(t, namespace, "scene", "gatehouse"),
		characterID: composeID(t, namespace, "character", "rook"),
		playerID:    composeID(t, namespace, "player", "one"),
	}
	world.seedWorld(t)
	world.buildStages(t, instantiation)
	world.buildLedger(t)
	return world
}

func composeID(t *testing.T, namespace, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(testOrg, namespace, testTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose %s id: %v", kind, err)
	}
	return id
}

// seedWorld creates the smallest world the applier accepts: somewhere to be,
// someone to be it, and a player bound to them.
func (w *archiveWorld) seedWorld(t *testing.T) {
	t.Helper()
	w.createEntity(t, w.sceneID, map[string]any{
		vocabulary.WorldEntityKind.String(): string(vocabulary.EntityKindScene),
		vocabulary.WorldEntityName.String(): "The Gatehouse",
	})
	w.createEntity(t, w.characterID, map[string]any{
		vocabulary.WorldEntityKind.String():        string(vocabulary.EntityKindCharacter),
		vocabulary.WorldEntityName.String():        "Rook",
		vocabulary.WorldLocationCurrent.String():   w.sceneID,
		vocabulary.CharacterStatusCurrent.String(): string(vocabulary.StatusHealthy),
	})
	w.createEntity(t, w.playerID, map[string]any{
		vocabulary.WorldEntityKind.String():        string(vocabulary.EntityKindPlayer),
		vocabulary.PlayerCharacterCurrent.String(): w.characterID,
	})
	w.harness.AwaitEntity(t, w.characterID)
	w.harness.AwaitEntity(t, w.playerID)
}

func (w *archiveWorld) createEntity(t *testing.T, id string, facts map[string]any) {
	t.Helper()
	at := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	triples := make([]message.Triple, 0, len(facts))
	for predicate, object := range facts {
		triples = append(triples, message.Triple{
			Subject: id, Predicate: predicate, Object: object,
			Source: "test", Timestamp: at, Confidence: 1.0,
		})
	}
	if _, err := w.graph.CreateEntity(t.Context(), &graph.EntityState{
		ID: id,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version: 1, UpdatedAt: at, Triples: triples,
	}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func (w *archiveWorld) buildStages(t *testing.T, instantiation campaign.Instantiation) {
	t.Helper()
	var err error
	if w.verdictTool, err = persona.NewVerdictExecutor(w.content, w.graph); err != nil {
		t.Fatalf("NewVerdictExecutor: %v", err)
	}
	if w.narrationTool, err = persona.NewNarrationExecutor(w.content, w.graph); err != nil {
		t.Fatalf("NewNarrationExecutor: %v", err)
	}

	roller, err := dice.NewRoller(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("dice.NewRoller: %v", err)
	}
	resolver, err := dice.NewResolver(roller, w.graph, instantiation)
	if err != nil {
		t.Fatalf("dice.NewResolver: %v", err)
	}
	if w.diceStage, err = stage.NewResolver(
		w.recorder, w.graph, w.content, w.content, w.graph, resolver); err != nil {
		t.Fatalf("stage.NewResolver: %v", err)
	}

	applier, err := effect.NewApplier(w.graph)
	if err != nil {
		t.Fatalf("effect.NewApplier: %v", err)
	}
	if w.effectStage, err = stage.NewEffector(
		w.recorder, w.recorder, w.graph, w.content, w.content, w.content, applier); err != nil {
		t.Fatalf("stage.NewEffector: %v", err)
	}
	if w.completeStage, err = stage.NewCompleter(w.recorder); err != nil {
		t.Fatalf("stage.NewCompleter: %v", err)
	}
}

func (w *archiveWorld) buildLedger(t *testing.T) {
	t.Helper()
	decoder := message.NewDecoder(w.harness.Registry)

	writer, err := ledger.NewWriter(
		w.graph, w.harness.Client, w.harness.Client, w.harness.Client, decoder, w.campaignID,
		ledger.WithPageLimit(2))
	if err != nil {
		t.Fatalf("ledger.NewWriter: %v", err)
	}
	w.writer = writer

	reader, err := ledger.NewReader(w.harness.Client, decoder, w.content, w.gate)
	if err != nil {
		t.Fatalf("ledger.NewReader: %v", err)
	}
	w.reader = reader
}

// rollingVerdict calls for the dice and declares all three bands, which is the
// only shape the engine accepts when a roll is required. The BAND is chosen by
// the seed and never by the script.
func (w *archiveWorld) rollingVerdict() map[string]any {
	return map[string]any{
		"scalars": map[string]any{
			"plausibility":  string(vocabulary.PlausibilityPlausible),
			"risk":          string(vocabulary.RiskModerate),
			"consequence":   string(vocabulary.ConsequenceHarm),
			"requires_roll": true,
		},
		"modifiers": []any{
			map[string]any{"source": string(vocabulary.ModifierEquipment), "value": 1, "note": "crowbar"},
		},
		"bands": map[string]any{
			string(vocabulary.BandMiss):    w.statusIntent(w.characterID, vocabulary.StatusWounded),
			string(vocabulary.BandPartial): w.statusIntent(w.characterID, vocabulary.StatusExhausted),
			string(vocabulary.BandFull):    w.statusIntent(w.characterID, vocabulary.StatusHealthy),
		},
		"rationale": "The gate is heavy but the fiction allows it.",
	}
}

// refusedVerdict declines the dice and names a wrong-KIND target: moving a
// character into an item is well formed and refused by the applier, which is how
// a real failed turn is produced rather than simulated.
func (w *archiveWorld) refusedVerdict() map[string]any {
	return map[string]any{
		"scalars": map[string]any{
			"plausibility":  string(vocabulary.PlausibilityCertain),
			"risk":          string(vocabulary.RiskNone),
			"consequence":   string(vocabulary.ConsequenceNone),
			"requires_roll": false,
		},
		"modifiers": []any{},
		"bands": map[string]any{
			string(vocabulary.BandAuto): w.statusIntent(w.sceneID, vocabulary.StatusHidden),
		},
		"rationale": "Nothing is at stake; the fiction already decided.",
	}
}

func (w *archiveWorld) statusIntent(target string, status vocabulary.Status) []any {
	return []any{map[string]any{
		"type": string(vocabulary.EffectSetStatus), "target": target, "status": string(status),
	}}
}

// resolvedTurn drives one turn all the way to a terminal phase through the
// production stages, and returns its identity.
//
// Nothing here is a shortcut around a stage: the recorder writes every phase,
// the verdict tool stores and projects the verdict, the dice stage rolls and
// records, the applier commits, the narration tool writes prose and its
// reference. Only the agentic loop is stood in for.
func (w *archiveWorld) resolvedTurn(t *testing.T, actionID string, verdict map[string]any) (turnID, entityID string) {
	t.Helper()

	action := &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   w.playerID,
		CampaignID: w.campaignID,
		SceneID:    w.sceneID,
		Text:       "I shoulder the gate open.",
		ArrivedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "ws://local/1"},
	}
	acceptance, err := w.recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("accept action %s: %v", actionID, err)
	}
	if !acceptance.Created {
		t.Fatalf("action %s did not create a turn", actionID)
	}
	turnID, entityID = acceptance.TurnID, acceptance.TurnEntityID
	trigger := w.trigger(t, turnID, entityID)

	// Adjudication: the real terminal tool, a scripted exit.
	w.advance(t, turnID, entityID, vocabulary.PhaseInterpreting)
	w.advance(t, turnID, entityID, vocabulary.PhaseAdjudicating)
	w.execute(t, w.verdictTool, agentic.ToolCall{
		ID: "call-" + actionID, Name: persona.VerdictToolName,
		Arguments: verdict, Metadata: w.metadata(turnID, entityID, actionID, ""),
	})

	rolls, _ := verdict["scalars"].(map[string]any)["requires_roll"].(bool)
	if rolls {
		if err := w.diceStage.Run(t.Context(), trigger); err != nil {
			t.Fatalf("dice stage: %v", err)
		}
	}
	if err := w.effectStage.Run(t.Context(), trigger); err != nil {
		t.Fatalf("effect stage: %v", err)
	}

	phase := w.phaseOf(t, entityID)
	if phase == vocabulary.PhaseFailed {
		return turnID, entityID
	}

	band := w.bandOf(t, entityID, rolls)
	w.advance(t, turnID, entityID, vocabulary.PhaseCompanion)
	w.advance(t, turnID, entityID, vocabulary.PhaseNarrating)
	w.execute(t, w.narrationTool, agentic.ToolCall{
		ID: "call-" + actionID, Name: persona.NarrationToolName,
		Arguments: map[string]any{"prose": "Rook shoulders the gate open and the hinges scream."},
		Metadata:  w.metadata(turnID, entityID, actionID, band),
	})
	if err := w.completeStage.Run(t.Context(), trigger); err != nil {
		t.Fatalf("completion stage: %v", err)
	}
	return turnID, entityID
}

func (w *archiveWorld) trigger(t *testing.T, turnID, entityID string) stage.Trigger {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(vocabulary.PhaseApplying)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	return stage.Trigger{TurnEntityID: entityID, TurnID: turnID, Subject: subject}
}

func (w *archiveWorld) metadata(turnID, entityID, actionID string, band vocabulary.OutcomeBand) map[string]any {
	metadata := map[string]any{
		persona.MetadataKeyTurnID:       turnID,
		persona.MetadataKeyTurnEntityID: entityID,
		persona.MetadataKeyActionID:     actionID,
		persona.MetadataKeySceneID:      w.sceneID,
	}
	if band != "" {
		metadata[persona.MetadataKeyBand] = string(band)
	}
	return metadata
}

type toolExecutor interface {
	Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error)
}

func (w *archiveWorld) execute(t *testing.T, tool toolExecutor, call agentic.ToolCall) {
	t.Helper()
	result, err := tool.Execute(t.Context(), call)
	if err != nil {
		t.Fatalf("the %s tool refused a scripted exit: %v", call.Name, err)
	}
	if result.Error != "" {
		t.Fatalf("the %s tool returned an error result: %s", call.Name, result.Error)
	}
}

func (w *archiveWorld) advance(t *testing.T, turnID, entityID string, phase vocabulary.TurnPhase) {
	t.Helper()
	if _, err := w.recorder.Advance(t.Context(), turnID, entityID, phase); err != nil {
		t.Fatalf("advance turn %s to %s: %v", entityID, phase, err)
	}
}

func (w *archiveWorld) phaseOf(t *testing.T, entityID string) vocabulary.TurnPhase {
	t.Helper()
	phase, err := w.recorder.Current(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read the phase of %s: %v", entityID, err)
	}
	return phase
}

func (w *archiveWorld) bandOf(t *testing.T, entityID string, rolled bool) vocabulary.OutcomeBand {
	t.Helper()
	if !rolled {
		return vocabulary.BandAuto
	}
	state, err := w.graph.GetEntity(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read turn %s: %v", entityID, err)
	}
	object := testinfra.FirstObject(state, vocabulary.TurnRollBand.String())
	if object == nil {
		t.Fatalf("turn %s carries no roll band after the dice stage", entityID)
	}
	band, err := vocabulary.ParseOutcomeBand(fmt.Sprint(object))
	if err != nil {
		t.Fatalf("turn %s records band %v: %v", entityID, object, err)
	}
	return band
}

func (w *archiveWorld) append(t *testing.T, entityID string) ledger.Outcome {
	t.Helper()
	outcome, err := w.writer.Append(t.Context(), entityID)
	if err != nil {
		t.Fatalf("append the manifest for %s: %v", entityID, err)
	}
	return outcome
}

// A completed turn's manifest must index the turn's REAL artifacts, and the only
// way to know that is to follow every reference into the store it names and find
// what the stage actually wrote there.
func TestLedger_ArchivesACompletedTurnWhoseReferencesResolve(t *testing.T) {
	world := startArchive(t)
	turnID, entityID := world.resolvedTurn(t, "act-complete", world.rollingVerdict())

	outcome := world.append(t, entityID)
	if !outcome.Appended {
		t.Fatal("the first append of a turn wrote nothing")
	}
	manifest := outcome.Manifest

	if manifest.TurnID != turnID {
		t.Fatalf("manifest turn id = %q, want %q", manifest.TurnID, turnID)
	}
	if manifest.Phase != vocabulary.PhaseComplete {
		t.Fatalf("manifest phase = %q, want %q", manifest.Phase, vocabulary.PhaseComplete)
	}
	if manifest.CampaignID != world.campaignID {
		t.Fatalf("manifest campaign = %q, want %q", manifest.CampaignID, world.campaignID)
	}
	if manifest.WorldTime != 0 {
		t.Fatalf("world_time = %d; the world clock is a later stage and the field is present and zero until then",
			manifest.WorldTime)
	}

	// Every reference resolves, and resolves to THIS turn's artifact.
	action, err := world.content.GetAction(t.Context(), parseRef(t, manifest.ActionRef))
	if err != nil {
		t.Fatalf("the archived action reference does not resolve: %v", err)
	}
	if action.ActionID != manifest.ActionID {
		t.Fatalf("the archived action reference resolves to action %q, manifest says %q",
			action.ActionID, manifest.ActionID)
	}
	if action.Text == "" {
		t.Fatal("the archived action reference resolves to an action with no text")
	}

	verdict, err := world.content.GetVerdict(t.Context(), parseRef(t, manifest.VerdictRef))
	if err != nil {
		t.Fatalf("the archived verdict reference does not resolve: %v", err)
	}
	if verdict.TurnID != turnID {
		t.Fatalf("the archived verdict belongs to turn %q", verdict.TurnID)
	}

	roll, err := world.content.GetRoll(t.Context(), parseRef(t, manifest.RollRef))
	if err != nil {
		t.Fatalf("the archived roll reference does not resolve: %v", err)
	}
	if roll.TurnID != turnID {
		t.Fatalf("the archived roll belongs to turn %q", roll.TurnID)
	}

	batch, err := world.content.GetEffectBatch(t.Context(), parseRef(t, manifest.EffectBatchRef))
	if err != nil {
		t.Fatalf("the archived effect-batch reference does not resolve: %v", err)
	}
	if batch.Band != roll.Band {
		t.Fatalf("the archived batch committed band %q while the roll selected %q", batch.Band, roll.Band)
	}

	narration, err := world.content.GetNarration(t.Context(), parseRef(t, manifest.NarrationRef))
	if err != nil {
		t.Fatalf("the archived narration reference does not resolve: %v", err)
	}
	if narration.Prose == "" {
		t.Fatal("the archived narration reference resolves to prose that is empty")
	}

	// The roll-gate agreement is recorded, with the version of the mapping it
	// was computed under — not left to be re-derived by a reader.
	gate := manifest.RollGate
	if gate == nil {
		t.Fatal("a completed turn was archived with no roll gate")
	}
	if gate.Reported != verdict.Scalars.RequiresRoll {
		t.Fatalf("archived gate reports %v, the verdict reported %v", gate.Reported, verdict.Scalars.RequiresRoll)
	}
	if gate.Mapping != vocabulary.RollGateMappingVersion() {
		t.Fatalf("archived mapping %q, want %q", gate.Mapping, vocabulary.RollGateMappingVersion())
	}
}

// A failed turn is part of the campaign's history. It is archived with the
// closed reason it failed for, and with no narration, because it was never
// narrated.
func TestLedger_ArchivesAFailedTurnWithItsReason(t *testing.T) {
	world := startArchive(t)
	_, entityID := world.resolvedTurn(t, "act-refused", world.refusedVerdict())

	if phase := world.phaseOf(t, entityID); phase != vocabulary.PhaseFailed {
		t.Fatalf("the refused turn is in phase %q, want %q", phase, vocabulary.PhaseFailed)
	}

	manifest := world.append(t, entityID).Manifest
	if manifest.Phase != vocabulary.PhaseFailed {
		t.Fatalf("manifest phase = %q", manifest.Phase)
	}
	if _, err := vocabulary.ParseFailureReason(manifest.FailureReason); err != nil {
		t.Fatalf("the archived failure reason %q is not a closed code: %v", manifest.FailureReason, err)
	}
	if manifest.FailureReason != string(vocabulary.FailureEffectEntityKind) {
		t.Fatalf("failure reason = %q, want %q", manifest.FailureReason, vocabulary.FailureEffectEntityKind)
	}
	if manifest.NarrationRef != "" {
		t.Fatalf("a failed turn archived narration %q", manifest.NarrationRef)
	}
	if manifest.EffectBatchRef != "" {
		t.Fatalf("a refused batch was archived as applied: %q", manifest.EffectBatchRef)
	}
	// It WAS adjudicated, so the gate it ran under is part of the record.
	if manifest.RollGate == nil {
		t.Fatal("an adjudicated turn that failed later archived no roll gate")
	}
}

// The ledger contains exactly one manifest per turn, however many times the
// writer is triggered — and the guard is durable rather than a de-duplication
// window, so it holds for a redelivery of any age.
func TestLedger_DropsADuplicateAppendAndLeavesExactlyOneManifest(t *testing.T) {
	world := startArchive(t)
	_, entityID := world.resolvedTurn(t, "act-dup", world.rollingVerdict())

	first := world.append(t, entityID)
	if !first.Appended {
		t.Fatal("the first append wrote nothing")
	}
	for attempt := range 3 {
		again := world.append(t, entityID)
		if again.Appended {
			t.Fatalf("append %d wrote a second manifest for one turn", attempt+2)
		}
		// A dropped duplicate still answers with the archived record: a caller
		// asking "is this turn archived?" gets the manifest either way. Checked
		// before the field access so a guard that returned nothing fails here
		// with a sentence rather than a nil dereference.
		if again.Manifest == nil {
			t.Fatalf("append %d dropped the duplicate and returned no manifest; the archived record is what a "+
				"caller needs back", attempt+2)
		}
		if again.Manifest.TurnID != first.Manifest.TurnID {
			t.Fatalf("the duplicate append returned another turn's manifest: %q", again.Manifest.TurnID)
		}
	}

	if count := world.manifestCount(t, first.Manifest.TurnID); count != 1 {
		t.Fatalf("the ledger holds %d manifests for one turn, want exactly 1", count)
	}
}

// manifestCount reads the stream's own message count for a turn's subject.
//
// The stream rather than the writer's answer, because "the writer says it did
// not append" and "the archive holds one record" are different claims and only
// the second is the requirement.
func (w *archiveWorld) manifestCount(t *testing.T, turnID string) uint64 {
	t.Helper()
	subject, err := ledger.SubjectFor(turnID)
	if err != nil {
		t.Fatalf("SubjectFor: %v", err)
	}
	js, err := w.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.Stream(t.Context(), ledger.Stream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", ledger.Stream, err)
	}
	info, err := stream.Info(t.Context(), jetstream.WithSubjectFilter(subject))
	if err != nil {
		t.Fatalf("read the %s stream info: %v", ledger.Stream, err)
	}
	return info.State.Subjects[subject]
}

// The archive's no-eviction promise, read back off the stream the broker
// actually created rather than off the config we asked for.
func TestLedger_TheCreatedStreamCannotEvictAManifest(t *testing.T) {
	world := startArchive(t)
	_, entityID := world.resolvedTurn(t, "act-retention", world.rollingVerdict())
	world.append(t, entityID)

	js, err := world.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.Stream(t.Context(), ledger.Stream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", ledger.Stream, err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read the %s stream info: %v", ledger.Stream, err)
	}

	if info.Config.MaxAge != 0 {
		t.Errorf("the created stream carries MaxAge %s; a manifest that expired is a turn the campaign can no "+
			"longer account for", info.Config.MaxAge)
	}
	for _, limit := range []struct {
		name  string
		value int64
	}{
		{"MaxBytes", info.Config.MaxBytes},
		{"MaxMsgs", info.Config.MaxMsgs},
		{"MaxMsgsPerSubject", info.Config.MaxMsgsPerSubject},
	} {
		if limit.value > 0 {
			t.Errorf("the created stream carries %s = %d; the archive must not be able to forget a turn",
				limit.name, limit.value)
		}
	}
	if info.Config.Storage != jetstream.FileStorage {
		t.Errorf("the created stream is %v-backed; an archive that dies with the process is not an archive",
			info.Config.Storage)
	}
}

// The whole 8.3 claim, against real infrastructure: a manifest alone is enough
// to re-execute the roll byte for byte, and the narration comes back from the
// preserved reference.
//
// SEVERAL turns are played and then replayed OUT OF ORDER, and that is the part
// of this test that does any work. A single turn replayed once proves almost
// nothing: the producing roller and the replaying roller would each be making
// their first call, so a roller whose dice depended on what it had rolled before
// would agree with itself and the test would pass. It did, until this test was
// mutation-checked against exactly that defect — a plain counter mixed into the
// seed, which the dice package's own purity scan cannot see because it bans a
// generator-typed field rather than state in general.
//
// Playing three turns through one resolver and replaying the last one first is
// what makes order-dependence visible: the producer is on its third call and the
// replay on its first.
func TestReplay_ReproducesTheRollByteForByteFromAManifestAlone(t *testing.T) {
	world := startArchive(t)

	const turns = 3
	played := make([]string, 0, turns)
	entities := make([]string, 0, turns)
	for i := range turns {
		turnID, entityID := world.resolvedTurn(t, fmt.Sprintf("act-replay-%d", i), world.rollingVerdict())
		world.append(t, entityID)
		played = append(played, turnID)
		entities = append(entities, entityID)
	}

	for i := turns - 1; i >= 0; i-- {
		turnID, entityID := played[i], entities[i]

		// Read the manifest back off the stream — nothing is carried over from
		// the append. This is the "from a manifest alone" part.
		manifest, err := world.reader.Manifest(t.Context(), turnID)
		if err != nil {
			t.Fatalf("read the archived manifest for %s: %v", turnID, err)
		}

		replay, err := world.reader.Replay(t.Context(), manifest)
		if err != nil {
			t.Fatalf("replay %s: %v", turnID, err)
		}
		if !replay.Rolled() {
			t.Fatalf("turn %s reached the dice but replayed as a no-roll turn", turnID)
		}

		// The reader already compared these; compare them again here so the
		// test does not rest on the code under test agreeing with itself.
		archived, err := json.Marshal(replay.Roll)
		if err != nil {
			t.Fatalf("encode the archived roll: %v", err)
		}
		reproduced, err := json.Marshal(replay.Reproduced)
		if err != nil {
			t.Fatalf("encode the re-executed roll: %v", err)
		}
		if string(archived) != string(reproduced) {
			t.Fatalf("re-executing turn %s did not reproduce it:\n archived %s\n replayed %s",
				turnID, archived, reproduced)
		}
		if replay.Roll.Band != replay.Reproduced.Band || replay.Roll.Total != replay.Reproduced.Total {
			t.Fatalf("turn %s changed band or total under replay", turnID)
		}

		// And it matches what the graph recorded at the time, which is what the
		// rule pack routed on.
		state, err := world.graph.GetEntity(t.Context(), entityID)
		if err != nil {
			t.Fatalf("read turn %s: %v", turnID, err)
		}
		if got := fmt.Sprint(testinfra.FirstObject(state, vocabulary.TurnRollBand.String())); got !=
			string(replay.Reproduced.Band) {
			t.Fatalf("turn %s: the graph recorded band %q and replay reproduces %q",
				turnID, got, replay.Reproduced.Band)
		}
	}
}

// Narration is READ from the preserved reference, never regenerated. Proven by
// changing the stored prose behind the reference: a reader that produced prose
// would return the same words as before, and a reader that reads returns the new
// ones.
func TestReplay_ReadsNarrationFromThePreservedRefRatherThanProducingIt(t *testing.T) {
	world := startArchive(t)
	turnID, entityID := world.resolvedTurn(t, "act-prose", world.rollingVerdict())
	world.append(t, entityID)

	manifest, err := world.reader.Manifest(t.Context(), turnID)
	if err != nil {
		t.Fatalf("read the archived manifest: %v", err)
	}
	before, err := world.reader.Replay(t.Context(), manifest)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if before.Narration == nil {
		t.Fatal("a completed turn replayed with no narration")
	}
	original := before.Narration.Prose

	// Overwrite the stored prose at the SAME derived key the reference
	// addresses. Nothing else about the turn changes.
	const rewritten = "A different sentence entirely, written by nobody who was there."
	if _, err := world.content.PutNarration(t.Context(), entityID, &content.Narration{
		TurnID: turnID, Band: before.Roll.Band, Prose: rewritten,
	}); err != nil {
		t.Fatalf("rewrite the stored narration: %v", err)
	}

	after, err := world.reader.Replay(t.Context(), manifest)
	if err != nil {
		t.Fatalf("replay after the store changed: %v", err)
	}
	if after.Narration.Prose == original {
		t.Fatal("replay returned the original prose after the stored object changed; it is not reading the " +
			"reference, so a 'replay' would be producing prose rather than recovering it")
	}
	if after.Narration.Prose != rewritten {
		t.Fatalf("replay returned %q, want the stored %q", after.Narration.Prose, rewritten)
	}
}

// The reader must be able to SAY the roll is not reproducible, or its agreement
// means nothing. The archived roll object is rewritten with different dice and
// the replay is expected to refuse.
func TestReplay_RefusesAnArchivedRollThatItsOwnInputsNoLongerProduce(t *testing.T) {
	world := startArchive(t)
	turnID, entityID := world.resolvedTurn(t, "act-tamper", world.rollingVerdict())
	world.append(t, entityID)

	manifest, err := world.reader.Manifest(t.Context(), turnID)
	if err != nil {
		t.Fatalf("read the archived manifest: %v", err)
	}
	replay, err := world.reader.Replay(t.Context(), manifest)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// Rewrite the stored roll with a DIFFERENT but internally coherent record:
	// dice that sum to a total whose band it correctly declares. It passes its
	// own contract; it is simply not what the seed produces.
	spec, err := vocabulary.MechanicSpecFor(replay.Roll.Mechanic)
	if err != nil {
		t.Fatalf("MechanicSpecFor: %v", err)
	}
	tampered := *replay.Roll
	tampered.Dice = []int{6, 6}
	if replay.Roll.Dice[0] == 6 && replay.Roll.Dice[1] == 6 {
		tampered.Dice = []int{1, 1}
	}
	tampered.Total = tampered.Dice[0] + tampered.Dice[1] + tampered.ModifierTotal
	tampered.Band = spec.BandForTotal(tampered.Total)
	if err := tampered.Validate(); err != nil {
		t.Fatalf("the tampered roll is not internally coherent, so this test would prove the wrong thing: %v", err)
	}
	if _, err := world.content.PutRoll(t.Context(), entityID, &tampered); err != nil {
		t.Fatalf("rewrite the stored roll: %v", err)
	}

	_, err = world.reader.Replay(t.Context(), manifest)
	if err == nil {
		t.Fatal("replay accepted a roll its own inputs do not produce; seeded determinism would be unfalsifiable")
	}
	if !errors.Is(err, ledger.ErrNotReproducible) {
		t.Fatalf("replay refused with %v, want an ErrNotReproducible", err)
	}
}

// A no-roll turn replays honestly: there is nothing deterministic to re-execute
// and the reader says so rather than inventing a roll.
func TestReplay_ANoRollTurnHasNothingToReExecute(t *testing.T) {
	world := startArchive(t)
	// A no-roll verdict whose auto band targets the CHARACTER, so it commits
	// rather than being refused.
	verdict := world.refusedVerdict()
	verdict["bands"] = map[string]any{
		string(vocabulary.BandAuto): world.statusIntent(world.characterID, vocabulary.StatusHidden),
	}
	turnID, entityID := world.resolvedTurn(t, "act-noroll", verdict)
	world.append(t, entityID)

	manifest, err := world.reader.Manifest(t.Context(), turnID)
	if err != nil {
		t.Fatalf("read the archived manifest: %v", err)
	}
	if manifest.RollRef != "" {
		t.Fatalf("a no-roll turn archived roll reference %q", manifest.RollRef)
	}

	replay, err := world.reader.Replay(t.Context(), manifest)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Rolled() {
		t.Fatal("a no-roll turn replayed as rolled")
	}
	if replay.Reproduced != nil {
		t.Fatal("a no-roll turn produced a re-executed roll")
	}
	if replay.Narration == nil {
		t.Fatal("a completed no-roll turn replayed with no narration")
	}
	if replay.Narration.Band != vocabulary.BandAuto {
		t.Fatalf("the archived narration voices band %q, want %q", replay.Narration.Band, vocabulary.BandAuto)
	}
}

// A turn the archive has no record of is a distinct answer, not an empty
// manifest.
func TestReplay_ReportsATurnTheArchiveNeverRecorded(t *testing.T) {
	world := startArchive(t)
	if _, err := world.reader.Manifest(t.Context(), "turn-never-happened"); !errors.Is(
		err, ledger.ErrManifestNotFound) {
		t.Fatalf("reading an unarchived turn returned %v, want ErrManifestNotFound", err)
	}
}

func parseRef(t *testing.T, value string) content.Ref {
	t.Helper()
	ref, err := content.ParseRef(value)
	if err != nil {
		t.Fatalf("the archived reference %q does not parse: %v", value, err)
	}
	if ref.IsZero() {
		t.Fatalf("the archived reference %q is empty", value)
	}
	return ref
}
