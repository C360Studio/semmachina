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

	// RollGate records what the adjudicator said about the dice, what the
	// advisory mapping said, and which version of that mapping said it.
	//
	// It is a POINTER because absence is a real state and must not be spelled
	// the same way as "impossible, unknown, they agreed": a turn that failed
	// before adjudication has no verdict and therefore no gate to record, and a
	// zero-valued struct would archive that turn as having reported no roll
	// under an empty mapping.
	//
	// It is derivable from the stored verdict TODAY, which is exactly why it
	// looks like a redundant field and is not. See RollGateExpectation.Mapping:
	// the mapping is advisory and expected to be tuned with play, and a value
	// this archive derived on read would silently change its account of every
	// historical turn the day the table moved. The ledger's job is to say what
	// was true then.
	RollGate *RollGateExpectation `json:"roll_gate,omitempty"`

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
		// A CLOSED code, never a sentence. The turn recorder already refuses
		// anything else on the way to the graph, and the same rule holds here
		// for a different reason: the ledger is where a campaign's history is
		// read back, so a free-text reason would put LLM- or applier-authored
		// prose into the archive's only structured account of why a turn ended —
		// and every consumer downstream of the ledger (the chronicler, the
		// writer loop, a metrics pass over failure rates) would inherit it as
		// data it cannot group on. The error is returned unwrapped because it
		// names the kind — "failure_reason" — itself.
		if _, err := vocabulary.ParseFailureReason(m.FailureReason); err != nil {
			return err
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
		// A completed turn was adjudicated by definition — it carries a verdict
		// — so the gate it was adjudicated under is knowable and must be
		// recorded. Making it required exactly where it is derivable is the
		// point: it is the turns that HAVE a verdict whose derived advice would
		// silently change when the mapping is tuned.
		if m.RollGate == nil {
			return errors.New(
				"a complete turn requires roll_gate; it carries a verdict, so the gate it ran under is known, " +
					"and deriving it later would re-decide history when the advisory mapping is tuned")
		}
	}
	if m.RollGate != nil {
		if err := m.RollGate.Validate(); err != nil {
			return fmt.Errorf("roll_gate: %w", err)
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
