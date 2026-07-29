package egress_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Targeted delivery is a claim about a real broker, a real graph and a real
// object store, and every substitute for one of them hides the failure it exists
// to catch.
//
// A fake graph would make "the pointers name the right turns" a test of the
// test's own bookkeeping — and the resolved-turn pointer is written by the REAL
// recorder through the REAL merge lane here, which is the only place "single
// valued" is a property rather than a fixture. A fake ObjectStore would make "the
// prose resolves" circular. And a single-connection test cannot state the
// requirement at all: what matters is what does NOT arrive at the second player.

func TestMain(m *testing.M) {
	if err := vocabulary.RegisterPredicates(); err != nil {
		fmt.Fprintf(os.Stderr, "register semmachina predicates: %v\n", err)
		os.Exit(1)
	}
	os.Exit(testinfra.RunTests(m))
}

var worldCounter atomic.Int64

// ---------------------------------------------------------------- world

// recordingSink is the transport stand-in, and it is the ONLY stand-in on the
// delivery path: everything upstream of it — the directory, the router, the
// composer, the graph, the store — is production.
//
// It records per connection, because "who did NOT receive this" is the assertion
// the requirement is made of.
type recordingSink struct {
	mu   sync.Mutex
	sent map[string][]*payload.TurnDelivery
}

func newRecordingSink() *recordingSink {
	return &recordingSink{sent: map[string][]*payload.TurnDelivery{}}
}

func (s *recordingSink) Deliver(
	_ context.Context,
	session gateway.Session,
	delivery *payload.TurnDelivery,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[session.Connection.ID] = append(s.sent[session.Connection.ID], delivery)
	return nil
}

func (s *recordingSink) to(connID string) []*payload.TurnDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*payload.TurnDelivery(nil), s.sent[connID]...)
}

func (s *recordingSink) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, deliveries := range s.sent {
		count += len(deliveries)
	}
	return count
}

// egressWorld is one campaign's whole answer path over a real broker.
type egressWorld struct {
	harness  *testinfra.Harness
	graph    *graphio.Store
	content  *content.Store
	recorder *turn.Recorder
	gateway  *gateway.Gateway
	results  *egress.Results
	router   *egress.Router
	notifier *egress.Notifier
	sink     *recordingSink

	namespace   string
	campaignID  string
	sceneID     string
	playerOneID string
	playerTwoID string
	characters  map[string]string

	verdictTool   *persona.VerdictExecutor
	narrationTool *persona.NarrationExecutor
	diceStage     *stage.Resolver
	effectStage   *stage.Effector
	completeStage *stage.Completer
}

const (
	credentialOne = "one-local-credential"
	credentialTwo = "two-local-credential"
)

func startEgress(t *testing.T) *egressWorld {
	t.Helper()
	harness := testinfra.Require(t)
	namespace := fmt.Sprintf("e%d", worldCounter.Add(1))

	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("graphio.NewStore: %v", err)
	}
	backend, err := content.NewObjectStore(t.Context(), harness.Client, content.WithBucket("EGRESS_"+namespace))
	if err != nil {
		t.Fatalf("content.NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	gate, err := campaign.NewGate(store, campaign.Identity{
		Org: testOrg, WorldNS: namespace, Template: testTemplate})
	if err != nil {
		t.Fatalf("campaign.NewGate: %v", err)
	}
	instantiation, err := gate.Claim(t.Context())
	if err != nil {
		t.Fatalf("claim the campaign: %v", err)
	}

	identity := turn.Identity{Org: testOrg, WorldNS: namespace, Template: testTemplate}
	recorder, err := turn.NewRecorder(store, artifacts, identity)
	if err != nil {
		t.Fatalf("turn.NewRecorder: %v", err)
	}

	world := &egressWorld{
		harness: harness, graph: store, content: artifacts, recorder: recorder,
		namespace:   namespace,
		campaignID:  instantiation.CampaignID,
		sceneID:     entityID(t, namespace, "scene", "gatehouse"),
		playerOneID: entityID(t, namespace, "player", "one"),
		playerTwoID: entityID(t, namespace, "player", "two"),
		characters:  map[string]string{},
		sink:        newRecordingSink(),
	}
	world.seedWorld(t)
	world.buildStages(t, instantiation)
	world.buildEgress(t, identity)
	return world
}

func entityID(t *testing.T, namespace, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(testOrg, namespace, testTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose %s id: %v", kind, err)
	}
	return id
}

// seedWorld gives each player their own character, so two turns can resolve
// without the two players' effects touching one entity.
func (w *egressWorld) seedWorld(t *testing.T) {
	t.Helper()
	w.createEntity(t, w.sceneID, map[string]any{
		vocabulary.WorldEntityKind.String(): string(vocabulary.EntityKindScene),
		vocabulary.WorldEntityName.String(): "The Gatehouse",
	})
	for playerID, name := range map[string]string{w.playerOneID: "rook", w.playerTwoID: "wren"} {
		characterID := entityID(t, w.namespace, "character", name)
		w.characters[playerID] = characterID
		w.createEntity(t, characterID, map[string]any{
			vocabulary.WorldEntityKind.String():        string(vocabulary.EntityKindCharacter),
			vocabulary.WorldEntityName.String():        strings.ToUpper(name[:1]) + name[1:],
			vocabulary.WorldLocationCurrent.String():   w.sceneID,
			vocabulary.CharacterStatusCurrent.String(): string(vocabulary.StatusHealthy),
		})
		w.createEntity(t, playerID, map[string]any{
			vocabulary.WorldEntityKind.String():        string(vocabulary.EntityKindPlayer),
			vocabulary.PlayerCharacterCurrent.String(): characterID,
		})
		w.harness.AwaitEntity(t, characterID)
		w.harness.AwaitEntity(t, playerID)
	}
}

func (w *egressWorld) createEntity(t *testing.T, id string, facts map[string]any) {
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

func (w *egressWorld) buildStages(t *testing.T, instantiation campaign.Instantiation) {
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

func (w *egressWorld) buildEgress(t *testing.T, identity turn.Identity) {
	t.Helper()
	roster, err := gateway.NewRoster(map[string]string{
		credentialOne: w.playerOneID,
		credentialTwo: w.playerTwoID,
	})
	if err != nil {
		t.Fatalf("gateway.NewRoster: %v", err)
	}
	gw, err := gateway.New(roster, w.graph, w.harness.Client, gateway.Config{
		CampaignID: w.campaignID, SceneID: w.sceneID,
	}, gateway.WithSubject("semmachina.action."+w.namespace),
		gateway.WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("gateway.New: %v", err)
	}
	w.gateway = gw

	if w.results, err = egress.NewResults(w.graph, w.content, identity, w.campaignID); err != nil {
		t.Fatalf("egress.NewResults: %v", err)
	}
	// The REAL gateway is the directory. A test double here would be a test of
	// the double's own indexing, and the index is the thing under test.
	if w.router, err = egress.NewRouter(w.gateway, w.sink, egress.WithRouterLogger(discardLogger())); err != nil {
		t.Fatalf("egress.NewRouter: %v", err)
	}
	if w.notifier, err = egress.NewNotifier(
		w.results, w.router, w.harness.Client, egress.WithNotifierLogger(discardLogger()),
	); err != nil {
		t.Fatalf("egress.NewNotifier: %v", err)
	}
}

func (w *egressWorld) connect(t *testing.T, credential, connID string) *gateway.Session {
	t.Helper()
	session, err := w.gateway.Authenticate(t.Context(), credential, gateway.Connection{
		ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID,
	})
	if err != nil {
		t.Fatalf("authenticate %s: %v", connID, err)
	}
	return session
}

// ---------------------------------------------------------------- turns

func (w *egressWorld) rollingVerdict(playerID string) map[string]any {
	character := w.characters[playerID]
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
			string(vocabulary.BandMiss):    statusIntent(character, vocabulary.StatusWounded),
			string(vocabulary.BandPartial): statusIntent(character, vocabulary.StatusExhausted),
			string(vocabulary.BandFull):    statusIntent(character, vocabulary.StatusHealthy),
		},
		"rationale": "The gate is heavy but the fiction allows it.",
	}
}

// refusedVerdict names a wrong-KIND target: moving a status onto a SCENE is well
// formed and refused by the applier, which produces a real failed turn — one that
// died before the narrator ran, and therefore has no prose.
func (w *egressWorld) refusedVerdict() map[string]any {
	return map[string]any{
		"scalars": map[string]any{
			"plausibility":  string(vocabulary.PlausibilityCertain),
			"risk":          string(vocabulary.RiskNone),
			"consequence":   string(vocabulary.ConsequenceNone),
			"requires_roll": false,
		},
		"modifiers": []any{},
		"bands": map[string]any{
			string(vocabulary.BandAuto): statusIntent(w.sceneID, vocabulary.StatusHidden),
		},
		"rationale": "Nothing is at stake; the fiction already decided.",
	}
}

func statusIntent(target string, status vocabulary.Status) []any {
	return []any{map[string]any{
		"type": string(vocabulary.EffectSetStatus), "target": target, "status": string(status),
	}}
}

// resolvedTurn drives one turn to a terminal phase through the production stages
// and the production recorder — which is what writes the resolved-turn pointer
// this package reads.
func (w *egressWorld) resolvedTurn(
	t *testing.T,
	actionID, playerID string,
	verdict map[string]any,
) (turnID, entityID string) {
	t.Helper()
	action := &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   playerID,
		CampaignID: w.campaignID,
		SceneID:    w.sceneID,
		Text:       "I shoulder the gate open.",
		ArrivedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-at-submission"},
	}
	acceptance, err := w.recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("accept action %s: %v", actionID, err)
	}
	turnID, entityID = acceptance.TurnID, acceptance.TurnEntityID

	subject, err := rulepack.SubjectForPhase(vocabulary.PhaseApplying)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	trigger := stage.Trigger{TurnEntityID: entityID, TurnID: turnID, Subject: subject}

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
	if w.phaseOf(t, entityID) == vocabulary.PhaseFailed {
		return turnID, entityID
	}

	w.advance(t, turnID, entityID, vocabulary.PhaseNarrating)
	w.execute(t, w.narrationTool, agentic.ToolCall{
		ID: "call-" + actionID, Name: persona.NarrationToolName,
		Arguments: map[string]any{"prose": "The hinges scream and the gate gives, " + actionID + "."},
		Metadata:  w.metadata(turnID, entityID, actionID, w.bandOf(t, entityID, rolls)),
	})
	if err := w.completeStage.Run(t.Context(), trigger); err != nil {
		t.Fatalf("completion stage: %v", err)
	}
	return turnID, entityID
}

func (w *egressWorld) metadata(
	turnID, entityID, actionID string,
	band vocabulary.OutcomeBand,
) map[string]any {
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

func (w *egressWorld) execute(t *testing.T, tool interface {
	Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error)
}, call agentic.ToolCall) {
	t.Helper()
	result, err := tool.Execute(t.Context(), call)
	if err != nil {
		t.Fatalf("the %s tool refused a scripted exit: %v", call.Name, err)
	}
	if result.Error != "" {
		t.Fatalf("the %s tool returned an error result: %s", call.Name, result.Error)
	}
}

func (w *egressWorld) advance(t *testing.T, turnID, entityID string, phase vocabulary.TurnPhase) {
	t.Helper()
	if _, err := w.recorder.Advance(t.Context(), turnID, entityID, phase); err != nil {
		t.Fatalf("advance turn %s to %s: %v", entityID, phase, err)
	}
}

func (w *egressWorld) phaseOf(t *testing.T, entityID string) vocabulary.TurnPhase {
	t.Helper()
	phase, err := w.recorder.Current(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read the phase of %s: %v", entityID, err)
	}
	return phase
}

func (w *egressWorld) bandOf(t *testing.T, entityID string, rolled bool) vocabulary.OutcomeBand {
	t.Helper()
	if !rolled {
		return vocabulary.BandAuto
	}
	state, err := w.graph.GetEntity(t.Context(), entityID)
	if err != nil {
		t.Fatalf("read turn %s: %v", entityID, err)
	}
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnRollBand.String() {
			return vocabulary.OutcomeBand(fmt.Sprint(triple.Object))
		}
	}
	t.Fatalf("turn %s records no roll band", entityID)
	return ""
}

// publishResolved puts exactly the bytes the rule engine's publish action emits
// onto the stage stream.
func (w *egressWorld) publishResolved(t *testing.T, entityID string) {
	t.Helper()
	notification, err := json.Marshal(map[string]any{
		"entity_id": entityID,
		"subject":   rulepack.SubjectResolved,
		"source":    rulepack.PackID,
	})
	if err != nil {
		t.Fatalf("encode the notification: %v", err)
	}
	if err := w.harness.Client.PublishToStream(t.Context(), rulepack.SubjectResolved, notification); err != nil {
		t.Fatalf("publish the resolved notification: %v", err)
	}
}

// publishResolvedForSequence publishes and returns the stream sequence, which is
// the handle a test needs to observe what the consumer did with THAT message
// rather than with the traffic around it.
func (w *egressWorld) publishResolvedForSequence(t *testing.T, entityID string) uint64 {
	t.Helper()
	notification, err := json.Marshal(map[string]any{
		"entity_id": entityID,
		"subject":   rulepack.SubjectResolved,
		"source":    rulepack.PackID,
	})
	if err != nil {
		t.Fatalf("encode the notification: %v", err)
	}
	js, err := w.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ack, err := js.Publish(t.Context(), rulepack.SubjectResolved, notification)
	if err != nil {
		t.Fatalf("publish the resolved notification: %v", err)
	}
	return ack.Sequence
}

// requireRetired waits for the egress consumer to finish with one specific stream
// sequence — acknowledged or terminated, which JetStream records identically and
// which is exactly the distinction this test does not need to draw.
//
// The deadline is deliberately shorter than the framework's 30-second
// nak-with-delay. That is the whole design of the assertion: a nak'd message is
// not merely slower to retire, it never retires at all.
func (w *egressWorld) requireRetired(t *testing.T, sequence uint64) {
	t.Helper()
	js, err := w.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	consumer, err := js.Consumer(t.Context(), rulepack.StageStream, egress.ConsumerName)
	if err != nil {
		t.Fatalf("read the egress consumer: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	var last *jetstream.ConsumerInfo
	for time.Now().Before(deadline) {
		info, infoErr := consumer.Info(t.Context())
		if infoErr != nil {
			t.Fatalf("read the egress consumer info: %v", infoErr)
		}
		last = info
		if info.AckFloor.Stream >= sequence {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("the egress consumer never retired stream sequence %d: its acknowledgement floor is still %d, "+
		"with %d delivery(ies) outstanding and %d redelivered. A notification that can never become a "+
		"delivery was nak'd rather than terminated, so it redelivers forever and every later player waits "+
		"behind it. READ THE FLOOR FIRST: the floor is CONTIGUOUS, so a floor stuck BELOW %d means some "+
		"earlier sequence never retired — an unfinished message another test left on this shared stream "+
		"reads exactly like a termination bug. A floor that reached %d and stopped is the failure this test "+
		"is about",
		sequence, last.AckFloor.Stream, last.NumAckPending, last.NumRedelivered, sequence-1, sequence-1)
}

func (w *egressWorld) startNotifier(t *testing.T) {
	t.Helper()
	if _, err := w.harness.Client.EnsureStream(t.Context(), stage.StreamConfig()); err != nil {
		t.Fatalf("ensure the %s stream: %v", rulepack.StageStream, err)
	}
	if err := w.notifier.Start(context.Background()); err != nil {
		t.Fatalf("start the egress notifier: %v", err)
	}
	t.Cleanup(w.harness.Client.StopAllConsumers)
}

// dropEgressConsumer deletes the durable so the next Start is a FIRST CREATION.
//
// That is the only moment DeliverPolicy applies at all — thereafter a durable
// resumes from its acknowledgment floor — so a test about the policy has to
// manufacture it. It is also not a contrived state: it is this path shipping
// mid-campaign, and it is an operator deleting a wedged consumer.
func (w *egressWorld) dropEgressConsumer(t *testing.T) {
	t.Helper()
	if _, err := w.harness.Client.EnsureStream(t.Context(), stage.StreamConfig()); err != nil {
		t.Fatalf("ensure the %s stream: %v", rulepack.StageStream, err)
	}
	js, err := w.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	err = js.DeleteConsumer(t.Context(), rulepack.StageStream, egress.ConsumerName)
	if err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
		t.Fatalf("delete the egress consumer: %v", err)
	}
}

func (w *egressWorld) awaitDelivery(t *testing.T, connID, turnID string) *payload.TurnDelivery {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, delivery := range w.sink.to(connID) {
			if delivery.Result.TurnID == turnID {
				return delivery
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("connection %s never received turn %s", connID, turnID)
	return nil
}

// ---------------------------------------------------------------- tests

// The requirement, over real infrastructure and with the assertion that only a
// second connected player can make.
func TestIntegration_OnePlayersResultDoesNotReachAnother(t *testing.T) {
	world := startEgress(t)
	world.startNotifier(t)

	world.connect(t, credentialOne, "conn-one")
	world.connect(t, credentialTwo, "conn-two")

	turnID, entityID := world.resolvedTurn(t, "act-targeted", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, entityID)

	delivery := world.awaitDelivery(t, "conn-one", turnID)
	if delivery.Result.PlayerID != world.playerOneID {
		t.Fatalf("the delivery names player %q, want %q", delivery.Result.PlayerID, world.playerOneID)
	}
	if delivery.Narration == nil || delivery.Narration.Prose == "" {
		t.Fatal("the delivered document carries no prose; the client cannot resolve the reference itself")
	}

	// The assertion that matters. Given a moment for a broadcast to arrive if one
	// were going to: the positive delivery above already proves the consumer ran.
	time.Sleep(500 * time.Millisecond)
	if got := world.sink.to("conn-two"); len(got) != 0 {
		t.Fatalf("the second connected player received %d documents; that is one player reading another's "+
			"fiction, which is what makes broadcast egress a disclosure defect", len(got))
	}
	if world.sink.total() != 1 {
		t.Fatalf("%d documents were delivered in total for one turn", world.sink.total())
	}
}

// Two turns, two players, delivered through one consumer. The failure this
// catches that the test above cannot: a router that used the LAST resolved
// player, or one whose directory lookup was keyed on something stable across
// turns, delivers both documents to one player and passes a one-turn test.
func TestIntegration_TwoPlayersTurnsDoNotCross(t *testing.T) {
	world := startEgress(t)
	world.startNotifier(t)
	world.connect(t, credentialOne, "conn-one")
	world.connect(t, credentialTwo, "conn-two")

	oneTurn, oneEntity := world.resolvedTurn(
		t, "act-cross-one", world.playerOneID, world.rollingVerdict(world.playerOneID))
	twoTurn, twoEntity := world.resolvedTurn(
		t, "act-cross-two", world.playerTwoID, world.rollingVerdict(world.playerTwoID))
	world.publishResolved(t, oneEntity)
	world.publishResolved(t, twoEntity)

	first := world.awaitDelivery(t, "conn-one", oneTurn)
	second := world.awaitDelivery(t, "conn-two", twoTurn)
	if first.Result.PlayerID != world.playerOneID || second.Result.PlayerID != world.playerTwoID {
		t.Fatalf("deliveries carry players %q and %q", first.Result.PlayerID, second.Result.PlayerID)
	}
	if first.Narration.Prose == second.Narration.Prose {
		t.Fatal("both players received the same prose; the two turns were narrated differently, so this is " +
			"one turn's fiction delivered twice")
	}
	for connID, wrongTurn := range map[string]string{"conn-one": twoTurn, "conn-two": oneTurn} {
		for _, delivery := range world.sink.to(connID) {
			if delivery.Result.TurnID == wrongTurn {
				t.Fatalf("connection %s received turn %s, which belongs to the other player", connID, wrongTurn)
			}
		}
	}
}

// Delivery resolves the connection at DELIVERY time. The socket the action was
// submitted on is gone by the time the turn resolves, which is the ordinary case
// at any cadence slower than a chat window.
func TestIntegration_DeliverySurvivesAReconnect(t *testing.T) {
	world := startEgress(t)
	world.startNotifier(t)
	world.connect(t, credentialOne, "conn-original")

	turnID, entityID := world.resolvedTurn(
		t, "act-reconnect", world.playerOneID, world.rollingVerdict(world.playerOneID))

	// The socket drops and the player comes back on a new one BEFORE the result
	// is delivered.
	world.gateway.Disconnect("conn-original")
	world.connect(t, credentialOne, "conn-new")
	world.publishResolved(t, entityID)

	delivery := world.awaitDelivery(t, "conn-new", turnID)
	if delivery.Result.PlayerID != world.playerOneID {
		t.Fatalf("the delivery names %q", delivery.Result.PlayerID)
	}
	if got := world.sink.to("conn-original"); len(got) != 0 {
		t.Fatalf("the dropped connection received %d documents; delivery followed an address captured at "+
			"submission rather than the player's identity", len(got))
	}
}

// A player who is away when their turn resolves is answered by RETRIEVAL, not by
// adapter memory. This is the disconnect-and-come-back case, and the retrieval
// runs against the graph and the object store with nothing held in process.
func TestIntegration_ResultSurvivesADisconnectAndIsRetrievable(t *testing.T) {
	world := startEgress(t)
	world.startNotifier(t)

	// Nobody is connected at all.
	turnID, entityID := world.resolvedTurn(
		t, "act-away", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, entityID)

	// The push acknowledged with no recipients; the durable answer is still here.
	byTurn, err := world.results.ByTurn(t.Context(), turnID)
	if err != nil {
		t.Fatalf("ByTurn after a delivery nobody received: %v", err)
	}
	if byTurn.Result.Phase != vocabulary.PhaseComplete {
		t.Fatalf("phase = %q", byTurn.Result.Phase)
	}
	if byTurn.Narration == nil {
		t.Fatal("the retrieved result carries no prose")
	}

	byAction, err := world.results.ByAction(t.Context(), "act-away")
	if err != nil {
		t.Fatalf("ByAction: %v", err)
	}
	if byAction.Result.TurnID != turnID {
		t.Fatalf("ByAction answered with %q, want %q", byAction.Result.TurnID, turnID)
	}

	latest, err := world.results.Latest(t.Context(), world.playerOneID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Result.TurnID != turnID {
		t.Fatalf("Latest answered with %q, want %q", latest.Result.TurnID, turnID)
	}
	if world.sink.total() != 0 {
		t.Fatalf("%d documents were delivered to nobody", world.sink.total())
	}
}

// The resolved-turn pointer, written by the real recorder through the real merge
// lane, and read back while the NEXT turn is live — which is the whole seam it
// exists for and the state player.turn.current cannot answer from.
func TestIntegration_LatestAnswersWhileTheNextTurnIsStillRunning(t *testing.T) {
	world := startEgress(t)

	finishedTurn, finishedEntity := world.resolvedTurn(
		t, "act-first", world.playerOneID, world.rollingVerdict(world.playerOneID))

	// The pointer is single-valued through the merge lane. Asserting it here is
	// the one place that is a property of real graph-ingest rather than of a fake.
	player, err := world.graph.GetEntity(t.Context(), world.playerOneID)
	if err != nil {
		t.Fatalf("read the player: %v", err)
	}
	resolved, unreadable := turn.ResolvedTurns(player)
	if len(resolved) != 1 || resolved[0] != finishedEntity || unreadable != 0 {
		t.Fatalf("the player holds resolved pointers %v (%d unreadable), want exactly %s",
			resolved, unreadable, finishedEntity)
	}

	// A second turn starts and does not finish.
	action := &payload.PlayerAction{
		ActionID: "act-second", PlayerID: world.playerOneID, CampaignID: world.campaignID,
		SceneID: world.sceneID, Text: "I look back down the road.",
		ArrivedAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		Channel:   payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-one"},
	}
	acceptance, err := world.recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("accept the second action: %v", err)
	}
	world.advance(t, acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)

	latest, err := world.results.Latest(t.Context(), world.playerOneID)
	if err != nil {
		t.Fatalf("Latest while a turn runs: %v", err)
	}
	if latest.Result.TurnID != finishedTurn {
		t.Fatalf("Latest answered with %q, want the last turn that ENDED (%q); player.turn.current names the "+
			"LIVE turn, so nothing else in the graph names this one", latest.Result.TurnID, finishedTurn)
	}
}

// A failed turn is what the player most needs an answer about, and the ledger
// already archives one — so retrieval must speak for it too. This failure is
// produced by the real applier refusing a real intent, not simulated.
func TestIntegration_AFailedTurnIsDeliveredAndRetrievable(t *testing.T) {
	world := startEgress(t)
	world.startNotifier(t)
	world.connect(t, credentialOne, "conn-one")

	succeededTurn, _ := world.resolvedTurn(
		t, "act-good", world.playerOneID, world.rollingVerdict(world.playerOneID))
	failedTurn, failedEntity := world.resolvedTurn(t, "act-refused", world.playerOneID, world.refusedVerdict())
	if phase := world.phaseOf(t, failedEntity); phase != vocabulary.PhaseFailed {
		t.Fatalf("the refused turn is in phase %q, want failed; the fixture no longer produces a real failure",
			phase)
	}

	world.publishResolved(t, failedEntity)
	delivery := world.awaitDelivery(t, "conn-one", failedTurn)
	if delivery.Result.Phase != vocabulary.PhaseFailed {
		t.Fatalf("the delivered phase is %q", delivery.Result.Phase)
	}
	if delivery.Result.FailureReason == "" {
		t.Fatal("a failed turn was delivered with no reason; bounded execution promises a turn never ends " +
			"in silence, and this is the surface that promise is kept at")
	}
	if delivery.Narration != nil {
		t.Fatalf("a turn that died before the narrator delivered prose: %q", delivery.Narration.Prose)
	}

	latest, err := world.results.Latest(t.Context(), world.playerOneID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Result.TurnID != failedTurn {
		t.Fatalf("Latest answered with %q, want the failure (%q) rather than the earlier success (%q)",
			latest.Result.TurnID, failedTurn, succeededTurn)
	}
}

// The delivered document is the published one. Composed from the graph, encoded,
// and compared against the result encoded on its own — over real infrastructure,
// where the artifacts came out of a real object store.
func TestIntegration_TheDeliveredDocumentIsThePublishedResult(t *testing.T) {
	world := startEgress(t)
	turnID, _ := world.resolvedTurn(
		t, "act-shape", world.playerOneID, world.rollingVerdict(world.playerOneID))

	delivery, err := world.results.ByTurn(t.Context(), turnID)
	if err != nil {
		t.Fatalf("ByTurn: %v", err)
	}
	published, err := json.Marshal(delivery.Result)
	if err != nil {
		t.Fatalf("marshal the result: %v", err)
	}
	encoded, err := json.Marshal(delivery)
	if err != nil {
		t.Fatalf("marshal the delivery: %v", err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode the envelope: %v", err)
	}
	if string(published) != string(envelope.Result) {
		t.Fatalf("the delivered result differs from the published one:\n delivered %s\n published %s",
			envelope.Result, published)
	}
}

// A FIRST-CREATION consumer must not replay the campaign's resolved history as
// live pushes.
//
// This is the shape that passes every test on a fresh broker and misbehaves only
// against history, so it is tested against history: a turn resolves and is
// announced BEFORE the consumer exists at all, and the player is connected when
// it starts. `TURN_STAGES` never evicts — limits retention, no MaxAge, no
// MaxBytes — so under "all" that notification is still there and is redelivered
// as a live result for a turn the player finished long ago, along with every
// other one the campaign ever published.
//
// The negative is made sound by a positive control rather than by a sleep: a NEW
// turn published after the consumer starts must arrive, which fixes a moment by
// which the historical one would have arrived too if it were going to.
func TestIntegration_AFirstCreationConsumerDoesNotReplayTheCampaignsHistory(t *testing.T) {
	world := startEgress(t)
	world.dropEgressConsumer(t)

	// History: resolved and announced with no consumer in existence.
	historicTurn, historicEntity := world.resolvedTurn(
		t, "act-history", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, historicEntity)

	world.connect(t, credentialOne, "conn-one")
	world.startNotifier(t)

	// The positive control, which also proves the consumer is running and caught
	// up before the negative below is read.
	liveTurn, liveEntity := world.resolvedTurn(
		t, "act-live", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, liveEntity)
	world.awaitDelivery(t, "conn-one", liveTurn)

	for _, delivery := range world.sink.to("conn-one") {
		if delivery.Result.TurnID == historicTurn {
			t.Fatalf("a first-creation consumer replayed turn %s, which resolved before it existed; the "+
				"stage stream never evicts, so this is the player's whole back-catalogue arriving as live "+
				"results — and the downtime case it would buy is the acknowledgment floor's job, not the "+
				"deliver policy's", historicTurn)
		}
	}
}

// The other half of the DeliverPolicy decision, and the reason "new" costs
// nothing: a turn that resolves while this process is DOWN is still delivered
// when it comes back.
//
// That is the acknowledgment floor's doing and not the policy's, which is the
// whole argument — the floor applies on every bind, while DeliverPolicy applies
// once and never again. Asserting it here means the claim "the downtime case is
// covered elsewhere" is measured rather than reasoned, so a future change to the
// policy cannot quietly take the downtime case with it.
func TestIntegration_ATurnResolvedWhileTheProcessWasDownIsStillDelivered(t *testing.T) {
	world := startEgress(t)
	world.dropEgressConsumer(t)
	world.connect(t, credentialOne, "conn-one")
	world.startNotifier(t)

	// Prove the consumer exists and has a floor before anything is missed.
	warmTurn, warmEntity := world.resolvedTurn(
		t, "act-warm", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, warmEntity)
	world.awaitDelivery(t, "conn-one", warmTurn)

	// The process goes away. The durable consumer stays on the server.
	world.harness.Client.StopAllConsumers()

	downTurn, downEntity := world.resolvedTurn(
		t, "act-during-downtime", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, downEntity)

	// And comes back, binding the SAME durable — no first creation, so
	// DeliverPolicy is not consulted at all.
	if err := world.notifier.Start(context.Background()); err != nil {
		t.Fatalf("restart the egress notifier: %v", err)
	}
	world.awaitDelivery(t, "conn-one", downTurn)
}

// A notification naming something that is not a turn can never deliver anything,
// and it is TERMINATED rather than nak'd.
//
// The obvious test — publish the poison, then prove a later turn still gets
// through — is a green lie, and was written and run before this one replaced it:
// a nak'd message goes back with a delay and the consumer pulls the next one
// regardless, so that test passes against a handler that naks forever. Measured,
// not assumed: the terminate was mutated to a plain error and it still passed.
//
// So this observes the POISON MESSAGE itself. Publish with an ack to learn its
// stream sequence, then poll the consumer's acknowledgement floor past it, inside
// a window deliberately shorter than the framework's 30-second nak-with-delay — a
// nak'd message is not slower to retire, it never retires, and a window under one
// redelivery cycle says so without waiting for a second refusal to prove it.
func TestIntegration_APoisonNotificationIsTerminatedRatherThanRetriedForever(t *testing.T) {
	world := startEgress(t)
	world.startNotifier(t)
	world.connect(t, credentialOne, "conn-one")

	// A scene entity: canonical, real, and not a turn. Nothing about it can ever
	// become a result, so every redelivery reproduces the same refusal.
	sequence := world.publishResolvedForSequence(t, world.sceneID)
	world.requireRetired(t, sequence)

	// And the consumer still works afterwards, which is the other half:
	// terminating the poison must not have cost the path its consumer.
	turnID, entityID := world.resolvedTurn(
		t, "act-after-poison", world.playerOneID, world.rollingVerdict(world.playerOneID))
	world.publishResolved(t, entityID)
	world.awaitDelivery(t, "conn-one", turnID)
}
