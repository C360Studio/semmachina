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

// The guard has to validate what is SENT, not what was passed. Options mutate
// the request after the parameter list is read, so a guard that ran first would
// check one value and transmit another — and the value graph-ingest answers a
// foreign subject with is silence: it splits the triple off and routes it onto
// the APPENDING lane, logging the failure without returning it.
func TestMergeTriples_RejectsAForeignSubjectAnOptionAdded(t *testing.T) {
	requester := &fakeRequester{}
	own := []message.Triple{
		{Subject: testEntityID, Predicate: "turn.roll.band", Object: "partial", Confidence: 1.0},
	}
	smuggle := func(request *graph.UpdateEntityWithTriplesRequest) {
		request.AddTriples = append(request.AddTriples, message.Triple{
			Subject: otherEntity, Predicate: "turn.roll.total", Object: 8, Confidence: 1.0,
		})
	}

	_, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, own, smuggle)
	if err == nil {
		t.Fatal("an option appended a triple for another entity and it was sent")
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

// A predicate's value set can legitimately become EMPTY — the last carried item
// is put down, the last alliance is broken. AddTriples cannot express that: it
// replaces a predicate's values with the ones it carries, and carrying none
// leaves the predicate untouched. So clearing needs the request's removal list,
// which upstream applies BEFORE the merge.
func TestMergeTriples_ClearsAPredicateThroughTheRemovalList(t *testing.T) {
	body, err := json.Marshal(graph.UpdateEntityWithTriplesResponse{Entity: validEntity()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}

	if _, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, nil,
		graphio.WithClearedPredicates("world.relation.carries")); err != nil {
		t.Fatalf("MergeTriples: %v", err)
	}

	var sent graph.UpdateEntityWithTriplesRequest
	if err := json.Unmarshal(requester.request, &sent); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	if len(sent.RemoveTriples) != 1 || sent.RemoveTriples[0] != "world.relation.carries" {
		t.Fatalf("sent removals %v, want the one cleared predicate", sent.RemoveTriples)
	}
	if len(sent.AddTriples) != 0 {
		t.Fatalf("a clear-only merge sent %d triples to add", len(sent.AddTriples))
	}
}

// A merge that neither adds nor clears anything is a no-op round trip, and a
// no-op that reports success is indistinguishable from a write that landed.
func TestMergeTriples_StillRejectsAWriteThatChangesNothing(t *testing.T) {
	requester := &fakeRequester{}

	if _, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, nil); err == nil {
		t.Fatal("a merge carrying neither triples nor removals was sent")
	}
	if _, err := newStore(t, requester).MergeTriples(t.Context(), testEntityID, nil,
		graphio.WithClearedPredicates()); err == nil {
		t.Fatal("an empty removal list was treated as a write")
	}
	if requester.requests != 0 {
		t.Fatal("the store issued an empty request")
	}
}

// ------------------------------------------------------- the read fan-in

// A batch that came back short is a NORMAL outcome with a per-id explanation,
// and the failure it replaces was invisible: before ADR-084 a not-found id was
// simply absent from the list, so a caller could not tell an entity that does
// not exist from one the read did not reach. Passing the accounting on is the
// point.
func TestGetEntities_ReportsEveryRequestedIDItDidNotReturn(t *testing.T) {
	body, err := json.Marshal(graph.EntityBatchResponse{
		Entities: []graph.EntityState{*validEntity()},
		Missing:  []graph.MissingEntity{{ID: otherEntity, Reason: graph.MissingNotFound}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}

	const unaccounted = "c360.semmachina.world1.starter.character.wren"
	result, err := newStore(t, requester).GetEntities(
		t.Context(), []string{testEntityID, otherEntity, unaccounted})
	if err != nil {
		t.Fatalf("GetEntities: %v", err)
	}
	if requester.subject != graphio.SubjectQueryBatch {
		t.Fatalf("the batch read went to %q", requester.subject)
	}
	if len(result.Entities) != 1 || result.Entities[0].ID != testEntityID {
		t.Fatalf("hydrated %d entities: %+v", len(result.Entities), result.Entities)
	}

	reasons := map[string]graph.MissingReason{}
	for _, missing := range result.Missing {
		reasons[missing.ID] = missing.Reason
	}
	if reasons[otherEntity] != graph.MissingNotFound {
		t.Fatalf("%s was reported as %q, want the handler's own reason", otherEntity, reasons[otherEntity])
	}
	// The id the response mentioned in NEITHER list is the one a caller cannot
	// see for itself, so the accounting has to be total over the request.
	if reasons[unaccounted] != graph.MissingUnknown {
		t.Fatalf("an id the response never mentioned was reported as %q, want %q; without it a dropped id "+
			"disappears from the accounting entirely", reasons[unaccounted], graph.MissingUnknown)
	}
}

func TestGetEntities_SendsNothingForAnEmptyRequest(t *testing.T) {
	requester := &fakeRequester{}

	result, err := newStore(t, requester).GetEntities(t.Context(), nil)
	if err != nil {
		t.Fatalf("GetEntities: %v", err)
	}
	if len(result.Entities) != 0 || len(result.Missing) != 0 {
		t.Fatalf("an empty request produced %+v", result)
	}
	if requester.requests != 0 {
		t.Fatal("the store issued a round trip for an empty id list")
	}
}

// The authoritative-state contract applies to every decoded entity, exactly as
// it does on the single read: poisoned stored bytes become a typed refusal here
// rather than half-usable state in a persona's context.
//
// And the refusal has to say WHICH one. The entity that poisons a reply is very
// often the one whose ID is missing — as here — so a message built from the
// entity's own ID names nothing; the position in the reply is the handle that
// always exists.
func TestGetEntities_RefusesAPoisonedEntityInTheBatchAndSaysWhichOne(t *testing.T) {
	poisoned := validEntity()
	poisoned.ID = ""
	body, err := json.Marshal(graph.EntityBatchResponse{
		Entities: []graph.EntityState{*validEntity(), *poisoned},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, err = newStore(t, &fakeRequester{reply: body}).GetEntities(t.Context(), []string{testEntityID})
	if err == nil {
		t.Fatal("a batch carrying an entity that fails the decoded-state contract was accepted")
	}
	if !strings.Contains(err.Error(), "entity[1]") {
		t.Fatalf("refusal %q does not name which candidate poisoned the reply", err)
	}
}

// An index still building is the one wrong answer that would be SILENT: a
// partial keyset reads as a smaller scene rather than as an error. Upstream
// refuses to serve one, and the refusal has to arrive as something a caller can
// tell from "that scene is empty".
func TestIncomingRelationships_MapsTheNotReadyCodeToAMatchableSentinel(t *testing.T) {
	requester := &fakeRequester{err: classified(graph.ErrorCodeIndexNotReady)}

	_, err := newStore(t, requester).IncomingRelationships(t.Context(), testEntityID)
	if !errors.Is(err, graphio.ErrIndexNotReady) {
		t.Fatalf("IncomingRelationships returned %v, want ErrIndexNotReady", err)
	}
	var ce *errs.ClassifiedError
	if !errors.As(err, &ce) {
		t.Fatal("the classified error was swallowed by the sentinel")
	}
}

func TestIncomingRelationships_DecodesTheQueryEnvelope(t *testing.T) {
	body, err := json.Marshal(graph.NewQueryResponse(graph.IncomingRelationshipsData{
		Relationships: []graph.IncomingEntry{
			{FromEntityID: otherEntity, Predicate: "world.location.current"},
		},
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}

	edges, err := newStore(t, requester).IncomingRelationships(t.Context(), testEntityID)
	if err != nil {
		t.Fatalf("IncomingRelationships: %v", err)
	}
	if requester.subject != graphio.SubjectIndexQueryIncoming {
		t.Fatalf("the reverse-edge read went to %q", requester.subject)
	}
	if len(edges) != 1 || edges[0].FromEntityID != otherEntity {
		t.Fatalf("decoded %+v, want the one incoming edge; a reader that unwrapped the envelope wrong sees "+
			"an empty room and reports no error", edges)
	}
}

// ------------------------------------------------------- the prefix enumeration

// The cursor is not an optimisation to skip. Upstream trims a page against a
// response BYTE budget as well as a count limit, so a caller that read one page
// and stopped would see a prefix of the world and call it the whole thing — and
// for the ledger's boot reconciliation that reads as a campaign whose later
// turns never happened.
func TestEntitiesWithPrefix_PassesTheCursorThroughAndReturnsTheNextOne(t *testing.T) {
	body, err := json.Marshal(graph.PrefixQueryResponse{
		Entities:   []graph.EntityState{*validEntity()},
		NextCursor: graph.EncodeCursor(testEntityID),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	requester := &fakeRequester{reply: body}

	page, err := newStore(t, requester).EntitiesWithPrefix(
		t.Context(), "c360.semmachina.world1.starter.turn", "cursor-from-the-last-page", 7)
	if err != nil {
		t.Fatalf("EntitiesWithPrefix: %v", err)
	}
	if requester.subject != graphio.SubjectQueryPrefix {
		t.Fatalf("the prefix read went to %q", requester.subject)
	}

	var sent graph.PrefixQueryRequest
	if err := json.Unmarshal(requester.request, &sent); err != nil {
		t.Fatalf("decode the request the store sent: %v", err)
	}
	if sent.Cursor != "cursor-from-the-last-page" {
		t.Fatalf("the store sent cursor %q; a dropped cursor restarts the scan at page one forever", sent.Cursor)
	}
	if sent.Limit != 7 {
		t.Fatalf("the store sent limit %d, want 7", sent.Limit)
	}
	// The prefix travels WITHOUT a trailing dot: upstream appends one when it
	// filters, so sending one would look for a segment named "".
	if strings.HasSuffix(sent.Prefix, ".") {
		t.Fatalf("the store sent prefix %q with a trailing dot", sent.Prefix)
	}

	if len(page.Entities) != 1 || page.Entities[0].ID != testEntityID {
		t.Fatalf("page held %+v", page.Entities)
	}
	if page.NextCursor == "" {
		t.Fatal("the next cursor was dropped; the caller would stop after one page")
	}
}

func TestEntitiesWithPrefix_RefusesAnEmptyPrefix(t *testing.T) {
	requester := &fakeRequester{}
	if _, err := newStore(t, requester).EntitiesWithPrefix(t.Context(), "", "", 0); err == nil {
		t.Fatal("an empty prefix was accepted; it enumerates every entity in the world")
	}
	if requester.requests != 0 {
		t.Fatal("the store issued a request for an empty prefix")
	}
}

// Stored bytes are untrusted input on every read path, and this one returns
// whole entities a persona or an archive would treat as fact.
func TestEntitiesWithPrefix_RefusesAPoisonedEntityInThePage(t *testing.T) {
	poisoned := *validEntity()
	poisoned.ID = ""
	body, err := json.Marshal(graph.PrefixQueryResponse{Entities: []graph.EntityState{poisoned}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := newStore(t, &fakeRequester{reply: body}).EntitiesWithPrefix(
		t.Context(), "c360.semmachina.world1.starter.turn", "", 0); err == nil {
		t.Fatal("a page carrying an entity that fails the decoded-state contract was accepted")
	}
}
