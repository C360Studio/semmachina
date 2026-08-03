package vocabulary

// AuthorizationReason is the closed reason a deterministic knowledge reveal
// proposal is refused. It lives in vocabulary so both authorization and stored
// failure diagnostics can depend on the same set without an import cycle.
type AuthorizationReason string

// Closed authorization reasons emitted by deterministic knowledge checks.
const (
	AuthorizationWrongTurn               AuthorizationReason = "wrong-turn"
	AuthorizationWrongCase               AuthorizationReason = "wrong-case"
	AuthorizationWrongActor              AuthorizationReason = "wrong-actor"
	AuthorizationInvalidTarget           AuthorizationReason = "invalid-target"
	AuthorizationIneligibleReveal        AuthorizationReason = "ineligible-reveal"
	AuthorizationIneligiblePhase         AuthorizationReason = "ineligible-phase"
	AuthorizationSolutionLocked          AuthorizationReason = "solution-locked"
	AuthorizationQuestionTargetMismatch  AuthorizationReason = "question-target-mismatch"
	AuthorizationShareSourceUnknown      AuthorizationReason = "share-source-unknown"
	AuthorizationShareTargetUnauthorized AuthorizationReason = "share-target-unauthorized"
	AuthorizationWitnessUnauthorized     AuthorizationReason = "witness-unauthorized"
	AuthorizationUnsupportedKind         AuthorizationReason = "unsupported-kind"
)

var authorizationReasonEnum = newEnum(KindAuthorizationReason,
	AuthorizationWrongTurn,
	AuthorizationWrongCase,
	AuthorizationWrongActor,
	AuthorizationInvalidTarget,
	AuthorizationIneligibleReveal,
	AuthorizationIneligiblePhase,
	AuthorizationSolutionLocked,
	AuthorizationQuestionTargetMismatch,
	AuthorizationShareSourceUnknown,
	AuthorizationShareTargetUnauthorized,
	AuthorizationWitnessUnauthorized,
	AuthorizationUnsupportedKind,
)

// AuthorizationReasons returns the complete closed reason set.
func AuthorizationReasons() []AuthorizationReason { return authorizationReasonEnum.all() }

// Valid reports whether r is in the closed authorization-reason set.
func (r AuthorizationReason) Valid() bool { return authorizationReasonEnum.valid(r) }

// ParseAuthorizationReason accepts only registered authorization reasons.
func ParseAuthorizationReason(value string) (AuthorizationReason, error) {
	return authorizationReasonEnum.parse(value)
}
