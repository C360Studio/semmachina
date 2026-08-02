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
// every other vocabulary here is seeded. Four are the effect applier's, two are
// the bounded-cognition guarantee's, and one belongs to the boot-time
// stranded-turn pass; further reasons join them as the stages that produce them
// land.
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
	// could not all be confirmed. A transient cause is retried and never records
	// this reason; an invalid or fatal classified cause ends once with it. The
	// named target may have mutated before its response failed, while Committed
	// is only the confirmed-success response prefix.
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
	// FailurePersonaLoopFailed reports a persona loop that ended without a
	// terminal exit for a reason OTHER than its iteration cap — a model error, a
	// provider refusal, a handler fault inside the loop.
	//
	// It is the far more common half of the same guarantee, and it had no code
	// until now, which meant the far more common half was the one that stalled.
	// Cap exhaustion is a loop that tried its whole budget; this is a loop that
	// stopped trying, and from the turn's point of view they are the same event:
	// nobody is going to produce the artifact this stage was spawned for. The
	// cause is not on the graph — it is prose the loop reported — so it rides
	// behind TurnFailureRef exactly as the cap's does, and the code says only
	// which of the two endings happened.
	FailurePersonaLoopFailed FailureReason = "persona-loop-failed"
	// FailureTurnStranded reports a turn abandoned after the boot-time
	// stranded-turn pass exhausted its re-trigger attempts.
	//
	// A stranded turn is one whose phase is non-terminal and whose stage is not
	// running: nothing holds a trigger for it, no rule currently matches it, and
	// no notification is owed. The reconciliation re-triggers it a bounded number
	// of times, and this code is what the bound MEANS — a turn that has not
	// produced its stage's artifact across that many separate boots is ended on
	// the record rather than left waiting forever, which is the same trade cap
	// exhaustion makes for a loop.
	FailureTurnStranded FailureReason = "turn-stranded"
	// FailureKnowledgeUnauthorized reports a permanently refused reveal batch.
	FailureKnowledgeUnauthorized FailureReason = "knowledge-unauthorized"
	// FailureAccusationInvalid reports foreign, malformed, or colliding accusation state.
	FailureAccusationInvalid FailureReason = "accusation-invalid"
	// FailureCaseProgressInvalid reports permanently invalid deterministic case-progress state.
	FailureCaseProgressInvalid FailureReason = "case-progress-invalid"
)

var failureReasonEnum = newEnum(KindFailureReason,
	FailureEffectInvalid, FailureEffectEntityMissing,
	FailureEffectEntityKind, FailureEffectCommitIncomplete,
	FailurePersonaCapExhausted, FailurePersonaLoopFailed, FailureTurnStranded,
	FailureKnowledgeUnauthorized, FailureAccusationInvalid, FailureCaseProgressInvalid)

// FailureReasons returns the closed failure-reason set.
func FailureReasons() []FailureReason { return failureReasonEnum.all() }

// Valid reports whether r is in the closed failure-reason set.
func (r FailureReason) Valid() bool { return failureReasonEnum.valid(r) }

// ParseFailureReason accepts only registered failure reasons.
func ParseFailureReason(s string) (FailureReason, error) { return failureReasonEnum.parse(s) }
