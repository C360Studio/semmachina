// Package effect is the deterministic applier: it turns the selected outcome
// band's proposed intents into committed world facts, or refuses the turn.
//
// It is the boundary where LLM judgment stops. The adjudicator declares intents
// per outcome band from a closed vocabulary, the dice pick a band, and from here
// on nothing reads fiction: every check is a lookup against registered
// vocabulary, registered bounds, and the graph's own state. No model is
// consulted, and none can be — there is no seam to consult one through.
//
// Two properties are worth stating because both are easy to lose.
//
// # Validation is the all-or-nothing
//
// Every intent in the batch is checked — vocabulary membership, per-type
// bounds, the existence of every entity it names, and whether the predicate it
// writes may live on the KIND of thing it names — before a single write is
// issued. So a batch rejected on validation grounds applies nothing at all.
// That is the only all-or-nothing available here, because the transport does
// not offer another: the entity merge lane is per-entity, a batch touching N
// targets is N independent writes, and the Nth can fail after the first N-1
// committed. A partial commit is therefore detected and named, never reported
// as applied, and recovery is idempotent re-application keyed on the turn —
// never a partial-batch repair.
//
// # The lane is the merge lane, and the whole value set travels with it
//
// Effects commit through graph.mutation.entity.update_with_triples and never
// through triple.add/.add_batch. The add lanes APPEND: a character committed
// through them accumulates health values instead of changing health, silently
// and with a success response. The merge lane replaces by (subject, predicate)
// — which is right for a single-valued fact and a trap for a multi-valued one,
// because it replaces the predicate's WHOLE value set. Adding one carried item
// through it while sending only the new one deletes every other carried item,
// again with a success response. So a relationship write reads the target's
// current set, folds the intent into it, and publishes the complete result;
// emptying a set needs the request's removal list, since an add list carrying
// no triples for a predicate leaves that predicate exactly as it was.
package effect
