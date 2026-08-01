package payload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// CompanionStageStatus is the closed terminal outcome of one companion stage.
type CompanionStageStatus string

const (
	// CompanionStageDecision means the stage committed a structural companion decision.
	CompanionStageDecision CompanionStageStatus = "decision"
	// CompanionStageNoActiveBond means the player has no active companion bond.
	CompanionStageNoActiveBond CompanionStageStatus = "no-active-bond"
	// CompanionStageNoTrigger means a bond exists but no authorized trigger fired.
	CompanionStageNoTrigger CompanionStageStatus = "no-trigger"
	// CompanionStageExhausted means the one-iteration warning path ended silently.
	CompanionStageExhausted CompanionStageStatus = "exhausted"
)

var companionStageStatuses = []CompanionStageStatus{
	CompanionStageDecision, CompanionStageNoActiveBond, CompanionStageNoTrigger, CompanionStageExhausted,
}

// CompanionStageRecord is an entity-only, prose-free completion artifact.
type CompanionStageRecord struct {
	TurnID        string                            `json:"turn_id"`
	PlayerID      string                            `json:"player_id"`
	CompanionID   string                            `json:"companion_id,omitempty"`
	BondID        string                            `json:"bond_id,omitempty"`
	Status        CompanionStageStatus              `json:"status"`
	TriggerKind   vocabulary.CompanionTriggerKind   `json:"trigger_kind,omitempty"`
	TriggerSource vocabulary.CompanionTriggerSource `json:"trigger_source,omitempty"`
	DecisionRef   string                            `json:"decision_ref,omitempty"`
}

// Schema names the entity-only artifact without registering a wire decoder.
func (r *CompanionStageRecord) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryCompanionStageRecord, Version: SchemaVersion}
}

// Validate enforces status-dependent structural identity and references.
func (r *CompanionStageRecord) Validate() error {
	if r == nil {
		return errors.New("companion stage record is required")
	}
	if err := requireIDSegment("turn_id", r.TurnID); err != nil {
		return err
	}
	if err := requireTypedEntityID("player_id", r.PlayerID, vocabulary.EntityKindPlayer); err != nil {
		return err
	}
	if !slices.Contains(companionStageStatuses, r.Status) {
		return fmt.Errorf("status %q is not a registered companion stage status", r.Status)
	}
	if r.Status == CompanionStageNoActiveBond {
		if r.CompanionID != "" || r.BondID != "" || r.TriggerKind != "" || r.TriggerSource != "" || r.DecisionRef != "" {
			return errors.New("no-active-bond record forbids bond, companion, trigger, and decision")
		}
		return nil
	}
	if err := requireTypedEntityID("companion_id", r.CompanionID, vocabulary.EntityKindCharacter); err != nil {
		return err
	}
	if err := requireTypedEntityID("bond_id", r.BondID, vocabulary.EntityKindCompanionBond); err != nil {
		return err
	}
	if _, err := vocabulary.ParseCompanionTrigger(string(r.TriggerKind)); err != nil {
		return fmt.Errorf("trigger_kind: %w", err)
	}
	if _, err := vocabulary.ParseCompanionTriggerSource(string(r.TriggerSource)); err != nil {
		return fmt.Errorf("trigger_source: %w", err)
	}
	if r.Status == CompanionStageNoTrigger {
		if r.TriggerKind != vocabulary.CompanionTriggerNone || r.TriggerSource != vocabulary.CompanionTriggerSourceNone || r.DecisionRef != "" {
			return errors.New("no-trigger record requires none/none and forbids a decision")
		}
		return nil
	}
	if r.TriggerKind == vocabulary.CompanionTriggerNone || r.TriggerSource == vocabulary.CompanionTriggerSourceNone {
		return errors.New("decision and exhausted records require a non-none trigger and source")
	}
	if (r.TriggerKind == vocabulary.CompanionTriggerPlayerHint) !=
		(r.TriggerSource == vocabulary.CompanionTriggerSourceCaseDecision) {
		return errors.New("player-hint and case-decision trigger values must be paired")
	}
	if (r.TriggerKind == vocabulary.CompanionTriggerWarning) !=
		(r.TriggerSource == vocabulary.CompanionTriggerSourceResolvedRisk) {
		return errors.New("warning and resolved-risk trigger values must be paired")
	}
	if r.Status == CompanionStageExhausted && r.TriggerKind != vocabulary.CompanionTriggerWarning {
		return errors.New("exhausted companion stage requires the automatic warning trigger")
	}
	return RequireStorageRef("decision_ref", r.DecisionRef)
}

func requireTypedEntityID(field, value string, kind vocabulary.EntityKind) error {
	if err := requireEntityID(field, value); err != nil {
		return err
	}
	id, err := types.ParseEntityID(value)
	if err != nil {
		return err
	}
	if id.Type != string(kind) {
		return fmt.Errorf("%s has type %s, want %s", field, id.Type, kind)
	}
	return nil
}

// MarshalJSON emits only the entity artifact body.
func (r *CompanionStageRecord) MarshalJSON() ([]byte, error) {
	type alias CompanionStageRecord
	return json.Marshal((*alias)(r))
}

// UnmarshalJSON refuses undeclared fields, including prose.
func (r *CompanionStageRecord) UnmarshalJSON(data []byte) error {
	type alias CompanionStageRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*alias)(r)); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("companion stage record carries more than one JSON value")
		}
		return err
	}
	return nil
}

// Triples exposes the exact stage reference plus only the closed trigger facts.
func (r *CompanionStageRecord) Triples(turnEntityID, stageRef, source string, at time.Time) ([]message.Triple, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if err := RequireTurnEntityID(r.TurnID, turnEntityID); err != nil {
		return nil, err
	}
	if err := RequireStorageRef("companion stage ref", stageRef); err != nil {
		return nil, err
	}
	triples := []message.Triple{{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionStageRef.String(), Object: stageRef,
		Source: source, Timestamp: at.UTC(), Confidence: 1, Context: turnEntityID}}
	if r.Status != CompanionStageNoActiveBond {
		triples = append(triples,
			message.Triple{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionTriggerKind.String(), Object: string(r.TriggerKind), Source: source, Timestamp: at.UTC(), Confidence: 1, Context: turnEntityID},
			message.Triple{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionTriggerSource.String(), Object: string(r.TriggerSource), Source: source, Timestamp: at.UTC(), Confidence: 1, Context: turnEntityID})
	}
	return triples, nil
}
