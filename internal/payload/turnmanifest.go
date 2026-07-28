package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnManifest is the append-only ledger record for one resolved turn.
// ENTITY_STATES is current truth; the ledger is the archive the writer loop
// replays, so a manifest carries REFERENCES ONLY — never prose, never a
// verdict body, never an intent list. Every field below is an identifier, a
// storage reference, a closed-vocabulary value, or a timestamp.
//
// Replay honesty: the deterministic stages (dice, effect validation) can be
// re-executed from these references and must reproduce exactly; persona
// output is reproduced only by reading the preserved reference. Re-running a
// narrator is a new rendition, not a replay.
type TurnManifest struct {
	// TurnID keys the manifest. Exactly one manifest exists per turn.
	TurnID string `json:"turn_id"`
	// ActionID is the originating action.
	ActionID string `json:"action_id"`
	// CampaignID is the campaign entity.
	CampaignID string `json:"campaign_id"`
	// SceneID is the scene entity.
	SceneID string `json:"scene_id"`
	// PlayerID is the player entity.
	PlayerID string `json:"player_id"`

	// Phase is the terminal phase: complete or failed. Failed turns are
	// ledgered too — a turn that failed is part of the campaign's history.
	Phase vocabulary.TurnPhase `json:"phase"`

	// ActionRef references the stored action payload.
	ActionRef string `json:"action_ref"`
	// VerdictRef references the stored verdict, if adjudication completed.
	VerdictRef string `json:"verdict_ref,omitempty"`
	// RollRef references the stored roll result, if the turn rolled.
	RollRef string `json:"roll_ref,omitempty"`
	// EffectBatchRef references the applied batch, if effects committed.
	EffectBatchRef string `json:"effect_batch_ref,omitempty"`
	// NarrationRef references narration prose in ObjectStore, if narrated.
	NarrationRef string `json:"narration_ref,omitempty"`
	// FailureReason is required on a failed turn and forbidden otherwise.
	FailureReason string `json:"failure_reason,omitempty"`

	// RecordedAt is the real-time stamp.
	RecordedAt time.Time `json:"recorded_at"`
	// WorldTime is the in-fiction time stamp. Always present, always zero
	// until the world clock exists, so ledger readers written now do not
	// need a schema change when it arrives.
	WorldTime int64 `json:"world_time"`
}

// Schema implements message.Payload.
func (m *TurnManifest) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryTurnManifest, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (m *TurnManifest) Validate() error {
	if err := requireIDSegment("turn_id", m.TurnID); err != nil {
		return err
	}
	if err := requireActionID("action_id", m.ActionID); err != nil {
		return err
	}
	if err := requireEntityID("campaign_id", m.CampaignID); err != nil {
		return err
	}
	if err := requireEntityID("scene_id", m.SceneID); err != nil {
		return err
	}
	if err := requireEntityID("player_id", m.PlayerID); err != nil {
		return err
	}
	if err := requireNonEmpty("action_ref", m.ActionRef); err != nil {
		return err
	}

	if _, err := vocabulary.ParseTurnPhase(string(m.Phase)); err != nil {
		return err
	}
	if !m.Phase.IsTerminal() {
		return fmt.Errorf("phase %q is not terminal; only resolved turns are ledgered", m.Phase)
	}

	switch m.Phase {
	case vocabulary.PhaseFailed:
		if m.FailureReason == "" {
			return errors.New("a failed turn requires failure_reason")
		}
	case vocabulary.PhaseComplete:
		if m.FailureReason != "" {
			return errors.New("a complete turn must not carry failure_reason")
		}
		if m.VerdictRef == "" {
			return errors.New("a complete turn requires verdict_ref")
		}
		if m.NarrationRef == "" {
			return errors.New("a complete turn requires narration_ref")
		}
	}

	if m.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (m *TurnManifest) MarshalJSON() ([]byte, error) {
	type Alias TurnManifest
	return json.Marshal((*Alias)(m))
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *TurnManifest) UnmarshalJSON(data []byte) error {
	type Alias TurnManifest
	return json.Unmarshal(data, (*Alias)(m))
}
