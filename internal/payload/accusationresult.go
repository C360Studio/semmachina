package payload

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/message"
)

const accusationResultIdentityDomain = "accusation-result/v1"

// AccusationOutcome is the deliberately non-diagnostic result vocabulary.
type AccusationOutcome string

const (
	// AccusationCorrect means every submitted canonical identity matched.
	AccusationCorrect AccusationOutcome = "correct"
	// AccusationIncorrect means at least one identity did not match. It does not
	// identify the failed dimension.
	AccusationIncorrect AccusationOutcome = "incorrect"
)

var orderedAccusationOutcomes = []AccusationOutcome{AccusationCorrect, AccusationIncorrect}

// AccusationOutcomes returns the closed outcome set in schema order.
func AccusationOutcomes() []AccusationOutcome { return slices.Clone(orderedAccusationOutcomes) }

// AccusationResult is the complete non-revealing verifier result carried on
// the message bus and stored behind the turn's accusation reference.
type AccusationResult struct {
	ResultID   string            `json:"result_id"`
	TurnID     string            `json:"turn_id"`
	CaseID     string            `json:"case_id"`
	DecisionID string            `json:"decision_id"`
	Outcome    AccusationOutcome `json:"outcome"`
}

// AccusationResultID returns the stable identity of one accusation decision.
func AccusationResultID(turnID, caseID, decisionID string) string {
	return deterministicID(accusationResultIdentityDomain, turnID, caseID, decisionID)
}

// Schema implements message.Payload.
func (r *AccusationResult) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryAccusationResult, Version: SchemaVersion}
}

// Validate implements message.Payload without normalizing the receiver.
func (r *AccusationResult) Validate() error {
	if err := requireIDSegment("turn_id", r.TurnID); err != nil {
		return err
	}
	if err := requireEntityID("case_id", r.CaseID); err != nil {
		return err
	}
	if err := requireSHA256("decision_id", r.DecisionID); err != nil {
		return err
	}
	if !slices.Contains(orderedAccusationOutcomes, r.Outcome) {
		return fmt.Errorf("outcome %q is not a registered accusation outcome", r.Outcome)
	}
	expected := AccusationResultID(r.TurnID, r.CaseID, r.DecisionID)
	if r.ResultID != expected {
		return fmt.Errorf("result_id %q does not match the deterministic identity %q", r.ResultID, expected)
	}
	return nil
}

func requireSHA256(field, value string) error {
	if len(value) != 64 {
		return fmt.Errorf("%s must be a 64-character lowercase SHA-256 value", field)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return fmt.Errorf("%s must be a 64-character lowercase SHA-256 value", field)
	}
	return nil
}

// MarshalJSON emits only the payload body; BaseMessage owns the envelope.
func (r *AccusationResult) MarshalJSON() ([]byte, error) {
	type Alias AccusationResult
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON decodes only the payload body.
func (r *AccusationResult) UnmarshalJSON(data []byte) error {
	type Alias AccusationResult
	return json.Unmarshal(data, (*Alias)(r))
}
