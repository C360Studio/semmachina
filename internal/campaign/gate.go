package campaign

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// InstantiationSource is the Source stamped on the campaign entity's triples.
const InstantiationSource = "campaign-instantiation"

// EntityMessageType is the provenance envelope stamped on the campaign entity.
//
// It is stamped explicitly because an entity with a zero envelope is
// indistinguishable from a referential stub to graph.EntityState.IsStub, and
// the campaign entity is precisely the thing boot-readiness checks ask about.
// No message of this type is ever published — the campaign entity is created
// through the atomic mutation lane, not the fact lane — so it is deliberately
// absent from the payload registry: it is entity provenance, not a decodable
// wire type.
//
// The category is payload's constant rather than a literal here, so it shares a
// namespace with the registered categories and a future collision is something
// the payload package can see.
var EntityMessageType = message.Type{
	Domain:   payload.Domain,
	Category: payload.CategoryCampaignEntity,
	Version:  payload.SchemaVersion,
}

// EntityStore is the graph surface the gate needs. Satisfied by *graphio.Store.
//
// Three methods, and the third carries a lane decision rather than a
// convenience. The campaign entity holds the seed AND the import-completion
// marker, so writing the marker is a write onto an entity whose other fact must
// survive it — which is the merge lane, by (subject, predicate), and never the
// appending one. A triple-add here would leave a re-marked campaign holding two
// completion instants and, on the day somebody re-used this surface for the
// seed, two seeds.
type EntityStore interface {
	CreateEntity(ctx context.Context, entity *graph.EntityState) (graphio.CreateResult, error)
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
	MergeTriples(
		ctx context.Context,
		entityID string,
		triples []message.Triple,
		opts ...graphio.MergeOption,
	) (*graph.EntityState, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ EntityStore = (*graphio.Store)(nil)

// Gate is the world-instantiation sentinel and the campaign seed's home.
//
// One entity does both jobs, and that is the design rather than a coincidence
// (F13). A world needs a campaign entity to hold the seed regardless; making
// that same entity's creation the instantiation decision costs one round trip
// instead of N and, crucially, is ATOMIC — where an exists-check followed by an
// import has a window in which two boots both find the world absent and both
// import it over each other.
type Gate struct {
	store      EntityStore
	identity   Identity
	campaignID string
	newSeed    func() (Seed, error)
	now        func() time.Time
}

// GateOption configures a Gate.
type GateOption func(*Gate)

// WithClock overrides the timestamp stamped on the campaign entity. Tests use
// it so a created entity's stamp is an assertable value.
func WithClock(now func() time.Time) GateOption {
	return func(g *Gate) { g.now = now }
}

// WithSeedSource overrides seed generation.
//
// It exists for tests that need to know which seed was minted — notably the one
// proving a second Claim returns the FIRST campaign's seed and not the one the
// second call generated. Production leaves it alone and gets crypto/rand.
func WithSeedSource(newSeed func() (Seed, error)) GateOption {
	return func(g *Gate) { g.newSeed = newSeed }
}

// NewGate builds the gate for one campaign identity.
func NewGate(store EntityStore, identity Identity, opts ...GateOption) (*Gate, error) {
	if store == nil {
		return nil, errors.New("campaign gate requires an entity store")
	}
	campaignID, err := identity.EntityID()
	if err != nil {
		return nil, err
	}
	gate := &Gate{
		store:      store,
		identity:   identity,
		campaignID: campaignID,
		newSeed:    NewSeed,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(gate)
	}
	if gate.newSeed == nil {
		return nil, errors.New("campaign gate requires a seed source")
	}
	if gate.now == nil {
		return nil, errors.New("campaign gate requires a clock")
	}
	return gate, nil
}

// CampaignID returns the campaign entity's canonical ID.
func (g *Gate) CampaignID() string { return g.campaignID }

// Instantiation is the gate's answer.
type Instantiation struct {
	// CampaignID is the campaign entity.
	CampaignID string
	// Seed is the campaign's seed: the one just minted on a fresh world, the
	// one already stored otherwise. Either way it is THE seed for this
	// campaign, for its whole life.
	Seed Seed
	// Fresh reports that THIS call created the campaign. It is the world
	// importer's gate: true means an empty world instance, false means a
	// campaign that has already been played and must not be re-imported over.
	Fresh bool

	// claimed records that this value came out of Claim.
	//
	// Unexported, with no setter, because Instantiation is an exported struct
	// with exported fields and MarkImported takes one as PROOF that the caller
	// won the create. Without this, `campaign.Instantiation{CampaignID: x,
	// Fresh: true}` is an ordinary composite literal that compiles, and the guard
	// it is handed to becomes a rule somebody has to follow rather than a fact
	// about where the value came from — which is the shape this codebase already
	// closed for verified players and for connection ids.
	//
	// Weaker than either of those on its own terms: one caller, same module, and
	// the value naturally comes from Claim anyway. It costs two lines, and the
	// alternative is a comment asking the next caller not to hand-build one.
	claimed bool
}

// Claim performs the instantiation decision as one atomic create.
//
// Created means a fresh world instance — proceed with the import. An
// already-existing campaign means the world was instantiated before, so the
// import MUST be skipped: a template re-import into a living campaign resets
// every predicate the template declares, dropping relationships play has since
// changed. That is the exact inverse of "a restart must not replay the dragon
// eating you".
//
// The seed is minted BEFORE the create and discarded when the create loses,
// because the alternative — read, then create if absent — is the TOCTOU this
// call exists to close. A discarded seed costs 32 bytes of entropy; the stored
// seed always wins, so a campaign's seed is generated at most once in its life
// no matter how many times Claim is called.
//
// WHAT THE GATE DOES NOT PROMISE: it answers "was this campaign entity created
// before?", not "did the import that followed it finish?". A caller that
// creates the campaign and then crashes part-way through materializing the
// world will, on restart, be told the campaign already exists and will skip the
// import — leaving the partial world in place. Import is convergent under
// replace semantics, so re-running it would be safe; knowing that it is NEEDED
// requires a completion marker this gate deliberately does not invent.
func (g *Gate) Claim(ctx context.Context) (Instantiation, error) {
	seed, err := g.newSeed()
	if err != nil {
		return Instantiation{}, err
	}
	if seed.IsZero() {
		return Instantiation{}, errors.New("campaign seed source produced the zero seed")
	}

	result, err := g.store.CreateEntity(ctx, g.entity(seed))
	switch {
	case err == nil:
		// Degraded means the write COMMITTED and only the read-back failed;
		// retrying would return entity_already_exists and a caller reading that
		// as "someone else instantiated it" would draw the opposite of the
		// truth. The minted seed is authoritative — this call wrote it.
		if result.Degraded {
			return Instantiation{CampaignID: g.campaignID, Seed: seed, Fresh: true, claimed: true}, nil
		}
		if err := g.confirmStoredSeed(result.Entity, seed); err != nil {
			return Instantiation{}, err
		}
		return Instantiation{CampaignID: g.campaignID, Seed: seed, Fresh: true, claimed: true}, nil

	case errors.Is(err, graphio.ErrEntityExists):
		stored, loadErr := g.LoadSeed(ctx)
		if loadErr != nil {
			return Instantiation{}, fmt.Errorf(
				"campaign %s already exists but its seed is unreadable, and a campaign's seed is never regenerated: %w",
				g.campaignID, loadErr)
		}
		return Instantiation{CampaignID: g.campaignID, Seed: stored, Fresh: false, claimed: true}, nil

	default:
		return Instantiation{}, err
	}
}

// LoadSeed reads the stored campaign seed.
//
// Every failure here is loud on purpose. The tempting recovery — mint a fresh
// seed and carry on — would leave every roll already recorded in the ledger
// unreproducible while every one of them still validated, so replay would
// "succeed" against different dice. An unreadable seed is a stop, not a
// fallback.
func (g *Gate) LoadSeed(ctx context.Context) (Seed, error) {
	state, err := g.store.GetEntity(ctx, g.campaignID)
	if err != nil {
		return Seed{}, err
	}
	return seedFromEntity(state, g.campaignID)
}

// seedFromEntity extracts the seed from a stored campaign entity.
func seedFromEntity(state *graph.EntityState, campaignID string) (Seed, error) {
	if state == nil {
		return Seed{}, fmt.Errorf("campaign entity %s read back as nil", campaignID)
	}
	// A stub occupies the key without carrying any of the entity's own facts.
	// If a campaign ID is ever referenced by another entity before the campaign
	// is created, the create loses to the stub and this is the only signal that
	// distinguishes "already instantiated" from "referenced, never born".
	if state.IsStub() {
		return Seed{}, fmt.Errorf(
			"campaign entity %s is a referential stub: something referenced it before it was created, "+
				"so it holds no seed", campaignID)
	}
	triple := state.GetTriple(vocabulary.CampaignSeedValue.String())
	if triple == nil {
		return Seed{}, fmt.Errorf("campaign entity %s carries no %s triple",
			campaignID, vocabulary.CampaignSeedValue)
	}
	value, ok := triple.Object.(string)
	if !ok {
		return Seed{}, fmt.Errorf("campaign entity %s carries a %T seed value, want a hex string",
			campaignID, triple.Object)
	}
	seed, err := ParseSeed(value)
	if err != nil {
		return Seed{}, fmt.Errorf("campaign entity %s: %w", campaignID, err)
	}
	return seed, nil
}

// confirmStoredSeed checks the read-back carries the seed this call wrote.
//
// Cheap, and it closes the one failure that would otherwise be invisible: a
// create that succeeds while the seed triple is dropped or rewritten leaves a
// campaign whose stored seed differs from the one this process is about to roll
// with — reproducible in memory, unreproducible on restart.
func (g *Gate) confirmStoredSeed(stored *graph.EntityState, minted Seed) error {
	readBack, err := seedFromEntity(stored, g.campaignID)
	if err != nil {
		return fmt.Errorf("campaign %s was created but its seed did not store: %w", g.campaignID, err)
	}
	if readBack != minted {
		return fmt.Errorf("campaign %s stored a different seed than the one it was created with", g.campaignID)
	}
	return nil
}

// entity builds the campaign entity: the sentinel and the seed holder.
func (g *Gate) entity(seed Seed) *graph.EntityState {
	at := g.now().UTC()
	return &graph.EntityState{
		ID:          g.campaignID,
		MessageType: EntityMessageType,
		Version:     1,
		UpdatedAt:   at,
		Triples: []message.Triple{{
			Subject:   g.campaignID,
			Predicate: vocabulary.CampaignSeedValue.String(),
			Object:    seed.String(),
			Source:    InstantiationSource,
			Timestamp: at,
			// The seed is minted and recorded, never inferred.
			Confidence: 1.0,
			Context:    g.campaignID,
		}},
	}
}
