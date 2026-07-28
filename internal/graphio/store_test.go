package graphio_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"

	"github.com/c360studio/semmachina/internal/graphio"
)

const (
	testEntityID = "c360.semmachina.world1.starter.campaign.main"
	otherEntity  = "c360.semmachina.world1.starter.character.rook"
)

var testTime = time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)

// fakeRequester is the classified request surface, scripted per subject. It
// exists for the failure shapes a healthy broker will not produce on demand —
// the success paths are proven against real graph-ingest in the campaign and
// dice integration suites.
type fakeRequester struct {
	reply    []byte
	err      error
	subject  string
	request  []byte
	requests int
}

func (r *fakeRequester) RequestClassified(
	_ context.Context,
	subject string,
	data []byte,
	_ time.Duration,
) ([]byte, error) {
	r.subject = subject
	r.request = data
	r.requests++
	return r.reply, r.err
}

func newStore(t *testing.T, requester graphio.Requester) *graphio.Store {
	t.Helper()
	store, err := graphio.NewStore(requester)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func validEntity() *graph.EntityState {
	return &graph.EntityState{
		ID:          testEntityID,
		MessageType: message.Type{Domain: "semmachina", Category: "campaign_entity", Version: "v1"},
		Version:     1,
		UpdatedAt:   testTime,
		Triples: []message.Triple{{
			Subject:    testEntityID,
			Predicate:  "campaign.seed.value",
			Object:     "abc123",
			Source:     "test",
			Timestamp:  testTime,
			Confidence: 1.0,
		}},
	}
}

func classified(code string) error {
	return errs.ClassifiedCode(errs.ErrorInvalid, code, errors.New("handler rejected the request"))
}

// The create-conflict is CONTROL FLOW — it is how the engine learns a world is
// already instantiated — so it must arrive as a matchable sentinel, and the
// classified error must stay reachable underneath it for diagnostics.
func TestCreateEntity_MapsTheConflictCodeToASentinelWithoutLosingTheClassifiedError(t *testing.T) {
	requester := &fakeRequester{err: classified(graph.ErrorCodeEntityExists)}

	_, err := newStore(t, requester).CreateEntity(t.Context(), validEntity())
	if !errors.Is(err, graphio.ErrEntityExists) {
		t.Fatalf("CreateEntity returned %v, want ErrEntityExists", err)
	}
	var ce *errs.ClassifiedError
	if !errors.As(err, &ce) {
		t.Fatal("the classified error was swallowed; the machine code is the contract")
	}
	if ce.Code != graph.ErrorCodeEntityExists {
		t.Fatalf("classified code = %q", ce.Code)
	}
	if !strings.Contains(err.Error(), testEntityID) {
		t.Fatalf("failure reason %q does not name the entity", err.Error())
	}
}

// Any OTHER classified failure must not be resolved into "already exists" — a
// transport failure is neither a fresh world nor an existing one.
func TestCreateEntity_DoesNotTreatOtherFailuresAsAConflict(t *testing.T) {
	for _, code := range []string{graph.ErrorCodeInternal, graph.ErrorCodeInvalidRequest, ""} {
		requester := &fakeRequester{err: classified(code)}

		_, err := newStore(t, requester).CreateEntity(t.Context(), validEntity())
		if errors.Is(err, graphio.ErrEntityExists) {
			t.Fatalf("code %q was reported as an existing entity", code)
		}
		if err == nil {
			t.Fatalf("code %q produced no error", code)
		}
	}
}

// Degraded means the write COMMITTED and only the read-back failed. It must not
// surface as an error, and it must not be retried by the store.
func TestCreateEntity_ReportsADegradedWriteAsCommitted(t *testing.T) {
	body, err := json.Marshal(graph.CreateEntityResponse{
		MutationResponse: graph.MutationResponse{Degraded: true, DegradedReason: "read-back failed"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}

	result, err := newStore(t, requester).CreateEntity(t.Context(), validEntity())
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if !result.Degraded || result.DegradedReason == "" {
		t.Fatalf("the degraded flag did not survive: %+v", result)
	}
	if result.Entity != nil {
		t.Fatal("a degraded response carried an entity; the read-back is what failed")
	}
	if requester.requests != 1 {
		t.Fatalf("the store issued %d requests; a degraded write must not be retried", requester.requests)
	}
}

// A malformed entity is named locally rather than sent and rejected remotely,
// where the offending token is stripped from the reply.
func TestCreateEntity_RejectsAnInvalidEntityBeforeItReachesTheWire(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*graph.EntityState)
	}{
		{"a non-canonical entity id", func(e *graph.EntityState) { e.ID = "campaign-main" }},
		{
			"an underscore-bearing predicate",
			func(e *graph.EntityState) { e.Triples[0].Predicate = "campaign.seed.value_hex" },
		},
		{
			"a two-segment predicate",
			func(e *graph.EntityState) { e.Triples[0].Predicate = "campaign.seed" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entity := validEntity()
			tc.mutate(entity)
			requester := &fakeRequester{}

			if _, err := newStore(t, requester).CreateEntity(t.Context(), entity); err == nil {
				t.Fatal("an invalid entity was sent")
			}
			if requester.requests != 0 {
				t.Fatal("the store issued a request for an entity the write gate would reject")
			}
		})
	}
}

func TestCreateEntity_RequiresAnEntity(t *testing.T) {
	if _, err := newStore(t, &fakeRequester{}).CreateEntity(t.Context(), nil); err == nil {
		t.Fatal("a nil entity was accepted")
	}
}

// "There is nothing there" must not come back as a nil entity with a nil
// error: at a call site deciding whether a world exists, those are opposite
// answers.
func TestGetEntity_MapsNotFoundToASentinel(t *testing.T) {
	requester := &fakeRequester{err: classified(graph.ErrorCodeEntityNotFound)}

	state, err := newStore(t, requester).GetEntity(t.Context(), testEntityID)
	if !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("GetEntity returned %v, want ErrEntityNotFound", err)
	}
	if state != nil {
		t.Fatal("a failed read returned an entity")
	}
}

// The positive case, so the refusal test below is not vacuous: a healthy stored
// entity decodes and comes back whole.
func TestGetEntity_ReturnsAHealthyStoredEntity(t *testing.T) {
	body, err := json.Marshal(validEntity())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}

	state, err := newStore(t, requester).GetEntity(t.Context(), testEntityID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if state.ID != testEntityID || len(state.Triples) != 1 {
		t.Fatalf("decoded entity does not match what was stored: %+v", state)
	}
	if requester.subject != graphio.SubjectQueryEntity {
		t.Fatalf("the read went to %q", requester.subject)
	}
}

func TestGetEntity_RefusesPoisonedStoredState(t *testing.T) {
	// A stored entity whose predicate violates the canonical contract: the
	// authoritative-state contract says a decoder refuses it rather than
	// handing half-usable state downstream.
	poisoned := validEntity()
	poisoned.Triples[0].Predicate = "campaign.seed"
	body, err := json.Marshal(poisoned)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	state, err := newStore(t, &fakeRequester{reply: body}).GetEntity(t.Context(), testEntityID)
	if err == nil {
		t.Fatal("poisoned stored state was returned as usable")
	}
	if state != nil {
		t.Fatal("a refused read returned an entity")
	}
}

func TestGetEntity_RequiresAnID(t *testing.T) {
	if _, err := newStore(t, &fakeRequester{}).GetEntity(t.Context(), ""); err == nil {
		t.Fatal("an empty entity id was accepted")
	}
}

// A foreign subject in a merge is not rejected upstream — it is split off and
// routed onto its own entity — so a typo would file a turn's roll on some other
// entity and report success. The guard is local because the failure is silent.
func TestMergeTriples_RejectsAForeignSubject(t *testing.T) {
	requester := &fakeRequester{}
	triples := []message.Triple{
		{Subject: testEntityID, Predicate: "turn.roll.band", Object: "partial", Confidence: 1.0},
		{Subject: otherEntity, Predicate: "turn.roll.total", Object: 8, Confidence: 1.0},
	}

	_, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, triples)
	if err == nil {
		t.Fatal("a triple targeting another entity was sent")
	}
	if !strings.Contains(err.Error(), otherEntity) {
		t.Fatalf("failure reason %q does not name the foreign subject", err.Error())
	}
	if requester.requests != 0 {
		t.Fatal("the store issued a request that would have filed a fact on the wrong entity")
	}
}

func TestMergeTriples_RejectsAnEmptyWrite(t *testing.T) {
	requester := &fakeRequester{}

	if _, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, nil); err == nil {
		t.Fatal("a merge carrying no triples was sent")
	}
	if _, err := newStore(t, requester).MergeTriples(t.Context(), "", nil); err == nil {
		t.Fatal("a merge with no entity id was accepted")
	}
	if requester.requests != 0 {
		t.Fatal("the store issued an empty request")
	}
}

func TestMergeTriples_MapsNotFoundToASentinel(t *testing.T) {
	requester := &fakeRequester{err: classified(graph.ErrorCodeEntityNotFound)}
	triples := []message.Triple{
		{Subject: testEntityID, Predicate: "turn.roll.band", Object: "partial", Confidence: 1.0},
	}

	_, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, triples)
	if !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("MergeTriples returned %v, want ErrEntityNotFound", err)
	}
}

// The merge must travel the REPLACE lane. Sending it to the append subject
// would still succeed and would leave single-valued predicates accumulating.
func TestMergeTriples_UsesTheEntityUpdateLaneAndPreservesTheStoredEnvelope(t *testing.T) {
	body, err := json.Marshal(graph.UpdateEntityWithTriplesResponse{Entity: validEntity()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}
	triples := []message.Triple{
		{Subject: testEntityID, Predicate: "turn.roll.band", Object: "partial", Confidence: 1.0},
	}

	if _, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, triples); err != nil {
		t.Fatalf("MergeTriples: %v", err)
	}
	if requester.subject != "graph.mutation.entity.update_with_triples" {
		t.Fatalf("merge went to %q; only the entity update lane replaces by predicate", requester.subject)
	}

	var sent graph.UpdateEntityWithTriplesRequest
	if err := json.Unmarshal(requester.request, &sent); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	// Zero envelope fields are preserve-when-zero upstream. Sending a
	// populated envelope here would overwrite the entity's provenance and its
	// immutable indexing profile on every triple write.
	if sent.Entity.MessageType != (message.Type{}) || sent.Entity.Version != 0 || sent.Entity.StorageRef != nil {
		t.Fatalf("the merge sent a populated envelope (%+v); it would clobber the stored one", sent.Entity)
	}
	if len(sent.AddTriples) != len(triples) {
		t.Fatalf("sent %d triples, want %d", len(sent.AddTriples), len(triples))
	}
}

// The subject constants are literals in production code so a graph CLIENT does
// not link the graph component. That trade is only safe if drift is caught, so
// the comparison happens here, from the test binary, against the upstream
// constants themselves.
func TestSubjects_MatchTheUpstreamConstants(t *testing.T) {
	cases := []struct {
		name  string
		local string
		want  string
	}{
		{"entity create", graphio.SubjectEntityCreate, graphingest.SubjectEntityCreate},
		{
			"entity update_with_triples",
			graphio.SubjectEntityUpdateWithTriples,
			graphingest.SubjectEntityUpdateWithTriples,
		},
	}

	for _, tc := range cases {
		if tc.local != tc.want {
			t.Fatalf("%s subject is %q locally and %q upstream", tc.name, tc.local, tc.want)
		}
	}

	// The merge lane must NOT be the append lane. Stated as a test because the
	// two subjects accept identical requests and differ only in whether
	// single-valued predicates replace or accumulate.
	if graphio.SubjectEntityUpdateWithTriples == graphingest.SubjectTripleAddBatch {
		t.Fatal("the merge lane is pointed at the append subject")
	}
}

func TestNewStore_RequiresARequesterAndAPositiveTimeout(t *testing.T) {
	if _, err := graphio.NewStore(nil); err == nil {
		t.Fatal("a store was built with no requester")
	}
	if _, err := graphio.NewStore(&fakeRequester{}, graphio.WithTimeout(0)); err == nil {
		t.Fatal("a store was built with a zero timeout")
	}
	if _, err := graphio.NewStore(&fakeRequester{}, graphio.WithTimeout(-time.Second)); err == nil {
		t.Fatal("a store was built with a negative timeout")
	}
}
