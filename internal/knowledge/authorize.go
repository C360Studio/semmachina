// Package knowledge authorizes and commits deterministic actor-scoped evidence
// grants. It never reads action prose and never invokes a model.
package knowledge

import (
	"context"
	"fmt"
	"slices"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// AuthorizationReason is the closed reason an untrusted reveal proposal is refused.
type AuthorizationReason string

const (
	// ReasonWrongTurn means the stored decision artifact belongs to a different
	// turn than the durable trigger being handled.
	ReasonWrongTurn AuthorizationReason = "wrong-turn"
	// ReasonWrongCase means the decision names a different case.
	ReasonWrongCase AuthorizationReason = "wrong-case"
	// ReasonWrongActor means the decision actor is not the turn's acting actor.
	ReasonWrongActor AuthorizationReason = "wrong-actor"
	// ReasonInvalidTarget means the decision's structural target is not admissible.
	ReasonInvalidTarget AuthorizationReason = "invalid-target"
	// ReasonIneligibleReveal means authored eligibility does not admit the evidence.
	ReasonIneligibleReveal AuthorizationReason = "ineligible-reveal"
	// ReasonIneligiblePhase means the case has not reached the evidence's minimum phase.
	ReasonIneligiblePhase AuthorizationReason = "ineligible-phase"
	// ReasonSolutionLocked means solution evidence was proposed before denouement.
	ReasonSolutionLocked AuthorizationReason = "solution-locked"
	// ReasonQuestionTargetMismatch means the questioned actor holds no matching authored belief.
	ReasonQuestionTargetMismatch AuthorizationReason = "question-target-mismatch"
	// ReasonShareSourceUnknown means the sharing actor does not know the evidence.
	ReasonShareSourceUnknown AuthorizationReason = "share-source-unknown"
	// ReasonShareTargetUnauthorized means the injected bond policy refuses the recipient.
	ReasonShareTargetUnauthorized AuthorizationReason = "share-target-unauthorized"
	// ReasonWitnessUnauthorized is reserved for the Group 5.3 witnessed-discovery flow.
	ReasonWitnessUnauthorized AuthorizationReason = "witness-unauthorized"
	// ReasonUnsupportedKind means the decision kind cannot authorize the proposed reveal.
	ReasonUnsupportedKind AuthorizationReason = "unsupported-kind"
)

var authorizationReasons = []AuthorizationReason{
	ReasonWrongTurn, ReasonWrongCase, ReasonWrongActor, ReasonInvalidTarget, ReasonIneligibleReveal,
	ReasonIneligiblePhase, ReasonSolutionLocked, ReasonQuestionTargetMismatch,
	ReasonShareSourceUnknown, ReasonShareTargetUnauthorized, ReasonWitnessUnauthorized,
	ReasonUnsupportedKind,
}

// AuthorizationReasons returns the complete reason set.
func AuthorizationReasons() []AuthorizationReason { return slices.Clone(authorizationReasons) }

// AuthorizationError is a permanent refusal of the complete proposed batch.
type AuthorizationError struct {
	Reason AuthorizationReason
	Detail string
}

func (e *AuthorizationError) Error() string {
	if e.Detail == "" {
		return string(e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

func reject(reason AuthorizationReason, format string, args ...any) error {
	return &AuthorizationError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// IsReason reports whether err is one authorization refusal with reason.
func IsReason(err error, reason AuthorizationReason) bool {
	rejection, ok := err.(*AuthorizationError)
	return ok && rejection.Reason == reason
}

// eligibility is the resolved authored allow-set for one evidence entity.
type eligibility struct {
	MinimumPhase vocabulary.CasePhase
	Kinds        []vocabulary.EvidenceRevealKind
	Targets      []string
}

// beliefKey selects one authored belief without consulting truth.
type beliefKey struct{ ActorID, EvidenceID string }

// knowledgeKey selects one actor-scoped knowledge fact.
type knowledgeKey struct{ ActorID, EvidenceID string }

// AuthoredBelief is the private testimony source. Truth is intentionally absent.
type AuthoredBelief struct {
	ID, HolderID, EvidenceID string
	Stance                   vocabulary.BeliefStance
	Prose                    string
}

// Preflight is the complete read snapshot used to authorize a batch before any write.
type Preflight struct {
	Decision       *payload.CaseDecision
	ActingActorID  string
	CaseID         string
	CasePhase      vocabulary.CasePhase
	CaseEvidence   map[string]bool
	CaseCharacters map[string]bool
	Eligibility    map[string]eligibility
	Beliefs        map[beliefKey]AuthoredBelief
	Known          map[knowledgeKey]bool
	SolutionLocked map[string]bool
}

// ShareAuthorizer admits a target through the durable player-companion bond seam.
type ShareAuthorizer interface {
	Authorized(context.Context, string, string, string) (bool, error)
}

// DenyShares is the production policy until Group 7 provides durable bond authorization.
type DenyShares struct{}

// Authorized refuses every share until durable companion bonds exist in Group 7.
func (DenyShares) Authorized(context.Context, string, string, string) (bool, error) {
	return false, nil
}

// TestimonyDraft is the attributed private artifact derived for a question.
type TestimonyDraft struct {
	BeliefID, SourceActorID, RecipientID, EvidenceID string
	Stance                                           vocabulary.BeliefStance
	Prose                                            string
}

// Entry is one actor/evidence grant and its optional attributed testimony.
type Entry struct {
	RecipientID string
	EvidenceID  string
	Testimony   *TestimonyDraft
}

// Plan is the fully authorized batch. Persistence may begin only after this exists.
type Plan struct{ Entries []Entry }

// Authorize derives a complete commit plan without writing anything.
func Authorize(ctx context.Context, input Preflight, shares ShareAuthorizer) (Plan, error) {
	decision := input.Decision
	if decision == nil {
		return Plan{}, reject(ReasonUnsupportedKind, "missing case decision")
	}
	if decision.CaseID != input.CaseID {
		return Plan{}, reject(ReasonWrongCase, "decision case %s does not match %s", decision.CaseID, input.CaseID)
	}
	if decision.ActorID != input.ActingActorID {
		return Plan{}, reject(ReasonWrongActor, "decision actor %s does not match %s", decision.ActorID, input.ActingActorID)
	}

	switch decision.Kind {
	case payload.CaseDecisionObserve, payload.CaseDecisionInvestigate:
		kind := vocabulary.EvidenceRevealKind(decision.Kind)
		for _, evidenceID := range decision.RevealRefs {
			if input.SolutionLocked[evidenceID] && input.CasePhase != vocabulary.CasePhaseDenouement {
				return Plan{}, reject(ReasonSolutionLocked, "evidence %s is solution material", evidenceID)
			}
			if !input.CaseEvidence[evidenceID] {
				return Plan{}, reject(ReasonIneligibleReveal, "evidence %s is outside case %s", evidenceID, input.CaseID)
			}
			eligible, ok := input.Eligibility[evidenceID]
			if !ok || !slices.Contains(eligible.Kinds, kind) {
				return Plan{}, reject(ReasonIneligibleReveal, "evidence %s does not admit %s", evidenceID, decision.Kind)
			}
			if !phaseAtLeast(input.CasePhase, eligible.MinimumPhase) {
				return Plan{}, reject(ReasonIneligiblePhase, "case phase %s is before %s", input.CasePhase, eligible.MinimumPhase)
			}
			if !intersects(decision.TargetRefs, eligible.Targets) {
				return Plan{}, reject(ReasonInvalidTarget, "decision targets do not authorize evidence %s", evidenceID)
			}
		}
		return actorEntries(input.ActingActorID, decision.RevealRefs), nil

	case payload.CaseDecisionQuestion:
		if len(decision.TargetRefs) != 1 || !input.CaseCharacters[decision.TargetRefs[0]] {
			return Plan{}, reject(ReasonInvalidTarget, "question requires exactly one case character")
		}
		target := decision.TargetRefs[0]
		entries := make([]Entry, 0, len(decision.RevealRefs))
		for _, evidenceID := range decision.RevealRefs {
			if input.SolutionLocked[evidenceID] && input.CasePhase != vocabulary.CasePhaseDenouement {
				return Plan{}, reject(ReasonSolutionLocked, "evidence %s is solution material", evidenceID)
			}
			if !input.CaseEvidence[evidenceID] {
				return Plan{}, reject(ReasonIneligibleReveal, "evidence %s is outside case %s", evidenceID, input.CaseID)
			}
			belief, ok := input.Beliefs[beliefKey{ActorID: target, EvidenceID: evidenceID}]
			if !ok {
				return Plan{}, reject(ReasonQuestionTargetMismatch,
					"target %s holds no authored belief for %s", target, evidenceID)
			}
			entries = append(entries, Entry{RecipientID: input.ActingActorID, EvidenceID: evidenceID,
				Testimony: &TestimonyDraft{
					BeliefID: belief.ID, SourceActorID: belief.HolderID, RecipientID: input.ActingActorID,
					EvidenceID: evidenceID, Stance: belief.Stance, Prose: belief.Prose,
				}})
		}
		return Plan{Entries: entries}, nil

	case payload.CaseDecisionShare:
		if len(decision.TargetRefs) != 1 {
			return Plan{}, reject(ReasonInvalidTarget, "share requires exactly one recipient")
		}
		recipient := decision.TargetRefs[0]
		for _, evidenceID := range decision.RevealRefs {
			if !input.Known[knowledgeKey{ActorID: input.ActingActorID, EvidenceID: evidenceID}] {
				return Plan{}, reject(ReasonShareSourceUnknown, "actor does not know %s", evidenceID)
			}
			allowed := false
			if shares != nil {
				var err error
				allowed, err = shares.Authorized(ctx, input.ActingActorID, recipient, evidenceID)
				if err != nil {
					return Plan{}, fmt.Errorf("authorize share target: %w", err)
				}
			}
			if !allowed {
				return Plan{}, reject(ReasonShareTargetUnauthorized, "recipient %s is not authorized", recipient)
			}
		}
		return actorEntries(recipient, decision.RevealRefs), nil

	case payload.CaseDecisionRequestHint, payload.CaseDecisionAccuse, payload.CaseDecisionOther:
		if len(decision.RevealRefs) != 0 {
			return Plan{}, reject(ReasonUnsupportedKind, "%s decisions cannot reveal evidence", decision.Kind)
		}
		return Plan{}, nil
	default:
		return Plan{}, reject(ReasonUnsupportedKind, "decision kind %s is unsupported", decision.Kind)
	}
}

func actorEntries(actorID string, evidenceIDs []string) Plan {
	entries := make([]Entry, 0, len(evidenceIDs))
	for _, evidenceID := range evidenceIDs {
		entries = append(entries, Entry{RecipientID: actorID, EvidenceID: evidenceID})
	}
	return Plan{Entries: entries}
}

func intersects(left, right []string) bool {
	for _, value := range left {
		if slices.Contains(right, value) {
			return true
		}
	}
	return false
}

func phaseAtLeast(current, minimum vocabulary.CasePhase) bool {
	order := vocabulary.CasePhases()
	return slices.Index(order, current) >= slices.Index(order, minimum) && slices.Index(order, minimum) >= 0
}
