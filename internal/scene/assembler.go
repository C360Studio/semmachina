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
//
// The cap counts ENTITIES while the cost it stands in for is BYTES, so the
// derived ceiling is worth stating rather than leaving to be rediscovered. Of
// the engine's registered predicates, twenty-nine are single-valued and an
// author-written string fact is bounded at payload.MaxFactTextBytes, so one
// entity's single-valued facts weigh at most ~15 KB and sixty-four of them ~0.9
// MB — comfortably inside a NATS message, and far past what belongs in a prompt.
// The five RELATION predicates are multi-valued and therefore outside that
// arithmetic: a character carrying a hundred things is a hundred triples on one
// entity. That is precisely why every view reports its own measured Size instead
// of trusting the entity count to stand for weight.
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
// The shape, in full — at most five reads, in this order:
//
//  1. the turn entity, for the scene it is happening in and its own artifacts;
//  2. the scene entity, for its own facts;
//  3. the scene's incoming edges, for who is in it;
//  4. one batch for the members;
//  5. one batch for the members' 1-hop neighbours.
//
// "At most" rather than "always" because a stage with no candidates issues no
// read at all — an empty room costs three. The bound is the load-bearing half:
// there is no sixth read, and the traversal stops at one hop because nothing
// here recurses, which is what makes the depth bound structural rather than a
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
	turn := project(turnState)
	sceneID, err := sceneOf(turn)
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
		Turn:         turn,
		Scene:        project(sceneState),
	}

	memberIDs, err := a.memberIDs(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	// The cap is checked against the CANDIDATE count, before the batch is
	// issued: an oversized scene must be refused rather than fetched and then
	// refused, or the read this bound exists to bound has already happened.
	//
	// The turn and the scene are counted with them (fixedEntities) because the
	// view carries both: a cap that counted only what it fetched would let a
	// context exceed its own reported ceiling by two and report the excess in
	// Size afterwards.
	if err := a.checkCap(sceneID, fixedEntities+len(memberIDs)); err != nil {
		return nil, err
	}
	members, excluded, err := a.hydrate(ctx, memberIDs)
	if err != nil {
		return nil, err
	}
	view.Members = members
	view.Excluded = excluded

	neighbourIDs := neighbourIDsOf(sceneID, view.Turn, view.Scene, members)
	if err := a.checkCap(sceneID, fixedEntities+len(memberIDs)+len(neighbourIDs)); err != nil {
		return nil, err
	}
	neighbours, neighbourExclusions, err := a.hydrate(ctx, neighbourIDs)
	if err != nil {
		return nil, err
	}
	view.Neighbours = neighbours
	view.Excluded = append(view.Excluded, neighbourExclusions...)

	actor, err := actorOf(view)
	if err != nil {
		return nil, err
	}
	view.Actor = actor

	view.Size = measure(view.Entities())
	return view, nil
}

// fixedEntities is what every view carries before anybody is in the room: the
// turn and the scene.
const fixedEntities = 2

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
func sceneOf(turn Entity) (string, error) {
	scenes, err := entityReferences(turn, vocabulary.TurnActionScene)
	if err != nil {
		return "", err
	}
	switch len(scenes) {
	case 1:
		return scenes[0], nil
	case 0:
		return "", fmt.Errorf(
			"turn %s carries no %s; the scene is written once, by the turn's birth record, and a turn "+
				"without one has no world to assemble", turn.ID, vocabulary.TurnActionScene)
	default:
		return "", fmt.Errorf(
			"turn %s holds %d values for the single-valued %s; a scene written on an appending lane leaves "+
				"this reader choosing one at random", turn.ID, len(scenes), vocabulary.TurnActionScene)
	}
}

// playerOf reads the player whose turn this is off the turn entity.
//
// Single-valued for the same reason the scene is, and established by the same
// write: the birth record names the player once, so a turn holding two is one
// this reader would pick an actor for at random.
func playerOf(turn Entity) (string, error) {
	players, err := entityReferences(turn, vocabulary.TurnActionPlayer)
	if err != nil {
		return "", err
	}
	switch len(players) {
	case 1:
		return players[0], nil
	case 0:
		return "", fmt.Errorf(
			"turn %s carries no %s; without it nothing can say which of the people in the room is acting, "+
				"and a persona shown a courier, an apprentice, and a sentry would have to guess",
			turn.ID, vocabulary.TurnActionPlayer)
	default:
		return "", fmt.Errorf(
			"turn %s holds %d values for the single-valued %s; two players on one turn leaves this reader "+
				"choosing an actor at random", turn.ID, len(players), vocabulary.TurnActionPlayer)
	}
}

// entityReferences returns every entity id an entity records for a predicate.
//
// A non-string object on a reference predicate is a refusal rather than a skip:
// it is authoritative state that cannot mean what its predicate says, and
// silently returning fewer references than the entity holds is how a reader ends
// up describing a smaller world than the graph does.
func entityReferences(entity Entity, predicate vocabulary.Predicate) ([]string, error) {
	objects := entity.Objects(predicate)
	ids := make([]string, 0, len(objects))
	for _, object := range objects {
		id, ok := object.(string)
		if !ok {
			return nil, fmt.Errorf("entity %s records a %T value for %s, want an entity id",
				entity.ID, object, predicate)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// actorOf answers the one question the assembled room cannot answer for itself:
// is the person acting in it?
//
// Membership is a REVERSE lookup, so an entity whose world.location.current the
// index has not applied yet is not a candidate at all — and a candidate is the
// only thing an Exclusion can name. Without this cross-check the acting
// character can be missing from the assembled room silently: no error, no
// exclusion, and a persona asked to judge what somebody does in a room they are
// not in.
//
// ErrIndexNotReady does not cover it, which is the whole reason this exists.
// Upstream's readiness latch flips as soon as initial enumeration completes over
// an EMPTY graph (target == 0 on a fresh instance-per-world boot, before the
// world import writes anything), and the query gate deliberately never fires on
// ordinary lag afterwards. The sentinel covers cold replay and known-degraded;
// the post-import window is exactly the gap.
//
// It costs NO read. The turn names the player, the player is already hydrated
// one hop out, and player.character.current is registered vocabulary — so this
// is in-memory work over data the view already holds, and the query shape stays
// what the doc comment says it is.
//
// A resolvable character absent from the room is a REFUSAL, because handing a
// persona a room without the actor is worse than failing the turn. A binding
// that cannot be read at all is recorded as doubt instead: a campaign whose
// player entity was never written is broken in a way no turn can fix, and
// failing every turn forever would report it worse than saying so once per view.
func actorOf(view *View) (Actor, error) {
	playerID, err := playerOf(view.Turn)
	if err != nil {
		return Actor{}, err
	}
	actor := Actor{PlayerID: playerID}

	player, hydrated := view.entity(playerID)
	if !hydrated {
		// Either the player was a candidate and did not survive hydration — in
		// which case Excluded says why — or the reference is not entity-shaped
		// and never became a candidate to hydrate. Both leave nothing to follow.
		actor.Doubt = ActorPlayerAbsent
		return actor, nil
	}
	characters, err := entityReferences(player, vocabulary.PlayerCharacterCurrent)
	if err != nil {
		return Actor{}, err
	}
	switch len(characters) {
	case 1:
	case 0:
		actor.Doubt = ActorBindingAbsent
		return actor, nil
	default:
		actor.Doubt = ActorBindingAmbiguous
		return actor, nil
	}

	actor.CharacterID = characters[0]
	for _, member := range view.Members {
		if member.ID == actor.CharacterID {
			return actor, nil
		}
	}

	absent := &AbsentActorError{
		SceneID:      view.SceneID,
		TurnEntityID: view.TurnEntityID,
		PlayerID:     actor.PlayerID,
		CharacterID:  actor.CharacterID,
		Members:      idsOf(view.Members),
	}
	// A character the assembler REFUSED is a different failure from one the
	// index never mentioned, and the two need different repairs — finish the
	// import versus wait for the index — so the refusal says which it saw.
	for _, excluded := range view.Excluded {
		if excluded.ID == actor.CharacterID {
			absent.Excluded = excluded.Reason
			break
		}
	}
	return Actor{}, absent
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

// AbsentActorError reports an assembled room that does not contain the person
// acting in it.
//
// It is a refusal for the same reason OversizeError is: the alternative is a
// persona handed a room it cannot narrate honestly, with nothing anywhere
// reporting a problem. This one is worse than a short room — the missing entity
// is the one the turn is ABOUT.
//
// The cause is one of two things, and the error says both because this reader
// cannot tell them apart: the membership index has not applied the character's
// world.location.current edge yet (retry), or the character genuinely left the
// scene the turn was submitted in (the turn's premise is void). Either way the
// turn cannot be judged against this room.
type AbsentActorError struct {
	// SceneID is the room that was assembled.
	SceneID string
	// TurnEntityID is the turn whose actor is missing.
	TurnEntityID string
	// PlayerID is the player the turn names.
	PlayerID string
	// CharacterID is the character that player is playing — the entity that
	// should have been in the room and was not.
	CharacterID string
	// Members is who WAS in the room, so the refusal is diagnosable without a
	// second query.
	Members []string
	// Excluded is set when the character reached the view as a candidate and was
	// refused, and empty when it never appeared at all. The two want different
	// repairs — finish the import versus wait for the index — so they are not
	// reported as one failure.
	Excluded ExclusionReason
}

// Error implements error.
func (e *AbsentActorError) Error() string {
	members := "nobody"
	if len(e.Members) > 0 {
		members = strings.Join(e.Members, ", ")
	}
	cause := fmt.Sprintf(
		"either the membership index has not applied that character's %s edge yet or the character is no "+
			"longer there", vocabulary.WorldLocationCurrent)
	if e.Excluded != "" {
		cause = fmt.Sprintf(
			"that character was excluded from this context as %q, so its facts are not available to show",
			e.Excluded)
	}
	return fmt.Sprintf(
		"scene %s does not hold %s, the character player %s is playing in turn %s; the room holds %s. %s — "+
			"refusing either way, because a persona handed a room without the actor in it narrates somebody "+
			"who is not present",
		e.SceneID, e.CharacterID, e.PlayerID, e.TurnEntityID, members, cause)
}
