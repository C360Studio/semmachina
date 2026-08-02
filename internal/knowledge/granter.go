package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Source is stamped on structural facts committed by the granter.
const Source = "knowledge-granter"

var (
	knowledgeEntityType  = message.Type{Domain: payload.Domain, Category: "knowledge_grant_entity", Version: payload.SchemaVersion}
	revelationEntityType = message.Type{Domain: payload.Domain, Category: "revelation_receipt_entity", Version: payload.SchemaVersion}
)

// GraphStore is the create-or-verify and final turn-witness surface.
type GraphStore interface {
	CreateEntity(context.Context, *graph.EntityState) (graphio.CreateResult, error)
	GetEntity(context.Context, string) (*graph.EntityState, error)
	MergeTriples(context.Context, string, []message.Triple, ...graphio.MergeOption) (*graph.EntityState, error)
}

// ArtifactStore keeps prose and aggregate receipts out of the graph.
type ArtifactStore interface {
	PutTestimony(context.Context, string, *content.Testimony) (content.Ref, error)
	PutKnowledgeReceipt(context.Context, string, *content.KnowledgeReceipt) (content.Ref, error)
}

// Granter commits an already fully-read authorization snapshot.
type Granter struct {
	graph GraphStore
	store ArtifactStore
	now   func() time.Time
}

// NewGranter builds the deterministic component.
func NewGranter(graphStore GraphStore, artifacts ArtifactStore) (*Granter, error) {
	if graphStore == nil || artifacts == nil {
		return nil, errors.New("knowledge granter requires graph and artifact stores")
	}
	return &Granter{graph: graphStore, store: artifacts, now: time.Now}, nil
}

// Grant authorizes the complete batch before beginning the ordered commit.
func (g *Granter) Grant(
	ctx context.Context, turnEntityID string, input Preflight, shares ShareAuthorizer,
) (content.Ref, error) {
	return g.GrantWithWitnesses(ctx, turnEntityID, input, shares, nil)
}

// GrantWithWitnesses authorizes the complete dual-recipient plan before writes.
func (g *Granter) GrantWithWitnesses(
	ctx context.Context, turnEntityID string, input Preflight,
	shares ShareAuthorizer, witnesses WitnessAuthorizer,
) (content.Ref, error) {
	plan, err := AuthorizeWithWitnesses(ctx, input, shares, witnesses)
	if err != nil {
		return content.Ref{}, err
	}
	return g.commit(ctx, turnEntityID, input.Decision, plan)
}

// GrantNotApplicable commits the deterministic non-mystery empty receipt.
func (g *Granter) GrantNotApplicable(ctx context.Context, turnID, turnEntityID string) (content.Ref, error) {
	receipt := &content.KnowledgeReceipt{TurnID: turnID, Status: content.KnowledgeNotApplicable}
	ref, err := g.store.PutKnowledgeReceipt(ctx, turnEntityID, receipt)
	if err != nil {
		return content.Ref{}, err
	}
	if err := g.writeTurnRef(ctx, turnEntityID, ref); err != nil {
		return content.Ref{}, err
	}
	return ref, nil
}

func (g *Granter) commit(
	ctx context.Context, turnEntityID string, decision *payload.CaseDecision, plan Plan,
) (content.Ref, error) {
	if decision == nil {
		return content.Ref{}, errors.New("knowledge commit requires a decision")
	}
	type derived struct {
		entry        Entry
		knowledgeID  string
		revelationID string
		testimonyID  string
		testimony    *content.Testimony
	}
	derivedEntries := make([]derived, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		knowledgeID, err := entityIDFor(entry.RecipientID, vocabulary.EntityKindKnowledge,
			framedID("knowledge-grant/v1", entry.RecipientID, entry.EvidenceID))
		if err != nil {
			return content.Ref{}, err
		}
		revelationID, err := entityIDFor(entry.RecipientID, vocabulary.EntityKindRevelation,
			framedID("revelation-receipt/v1", decision.TurnID, decision.DecisionID,
				entry.RecipientID, entry.EvidenceID))
		if err != nil {
			return content.Ref{}, err
		}
		d := derived{entry: entry, knowledgeID: knowledgeID, revelationID: revelationID}
		if entry.Testimony != nil {
			d.testimony = &content.Testimony{
				TurnID: decision.TurnID, DecisionID: decision.DecisionID,
				BeliefID: entry.Testimony.BeliefID, SourceActorID: entry.Testimony.SourceActorID,
				RecipientID: entry.RecipientID, EvidenceID: entry.EvidenceID,
				Stance: entry.Testimony.Stance, Prose: entry.Testimony.Prose,
			}
			d.testimonyID = content.TestimonyID(decision.TurnID, decision.DecisionID,
				d.testimony.BeliefID, entry.RecipientID, entry.EvidenceID)
		}
		derivedEntries = append(derivedEntries, d)
	}

	testimonyRefs := make(map[string]content.Ref, len(derivedEntries))
	for _, d := range derivedEntries {
		if d.testimony == nil {
			continue
		}
		ref, err := g.store.PutTestimony(ctx, d.testimonyID, d.testimony)
		if err != nil {
			return content.Ref{}, fmt.Errorf("store testimony: %w", err)
		}
		testimonyRefs[d.revelationID] = ref
	}

	at := g.now().UTC()
	for _, d := range derivedEntries {
		wanted := entity(d.knowledgeID, knowledgeEntityType, at,
			fact(d.knowledgeID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindKnowledge), at),
			fact(d.knowledgeID, vocabulary.KnowledgeActorHolder, d.entry.RecipientID, at),
			fact(d.knowledgeID, vocabulary.KnowledgeEvidenceRef, d.entry.EvidenceID, at))
		if err := g.createOrVerify(ctx, wanted); err != nil {
			return content.Ref{}, fmt.Errorf("commit knowledge grant: %w", err)
		}
	}
	for _, d := range derivedEntries {
		triples := []message.Triple{
			fact(d.revelationID, vocabulary.WorldEntityKind, string(vocabulary.EntityKindRevelation), at),
			fact(d.revelationID, vocabulary.RevelationActorHolder, d.entry.RecipientID, at),
			fact(d.revelationID, vocabulary.RevelationEvidenceRef, d.entry.EvidenceID, at),
			fact(d.revelationID, vocabulary.RevelationTurnID, decision.TurnID, at),
		}
		if d.entry.Testimony != nil {
			triples = append(triples,
				fact(d.revelationID, vocabulary.RevelationSourceActor, d.entry.Testimony.SourceActorID, at),
				fact(d.revelationID, vocabulary.RevelationTestimonyRef, testimonyRefs[d.revelationID].String(), at))
		}
		if err := g.createOrVerify(ctx, entity(d.revelationID, revelationEntityType, at, triples...)); err != nil {
			return content.Ref{}, fmt.Errorf("commit revelation receipt: %w", err)
		}
	}

	entries := make([]content.KnowledgeReceiptEntry, 0, len(derivedEntries))
	for _, d := range derivedEntries {
		entries = append(entries, content.KnowledgeReceiptEntry{
			RecipientID: d.entry.RecipientID, EvidenceID: d.entry.EvidenceID,
			KnowledgeID: d.knowledgeID, RevelationID: d.revelationID,
			TestimonyRef: testimonyRefs[d.revelationID].String(),
		})
	}
	receipt := &content.KnowledgeReceipt{
		TurnID: decision.TurnID, DecisionID: decision.DecisionID,
		Status: content.KnowledgeCommitted, Entries: content.SortedKnowledgeEntries(entries),
	}
	ref, err := g.store.PutKnowledgeReceipt(ctx, turnEntityID, receipt)
	if err != nil {
		return content.Ref{}, fmt.Errorf("store aggregate knowledge receipt: %w", err)
	}
	if err := g.writeTurnRef(ctx, turnEntityID, ref); err != nil {
		return content.Ref{}, err
	}
	return ref, nil
}

func (g *Granter) createOrVerify(ctx context.Context, wanted *graph.EntityState) error {
	result, err := g.graph.CreateEntity(ctx, wanted)
	if err == nil {
		if result.Degraded {
			return nil
		}
		if result.Entity == nil {
			return fmt.Errorf("create entity %s returned no read-back", wanted.ID)
		}
		return exactEntitySemantics(result.Entity, wanted)
	}
	if !errors.Is(err, graphio.ErrEntityExists) {
		return err
	}
	resident, readErr := g.graph.GetEntity(ctx, wanted.ID)
	if readErr != nil {
		return fmt.Errorf("verify existing entity %s: %w", wanted.ID, readErr)
	}
	return exactEntitySemantics(resident, wanted)
}

func exactEntitySemantics(got, want *graph.EntityState) error {
	if want == nil {
		return errors.New("integrity verification requires an expected entity")
	}
	if got == nil || got.ID != want.ID {
		return fmt.Errorf("integrity mismatch for entity %s", want.ID)
	}
	if !got.MessageType.Equal(want.MessageType) {
		return fmt.Errorf("integrity mismatch for existing entity %s: message type %s, want %s",
			want.ID, got.MessageType, want.MessageType)
	}
	if got.Version != want.Version {
		return fmt.Errorf("integrity mismatch for existing entity %s: version %d, want %d",
			want.ID, got.Version, want.Version)
	}
	normalize := func(triples []message.Triple, resident bool) ([]string, error) {
		out := make([]string, 0, len(triples))
		for _, triple := range triples {
			// Referential-integrity markers are the only framework-owned facts
			// permitted beside the granter's exact semantic entity. A prefix test
			// would let arbitrary core.identity.* or provenance-shaped facts hide
			// here, so the exception is deliberately enumerated.
			if resident && frameworkManagedPredicate(triple.Predicate) {
				continue
			}
			object, err := json.Marshal(triple.Object)
			if err != nil {
				return nil, fmt.Errorf("encode %s object for exact verification: %w", triple.Predicate, err)
			}
			out = append(out, triple.Subject+"\x00"+triple.Predicate+"\x00"+string(object))
		}
		slices.Sort(out)
		return out, nil
	}
	gotTriples, err := normalize(got.Triples, true)
	if err != nil {
		return err
	}
	wantTriples, err := normalize(want.Triples, false)
	if err != nil {
		return err
	}
	if !slices.Equal(gotTriples, wantTriples) {
		return fmt.Errorf("integrity mismatch for existing entity %s", want.ID)
	}
	return nil
}

func frameworkManagedPredicate(predicate string) bool {
	switch predicate {
	case graph.PredStubMarker, graph.PredStubReferencedBy, graph.PredStubOwner,
		ssvocab.EntityIndexingProfile:
		return true
	default:
		return false
	}
}

func (g *Granter) writeTurnRef(ctx context.Context, turnEntityID string, ref content.Ref) error {
	at := g.now().UTC()
	_, err := g.graph.MergeTriples(ctx, turnEntityID,
		[]message.Triple{fact(turnEntityID, vocabulary.TurnKnowledgeRef, ref.String(), at)})
	if err != nil {
		return fmt.Errorf("record knowledge receipt on turn: %w", err)
	}
	return nil
}

func entity(id string, typ message.Type, at time.Time, triples ...message.Triple) *graph.EntityState {
	return &graph.EntityState{ID: id, MessageType: typ, Version: 1, UpdatedAt: at, Triples: triples}
}

func fact(subject string, predicate vocabulary.Predicate, object any, at time.Time) message.Triple {
	return message.Triple{Subject: subject, Predicate: predicate.String(), Object: object,
		Source: Source, Timestamp: at, Confidence: 1, Context: subject}
}

func entityIDFor(anchor string, kind vocabulary.EntityKind, instance string) (string, error) {
	parts := strings.Split(anchor, ".")
	if len(parts) != 6 {
		return "", fmt.Errorf("anchor %q is not a six-part entity id", anchor)
	}
	return vocabulary.ComposeEntityID(parts[0], parts[2], parts[3], string(kind), instance)
}

func framedID(parts ...string) string {
	hash := sha256.New()
	var size [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
