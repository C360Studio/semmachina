package vocabulary

// FailureReason is the closed set of machine-readable reasons a turn ends in
// the failed phase.
//
// It is a CODE, never a sentence, and that is the entire reason the type
// exists. The failure reason lands on the turn entity, which is rule-matching
// surface, and the shared triple projection gates an object's SHAPE (scalar,
// bounded length) but not its CLOSURE — so an applier- or persona-authored
// explanation would pass every gate and put free text where rules match.
// Explanatory detail belongs behind a reference; the graph gets the code.
//
// The set is seeded by the capability that produces each member, the same way
// every other vocabulary here is seeded. Four are the effect applier's and one
// is the bounded-cognition guarantee's; further reasons join them as the stages
// that produce them land.
type FailureReason string

// The closed failure-reason set.
const (
	// FailureEffectInvalid reports an intent that is out of vocabulary, carries
	// the wrong fields for its type, or exceeds a registered bound — plus the
	// batch-level incoherences (one target given two values for a single-valued
	// predicate, one relationship asserted twice) that have no single-intent
	// culprit.
	FailureEffectInvalid FailureReason = "effect-invalid"
	// FailureEffectEntityMissing reports an intent naming an entity that does
	// not exist, or that exists only as a referential stub. A stub is queryable
	// and factless, so treating it as present is how a turn commits a change to
	// an entity that was never born.
	FailureEffectEntityMissing FailureReason = "effect-entity-missing"
	// FailureEffectEntityKind reports an intent whose predicate cannot be
	// carried by the entity it names — health on a scene, a character stored
	// inside an item.
	FailureEffectEntityKind FailureReason = "effect-entity-kind"
	// FailureEffectCommitIncomplete reports a batch whose per-entity writes
	// partially landed. The merge lane is per-entity, so a multi-target batch is
	// N independent writes and any of them can fail after its predecessors
	// committed; the turn fails naming the target that did not land, and
	// recovery is idempotent re-application, never a partial-batch repair.
	FailureEffectCommitIncomplete FailureReason = "effect-commit-incomplete"
	// FailurePersonaCapExhausted reports a persona loop that reached its
	// MaxIterations cap without a terminal exit.
	//
	// It is the bounded-cognition guarantee made observable. Cap exhaustion with
	// no recorded outcome is a turn that stalls silently and a player who waits
	// forever, so the cap has an explicit fallback: the turn ends, on the record,
	// with this code. Any detail about WHICH loop and how far it got rides behind
	// TurnFailureRef.
	FailurePersonaCapExhausted FailureReason = "persona-cap-exhausted"
)

var failureReasonEnum = newEnum(KindFailureReason,
	FailureEffectInvalid, FailureEffectEntityMissing,
	FailureEffectEntityKind, FailureEffectCommitIncomplete,
	FailurePersonaCapExhausted)

// FailureReasons returns the closed failure-reason set.
func FailureReasons() []FailureReason { return failureReasonEnum.all() }

// Valid reports whether r is in the closed failure-reason set.
func (r FailureReason) Valid() bool { return failureReasonEnum.valid(r) }

// ParseFailureReason accepts only registered failure reasons.
func ParseFailureReason(s string) (FailureReason, error) { return failureReasonEnum.parse(s) }
