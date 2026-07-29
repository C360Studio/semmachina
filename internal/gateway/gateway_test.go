package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testOrg        = "c360.semmachina.world1.starter"
	testPlayerID   = testOrg + ".player.pat"
	testOtherID    = testOrg + ".player.alex"
	testCampaignID = testOrg + ".campaign.instance"
	testSceneID    = testOrg + ".scene.gate"
	testCredential = "pat-local-credential"
	testConnID     = "conn-7"
)

var testTime = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------- fakes

// fakeStore is an in-memory graph read surface. Every property that depends on
// what a REAL graph does with these writes is proven against real graph-ingest
// in gateway_integration_test.go instead; this exists for the failure paths a
// broker will not produce on demand.
type fakeStore struct {
	entities map[string]*graph.EntityState
	err      map[string]error
	reads    []string
}

func newFakeStore() *fakeStore {
	store := &fakeStore{entities: map[string]*graph.EntityState{}, err: map[string]error{}}
	store.putPlayer(testPlayerID)
	store.putPlayer(testOtherID)
	return store
}

func (s *fakeStore) putPlayer(id string) {
	s.entities[id] = &graph.EntityState{
		ID: id,
		MessageType: message.Type{
			Domain: payload.Domain, Category: payload.CategoryWorldEntity, Version: payload.SchemaVersion,
		},
		Version: 1,
		Triples: []message.Triple{{
			Subject:   id,
			Predicate: vocabulary.WorldEntityKind.String(),
			Object:    string(vocabulary.EntityKindPlayer),
			Source:    "world-import",
			Timestamp: testTime,
		}},
	}
}

// pointAt writes the admission gate's pointer the way the turn recorder does.
func (s *fakeStore) pointAt(t *testing.T, playerID, turnEntityID string) {
	t.Helper()
	pointer := &payload.PlayerTurn{
		PlayerID:     playerID,
		TurnID:       turnIDOf(turnEntityID),
		TurnEntityID: turnEntityID,
	}
	triples, err := pointer.Triples(playerID, turn.Source, testTime)
	if err != nil {
		t.Fatalf("compose the player pointer: %v", err)
	}
	s.merge(playerID, triples)
}

// appendPointer writes a SECOND pointer the way an appending lane would, which
// is the anomaly the gate has to survive.
func (s *fakeStore) appendPointer(t *testing.T, playerID, turnEntityID string) {
	t.Helper()
	pointer := &payload.PlayerTurn{
		PlayerID:     playerID,
		TurnID:       turnIDOf(turnEntityID),
		TurnEntityID: turnEntityID,
	}
	triples, err := pointer.Triples(playerID, turn.Source, testTime)
	if err != nil {
		t.Fatalf("compose the player pointer: %v", err)
	}
	s.entities[playerID].Triples = append(s.entities[playerID].Triples, triples...)
}

func (s *fakeStore) merge(entityID string, triples []message.Triple) {
	stored := s.entities[entityID]
	for _, triple := range triples {
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
}

// putTurn stores a turn entity in a phase.
func (s *fakeStore) putTurn(turnEntityID string, phase vocabulary.TurnPhase) {
	s.entities[turnEntityID] = &graph.EntityState{
		ID:          turnEntityID,
		MessageType: turn.EntityMessageType,
		Version:     1,
		Triples: []message.Triple{{
			Subject:   turnEntityID,
			Predicate: vocabulary.TurnPhaseCurrent.String(),
			Object:    string(phase),
			Source:    turn.Source,
			Timestamp: testTime,
		}},
	}
}

func (s *fakeStore) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	s.reads = append(s.reads, id)
	if err, ok := s.err[id]; ok {
		return nil, err
	}
	stored, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	copied := *stored
	copied.Triples = append([]message.Triple(nil), stored.Triples...)
	return &copied, nil
}

// fakePublisher records what reached the stream, and can refuse.
type fakePublisher struct {
	published []publication
	err       error
}

type publication struct {
	subject string
	msgID   string
	data    []byte
}

func (p *fakePublisher) PublishToStreamWithMsgID(
	_ context.Context, subject string, data []byte, msgID string,
) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, publication{subject: subject, msgID: msgID, data: append([]byte(nil), data...)})
	return nil
}

// actions decodes every published action, through the SAME envelope intake
// decodes: a test that read the struct it handed the gateway would prove the
// gateway copied a pointer, not that an action reached the stream.
func (p *fakePublisher) actions(t *testing.T) []*payload.PlayerAction {
	t.Helper()
	out := make([]*payload.PlayerAction, 0, len(p.published))
	for _, pub := range p.published {
		var envelope struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(pub.data, &envelope); err != nil {
			t.Fatalf("the published bytes are not a BaseMessage envelope: %v", err)
		}
		var action payload.PlayerAction
		if err := json.Unmarshal(envelope.Payload, &action); err != nil {
			t.Fatalf("the envelope payload is not a player action: %v", err)
		}
		out = append(out, &action)
	}
	return out
}

func turnIDOf(turnEntityID string) string {
	parts := strings.Split(turnEntityID, ".")
	return parts[len(parts)-1]
}

func turnEntity(turnID string) string { return testOrg + ".turn." + turnID }

// ---------------------------------------------------------------- harness

type harness struct {
	gateway   *gateway.Gateway
	store     *fakeStore
	publisher *fakePublisher
}

func newHarness(t *testing.T, opts ...gateway.Option) *harness {
	t.Helper()
	store := newFakeStore()
	publisher := &fakePublisher{}
	roster, err := gateway.NewRoster(map[string]string{
		testCredential:   testPlayerID,
		"alex-local-key": testOtherID,
	})
	if err != nil {
		t.Fatalf("NewRoster: %v", err)
	}
	options := append([]gateway.Option{
		gateway.WithClock(func() time.Time { return testTime }),
		gateway.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}, opts...)
	gw, err := gateway.New(
		roster, store, publisher,
		gateway.Config{CampaignID: testCampaignID, SceneID: testSceneID},
		options...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{gateway: gw, store: store, publisher: publisher}
}

func conn(id string) gateway.Connection {
	return gateway.Connection{ID: id, Adapter: vocabulary.AdapterWebSocket, ReplyTo: id}
}

func (h *harness) authenticate(t *testing.T, credential, connID string) *gateway.Session {
	t.Helper()
	session, err := h.gateway.Authenticate(t.Context(), credential, conn(connID))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return session
}

func submission(key, text string) []byte {
	return []byte(fmt.Sprintf(
		`{"protocol":%q,"text":%q,"idempotency_key":%q}`, payload.PlayerProtocolV1, text, key))
}

func (h *harness) submit(t *testing.T, connID string, raw []byte) *payload.SubmitResponse {
	t.Helper()
	response, err := h.gateway.Submit(t.Context(), connID, raw)
	if err != nil {
		t.Fatalf("Submit reported an operator failure: %v", err)
	}
	if response == nil {
		t.Fatal("Submit returned no response; the client would be told nothing")
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("the gateway produced a response that fails its own contract: %v", err)
	}
	return response
}

func (h *harness) accept(t *testing.T, connID string, raw []byte) *payload.SubmitResponse {
	t.Helper()
	response := h.submit(t, connID, raw)
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the submission was refused: %+v", response.Refusal)
	}
	return response
}

func (h *harness) refusal(t *testing.T, connID string, raw []byte) *payload.SubmitRefusal {
	t.Helper()
	response := h.submit(t, connID, raw)
	if response.Status != payload.StatusRefused {
		t.Fatalf("the submission was accepted as %s; a refusal was expected", response.ActionID)
	}
	return response.Refusal
}

// ---------------------------------------------------------------- identity

func TestAuthenticate_BindsAConnectionToThePlayerEntity(t *testing.T) {
	h := newHarness(t)

	session := h.authenticate(t, testCredential, testConnID)
	if session.PlayerID != testPlayerID {
		t.Fatalf("session player = %q, want %q", session.PlayerID, testPlayerID)
	}
	if session.Connection.ID != testConnID {
		t.Fatalf("session connection = %q, want %q", session.Connection.ID, testConnID)
	}
}

// The requirement the whole package exists for: identity is not a connection.
func TestAuthenticate_ReconnectingOnANewConnectionIsTheSamePlayer(t *testing.T) {
	h := newHarness(t)

	first := h.authenticate(t, testCredential, "conn-1")
	accepted := h.accept(t, "conn-1", submission("key-1", "I open the gate."))

	// The socket drops.
	h.gateway.Disconnect("conn-1")
	if _, err := h.gateway.Submit(t.Context(), "conn-1", submission("key-2", "again")); err != nil {
		t.Fatalf("Submit after disconnect: %v", err)
	}

	second := h.authenticate(t, testCredential, "conn-2")
	if second.PlayerID != first.PlayerID {
		t.Fatalf("the reconnect authenticated to %q, the first connection to %q; identity followed the socket",
			second.PlayerID, first.PlayerID)
	}

	// Finish the first turn so the gate lets the reconnected player act.
	h.store.putTurn(turnEntity(accepted.TurnID), vocabulary.PhaseComplete)
	h.store.pointAt(t, testPlayerID, turnEntity(accepted.TurnID))

	after := h.accept(t, "conn-2", submission("key-2", "I step through."))
	actions := h.publisher.actions(t)
	if len(actions) != 2 {
		t.Fatalf("two turns produced %d published actions", len(actions))
	}
	for i, action := range actions {
		if action.PlayerID != testPlayerID {
			t.Fatalf("action %d carries player %q, want the durable entity %q", i, action.PlayerID, testPlayerID)
		}
	}
	// The connection DID change on the wire, which is what makes the assertion
	// above non-vacuous: reply-to moved and identity did not.
	if actions[0].Channel.ReplyTo == actions[1].Channel.ReplyTo {
		t.Fatal("both actions carry the same reply-to; the reconnect did not actually change connection, " +
			"so 'identity survived it' proves nothing")
	}
	if after.ActionID == accepted.ActionID {
		t.Fatal("two distinct idempotency keys derived one action id")
	}
}

func TestAuthenticate_RefusesACredentialThisWorldDoesNotKnow(t *testing.T) {
	h := newHarness(t)

	_, err := h.gateway.Authenticate(t.Context(), "not-a-credential", conn(testConnID))
	if !errors.Is(err, gateway.ErrUnauthenticated) {
		t.Fatalf("an unknown credential produced %v, want ErrUnauthenticated", err)
	}
	if _, ok := h.gateway.Session(testConnID); ok {
		t.Fatal("a refused authentication still bound a session to the connection")
	}
}

// "player_id is a graph entity" is a READ, not a naming convention.
func TestAuthenticate_RefusesAPlayerThatIsNotARealEntity(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(*fakeStore)
	}{
		{
			name:   "the player was never imported",
			break_: func(s *fakeStore) { delete(s.entities, testPlayerID) },
		},
		{
			name: "the player is only a referential stub",
			break_: func(s *fakeStore) {
				s.entities[testPlayerID] = stubEntity(testPlayerID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			test.break_(h.store)

			_, err := h.gateway.Authenticate(t.Context(), testCredential, conn(testConnID))
			if !errors.Is(err, gateway.ErrUnauthenticated) {
				t.Fatalf("authentication produced %v, want ErrUnauthenticated", err)
			}
		})
	}
}

// Anti-vacuity for the stub case above: the fixture the test calls "a stub" must
// actually read as one, or the refusal could be coming from anywhere.
func TestAuthenticate_TheStubFixtureIsReallyAStub(t *testing.T) {
	if !stubEntity(testPlayerID).IsStub() {
		t.Fatal("the fixture the stub test uses does not read as a referential stub, so that test proves nothing")
	}
	if newFakeStore().entities[testPlayerID].IsStub() {
		t.Fatal("the healthy player fixture reads as a stub, so the refusal above would fire for any player")
	}
}

// stubEntity is what graph-ingest materializes at a referenced-but-not-yet-born
// id. IsStub keys on the ENVELOPE — the marker triple persists after real birth
// — so a fixture that carried only the marker would not be a stub at all.
func stubEntity(id string) *graph.EntityState {
	return &graph.EntityState{
		ID:          id,
		MessageType: graph.StubMessageType,
		Triples: []message.Triple{{
			Subject:   id,
			Predicate: graph.PredStubMarker,
			Object:    true,
			Source:    "graph-ingest",
			Timestamp: testTime,
		}},
	}
}

func TestAuthenticate_RefusesAConnectionItCannotBind(t *testing.T) {
	h := newHarness(t)

	for _, bad := range []gateway.Connection{
		{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "x"},
		{ID: "c", ReplyTo: "x"},
		{ID: "c", Adapter: vocabulary.ChannelAdapter("carrier-pigeon"), ReplyTo: "x"},
		{ID: "c", Adapter: vocabulary.AdapterWebSocket},
	} {
		if _, err := h.gateway.Authenticate(t.Context(), testCredential, bad); err == nil {
			t.Fatalf("connection %+v was bound to a session", bad)
		}
	}
}

func TestSubmit_RefusesAConnectionWithNoSession(t *testing.T) {
	h := newHarness(t)

	response, err := h.gateway.Submit(t.Context(), testConnID, submission("key-1", "I open the gate."))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if response.Refusal == nil || response.Refusal.Code != payload.RefusalUnauthenticated {
		t.Fatalf("an unauthenticated submission was answered %+v", response)
	}
	if len(h.publisher.published) != 0 {
		t.Fatal("an unauthenticated submission published an action")
	}
}

// ---------------------------------------------------------------- the stamp

func TestSubmit_StampsEveryServerOwnedFieldOnTheCanonicalAction(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	accepted := h.accept(t, testConnID, submission("key-1", "I lever the gate open."))

	actions := h.publisher.actions(t)
	if len(actions) != 1 {
		t.Fatalf("one submission published %d actions", len(actions))
	}
	action := actions[0]

	if action.PlayerID != testPlayerID {
		t.Errorf("player_id = %q, want the session's %q", action.PlayerID, testPlayerID)
	}
	if action.CampaignID != testCampaignID {
		t.Errorf("campaign_id = %q, want %q", action.CampaignID, testCampaignID)
	}
	if action.SceneID != testSceneID {
		t.Errorf("scene_id = %q, want %q", action.SceneID, testSceneID)
	}
	if !action.ArrivedAt.Equal(testTime) {
		t.Errorf("arrived_at = %s, want the GATEWAY's clock %s; a client that could stamp it could arrive "+
			"before a deadline it had already missed", action.ArrivedAt, testTime)
	}
	if action.Channel.Adapter != vocabulary.AdapterWebSocket || action.Channel.ReplyTo != testConnID {
		t.Errorf("channel = %+v, want the session's connection binding", action.Channel)
	}
	if action.Text != "I lever the gate open." {
		t.Errorf("text = %q; the one field the client owns was not carried through", action.Text)
	}
	if action.ActionID != accepted.ActionID {
		t.Errorf("published action id %q, answered %q", action.ActionID, accepted.ActionID)
	}
	if err := action.Validate(); err != nil {
		t.Errorf("the published action does not satisfy the engine's own contract: %v", err)
	}
	if accepted.ArrivedAt.IsZero() || !accepted.ArrivedAt.Equal(action.ArrivedAt) {
		t.Errorf("the answer reports arrival %s, the action carries %s; the player must be told the time "+
			"the engine will judge them by", accepted.ArrivedAt, action.ArrivedAt)
	}
}

func TestSubmit_PublishesOnTheCanonicalSubjectUnderTheActionIDAsMessageID(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	accepted := h.accept(t, testConnID, submission("key-1", "I open the gate."))

	pub := h.publisher.published[0]
	if pub.subject != turn.ActionSubject {
		t.Errorf("published on %q, want the canonical %q", pub.subject, turn.ActionSubject)
	}
	if pub.msgID != accepted.ActionID {
		t.Errorf("Nats-Msg-Id = %q, want the derived action id %q; without it a re-publish inside the "+
			"duplicate window stores a second message", pub.msgID, accepted.ActionID)
	}
}

// The envelope is what intake's registry-bound decoder requires. A bare payload
// would decode to nothing and the intake would terminate the delivery.
func TestSubmit_PublishesThroughTheEnvelopeIntakeDecodes(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.accept(t, testConnID, submission("key-1", "I open the gate."))

	var envelope struct {
		Type struct {
			Domain   string `json:"domain"`
			Category string `json:"category"`
			Version  string `json:"version"`
		} `json:"type"`
		Meta struct {
			Source string `json:"source"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(h.publisher.published[0].data, &envelope); err != nil {
		t.Fatalf("the published bytes are not a BaseMessage: %v", err)
	}
	if envelope.Type.Domain != payload.Domain ||
		envelope.Type.Category != payload.CategoryPlayerAction ||
		envelope.Type.Version != payload.SchemaVersion {
		t.Fatalf("the envelope declares %+v, which the payload registry would not resolve to a player action",
			envelope.Type)
	}
	if envelope.Meta.Source != gateway.Source {
		t.Errorf("envelope source = %q, want %q", envelope.Meta.Source, gateway.Source)
	}
}

// ---------------------------------------------------------------- refusals

func TestSubmit_RefusesAServerOwnedFieldByName(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	for _, field := range []string{"player_id", "action_id", "arrived_at", "campaign_id", "scene_id", "reply_to"} {
		t.Run(field, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(
				`{"protocol":%q,"text":"I open the gate.","idempotency_key":"key-1",%q:"anything"}`,
				payload.PlayerProtocolV1, field))

			refusal := h.refusal(t, testConnID, raw)
			if refusal.Code != payload.RefusalServerOwnedField {
				t.Fatalf("refusal code = %q, want %q", refusal.Code, payload.RefusalServerOwnedField)
			}
			if refusal.Field != field {
				t.Fatalf("refusal names field %q, want %q", refusal.Field, field)
			}
			if len(h.publisher.published) != 0 {
				t.Fatal("a request carrying a server-owned field still published an action")
			}
		})
	}
}

func TestSubmit_RefusesEveryMalformedRequestWithItsOwnCode(t *testing.T) {
	tests := []struct {
		name  string
		raw   []byte
		code  payload.SubmitRefusalCode
		field string
	}{
		{
			name: "no protocol version",
			raw:  []byte(`{"text":"I open the gate.","idempotency_key":"key-1"}`),
			code: payload.RefusalUnsupportedProtocol,
		},
		{
			name: "a protocol version this engine does not speak",
			raw:  []byte(`{"protocol":"player/v9","text":"go","idempotency_key":"key-1"}`),
			code: payload.RefusalUnsupportedProtocol,
		},
		{
			name:  "a field this protocol does not define",
			raw:   []byte(`{"protocol":"player/v1","text":"go","idempotency_key":"k","mood":"bold"}`),
			code:  payload.RefusalUnknownField,
			field: "mood",
		},
		{
			name: "not JSON at all",
			raw:  []byte(`not json`),
			code: payload.RefusalMalformedRequest,
		},
		{
			name: "a JSON null",
			raw:  []byte(`null`),
			code: payload.RefusalMalformedRequest,
		},
		{
			name:  "no action text",
			raw:   submission("key-1", ""),
			code:  payload.RefusalInvalidField,
			field: "text",
		},
		{
			name:  "an action of nothing but whitespace",
			raw:   submission("key-1", "   \t "),
			code:  payload.RefusalInvalidField,
			field: "text",
		},
		{
			name:  "no idempotency key",
			raw:   submission("", "I open the gate."),
			code:  payload.RefusalInvalidField,
			field: "idempotency_key",
		},
		{
			name:  "an idempotency key past the budget",
			raw:   submission(strings.Repeat("k", payload.MaxIdempotencyKeyBytes+1), "go"),
			code:  payload.RefusalInvalidField,
			field: "idempotency_key",
		},
		{
			name:  "action text past the budget",
			raw:   submission("key-1", strings.Repeat("a", payload.MaxActionTextBytes+1)),
			code:  payload.RefusalInvalidField,
			field: "text",
		},
		{
			name: "a frame past the request budget",
			raw:  submission("key-1", strings.Repeat("a", gateway.MaxRequestBytes)),
			code: payload.RefusalMalformedRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.authenticate(t, testCredential, testConnID)

			refusal := h.refusal(t, testConnID, test.raw)
			if refusal.Code != test.code {
				t.Fatalf("refusal code = %q (%s), want %q", refusal.Code, refusal.Message, test.code)
			}
			if test.field != "" && refusal.Field != test.field {
				t.Fatalf("refusal names field %q, want %q", refusal.Field, test.field)
			}
			if len(h.publisher.published) != 0 {
				t.Fatal("a refused submission still published an action")
			}
		})
	}
}

// The frame bound has to bite BEFORE the decoder allocates, or it is only a
// slower way of reaching the same answer.
func TestSubmit_RefusesAnOversizedFrameWithoutParsingIt(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	// Deliberately not valid JSON: if the gate parsed first, this would be
	// refused as malformed-because-unparseable and name a parse error instead.
	raw := []byte(strings.Repeat("x", gateway.MaxRequestBytes+1))

	refusal := h.refusal(t, testConnID, raw)
	if refusal.Code != payload.RefusalMalformedRequest {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, payload.RefusalMalformedRequest)
	}
	if !strings.Contains(refusal.Message, "request budget") {
		t.Fatalf("the refusal %q does not report the size gate; the frame was parsed first", refusal.Message)
	}
}

func TestSubmit_EchoesTheIdempotencyKeyOnARefusalItCouldRead(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.store.putTurn(turnEntity("turn-live"), vocabulary.PhaseAdjudicating)
	h.store.pointAt(t, testPlayerID, turnEntity("turn-live"))

	response := h.submit(t, testConnID, submission("key-echo", "I open the gate."))
	if response.IdempotencyKey != "key-echo" {
		t.Fatalf("the refusal echoes key %q, want %q; a client cannot match an answer to a submission "+
			"without it, and a refused submission has no action id to match on",
			response.IdempotencyKey, "key-echo")
	}
}

// A refused request never became a SubmitAction — the refusal happens on the
// object's key set, before any value is mapped — so the key has to be recovered
// from the raw bytes, and VALIDATED before it is written back out. This is the
// one value the engine takes from an untrusted request and echoes.
func TestSubmit_EchoesTheKeyOfARequestThatNeverParsed(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	raw := []byte(fmt.Sprintf(
		`{"protocol":%q,"text":"go","idempotency_key":"key-recoverable","player_id":"x"}`,
		payload.PlayerProtocolV1))

	response := h.submit(t, testConnID, raw)
	if response.Status != payload.StatusRefused {
		t.Fatalf("the request was accepted: %+v", response)
	}
	if response.IdempotencyKey != "key-recoverable" {
		t.Fatalf("the refusal echoes %q, want %q; a client on an asynchronous transport cannot match this "+
			"answer to a submission any other way, because a refused submission has no action id",
			response.IdempotencyKey, "key-recoverable")
	}
}

func TestSubmit_RefusesToEchoAKeyThatIsNotAName(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	for _, name := range []string{"control bytes", "past the budget"} {
		key := "key\u0007bell"
		if name == "past the budget" {
			key = strings.Repeat("k", payload.MaxIdempotencyKeyBytes+1)
		}
		raw := []byte(fmt.Sprintf(
			`{"protocol":%q,"text":"go","idempotency_key":"%s","player_id":"x"}`,
			payload.PlayerProtocolV1, key))

		response := h.submit(t, testConnID, raw)
		if response.IdempotencyKey != "" {
			t.Fatalf("%s: the refusal echoed %q; the key is written into the answer and into every "+
				"operator diagnostic that quotes it, so an unvalidated echo is an injection",
				name, response.IdempotencyKey)
		}
	}
}

// ---------------------------------------------------------------- action ids

func TestActionIDFor_IsDeterministicAndPartitionedByPlayer(t *testing.T) {
	const key = "shared-idempotency-key"

	mine, err := gateway.ActionIDFor(testPlayerID, key)
	if err != nil {
		t.Fatalf("ActionIDFor: %v", err)
	}
	again, err := gateway.ActionIDFor(testPlayerID, key)
	if err != nil {
		t.Fatalf("ActionIDFor: %v", err)
	}
	if mine != again {
		t.Fatalf("the same (player, key) derived %q then %q; a resubmission would take a second turn",
			mine, again)
	}

	theirs, err := gateway.ActionIDFor(testOtherID, key)
	if err != nil {
		t.Fatalf("ActionIDFor: %v", err)
	}
	if theirs == mine {
		t.Fatal("two players choosing the same idempotency key derived ONE action id; either could then " +
			"pre-claim the other's turn and have it absorbed as a duplicate")
	}
}

// The framing test. Unframed concatenation makes these two inputs one preimage,
// which is the pre-claim attack arriving by arithmetic instead of by design.
func TestActionIDFor_LengthFramingStopsTheRepartitionCollision(t *testing.T) {
	// Two (player, key) pairs whose naive concatenations are byte-identical:
	// the boundary between the entity id and the key moves by one character.
	first, err := gateway.ActionIDFor(testOrg+".player.pa", "tkey")
	if err != nil {
		t.Fatalf("ActionIDFor: %v", err)
	}
	second, err := gateway.ActionIDFor(testOrg+".player.pat", "key")
	if err != nil {
		t.Fatalf("ActionIDFor: %v", err)
	}
	if first == second {
		t.Fatal("two different (player, key) pairs sharing a concatenation derived one action id; the " +
			"inputs are not length-framed")
	}
}

func TestActionIDFor_ProducesAUsableTurnIdentity(t *testing.T) {
	actionID, err := gateway.ActionIDFor(testPlayerID, "key-1")
	if err != nil {
		t.Fatalf("ActionIDFor: %v", err)
	}
	if err := payload.RequireActionID(actionID); err != nil {
		t.Fatalf("the derived action id %q is not usable as a turn identity: %v", actionID, err)
	}
	turnID := payload.TurnIDForAction(actionID)
	if err := vocabulary.ValidateIDSegment(turnID); err != nil {
		t.Fatalf("the derived turn id %q is not a legal entity-ID segment: %v", turnID, err)
	}
	if _, err := vocabulary.ComposeEntityID("c360", "world1", "starter", "turn", turnID); err != nil {
		t.Fatalf("the derived turn id does not compose a turn entity: %v", err)
	}
}

// Both inputs face their own FULL contract here, not one of them. The key is
// echoed back to the client on a refusal and quoted in operator diagnostics, so
// a derivation that accepted anything non-empty would be a second door into the
// value payload.RequireIdempotencyKey exists to hold.
func TestActionIDFor_RefusesInputsItCannotReproduce(t *testing.T) {
	for _, test := range []struct {
		name        string
		player, key string
	}{
		{"no player", "", "key"},
		{"a player that is not an entity id", "pat", "key"},
		{"a player id with too few positions", "c360.semmachina.world1.player.pat", "key"},
		{"no key", testOrg + ".player.pat", ""},
		{"a key past the budget", testOrg + ".player.pat",
			strings.Repeat("k", payload.MaxIdempotencyKeyBytes+1)},
		{"a key carrying a control character", testOrg + ".player.pat", "key\x07bell"},
		{"a key that is not valid UTF-8", testOrg + ".player.pat", "key\xff"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := gateway.ActionIDFor(test.player, test.key); err == nil {
				t.Fatalf("ActionIDFor(%q, %q) produced an id", test.player, test.key)
			}
		})
	}
}

// ---------------------------------------------------------------- admission

func TestSubmit_RefusesASecondDistinctActionWhileATurnIsLive(t *testing.T) {
	for _, phase := range vocabulary.TurnPhases() {
		if phase.IsTerminal() {
			continue
		}
		t.Run(string(phase), func(t *testing.T) {
			h := newHarness(t)
			h.authenticate(t, testCredential, testConnID)
			live := turnEntity("turn-live")
			h.store.putTurn(live, phase)
			h.store.pointAt(t, testPlayerID, live)

			refusal := h.refusal(t, testConnID, submission("key-2", "I run for the door."))
			if refusal.Code != payload.RefusalTurnInProgress {
				t.Fatalf("refusal code = %q (%s), want %q",
					refusal.Code, refusal.Message, payload.RefusalTurnInProgress)
			}
			if refusal.ActiveTurnID != "turn-live" {
				t.Fatalf("the refusal names active turn %q, want %q", refusal.ActiveTurnID, "turn-live")
			}
			if strings.Contains(refusal.ActiveTurnID, ".") {
				t.Fatalf("the refusal hands the client the entity id %q rather than the turn id",
					refusal.ActiveTurnID)
			}
			if len(h.publisher.published) != 0 {
				t.Fatal("a refused submission still published a second action")
			}
		})
	}
}

func TestSubmit_AdmitsOnceTheTurnHasEnded(t *testing.T) {
	for _, phase := range []vocabulary.TurnPhase{vocabulary.PhaseComplete, vocabulary.PhaseFailed} {
		t.Run(string(phase), func(t *testing.T) {
			h := newHarness(t)
			h.authenticate(t, testCredential, testConnID)
			ended := turnEntity("turn-ended")
			h.store.putTurn(ended, phase)
			h.store.pointAt(t, testPlayerID, ended)

			h.accept(t, testConnID, submission("key-2", "I run for the door."))
		})
	}
}

func TestSubmit_APlayerWhoHasNeverActedIsAdmitted(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	h.accept(t, testConnID, submission("key-1", "I open the gate."))
}

// A resubmission of the SAME key is not a conflict with itself. It resolves to
// the same action id and the same turn, so it is republished — idempotent at
// intake — rather than refused, which also heals a first publish that was lost.
func TestSubmit_AResubmissionOfTheHeldTurnIsNotRefused(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	first := h.accept(t, testConnID, submission("key-1", "I open the gate."))

	// Intake has since created the turn and the recorder pointed the player at
	// it; the turn is still running.
	live := turnEntity(first.TurnID)
	h.store.putTurn(live, vocabulary.PhaseAdjudicating)
	h.store.pointAt(t, testPlayerID, live)

	second := h.accept(t, testConnID, submission("key-1", "I open the gate."))
	if second.ActionID != first.ActionID || second.TurnID != first.TurnID {
		t.Fatalf("the resubmission resolved to action %s / turn %s, the first to %s / %s",
			second.ActionID, second.TurnID, first.ActionID, first.TurnID)
	}
	if len(h.publisher.published) != 2 {
		t.Fatalf("the resubmission published %d actions in total; it must republish so a lost first "+
			"publish heals", len(h.publisher.published))
	}
	if h.publisher.published[0].msgID != h.publisher.published[1].msgID {
		t.Fatal("the two publishes carry different message ids, so the duplicate window cannot collapse them")
	}
}

// One player's live turn must not block another's action.
func TestSubmit_OnePlayersLiveTurnDoesNotBlockAnother(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, "conn-pat")
	h.authenticate(t, "alex-local-key", "conn-alex")

	live := turnEntity("turn-live")
	h.store.putTurn(live, vocabulary.PhaseAdjudicating)
	h.store.pointAt(t, testPlayerID, live)

	if refusal := h.refusal(t, "conn-pat", submission("k", "again")); refusal.Code !=
		payload.RefusalTurnInProgress {
		t.Fatalf("the turn holder was not refused: %+v", refusal)
	}
	h.accept(t, "conn-alex", submission("k", "I watch from the doorway."))
}

// Fail-open, deliberately: a pointer at a turn nothing can find is a graph
// anomaly, and refusing would lock that player out of their campaign forever for
// a fault they cannot see and cannot clear.
func TestSubmit_APointerAtAVanishedTurnAdmitsRatherThanLockingThePlayerOut(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.store.pointAt(t, testPlayerID, turnEntity("turn-deleted"))

	h.accept(t, testConnID, submission("key-2", "I try the door again."))
}

// The append-lane anomaly needs no policy choice: check every pointer, and
// refuse if any names a turn that has not ended.
func TestSubmit_SeveralPointersAreAllCheckedRatherThanTheFirst(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)

	ended := turnEntity("turn-ended")
	live := turnEntity("turn-live")
	h.store.putTurn(ended, vocabulary.PhaseComplete)
	h.store.putTurn(live, vocabulary.PhaseNarrating)
	h.store.appendPointer(t, testPlayerID, ended)
	h.store.appendPointer(t, testPlayerID, live)

	// Anti-vacuity: the fixture must really hold two pointers, or "all are
	// checked" is a claim about one.
	state, err := h.store.GetEntity(t.Context(), testPlayerID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	pointers := 0
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.PlayerTurnCurrent.String() {
			pointers++
		}
	}
	if pointers != 2 {
		t.Fatalf("the fixture holds %d pointers, want 2; the test would pass on a reader that takes the first",
			pointers)
	}

	refusal := h.refusal(t, testConnID, submission("key-2", "I run."))
	if refusal.ActiveTurnID != "turn-live" {
		t.Fatalf("the gate named %q, want the LIVE turn; a reader taking the first pointer would have "+
			"admitted on the completed one", refusal.ActiveTurnID)
	}
}

// A pointer whose object is not a turn entity id at all is unreachable through
// the projection, which admits only a string — and it is handled anyway, because
// "ignored" plus "silent" is how a player ends up holding two live turns with
// nothing anywhere saying why.
func TestSubmit_AnUnreadablePointerIsReportedAndDoesNotLockThePlayerOut(t *testing.T) {
	logs := &captureHandler{}
	h := newHarness(t, gateway.WithLogger(slog.New(logs)))
	h.authenticate(t, testCredential, testConnID)
	h.store.entities[testPlayerID].Triples = append(h.store.entities[testPlayerID].Triples, message.Triple{
		Subject:   testPlayerID,
		Predicate: vocabulary.PlayerTurnCurrent.String(),
		Object:    42,
		Source:    "something-that-should-not-exist",
		Timestamp: testTime,
	})

	// Fail-open: an unreadable pointer is not a live turn, so it must not lock
	// the player out of their own campaign.
	h.accept(t, testConnID, submission("key-1", "I open the gate."))

	if !logs.sawWarning("unreadable") {
		t.Fatalf("nothing reported the unreadable pointer; the gate ignored a fact about this player and "+
			"said so nowhere. Records: %v", logs.records)
	}
}

// And an unreadable pointer beside a live one must not hide the live one.
func TestSubmit_AnUnreadablePointerDoesNotHideALiveTurn(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	live := turnEntity("turn-live")
	h.store.putTurn(live, vocabulary.PhaseNarrating)
	h.store.entities[testPlayerID].Triples = append(h.store.entities[testPlayerID].Triples, message.Triple{
		Subject:   testPlayerID,
		Predicate: vocabulary.PlayerTurnCurrent.String(),
		Object:    42,
		Timestamp: testTime,
	})
	h.store.appendPointer(t, testPlayerID, live)

	refusal := h.refusal(t, testConnID, submission("key-2", "I run."))
	if refusal.ActiveTurnID != "turn-live" {
		t.Fatalf("the gate named %q, want the live turn", refusal.ActiveTurnID)
	}
}

// captureHandler records what the gateway told the operator.
type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) sawWarning(substring string) bool {
	for _, record := range h.records {
		if record.Level < slog.LevelWarn {
			continue
		}
		if strings.Contains(record.Message, substring) {
			return true
		}
		found := false
		record.Attrs(func(attr slog.Attr) bool {
			if strings.Contains(attr.Key, substring) {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// A corrupt turn record is the one admission failure that is NOT fail-open: the
// phase is the fact the turn design rests on, and guessing would start a second
// turn on top of one that may still be running.
func TestSubmit_ACorruptTurnRecordIsRefusedRatherThanGuessedAt(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*graph.EntityState)
	}{
		{
			name:    "no phase at all",
			corrupt: func(s *graph.EntityState) { s.Triples = nil },
		},
		{
			name: "two phases, the signature of an appending write",
			corrupt: func(s *graph.EntityState) {
				second := s.Triples[0]
				second.Object = string(vocabulary.PhaseComplete)
				s.Triples = append(s.Triples, second)
			},
		},
		{
			name:    "a phase outside the closed set",
			corrupt: func(s *graph.EntityState) { s.Triples[0].Object = "adjudicatin" },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.authenticate(t, testCredential, testConnID)
			live := turnEntity("turn-live")
			h.store.putTurn(live, vocabulary.PhaseAdjudicating)
			test.corrupt(h.store.entities[live])
			h.store.pointAt(t, testPlayerID, live)

			response, err := h.gateway.Submit(t.Context(), testConnID, submission("key-2", "I run."))
			if err == nil {
				t.Fatal("a corrupt turn record produced no operator error, so nothing would be diagnosed")
			}
			if response.Refusal == nil || response.Refusal.Code != payload.RefusalUnavailable {
				t.Fatalf("the client was answered %+v, want an unavailable refusal", response.Refusal)
			}
			if len(h.publisher.published) != 0 {
				t.Fatal("a submission the gate could not judge still published an action")
			}
		})
	}
}

// The admission read is TWO reads, not a history scan. That is the entire reason
// the pointer exists, and a regression to an incoming-edge walk would be
// invisible in every behavioural assertion above.
func TestSubmit_AdmissionCostsTwoReads(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	live := turnEntity("turn-live")
	h.store.putTurn(live, vocabulary.PhaseComplete)
	h.store.pointAt(t, testPlayerID, live)

	h.store.reads = nil
	h.accept(t, testConnID, submission("key-2", "I step through."))

	if len(h.store.reads) != 2 {
		t.Fatalf("admission issued %d reads (%v), want exactly two: the player, then the turn they name",
			len(h.store.reads), h.store.reads)
	}
	if h.store.reads[0] != testPlayerID || h.store.reads[1] != live {
		t.Fatalf("admission read %v, want [%s %s]", h.store.reads, testPlayerID, live)
	}
}

// ---------------------------------------------------------------- failures

func TestSubmit_AFailedPublishIsARetryableRefusalThatLeaksNothing(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.publisher.err = errors.New("nats: no responders available on ENTITY_STATES bucket")

	response, err := h.gateway.Submit(t.Context(), testConnID, submission("key-1", "I open the gate."))
	if err == nil {
		t.Fatal("a failed publish produced no operator error")
	}
	if response.Refusal == nil || response.Refusal.Code != payload.RefusalUnavailable {
		t.Fatalf("the client was answered %+v, want an unavailable refusal", response.Refusal)
	}
	if strings.Contains(response.Refusal.Message, "ENTITY_STATES") ||
		strings.Contains(response.Refusal.Message, "no responders") {
		t.Fatalf("the client's refusal repeats the engine's error: %q; a submission surface is exactly "+
			"where a curious client would probe for infrastructure names", response.Refusal.Message)
	}
	if !strings.Contains(err.Error(), "no responders") {
		t.Fatalf("the operator's error lost the cause: %v", err)
	}
}

func TestSubmit_AnUnreachableGraphIsARefusalAndNotAPublishedAction(t *testing.T) {
	h := newHarness(t)
	h.authenticate(t, testCredential, testConnID)
	h.store.err[testPlayerID] = errors.New("nats: timeout")

	response, err := h.gateway.Submit(t.Context(), testConnID, submission("key-1", "I open the gate."))
	if err == nil {
		t.Fatal("an unreachable graph produced no operator error")
	}
	if response.Refusal == nil || response.Refusal.Code != payload.RefusalUnavailable {
		t.Fatalf("the client was answered %+v, want an unavailable refusal", response.Refusal)
	}
	if len(h.publisher.published) != 0 {
		t.Fatal("an action was published while the admission gate could not answer")
	}
}

func TestNew_RefusesAGatewayThatCannotDoItsJob(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	roster, err := gateway.NewRoster(map[string]string{testCredential: testPlayerID})
	if err != nil {
		t.Fatalf("NewRoster: %v", err)
	}
	config := gateway.Config{CampaignID: testCampaignID, SceneID: testSceneID}

	cases := []struct {
		name string
		call func() (*gateway.Gateway, error)
	}{
		{"no authenticator", func() (*gateway.Gateway, error) {
			return gateway.New(nil, store, publisher, config)
		}},
		{"no store", func() (*gateway.Gateway, error) {
			return gateway.New(roster, nil, publisher, config)
		}},
		{"no publisher", func() (*gateway.Gateway, error) {
			return gateway.New(roster, store, nil, config)
		}},
		{"a campaign id that is not an entity", func() (*gateway.Gateway, error) {
			return gateway.New(roster, store, publisher,
				gateway.Config{CampaignID: "campaign", SceneID: testSceneID})
		}},
		{"a scene id that is not an entity", func() (*gateway.Gateway, error) {
			return gateway.New(roster, store, publisher,
				gateway.Config{CampaignID: testCampaignID, SceneID: "gate"})
		}},
		{"no publish subject", func() (*gateway.Gateway, error) {
			return gateway.New(roster, store, publisher, config, gateway.WithSubject(""))
		}},
		{"no clock", func() (*gateway.Gateway, error) {
			return gateway.New(roster, store, publisher, config, gateway.WithClock(nil))
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.call(); err == nil {
				t.Fatal("the gateway was built")
			}
		})
	}
}

func TestNewRoster_RefusesConfigurationThatWouldAuthenticateNobodyOrAGhost(t *testing.T) {
	cases := []struct {
		name    string
		entries map[string]string
	}{
		{"no entries", map[string]string{}},
		{"an empty credential", map[string]string{"": testPlayerID}},
		{"a player that is not an entity id", map[string]string{"c": "pat"}},
		{"a player id with too few positions", map[string]string{"c": "c360.semmachina.world1.player.pat"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := gateway.NewRoster(test.entries); err == nil {
				t.Fatal("the roster was built")
			}
		})
	}
}

// ---------------------------------------------------------------- pacing

// No turn-processing step may degrade a turn because of elapsed wall-clock time.
// A gateway is the easiest place to introduce one by accident.
func TestSubmit_AnArbitrarilyLongGapBetweenTurnsChangesNothing(t *testing.T) {
	now := testTime
	h := newHarness(t, gateway.WithClock(func() time.Time { return now }))
	h.authenticate(t, testCredential, testConnID)

	first := h.accept(t, testConnID, submission("key-1", "I open the gate."))
	h.store.putTurn(turnEntity(first.TurnID), vocabulary.PhaseComplete)
	h.store.pointAt(t, testPlayerID, turnEntity(first.TurnID))

	// Four years later, on the same session.
	now = testTime.AddDate(4, 0, 0)
	second := h.accept(t, testConnID, submission("key-2", "I step through."))

	if !second.ArrivedAt.Equal(now) {
		t.Fatalf("the late action arrived at %s, want %s", second.ArrivedAt, now)
	}
	actions := h.publisher.actions(t)
	if len(actions) != 2 {
		t.Fatalf("the late submission published %d actions in total", len(actions))
	}
	if !actions[1].ArrivedAt.Equal(now) {
		t.Fatalf("the published late action carries %s, want %s", actions[1].ArrivedAt, now)
	}
	if actions[0].SceneID != actions[1].SceneID || actions[0].CampaignID != actions[1].CampaignID {
		t.Fatal("the late action was composed differently from the prompt one")
	}
}
