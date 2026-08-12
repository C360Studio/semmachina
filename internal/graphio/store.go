// Package graphio is the engine's narrow client for the graph mutation and
// query APIs.
//
// It exists so the handful of upstream contracts that are easy to get silently
// wrong are stated once. Three of them shape everything here:
//
//   - A mutation reply is EITHER a success body OR a typed error (ADR-060), and
//     the machine-readable reason lives on *errs.ClassifiedError.Code — not in
//     the message text. Sniffing the string is how a create-conflict becomes an
//     unhandled failure.
//   - typed create is atomic create-or-fail. That is what closes the
//     exists-check-then-write TOCTOU, and it is the whole mechanism behind the
//     world-instantiation gate.
//   - every update names a projection contract and complete predicate group.
//     Reconciliation can therefore clear an empty group without touching
//     sibling groups and is revision-fenced by the authoritative reader.
//
// graph-ingest remains the sole ENTITY_STATES writer: everything here is a
// request to it, never a write around it.
package graphio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
	"github.com/c360studio/semstreams/pkg/projection"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/projectioncontract"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The subjects this client speaks to graph-ingest on.
//
// They are literals rather than an import of the graph-ingest package, because
// a CLIENT should not link the component it talks to — the whole point of
// request/reply is that the two sides share a subject and nothing else. The
// upstream constants are still authoritative: TestSubjects_MatchTheUpstream
// -Constants imports graph-ingest from the TEST binary and fails if any of these
// drifts, so the check happens without the coupling.
//
// SubjectQueryEntity has no upstream constant to compare against at all —
// graph-ingest registers it as a literal — so it is pinned by an integration
// test that actually reads an entity through it.
const (
	// SubjectQueryEntity is the single-entity read.
	SubjectQueryEntity = "graph.ingest.query.entity"
	// SubjectQueryBatch is the many-entities read. One round trip for a set of
	// known ids, with per-id omissions reported rather than silently shortening
	// the list (ADR-084).
	SubjectQueryBatch = "graph.ingest.query.batch"
	// SubjectQueryPrefix is the paginated enumeration of every entity under an
	// entity-ID prefix.
	//
	// It answers the one question the other two cannot: "which entities of this
	// kind exist?" — where the caller holds no id list to batch and no reverse
	// edge to follow. The campaign ledger's boot reconciliation is exactly that
	// shape: it has to find terminal turns nobody told it about, and the
	// alternative to asking upstream is scanning ENTITY_STATES by hand, which
	// would be a local index over data the substrate already keys.
	SubjectQueryPrefix = "graph.ingest.query.prefix"
	// SubjectIndexQueryIncoming is graph-index's reverse-edge read: every
	// (source, predicate) pointing AT an entity.
	//
	// It is served by graph-index rather than graph-ingest, which is why it is
	// the one subject here in a different namespace. The engine needs it because
	// occupancy is asserted by the MEMBER — a character carries
	// world.location.current pointing at a location, and the location says
	// nothing about who is in it — so "who is here" is a reverse lookup, and
	// reverse lookups are what that index exists for. Scanning the world and
	// filtering would be a hand-rolled index over data the substrate already
	// indexes (M6).
	SubjectIndexQueryIncoming = "graph.index.query.incoming"
	// SubjectIndexQueryPredicate is graph-index's predicate membership query.
	// Supplying Value asks the index to hydrate candidates internally and filter
	// exact string objects; callers still supply a hard result limit.
	SubjectIndexQueryPredicate = "graph.index.query.predicate"
)

// DefaultTimeout bounds one request/reply round trip.
const DefaultTimeout = 5 * time.Second

// Sentinels for the two outcomes that are CONTROL FLOW rather than failure.
//
// A create that finds the key taken is how the engine learns a world is already
// instantiated; a read that finds nothing is how it learns a turn has no
// recorded roll. Both are wrapped, not swallowed: the classified error is still
// reachable with errors.As.
var (
	// ErrEntityExists reports an atomic create losing to an existing key.
	ErrEntityExists = errors.New("graph entity already exists")
	// ErrEntityNotFound reports a read or must-exist write against an absent
	// entity.
	ErrEntityNotFound = errors.New("graph entity not found")
	// ErrIndexNotReady reports a reverse-edge read that arrived while the index
	// was still building.
	//
	// It is named rather than folded into a generic failure because the WRONG
	// answer here is silent: an index mid-build would otherwise return a short
	// list, and a short list of scene members reads as a smaller scene rather
	// than as an error. Upstream refuses to serve a partial keyset; this sentinel
	// is what lets a caller tell "wait and retry" from "that scene is empty".
	ErrIndexNotReady = errors.New("graph index is not ready")
)

// Requester is the classified request surface the store needs.
//
// Classified, never raw Request: the failure path carries no body, so a caller
// that unmarshals the reply without checking the error reads an empty struct as
// success.
type Requester interface {
	RequestClassified(ctx context.Context, subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Requester = (*natsclient.Client)(nil)

// Store issues graph mutations and reads over NATS request/reply.
type Store struct {
	requester Requester
	mutations MutationClient
	timeout   time.Duration
}

// MutationClient is the canonical contract-bound mutation surface used by Store.
type MutationClient interface {
	Create(context.Context, projection.CreateMutation) (projection.MutationReceipt, error)
	Reconcile(context.Context, projection.ReconcileMutation) (projection.MutationReceipt, error)
	ReadAuthoritative(context.Context, string) (*graph.ExactEntity, error)
}

// Option configures a Store.
type Option func(*Store)

// WithTimeout overrides the per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(s *Store) { s.timeout = d }
}

// WithMutationClient supplies the canonical mutation surface. Production
// callers normally omit it; it exists for focused tests and alternate wiring.
func WithMutationClient(client MutationClient) Option {
	return func(s *Store) { s.mutations = client }
}

// NewStoreWithMutationClient builds a Store around the narrow canonical
// mutation surface. It is primarily useful to test mutation behavior without
// requiring a concrete NATS client.
func NewStoreWithMutationClient(requester Requester, mutations MutationClient, opts ...Option) (*Store, error) {
	opts = append(opts, WithMutationClient(mutations))
	return NewStore(requester, opts...)
}

// NewStore builds a store over a classified requester.
func NewStore(requester Requester, opts ...Option) (*Store, error) {
	if requester == nil {
		return nil, errors.New("graph store requires a requester")
	}
	store := &Store{requester: requester, timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(store)
	}
	if store.timeout <= 0 {
		return nil, errors.New("graph store requires a positive timeout")
	}
	if err := vocabulary.RegisterPredicates(); err != nil {
		return nil, fmt.Errorf("register graph projection vocabulary: %w", err)
	}
	if store.mutations == nil {
		client, ok := requester.(*natsclient.Client)
		if !ok {
			return nil, errors.New("graph store requires a canonical mutation client for a non-NATS requester")
		}
		mutations, err := projection.NewMutationClient(projection.MutationClientConfig{
			NATS: client, Contracts: projectioncontract.Contracts(), Timeout: store.timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("build graph mutation client: %w", err)
		}
		store.mutations = mutations
	}
	return store, nil
}

// CreateResult is a verified successful atomic create.
type CreateResult struct {
	Entity   *graph.EntityState
	Revision uint64
}

// CreateEntity performs an ATOMIC create-or-fail.
//
// This is the primitive the world-instantiation gate is built on, and its value
// is entirely in the atomicity: an exists-check followed by a write has a window
// in which two booting processes both decide the world is fresh and both import
// it. Concurrent creates of one ID have exactly one winner; every loser gets
// ErrEntityExists.
//
// The canonical create request carries an empty entity envelope and the birth
// facts separately. Contract validation proves every birth predicate belongs
// to the named projection before the atomic request is sent.
func (s *Store) CreateEntity(ctx context.Context, contract string, entity *graph.EntityState) (CreateResult, error) {
	if entity == nil {
		return CreateResult{}, errors.New("create requires an entity")
	}
	// Validate against the same contract the write gate applies, so a bad
	// predicate or a non-canonical ID is named here rather than arriving as a
	// remote rejection with the offending token stripped out.
	if err := graph.ValidateEntityStateContract(entity); err != nil {
		return CreateResult{}, fmt.Errorf("create entity %s: %w", entity.ID, err)
	}

	if contract == "" {
		return CreateResult{}, errors.New("create requires a projection contract")
	}
	bare := entity.Clone()
	triples := append([]message.Triple(nil), bare.Triples...)
	bare.Triples = nil
	// beta.160 reserves Triple.Context for mutation correlation: any explicit
	// context must equal Metadata.RequestID. Birth producers predate that
	// contract and used Context for domain provenance (for example a logical
	// turn ID or template@version), so normalize the request-owned COPY here.
	// The entity ID is the only stable per-entity create identity; deriving it
	// from producer context would let two entity births share one request ID.
	for index := range triples {
		triples[index].Context = entity.ID
	}
	metadata := createMetadata(entity.ID, triples)
	receipt, err := s.mutations.Create(ctx, projection.CreateMutation{
		Contract: contract, Entity: bare, Triples: triples, Metadata: metadata,
	})
	if err != nil {
		var mutationErr *projection.MutationError
		if errors.As(err, &mutationErr) && mutationErr.Kind == projection.MutationConflict {
			return CreateResult{}, fmt.Errorf("create entity %s: %w: %w", entity.ID, ErrEntityExists, err)
		}
		if errors.As(err, &mutationErr) && mutationErr.Kind == projection.MutationCommitUnknown {
			if exact, readErr := s.mutations.ReadAuthoritative(ctx, entity.ID); readErr == nil && exact != nil &&
				birthMatches(exact.Entity, bare, triples, projectioncontract.BirthPredicates(contract)) {
				return CreateResult{Entity: exact.Entity.Clone(), Revision: exact.KVRevision}, nil
			}
		}
		return CreateResult{}, fmt.Errorf("create entity %s: %w", entity.ID, err)
	}
	return CreateResult{Entity: receipt.Entity, Revision: receipt.KVRevision}, nil
}

func createMetadata(entityID string, triples []message.Triple) projection.MutationMetadata {
	metadata := projection.MutationMetadata{RequestID: entityID, Source: "semmachina-create"}
	if len(triples) == 0 {
		return metadata
	}
	metadata.Source = triples[0].Source
	metadata.Timestamp = triples[0].Timestamp
	return metadata
}

// GetEntity reads one entity through graph-ingest's query surface.
//
// An absent entity is ErrEntityNotFound, never a nil entity with a nil error:
// "there is nothing there" and "here is nothing" must not be the same value at
// a call site that decides whether a world exists.
//
// Missing relationship targets are absent authority entries in beta.160; they
// are returned as ErrEntityNotFound rather than synthesized entities.
func (s *Store) GetEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	if id == "" {
		return nil, errors.New("get requires an entity id")
	}
	exact, err := s.mutations.ReadAuthoritative(ctx, id)
	if err != nil {
		var mutationErr *projection.MutationError
		if (errors.As(err, &mutationErr) && mutationErr.Kind == projection.MutationNotFound) ||
			codeOf(err) == graph.ErrorCodeEntityNotFound {
			return nil, fmt.Errorf("get entity %s: %w: %w", id, ErrEntityNotFound, err)
		}
		return nil, fmt.Errorf("get entity %s: %w", id, err)
	}
	if exact == nil || exact.Entity == nil || exact.KVRevision == 0 {
		return nil, fmt.Errorf("get entity %s: invalid exact authority response", id)
	}
	return exact.Entity.Clone(), nil
}

// BatchResult is a many-entity read.
//
// Missing is part of the result rather than an error because a batch that came
// back short is a NORMAL outcome with a per-id explanation, and the failure it
// replaces was invisible: before ADR-084 a not-found id was simply absent from
// the list, so a caller could not tell an entity that does not exist from one
// the read did not reach — and nothing checked. A reader that ignores this field
// is a reader that will one day assemble a smaller world and not know it.
type BatchResult struct {
	// Entities are the entities that hydrated, in the order the graph returned
	// them.
	Entities []graph.EntityState
	// Missing names every requested id that is not in Entities, with a reason.
	// It is total over the request: an id the response accounted for in neither
	// list is reported as graph.MissingUnknown rather than dropped.
	Missing []graph.MissingEntity
}

// GetEntities reads many entities in one round trip.
//
// It exists so a scene-scoped read is ONE request whose size is the scene's,
// rather than N requests whose count is the scene's — the difference between a
// bounded retrieval and a fan-out that grows with the world.
func (s *Store) GetEntities(ctx context.Context, ids []string) (BatchResult, error) {
	if len(ids) == 0 {
		return BatchResult{}, nil
	}
	request, err := json.Marshal(struct {
		IDs []string `json:"ids"`
	}{IDs: ids})
	if err != nil {
		return BatchResult{}, fmt.Errorf("encode batch query for %d ids: %w", len(ids), err)
	}

	reply, err := s.requester.RequestClassified(ctx, SubjectQueryBatch, request, s.timeout)
	if err != nil {
		return BatchResult{}, fmt.Errorf("batch query for %d ids: %w", len(ids), err)
	}
	var response graph.EntityBatchResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return BatchResult{}, fmt.Errorf("decode batch query response: %w", err)
	}

	// The authoritative-state contract applies to every decoded entity, exactly
	// as it does on the single read: poisoned stored bytes become a typed
	// refusal here rather than half-usable state in a persona's context.
	//
	// Upstream's aggregate validator rather than a local loop, because the
	// failure it reports is one a local loop reports badly: the entity that
	// poisons a reply is very often the one whose ID is missing, and "entity :
	// ..." names nothing. The indexed wrapper names the POSITION in the reply,
	// which is the only handle that always exists.
	if err := graph.ValidateDecodedEntityStates(response.Entities); err != nil {
		return BatchResult{}, err
	}

	// Reconcile CLIENT-side so the answer is total over the request. Upstream
	// documents this as the caller's job (graph.MissingUnknown exists for it),
	// and without it a response that mentioned an id in neither list would
	// disappear from the accounting entirely.
	accounted := make(map[string]bool, len(response.Entities)+len(response.Missing))
	for idx := range response.Entities {
		accounted[response.Entities[idx].ID] = true
	}
	for _, missing := range response.Missing {
		accounted[missing.ID] = true
	}
	result := BatchResult{Entities: response.Entities, Missing: response.Missing}
	for _, id := range ids {
		if !accounted[id] {
			result.Missing = append(result.Missing, graph.MissingEntity{ID: id, Reason: graph.MissingUnknown})
			accounted[id] = true
		}
	}
	return result, nil
}

// PrefixPage is one page of an entity-ID prefix enumeration.
type PrefixPage struct {
	// Entities are the entities on this page, whole, sorted by ID.
	Entities []graph.EntityState
	// NextCursor continues the enumeration. Empty means exhausted.
	//
	// It must be FOLLOWED, not treated as an optimization: upstream trims a
	// page against a response byte budget as well as a count limit, so a caller
	// that read one page and stopped would silently see a prefix of the world
	// and call it the whole thing.
	NextCursor string
}

// EntitiesWithPrefix enumerates entities under a dot-delimited entity-ID prefix,
// one page at a time.
//
// The prefix carries NO trailing dot — upstream appends one when it filters, so
// supplying it would look for a segment named "". Passing a full six-part ID is
// legal and returns that single entity.
//
// cursor is empty for the first page and thereafter the previous page's
// NextCursor; limit <= 0 takes the server default. Pages are sorted by entity
// ID, which is what makes the cursor meaningful at all.
func (s *Store) EntitiesWithPrefix(
	ctx context.Context,
	prefix, cursor string,
	limit int,
) (PrefixPage, error) {
	if prefix == "" {
		return PrefixPage{}, errors.New(
			"prefix query requires a prefix; an empty one enumerates every entity in the world, which is a " +
				"scan wearing a query's clothes")
	}
	request, err := json.Marshal(graph.PrefixQueryRequest{Prefix: prefix, Cursor: cursor, Limit: limit})
	if err != nil {
		return PrefixPage{}, fmt.Errorf("encode prefix query for %s: %w", prefix, err)
	}

	reply, err := s.requester.RequestClassified(ctx, SubjectQueryPrefix, request, s.timeout)
	if err != nil {
		return PrefixPage{}, fmt.Errorf("prefix query for %s: %w", prefix, err)
	}
	var response graph.PrefixQueryResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return PrefixPage{}, fmt.Errorf("decode prefix query response for %s: %w", prefix, err)
	}
	// The authoritative-state contract applies to every decoded entity here
	// exactly as it does on the single and batch reads.
	if err := graph.ValidateDecodedEntityStates(response.Entities); err != nil {
		return PrefixPage{}, err
	}
	return PrefixPage{Entities: response.Entities, NextCursor: response.NextCursor}, nil
}

// IncomingRelationships returns every edge pointing AT an entity.
//
// This is the reverse lookup the graph itself cannot answer: a triple lives on
// its subject, so "who points at this target" is not readable from the target.
// graph-index maintains that direction, and asking it is the alternative to
// scanning the world — which would be a hand-rolled index over data the
// substrate already indexes.
//
// The result is EVERY incoming edge, not only the ones a caller cares about:
// filtering by predicate is the caller's, because which predicates constitute
// membership is game vocabulary and this client is deliberately vocabulary-blind.
func (s *Store) IncomingRelationships(ctx context.Context, entityID string) ([]graph.IncomingEntry, error) {
	if entityID == "" {
		return nil, errors.New("incoming query requires an entity id")
	}
	request, err := json.Marshal(struct {
		EntityID string `json:"entity_id"`
	}{EntityID: entityID})
	if err != nil {
		return nil, fmt.Errorf("encode incoming query for %s: %w", entityID, err)
	}

	reply, err := s.requester.RequestClassified(ctx, SubjectIndexQueryIncoming, request, s.timeout)
	if err != nil {
		if codeOf(err) == graph.ErrorCodeIndexNotReady {
			return nil, fmt.Errorf("incoming edges of %s: %w: %w", entityID, ErrIndexNotReady, err)
		}
		return nil, fmt.Errorf("incoming edges of %s: %w", entityID, err)
	}
	var response graph.IncomingQueryResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return nil, fmt.Errorf("decode incoming edges of %s: %w", entityID, err)
	}
	return response.Data.Relationships, nil
}

// EntitiesByPredicateValue returns entity IDs carrying an exact string object
// for predicate through graph-index's NATS-direct query surface.
//
// The current upstream value filter first reads predicate-index membership and
// then scans those candidate ENTITY_STATES records until limit matches. The
// public call is bounded and stops early, but it is not a native value index;
// callers must keep limits small and must not mistake this for world-size-flat
// retrieval. This local reader does not add a substrate workaround or cache.
func (s *Store) EntitiesByPredicateValue(
	ctx context.Context,
	predicate, value string,
	limit int,
) ([]string, error) {
	if predicate == "" {
		return nil, errors.New("predicate-value query requires a predicate")
	}
	if value == "" {
		return nil, errors.New("predicate-value query requires a value")
	}
	if limit <= 0 {
		return nil, errors.New("predicate-value query requires a positive limit")
	}
	request, err := json.Marshal(struct {
		Predicate string  `json:"predicate"`
		Value     *string `json:"value"`
		Limit     int     `json:"limit"`
	}{Predicate: predicate, Value: &value, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("encode predicate-value query for %s: %w", predicate, err)
	}
	reply, err := s.requester.RequestClassified(ctx, SubjectIndexQueryPredicate, request, s.timeout)
	if err != nil {
		return nil, fmt.Errorf("predicate-value query for %s: %w", predicate, err)
	}
	var response graph.PredicateQueryResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return nil, fmt.Errorf("decode predicate-value query for %s: %w", predicate, err)
	}
	if len(response.Data.Entities) > limit {
		return nil, fmt.Errorf("predicate-value query for %s returned %d entities; hard limit is %d",
			predicate, len(response.Data.Entities), limit)
	}
	return response.Data.Entities, nil
}

// Reconcile makes one declared predicate group equal the complete desired set.
// An empty desired slice is an intentional clear. Revision conflicts and
// commit-unknown errors are returned unchanged for caller-level reread and
// recomputation; Store never retries a mutation blindly.
func (s *Store) Reconcile(ctx context.Context, target projectioncontract.Target, entityID string, desired []message.Triple) (*graph.EntityState, error) {
	if entityID == "" {
		return nil, errors.New("reconcile requires an entity id")
	}
	if target.Contract == "" || target.Group == "" {
		return nil, errors.New("reconcile requires a projection contract and group")
	}
	receipt, err := s.mutations.Reconcile(ctx, projection.ReconcileMutation{
		Contract: target.Contract, Group: target.Group, EntityID: entityID, Desired: desired,
	})
	if err != nil {
		var mutationErr *projection.MutationError
		if errors.As(err, &mutationErr) && mutationErr.Kind == projection.MutationNotFound {
			return nil, fmt.Errorf("reconcile %s/%s into %s: %w: %w", target.Contract, target.Group, entityID, ErrEntityNotFound, err)
		}
		if errors.As(err, &mutationErr) && mutationErr.Kind == projection.MutationCommitUnknown {
			if exact, readErr := s.mutations.ReadAuthoritative(ctx, entityID); readErr == nil && exact != nil &&
				groupMatches(exact.Entity, desired, projectioncontract.Predicates(target)) {
				return exact.Entity.Clone(), nil
			}
		}
		return nil, fmt.Errorf("reconcile %s/%s into %s: %w", target.Contract, target.Group, entityID, err)
	}
	if receipt.Entity == nil {
		return nil, fmt.Errorf("reconcile %s/%s into %s returned no entity", target.Contract, target.Group, entityID)
	}
	return receipt.Entity.Clone(), nil
}

func birthMatches(got, want *graph.EntityState, desired []message.Triple, predicates []string) bool {
	if !createEnvelopeMatches(got, want) {
		return false
	}
	allowed := make(map[string]bool, len(predicates))
	for _, predicate := range predicates {
		allowed[predicate] = true
	}
	for _, triple := range desired {
		if !allowed[triple.Predicate] {
			return false
		}
	}
	resident := make([]message.Triple, 0, len(got.Triples))
	indexingFacts := 0
	for _, triple := range got.Triples {
		// graph-ingest injects exactly this operational fact at the canonical
		// create seam. It is not caller-owned projection state.
		if triple.Predicate == ssvocab.EntityIndexingProfile {
			indexingFacts++
			profile, validProfile := triple.Object.(string)
			if indexingFacts != 1 || triple.Subject != got.ID ||
				triple.Source != "graph-ingest-indexing-profile" || triple.Timestamp.IsZero() ||
				triple.Confidence != 1 || triple.Context != "" || triple.Datatype != "" ||
				triple.ExpiresAt != nil || !validProfile || !ssvocab.IsValidIndexingProfile(profile) {
				return false
			}
			continue
		}
		resident = append(resident, triple)
	}
	return indexingFacts == 1 && triplesEqual(resident, desired)
}

func createEnvelopeMatches(got, want *graph.EntityState) bool {
	if got == nil || want == nil || got.ID != want.ID || !got.MessageType.Equal(want.MessageType) ||
		!reflect.DeepEqual(got.StorageRef, want.StorageRef) {
		return false
	}
	// The canonical create handler supplies these defaults only when the
	// caller leaves them zero; nonzero caller values are preserved verbatim.
	if want.Version == 0 {
		if got.Version != 1 {
			return false
		}
	} else if got.Version != want.Version {
		return false
	}
	if want.UpdatedAt.IsZero() {
		if got.UpdatedAt.IsZero() {
			return false
		}
	} else if !got.UpdatedAt.Equal(want.UpdatedAt) {
		return false
	}
	return true
}

func groupMatches(state *graph.EntityState, desired []message.Triple, predicates []string) bool {
	if state == nil || len(predicates) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(predicates))
	for _, predicate := range predicates {
		allowed[predicate] = true
	}
	resident := make([]message.Triple, 0, len(desired))
	for _, triple := range state.Triples {
		if allowed[triple.Predicate] {
			resident = append(resident, triple)
		}
	}
	return triplesEqual(resident, desired)
}

func triplesEqual(resident, desired []message.Triple) bool {
	resident = append([]message.Triple(nil), resident...)
	want := append([]message.Triple(nil), desired...)
	order := func(a, b message.Triple) int {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return stringCompare(string(left), string(right))
	}
	slices.SortFunc(resident, order)
	slices.SortFunc(want, order)
	return reflect.DeepEqual(resident, want)
}

func stringCompare(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

// codeOf returns the stable machine code from a classified error, or "".
//
// The code is the contract (ADR-060); the message is prose that may change. A
// caller branching on the message would branch on documentation.
func codeOf(err error) string {
	var classified *errs.ClassifiedError
	if errors.As(err, &classified) {
		return classified.Code
	}
	return ""
}
