package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Everything the admission gate rests on is a claim about a REAL graph and a
// REAL durable consumer. The pointer it reads is written on the merge lane, and
// the mutation API offers a second lane that accepts the same triples and
// APPENDS — through which a player's second turn leaves them holding two
// pointers, with a success response and no error anywhere. The turn it reads is
// created by an intake that only exists once a broker delivers something. A fake
// store has one lane and a fake consumer acknowledges whatever it is handed, so
// neither can see either failure.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

var integrationCounter atomic.Int64

const (
	liveOrg      = "c360"
	liveTemplate = "starter"
)

// liveGateway is one world namespace with the production gateway over the real
// graph, the real action stream, and the real turn intake behind it.
type liveGateway struct {
	harness  *testinfra.Harness
	gateway  *gateway.Gateway
	store    *graphio.Store
	recorder *turn.Recorder

	namespace  string
	playerID   string
	campaignID string
	sceneID    string
	stream     string
	subject    string
	consumer   string
	now        func() time.Time
}

func compose(t *testing.T, namespace, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(liveOrg, namespace, liveTemplate, kind, instance)
	if err != nil {
		t.Fatalf("ComposeEntityID: %v", err)
	}
	return id
}

// startGateway builds the whole ingress path: a real player entity in the graph,
// the real action stream, a real intake bound to it, and the gateway in front.
func startGateway(t *testing.T) *liveGateway {
	t.Helper()
	harness := testinfra.Require(t)

	namespace := fmt.Sprintf("gww%d", integrationCounter.Add(1))
	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	live := &liveGateway{
		harness:    harness,
		store:      store,
		namespace:  namespace,
		playerID:   compose(t, namespace, "player", "pat"),
		campaignID: compose(t, namespace, "campaign", "main"),
		sceneID:    compose(t, namespace, "scene", "gatehouse"),
		// Deliberately OUTSIDE the canonical `player.action.>` space: NATS
		// refuses a stream whose subjects overlap an existing one, so a per-test
		// stream under the real prefix would make the canonical stream
		// uncreatable. The canonical coordinates are pinned by their own test.
		stream:   "GATEWAY_ACTIONS_" + namespace,
		subject:  "itest.gateway.action." + namespace,
		consumer: "gateway-intake-" + namespace,
	}
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	live.now = func() time.Time { return base }

	live.bornPlayer(t)

	if _, err := harness.Client.EnsureStream(t.Context(), jetstream.StreamConfig{
		Name:      live.stream,
		Subjects:  []string{live.subject},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    turn.ActionMaxAge,
		MaxBytes:  turn.ActionMaxBytes,
		Discard:   jetstream.DiscardNew,
	}); err != nil {
		t.Fatalf("EnsureStream %s: %v", live.stream, err)
	}

	roster, err := gateway.NewRoster(map[string]string{liveCredential: live.playerID})
	if err != nil {
		t.Fatalf("NewRoster: %v", err)
	}
	gw, err := gateway.New(roster, store, harness.Client,
		gateway.Config{CampaignID: live.campaignID, SceneID: live.sceneID},
		gateway.WithSubject(live.subject),
		gateway.WithClock(func() time.Time { return live.now() }),
		gateway.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatalf("gateway.New: %v", err)
	}
	live.gateway = gw
	return live
}

const liveCredential = "local-player-credential"

// bornPlayer materializes the player as a REAL entity through graph-ingest, so
// the gateway's "player_id is a graph entity" read has something true to find.
func (l *liveGateway) bornPlayer(t *testing.T) {
	t.Helper()
	entity := &graph.EntityState{
		ID: l.playerID,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version:   1,
		UpdatedAt: time.Now().UTC(),
		Triples: []message.Triple{{
			Subject:    l.playerID,
			Predicate:  vocabulary.WorldEntityKind.String(),
			Object:     string(vocabulary.EntityKindPlayer),
			Source:     "integration-world-import",
			Timestamp:  time.Now().UTC(),
			Confidence: 1.0,
		}},
	}
	if _, err := l.store.CreateEntity(t.Context(), entity); err != nil {
		t.Fatalf("create the player entity: %v", err)
	}
	l.harness.AwaitEntity(t, l.playerID)
}

// startIntake binds the REAL turn intake to the stream the gateway publishes on,
// with a real recorder over the real graph and a real content store.
func (l *liveGateway) startIntake(t *testing.T) {
	t.Helper()

	backend, err := content.NewObjectStore(t.Context(), l.harness.Client,
		content.WithBucket("GW_CONTENT_"+l.namespace))
	if err != nil {
		t.Fatalf("NewObjectStore: %v", err)
	}
	t.Cleanup(func() { backend.Close() }) //nolint:errcheck // best effort in teardown
	contentStore, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("content.NewStore: %v", err)
	}

	identity := turn.Identity{Org: liveOrg, WorldNS: l.namespace, Template: liveTemplate}
	recorder, err := turn.NewRecorder(l.store, contentStore, identity)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	l.recorder = recorder

	intake, err := turn.NewIntake(
		l.harness.Client, recorder, message.NewDecoder(l.harness.Registry),
		turn.WithStream(l.stream, l.subject),
		turn.WithConsumerName(l.consumer))
	if err != nil {
		t.Fatalf("NewIntake: %v", err)
	}
	if err := intake.Start(t.Context()); err != nil {
		t.Fatalf("Start intake: %v", err)
	}
	t.Cleanup(func() { l.harness.Client.StopConsumer(l.stream, l.consumer) })
}

func (l *liveGateway) authenticate(t *testing.T, connID string) *gateway.Session {
	t.Helper()
	session, err := l.gateway.Authenticate(t.Context(), liveCredential,
		gateway.Connection{ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return session
}

func (l *liveGateway) submit(t *testing.T, connID, key, text string) *payload.SubmitResponse {
	t.Helper()
	raw := []byte(fmt.Sprintf(
		`{"protocol":%q,"text":%q,"idempotency_key":%q}`, payload.PlayerProtocolV1, text, key))
	response, err := l.gateway.Submit(t.Context(), connID, raw)
	if err != nil {
		t.Fatalf("Submit reported an operator failure: %v", err)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("the gateway produced a response that fails its own contract: %v", err)
	}
	return response
}

func (l *liveGateway) turnEntityID(t *testing.T, turnID string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(liveOrg, l.namespace, liveTemplate, turn.TypeSegment, turnID)
	if err != nil {
		t.Fatalf("ComposeEntityID: %v", err)
	}
	return id
}

// awaitPointer polls until the player's pointer names the expected turn.
func (l *liveGateway) awaitPointer(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last []any
	for time.Now().Before(deadline) {
		state, err := l.harness.QueryEntity(t.Context(), l.playerID)
		if err == nil {
			last = testinfra.ObjectsFor(state, vocabulary.PlayerTurnCurrent.String())
			if len(last) == 1 && last[0] == want {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the player never came to point at turn %s; the graph holds %v", want, last)
}

// ------------------------------------------------------------------ the loop

// The whole ingress path, on real infrastructure: a submission becomes an action
// on a real stream, a real durable consumer turns it into a real turn entity,
// and the pointer that turn's acceptance writes is what closes the gate behind
// the player.
func TestIntegration_ASubmissionBecomesATurnAndClosesTheGateBehindIt(t *testing.T) {
	live := startGateway(t)
	live.startIntake(t)
	live.authenticate(t, "conn-1")

	accepted := live.submit(t, "conn-1", "key-1", "I lever the gate open with the crowbar.")
	if accepted.Status != payload.StatusAccepted {
		t.Fatalf("the first submission was refused: %+v", accepted.Refusal)
	}

	turnEntityID := live.turnEntityID(t, accepted.TurnID)
	state := live.harness.AwaitEntity(t, turnEntityID)
	if got := testinfra.ObjectsFor(state, vocabulary.TurnPhaseCurrent.String()); len(got) != 1 ||
		got[0] != string(vocabulary.PhaseAccepted) {
		t.Fatalf("the turn holds phases %v, want exactly [accepted]", got)
	}
	if got := testinfra.FirstObject(state, vocabulary.TurnActionPlayer.String()); got != live.playerID {
		t.Fatalf("the turn records player %v, want the session's %q", got, live.playerID)
	}

	live.awaitPointer(t, turnEntityID)

	// The gate is now closed: a second DISTINCT action is refused, and the
	// refusal names the turn the player is waiting on.
	refused := live.submit(t, "conn-1", "key-2", "I run for the treeline.")
	if refused.Status != payload.StatusRefused {
		t.Fatalf("a second distinct action was accepted while a turn was live: %+v", refused)
	}
	if refused.Refusal.Code != payload.RefusalTurnInProgress {
		t.Fatalf("refusal code = %q (%s), want %q",
			refused.Refusal.Code, refused.Refusal.Message, payload.RefusalTurnInProgress)
	}
	if refused.Refusal.ActiveTurnID != accepted.TurnID {
		t.Fatalf("the refusal names turn %q, want the live %q",
			refused.Refusal.ActiveTurnID, accepted.TurnID)
	}

	// And it opens again when the turn ends — read from the TURN, which owns
	// terminality, with nothing anywhere needing to be cleared.
	transition, err := live.recorder.Advance(t.Context(), accepted.TurnID, turnEntityID,
		vocabulary.PhaseAdjudicating)
	if err != nil || transition.Outcome != turn.OutcomeAdvanced {
		t.Fatalf("Advance: %v (%q)", err, transition.Outcome)
	}
	live.advanceToComplete(t, accepted.TurnID, turnEntityID)

	second := live.submit(t, "conn-1", "key-2", "I run for the treeline.")
	if second.Status != payload.StatusAccepted {
		t.Fatalf("the next action was refused after the turn ended: %+v", second.Refusal)
	}

	secondEntityID := live.turnEntityID(t, second.TurnID)
	live.harness.AwaitEntity(t, secondEntityID)
	live.awaitPointer(t, secondEntityID)
}

func (l *liveGateway) advanceToComplete(t *testing.T, turnID, turnEntityID string) {
	t.Helper()
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseResolving, vocabulary.PhaseApplying,
		vocabulary.PhaseNarrating, vocabulary.PhaseComplete,
	} {
		transition, err := l.recorder.Advance(t.Context(), turnID, turnEntityID, phase)
		if err != nil {
			t.Fatalf("Advance to %q: %v", phase, err)
		}
		if transition.Outcome != turn.OutcomeAdvanced {
			t.Fatalf("Advance to %q reported %q", phase, transition.Outcome)
		}
	}
}

// The pointer is single-valued on the REAL graph. Two turns for one player leave
// exactly one pointer, and the negative control shows the append lane leaving
// two — which is the anomaly that would turn the gate into a coin flip.
func TestIntegration_ASecondTurnLeavesThePlayerHoldingOnePointer(t *testing.T) {
	live := startGateway(t)
	live.startIntake(t)
	live.authenticate(t, "conn-1")

	first := live.submit(t, "conn-1", "key-1", "I open the gate.")
	firstEntity := live.turnEntityID(t, first.TurnID)
	live.harness.AwaitEntity(t, firstEntity)
	live.awaitPointer(t, firstEntity)
	live.advanceToCompleteFrom(t, first.TurnID, firstEntity)

	second := live.submit(t, "conn-1", "key-2", "I step through.")
	secondEntity := live.turnEntityID(t, second.TurnID)
	live.harness.AwaitEntity(t, secondEntity)
	live.awaitPointer(t, secondEntity)

	state := live.harness.AwaitEntity(t, live.playerID)
	pointers := testinfra.ObjectsFor(state, vocabulary.PlayerTurnCurrent.String())
	if len(pointers) != 1 || pointers[0] != secondEntity {
		t.Fatalf("after two turns the player holds %v, want exactly [%s]", pointers, secondEntity)
	}

	// Negative control on the SAME graph: the appending lane leaves two, so the
	// assertion above is about the lane and not about the store being empty.
	pointer := &payload.PlayerTurn{
		PlayerID: live.playerID, TurnID: first.TurnID, TurnEntityID: firstEntity,
	}
	triples, err := pointer.Triples(live.playerID, turn.Source, time.Now().UTC())
	if err != nil {
		t.Fatalf("compose the pointer: %v", err)
	}
	request, err := json.Marshal(graph.AddTriplesBatchRequest{Triples: triples})
	if err != nil {
		t.Fatalf("encode add_batch: %v", err)
	}
	reply, err := live.harness.Client.RequestClassified(
		t.Context(), graphingest.SubjectTripleAddBatch, request, 5*time.Second)
	if err != nil {
		t.Fatalf("add_batch: %v", err)
	}
	var response graph.AddTriplesBatchResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		t.Fatalf("decode add_batch response: %v", err)
	}
	// F7: a partial commit is a SUCCESS body with a nil error, so the
	// failed-subject map is the only signal that a write did not land.
	if len(response.FailedSubjects) != 0 {
		t.Fatalf("add_batch reported failed subjects: %v", response.FailedSubjects)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state := live.harness.AwaitEntity(t, live.playerID)
		if len(testinfra.ObjectsFor(state, vocabulary.PlayerTurnCurrent.String())) == 2 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the appending lane left one pointer, so the merge-lane assertion above proves nothing about " +
		"which lane the recorder used")
}

func (l *liveGateway) advanceToCompleteFrom(t *testing.T, turnID, turnEntityID string) {
	t.Helper()
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving, vocabulary.PhaseApplying,
		vocabulary.PhaseNarrating, vocabulary.PhaseComplete,
	} {
		if _, err := l.recorder.Advance(t.Context(), turnID, turnEntityID, phase); err != nil {
			t.Fatalf("Advance to %q: %v", phase, err)
		}
	}
}

// A resubmission of the same key is one turn, not two — proven where it can
// actually be false, against a real stream and a real durable consumer.
func TestIntegration_AResubmittedKeyMakesOneTurnAndNotTwo(t *testing.T) {
	live := startGateway(t)
	live.startIntake(t)
	live.authenticate(t, "conn-1")

	first := live.submit(t, "conn-1", "key-1", "I open the gate.")
	turnEntityID := live.turnEntityID(t, first.TurnID)
	live.harness.AwaitEntity(t, turnEntityID)
	live.awaitPointer(t, turnEntityID)

	second := live.submit(t, "conn-1", "key-1", "I open the gate.")
	if second.Status != payload.StatusAccepted {
		t.Fatalf("a resubmission of the held turn was refused: %+v", second.Refusal)
	}
	if second.ActionID != first.ActionID || second.TurnID != first.TurnID {
		t.Fatalf("the resubmission resolved to %s/%s, the first to %s/%s",
			second.ActionID, second.TurnID, first.ActionID, first.TurnID)
	}

	// Let the redelivery land, then check the turn is still exactly one turn in
	// exactly one phase.
	live.awaitDrained(t)
	state := live.harness.AwaitEntity(t, turnEntityID)
	if got := testinfra.ObjectsFor(state, vocabulary.TurnPhaseCurrent.String()); len(got) != 1 {
		t.Fatalf("the resubmitted action left %d phase values: %v", len(got), got)
	}
	pointers := testinfra.ObjectsFor(live.harness.AwaitEntity(t, live.playerID),
		vocabulary.PlayerTurnCurrent.String())
	if len(pointers) != 1 || pointers[0] != turnEntityID {
		t.Fatalf("the resubmission left the player holding %v", pointers)
	}
}

// awaitDrained waits until the intake consumer has nothing pending and nothing
// unacknowledged. Both halves matter: pending zero alone would be satisfied by a
// consumer that took every message and never acked one.
func (l *liveGateway) awaitDrained(t *testing.T) {
	t.Helper()
	js, err := l.harness.Client.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		stream, err := js.Stream(t.Context(), l.stream)
		if err == nil {
			info, err := stream.Consumer(t.Context(), l.consumer)
			if err == nil {
				status, err := info.Info(t.Context())
				if err == nil && status.NumPending == 0 && status.NumAckPending == 0 {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the intake consumer never drained")
}

// Identity is not a connection, on the real path: a reconnect publishes actions
// carrying the same player entity id, and the turn records it.
func TestIntegration_ReconnectPublishesTheSamePlayerEntity(t *testing.T) {
	live := startGateway(t)
	live.startIntake(t)

	first := live.authenticate(t, "conn-1")
	accepted := live.submit(t, "conn-1", "key-1", "I open the gate.")
	firstEntity := live.turnEntityID(t, accepted.TurnID)
	live.harness.AwaitEntity(t, firstEntity)
	live.awaitPointer(t, firstEntity)
	live.advanceToCompleteFrom(t, accepted.TurnID, firstEntity)

	live.gateway.Disconnect("conn-1")
	second := live.authenticate(t, "conn-2")
	if second.PlayerID != first.PlayerID {
		t.Fatalf("the reconnect authenticated to %q, the first connection to %q",
			second.PlayerID, first.PlayerID)
	}

	next := live.submit(t, "conn-2", "key-2", "I step through.")
	if next.Status != payload.StatusAccepted {
		t.Fatalf("the reconnected player was refused: %+v", next.Refusal)
	}
	nextEntity := live.turnEntityID(t, next.TurnID)
	state := live.harness.AwaitEntity(t, nextEntity)
	if got := testinfra.FirstObject(state, vocabulary.TurnActionPlayer.String()); got != live.playerID {
		t.Fatalf("the turn taken after the reconnect records player %v, want %q", got, live.playerID)
	}
}

// A player who has never been imported cannot get a session, so no action is
// ever published in their name. This is the one property that makes "player_id
// is a graph entity" true rather than a naming convention.
func TestIntegration_AnUnimportedPlayerGetsNoSession(t *testing.T) {
	live := startGateway(t)

	ghost := compose(t, live.namespace, "player", "ghost")
	roster, err := gateway.NewRoster(map[string]string{"ghost-credential": ghost})
	if err != nil {
		t.Fatalf("NewRoster: %v", err)
	}
	gw, err := gateway.New(roster, live.store, live.harness.Client,
		gateway.Config{CampaignID: live.campaignID, SceneID: live.sceneID},
		gateway.WithSubject(live.subject),
		gateway.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatalf("gateway.New: %v", err)
	}

	_, err = gw.Authenticate(t.Context(), "ghost-credential",
		gateway.Connection{ID: "conn-ghost", Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-ghost"})
	if err == nil {
		t.Fatal("a player nothing ever imported was given a session")
	}
}

// ------------------------------------------------------- the action stream

// The stream nothing in production declared until now. Every limit is checked
// against the real broker's own view of the stream it created.
func TestIntegration_EnsureActionStreamCreatesTheStreamThisEngineRequires(t *testing.T) {
	harness := testinfra.Require(t)

	if err := turn.EnsureActionStream(t.Context(), harness.Client); err != nil {
		t.Fatalf("EnsureActionStream: %v", err)
	}

	js, err := harness.Client.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	stream, err := js.Stream(t.Context(), turn.ActionStream)
	if err != nil {
		t.Fatalf("the stream was not created: %v", err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}

	if info.Config.MaxAge != turn.ActionMaxAge {
		t.Errorf("MaxAge = %s, want %s; the auto-created default is seven days and an action that expires "+
			"unread is a move that silently never happened", info.Config.MaxAge, turn.ActionMaxAge)
	}
	if info.Config.MaxBytes != turn.ActionMaxBytes {
		t.Errorf("MaxBytes = %d, want %d", info.Config.MaxBytes, turn.ActionMaxBytes)
	}
	if info.Config.Discard != jetstream.DiscardNew {
		t.Errorf("Discard = %v, want DiscardNew; DiscardOld drops an already-queued action with no error",
			info.Config.Discard)
	}
	if info.Config.Retention != jetstream.LimitsPolicy {
		t.Errorf("Retention = %v, want limits", info.Config.Retention)
	}

	// The canonical publish subject must fall under the stream, or every
	// published action is refused by the broker.
	covered := false
	for _, subject := range info.Config.Subjects {
		if subject == turn.ActionSubjectPrefix {
			covered = true
		}
	}
	if !covered {
		t.Fatalf("the stream's subjects %v do not include %q", info.Config.Subjects, turn.ActionSubjectPrefix)
	}

	// Idempotent: a second call against the stream it just created must agree.
	if err := turn.EnsureActionStream(t.Context(), harness.Client); err != nil {
		t.Fatalf("a second EnsureActionStream refused the stream it had just created: %v", err)
	}
}

// The failure this readback exists for: something created the stream first with
// natsclient's defaults. EnsureStream returns the existing stream UNCHANGED, so
// without the readback the engine would run on a seven-day-MaxAge, DiscardOld
// stream and quietly drop a player's queued move.
func TestIntegration_EnsureActionStreamRefusesAStreamCreatedWithTheWrongLimits(t *testing.T) {
	harness := testinfra.Require(t)

	wrong := turn.ActionStreamConfig()
	wrong.Name = turn.ActionStream + "_WRONG"
	wrong.Subjects = []string{"itest.wronglimits.>"}
	wrong.MaxAge = 7 * 24 * time.Hour
	wrong.Discard = jetstream.DiscardOld
	if _, err := harness.Client.EnsureStream(t.Context(), wrong); err != nil {
		t.Fatalf("create the mis-configured stream: %v", err)
	}

	// EnsureActionStream names one stream, so aim the ensurer at the one just
	// created by renaming through the same code path the production call uses.
	err := turn.EnsureActionStream(t.Context(), renamingEnsurer{
		StreamEnsurer: harness.Client, name: wrong.Name, subjects: wrong.Subjects,
	})
	if err == nil {
		t.Fatal("a stream carrying a seven-day MaxAge and DiscardOld was accepted; the engine would " +
			"silently drop a player's queued move")
	}
	for _, want := range []string{"MaxAge", "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}

// renamingEnsurer points EnsureActionStream at a differently-named stream, so
// the mismatch check can be exercised without colliding with the canonical
// stream other tests in this broker create.
type renamingEnsurer struct {
	turn.StreamEnsurer
	name     string
	subjects []string
}

func (r renamingEnsurer) EnsureStream(
	ctx context.Context, cfg jetstream.StreamConfig,
) (jetstream.Stream, error) {
	cfg.Name = r.name
	cfg.Subjects = r.subjects
	return r.StreamEnsurer.EnsureStream(ctx, cfg)
}
