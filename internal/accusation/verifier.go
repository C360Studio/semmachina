// Package accusation owns deterministic, exact-identity accusation handling.
package accusation

import (
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
)

// Verify compares the three submitted canonical identities exactly. Every
// mismatch collapses to the same incorrect result and exposes no dimension.
func Verify(decision *payload.CaseDecision, solution epistemic.Solution) (*payload.AccusationResult, error) {
	if decision == nil {
		return nil, errors.New("accusation verification requires a decision")
	}
	if err := decision.Validate(); err != nil {
		return nil, fmt.Errorf("invalid accusation decision: %w", err)
	}
	if decision.Kind != payload.CaseDecisionAccuse {
		return nil, fmt.Errorf("decision %s has kind %q, not accuse", decision.DecisionID, decision.Kind)
	}
	outcome := payload.AccusationIncorrect
	if decision.CulpritRef == solution.Culprit &&
		decision.MethodRef == solution.Method &&
		decision.MotiveRef == solution.Motive {
		outcome = payload.AccusationCorrect
	}
	result := &payload.AccusationResult{
		TurnID: decision.TurnID, CaseID: decision.CaseID,
		DecisionID: decision.DecisionID, Outcome: outcome,
	}
	result.ResultID = payload.AccusationResultID(result.TurnID, result.CaseID, result.DecisionID)
	return result, nil
}
