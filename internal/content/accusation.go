package content

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// AccusationRecordStatus distinguishes the universal no-op barrier from a
// completed deterministic verification.
type AccusationRecordStatus string

const (
	// AccusationNotApplicable completes the barrier for non-accuse and non-mystery work.
	AccusationNotApplicable AccusationRecordStatus = "not-applicable"
	// AccusationResultRecorded carries one complete registered result.
	AccusationResultRecorded AccusationRecordStatus = "result"
)

// AccusationRecord is the unregistered ObjectStore envelope behind every
// turn.accusation.ref. Only its Result member is a registered bus payload.
type AccusationRecord struct {
	TurnID string                    `json:"turn_id"`
	Status AccusationRecordStatus    `json:"status"`
	Result *payload.AccusationResult `json:"result,omitempty"`
}

// Validate enforces exactly one of the no-op and result shapes.
func (r *AccusationRecord) Validate() error {
	if r == nil {
		return errors.New("accusation record is nil")
	}
	if err := vocabulary.ValidateIDSegment(r.TurnID); err != nil {
		return fmt.Errorf("turn_id: %w", err)
	}
	switch r.Status {
	case AccusationNotApplicable:
		if r.Result != nil {
			return errors.New("not-applicable accusation record forbids a result")
		}
	case AccusationResultRecorded:
		if r.Result == nil {
			return errors.New("result accusation record requires a result")
		}
		if err := r.Result.Validate(); err != nil {
			return fmt.Errorf("accusation result: %w", err)
		}
		if r.Result.TurnID != r.TurnID {
			return errors.New("accusation record and result name different turns")
		}
	default:
		return fmt.Errorf("unknown accusation record status %q", r.Status)
	}
	return nil
}

// MarshalJSON keeps the storage artifact envelope-free.
func (r *AccusationRecord) MarshalJSON() ([]byte, error) {
	type Alias AccusationRecord
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON decodes the storage artifact body.
func (r *AccusationRecord) UnmarshalJSON(data []byte) error {
	type Alias AccusationRecord
	return json.Unmarshal(data, (*Alias)(r))
}
