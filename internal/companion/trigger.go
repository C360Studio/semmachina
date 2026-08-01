package companion

import (
	"fmt"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Trigger is the closed, persisted decision about whether the companion stage acts.
type Trigger struct {
	Kind   vocabulary.CompanionTriggerKind
	Source vocabulary.CompanionTriggerSource
}

// SelectTrigger applies the structural priority player-hint > warning > none.
func SelectTrigger(state *graph.EntityState, bond *Bond) (Trigger, error) {
	if state == nil || bond == nil {
		return Trigger{}, fmt.Errorf("companion trigger selection requires a turn and bond")
	}
	caseKind, casePresent, err := optionalSoleString(state, vocabulary.TurnCaseDecisionKind)
	if err != nil {
		return Trigger{}, err
	}
	if casePresent && caseKind == string(payload.CaseDecisionRequestHint) {
		return Trigger{Kind: vocabulary.CompanionTriggerPlayerHint,
			Source: vocabulary.CompanionTriggerSourceCaseDecision}, nil
	}
	if bond.Policy == vocabulary.CompanionPolicyBoundedInitiative {
		band, bandPresent, err := optionalSoleString(state, vocabulary.TurnRollBand)
		if err != nil {
			return Trigger{}, err
		}
		risk, riskPresent, err := optionalSoleString(state, vocabulary.TurnVerdictRisk)
		if err != nil {
			return Trigger{}, err
		}
		consequence, consequencePresent, err := optionalSoleString(state, vocabulary.TurnVerdictConsequence)
		if err != nil {
			return Trigger{}, err
		}
		badBand := band == string(vocabulary.BandMiss) || band == string(vocabulary.BandPartial)
		badConsequence := consequence == string(vocabulary.ConsequenceHarm) ||
			consequence == string(vocabulary.ConsequenceEscalation)
		if bandPresent && riskPresent && consequencePresent && badBand &&
			risk == string(vocabulary.RiskHigh) && badConsequence {
			return Trigger{Kind: vocabulary.CompanionTriggerWarning,
				Source: vocabulary.CompanionTriggerSourceResolvedRisk}, nil
		}
	}
	return Trigger{Kind: vocabulary.CompanionTriggerNone, Source: vocabulary.CompanionTriggerSourceNone}, nil
}

// PersistedTrigger reads the exact closed trigger facts written before work began.
func PersistedTrigger(state *graph.EntityState) (Trigger, error) {
	kindText, present, err := optionalSoleString(state, vocabulary.TurnCompanionTriggerKind)
	if err != nil {
		return Trigger{}, err
	}
	if !present {
		return Trigger{}, fmt.Errorf("companion trigger kind is not durably recorded")
	}
	sourceText, present, err := optionalSoleString(state, vocabulary.TurnCompanionTriggerSource)
	if err != nil {
		return Trigger{}, err
	}
	if !present {
		return Trigger{}, fmt.Errorf("companion trigger source is not durably recorded")
	}
	kind, err := vocabulary.ParseCompanionTrigger(kindText)
	if err != nil {
		return Trigger{}, err
	}
	source, err := vocabulary.ParseCompanionTriggerSource(sourceText)
	if err != nil {
		return Trigger{}, err
	}
	return Trigger{Kind: kind, Source: source}, nil
}

func optionalSoleString(state *graph.EntityState, predicate vocabulary.Predicate) (string, bool, error) {
	var value string
	count := 0
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		text, ok := triple.Object.(string)
		if !ok || text == "" {
			return "", false, fmt.Errorf("turn %s predicate %s is not a non-empty string", state.ID, predicate)
		}
		value = text
		count++
	}
	if count > 1 {
		return "", false, fmt.Errorf("turn %s carries %d values for %s", state.ID, count, predicate)
	}
	return value, count == 1, nil
}
