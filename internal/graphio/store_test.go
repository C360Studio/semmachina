package graphio_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	"github.com/c360studio/semstreams/pkg/projection"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/projectioncontract"
)

const testEntityID = "c360.semmachina.world1.starter.campaign.main"

var testTime = time.Date(2026, 8, 12, 9, 15, 30, 0, time.UTC)

type fakeRequester struct {
	reply    []byte
	err      error
	subject  string
	request  []byte
	requests int
}

func (r *fakeRequester) RequestClassified(_ context.Context, subject string, data []byte, _ time.Duration) ([]byte, error) {
	r.subject, r.request = subject, append([]byte(nil), data...)
	r.requests++
	return r.reply, r.err
}

type fakeMutations struct {
	create                           projection.CreateMutation
	reconcile                        projection.ReconcileMutation
	createReceipt                    projection.MutationReceipt
	reconcileReceipt                 projection.MutationReceipt
	exact                            *graph.ExactEntity
	createErr, reconcileErr, readErr error
	creates, reconciles, reads       int
}

func (f *fakeMutations) Create(_ context.Context, request projection.CreateMutation) (projection.MutationReceipt, error) {
	f.creates++
	f.create = request
	return f.createReceipt, f.createErr
}
func (f *fakeMutations) Reconcile(_ context.Context, request projection.ReconcileMutation) (projection.MutationReceipt, error) {
	f.reconciles++
	f.reconcile = request
	return f.reconcileReceipt, f.reconcileErr
}
func (f *fakeMutations) ReadAuthoritative(_ context.Context, _ string) (*graph.ExactEntity, error) {
	f.reads++
	return f.exact, f.readErr
}

func newStore(t *testing.T, requester graphio.Requester, provided ...*fakeMutations) *graphio.Store {
	t.Helper()
	mutations := &fakeMutations{}
	if len(provided) != 0 {
		mutations = provided[0]
	}
	store, err := graphio.NewStoreWithMutationClient(requester, mutations)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func validEntity() *graph.EntityState {
	return &graph.EntityState{
		ID:          testEntityID,
		MessageType: message.Type{Domain: "semmachina", Category: "campaign_entity", Version: "v1"},
		Version:     1, UpdatedAt: testTime,
		Triples: []message.Triple{{Subject: testEntityID, Predicate: "campaign.seed.value", Object: "abc123",
			Source: "campaign-instantiation", Context: testEntityID, Timestamp: testTime, Confidence: 1}},
	}
}

func withFrameworkIndexing(entity *graph.EntityState) *graph.EntityState {
	resident := entity.Clone()
	resident.Triples = append(resident.Triples, message.Triple{
		Subject: resident.ID, Predicate: ssvocab.EntityIndexingProfile, Object: ssvocab.IndexingProfileControl,
		Source: "graph-ingest-indexing-profile", Timestamp: testTime.Add(time.Second), Confidence: 1,
	})
	return resident
}

func TestCreateEntity_SeparatesEnvelopeFromBirthFactsAndUsesStableIdentity(t *testing.T) {
	stored := validEntity()
	mutations := &fakeMutations{createReceipt: projection.MutationReceipt{
		Entity: stored, KVRevision: 12, Commit: projection.CommitVerified,
	}}
	result, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, validEntity())
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if mutations.creates != 1 {
		t.Fatalf("creates = %d", mutations.creates)
	}
	if len(mutations.create.Entity.Triples) != 0 {
		t.Fatal("entity envelope carried triples")
	}
	if len(mutations.create.Triples) != 1 {
		t.Fatalf("birth triples = %d", len(mutations.create.Triples))
	}
	if mutations.create.Metadata.RequestID != testEntityID || mutations.create.Metadata.Source != "campaign-instantiation" ||
		!mutations.create.Metadata.Timestamp.Equal(testTime) {
		t.Fatalf("unstable create identity: %+v", mutations.create.Metadata)
	}
	if result.Entity.ID != testEntityID || result.Revision != 12 {
		t.Fatalf("result = %+v", result)
	}
}

func TestCreateEntity_NormalizesProducerContextsToEntityScopedMutationIdentity(t *testing.T) {
	first := validEntity()
	second := validEntity()
	second.ID = "c360.semmachina.world1.starter.campaign.second"
	originalContext := "shared-import-operation"
	for _, entity := range []*graph.EntityState{first, second} {
		for index := range entity.Triples {
			entity.Triples[index].Subject = entity.ID
			entity.Triples[index].Context = originalContext
		}
	}

	firstMutations := &fakeMutations{createReceipt: projection.MutationReceipt{
		Entity: first, KVRevision: 1, Commit: projection.CommitVerified,
	}}
	if _, err := newStore(t, &fakeRequester{}, firstMutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, first); err != nil {
		t.Fatalf("first CreateEntity: %v", err)
	}
	secondMutations := &fakeMutations{createReceipt: projection.MutationReceipt{
		Entity: second, KVRevision: 2, Commit: projection.CommitVerified,
	}}
	if _, err := newStore(t, &fakeRequester{}, secondMutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, second); err != nil {
		t.Fatalf("second CreateEntity: %v", err)
	}
	if firstMutations.create.Metadata.RequestID != first.ID || secondMutations.create.Metadata.RequestID != second.ID {
		t.Fatalf("create identities = %q and %q", firstMutations.create.Metadata.RequestID,
			secondMutations.create.Metadata.RequestID)
	}
	if firstMutations.create.Triples[0].Context != first.ID || secondMutations.create.Triples[0].Context != second.ID {
		t.Fatalf("canonical contexts = %q and %q", firstMutations.create.Triples[0].Context,
			secondMutations.create.Triples[0].Context)
	}
	if first.Triples[0].Context != originalContext || second.Triples[0].Context != originalContext {
		t.Fatalf("caller-owned entities were mutated: %q and %q", first.Triples[0].Context, second.Triples[0].Context)
	}
}

func TestCreateEntity_NormalizesMixedProducerContextsWithoutChangingRequestIdentity(t *testing.T) {
	wanted := validEntity()
	wanted.Triples = append(wanted.Triples, message.Triple{
		Subject: wanted.ID, Predicate: "campaign.experience.persona-pack", Object: "persona.pack.v1",
		Source: "campaign-instantiation", Context: "template.starter@v3", Timestamp: testTime, Confidence: 1,
	})
	original := wanted.Clone()
	mutations := &fakeMutations{createReceipt: projection.MutationReceipt{
		Entity: wanted, KVRevision: 3, Commit: projection.CommitVerified,
	}}
	if _, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, wanted); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if mutations.create.Metadata.RequestID != wanted.ID {
		t.Fatalf("request identity = %q, want %q", mutations.create.Metadata.RequestID, wanted.ID)
	}
	for index, triple := range mutations.create.Triples {
		if triple.Context != wanted.ID {
			t.Fatalf("triple[%d] context = %q, want request identity %q", index, triple.Context, wanted.ID)
		}
	}
	for index := range wanted.Triples {
		if wanted.Triples[index].Context != original.Triples[index].Context {
			t.Fatalf("caller triple[%d] context changed from %q to %q", index,
				original.Triples[index].Context, wanted.Triples[index].Context)
		}
	}
}

func TestCreateEntity_MapsOnlyTypedConflictAndNeverRetriesUnknownCommit(t *testing.T) {
	conflict := &projection.MutationError{Operation: projection.MutationOperationCreate,
		Kind: projection.MutationConflict, Commit: projection.CommitNotCommitted, Err: errors.New("taken")}
	mutations := &fakeMutations{createErr: conflict}
	_, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, validEntity())
	if !errors.Is(err, graphio.ErrEntityExists) {
		t.Fatalf("got %v", err)
	}

	unknown := &projection.MutationError{Operation: projection.MutationOperationCreate,
		Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")}
	mutations = &fakeMutations{createErr: unknown}
	_, err = newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, validEntity())
	if err == nil || errors.Is(err, graphio.ErrEntityExists) {
		t.Fatalf("got %v", err)
	}
	if mutations.creates != 1 {
		t.Fatalf("commit-unknown was retried %d times", mutations.creates)
	}
}

func TestCreateEntity_CommitUnknownResolvesFromExactMatchingBirth(t *testing.T) {
	wanted := validEntity()
	mutations := &fakeMutations{
		createErr: &projection.MutationError{Operation: projection.MutationOperationCreate,
			Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")},
		exact: &graph.ExactEntity{Entity: withFrameworkIndexing(wanted), KVRevision: 17},
	}
	result, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, wanted)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if result.Revision != 17 || mutations.creates != 1 || mutations.reads != 1 {
		t.Fatalf("result=%+v creates=%d reads=%d", result, mutations.creates, mutations.reads)
	}
}

func TestCreateEntity_CommitUnknownComparesTheCanonicalBirthContext(t *testing.T) {
	wanted := validEntity()
	wanted.Triples[0].Context = "logical-turn-or-template-provenance"
	resident := wanted.Clone()
	for index := range resident.Triples {
		resident.Triples[index].Context = resident.ID
	}
	mutations := &fakeMutations{
		createErr: &projection.MutationError{Operation: projection.MutationOperationCreate,
			Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")},
		exact: &graph.ExactEntity{Entity: withFrameworkIndexing(resident), KVRevision: 19},
	}
	result, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, wanted)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if result.Revision != 19 {
		t.Fatalf("revision = %d, want 19", result.Revision)
	}
	if wanted.Triples[0].Context != "logical-turn-or-template-provenance" {
		t.Fatalf("caller context changed to %q", wanted.Triples[0].Context)
	}
}

func TestCreateEntity_CommitUnknownRejectsSameBirthFactsWithDifferentEnvelopeVersion(t *testing.T) {
	wanted := validEntity()
	resident := withFrameworkIndexing(wanted)
	resident.Version++
	mutations := &fakeMutations{
		createErr: &projection.MutationError{Operation: projection.MutationOperationCreate,
			Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")},
		exact: &graph.ExactEntity{Entity: resident, KVRevision: 17},
	}
	result, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, wanted)
	if err == nil {
		t.Fatalf("different authoritative envelope was misattributed as this create: %+v", result)
	}
	if mutations.creates != 1 || mutations.reads != 1 {
		t.Fatalf("creates=%d reads=%d", mutations.creates, mutations.reads)
	}
}

func TestCreateEntity_CommitUnknownAllowsOnlyFrameworkInjectedIndexingFact(t *testing.T) {
	wanted := validEntity()
	resident := withFrameworkIndexing(wanted)
	mutations := &fakeMutations{
		createErr: &projection.MutationError{Operation: projection.MutationOperationCreate,
			Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")},
		exact: &graph.ExactEntity{Entity: resident, KVRevision: 17},
	}
	result, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, wanted)
	if err != nil {
		t.Fatalf("framework indexing fact prevented convergence: %v", err)
	}
	if result.Entity == nil || len(result.Entity.Triples) != 2 {
		t.Fatalf("authoritative result lost framework fact: %+v", result)
	}
}

func TestCreateEntity_CommitUnknownRejectsCallerShapedIndexingFact(t *testing.T) {
	wanted := validEntity()
	resident := withFrameworkIndexing(wanted)
	resident.Triples[len(resident.Triples)-1].Source = "some-other-creator"
	mutations := &fakeMutations{
		createErr: &projection.MutationError{Operation: projection.MutationOperationCreate,
			Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")},
		exact: &graph.ExactEntity{Entity: resident, KVRevision: 17},
	}
	if result, err := newStore(t, &fakeRequester{}, mutations).CreateEntity(
		t.Context(), projectioncontract.CampaignBirthContract, wanted); err == nil {
		t.Fatalf("non-framework indexing fact was ignored: %+v", result)
	}
}

func TestReconcile_PassesExplicitCompleteGroupIncludingEmptyClear(t *testing.T) {
	mutations := &fakeMutations{reconcileReceipt: projection.MutationReceipt{
		Entity: validEntity(), KVRevision: 13, Commit: projection.CommitVerified,
	}}
	store := newStore(t, &fakeRequester{}, mutations)
	if _, err := store.Reconcile(t.Context(), projectioncontract.CampaignImport, testEntityID, nil); err != nil {
		t.Fatalf("Reconcile clear: %v", err)
	}
	if mutations.reconcile.Contract != projectioncontract.CampaignStateContract ||
		mutations.reconcile.Group != projectioncontract.CampaignImport.Group ||
		mutations.reconcile.EntityID != testEntityID || mutations.reconcile.Desired == nil {
		// MutationClient accepts both nil and empty as a complete empty desired
		// set; the important distinction is that Store does not reject the clear.
		if len(mutations.reconcile.Desired) != 0 {
			t.Fatalf("request = %+v", mutations.reconcile)
		}
	}
}

func TestReconcile_MapsTypedAbsenceAndDoesNotRetryCASOrCommitUnknown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     projection.MutationErrorKind
		sentinel bool
	}{
		{"missing", projection.MutationNotFound, true},
		{"cas", projection.MutationRevisionConflict, false},
		{"commit unknown", projection.MutationCommitUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := &fakeMutations{reconcileErr: &projection.MutationError{
				Operation: projection.MutationOperationReconcile, Kind: tc.kind,
				Commit: projection.CommitNotCommitted, Err: errors.New(tc.name),
			}}
			_, err := newStore(t, &fakeRequester{}, mutations).Reconcile(
				t.Context(), projectioncontract.CampaignImport, testEntityID, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			if errors.Is(err, graphio.ErrEntityNotFound) != tc.sentinel {
				t.Fatalf("sentinel mismatch: %v", err)
			}
			if mutations.reconciles != 1 {
				t.Fatalf("mutation attempted %d times", mutations.reconciles)
			}
		})
	}
}

func TestReconcile_CommitUnknownResolvesOnlyFromExactWholeGroupMatch(t *testing.T) {
	desired := []message.Triple{{Subject: testEntityID, Predicate: "campaign.import.completed", Object: "now",
		Source: "world-import", Context: testEntityID, Timestamp: testTime, Confidence: 1}}
	resident := validEntity()
	resident.Triples = append(resident.Triples, desired...)
	mutations := &fakeMutations{
		reconcileErr: &projection.MutationError{Operation: projection.MutationOperationReconcile,
			Kind: projection.MutationCommitUnknown, Commit: projection.CommitUnknown, Err: errors.New("reply lost")},
		exact: &graph.ExactEntity{Entity: resident, KVRevision: 18},
	}
	state, err := newStore(t, &fakeRequester{}, mutations).Reconcile(
		t.Context(), projectioncontract.CampaignImport, testEntityID, desired)
	if err != nil || state.ID != testEntityID {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if mutations.reconciles != 1 || mutations.reads != 1 {
		t.Fatalf("reconciles=%d reads=%d", mutations.reconciles, mutations.reads)
	}
}

func TestGetEntity_UsesExactAuthorityAndMapsTypedAbsence(t *testing.T) {
	mutations := &fakeMutations{exact: &graph.ExactEntity{Entity: validEntity(), KVRevision: 21}}
	state, err := newStore(t, &fakeRequester{}, mutations).GetEntity(t.Context(), testEntityID)
	if err != nil || state.ID != testEntityID || mutations.reads != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}

	mutations = &fakeMutations{readErr: &projection.MutationError{
		Operation: projection.MutationOperationReadAuthoritative, Kind: projection.MutationNotFound,
		Commit: projection.CommitNotCommitted, Err: errs.ClassifiedCode(errs.ErrorInvalid, graph.ErrorCodeEntityNotFound, errors.New("missing")),
	}}
	state, err = newStore(t, &fakeRequester{}, mutations).GetEntity(t.Context(), testEntityID)
	if state != nil || !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestBatchReadAccountsForEveryRequestedID(t *testing.T) {
	missingID := "c360.semmachina.world1.starter.character.missing"
	unaccountedID := "c360.semmachina.world1.starter.character.unaccounted"
	body, err := json.Marshal(graph.EntityBatchResponse{Entities: []graph.EntityState{*validEntity()},
		Missing: []graph.MissingEntity{{ID: missingID, Reason: graph.MissingNotFound}}})
	if err != nil {
		t.Fatal(err)
	}
	requester := &fakeRequester{reply: body}
	result, err := newStore(t, requester, &fakeMutations{}).GetEntities(
		t.Context(), []string{testEntityID, missingID, unaccountedID})
	if err != nil {
		t.Fatalf("GetEntities: %v", err)
	}
	reasons := map[string]graph.MissingReason{}
	for _, missing := range result.Missing {
		reasons[missing.ID] = missing.Reason
	}
	if reasons[missingID] != graph.MissingNotFound || reasons[unaccountedID] != graph.MissingUnknown {
		t.Fatalf("missing accounting = %+v", reasons)
	}
}

func TestNewStoreRequiresDependenciesAndPositiveTimeout(t *testing.T) {
	if _, err := graphio.NewStore(nil); err == nil {
		t.Fatal("accepted nil requester")
	}
	if _, err := graphio.NewStoreWithMutationClient(&fakeRequester{}, nil); err == nil {
		t.Fatal("accepted nil mutations")
	}
	if _, err := graphio.NewStoreWithMutationClient(&fakeRequester{}, &fakeMutations{}, graphio.WithTimeout(0)); err == nil {
		t.Fatal("accepted zero timeout")
	}
}
