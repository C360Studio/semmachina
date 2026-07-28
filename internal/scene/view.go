package scene

import (
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Entity is one entity as the assembler retrieved it: its identity and its
// REGISTERED facts, and nothing else.
//
// The triples are filtered to the engine's own vocabulary, which drops the
// framework's identity markers and anything a future component writes without
// registering. That is a bound as much as a tidy-up: a persona's context is
// made of facts this engine defines, so it cannot grow by something else
// deciding to write to an entity the scene happens to include.
//
// The triples are upstream message.Triple values rather than a local
// re-modelling, because the consumer of this view is a prompt builder and every
// re-shaping between the graph and the prompt is a place for the world and the
// story to disagree.
type Entity struct {
	// ID is the six-part entity ID.
	ID string
	// Triples are the entity's registered facts, in the order the graph
	// returned them.
	Triples []message.Triple
}

// Objects returns every object recorded for a predicate.
//
// A slice rather than a single value because several of the vocabulary's
// predicates are legitimately multi-valued — a character carries more than one
// thing — and a helper that returned the first would quietly hide the rest.
func (e Entity) Objects(predicate vocabulary.Predicate) []any {
	var out []any
	for _, triple := range e.Triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

// ExclusionReason names why a candidate entity is not in the view.
//
// It never reaches the graph — the graph records the world, not what a reader
// declined to show a persona — so it is an in-process answer rather than
// vocabulary.
type ExclusionReason string

// The ways a candidate entity is left out.
const (
	// ExcludedStub is a referenced-but-undelivered entity: queryable, carrying
	// only identity markers and none of its own facts.
	//
	// This is the exclusion that matters. A stub answers a read successfully, so
	// anything treating "the id resolves" as "the entity is loaded" hands a
	// persona a character with no name, no state, and no description — and
	// nothing errors. The scene quietly has three of seven members.
	ExcludedStub ExclusionReason = "stub"
	// ExcludedMissing is a referenced id the graph did not return at all.
	ExcludedMissing ExclusionReason = "missing"
)

// Exclusion is one candidate entity the assembler refused to include.
//
// Exclusions are REPORTED rather than swallowed, because the whole hazard of a
// half-populated context is that it looks like a small one. A caller that logs
// this list can tell "the room has three people in it" from "the room has seven
// people in it and four of them have not been imported yet".
type Exclusion struct {
	// ID is the entity that was left out.
	ID string
	// Reason is why.
	Reason ExclusionReason
}

// Size is the assembled context's measured weight.
//
// It exists so "how big was this context" is answerable after the fact, which
// is what a flat per-turn cost claim needs in order to be checkable rather than
// asserted. Bytes is a RETRIEVAL weight — the bytes of the facts retrieved —
// and deliberately not a token count: tokens are a property of the prompt, and
// the prompt does not exist yet at this layer.
type Size struct {
	// Entities is how many entities the view carries, including the scene and
	// the turn.
	Entities int
	// Triples is how many registered facts they carry between them.
	Triples int
	// Bytes is the summed byte weight of those facts.
	Bytes int
}

func (s Size) String() string {
	return fmt.Sprintf("%d entities, %d triples, %d bytes", s.Entities, s.Triples, s.Bytes)
}

// View is what a persona is shown: the turn, the scene, who is in it, what they
// are one hop from, and what was left out.
//
// It is a value, not a handle. Nothing here re-reads the graph, so a view is a
// record of what the world looked like at AssembledAt — which is execution
// time, because that is when the assembler ran.
type View struct {
	// TurnID and TurnEntityID identify the turn this context was assembled for.
	TurnID       string
	TurnEntityID string
	// SceneID is the scene the turn is happening in, read from the turn entity
	// rather than supplied by the caller.
	SceneID string
	// AssembledAt is when the query ran. It is the answer to "how fresh is
	// this", and under email-cadence play the gap between it and the action's
	// arrival can be days.
	AssembledAt time.Time

	// Turn carries the turn's own artifacts — the phase, and the references to
	// the action, verdict, roll, effects, and narration produced so far.
	//
	// The references are left as references. Resolving them means reading the
	// content store, which is the prompt builder's business: this component
	// answers what the graph says, and the graph says where the prose is.
	Turn Entity
	// Scene is the scene entity's own facts.
	Scene Entity
	// Members are the entities IN the scene, sorted by id so one world state
	// assembles to one view.
	Members []Entity
	// Neighbours are the entities members and the scene point at that are not
	// themselves in the scene — the 1-hop boundary. They are hydrated so a
	// persona can name what a character carries or knows, and the traversal
	// stops here because there is no code that goes further.
	Neighbours []Entity
	// Excluded names every candidate left out, with a reason.
	Excluded []Exclusion
	// Size is this view's measured weight.
	Size Size
}

// Entities returns every entity in the view, in a stable order: the turn, the
// scene, the members, then the neighbours.
func (v *View) Entities() []Entity {
	out := make([]Entity, 0, 2+len(v.Members)+len(v.Neighbours))
	out = append(out, v.Turn, v.Scene)
	out = append(out, v.Members...)
	return append(out, v.Neighbours...)
}

// project filters an entity's triples to the registered vocabulary and measures
// what survived.
func project(state *graph.EntityState) Entity {
	entity := Entity{ID: state.ID, Triples: make([]message.Triple, 0, len(state.Triples))}
	for _, triple := range state.Triples {
		if !registered[vocabulary.Predicate(triple.Predicate)] {
			continue
		}
		entity.Triples = append(entity.Triples, triple)
	}
	return entity
}

// registered is the engine's predicate set as a lookup. Built once from
// vocabulary.AllPredicates so the filter cannot drift from the vocabulary.
var registered = func() map[vocabulary.Predicate]bool {
	set := make(map[vocabulary.Predicate]bool)
	for _, predicate := range vocabulary.AllPredicates() {
		set[predicate] = true
	}
	return set
}()

// measure sums a view's retrieval weight.
func measure(entities []Entity) Size {
	size := Size{Entities: len(entities)}
	for _, entity := range entities {
		size.Bytes += len(entity.ID)
		size.Triples += len(entity.Triples)
		for _, triple := range entity.Triples {
			size.Bytes += len(triple.Predicate) + len(fmt.Sprint(triple.Object))
		}
	}
	return size
}
