package turn_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testOrg      = "c360"
	testWorldNS  = "world1"
	testTemplate = "starter"

	testActionID     = "act-1"
	testTurnID       = "turn-act-1"
	testTurnEntityID = "c360.semmachina.world1.starter.turn.turn-act-1"

	testPlayerID   = "c360.semmachina.world1.starter.player.p1"
	testSceneID    = "c360.semmachina.world1.starter.scene.gatehouse"
	testCampaignID = "c360.semmachina.world1.starter.campaign.main"
)

var testTime = time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)

func testIdentity() turn.Identity {
	return turn.Identity{Org: testOrg, WorldNS: testWorldNS, Template: testTemplate}
}

func testAction() *payload.PlayerAction {
	return &payload.PlayerAction{
		ActionID:   testActionID,
		PlayerID:   testPlayerID,
		CampaignID: testCampaignID,
		SceneID:    testSceneID,
		Text:       "I lever the gate open with the crowbar.",
		ArrivedAt:  testTime,
		Channel: payload.ChannelBinding{
			Adapter: vocabulary.AdapterWebSocket,
			ReplyTo: "conn-7",
		},
	}
}

// journal records the ORDER of writes across the two stores a turn's birth
// touches.
//
// It exists because "the action object is written before the turn entity is
// created" is the entire crash-safety argument of this seam, and neither store
// can observe it alone: each sees only its own call. The failure it guards
// against — a reference committed to a turn whose object is not there yet — is
// invisible in every end-state assertion, because the end state after a
// successful run is identical either way.
type journal struct{ entries []string }

func (j *journal) add(entry string) { j.entries = append(j.entries, entry) }

// fakeStore is an in-memory graph with the two lanes that matter kept honest:
// create is atomic create-or-fail, and merge REPLACES by (subject, predicate).
// It exists for the failure paths a real broker will not produce on demand — a
// stub at a turn's key, a create that reports degraded, a transport error — and
// every property that depends on the real lanes is proven against real
// graph-ingest in turn_integration_test.go instead.
type fakeStore struct {
	entities map[string]*graph.EntityState
	journal  *journal

	createErr error
	getErr    error
	mergeErr  error
	degraded  bool

	creates int
	merges  int
}

func newFakeStore() *fakeStore {
	return &fakeStore{entities: map[string]*graph.EntityState{}, journal: &journal{}}
}

func (s *fakeStore) CreateEntity(_ context.Context, entity *graph.EntityState) (graphio.CreateResult, error) {
	s.creates++
	s.journal.add("create " + entity.ID)
	if s.createErr != nil {
		return graphio.CreateResult{}, s.createErr
	}
	if _, taken := s.entities[entity.ID]; taken {
		return graphio.CreateResult{}, fmt.Errorf("create entity %s: %w", entity.ID, graphio.ErrEntityExists)
	}
	stored := clone(entity)
	s.entities[entity.ID] = stored
	if s.degraded {
		return graphio.CreateResult{Degraded: true, DegradedReason: "read-back failed"}, nil
	}
	return graphio.CreateResult{Entity: clone(stored), Revision: uint64(s.creates)}, nil
}

func (s *fakeStore) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	stored, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	return clone(stored), nil
}

func (s *fakeStore) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	_ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	s.merges++
	if s.mergeErr != nil {
		return nil, s.mergeErr
	}
	stored, ok := s.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("merge into %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	for _, triple := range triples {
		if triple.Subject != entityID {
			return nil, fmt.Errorf("triple %q targets %q, not %q", triple.Predicate, triple.Subject, entityID)
		}
		replaced := false
		for idx := range stored.Triples {
			if stored.Triples[idx].Predicate == triple.Predicate {
				stored.Triples[idx] = triple
				replaced = true
				break
			}
		}
		if !replaced {
			stored.Triples = append(stored.Triples, triple)
		}
	}
	return clone(stored), nil
}

func clone(entity *graph.EntityState) *graph.EntityState {
	copied := *entity
	copied.Triples = append([]message.Triple(nil), entity.Triples...)
	return &copied
}

// appendingStore is the fakeStore with the other lane: it APPENDS rather than
// replacing. It exists so the reader's two-phase anomaly check is exercised
// against the shape that actually produces it.
type appendingStore struct{ *fakeStore }

func (s *appendingStore) MergeTriples(
	_ context.Context,
	entityID string,
	triples []message.Triple,
	_ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	stored, ok := s.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("merge into %s: %w", entityID, graphio.ErrEntityNotFound)
	}
	stored.Triples = append(stored.Triples, triples...)
	return clone(stored), nil
}

// fakeActions is an in-memory ActionStore that keeps the one property the
// production store's key derivation buys: a redelivery re-puts onto the same
// reference rather than producing a second one.
type fakeActions struct {
	journal *journal
	stored  map[string]*payload.PlayerAction
	puts    int
	err     error
}

func newFakeActions(j *journal) *fakeActions {
	return &fakeActions{journal: j, stored: map[string]*payload.PlayerAction{}}
}

func (a *fakeActions) PutAction(
	_ context.Context,
	turnEntityID string,
	action *payload.PlayerAction,
) (content.Ref, error) {
	a.puts++
	if a.err != nil {
		return content.Ref{}, a.err
	}
	turnID := payload.TurnIDForAction(action.ActionID)
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return content.Ref{}, err
	}
	key, err := content.KeyFor(vocabulary.TurnActionRef, turnID)
	if err != nil {
		return content.Ref{}, err
	}
	a.journal.add("put " + key)
	stored := *action
	a.stored[key] = &stored
	return content.Ref{Instance: "TEST_CONTENT", Key: key}, nil
}

func newRecorder(t *testing.T, store turn.Store) *turn.Recorder {
	t.Helper()
	return newRecorderWithActions(t, store, newFakeActions(journalOf(store)))
}

func newRecorderWithActions(t *testing.T, store turn.Store, actions turn.ActionStore) *turn.Recorder {
	t.Helper()
	recorder, err := turn.NewRecorder(
		store, actions, testIdentity(), turn.WithClock(func() time.Time { return testTime }))
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	return recorder
}

// journalOf reaches the shared write log through whichever store wrapper a test
// is using, so both stores record into one sequence.
func journalOf(store turn.Store) *journal {
	switch s := store.(type) {
	case *fakeStore:
		return s.journal
	case *appendingStore:
		return s.journal
	default:
		return &journal{}
	}
}

func objectsFor(state *graph.EntityState, predicate vocabulary.Predicate) []any {
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

// ---------------------------------------------------------------- identity

func TestIdentity_ComposesTheTurnIDAsTheInstanceSegment(t *testing.T) {
	turnID, entityID, err := testIdentity().EntityIDForAction(testActionID)
	if err != nil {
		t.Fatalf("EntityIDForAction: %v", err)
	}
	if turnID != testTurnID {
		t.Fatalf("derived turn id %q, want %q", turnID, testTurnID)
	}
	if entityID != testTurnEntityID {
		t.Fatalf("composed %q, want %q", entityID, testTurnEntityID)
	}
	// The binding the rest of the slice asserts everywhere the two are paired.
	if err := payload.RequireTurnEntityID(turnID, entityID); err != nil {
		t.Fatalf("the composer produced a pair its own binding rejects: %v", err)
	}
}

func TestIdentity_RejectsAnActionIDThatCannotBeASegment(t *testing.T) {
	for _, actionID := range []string{"", "a.b", "-leading"} {
		if _, _, err := testIdentity().EntityIDForAction(actionID); err == nil {
			t.Fatalf("action id %q composed a turn entity id", actionID)
		}
	}
}

// ------------------------------------------------------------------ accept

func TestAccept_CreatesTheTurnInAcceptedBeforeReturning(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)

	acceptance, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !acceptance.Created {
		t.Fatal("the first delivery did not create the turn")
	}
	if acceptance.Phase != vocabulary.PhaseAccepted {
		t.Fatalf("the turn was created in phase %q", acceptance.Phase)
	}

	// The entity must exist by the time Accept returns, because the caller
	// acknowledges the action on exactly that signal.
	stored, err := store.GetEntity(t.Context(), acceptance.TurnEntityID)
	if err != nil {
		t.Fatalf("the turn entity does not exist after a successful accept: %v", err)
	}
	if got := objectsFor(stored, vocabulary.TurnPhaseCurrent); len(got) != 1 ||
		got[0] != string(vocabulary.PhaseAccepted) {
		t.Fatalf("the created turn records phase %v", got)
	}
	if got := objectsFor(stored, vocabulary.TurnActionPlayer); len(got) != 1 || got[0] != testPlayerID {
		t.Fatalf("the created turn records player %v", got)
	}
	if got := objectsFor(stored, vocabulary.TurnActionScene); len(got) != 1 || got[0] != testSceneID {
		t.Fatalf("the created turn records scene %v", got)
	}
	if stored.IsStub() {
		t.Fatal("the created turn reads as a referential stub; every guard that asks about it would refuse")
	}
}

// The player's words reach the graph as a pointer, and the pointer is part of
// the turn's BIRTH. A turn that exists without one is a turn the rule pack can
// re-trigger and the adjudicator cannot re-prompt.
func TestAccept_TheActionReferenceRidesInsideTheAtomicCreate(t *testing.T) {
	store := newFakeStore()
	actions := newFakeActions(store.journal)
	recorder := newRecorderWithActions(t, store, actions)

	acceptance, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// One create and no follow-up write: a create-then-add-the-reference
	// sequence would leave a window in which the turn exists carrying no
	// pointer, and every guard would read that turn as complete.
	if store.creates != 1 || store.merges != 0 {
		t.Fatalf("the turn's birth took %d create(s) and %d merge(s); the reference must ride inside the "+
			"create, or the crash window the atomic create closes is reopened", store.creates, store.merges)
	}

	stored, err := store.GetEntity(t.Context(), acceptance.TurnEntityID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	refs := objectsFor(stored, vocabulary.TurnActionRef)
	if len(refs) != 1 {
		t.Fatalf("the created turn holds %d action references: %v", len(refs), refs)
	}
	text, _ := refs[0].(string)
	ref, err := content.ParseRef(text)
	if err != nil {
		t.Fatalf("the reference on the turn entity is not a storage reference: %v", err)
	}
	if actions.stored[ref.Key] == nil {
		t.Fatalf("the turn references %s, and no action was stored there; a reference to a missing object is "+
			"a turn nothing can re-prompt", ref)
	}
}

// Prose first, reference last — the ordering the whole seam rests on. The two
// failure modes are not symmetric: an object nobody references is garbage a
// redelivery overwrites, while a reference to a missing object is a correctness
// bug nothing can repair.
func TestAccept_StoresTheActionBeforeCreatingTheTurn(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorderWithActions(t, store, newFakeActions(store.journal))

	if _, err := recorder.Accept(t.Context(), testAction()); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if len(store.journal.entries) != 2 {
		t.Fatalf("the turn's birth issued %d writes: %v", len(store.journal.entries), store.journal.entries)
	}
	if !strings.HasPrefix(store.journal.entries[0], "put ") {
		t.Fatalf("the first write was %q, want the action object; a turn created first can be re-triggered "+
			"by the rule pack while the words it points at do not exist yet", store.journal.entries[0])
	}
	if !strings.HasPrefix(store.journal.entries[1], "create ") {
		t.Fatalf("the second write was %q, want the turn create", store.journal.entries[1])
	}
}

// The store is written unconditionally, including on the duplicate path. A
// crash may have happened before the first put landed, so the second delivery
// has to write rather than assume — and because the key is derived, writing
// again is free of consequence.
func TestAccept_ARedeliveryRePutsTheActionAndStillCreatesNoSecondTurn(t *testing.T) {
	store := newFakeStore()
	actions := newFakeActions(store.journal)
	recorder := newRecorderWithActions(t, store, actions)

	if _, err := recorder.Accept(t.Context(), testAction()); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if _, err := recorder.Accept(t.Context(), testAction()); err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	if actions.puts != 2 {
		t.Fatalf("two deliveries issued %d put(s); the second must re-put, since the crash it recovers from "+
			"may have happened before the first one landed", actions.puts)
	}
	if len(actions.stored) != 1 {
		t.Fatalf("two deliveries left %d stored actions: %v", len(actions.stored), actions.stored)
	}
	if len(store.entities) != 1 {
		t.Fatalf("two deliveries left %d turn entities", len(store.entities))
	}
}

// An unreachable store is a TRANSIENT failure, not a rejected action. Rejecting
// it would terminate the delivery and throw away a move the player can never
// resubmit, to spare a redelivery.
func TestAccept_AFailingActionStoreIsNotARejectionAndCreatesNoTurn(t *testing.T) {
	store := newFakeStore()
	actions := newFakeActions(store.journal)
	actions.err = errors.New("nats: no responders available")

	_, err := newRecorderWithActions(t, store, actions).Accept(t.Context(), testAction())
	if err == nil {
		t.Fatal("a turn was accepted while the player's words could not be stored")
	}
	var rejected *turn.RejectedActionError
	if errors.As(err, &rejected) {
		t.Fatal("an unreachable action store was classified as a permanent rejection; the player's move " +
			"would be terminated for a broken connection")
	}
	if store.creates != 0 {
		t.Fatal("a turn was created pointing at an action that was never stored")
	}
}

// The dependency is required, not optional. A recorder that could be built
// without one would accept turns whose action reference nothing wrote.
func TestNewRecorder_RefusesToBuildWithoutAnActionStore(t *testing.T) {
	if _, err := turn.NewRecorder(newFakeStore(), nil, testIdentity()); err == nil {
		t.Fatal("a recorder was built with no action store; every turn it accepted would lose the move")
	}
}

func TestAccept_ASecondDeliveryCreatesNoSecondTurn(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)

	first, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	second, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	if second.Created {
		t.Fatal("a duplicate delivery reported that it created the turn")
	}
	if second.TurnEntityID != first.TurnEntityID {
		t.Fatalf("the duplicate resolved to %q, the first to %q", second.TurnEntityID, first.TurnEntityID)
	}
	if len(store.entities) != 1 {
		t.Fatalf("two deliveries left %d entities", len(store.entities))
	}
}

// The duplicate that actually matters is the one that arrives LATE. Reporting a
// narrating turn as freshly accepted would invite a caller to start it over.
func TestAccept_ADuplicateReportsThePhaseTheTurnHasReached(t *testing.T) {
	store := newFakeStore()
	recorder := newRecorder(t, store)

	first, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	advanceTo(t, recorder, first, vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving)

	duplicate, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("duplicate Accept: %v", err)
	}
	if duplicate.Phase != vocabulary.PhaseResolving {
		t.Fatalf("the duplicate reports phase %q; the turn is in %q",
			duplicate.Phase, vocabulary.PhaseResolving)
	}
	stored, _ := store.GetEntity(t.Context(), first.TurnEntityID)
	if got := objectsFor(stored, vocabulary.TurnPhaseCurrent); got[0] != string(vocabulary.PhaseResolving) {
		t.Fatalf("the duplicate reset the turn's phase to %v", got[0])
	}
}

// A committed write whose read-back failed is a SUCCESS. Retrying it would come
// back as entity_already_exists, and a caller reading that as "somebody else got
// here first" would draw exactly the wrong conclusion about a turn it just made.
func TestAccept_TreatsADegradedCreateAsCreated(t *testing.T) {
	store := newFakeStore()
	store.degraded = true

	acceptance, err := newRecorder(t, store).Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !acceptance.Created {
		t.Fatal("a degraded create was reported as a duplicate")
	}
}

func TestAccept_RejectsAnActionThatCannotBecomeATurn(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*payload.PlayerAction)
	}{
		{"no action id", func(a *payload.PlayerAction) { a.ActionID = "" }},
		{"an action id that would add ID positions", func(a *payload.PlayerAction) { a.ActionID = "a.b" }},
		{"no player entity", func(a *payload.PlayerAction) { a.PlayerID = "" }},
		{"a player id that is not an entity", func(a *payload.PlayerAction) { a.PlayerID = "conn-7" }},
		{"no scene", func(a *payload.PlayerAction) { a.SceneID = "" }},
		{"no text", func(a *payload.PlayerAction) { a.Text = "" }},
		{"no arrival time", func(a *payload.PlayerAction) { a.ArrivedAt = time.Time{} }},
		{"an adapter outside the closed set", func(a *payload.PlayerAction) { a.Channel.Adapter = "carrier-pigeon" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := testAction()
			tc.mutate(action)

			store := newFakeStore()
			_, err := newRecorder(t, store).Accept(t.Context(), action)
			if err == nil {
				t.Fatal("the action was accepted")
			}
			var rejected *turn.RejectedActionError
			if !errors.As(err, &rejected) {
				t.Fatalf("rejection is a %T, not a RejectedActionError; only that type terminates the "+
					"delivery, so anything else redelivers a poison message forever", err)
			}
			if len(store.entities) != 0 {
				t.Fatal("a rejected action still created a turn entity")
			}
		})
	}
}

// A transport failure is NOT a rejection. It must redeliver, not terminate.
func TestAccept_ATransportFailureIsNotARejection(t *testing.T) {
	store := newFakeStore()
	store.createErr = errors.New("nats: timeout")

	_, err := newRecorder(t, store).Accept(t.Context(), testAction())
	if err == nil {
		t.Fatal("a failing create was reported as an accept")
	}
	var rejected *turn.RejectedActionError
	if errors.As(err, &rejected) {
		t.Fatal("a transport failure was classified as a permanent rejection; the player's action would " +
			"be terminated for a broken connection")
	}
}

// ------------------------------------------------------------------- read

func TestCurrent_RefusesEveryUnreadableTurnRecord(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeStore)
	}{
		{
			name: "a referential stub",
			setup: func(s *fakeStore) {
				s.entities[testTurnEntityID] = &graph.EntityState{ID: testTurnEntityID}
			},
		},
		{
			name: "a turn carrying no phase",
			setup: func(s *fakeStore) {
				s.entities[testTurnEntityID] = &graph.EntityState{
					ID: testTurnEntityID, MessageType: turn.EntityMessageType, Version: 1,
				}
			},
		},
		{
			name: "a phase value outside the closed set",
			setup: func(s *fakeStore) {
				s.entities[testTurnEntityID] = turnEntityWithPhase("adjudicatin")
			},
		},
		{
			name: "a phase stored as a number",
			setup: func(s *fakeStore) {
				entity := turnEntityWithPhase(vocabulary.PhaseApplying)
				entity.Triples[0].Object = 3
				s.entities[testTurnEntityID] = entity
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			tc.setup(store)

			_, err := newRecorder(t, store).Current(t.Context(), testTurnEntityID)
			if err == nil {
				t.Fatal("an unreadable turn record was read as a phase")
			}
			var record *turn.RecordError
			if !errors.As(err, &record) {
				t.Fatalf("anomaly reported as %T, want a RecordError so the caller escalates rather than "+
					"recording a game outcome on evidence the paperwork is corrupt", err)
			}
		})
	}
}

// A turn holding two phases is what an APPENDING lane leaves behind, and a
// reader that took the first value would be reading a coin flip.
func TestCurrent_RefusesATurnHoldingTwoPhases(t *testing.T) {
	store := &appendingStore{fakeStore: newFakeStore()}
	recorder := newRecorder(t, store)

	acceptance, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := recorder.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	_, err = recorder.Current(t.Context(), acceptance.TurnEntityID)
	if err == nil {
		t.Fatal("a turn holding two phase values reported a phase")
	}
	var record *turn.RecordError
	if !errors.As(err, &record) {
		t.Fatalf("anomaly reported as %T, want a RecordError", err)
	}
}

func turnEntityWithPhase(phase vocabulary.TurnPhase) *graph.EntityState {
	return &graph.EntityState{
		ID:          testTurnEntityID,
		MessageType: turn.EntityMessageType,
		Version:     1,
		UpdatedAt:   testTime,
		Triples: []message.Triple{{
			Subject:    testTurnEntityID,
			Predicate:  vocabulary.TurnPhaseCurrent.String(),
			Object:     string(phase),
			Source:     turn.Source,
			Timestamp:  testTime,
			Confidence: 1.0,
			Context:    testTurnID,
		}},
	}
}

// ---------------------------------------------------------------- advance

func advanceTo(t *testing.T, recorder *turn.Recorder, acceptance turn.Acceptance, phases ...vocabulary.TurnPhase) {
	t.Helper()
	for _, phase := range phases {
		transition, err := recorder.Advance(t.Context(), acceptance.TurnID, acceptance.TurnEntityID, phase)
		if err != nil {
			t.Fatalf("Advance to %q: %v", phase, err)
		}
		if transition.Outcome != turn.OutcomeAdvanced {
			t.Fatalf("Advance to %q reported %q, want advanced", phase, transition.Outcome)
		}
	}
}

func acceptedTurn(t *testing.T) (*fakeStore, *turn.Recorder, turn.Acceptance) {
	t.Helper()
	store := newFakeStore()
	recorder := newRecorder(t, store)
	acceptance, err := recorder.Accept(t.Context(), testAction())
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	return store, recorder, acceptance
}

func TestAdvance_WalksTheWholeTurnAndLeavesOnePhase(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)

	advanceTo(t, recorder, acceptance,
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseNarrating, vocabulary.PhaseComplete)

	stored, _ := store.GetEntity(t.Context(), acceptance.TurnEntityID)
	phases := objectsFor(stored, vocabulary.TurnPhaseCurrent)
	if len(phases) != 1 {
		t.Fatalf("a full turn left %d phase values: %v", len(phases), phases)
	}
	if phases[0] != string(vocabulary.PhaseComplete) {
		t.Fatalf("the turn ended in %v", phases[0])
	}
}

// A no-roll verdict never touches the dice, so the applier must be reachable
// straight from adjudicating (design D3).
func TestAdvance_ANoRollTurnReachesTheApplierWithoutResolving(t *testing.T) {
	_, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)
}

// The duplicate-trigger guarantee: the second trigger writes nothing and reports
// that the stage is being resumed rather than claimed.
func TestAdvance_ADuplicateTriggerWritesNothing(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating)

	before := store.merges
	transition, err := recorder.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("duplicate Advance: %v", err)
	}
	if transition.Outcome != turn.OutcomeResumed {
		t.Fatalf("a duplicate trigger reported %q, want resumed", transition.Outcome)
	}
	if store.merges != before {
		t.Fatalf("a duplicate trigger issued %d write(s)", store.merges-before)
	}
}

// A trigger for a stage the turn has already left is what at-least-once delivery
// produces on redelivery, and doing nothing is the correct response — this is
// "completed stages SHALL NOT re-run".
func TestAdvance_AStaleTriggerForACompletedStageDeclines(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving)

	before := store.merges
	transition, err := recorder.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("stale Advance: %v", err)
	}
	if transition.Outcome != turn.OutcomeDeclined {
		t.Fatalf("a stale trigger reported %q, want declined", transition.Outcome)
	}
	if transition.Phase != vocabulary.PhaseResolving {
		t.Fatalf("a declined transition reports phase %q, want the recorded %q",
			transition.Phase, vocabulary.PhaseResolving)
	}
	if store.merges != before {
		t.Fatalf("a stale trigger issued %d write(s)", store.merges-before)
	}
}

// A hop that skips a stage is a WIRING BUG, not a duplicate, and swallowing it
// would leave a turn that quietly never adjudicated.
func TestAdvance_RefusesToSkipAStage(t *testing.T) {
	cases := []struct {
		name string
		to   vocabulary.TurnPhase
	}{
		{"accepted straight to applying", vocabulary.PhaseApplying},
		{"accepted straight to narrating", vocabulary.PhaseNarrating},
		{"accepted straight to complete", vocabulary.PhaseComplete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, recorder, acceptance := acceptedTurn(t)

			_, err := recorder.Advance(t.Context(), acceptance.TurnID, acceptance.TurnEntityID, tc.to)
			if err == nil {
				t.Fatal("a stage-skipping hop was recorded")
			}
			var illegal *turn.IllegalTransitionError
			if !errors.As(err, &illegal) {
				t.Fatalf("refusal is a %T, want an IllegalTransitionError so a wiring bug is loud", err)
			}
			if store.merges != 0 {
				t.Fatal("a refused transition still wrote")
			}
		})
	}
}

func TestAdvance_ATerminalTurnDeclinesEverything(t *testing.T) {
	for _, ending := range []vocabulary.TurnPhase{vocabulary.PhaseComplete, vocabulary.PhaseFailed} {
		t.Run(string(ending), func(t *testing.T) {
			store, recorder, acceptance := acceptedTurn(t)
			advanceTo(t, recorder, acceptance,
				vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
				vocabulary.PhaseApplying, vocabulary.PhaseNarrating)
			if ending == vocabulary.PhaseComplete {
				advanceTo(t, recorder, acceptance, vocabulary.PhaseComplete)
			} else if _, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
				vocabulary.FailurePersonaCapExhausted, content.Ref{}); err != nil {
				t.Fatalf("Fail: %v", err)
			}

			before := store.merges
			for _, to := range []vocabulary.TurnPhase{
				vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
				vocabulary.PhaseApplying, vocabulary.PhaseNarrating,
			} {
				transition, err := recorder.Advance(t.Context(), acceptance.TurnID, acceptance.TurnEntityID, to)
				if err != nil {
					t.Fatalf("Advance to %q on a %q turn: %v", to, ending, err)
				}
				if transition.Outcome != turn.OutcomeDeclined {
					t.Fatalf("a %q turn reported %q for a move to %q", ending, transition.Outcome, to)
				}
			}
			if store.merges != before {
				t.Fatalf("a %q turn was written %d more time(s)", ending, store.merges-before)
			}
		})
	}
}

// The two phases Advance refuses outright are the same WIRING-BUG class as a
// stage-skipping hop — a caller reaching for the wrong lane — so they must be
// the same type, or an `errors.As` written to catch the class would silently
// miss two of its members.
func TestAdvance_RefusesTheTwoPhasesThatAreNotItsToWrite(t *testing.T) {
	cases := []struct {
		name  string
		to    vocabulary.TurnPhase
		fault turn.TransitionFault
	}{
		{"advanced back into accepted", vocabulary.PhaseAccepted, turn.FaultCreatedNotAdvanced},
		{"failed through Advance, which carries no reason", vocabulary.PhaseFailed, turn.FaultFailureNeedsReason},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, recorder, acceptance := acceptedTurn(t)

			_, err := recorder.Advance(t.Context(), acceptance.TurnID, acceptance.TurnEntityID, tc.to)
			if err == nil {
				t.Fatalf("a turn was %s", tc.name)
			}
			var illegal *turn.IllegalTransitionError
			if !errors.As(err, &illegal) {
				t.Fatalf("refusal is a %T, want an IllegalTransitionError so one errors.As catches the "+
					"whole wiring-bug class", err)
			}
			if illegal.Fault != tc.fault {
				t.Fatalf("refusal carries fault %q, want %q", illegal.Fault, tc.fault)
			}
			if store.merges != 0 {
				t.Fatal("a refused transition still wrote")
			}
		})
	}
}

// Four bugs share one type, so the MESSAGE is what tells them apart. A reader
// sent to the rule pack's sequencing by "that hop skips a stage" is reading the
// wrong file when the real fault is a misspelled phase or the wrong method, and
// one message for four bugs is how that happens.
func TestAdvance_DistinguishesTheFourWaysATransitionIsRefused(t *testing.T) {
	cases := []struct {
		name string
		to   vocabulary.TurnPhase
	}{
		{"created, not advanced", vocabulary.PhaseAccepted},
		{"a failure needs a reason", vocabulary.PhaseFailed},
		{"the hop skips a stage", vocabulary.PhaseComplete},
		{"the phase is outside the closed set", vocabulary.TurnPhase("narrat")},
	}

	_, recorder, acceptance := acceptedTurn(t)
	faults := map[turn.TransitionFault]string{}
	messages := map[string]string{}

	for _, tc := range cases {
		_, err := recorder.Advance(t.Context(), acceptance.TurnID, acceptance.TurnEntityID, tc.to)
		var illegal *turn.IllegalTransitionError
		if !errors.As(err, &illegal) {
			t.Fatalf("%s: produced %T, want an IllegalTransitionError", tc.name, err)
		}
		if first, repeated := faults[illegal.Fault]; repeated {
			t.Fatalf("%s reports fault %q, already reported by %q; two different bugs wear one fault",
				tc.name, illegal.Fault, first)
		}
		if first, repeated := messages[err.Error()]; repeated {
			t.Fatalf("%s reads identically to %q, so the message cannot tell a reader which bug to go "+
				"look for: %v", tc.name, first, err)
		}
		faults[illegal.Fault] = tc.name
		messages[err.Error()] = tc.name
	}
}

// A phase outside the closed set is a typo, and a typo must be loud from every
// phase — including a terminal one, where every legitimate trigger is declined
// as a benign no-op. An unknown target has rank zero, so a guard that consulted
// the table before checking membership would answer "declined" and swallow it.
func TestAdvance_AnUnknownPhaseIsLoudEvenOnATerminalTurn(t *testing.T) {
	_, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance,
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseNarrating, vocabulary.PhaseComplete)

	// Anti-vacuity: a real phase at this point is genuinely declined, so the
	// difference below is membership and nothing else.
	declined, err := recorder.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)
	if err != nil || declined.Outcome != turn.OutcomeDeclined {
		t.Fatalf("a stale trigger on a complete turn reported (%v, %v), want a decline", declined.Outcome, err)
	}

	_, err = recorder.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.TurnPhase("adjudicatin"))
	var illegal *turn.IllegalTransitionError
	if !errors.As(err, &illegal) {
		t.Fatalf("a misspelled phase on a terminal turn produced %T; the typo would be swallowed as a "+
			"benign stale trigger", err)
	}
	if illegal.Fault != turn.FaultUnknownPhase {
		t.Fatalf("a misspelled phase reported fault %q", illegal.Fault)
	}
}

// The wiring bug this group can introduce: a stage handed one turn's entity with
// another turn's id.
func TestAdvance_RefusesATurnEntityAddressingADifferentTurn(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)

	_, err := recorder.Advance(
		t.Context(), "turn-act-2", acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)
	if err == nil {
		t.Fatal("a phase was recorded for a turn the entity does not address")
	}
	if store.merges != 0 {
		t.Fatal("a mismatched pair still wrote")
	}
}

// -------------------------------------------------------------------- fail

func TestFail_RecordsThePhaseAndTheClosedReasonTogether(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)

	transition, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailureEffectInvalid, content.Ref{})
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if transition.Outcome != turn.OutcomeAdvanced {
		t.Fatalf("Fail reported %q", transition.Outcome)
	}

	stored, _ := store.GetEntity(t.Context(), acceptance.TurnEntityID)
	if got := objectsFor(stored, vocabulary.TurnPhaseCurrent); len(got) != 1 ||
		got[0] != string(vocabulary.PhaseFailed) {
		t.Fatalf("the failed turn records phase %v", got)
	}
	if got := objectsFor(stored, vocabulary.TurnFailureReason); len(got) != 1 ||
		got[0] != string(vocabulary.FailureEffectInvalid) {
		t.Fatalf("the failed turn records reason %v", got)
	}
	// One write, not two: a crash between them would leave a failed turn nobody
	// can explain, or a reason on a turn still reporting itself as running.
	if store.merges != 3 {
		t.Fatalf("the turn took %d merges; the failure must be one of them", store.merges)
	}
}

// The load-bearing gate of 6.3: the reason is a CODE, and a sentence is refused
// before it can reach the rule-matching surface.
func TestFail_RefusesAReasonOutsideTheClosedSet(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)

	for _, reason := range []vocabulary.FailureReason{
		"",
		"effect_invalid",
		"intent 3 (set_attribute on rook) exceeded the health bound",
	} {
		if _, err := recorder.Fail(
			t.Context(), acceptance.TurnID, acceptance.TurnEntityID, reason, content.Ref{}); err == nil {
			t.Fatalf("reason %q was recorded on the turn entity", reason)
		}
	}
	if store.merges != 0 {
		t.Fatal("a refused reason still wrote to the turn")
	}
}

func TestFail_DetailRidesAsAReference(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)

	key, err := content.KeyFor(vocabulary.TurnFailureRef, acceptance.TurnID)
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}
	detail := content.Ref{Instance: "TEST_CONTENT", Key: key}

	if _, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailureEffectInvalid, detail); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	stored, _ := store.GetEntity(t.Context(), acceptance.TurnEntityID)
	if got := objectsFor(stored, vocabulary.TurnFailureRef); len(got) != 1 || got[0] != detail.String() {
		t.Fatalf("the failed turn records detail %v, want %s", got, detail)
	}
}

// The detail parameter is a REFERENCE, and the type is what enforces it. A
// caller holding an explanation has nothing to pass — it has to store the
// explanation first — which is the whole reason the closed reason code survives
// contact with an applier that has plenty to say.
func TestFail_RefusesADetailThatIsNotAResolvableReference(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying)

	notAReference := content.Ref{Instance: "TEST_CONTENT"}
	if _, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailureEffectInvalid, notAReference); err == nil {
		t.Fatal("a reference with no key was recorded on the turn entity")
	}
	if store.merges != 2 {
		t.Fatalf("the refused failure wrote to the turn (%d merges, want the 2 advances)", store.merges)
	}
}

// The first reason is the one that actually happened. A later failure is a stale
// trigger, and a completed turn is not failed after the fact.
func TestFail_ATerminalTurnKeepsItsFirstOutcome(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating)

	if _, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailurePersonaCapExhausted, content.Ref{}); err != nil {
		t.Fatalf("first Fail: %v", err)
	}

	before := store.merges
	transition, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
		vocabulary.FailureEffectInvalid, content.Ref{})
	if err != nil {
		t.Fatalf("second Fail: %v", err)
	}
	if transition.Outcome != turn.OutcomeDeclined {
		t.Fatalf("a second failure reported %q, want declined", transition.Outcome)
	}
	if store.merges != before {
		t.Fatal("a second failure overwrote the first reason")
	}

	stored, _ := store.GetEntity(t.Context(), acceptance.TurnEntityID)
	if got := objectsFor(stored, vocabulary.TurnFailureReason); got[0] !=
		string(vocabulary.FailurePersonaCapExhausted) {
		t.Fatalf("the turn records reason %v, want the first one", got[0])
	}
}

// Every non-terminal stage can end the turn explicitly. A stage with no path to
// failed is a stage that can only stall.
func TestFail_IsReachableFromEveryNonTerminalPhase(t *testing.T) {
	walk := []vocabulary.TurnPhase{
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving,
		vocabulary.PhaseApplying, vocabulary.PhaseNarrating,
	}
	for idx := range append([]vocabulary.TurnPhase{vocabulary.PhaseAccepted}, walk...) {
		_, recorder, acceptance := acceptedTurn(t)
		advanceTo(t, recorder, acceptance, walk[:idx]...)

		transition, err := recorder.Fail(t.Context(), acceptance.TurnID, acceptance.TurnEntityID,
			vocabulary.FailurePersonaCapExhausted, content.Ref{})
		if err != nil {
			t.Fatalf("Fail after %d advances: %v", idx, err)
		}
		if transition.Outcome != turn.OutcomeAdvanced {
			t.Fatalf("Fail after %d advances reported %q", idx, transition.Outcome)
		}
	}
}

// --------------------------------------------------------------- resume

// Crash-at-any-phase, in the shape the spec names: the process dies with the
// turn in `resolving`, restarts as a brand-new recorder holding no memory, and
// the recorded phase alone has to decide what runs. The completed stage declines,
// the interrupted one resumes, and the next one advances.
func TestResume_AFreshRecorderReadsTheRecordedPhaseAndNothingElse(t *testing.T) {
	store, recorder, acceptance := acceptedTurn(t)
	advanceTo(t, recorder, acceptance, vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving)

	// The crash: everything in memory is gone. Only the graph survives.
	restarted := newRecorder(t, store)

	completed, err := restarted.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseAdjudicating)
	if err != nil {
		t.Fatalf("re-entering a completed stage: %v", err)
	}
	if completed.Outcome != turn.OutcomeDeclined {
		t.Fatalf("a completed stage reported %q after restart; it would re-run", completed.Outcome)
	}

	interrupted, err := restarted.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseResolving)
	if err != nil {
		t.Fatalf("re-entering the interrupted stage: %v", err)
	}
	if interrupted.Outcome != turn.OutcomeResumed {
		t.Fatalf("the interrupted stage reported %q after restart; it would never finish",
			interrupted.Outcome)
	}

	next, err := restarted.Advance(
		t.Context(), acceptance.TurnID, acceptance.TurnEntityID, vocabulary.PhaseApplying)
	if err != nil {
		t.Fatalf("advancing past the interrupted stage: %v", err)
	}
	if next.Outcome != turn.OutcomeAdvanced {
		t.Fatalf("the turn could not advance after restart: %q", next.Outcome)
	}
}

// ------------------------------------------------------------------ intake

func testDecoder(t *testing.T) *message.Decoder {
	t.Helper()
	reg := payloadregistry.New()
	if err := payload.RegisterPayloads(reg); err != nil {
		t.Fatalf("RegisterPayloads: %v", err)
	}
	return message.NewDecoder(reg)
}

func wire(t *testing.T, p message.Payload) []byte {
	t.Helper()
	data, err := json.Marshal(message.NewBaseMessage(p.Schema(), p, "ingress-test", message.WithTime(testTime)))
	if err != nil {
		t.Fatalf("marshal BaseMessage: %v", err)
	}
	return data
}

// wireWithBody builds a decodable envelope while BYPASSING BaseMessage's
// validation gate.
//
// Both cases it serves are messages a well-behaved publisher cannot produce —
// BaseMessage validates on marshal — and both are messages intake will
// nonetheless meet: a misrouted publisher putting the wrong type on the subject,
// and an ingress adapter that hand-built its own envelope. Intake cannot assume
// its input passed anybody else's gate.
func wireWithBody(t *testing.T, msgType message.Type, body any) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload body: %v", err)
	}
	data, err := json.Marshal(map[string]any{
		"id":      "intake-test-message",
		"type":    msgType,
		"payload": json.RawMessage(encoded),
		"meta": map[string]any{
			"created_at":  testTime.UnixMilli(),
			"received_at": testTime.UnixMilli(),
			"source":      "intake-test",
		},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return data
}

func newIntake(t *testing.T, store turn.Store) *turn.Intake {
	t.Helper()
	intake, err := turn.NewIntake(stubConsumer{}, newRecorder(t, store), testDecoder(t))
	if err != nil {
		t.Fatalf("NewIntake: %v", err)
	}
	return intake
}

// stubConsumer stands in for the framework's durable consumer in the tests that
// drive Handle directly. Start is exercised against a real broker instead.
type stubConsumer struct{}

func (stubConsumer) ConsumeDurable(
	context.Context, natsclient.StreamConsumerConfig, time.Duration, func(context.Context, []byte) error,
) error {
	return nil
}

// Handle's return value IS the ack decision, so "acknowledge only after the turn
// entity exists" is a property of exactly this: nil means the entity is there.
func TestIntakeHandle_ReturnsNilOnlyOnceTheTurnExists(t *testing.T) {
	store := newFakeStore()
	intake := newIntake(t, store)

	if err := intake.Handle(t.Context(), wire(t, testAction())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if _, err := store.GetEntity(t.Context(), testTurnEntityID); err != nil {
		t.Fatalf("the handler acknowledged an action whose turn does not exist: %v", err)
	}
}

func TestIntakeHandle_ASecondDeliveryAcksAndCreatesNoSecondTurn(t *testing.T) {
	store := newFakeStore()
	intake := newIntake(t, store)
	data := wire(t, testAction())

	for attempt := range 3 {
		if err := intake.Handle(t.Context(), data); err != nil {
			t.Fatalf("delivery %d: %v", attempt+1, err)
		}
	}
	if len(store.entities) != 1 {
		t.Fatalf("three deliveries left %d turn entities", len(store.entities))
	}
	stored, _ := store.GetEntity(t.Context(), testTurnEntityID)
	if got := objectsFor(stored, vocabulary.TurnPhaseCurrent); len(got) != 1 {
		t.Fatalf("three deliveries left %d phase values: %v", len(got), got)
	}
}

// A crash between the create and the ack redelivers the action into an existing
// turn — including one that has since moved on. It must be acknowledged, and it
// must not be restarted.
func TestIntakeHandle_ARedeliveryIntoAnAdvancedTurnAcksWithoutResetting(t *testing.T) {
	store := newFakeStore()
	intake := newIntake(t, store)
	recorder := newRecorder(t, store)
	data := wire(t, testAction())

	if err := intake.Handle(t.Context(), data); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	advanceTo(t, recorder, turn.Acceptance{TurnID: testTurnID, TurnEntityID: testTurnEntityID},
		vocabulary.PhaseAdjudicating, vocabulary.PhaseResolving, vocabulary.PhaseApplying)

	if err := intake.Handle(t.Context(), data); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	stored, _ := store.GetEntity(t.Context(), testTurnEntityID)
	if got := objectsFor(stored, vocabulary.TurnPhaseCurrent); got[0] != string(vocabulary.PhaseApplying) {
		t.Fatalf("a redelivery reset an in-flight turn to %v", got[0])
	}
}

// A message that can never succeed must be TERMINATED, not nak'd: unlimited
// redelivery of a deterministic failure is an infinite loop that wedges every
// action behind it.
func TestIntakeHandle_TerminatesDeliveriesThatCanNeverSucceed(t *testing.T) {
	badAction := testAction()
	badAction.ActionID = "a.b"

	cases := []struct {
		name string
		data []byte
	}{
		{"bytes that are not a message at all", []byte("{not json")},
		{
			name: "a message of some other registered type",
			data: wireWithBody(t, (&payload.RollResult{}).Schema(), struct{}{}),
		},
		{
			name: "an action whose identity cannot compose a turn entity",
			data: wireWithBody(t, badAction.Schema(), badAction),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			err := newIntake(t, store).Handle(t.Context(), tc.data)
			if err == nil {
				t.Fatal("the delivery was acknowledged")
			}
			var permanent *natsclient.PermanentDeliveryError
			if !errors.As(err, &permanent) {
				t.Fatalf("failure is a %T, not a PermanentDeliveryError; it would be redelivered forever", err)
			}
			if len(store.entities) != 0 {
				t.Fatal("a terminated delivery still created a turn")
			}
		})
	}
}

// A transient failure must NOT terminate: the player's action is still in the
// stream and must be redelivered.
func TestIntakeHandle_NaksATransientFailure(t *testing.T) {
	store := newFakeStore()
	store.createErr = errors.New("nats: no responders available")

	err := newIntake(t, store).Handle(t.Context(), wire(t, testAction()))
	if err == nil {
		t.Fatal("a failing create was acknowledged")
	}
	var permanent *natsclient.PermanentDeliveryError
	if errors.As(err, &permanent) {
		t.Fatal("a transport failure terminated the delivery; the player's action would be dropped for " +
			"a broken connection")
	}
}

// DeliverPolicy is the difference between "an action submitted while the engine
// was down is processed on the next boot" and "it is silently skipped forever".
func TestIntakeConsumerConfig_DeliversEverythingTheStreamHolds(t *testing.T) {
	cfg := newIntake(t, newFakeStore()).ConsumerConfig()

	if cfg.DeliverPolicy != "all" {
		t.Fatalf("deliver policy is %q; with anything else an action published while the engine was down "+
			"is skipped, which is exactly the email-cadence case", cfg.DeliverPolicy)
	}
	if cfg.ConsumerName == "" {
		t.Fatal("the consumer is ephemeral; a restart would not resume from the last ack")
	}
	if cfg.AckPolicy != "explicit" {
		t.Fatalf("ack policy is %q; without explicit acks the ordering guarantee is meaningless", cfg.AckPolicy)
	}
	if cfg.MaxDeliver != 0 {
		t.Fatalf("max deliver is %d; a bounded redelivery count silently drops a player's action after a "+
			"run of transport failures", cfg.MaxDeliver)
	}
}
