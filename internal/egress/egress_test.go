package egress_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testOrg      = "c360"
	testWorldNS  = "world1"
	testTemplate = "starter"
	testPrefix   = testOrg + ".semmachina." + testWorldNS + "." + testTemplate

	testCampaignID = testPrefix + ".campaign.main"
	testSceneID    = testPrefix + ".scene.gatehouse"
	testPlayerID   = testPrefix + ".player.pat"
	testOtherID    = testPrefix + ".player.alex"

	testActionID     = "act-1"
	testTurnID       = "turn-act-1"
	testTurnEntityID = testPrefix + ".turn.turn-act-1"

	testSource   = "test"
	testInstance = "SEMMACHINA_CONTENT"
)

var testTime = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func testIdentity() turn.Identity {
	return turn.Identity{Org: testOrg, WorldNS: testWorldNS, Template: testTemplate}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---------------------------------------------------------------- fixtures

// turnBuilder assembles a turn entity out of the SAME triple projections the
// stages write, so a retrieval test reads the shape production produces rather
// than one a test author imagined.
type turnBuilder struct {
	t            *testing.T
	turnID       string
	turnEntityID string
	triples      []message.Triple
	at           time.Time
}

func newTurn(t *testing.T, turnID string) *turnBuilder {
	t.Helper()
	entityID, err := testIdentity().EntityID(turnID)
	if err != nil {
		t.Fatalf("compose the turn entity id: %v", err)
	}
	return &turnBuilder{t: t, turnID: turnID, turnEntityID: entityID, at: testTime}
}

func (b *turnBuilder) add(triples []message.Triple, err error) *turnBuilder {
	b.t.Helper()
	if err != nil {
		b.t.Fatalf("project triples: %v", err)
	}
	// Replace by predicate, exactly as the merge lane does.
	for _, triple := range triples {
		kept := b.triples[:0]
		for _, existing := range b.triples {
			if existing.Predicate != triple.Predicate {
				kept = append(kept, existing)
			}
		}
		b.triples = append(kept, triple)
	}
	return b
}

func (b *turnBuilder) accepted(playerID string) *turnBuilder {
	state := &payload.TurnState{
		TurnID:    b.turnID,
		Phase:     vocabulary.PhaseAccepted,
		PlayerID:  playerID,
		SceneID:   testSceneID,
		ActionRef: refFor(vocabulary.TurnActionRef, b.turnID),
	}
	return b.add(state.Triples(b.turnEntityID, testSource, b.at))
}

func (b *turnBuilder) adjudicated(verdict *payload.Verdict) *turnBuilder {
	return b.add(verdict.Triples(b.turnEntityID, refFor(vocabulary.TurnVerdictRef, b.turnID), testSource, b.at))
}

func (b *turnBuilder) rolled(roll *payload.RollResult) *turnBuilder {
	return b.add(roll.Triples(b.turnEntityID, refFor(vocabulary.TurnRollRef, b.turnID), testSource, b.at))
}

func (b *turnBuilder) applied(batch *payload.EffectBatch) *turnBuilder {
	return b.add(batch.Triples(b.turnEntityID, refFor(vocabulary.TurnEffectsRef, b.turnID), testSource, b.at))
}

func (b *turnBuilder) narrated() *turnBuilder {
	record := &payload.TurnNarration{
		TurnID:       b.turnID,
		NarrationRef: refFor(vocabulary.TurnNarrationRef, b.turnID),
	}
	return b.add(record.Triples(b.turnEntityID, testSource, b.at))
}

// phase writes the turn's phase at a chosen moment, which is what a
// most-recent-terminal lookup compares.
func (b *turnBuilder) phase(phase vocabulary.TurnPhase, at time.Time) *turnBuilder {
	state := &payload.TurnState{TurnID: b.turnID, Phase: phase}
	return b.add(state.Triples(b.turnEntityID, testSource, at))
}

func (b *turnBuilder) failed(reason vocabulary.FailureReason, at time.Time) *turnBuilder {
	state := &payload.TurnState{TurnID: b.turnID, Phase: vocabulary.PhaseFailed, Reason: reason}
	return b.add(state.Triples(b.turnEntityID, testSource, at))
}

func (b *turnBuilder) build() *graph.EntityState {
	return &graph.EntityState{
		ID: b.turnEntityID,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryTurnState, Version: payload.SchemaVersion,
		},
		Version:   1,
		UpdatedAt: b.at,
		Triples:   append([]message.Triple(nil), b.triples...),
	}
}

func refFor(predicate vocabulary.Predicate, turnID string) string {
	key, err := content.KeyFor(predicate, content.SubjectTurn, turnID)
	if err != nil {
		panic("test reference: " + err.Error())
	}
	return content.Ref{Instance: testInstance, Key: key}.String()
}

func testVerdict(turnID string, requiresRoll bool) *payload.Verdict {
	actionID, err := payload.ActionIDForTurn(turnID)
	if err != nil {
		panic("test verdict: " + err.Error())
	}
	verdict := &payload.Verdict{
		TurnID:   turnID,
		ActionID: actionID,
		SceneID:  testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskHigh,
			Consequence:  vocabulary.ConsequenceHarm,
			RequiresRoll: requiresRoll,
		},
		Modifiers: []payload.Modifier{
			{Source: vocabulary.ModifierEquipment, Value: 1, Note: "crowbar"},
		},
		Rationale: "The gate is heavy but the crowbar bites.",
	}
	if requiresRoll {
		verdict.Bands = payload.EffectBands{
			vocabulary.BandMiss:    {},
			vocabulary.BandPartial: {},
			vocabulary.BandFull:    {},
		}
	} else {
		verdict.Scalars.Risk = vocabulary.RiskNone
		verdict.Scalars.Consequence = vocabulary.ConsequenceNone
		verdict.Modifiers = nil
		verdict.Bands = payload.EffectBands{vocabulary.BandAuto: {}}
	}
	return verdict
}

func testRoll(turnID string) *payload.RollResult {
	return &payload.RollResult{
		TurnID:        turnID,
		Mechanic:      vocabulary.Mechanic2d6PbtaV1,
		RNGVersion:    vocabulary.RNGPCGV1,
		Seed:          payload.SeedSource{CampaignID: testCampaignID, TurnID: turnID},
		Dice:          []int{4, 4},
		Modifiers:     []payload.Modifier{{Source: vocabulary.ModifierEquipment, Value: 1, Note: "crowbar"}},
		ModifierTotal: 1,
		Total:         9,
		Band:          vocabulary.BandPartial,
	}
}

func testNarration(turnID string, band vocabulary.OutcomeBand) *content.Narration {
	return &content.Narration{TurnID: turnID, Band: band, Prose: "The gate groans open a hand's width."}
}

// ---------------------------------------------------------------- doubles

type fakeGraph struct {
	entities map[string]*graph.EntityState
	err      map[string]error
	reads    []string
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{entities: map[string]*graph.EntityState{}, err: map[string]error{}}
}

func (g *fakeGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	g.reads = append(g.reads, id)
	if err, ok := g.err[id]; ok {
		return nil, err
	}
	state, ok := g.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	clone := *state
	clone.Triples = append([]message.Triple(nil), state.Triples...)
	return &clone, nil
}

func (g *fakeGraph) putTurn(state *graph.EntityState) { g.entities[state.ID] = state }

// player writes a player entity carrying whichever pointers a test needs, through
// the production projections — so a test cannot accidentally write a pointer shape
// the recorder could not produce.
func (g *fakeGraph) player(t *testing.T, playerID string, current, resolved *graph.EntityState) {
	t.Helper()
	state := &graph.EntityState{
		ID: playerID,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version: 1,
		Triples: []message.Triple{{
			Subject:   playerID,
			Predicate: vocabulary.WorldEntityKind.String(),
			Object:    string(vocabulary.EntityKindPlayer),
			Source:    "world-import",
			Timestamp: testTime,
		}},
	}
	if current != nil {
		pointer := &payload.PlayerTurn{
			PlayerID: playerID, TurnID: turnIDOf(current.ID), TurnEntityID: current.ID,
		}
		triples, err := pointer.Triples(playerID, turn.Source, testTime)
		if err != nil {
			t.Fatalf("project the current pointer: %v", err)
		}
		state.Triples = append(state.Triples, triples...)
	}
	if resolved != nil {
		pointer := &payload.PlayerResolvedTurn{
			PlayerID: playerID, TurnID: turnIDOf(resolved.ID), TurnEntityID: resolved.ID,
		}
		triples, err := pointer.Triples(playerID, turn.Source, testTime)
		if err != nil {
			t.Fatalf("project the resolved pointer: %v", err)
		}
		state.Triples = append(state.Triples, triples...)
	}
	g.entities[playerID] = state
}

func turnIDOf(turnEntityID string) string {
	for i := len(turnEntityID) - 1; i >= 0; i-- {
		if turnEntityID[i] == '.' {
			return turnEntityID[i+1:]
		}
	}
	return turnEntityID
}

type fakeArtifacts struct {
	rolls      map[string]*payload.RollResult
	narrations map[string]*content.Narration
}

func newFakeArtifacts() *fakeArtifacts {
	return &fakeArtifacts{
		rolls:      map[string]*payload.RollResult{},
		narrations: map[string]*content.Narration{},
	}
}

func (a *fakeArtifacts) GetRoll(_ context.Context, ref content.Ref) (*payload.RollResult, error) {
	roll, ok := a.rolls[ref.String()]
	if !ok {
		return nil, fmt.Errorf("resolve %s: %w", ref, content.ErrArtifactNotFound)
	}
	return roll, nil
}

func (a *fakeArtifacts) GetNarration(_ context.Context, ref content.Ref) (*content.Narration, error) {
	narration, ok := a.narrations[ref.String()]
	if !ok {
		return nil, fmt.Errorf("resolve %s: %w", ref, content.ErrArtifactNotFound)
	}
	return narration, nil
}

// fakeDirectory answers the ONE question the router may ask. It deliberately
// exposes no way to list every session, mirroring the production interface: a
// double that could broadcast would let a test pass that production could not.
type fakeDirectory struct {
	sessions map[string][]gateway.Session
}

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{sessions: map[string][]gateway.Session{}}
}

func (d *fakeDirectory) connect(playerID string, connIDs ...string) {
	for _, connID := range connIDs {
		d.sessions[playerID] = append(d.sessions[playerID], gateway.Session{
			PlayerID: playerID,
			Connection: gateway.Connection{
				ID: connID, Adapter: vocabulary.AdapterWebSocket, ReplyTo: connID,
			},
		})
	}
}

func (d *fakeDirectory) SessionsFor(playerID string) []gateway.Session {
	return append([]gateway.Session(nil), d.sessions[playerID]...)
}

// sentTo is one document written to one connection.
type sentTo struct {
	ConnID   string
	PlayerID string
	TurnID   string
	Prose    string
}

type fakeSink struct {
	sent []sentTo
	err  map[string]error
}

func newFakeSink() *fakeSink { return &fakeSink{err: map[string]error{}} }

func (s *fakeSink) Deliver(
	_ context.Context,
	session gateway.Session,
	delivery *payload.TurnDelivery,
) error {
	if err, ok := s.err[session.Connection.ID]; ok {
		return err
	}
	entry := sentTo{
		ConnID:   session.Connection.ID,
		PlayerID: delivery.Result.PlayerID,
		TurnID:   delivery.Result.TurnID,
	}
	if delivery.Narration != nil {
		entry.Prose = delivery.Narration.Prose
	}
	s.sent = append(s.sent, entry)
	return nil
}

func (s *fakeSink) to(connID string) []sentTo {
	var out []sentTo
	for _, entry := range s.sent {
		if entry.ConnID == connID {
			out = append(out, entry)
		}
	}
	return out
}

// ---------------------------------------------------------------- harness

type harness struct {
	graph     *fakeGraph
	artifacts *fakeArtifacts
	directory *fakeDirectory
	sink      *fakeSink
	results   *egress.Results
	router    *egress.Router
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		graph:     newFakeGraph(),
		artifacts: newFakeArtifacts(),
		directory: newFakeDirectory(),
		sink:      newFakeSink(),
	}
	results, err := egress.NewResults(h.graph, h.artifacts, testIdentity(), testCampaignID)
	if err != nil {
		t.Fatalf("NewResults: %v", err)
	}
	router, err := egress.NewRouter(h.directory, h.sink, egress.WithRouterLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	h.results, h.router = results, router
	return h
}

// completedTurn stores a fully resolved turn plus the artifacts it references.
func (h *harness) completedTurn(t *testing.T, turnID, playerID string, at time.Time) *graph.EntityState {
	t.Helper()
	verdict := testVerdict(turnID, true)
	roll := testRoll(turnID)
	batch := payload.NewEffectBatch(turnID, roll.Band, nil)

	state := newTurn(t, turnID).
		accepted(playerID).
		adjudicated(verdict).
		rolled(roll).
		applied(batch).
		narrated().
		phase(vocabulary.PhaseComplete, at).
		build()

	h.graph.putTurn(state)
	h.artifacts.rolls[refFor(vocabulary.TurnRollRef, turnID)] = roll
	h.artifacts.narrations[refFor(vocabulary.TurnNarrationRef, turnID)] = testNarration(turnID, roll.Band)
	return state
}

func (h *harness) mustDeliver(t *testing.T, turnID string) *payload.TurnDelivery {
	t.Helper()
	delivery, err := h.results.ByTurn(t.Context(), turnID)
	if err != nil {
		t.Fatalf("ByTurn(%s): %v", turnID, err)
	}
	return delivery
}
