package content_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c360studio/semstreams/storage"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testOrg      = "testorg"
	testNS       = "testns"
	testTemplate = "starter"
)

func compose(t *testing.T, kind, instance string) string {
	t.Helper()
	id, err := vocabulary.ComposeEntityID(testOrg, testNS, testTemplate, kind, instance)
	if err != nil {
		t.Fatalf("compose %s/%s: %v", kind, instance, err)
	}
	return id
}

func testAction(t *testing.T, actionID string) *payload.PlayerAction {
	t.Helper()
	return &payload.PlayerAction{
		ActionID:   actionID,
		PlayerID:   compose(t, "player", "p1"),
		CampaignID: compose(t, "campaign", "main"),
		SceneID:    compose(t, "scene", "gatehouse"),
		Text:       "I lever the gate open with the crowbar.",
		ArrivedAt:  time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC),
		Channel:    payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "conn-7"},
	}
}

// memoryBackend is an in-memory Backend. It exists for the contract tests
// (grammar, key derivation, refusals) that have nothing to do with NATS; every
// claim about DURABILITY is proved against a real ObjectStore in
// content_integration_test.go, because a map cannot lose data in any of the
// ways this package exists to survive.
type memoryBackend struct {
	instance string
	mu       sync.Mutex
	objects  map[string][]byte
	claims   map[string][]byte
	puts     map[string]int
}

func newMemoryBackend(instance string) *memoryBackend {
	return &memoryBackend{
		instance: instance,
		objects:  make(map[string][]byte),
		claims:   make(map[string][]byte),
		puts:     make(map[string]int),
	}
}

func (b *memoryBackend) ClaimExact(_ context.Context, key string, candidate []byte) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	winner, exists := b.claims[key]
	if !exists {
		winner = bytes.Clone(candidate)
		b.claims[key] = winner
	}
	resident, exists := b.objects[key]
	if !exists {
		b.objects[key] = bytes.Clone(winner)
		b.puts[key]++
	} else if !bytes.Equal(resident, winner) {
		return nil, content.ErrArtifactCorrupt
	}
	return bytes.Clone(winner), nil
}

func (b *memoryBackend) InstanceName() string { return b.instance }

func (b *memoryBackend) Put(_ context.Context, key string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored := make([]byte, len(data))
	copy(stored, data)
	b.objects[key] = stored
	b.puts[key]++
	return nil
}

func (b *memoryBackend) Get(_ context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return data, nil
}

func newTestStore(t *testing.T) (*content.Store, *memoryBackend) {
	t.Helper()
	backend := newMemoryBackend("TEST_CONTENT")
	store, err := content.NewStore(backend)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, backend
}

// ------------------------------------------------------------- the grammar

func TestRef_RoundTripsThroughItsCanonicalSpelling(t *testing.T) {
	ref := content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/turn-act-1/action"}
	text := ref.String()
	if !strings.HasPrefix(text, content.RefScheme) {
		t.Fatalf("%q does not carry the reference scheme", text)
	}

	parsed, err := content.ParseRef(text)
	if err != nil {
		t.Fatalf("ParseRef(%q): %v", text, err)
	}
	if parsed != ref {
		t.Fatalf("round trip produced %+v, want %+v", parsed, ref)
	}
}

// The empty string is how an ABSENT optional reference travels — a turn that
// failed with no detail to store. Parsing it as an error would make "no
// reference" indistinguishable from a malformed one at every call site.
func TestRef_TheEmptyStringIsTheAbsentReference(t *testing.T) {
	ref, err := content.ParseRef("")
	if err != nil {
		t.Fatalf("ParseRef(\"\"): %v", err)
	}
	if !ref.IsZero() {
		t.Fatalf("the empty string parsed to %+v, want the zero reference", ref)
	}
	if ref.String() != "" {
		t.Fatalf("the zero reference stringifies to %q, want the empty string", ref.String())
	}
	if ref.StorageReference() != nil {
		t.Fatal("the zero reference produced an upstream StorageReference; there is nothing for it to point at")
	}
}

// The scheme is the check that an object is a POINTER at all. A sentence handed
// to a reference predicate passes every shape gate between here and the graph —
// it is a bounded scalar string — so this is where it has to stop.
func TestParseRef_RefusesAnythingThatIsNotAReference(t *testing.T) {
	for _, text := range []string{
		"the batch was refused because health would go to 43",
		"turn/turn-act-1/action",
		"obj://",
		"obj://SEMMACHINA_CONTENT",
		"obj:/SEMMACHINA_CONTENT/turn/turn-act-1/action",
	} {
		if ref, err := content.ParseRef(text); err == nil {
			t.Fatalf("ParseRef(%q) accepted it as %+v", text, ref)
		}
	}
}

// The bound has to hold for the WORST case the engine can compose, not for the
// example in the doc comment: the reference is going onto a triple, and one
// discovered oversized after the object was written would leave an artifact no
// triple can ever address.
func TestKeyFor_WorstCaseReferenceFitsTheTripleBudget(t *testing.T) {
	longestTurnID := "turn-" + strings.Repeat("a", vocabulary.MaxIDSegmentBytes-len(payload.TurnIDPrefix))
	if err := vocabulary.ValidateIDSegment(longestTurnID); err != nil {
		t.Fatalf("the longest legal turn id is not legal: %v", err)
	}
	longestInstance := strings.Repeat("b", content.MaxInstanceNameBytes)

	// Over every registered subject kind, not just the turn: the budget has to
	// survive the LONGEST first segment the key shape can carry, or adding a
	// subject kind would quietly shorten the id budget of every key under it.
	for _, kind := range content.SubjectKinds() {
		for _, predicate := range vocabulary.StorageRefPredicates() {
			key, err := content.KeyFor(predicate, kind, longestTurnID)
			if err != nil {
				t.Fatalf("KeyFor(%q, %q): %v", predicate, kind, err)
			}
			ref := content.Ref{Instance: longestInstance, Key: key}
			if err := ref.Validate(); err != nil {
				t.Fatalf("the longest reference the engine can compose for %q/%q is invalid: %v",
					kind, predicate, err)
			}
			if len(ref.String()) > payload.MaxTripleObjectBytes {
				t.Fatalf("worst-case reference for %q/%q is %d bytes, over the %d-byte triple-object budget",
					kind, predicate, len(ref.String()), payload.MaxTripleObjectBytes)
			}
		}
	}
}

// The key names its SUBJECT, so the derivation a scene-scoped or arc-scoped
// artifact needs is this one with a different first segment rather than a second
// derivation beside it — and a second store one step behind that.
//
// The turn's keys are byte-identical to the ones this store has always written,
// which is what makes the shape a pre-adaptation rather than a migration.
func TestKeyFor_NamesTheSubjectAndLeavesTheTurnsKeysUnchanged(t *testing.T) {
	key, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, "turn-act-1")
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}
	if key != "turn/turn-act-1/action" {
		t.Fatalf("a turn's action key is %q; every object already stored is at the old spelling", key)
	}

	if _, err := content.KeyFor(vocabulary.TurnActionRef, "scene", "gatehouse"); err == nil {
		t.Fatal("an unregistered subject kind derived a key; artifacts would land in a namespace no reader " +
			"looks in")
	}
}

// Every reference predicate has to derive a key, and the derivation is what
// keeps the triple and the object location from disagreeing.
func TestKeyFor_DerivesOneKeyPerPredicatePerTurn(t *testing.T) {
	seen := make(map[string]vocabulary.Predicate)
	for _, predicate := range vocabulary.StorageRefPredicates() {
		key, err := content.KeyFor(predicate, content.SubjectTurn, "turn-act-1")
		if err != nil {
			t.Fatalf("KeyFor(%q): %v", predicate, err)
		}
		if other, clash := seen[key]; clash {
			t.Fatalf("%q and %q derive the same key %q", predicate, other, key)
		}
		seen[key] = predicate

		again, err := content.KeyFor(predicate, content.SubjectTurn, "turn-act-1")
		if err != nil {
			t.Fatalf("KeyFor(%q) second call: %v", predicate, err)
		}
		if again != key {
			t.Fatalf("%q derived %q then %q; a redelivery would write a second object", predicate, key, again)
		}
	}

	// Different turns never share a key, or one turn's artifact would overwrite
	// another's.
	first, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, "turn-act-1")
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}
	second, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, "turn-act-2")
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}
	if first == second {
		t.Fatalf("two turns derive the same action key %q", first)
	}
}

func TestKeyFor_RefusesAPredicateWithNoArtifactAndATurnIDThatIsNotOne(t *testing.T) {
	if key, err := content.KeyFor(vocabulary.TurnPhaseCurrent, content.SubjectTurn, "turn-act-1"); err == nil {
		t.Fatalf("a value-bearing predicate derived key %q", key)
	}
	if key, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, "turn.act.1"); err == nil {
		t.Fatalf("a dotted turn id derived key %q", key)
	}
	if key, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, ""); err == nil {
		t.Fatalf("an empty turn id derived key %q", key)
	}
}

// ------------------------------------------------------------------ writes

// The whole canonical action, not just the text. Both of the fields checked
// here have no other durable home once the stream message is acknowledged, and
// both are read by a stage that has not been built yet.
func TestPutAction_StoresTheWholeCanonicalAction(t *testing.T) {
	store, _ := newTestStore(t)
	action := testAction(t, "act-1")
	turnEntityID := compose(t, "turn", payload.TurnIDForAction(action.ActionID))

	ref, err := store.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("PutAction: %v", err)
	}

	got, err := store.GetAction(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if got.Channel.ReplyTo != action.Channel.ReplyTo {
		t.Fatalf("the stored action's reply address is %q, want %q; egress resolves the player's delivery "+
			"target from it and a turn whose reply address died at the ack can be completed and never delivered",
			got.Channel.ReplyTo, action.Channel.ReplyTo)
	}
	if !got.ArrivedAt.Equal(action.ArrivedAt) {
		t.Fatalf("the stored action arrived at %v, want %v; deadline evaluation uses arrival time and never "+
			"processing time, so a queued action must not miss a deadline it made", got.ArrivedAt, action.ArrivedAt)
	}
	if got.Text != action.Text {
		t.Fatalf("the stored action's text is %q, want %q", got.Text, action.Text)
	}
	if got.PlayerID != action.PlayerID || got.SceneID != action.SceneID || got.CampaignID != action.CampaignID {
		t.Fatalf("the stored action's identity fields did not survive: %+v", got)
	}
}

// At-least-once delivery means this WILL happen. The key is derived, so the
// second put lands on the first object rather than beside it.
func TestPutAction_ARedeliveryRePutsIdenticallyRatherThanDuplicating(t *testing.T) {
	store, backend := newTestStore(t)
	action := testAction(t, "act-dup")
	turnEntityID := compose(t, "turn", payload.TurnIDForAction(action.ActionID))

	first, err := store.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("first PutAction: %v", err)
	}
	stored := string(backend.objects[first.Key])

	second, err := store.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("second PutAction: %v", err)
	}

	if second != first {
		t.Fatalf("a redelivery produced reference %s, the first produced %s; the turn entity carries one of "+
			"them and the other is an orphan", second, first)
	}
	if len(backend.objects) != 1 {
		t.Fatalf("two deliveries left %d objects: %v", len(backend.objects), backend.objects)
	}
	if got := string(backend.objects[first.Key]); got != stored {
		t.Fatalf("the re-put wrote different bytes to the same key:\n first: %s\nsecond: %s", stored, got)
	}
	if backend.puts[first.Key] != 2 {
		t.Fatalf("the second delivery issued %d put(s); it must actually re-put, since the crash it recovers "+
			"from may have happened before the first one landed", backend.puts[first.Key])
	}
}

// A stage handed turn A's entity with turn B's action would store the player's
// words under a turn nobody is running, and strand the one that is.
func TestPutAction_RefusesAMismatchedTurnEntity(t *testing.T) {
	store, backend := newTestStore(t)
	action := testAction(t, "act-mine")
	otherTurn := compose(t, "turn", payload.TurnIDForAction("act-yours"))

	if ref, err := store.PutAction(t.Context(), otherTurn, action); err == nil {
		t.Fatalf("an action was stored under another turn's entity as %s", ref)
	}
	if len(backend.objects) != 0 {
		t.Fatalf("the refused write stored %d object(s)", len(backend.objects))
	}
}

// An artifact that cannot pass its own contract must not become the durable
// record of what happened.
func TestPutAction_RefusesAnInvalidActionWithoutStoringIt(t *testing.T) {
	store, backend := newTestStore(t)
	action := testAction(t, "act-bad")
	action.Text = ""
	turnEntityID := compose(t, "turn", payload.TurnIDForAction(action.ActionID))

	if _, err := store.PutAction(t.Context(), turnEntityID, action); err == nil {
		t.Fatal("an action with no text was stored")
	}
	if len(backend.objects) != 0 {
		t.Fatalf("the refused write stored %d object(s)", len(backend.objects))
	}
}

// ------------------------------------------------------------------- reads

// A reference the graph carries whose object is missing is the failure mode the
// write ordering exists to prevent, so a reader has to be able to NAME it
// rather than report a generic read fault it would retry forever.
func TestGetAction_ReportsAMissingObjectAsSuchRatherThanAsAFault(t *testing.T) {
	store, _ := newTestStore(t)
	key, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, "turn-act-ghost")
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}

	_, err = store.GetAction(t.Context(), content.Ref{Instance: store.InstanceName(), Key: key})
	if err == nil {
		t.Fatal("reading a reference with no object behind it succeeded")
	}
	if !errors.Is(err, content.ErrArtifactNotFound) {
		t.Fatalf("a missing object was reported as %v, which a caller cannot distinguish from a transient "+
			"fault it should retry", err)
	}
}

// A reference resolves through the instance that WROTE it. Reading one from
// another store would read a different object, or none, and report neither.
func TestGetAction_RefusesAReferenceFromAnotherStorageInstance(t *testing.T) {
	store, _ := newTestStore(t)
	action := testAction(t, "act-elsewhere")
	turnEntityID := compose(t, "turn", payload.TurnIDForAction(action.ActionID))

	ref, err := store.PutAction(t.Context(), turnEntityID, action)
	if err != nil {
		t.Fatalf("PutAction: %v", err)
	}
	ref.Instance = "SOME_OTHER_STORE"

	if _, err := store.GetAction(t.Context(), ref); err == nil {
		t.Fatal("a reference naming another storage instance was resolved here")
	}
}

// The key's last segment is the artifact's type discriminator — it is what the
// stored bytes have instead of an envelope — so reading one kind through
// another kind's accessor is a wiring bug and must say so.
func TestGet_RefusesAReferenceAddressingAnotherArtifactKind(t *testing.T) {
	store, _ := newTestStore(t)
	detail := &content.FailureDetail{
		TurnID:  "turn-act-kind",
		Reason:  vocabulary.FailureEffectInvalid,
		Message: "health 43 exceeds the registered bound",
	}
	turnEntityID := compose(t, "turn", detail.TurnID)

	ref, err := store.PutFailureDetail(t.Context(), turnEntityID, detail)
	if err != nil {
		t.Fatalf("PutFailureDetail: %v", err)
	}
	if _, err := store.GetAction(t.Context(), ref); err == nil {
		t.Fatal("a failure detail was read back as a player action")
	}
}

// Stored bytes are untrusted input like any other: an artifact from an older
// schema decodes into a half-populated struct that a persona would read as
// fact.
func TestGetAction_RefusesStoredBytesThatDoNotSatisfyTheContract(t *testing.T) {
	store, backend := newTestStore(t)
	key, err := content.KeyFor(vocabulary.TurnActionRef, content.SubjectTurn, "turn-act-rot")
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}
	if err := backend.Put(t.Context(), key, []byte(`{"action_id":"act-rot"}`)); err != nil {
		t.Fatalf("seed backend: %v", err)
	}

	if _, err := store.GetAction(t.Context(), content.Ref{Instance: store.InstanceName(), Key: key}); err == nil {
		t.Fatal("a half-populated stored action was returned as if it were the player's move")
	}
}

// ---------------------------------------------------------- failure detail

func TestFailureDetail_RoundTripsWithTheTargetAndTheCommittedSet(t *testing.T) {
	store, _ := newTestStore(t)
	detail := &content.FailureDetail{
		TurnID:    "turn-act-partial",
		Reason:    vocabulary.FailureEffectCommitIncomplete,
		Message:   "merge into the sentry failed after the courier committed",
		Target:    compose(t, "character", "hollis"),
		Committed: []string{compose(t, "character", "rook")},
	}
	turnEntityID := compose(t, "turn", detail.TurnID)

	ref, err := store.PutFailureDetail(t.Context(), turnEntityID, detail)
	if err != nil {
		t.Fatalf("PutFailureDetail: %v", err)
	}
	got, err := store.GetFailureDetail(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetFailureDetail: %v", err)
	}

	if got.Target != detail.Target {
		t.Fatalf("the stored detail names target %q, want %q; which target failed is the first question a "+
			"partial commit raises and the one the closed code cannot answer", got.Target, detail.Target)
	}
	if len(got.Committed) != 1 || got.Committed[0] != detail.Committed[0] {
		t.Fatalf("the stored detail records committed targets %v, want %v; a partial commit that recorded "+
			"only its failure leaves the world changed and the change unrecorded", got.Committed, detail.Committed)
	}
	if got.Message != detail.Message || got.Reason != detail.Reason {
		t.Fatalf("the stored detail reads %+v", got)
	}
}

func TestFailureDetail_RefusesRecordsThatExplainNothing(t *testing.T) {
	store, backend := newTestStore(t)
	for name, detail := range map[string]*content.FailureDetail{
		"no message": {TurnID: "turn-act-x", Reason: vocabulary.FailureEffectInvalid},
		"reason outside the closed set": {
			TurnID: "turn-act-x", Reason: "it seemed wrong", Message: "something",
		},
		"target that is not an entity": {
			TurnID: "turn-act-x", Reason: vocabulary.FailureEffectInvalid,
			Message: "something", Target: "rook",
		},
		"oversized message": {
			TurnID: "turn-act-x", Reason: vocabulary.FailureEffectInvalid,
			Message: strings.Repeat("a", content.MaxFailureMessageBytes+1),
		},
	} {
		turnEntityID := compose(t, "turn", detail.TurnID)
		if _, err := store.PutFailureDetail(t.Context(), turnEntityID, detail); err == nil {
			t.Fatalf("%s: stored anyway", name)
		}
	}
	if len(backend.objects) != 0 {
		t.Fatalf("refused failure details left %d object(s)", len(backend.objects))
	}
}

// ------------------------------------------------------------ construction

// The instance name is the only free variable in the reference budget, so an
// unbounded one would make every reference oversized and the first symptom
// would be turns failing at accept.
func TestNewStore_RefusesAnInstanceNameTheReferenceBudgetCannotCarry(t *testing.T) {
	for name, instance := range map[string]string{
		"empty":        "",
		"oversized":    strings.Repeat("a", content.MaxInstanceNameBytes+1),
		"with a slash": "buckets/content",
	} {
		if _, err := content.NewStore(newMemoryBackend(instance)); err == nil {
			t.Fatalf("%s instance name was accepted", name)
		}
	}
}
