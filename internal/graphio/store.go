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
//   - `entity.create` is atomic create-or-fail. That is what closes the
//     exists-check-then-write TOCTOU, and it is the whole mechanism behind the
//     world-instantiation gate.
//   - `triple.add` / `triple.add_batch` APPEND; only the entity update lane
//     MERGES by (subject, predicate). Every single-valued predicate the engine
//     writes — turn phase, roll band, roll total — must travel the merge lane,
//     or a duplicate delivery leaves an entity holding two values for a
//     single-valued fact and no error anywhere.
//
// graph-ingest remains the sole ENTITY_STATES writer: everything here is a
// request to it, never a write around it.
package graphio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
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
	// SubjectEntityCreate is the atomic create-or-fail mutation.
	SubjectEntityCreate = "graph.mutation.entity.create"
	// SubjectEntityUpdateWithTriples is the must-exist merge lane.
	SubjectEntityUpdateWithTriples = "graph.mutation.entity.update_with_triples"
	// SubjectQueryEntity is the single-entity read.
	SubjectQueryEntity = "graph.ingest.query.entity"
	// SubjectQueryBatch is the many-entities read. One round trip for a set of
	// known ids, with per-id omissions reported rather than silently shortening
	// the list (ADR-084).
	SubjectQueryBatch = "graph.ingest.query.batch"
	// SubjectIndexQueryIncoming is graph-index's reverse-edge read: every
	// (source, predicate) pointing AT an entity.
	//
	// It is served by graph-index rather than graph-ingest, which is why it is
	// the one subject here in a different namespace. The engine needs it because
	// membership in a scene is asserted by the MEMBER — a character carries
	// world.location.current pointing at the scene, and the scene says nothing
	// about who is in it — so "who is here" is a reverse lookup, and reverse
	// lookups are what that index exists for. Scanning the world and filtering
	// would be a hand-rolled index over data the substrate already indexes (M6).
	SubjectIndexQueryIncoming = "graph.index.query.incoming"
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
	timeout   time.Duration
}

// Option configures a Store.
type Option func(*Store)

// WithTimeout overrides the per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(s *Store) { s.timeout = d }
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
	return store, nil
}

// CreateResult is a successful atomic create.
//
// Degraded is not an error and must not be retried: the write COMMITTED and
// only the post-write read-back failed (#120). A retry would come back as
// entity_already_exists and a caller treating that as "somebody else got here
// first" would draw exactly the wrong conclusion about a world it just created.
type CreateResult struct {
	// Entity is the stored entity as graph-ingest read it back, including any
	// framework-injected triples. Nil when Degraded.
	Entity *graph.EntityState
	// Revision is the KV revision after the write. Zero when Degraded.
	Revision uint64
	// Degraded reports a committed write whose read-back failed.
	Degraded bool
	// DegradedReason carries the read-back failure.
	DegradedReason string
}

// CreateEntity performs an ATOMIC create-or-fail.
//
// This is the primitive the world-instantiation gate is built on, and its value
// is entirely in the atomicity: an exists-check followed by a write has a window
// in which two booting processes both decide the world is fresh and both import
// it. Concurrent creates of one ID have exactly one winner; every loser gets
// ErrEntityExists.
//
// It deliberately uses `entity.create` rather than `entity.create_with_triples`,
// which carries the same shape plus a stub re-stamp path: a create_with_triples
// landing on a referential stub SUCCEEDS by merging into it. That is right for
// an entity being born late, and wrong for a sentinel — the gate must report
// "already there" for any resident at that key, so the caller can look and
// decide, rather than being told the world is fresh because the resident was
// only half real.
func (s *Store) CreateEntity(ctx context.Context, entity *graph.EntityState) (CreateResult, error) {
	if entity == nil {
		return CreateResult{}, errors.New("create requires an entity")
	}
	// Validate against the same contract the write gate applies, so a bad
	// predicate or a non-canonical ID is named here rather than arriving as a
	// remote rejection with the offending token stripped out.
	if err := graph.ValidateEntityStateContract(entity); err != nil {
		return CreateResult{}, fmt.Errorf("create entity %s: %w", entity.ID, err)
	}

	request, err := json.Marshal(graph.CreateEntityRequest{Entity: entity})
	if err != nil {
		return CreateResult{}, fmt.Errorf("encode create request for %s: %w", entity.ID, err)
	}

	reply, err := s.requester.RequestClassified(ctx, SubjectEntityCreate, request, s.timeout)
	if err != nil {
		if codeOf(err) == graph.ErrorCodeEntityExists {
			return CreateResult{}, fmt.Errorf("create entity %s: %w: %w", entity.ID, ErrEntityExists, err)
		}
		return CreateResult{}, fmt.Errorf("create entity %s: %w", entity.ID, err)
	}

	var response graph.CreateEntityResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return CreateResult{}, fmt.Errorf("decode create response for %s: %w", entity.ID, err)
	}
	if response.Degraded {
		return CreateResult{Degraded: true, DegradedReason: response.DegradedReason}, nil
	}
	return CreateResult{Entity: response.Entity, Revision: response.KVRevision}, nil
}

// GetEntity reads one entity through graph-ingest's query surface.
//
// An absent entity is ErrEntityNotFound, never a nil entity with a nil error:
// "there is nothing there" and "here is nothing" must not be the same value at
// a call site that decides whether a world exists.
//
// The returned entity may be a referential STUB — queryable, carrying only
// core.identity.* markers and none of its own facts — so a caller asking "is
// this loaded?" must check graph.EntityState.IsStub() and not merely that the
// read succeeded.
func (s *Store) GetEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	if id == "" {
		return nil, errors.New("get requires an entity id")
	}
	request, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	if err != nil {
		return nil, fmt.Errorf("encode query for %s: %w", id, err)
	}

	reply, err := s.requester.RequestClassified(ctx, SubjectQueryEntity, request, s.timeout)
	if err != nil {
		if codeOf(err) == graph.ErrorCodeEntityNotFound {
			return nil, fmt.Errorf("get entity %s: %w: %w", id, ErrEntityNotFound, err)
		}
		return nil, fmt.Errorf("get entity %s: %w", id, err)
	}

	var state graph.EntityState
	if err := json.Unmarshal(reply, &state); err != nil {
		return nil, fmt.Errorf("decode entity %s: %w", id, err)
	}
	// The authoritative-state contract applies to anything decoding an
	// EntityState off the wire: poisoned stored bytes become a typed refusal
	// here rather than half-usable state downstream.
	if err := graph.ValidateDecodedEntityState(&state); err != nil {
		return nil, fmt.Errorf("entity %s: %w", id, err)
	}
	return &state, nil
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

// IncomingRelationships returns every edge pointing AT an entity.
//
// This is the reverse lookup the graph itself cannot answer: a triple lives on
// its subject, so "who points at this scene" is not readable from the scene.
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

// MergeOption adjusts one merge request.
type MergeOption func(*graph.UpdateEntityWithTriplesRequest)

// WithClearedPredicates deletes every value of the named predicates before the
// merge applies.
//
// It exists for the one thing the merge lane cannot otherwise express: an EMPTY
// value set. AddTriples replaces a predicate's values with the ones it carries,
// so carrying none leaves the predicate exactly as it was — which means "the
// character put down the last thing they were carrying" would silently commit
// as "the character is still carrying it", with a success response. Upstream
// applies removals BEFORE the merge, so clearing and re-adding in one request is
// well defined.
func WithClearedPredicates(predicates ...string) MergeOption {
	return func(request *graph.UpdateEntityWithTriplesRequest) {
		request.RemoveTriples = append(request.RemoveTriples, predicates...)
	}
}

// MergeTriples writes triples with REPLACE-by-(subject, predicate) semantics on
// an entity that must already exist.
//
// This is the lane every single-valued engine predicate takes. The alternative,
// triple.add_batch, APPENDS: writing turn.roll.band twice through it leaves the
// turn holding two bands, both "correct", with no error raised — the exact
// silent divergence at-most-one-roll-per-turn exists to prevent. Choosing
// between the two lanes is not a performance decision.
//
// Replace is per (subject, predicate), which makes it a trap for a MULTI-valued
// predicate as surely as it is the right lane for a single-valued one. Upstream
// states it plainly (graph.UpdateEntityWithTriplesRequest.AddTriples): "For a
// MULTI-valued predicate, send the FULL desired set — a partial set drops the
// omitted siblings." So adding one carried item while sending only the new one
// deletes every other carried item and returns success. This client is
// deliberately vocabulary-agnostic — it cannot know which of a caller's
// predicates are multi-valued — so knowing the difference is the caller's job,
// and WithClearedPredicates is the only way to say "this set is now empty".
//
// The request carries a bare EntityState{ID}: MessageType, Version, and
// StorageRef are preserve-when-zero upstream, so sending zeroes keeps the stored
// envelope instead of erasing the entity's provenance and its indexing profile.
//
// Every triple must target entityID. A foreign subject would not fail — it would
// be split off and routed onto its own entity, on the APPENDING lane, with the
// failure logged and not returned — so a typo would file a turn's roll on some
// other entity and report success. The merge lane is per-entity: a write
// touching N entities is N calls, each with its own classified error.
func (s *Store) MergeTriples(
	ctx context.Context,
	entityID string,
	triples []message.Triple,
	opts ...MergeOption,
) (*graph.EntityState, error) {
	if entityID == "" {
		return nil, errors.New("merge requires an entity id")
	}

	body := graph.UpdateEntityWithTriplesRequest{
		Entity:     &graph.EntityState{ID: entityID},
		AddTriples: triples,
	}
	for _, opt := range opts {
		opt(&body)
	}
	// Both checks run AFTER the options, over the request as it will actually
	// travel. Validating the triples parameter instead would validate a
	// different value than it transmits — an option appending to AddTriples
	// would walk straight past a guard whose whole reason for existing is that
	// the failure it prevents is silent. And a clear-only merge carries no
	// triples and is a legitimate write, while a request that neither adds nor
	// clears is a round trip whose success proves nothing.
	for idx := range body.AddTriples {
		if body.AddTriples[idx].Subject != entityID {
			return nil, fmt.Errorf(
				"triple %d targets %q, not %q; a foreign subject is routed to its own entity rather than rejected",
				idx, body.AddTriples[idx].Subject, entityID)
		}
	}
	if len(body.AddTriples) == 0 && len(body.RemoveTriples) == 0 {
		return nil, fmt.Errorf("merge into %s carries no triples and clears no predicates", entityID)
	}

	request, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode merge request for %s: %w", entityID, err)
	}

	reply, err := s.requester.RequestClassified(ctx, SubjectEntityUpdateWithTriples, request, s.timeout)
	if err != nil {
		if codeOf(err) == graph.ErrorCodeEntityNotFound {
			return nil, fmt.Errorf("merge into %s: %w: %w", entityID, ErrEntityNotFound, err)
		}
		return nil, fmt.Errorf("merge into %s: %w", entityID, err)
	}

	var response graph.UpdateEntityWithTriplesResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return nil, fmt.Errorf("decode merge response for %s: %w", entityID, err)
	}
	return response.Entity, nil
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
