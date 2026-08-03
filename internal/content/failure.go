package content

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxFailureMessageBytes bounds the stored explanation.
//
// The message is an error's own words, and an error can be arbitrarily long —
// a wrapped chain naming every intent in a refused batch, say. A bound here is
// what keeps "the detail is stored" from meaning "the detail is unbounded
// spend", since the ledger and any operator surface read it back.
const MaxFailureMessageBytes = 4 * 1024

// FailureClass is the closed, credential-safe diagnostic category for a stored
// failure. Unlike Message, it is safe to project onto an operator surface.
type FailureClass string

// Closed failure classes safe for projection onto operator surfaces.
const (
	FailureClassProviderModel    FailureClass = "provider-model"
	FailureClassModelOutputLimit FailureClass = "model-output-limit"
	FailureClassAgentRuntime     FailureClass = "agent-runtime"
	FailureClassAgentLimit       FailureClass = "agent-limit"
	FailureClassDeterministic    FailureClass = "deterministic"
	FailureClassUnknown          FailureClass = "unknown"
)

var failureClasses = []FailureClass{
	FailureClassProviderModel,
	FailureClassModelOutputLimit,
	FailureClassAgentRuntime,
	FailureClassAgentLimit,
	FailureClassDeterministic,
	FailureClassUnknown,
}

// FailureClasses returns the closed diagnostic class set in declaration order.
func FailureClasses() []FailureClass { return slices.Clone(failureClasses) }

// ParseFailureClass accepts only registered diagnostic classes.
func ParseFailureClass(value string) (FailureClass, error) {
	class := FailureClass(value)
	if slices.Contains(failureClasses, class) {
		return class, nil
	}
	return "", fmt.Errorf("failure class %q is outside the closed diagnostic set", value)
}

// FailureDetail is the stored explanation behind a turn's closed failure code.
//
// It is deliberately NOT a registered payload. No message of this type is ever
// published: it is written to the content store and referenced from the turn
// entity, and nothing consumes it except a reader following that reference.
// Registering it would advertise a wire type nothing sends.
//
// Every field here is the half of a failure that must not reach a triple. The
// code on the graph is closed vocabulary a rule can match; Message is free
// text, and Committed is a list — neither is scalar, neither is closed, and
// both are exactly what an operator needs to understand what happened.
type FailureDetail struct {
	// TurnID is the turn this failure belongs to. A stored artifact that names
	// its own turn can be read back without its reference for context.
	TurnID string `json:"turn_id"`
	// Reason repeats the closed code that lands on the graph, so a detail read
	// on its own is self-describing and a detail filed under the wrong code is
	// detectable.
	Reason vocabulary.FailureReason `json:"reason"`
	// Class is a closed diagnostic category. It is optional only so details
	// written before this field existed remain readable; diagnostic readers map
	// an absent legacy value to unknown.
	Class FailureClass `json:"class,omitempty"`
	// AuthorizationReason is the closed deterministic refusal carried only by
	// knowledge-unauthorized failures. It is optional for legacy and ref-less
	// failure records.
	AuthorizationReason vocabulary.AuthorizationReason `json:"authorization_reason,omitempty"`
	// Message is the failure's own words — rule-opaque by construction, because
	// it lives here rather than on a triple.
	Message string `json:"message"`
	// Target names the entity a write failed on, when the failure had one. The
	// merge lane is per-entity, so "which target" is the first question a
	// partial commit raises and the one the closed code cannot answer.
	Target string `json:"target,omitempty"`
	// Committed names the targets that DID land before the failure, in commit
	// order. A partial commit that recorded only its failure would leave the
	// world changed and the change unrecorded.
	Committed []string `json:"committed,omitempty"`
}

// Validate holds the detail to its contract on the way in and on the way out.
func (d *FailureDetail) Validate() error {
	if err := vocabulary.ValidateIDSegment(d.TurnID); err != nil {
		return fmt.Errorf("turn_id: %w", err)
	}
	if _, err := vocabulary.ParseFailureReason(string(d.Reason)); err != nil {
		return err
	}
	if d.Class != "" {
		if _, err := ParseFailureClass(string(d.Class)); err != nil {
			return err
		}
	}
	if d.Reason == vocabulary.FailureKnowledgeUnauthorized && d.Class != "" && d.AuthorizationReason == "" {
		return fmt.Errorf("classed %s failure detail requires authorization_reason",
			vocabulary.FailureKnowledgeUnauthorized)
	}
	if d.AuthorizationReason != "" {
		if d.Reason != vocabulary.FailureKnowledgeUnauthorized {
			return fmt.Errorf("authorization_reason is only valid for %s failures",
				vocabulary.FailureKnowledgeUnauthorized)
		}
		if d.Class != FailureClassDeterministic {
			return fmt.Errorf("authorization_reason requires failure class %q", FailureClassDeterministic)
		}
		if _, err := vocabulary.ParseAuthorizationReason(string(d.AuthorizationReason)); err != nil {
			return err
		}
	}
	if d.Message == "" {
		return fmt.Errorf(
			"failure detail for turn %s carries no message; a detail with nothing in it is worse than no "+
				"detail, because the turn advertises an explanation that does not exist", d.TurnID)
	}
	if len(d.Message) > MaxFailureMessageBytes {
		return fmt.Errorf("failure detail message is %d bytes, which exceeds the %d-byte budget",
			len(d.Message), MaxFailureMessageBytes)
	}
	if d.Target != "" {
		if err := types.ValidateEntityID(d.Target); err != nil {
			return fmt.Errorf("failure detail target %q is not a canonical six-part entity ID: %w", d.Target, err)
		}
	}
	for i, committed := range d.Committed {
		if err := types.ValidateEntityID(committed); err != nil {
			return fmt.Errorf("failure detail committed[%d] %q is not a canonical six-part entity ID: %w",
				i, committed, err)
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (d *FailureDetail) MarshalJSON() ([]byte, error) {
	type Alias FailureDetail
	return json.Marshal((*Alias)(d))
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *FailureDetail) UnmarshalJSON(data []byte) error {
	type Alias FailureDetail
	return json.Unmarshal(data, (*Alias)(d))
}
