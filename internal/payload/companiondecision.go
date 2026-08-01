package payload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// MaxCompanionDecisionEvidence bounds the evidence a single structural exit may cite.
	MaxCompanionDecisionEvidence = 8

	companionDecisionIdentityDomain = "companion-decision/v1"
)

// CompanionDecisionKind is the closed structural action selected by a companion persona.
type CompanionDecisionKind string

const (
	// CompanionDecisionSilent chooses no voiced companion intervention.
	CompanionDecisionSilent CompanionDecisionKind = "silent"
	// CompanionDecisionQuip requests a brief voiced observation without evidence.
	CompanionDecisionQuip CompanionDecisionKind = "quip"
	// CompanionDecisionQuestion requests a voiced question without evidence.
	CompanionDecisionQuestion CompanionDecisionKind = "question"
	// CompanionDecisionWarning warns from cited companion-known evidence.
	CompanionDecisionWarning CompanionDecisionKind = "warning"
	// CompanionDecisionRecall recalls cited companion-known evidence.
	CompanionDecisionRecall CompanionDecisionKind = "recall"
	// CompanionDecisionHint offers a bounded hint from cited companion-known evidence.
	CompanionDecisionHint CompanionDecisionKind = "hint"
)

var companionDecisionKinds = []CompanionDecisionKind{
	CompanionDecisionSilent, CompanionDecisionQuip, CompanionDecisionQuestion,
	CompanionDecisionWarning, CompanionDecisionRecall, CompanionDecisionHint,
}

// CompanionDecisionKinds returns the closed decision set in schema order.
func CompanionDecisionKinds() []CompanionDecisionKind { return slices.Clone(companionDecisionKinds) }

// CompanionDecision is a prose-free companion exit. Identity is runtime-injected;
// only Kind, HintLevel, EvidenceRefs, and TargetRef are model-controlled.
type CompanionDecision struct {
	DecisionID   string                `json:"decision_id"`
	TurnID       string                `json:"turn_id"`
	ContextRef   string                `json:"context_ref"`
	PlayerID     string                `json:"player_id"`
	CompanionID  string                `json:"companion_id"`
	Kind         CompanionDecisionKind `json:"kind"`
	HintLevel    vocabulary.HintLevel  `json:"hint_level,omitempty"`
	EvidenceRefs []string              `json:"evidence_refs"`
	TargetRef    string                `json:"target_ref,omitempty"`
}

// CompanionDecisionID derives the one identity for a turn, context, player, and companion.
func CompanionDecisionID(turnID, contextRef, playerID, companionID string) string {
	return deterministicID(companionDecisionIdentityDomain, turnID, contextRef, playerID, companionID)
}

// Schema implements message.Payload.
func (d *CompanionDecision) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryCompanionDecision, Version: SchemaVersion}
}

// Validate enforces the complete structural contract without mutating the decision.
func (d *CompanionDecision) Validate() error {
	if d == nil {
		return errors.New("companion decision is required")
	}
	if err := requireIDSegment("turn_id", d.TurnID); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"context_ref", d.ContextRef}, {"player_id", d.PlayerID}, {"companion_id", d.CompanionID},
	} {
		if err := requireEntityID(field.name, field.value); err != nil {
			return err
		}
	}
	if !slices.Contains(companionDecisionKinds, d.Kind) {
		return fmt.Errorf("kind %q is not a registered companion decision kind", d.Kind)
	}
	if d.Kind == CompanionDecisionHint {
		if _, err := vocabulary.ParseHintLevel(string(d.HintLevel)); err != nil {
			return fmt.Errorf("hint_level is required and must be closed when kind is hint: %w", err)
		}
	} else if d.HintLevel != "" {
		return fmt.Errorf("hint_level is forbidden when kind is %q", d.Kind)
	}
	if len(d.EvidenceRefs) > MaxCompanionDecisionEvidence {
		return fmt.Errorf("evidence_refs carries %d references; limit is %d",
			len(d.EvidenceRefs), MaxCompanionDecisionEvidence)
	}
	seen := make(map[string]struct{}, len(d.EvidenceRefs))
	for index, ref := range d.EvidenceRefs {
		if err := requireEntityID(fmt.Sprintf("evidence_refs[%d]", index), ref); err != nil {
			return err
		}
		if _, duplicate := seen[ref]; duplicate {
			return fmt.Errorf("evidence_refs repeats reference %q", ref)
		}
		seen[ref] = struct{}{}
	}
	if slices.Contains([]CompanionDecisionKind{
		CompanionDecisionWarning, CompanionDecisionRecall, CompanionDecisionHint,
	}, d.Kind) && len(d.EvidenceRefs) == 0 {
		return fmt.Errorf("evidence_refs is required when kind is %q", d.Kind)
	}
	if d.TargetRef != "" {
		if err := requireEntityID("target_ref", d.TargetRef); err != nil {
			return err
		}
	}
	expected := CompanionDecisionID(d.TurnID, d.ContextRef, d.PlayerID, d.CompanionID)
	if d.DecisionID != expected {
		return fmt.Errorf("decision_id %q does not match the deterministic identity %q", d.DecisionID, expected)
	}
	return nil
}

// MarshalJSON emits only the payload body; the publisher owns the envelope.
func (d *CompanionDecision) MarshalJSON() ([]byte, error) {
	type Alias CompanionDecision
	return json.Marshal((*Alias)(d))
}

// UnmarshalJSON uses an alias while refusing undeclared fields, including prose.
func (d *CompanionDecision) UnmarshalJSON(data []byte) error {
	type Alias CompanionDecision
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*Alias)(d)); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("companion decision carries more than one JSON value")
		}
		return fmt.Errorf("companion decision trailing JSON: %w", err)
	}
	return nil
}
