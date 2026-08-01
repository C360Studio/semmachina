package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/storage"
	"github.com/c360studio/semstreams/storage/objectstore"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The engine's content store coordinates.
const (
	// DefaultBucket is the ObjectStore bucket every turn artifact lands in.
	// One bucket, because instance-per-world means one campaign per stack and
	// the key space is already partitioned by turn.
	DefaultBucket = "SEMMACHINA_CONTENT"
	// ContentType is stamped on every reference this store produces. Every
	// artifact is canonical JSON.
	ContentType = "application/json"
	// MaxInstanceNameBytes bounds the storage instance name.
	//
	// It is what makes "a reference always fits a triple object" a property
	// rather than a hope: the reference is scheme + instance + key, the key is
	// bounded by the turn-id segment budget, and this is the only remaining
	// free variable. Without it an operator could name a bucket long enough to
	// push every reference past the triple-object budget, and the first symptom
	// would be turns failing at accept.
	MaxInstanceNameBytes = 32
	// MaxCompanionDecisionBytes bounds the full canonical value stored in the
	// immutable claim. The payload's bounded IDs and eight-reference ceiling fit
	// below this cap with room for JSON field names.
	MaxCompanionDecisionBytes = 8 << 10
)

// ErrArtifactNotFound reports a reference that resolved to no object.
//
// It is a distinct sentinel because the two ways a read comes back empty mean
// opposite things. A missing object under a reference the graph carries is a
// CORRECTNESS bug — the write ordering this package exists to enforce says a
// reference is only ever committed after its object — while a transport fault
// is a retry. A caller that cannot tell them apart retries the bug forever.
var ErrArtifactNotFound = errors.New("stored artifact not found")

// ErrArtifactReference reports a malformed, foreign-instance, or wrong-slot
// reference. Retrying the same reference cannot repair it.
var ErrArtifactReference = errors.New("invalid stored artifact reference")

// ErrArtifactCorrupt reports bytes that cannot decode or validate as the
// artifact named by their slot. Retrying cannot change resident bytes.
var ErrArtifactCorrupt = errors.New("stored artifact is corrupt")

// ErrArtifactConflict reports deterministic identity already occupied by different bytes.
var ErrArtifactConflict = errors.New("stored artifact identity conflict")

// Backend is the storage surface this package needs.
//
// It is deliberately three methods. The engine stores whole artifacts under
// derived keys and reads them back whole; it does not stream, list, or delete,
// and a wider interface here would be a wider surface for a component to reach
// around the key derivation with.
//
// InstanceName is part of the interface rather than a constructor argument so a
// reference cannot be stamped with an instance the store does not answer to:
// the value on the reference comes from the store that wrote the bytes, always.
type Backend interface {
	// InstanceName is the storage instance this backend answers to — the value
	// a resolver maps back to it (ADR-063).
	InstanceName() string
	// Put stores data at the key, replacing any prior object there.
	Put(ctx context.Context, key string, data []byte) error
	// Get reads the object at the key, reporting an absent one as
	// storage.ErrObjectNotFound.
	Get(ctx context.Context, key string) ([]byte, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Backend = (*objectstore.Store)(nil)

// ExactResidentBackend atomically establishes one canonical value for a key.
// It returns the winning value, repairing its ordinary object copy before
// return. Store compares the caller's candidate with that winner.
type ExactResidentBackend interface {
	ClaimExact(context.Context, string, []byte) ([]byte, error)
}

// ObjectBackend adds exact-resident claims to the ordinary content ObjectStore.
type ObjectBackend struct {
	store    *objectstore.Store
	claims   jetstream.KeyValue
	instance string
}

// InstanceName returns the storage instance carried by references from this backend.
func (b *ObjectBackend) InstanceName() string { return b.instance }

// Put replaces an ordinary, non-exact artifact at key.
func (b *ObjectBackend) Put(ctx context.Context, key string, data []byte) error {
	return b.store.Put(ctx, key, data)
}

// Get reads one artifact from the underlying ObjectStore.
func (b *ObjectBackend) Get(ctx context.Context, key string) ([]byte, error) {
	return b.store.Get(ctx, key)
}

// List exposes the existing integration diagnostic surface without making it
// part of the production Backend contract.
func (b *ObjectBackend) List(ctx context.Context, prefix string) ([]string, error) {
	return b.store.List(ctx, prefix)
}

// Close releases the underlying ObjectStore resources.
func (b *ObjectBackend) Close() error { return b.store.Close() }

// ClaimExact uses KV Create as the linearization point. An existing claim is
// authoritative: its full bytes repair a missing object copy, and an object
// inconsistent with it fails closed without either value being overwritten.
func (b *ObjectBackend) ClaimExact(ctx context.Context, key string, candidate []byte) ([]byte, error) {
	if len(candidate) > MaxCompanionDecisionBytes {
		return nil, fmt.Errorf("immutable companion decision is %d bytes; limit is %d",
			len(candidate), MaxCompanionDecisionBytes)
	}
	var winner []byte
	if _, err := b.claims.Create(ctx, key, candidate); err == nil {
		winner = bytes.Clone(candidate)
	} else if !errors.Is(err, jetstream.ErrKeyExists) {
		return nil, fmt.Errorf("create exact-resident claim %q: %w", key, err)
	} else {
		entry, err := b.claims.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read exact-resident claim %q: %w", key, err)
		}
		winner = bytes.Clone(entry.Value())
	}
	resident, err := b.store.Get(ctx, key)
	if errors.Is(err, storage.ErrObjectNotFound) {
		if err := b.store.Put(ctx, key, winner); err != nil {
			return nil, fmt.Errorf("repair exact-resident object %q: %w", key, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read exact-resident object %q: %w", key, err)
	} else if !bytes.Equal(resident, winner) {
		return nil, fmt.Errorf("%w: object %q differs from its immutable claim", ErrArtifactCorrupt, key)
	}
	return winner, nil
}

// ObjectStoreOption configures the engine's ObjectStore backend.
type ObjectStoreOption func(*objectstore.Config)

// WithBucket overrides the ObjectStore bucket (and therefore the storage
// instance every reference names).
func WithBucket(bucket string) ObjectStoreOption {
	return func(cfg *objectstore.Config) {
		cfg.BucketName = bucket
		cfg.InstanceName = bucket
	}
}

// NewObjectStore builds the engine's ObjectStore backend.
//
// The cache is left DISABLED, which is a deliberate reversal of the upstream
// default. This store's entire value is durability across a crash, and a cached
// read is a read of this process's own memory: a test — or a recovery path —
// that "found the artifact" out of cache has proved the store remembers what it
// just wrote, which is the one thing that was never in question. Artifacts are
// read a handful of times per turn, so the round trip is not the cost that
// matters here.
func NewObjectStore(
	ctx context.Context,
	client *natsclient.Client,
	opts ...ObjectStoreOption,
) (*ObjectBackend, error) {
	if client == nil {
		return nil, errors.New("content object store requires a NATS client")
	}
	cfg := objectstore.Config{BucketName: DefaultBucket, InstanceName: DefaultBucket}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := validateInstanceName(cfg.InstanceName); err != nil {
		return nil, err
	}
	store, err := objectstore.NewStoreWithConfig(ctx, client, cfg)
	if err != nil {
		return nil, fmt.Errorf("open content object store %q: %w", cfg.BucketName, err)
	}
	claims, err := openExactClaims(ctx, client, cfg.BucketName)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &ObjectBackend{store: store, claims: claims, instance: cfg.InstanceName}, nil
}

func openExactClaims(ctx context.Context, client *natsclient.Client, contentBucket string) (jetstream.KeyValue, error) {
	js, err := client.JetStream()
	if err != nil {
		return nil, err
	}
	objects, err := js.ObjectStore(ctx, contentBucket)
	if err != nil {
		return nil, fmt.Errorf("inspect content ObjectStore %q: %w", contentBucket, err)
	}
	objectStatus, err := objects.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect content ObjectStore status %q: %w", contentBucket, err)
	}
	if objectStatus.Storage() != jetstream.FileStorage {
		return nil, fmt.Errorf("content ObjectStore %q is not file-backed", contentBucket)
	}
	name := contentBucket + "_IMMUTABLE"
	claims, err := js.KeyValue(ctx, name)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		claims, err = js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: name, Description: "Exact-resident companion decision claims",
			MaxValueSize: MaxCompanionDecisionBytes, History: 1, TTL: 0,
			Storage: jetstream.FileStorage, Replicas: objectStatus.Replicas(),
		})
		if errors.Is(err, jetstream.ErrBucketExists) {
			claims, err = js.KeyValue(ctx, name)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open exact-resident claim bucket %q: %w", name, err)
	}
	status, err := claims.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect exact-resident claim bucket %q: %w", name, err)
	}
	stream, err := js.Stream(ctx, "KV_"+name)
	if err != nil {
		return nil, fmt.Errorf("inspect exact-resident claim stream %q: %w", name, err)
	}
	info := stream.CachedInfo()
	if status.History() != 1 || status.TTL() != 0 || info.Config.Storage != jetstream.FileStorage ||
		info.Config.Replicas != objectStatus.Replicas() || info.Config.MaxMsgSize != MaxCompanionDecisionBytes {
		return nil, fmt.Errorf("exact-resident claim bucket %q has incompatible configuration: "+
			"history=%d ttl=%s storage=%s replicas=%d max_value=%d; want 1, 0, %s, %d, %d",
			name, status.History(), status.TTL(), info.Config.Storage, info.Config.Replicas,
			info.Config.MaxMsgSize, jetstream.FileStorage, objectStatus.Replicas(), MaxCompanionDecisionBytes)
	}
	return claims, nil
}

// validateInstanceName holds the bound the reference budget depends on.
func validateInstanceName(name string) error {
	if name == "" {
		return errors.New("content store requires a storage instance name; a reference that names no instance " +
			"cannot be resolved by anything")
	}
	if len(name) > MaxInstanceNameBytes {
		return fmt.Errorf(
			"storage instance %q is %d bytes, which exceeds the %d-byte budget every reference has to fit "+
				"inside the %d-byte triple-object limit",
			name, len(name), MaxInstanceNameBytes, payload.MaxTripleObjectBytes)
	}
	if strings.Contains(name, refSeparator) {
		return fmt.Errorf("storage instance %q contains %q and could not be read back out of a reference",
			name, refSeparator)
	}
	return nil
}

// Store reads and writes turn artifacts under derived keys.
type Store struct {
	backend Backend
}

// NewStore builds the content store over a backend.
func NewStore(backend Backend) (*Store, error) {
	if backend == nil {
		return nil, errors.New("content store requires a storage backend")
	}
	if err := validateInstanceName(backend.InstanceName()); err != nil {
		return nil, err
	}
	return &Store{backend: backend}, nil
}

// InstanceName is the storage instance every reference from this store names.
func (s *Store) InstanceName() string { return s.backend.InstanceName() }

// Close releases the backend when it owns a resource worth releasing.
func (s *Store) Close() error {
	if closer, ok := s.backend.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// PutAction stores the canonical player action and returns the reference the
// turn entity will carry.
//
// The WHOLE action is stored, not just its text. Two of its fields have no
// other durable home once the stream message is acknowledged:
//
//   - Channel is the PROVENANCE of the submission: which adapter it arrived
//     through and what address it named at the time. Whether that address is
//     still worth anything is PER ADAPTER, and this is the field the distinction
//     hangs on, so it is stated here rather than left to a reader to infer.
//     For a WebSocket, ReplyTo is a connection identifier and is a DELIVERY HINT
//     ONLY: it is invalid the moment the socket drops, and nothing may dial it.
//     For an adapter with a durable address — an email box, a chat channel — it
//     IS an address, and an adapter that has one may deliver to it. Targeted
//     egress therefore resolves the live target from PLAYER ID at delivery time
//     (internal/egress) and never from this field; an adapter whose transport has
//     no live session table is the only kind that reads it as an address.
//   - ArrivedAt is when the player acted. Deadline evaluation uses the action's
//     ARRIVAL time and never processing time, so a turn that sat in a queue
//     must not miss a deadline it made — and the arrival stamp has to survive
//     the queue for that to be checkable.
//
// turnEntityID is not part of the key (see KeyFor); it is here because this is
// a seam where a turn is named twice, and RequireTurnEntityID is what stops one
// turn's action being stored under another turn's paperwork.
func (s *Store) PutAction(
	ctx context.Context,
	turnEntityID string,
	action *payload.PlayerAction,
) (Ref, error) {
	if action == nil {
		return Ref{}, errors.New("storing an action requires an action")
	}
	turnID := payload.TurnIDForAction(action.ActionID)
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnActionRef, SubjectTurn, turnID, action)
}

// GetAction reads a stored action back.
func (s *Store) GetAction(ctx context.Context, ref Ref) (*payload.PlayerAction, error) {
	action := &payload.PlayerAction{}
	if err := s.get(ctx, vocabulary.TurnActionRef, ref, action); err != nil {
		return nil, err
	}
	return action, nil
}

// PutVerdict stores the adjudicator's verdict and returns the reference the
// turn entity will carry.
//
// The banded intent set is why this exists. A verdict's rule-matched half is
// four scalars; the rest — three bands of proposed effect intents, the typed
// modifiers, the rationale — is bulky, is read only by the applier and the
// resolution card, and would put LLM-authored structure on the graph's
// rule-matching surface if it travelled any other way (F6).
//
// It is called BEFORE the reference is committed to the turn, for the same
// reason PutAction is: a reference to a verdict nobody stored is a turn the
// dice and the applier cannot resolve, while a stored verdict no turn points at
// is garbage a retry overwrites at the identical derived key.
func (s *Store) PutVerdict(
	ctx context.Context,
	turnEntityID string,
	verdict *payload.Verdict,
) (Ref, error) {
	if verdict == nil {
		return Ref{}, errors.New("storing a verdict requires a verdict")
	}
	if err := payload.RequireTurnEntityID(verdict.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnVerdictRef, SubjectTurn, verdict.TurnID, verdict)
}

// GetVerdict reads a stored verdict back.
func (s *Store) GetVerdict(ctx context.Context, ref Ref) (*payload.Verdict, error) {
	verdict := &payload.Verdict{}
	if err := s.get(ctx, vocabulary.TurnVerdictRef, ref, verdict); err != nil {
		return nil, err
	}
	return verdict, nil
}

// PutCaseDecisionRecord stores the private interpretation artifact before its
// reference is eligible to land on the turn entity.
func (s *Store) PutCaseDecisionRecord(
	ctx context.Context,
	turnEntityID string,
	record *payload.CaseDecisionRecord,
) (Ref, error) {
	if record == nil {
		return Ref{}, errors.New("storing a case decision record requires a record")
	}
	if err := payload.RequireTurnEntityID(record.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnCaseDecisionRef, SubjectTurn, record.TurnID, record)
}

// GetCaseDecisionRecord reads a stored interpretation artifact back.
func (s *Store) GetCaseDecisionRecord(ctx context.Context, ref Ref) (*payload.CaseDecisionRecord, error) {
	record := &payload.CaseDecisionRecord{}
	if err := s.get(ctx, vocabulary.TurnCaseDecisionRef, ref, record); err != nil {
		return nil, err
	}
	return record, nil
}

// PutCompanionDecision stores the structural companion exit with exact-resident
// idempotency. A retry of equal bytes succeeds; the same deterministic identity
// carrying different semantics is an integrity conflict and is never overwritten.
func (s *Store) PutCompanionDecision(
	ctx context.Context, turnEntityID string, decision *payload.CompanionDecision,
) (Ref, error) {
	if decision == nil {
		return Ref{}, errors.New("storing a companion decision requires a decision")
	}
	if err := payload.RequireTurnEntityID(decision.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	if err := decision.Validate(); err != nil {
		return Ref{}, fmt.Errorf("refusing to store an invalid companion decision: %w", err)
	}
	key, err := KeyFor(vocabulary.TurnCompanionDecisionRef, SubjectTurn, decision.TurnID)
	if err != nil {
		return Ref{}, err
	}
	ref := Ref{Instance: s.backend.InstanceName(), Key: key}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	data, err := json.Marshal(decision)
	if err != nil {
		return Ref{}, fmt.Errorf("encode companion decision: %w", err)
	}
	exact, ok := s.backend.(ExactResidentBackend)
	if !ok {
		return Ref{}, errors.New("content backend does not support exact-resident companion decisions")
	}
	winner, err := exact.ClaimExact(ctx, key, data)
	if err != nil {
		return Ref{}, fmt.Errorf("establish companion decision at %s: %w", ref, err)
	}
	if !bytes.Equal(winner, data) {
		return Ref{}, fmt.Errorf("%w: companion decision %s already carries different semantics",
			ErrArtifactConflict, decision.DecisionID)
	}
	return ref, nil
}

// GetCompanionDecision reads a structural companion exit.
func (s *Store) GetCompanionDecision(ctx context.Context, ref Ref) (*payload.CompanionDecision, error) {
	decision := &payload.CompanionDecision{}
	if err := s.get(ctx, vocabulary.TurnCompanionDecisionRef, ref, decision); err != nil {
		return nil, err
	}
	return decision, nil
}

// PutAccusationRecord stores the universal barrier artifact before its
// reference lands on the turn.
func (s *Store) PutAccusationRecord(
	ctx context.Context, turnEntityID string, record *AccusationRecord,
) (Ref, error) {
	if record == nil {
		return Ref{}, errors.New("storing an accusation record requires a record")
	}
	if err := payload.RequireTurnEntityID(record.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnAccusationRef, SubjectTurn, record.TurnID, record)
}

// GetAccusationRecord reads one universal barrier artifact.
func (s *Store) GetAccusationRecord(ctx context.Context, ref Ref) (*AccusationRecord, error) {
	record := &AccusationRecord{}
	if err := s.get(ctx, vocabulary.TurnAccusationRef, ref, record); err != nil {
		return nil, err
	}
	return record, nil
}

// PutKnowledgeReceipt stores the aggregate before its reference lands on the turn.
func (s *Store) PutKnowledgeReceipt(
	ctx context.Context, turnEntityID string, receipt *KnowledgeReceipt,
) (Ref, error) {
	if receipt == nil {
		return Ref{}, errors.New("storing a knowledge receipt requires a receipt")
	}
	if err := payload.RequireTurnEntityID(receipt.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnKnowledgeRef, SubjectTurn, receipt.TurnID, receipt)
}

// GetKnowledgeReceipt reads a stored aggregate receipt.
func (s *Store) GetKnowledgeReceipt(ctx context.Context, ref Ref) (*KnowledgeReceipt, error) {
	receipt := &KnowledgeReceipt{}
	if err := s.get(ctx, vocabulary.TurnKnowledgeRef, ref, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

// PutTestimony stores attributed prose under its deterministic revelation identity.
func (s *Store) PutTestimony(ctx context.Context, testimonyID string, testimony *Testimony) (Ref, error) {
	if testimony == nil {
		return Ref{}, errors.New("storing testimony requires a record")
	}
	if expected := TestimonyID(testimony.TurnID, testimony.DecisionID, testimony.BeliefID,
		testimony.RecipientID, testimony.EvidenceID); testimonyID != expected {
		return Ref{}, fmt.Errorf("testimony id %q does not match deterministic identity %q", testimonyID, expected)
	}
	return s.put(ctx, vocabulary.RevelationTestimonyRef, SubjectRevelation, testimonyID, testimony)
}

// GetTestimony reads one attributed testimony artifact.
func (s *Store) GetTestimony(ctx context.Context, ref Ref) (*Testimony, error) {
	testimony := &Testimony{}
	if err := s.get(ctx, vocabulary.RevelationTestimonyRef, ref, testimony); err != nil {
		return nil, err
	}
	return testimony, nil
}

// PutRoll stores the turn's resolution record and returns the reference the
// turn entity will carry.
//
// The roll's rule-matched half is a band and a total; everything that makes it
// REPLAYABLE — the mechanic version, the RNG version, the seed derivation
// inputs, the raw dice, the modifier list — is bulky and is never rule-matched,
// so it travels the same way the verdict's bands do. Replay honesty (8.3) reads
// this object, not the triples.
func (s *Store) PutRoll(
	ctx context.Context,
	turnEntityID string,
	roll *payload.RollResult,
) (Ref, error) {
	if roll == nil {
		return Ref{}, errors.New("storing a roll requires a roll result")
	}
	if err := payload.RequireTurnEntityID(roll.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnRollRef, SubjectTurn, roll.TurnID, roll)
}

// GetRoll reads a stored roll result back.
func (s *Store) GetRoll(ctx context.Context, ref Ref) (*payload.RollResult, error) {
	roll := &payload.RollResult{}
	if err := s.get(ctx, vocabulary.TurnRollRef, ref, roll); err != nil {
		return nil, err
	}
	return roll, nil
}

// PutEffectBatch stores the batch the applier committed and returns the
// reference the turn entity will carry.
//
// It is stored BEFORE the applier runs, because the applier is handed the
// reference and lands it on the turn as the batch marker — the marker is its own
// idempotency guard, so a marker pointing at nothing would be a turn that claims
// effects nobody can inspect. The committed CHANGES are on the target entities;
// this is the record of what was asked for, which is what a ledger replay and a
// rejection diagnosis both need.
func (s *Store) PutEffectBatch(
	ctx context.Context,
	turnEntityID string,
	batch *payload.EffectBatch,
) (Ref, error) {
	if batch == nil {
		return Ref{}, errors.New("storing an effect batch requires a batch")
	}
	if err := payload.RequireTurnEntityID(batch.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnEffectsRef, SubjectTurn, batch.TurnID, batch)
}

// GetEffectBatch reads a stored effect batch back.
func (s *Store) GetEffectBatch(ctx context.Context, ref Ref) (*payload.EffectBatch, error) {
	batch := &payload.EffectBatch{}
	if err := s.get(ctx, vocabulary.TurnEffectsRef, ref, batch); err != nil {
		return nil, err
	}
	return batch, nil
}

// PutNarration stores one turn's narration prose and returns the reference the
// turn entity will carry.
//
// This is the write half of prose-first-ref-last, and the ordering is the whole
// crash-safety argument of the narration stage: the object lands here, and only
// a successful return lets the caller commit turn.narration.ref. A reference to
// missing prose is a completed turn whose result cannot be delivered and cannot
// be re-derived; an orphaned object is garbage that the next attempt overwrites
// at the same derived key.
func (s *Store) PutNarration(
	ctx context.Context,
	turnEntityID string,
	narration *Narration,
) (Ref, error) {
	if narration == nil {
		return Ref{}, errors.New("storing a narration requires a narration")
	}
	if err := payload.RequireTurnEntityID(narration.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnNarrationRef, SubjectTurn, narration.TurnID, narration)
}

// GetNarration reads stored narration prose back.
func (s *Store) GetNarration(ctx context.Context, ref Ref) (*Narration, error) {
	narration := &Narration{}
	if err := s.get(ctx, vocabulary.TurnNarrationRef, ref, narration); err != nil {
		return nil, err
	}
	return narration, nil
}

// PutFailureDetail stores the explanation behind a turn's closed failure code.
//
// The code on the graph answers "what kind of failure"; this answers "which
// one". Without somewhere for that answer to live, the only place left for it
// is the reason predicate itself — and a sentence written there lands free text
// on rule-matching surface, which is precisely what the closed code exists to
// prevent.
func (s *Store) PutFailureDetail(
	ctx context.Context,
	turnEntityID string,
	detail *FailureDetail,
) (Ref, error) {
	if detail == nil {
		return Ref{}, errors.New("storing a failure detail requires a detail")
	}
	if err := payload.RequireTurnEntityID(detail.TurnID, turnEntityID); err != nil {
		return Ref{}, err
	}
	return s.put(ctx, vocabulary.TurnFailureRef, SubjectTurn, detail.TurnID, detail)
}

// GetFailureDetail reads a stored failure detail back.
func (s *Store) GetFailureDetail(ctx context.Context, ref Ref) (*FailureDetail, error) {
	detail := &FailureDetail{}
	if err := s.get(ctx, vocabulary.TurnFailureRef, ref, detail); err != nil {
		return nil, err
	}
	return detail, nil
}

// artifact is what this store can hold: something that states its own contract.
//
// Validate is required on the way IN and on the way OUT. In, because an
// artifact that cannot pass its own contract must not become the durable record
// of what happened; out, because stored bytes are untrusted input like any
// other — an artifact written by an older schema, or by something that was
// never this engine, decodes into a half-populated struct that a persona would
// read as fact.
type artifact interface {
	Validate() error
}

// put is the one write path: validate, derive the key, compose the reference,
// then store.
//
// The reference is validated BEFORE the object is written, because the order
// decides what a failure leaves behind. A reference the graph would refuse,
// discovered after the put, leaves an object no triple can ever point at; the
// same refusal before the put leaves nothing at all.
func (s *Store) put(
	ctx context.Context,
	refPredicate vocabulary.Predicate,
	kind SubjectKind,
	subjectID string,
	a artifact,
) (Ref, error) {
	if err := a.Validate(); err != nil {
		return Ref{}, fmt.Errorf("refusing to store an invalid artifact for %s %s: %w", kind, subjectID, err)
	}
	key, err := KeyFor(refPredicate, kind, subjectID)
	if err != nil {
		return Ref{}, err
	}
	ref := Ref{Instance: s.backend.InstanceName(), Key: key}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}

	data, err := json.Marshal(a)
	if err != nil {
		return Ref{}, fmt.Errorf("encode artifact for %s %s: %w", kind, subjectID, err)
	}
	if err := s.backend.Put(ctx, key, data); err != nil {
		return Ref{}, fmt.Errorf("store artifact at %s: %w", ref, err)
	}
	return ref, nil
}

// get is the one read path: check the reference addresses this store and this
// kind of artifact, read, decode, and hold the decoded value to its contract.
func (s *Store) get(
	ctx context.Context,
	refPredicate vocabulary.Predicate,
	ref Ref,
	into artifact,
) error {
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrArtifactReference, err)
	}
	if ref.Instance != s.backend.InstanceName() {
		return fmt.Errorf(
			"%w: reference %s names storage instance %q; this store is %q. A reference resolves through the "+
				"instance that wrote it, so reading it here would read a different object or none",
			ErrArtifactReference, ref, ref.Instance, s.backend.InstanceName())
	}
	// The slot is the artifact's type discriminator — it is what the stored
	// bytes have instead of an envelope — so a reference addressing another
	// slot is a wiring bug, not a decode failure. Decoding first would report
	// it as a validation error against fields the artifact never had.
	slot, err := vocabulary.ArtifactSlot(refPredicate)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrArtifactReference, err)
	}
	if !strings.HasSuffix(ref.Key, refSeparator+slot) {
		return fmt.Errorf(
			"%w: reference %s does not address a %q artifact; the key's last segment is the artifact kind, and "+
				"decoding one kind's bytes as another's is how a persona reads the wrong turn's record",
			ErrArtifactReference, ref, slot)
	}

	data, err := s.backend.Get(ctx, ref.Key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			return fmt.Errorf("resolve %s: %w: %w", ref, ErrArtifactNotFound, err)
		}
		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%w: decode artifact at %s: %v", ErrArtifactCorrupt, ref, err)
	}
	if err := into.Validate(); err != nil {
		return fmt.Errorf("%w: stored artifact at %s does not satisfy its own contract: %v", ErrArtifactCorrupt, ref, err)
	}
	return nil
}
