package payload

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// CaseDecisionStatus distinguishes a real casekeeper interpretation from the
// deterministic artifact used when the turn has no active mystery case.
type CaseDecisionStatus string

const (
	// CaseDecisionStatusDecision records a real casekeeper decision.
	CaseDecisionStatusDecision CaseDecisionStatus = "decision"
	// CaseDecisionStatusNotApplicable records deterministic non-mystery work.
	CaseDecisionStatusNotApplicable CaseDecisionStatus = "not-applicable"
)

// CaseDecisionRecord is the entity-only stored envelope for interpretation.
// It is not registered because it never crosses the message bus.
type CaseDecisionRecord struct {
	TurnID   string             `json:"turn_id"`
	ActionID string             `json:"action_id"`
	Status   CaseDecisionStatus `json:"status"`
	Decision *CaseDecision      `json:"decision,omitempty"`
}

// Schema identifies the entity-only record without registering a decoder
// factory for a message shape the engine never publishes.
func (r *CaseDecisionRecord) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryCaseDecisionRecord, Version: SchemaVersion}
}

// Validate holds the record and its optional decision to one turn/action pair.
func (r *CaseDecisionRecord) Validate() error {
	if err := requireIDSegment("turn_id", r.TurnID); err != nil {
		return err
	}
	if err := requireActionID("action_id", r.ActionID); err != nil {
		return err
	}
	if expected := TurnIDForAction(r.ActionID); r.TurnID != expected {
		return fmt.Errorf("turn_id %q is not the 1:1 turn for action_id %q (expected %q)",
			r.TurnID, r.ActionID, expected)
	}
	switch r.Status {
	case CaseDecisionStatusDecision:
		if r.Decision == nil {
			return fmt.Errorf("decision is required when status is %q", r.Status)
		}
		if err := r.Decision.Validate(); err != nil {
			return fmt.Errorf("decision: %w", err)
		}
		if r.Decision.TurnID != r.TurnID || r.Decision.ActionID != r.ActionID {
			return fmt.Errorf(
				"decision identity (%q, %q) does not match record identity (%q, %q)",
				r.Decision.TurnID, r.Decision.ActionID, r.TurnID, r.ActionID)
		}
	case CaseDecisionStatusNotApplicable:
		if r.Decision != nil {
			return fmt.Errorf("decision is forbidden when status is %q", r.Status)
		}
	default:
		return fmt.Errorf("status %q is not a registered case decision record status", r.Status)
	}
	return nil
}

// MarshalJSON implements json.Marshaler using the entity artifact body only.
func (r *CaseDecisionRecord) MarshalJSON() ([]byte, error) {
	type Alias CaseDecisionRecord
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *CaseDecisionRecord) UnmarshalJSON(data []byte) error {
	type Alias CaseDecisionRecord
	return json.Unmarshal(data, (*Alias)(r))
}

// Triples exposes only the durable artifact reference and, for a real
// decision, its closed kind. Private identities and proposed references remain
// inside the stored record.
func (r *CaseDecisionRecord) Triples(
	turnEntityID, decisionRef, source string,
	at time.Time,
) ([]message.Triple, error) {
	projection := tripleProjection{
		payload: r,
		subject: turnEntityID,
		turnID:  r.TurnID,
		source:  source,
		at:      at,

		refPredicate: vocabulary.TurnCaseDecisionRef,
		refName:      "case decision ref",
		ref:          decisionRef,
	}
	if r.Status == CaseDecisionStatusNotApplicable {
		projection.scalarless = true
		projection.registered = vocabulary.CaseDecisionNoOpScalarPredicates()
	} else if r.Decision != nil {
		projection.registered = vocabulary.CaseDecisionScalarPredicates()
		projection.objects = map[vocabulary.Predicate]any{
			vocabulary.TurnCaseDecisionKind: string(r.Decision.Kind),
		}
	}
	return projection.build()
}
