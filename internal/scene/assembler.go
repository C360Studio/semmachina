package scene

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// DefaultMaxEntities bounds one assembled context.
//
// It is a real ceiling, not a formality: a scene beyond it is refused by name
// rather than trimmed to fit. Truncation was the alternative and it is the worse
// failure — a persona handed nine of twenty characters narrates a room that is
// not there, and nothing anywhere reports a problem. A world that puts more than
// this in one scene is telling the operator something, and the operator should
// hear it.
//
// Sixty-four is sized to the kind of scene this engine is for (the starter world
// uses seven) with room for a crowded one, and it is a dial rather than a
// constant: worlds are data, so a world that genuinely needs a bigger room says
// so at construction.
const DefaultMaxEntities = 64

// Graph is the read surface the assembler needs.
//
// Every method is a READ, and that is enforced here rather than promised in a
// comment: graph-ingest is the sole ENTITY_STATES writer, and an assembler
// holding a merge method is one refactor away from repairing the world it was
// asked to describe. TestGraph_CarriesNoWriteMethod holds this interface to
// exactly three reads.
type Graph interface {
	// GetEntity reads one entity, reporting an absent one as
	// graphio.ErrEntityNotFound rather than as an empty state.
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
	// GetEntities reads many entities in one round trip, reporting the ones it
	// could not hydrate rather than returning a quietly shorter list.
	GetEntities(ctx context.Context, ids []string) (graphio.BatchResult, error)
	// IncomingRelationships returns every edge pointing AT an entity — the
	// direction a triple cannot be read from, and the one "who is in this
	// scene" is answered in.
	IncomingRelationships(ctx context.Context, entityID string) ([]graph.IncomingEntry, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Graph = (*graphio.Store)(nil)

// Assembler builds a persona's view of the world.
type Assembler struct {
	graph       Graph
	maxEntities int
	now         func() time.Time
}

// Option configures an Assembler.
type Option func(*Assembler)

// WithMaxEntities overrides the per-context entity cap. Worlds are data, so a
// world that genuinely needs a bigger room says so here.
func WithMaxEntities(max int) Option {
	return func(a *Assembler) { a.maxEntities = max }
}

// WithClock overrides the assembler's clock, so an assembled view's timestamp is
// an assertable value rather than whatever time it happened to be.
func WithClock(now func() time.Time) Option {
	return func(a *Assembler) { a.now = now }
}

// NewAssembler builds an assembler over a graph read surface.
func NewAssembler(g Graph, opts ...Option) (*Assembler, error) {
	if g == nil {
		return nil, errors.New("context assembler requires a graph read surface")
	}
	assembler := &Assembler{graph: g, maxEntities: DefaultMaxEntities, now: time.Now}
	for _, opt := range opts {
		opt(assembler)
	}
	if assembler.maxEntities <= 0 {
		return nil, errors.New(
			"context assembler requires a positive entity cap; an unbounded context is an unbounded prompt, " +
				"and per-turn cost stops being flat the first time a scene fills up")
	}
	if assembler.now == nil {
		return nil, errors.New("context assembler requires a clock")
	}
	return assembler, nil
}

// Assemble runs the fixed scene-scoped query for one turn.
//
// It is called at PERSONA EXECUTION TIME and takes no snapshot: the scene is
// read off the turn entity, and every fact in the result is read now. A turn
// submitted on Monday and adjudicated on Friday is judged against Friday's
// world, which is the whole reason this is a component the persona calls rather
// than a blob the action carries.
//
// The shape, in full — five reads, always, in this order:
//
//  1. the turn entity, for the scene it is happening in and its own artifacts;
//  2. the scene entity, for its own facts;
//  3. the scene's incoming edges, for who is in it;
//  4. one batch for the members;
//  5. one batch for the members' 1-hop neighbours.
//
// There is no sixth. The traversal stops at one hop because nothing here
// recurses, which is what makes the depth bound structural rather than a
// parameter somebody can raise.
func (a *Assembler) Assemble(ctx context.Context, turnID, turnEntityID string) (*View, error) {
	// The turn and its entity are one fact wearing two shapes, and this is a
	// seam where they arrive as two arguments. A stage handed turn A's entity
	// with turn B's id would assemble another turn's scene and hand it to a
	// persona as this turn's world.
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return nil, err
	}

	turnState, err := a.readSolid(ctx, turnEntityID, "turn")
	if err != nil {
		return nil, err
	}
	sceneID, err := sceneOf(turnState)
	if err != nil {
		return nil, err
	}
	sceneState, err := a.readSolid(ctx, sceneID, "scene")
	if err != nil {
		return nil, err
	}

	view := &View{
		TurnID:       turnID,
		TurnEntityID: turnEntityID,
		SceneID:      sceneID,
		AssembledAt:  a.now().UTC(),
		Turn:         project(turnState),
		Scene:        project(sceneState),
	}

	memberIDs, err := a.memberIDs(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	// The cap is checked against the CANDIDATE count, before the batch is
	// issued: an oversized scene must be refused rather than fetched and then
	// refused, or the read this bound exists to bound has already happened.
	if err := a.checkCap(sceneID, len(memberIDs)+1); err != nil {
		return nil, err
	}
	members, excluded, err := a.hydrate(ctx, memberIDs)
	if err != nil {
		return nil, err
	}
	view.Members = members
	view.Excluded = excluded

	neighbourIDs := neighbourIDsOf(sceneID, view.Turn, view.Scene, members)
	if err := a.checkCap(sceneID, len(memberIDs)+len(neighbourIDs)+1); err != nil {
		return nil, err
	}
	neighbours, neighbourExclusions, err := a.hydrate(ctx, neighbourIDs)
	if err != nil {
		return nil, err
	}
	view.Neighbours = neighbours
	view.Excluded = append(view.Excluded, neighbourExclusions...)

	view.Size = measure(view.Entities())
	return view, nil
}

// readSolid reads one entity and refuses a referential stub.
//
// A stub answers a read successfully while carrying none of its own facts, so
// "the id resolved" is not "the entity is loaded" (F11). For the turn and the
// scene that is fatal rather than an exclusion: a persona cannot be given a turn
// with no phase or a room with no name and asked to judge what happens in it.
func (a *Assembler) readSolid(ctx context.Context, id, role string) (*graph.EntityState, error) {
	state, err := a.graph.GetEntity(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read %s entity %s: %w", role, id, err)
	}
	if state == nil {
		return nil, fmt.Errorf("read %s entity %s: the graph returned nothing", role, id)
	}
	if state.IsStub() {
		return nil, fmt.Errorf(
			"%s entity %s is a referential stub: it is queryable and holds none of its own facts, so "+
				"assembling context from it would hand a persona an empty %s that reads as a real one",
			role, id, role)
	}
	return state, nil
}

// sceneOf reads the scene the turn is happening in off the turn entity.
func sceneOf(turnState *graph.EntityState) (string, error) {
	var scenes []string
	for _, triple := range turnState.Triples {
		if triple.Predicate != vocabulary.TurnActionScene.String() {
			continue
		}
		id, ok := triple.Object.(string)
		if !ok {
			return "", fmt.Errorf("turn %s records a %T scene reference, want an entity id",
				turnState.ID, triple.Object)
		}
		scenes = append(scenes, id)
	}
	switch len(scenes) {
	case 1:
		return scenes[0], nil
	case 0:
		return "", fmt.Errorf(
			"turn %s carries no %s; the scene is written once, by the turn's birth record, and a turn "+
				"without one has no world to assemble", turnState.ID, vocabulary.TurnActionScene)
	default:
		return "", fmt.Errorf(
			"turn %s holds %d values for the single-valued %s; a scene written on an appending lane leaves "+
				"this reader choosing one at random", turnState.ID, len(scenes), vocabulary.TurnActionScene)
	}
}

// memberIDs asks who is in the scene.
//
// The answer is a REVERSE lookup: membership is asserted by the member, so the
// scene's own triples say nothing about it. Every incoming edge is returned by
// the index, and only the closed membership set counts — the engine's own turn
// entities point at the scene too, one per turn ever taken there, and treating
// those as members would grow a persona's context with the campaign's history.
func (a *Assembler) memberIDs(ctx context.Context, sceneID string) ([]string, error) {
	incoming, err := a.graph.IncomingRelationships(ctx, sceneID)
	if err != nil {
		return nil, fmt.Errorf("read the members of scene %s: %w", sceneID, err)
	}

	seen := make(map[string]bool, len(incoming))
	ids := make([]string, 0, len(incoming))
	for _, edge := range incoming {
		if !vocabulary.IsSceneMembership(vocabulary.Predicate(edge.Predicate)) {
			continue
		}
		if edge.FromEntityID == "" || edge.FromEntityID == sceneID || seen[edge.FromEntityID] {
			continue
		}
		seen[edge.FromEntityID] = true
		ids = append(ids, edge.FromEntityID)
	}
	// Sorted so one world state assembles to one view: the index returns a
	// deterministic order today, and a context whose entity order depended on
	// storage layout would make two identical worlds produce two prompts.
	slices.Sort(ids)
	return ids, nil
}

// neighbourIDsOf collects the 1-hop boundary: everything the turn, the scene,
// and the scene's members point at that is not already in the view.
//
// This is where a referenced-but-undelivered entity actually turns up. A member
// carrying an item that was never imported has a perfectly good edge to a
// perfectly queryable stub, and hydrating it without checking is how a persona
// is handed a thing with no name and narrates it anyway.
//
// The TURN is one of the three sources, and that is not incidental: the turn's
// player reference is the only thing that says WHICH of the people in the room
// is acting. Without it a persona is shown a courier, an apprentice, and a
// sentry, and no way to tell which one the player is.
func neighbourIDsOf(sceneID string, turn, scene Entity, members []Entity) []string {
	inView := map[string]bool{sceneID: true, turn.ID: true}
	for _, member := range members {
		inView[member.ID] = true
	}

	seen := make(map[string]bool)
	var ids []string
	for _, entity := range append([]Entity{turn, scene}, members...) {
		for _, triple := range entity.Triples {
			if !triple.IsRelationship() {
				continue
			}
			target, ok := triple.Object.(string)
			if !ok || inView[target] || seen[target] {
				continue
			}
			seen[target] = true
			ids = append(ids, target)
		}
	}
	slices.Sort(ids)
	return ids
}

// hydrate reads a set of entities and partitions them into what a persona may
// be shown and what it may not.
func (a *Assembler) hydrate(ctx context.Context, ids []string) ([]Entity, []Exclusion, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	result, err := a.graph.GetEntities(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("read %d entities: %w", len(ids), err)
	}

	entities := make([]Entity, 0, len(result.Entities))
	var excluded []Exclusion
	for idx := range result.Entities {
		state := &result.Entities[idx]
		if state.IsStub() {
			excluded = append(excluded, Exclusion{ID: state.ID, Reason: ExcludedStub})
			continue
		}
		entities = append(entities, project(state))
	}
	// A requested id the graph did not return is an exclusion too, and reporting
	// it is the difference between "the room is small" and "the room is
	// half-loaded". Upstream stopped shortening the list silently for exactly
	// this reason; passing that on rather than dropping it is the point.
	for _, missing := range result.Missing {
		excluded = append(excluded, Exclusion{ID: missing.ID, Reason: ExcludedMissing})
	}

	// Sorted for the same reason the member ids are: one world state assembles
	// to one view, so a prompt does not depend on storage layout.
	slices.SortFunc(entities, func(left, right Entity) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(excluded, func(left, right Exclusion) int {
		return strings.Compare(left.ID, right.ID)
	})
	return entities, excluded, nil
}

// checkCap refuses an oversized context by name.
func (a *Assembler) checkCap(sceneID string, entities int) error {
	if entities > a.maxEntities {
		return &OversizeError{SceneID: sceneID, Entities: entities, Max: a.maxEntities}
	}
	return nil
}

// OversizeError reports a scene too large to assemble a bounded context from.
//
// It is an error rather than a truncation on purpose. Trimming to fit would make
// a persona's world quietly incomplete, which is the same silent hole a stub is,
// and it would hide the one thing an operator needs to know: that a scene has
// grown past what the cost model was built for.
type OversizeError struct {
	// SceneID is the scene that is too big.
	SceneID string
	// Entities is how many the context would have carried.
	Entities int
	// Max is the cap in force.
	Max int
}

// Error implements error.
func (e *OversizeError) Error() string {
	return fmt.Sprintf(
		"scene %s would assemble a context of %d entities, over the cap of %d; refusing rather than "+
			"trimming, because a persona handed part of a room narrates a room that is not there and "+
			"nothing reports it", e.SceneID, e.Entities, e.Max)
}
