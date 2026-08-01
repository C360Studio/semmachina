package payload

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/message"
)

const (
	// MaxCaseDecisionTargets bounds the casekeeper's structural targets.
	MaxCaseDecisionTargets = 8
	// MaxCaseDecisionReveals bounds the casekeeper's proposed knowledge reveals.
	MaxCaseDecisionReveals = 12

	caseDecisionIdentityDomain = "case-decision/v1"
)

// CaseDecisionKind is the casekeeper's closed structural interpretation of a
// player's natural-language mystery action.
type CaseDecisionKind string

const (
	// CaseDecisionObserve classifies direct observation.
	CaseDecisionObserve CaseDecisionKind = "observe"
	// CaseDecisionInvestigate classifies evidence-seeking investigation.
	CaseDecisionInvestigate CaseDecisionKind = "investigate"
	// CaseDecisionQuestion classifies questioning a target.
	CaseDecisionQuestion CaseDecisionKind = "question"
	// CaseDecisionShare classifies explicitly sharing known evidence.
	CaseDecisionShare CaseDecisionKind = "share"
	// CaseDecisionRequestHint classifies a request for companion help.
	CaseDecisionRequestHint CaseDecisionKind = "request_hint"
	// CaseDecisionAccuse classifies a complete structural accusation.
	CaseDecisionAccuse CaseDecisionKind = "accuse"
	// CaseDecisionOther is the closed fallback for an in-scope action.
	CaseDecisionOther CaseDecisionKind = "other"
)

var orderedCaseDecisionKinds = []CaseDecisionKind{
	CaseDecisionObserve, CaseDecisionInvestigate, CaseDecisionQuestion,
	CaseDecisionShare, CaseDecisionRequestHint, CaseDecisionAccuse, CaseDecisionOther,
}

// CaseDecisionKinds returns the closed kind set in schema order.
func CaseDecisionKinds() []CaseDecisionKind { return slices.Clone(orderedCaseDecisionKinds) }

// CaseDecision is the casekeeper persona's only structured exit. It contains
// closed classifications and entity references only; action prose remains in
// the private stored action and never becomes rule-visible state.
type CaseDecision struct {
	DecisionID string           `json:"decision_id"`
	TurnID     string           `json:"turn_id"`
	ActionID   string           `json:"action_id"`
	CaseID     string           `json:"case_id"`
	ActorID    string           `json:"actor_id"`
	Kind       CaseDecisionKind `json:"kind"`

	TargetRefs []string `json:"target_refs"`
	RevealRefs []string `json:"reveal_refs"`

	CulpritRef string `json:"culprit_ref,omitempty"`
	MethodRef  string `json:"method_ref,omitempty"`
	MotiveRef  string `json:"motive_ref,omitempty"`
}

// CaseDecisionID returns the stable identity for one interpretation attempt.
func CaseDecisionID(turnID, actionID, caseID, actorID string) string {
	return deterministicID(caseDecisionIdentityDomain, turnID, actionID, caseID, actorID)
}

// Schema implements message.Payload.
func (d *CaseDecision) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryCaseDecision, Version: SchemaVersion}
}

// Validate implements message.Payload without normalizing or mutating the
// receiver. Every reference is a canonical six-part entity identity.
func (d *CaseDecision) Validate() error {
	if err := requireIDSegment("turn_id", d.TurnID); err != nil {
		return err
	}
	if err := requireActionID("action_id", d.ActionID); err != nil {
		return err
	}
	if expected := TurnIDForAction(d.ActionID); d.TurnID != expected {
		return fmt.Errorf("turn_id %q is not the 1:1 turn for action_id %q (expected %q)",
			d.TurnID, d.ActionID, expected)
	}
	if err := requireEntityID("case_id", d.CaseID); err != nil {
		return err
	}
	if err := requireEntityID("actor_id", d.ActorID); err != nil {
		return err
	}
	if !slices.Contains(orderedCaseDecisionKinds, d.Kind) {
		return fmt.Errorf("kind %q is not a registered case decision kind", d.Kind)
	}
	if err := validateCaseDecisionRefs("target_refs", d.TargetRefs, MaxCaseDecisionTargets); err != nil {
		return err
	}
	if err := validateCaseDecisionRefs("reveal_refs", d.RevealRefs, MaxCaseDecisionReveals); err != nil {
		return err
	}
	if err := d.validateAccusationRefs(); err != nil {
		return err
	}
	if expected := CaseDecisionID(d.TurnID, d.ActionID, d.CaseID, d.ActorID); d.DecisionID != expected {
		return fmt.Errorf("decision_id %q does not match the deterministic identity %q", d.DecisionID, expected)
	}
	return nil
}

func validateCaseDecisionRefs(field string, refs []string, limit int) error {
	if len(refs) > limit {
		return fmt.Errorf("%s carries %d references; limit is %d", field, len(refs), limit)
	}
	seen := make(map[string]struct{}, len(refs))
	for idx, ref := range refs {
		if err := requireEntityID(fmt.Sprintf("%s[%d]", field, idx), ref); err != nil {
			return err
		}
		if _, duplicate := seen[ref]; duplicate {
			return fmt.Errorf("%s repeats reference %q", field, ref)
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func (d *CaseDecision) validateAccusationRefs() error {
	refs := []struct {
		name  string
		value string
	}{
		{name: "culprit_ref", value: d.CulpritRef},
		{name: "method_ref", value: d.MethodRef},
		{name: "motive_ref", value: d.MotiveRef},
	}
	if d.Kind == CaseDecisionAccuse {
		for _, ref := range refs {
			if err := requireEntityID(ref.name, ref.value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ref := range refs {
		if ref.value != "" {
			return fmt.Errorf("%s is forbidden when kind is %q", ref.name, d.Kind)
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The publisher owns the envelope.
func (d *CaseDecision) MarshalJSON() ([]byte, error) {
	type Alias CaseDecision
	return json.Marshal((*Alias)(d))
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *CaseDecision) UnmarshalJSON(data []byte) error {
	type Alias CaseDecision
	return json.Unmarshal(data, (*Alias)(d))
}
