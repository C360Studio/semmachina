package content

import (
	"errors"
	"fmt"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

const caseProgressIdentityDomain = "case-progress/v1"

// CaseProgressStatus distinguishes a recorded lifecycle receipt from the
// deterministic barrier used by decisions that do not advance case phase.
type CaseProgressStatus string

const (
	// CaseProgressEventRecorded records that one lifecycle receipt was handled.
	CaseProgressEventRecorded CaseProgressStatus = "event-recorded"
	// CaseProgressNotApplicable is the exact barrier for a non-progressing decision.
	CaseProgressNotApplicable CaseProgressStatus = "not-applicable"
)

// CaseProgressRecord is the entity-only result of deterministic case progress.
type CaseProgressRecord struct {
	TurnID     string                            `json:"turn_id"`
	DecisionID string                            `json:"decision_id,omitempty"`
	CaseID     string                            `json:"case_id,omitempty"`
	Status     CaseProgressStatus                `json:"status"`
	EventID    string                            `json:"event_id,omitempty"`
	EventKind  vocabulary.CaseLifecycleEventKind `json:"event_kind,omitempty"`
}

// CaseProgressEventID derives one stable event from the turn, decision, and kind.
func CaseProgressEventID(
	turnID, decisionID string, kind vocabulary.CaseLifecycleEventKind,
) string {
	return framedID(caseProgressIdentityDomain, turnID, decisionID, string(kind))
}

// Validate holds the resident barrier to one of its two closed shapes.
func (r *CaseProgressRecord) Validate() error {
	if r == nil {
		return errors.New("case progress record is nil")
	}
	if err := vocabulary.ValidateIDSegment(r.TurnID); err != nil {
		return fmt.Errorf("turn_id: %w", err)
	}
	switch r.Status {
	case CaseProgressEventRecorded:
		if r.DecisionID == "" || r.CaseID == "" || r.EventID == "" {
			return errors.New("recorded case progress requires decision_id, case_id, and event_id")
		}
		if err := types.ValidateEntityID(r.CaseID); err != nil {
			return fmt.Errorf("case_id: %w", err)
		}
		if _, err := vocabulary.ParseCaseLifecycleEventKind(string(r.EventKind)); err != nil {
			return fmt.Errorf("event_kind: %w", err)
		}
		if expected := CaseProgressEventID(r.TurnID, r.DecisionID, r.EventKind); r.EventID != expected {
			return fmt.Errorf("event_id %q does not match deterministic identity %q", r.EventID, expected)
		}
	case CaseProgressNotApplicable:
		if r.EventID != "" || r.EventKind != "" {
			return errors.New("not-applicable case progress forbids event_id and event_kind")
		}
		if r.CaseID != "" {
			if err := types.ValidateEntityID(r.CaseID); err != nil {
				return fmt.Errorf("case_id: %w", err)
			}
			if r.DecisionID == "" {
				return errors.New("case-scoped not-applicable progress requires decision_id")
			}
		} else if r.DecisionID != "" {
			return errors.New("non-case progress forbids decision_id")
		}
	default:
		return fmt.Errorf("unknown case progress status %q", r.Status)
	}
	return nil
}

var _ interface{ Validate() error } = (*CaseProgressRecord)(nil)
