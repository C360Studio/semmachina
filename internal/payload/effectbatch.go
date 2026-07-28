package payload

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// BatchIDForTurn derives the effect-batch identity from the turn identity.
// The derivation is total and pure, which is what makes re-application
// idempotent: a duplicate trigger or a crash-restart recomputes the same
// batch ID, finds it already recorded on the turn entity, and no-ops instead
// of applying the world change twice.
func BatchIDForTurn(turnID string) string {
	if turnID == "" {
		return ""
	}
	return "batch-" + turnID
}

// EffectBatch is the intent set selected for the resolved band, as the
// applier commits it. It is deliberately a separate payload from the verdict:
// the verdict proposes intents for every band, and exactly one band survives
// the roll.
type EffectBatch struct {
	// BatchID is derived from TurnID and is the idempotency key.
	BatchID string `json:"batch_id"`
	// TurnID is the turn these effects belong to.
	TurnID string `json:"turn_id"`
	// Band is the band the roll selected, or auto for a no-roll verdict.
	Band vocabulary.OutcomeBand `json:"band"`
	// Intents is the selected band's intent list. Empty is legal and normal:
	// a band may declare no world change.
	Intents []EffectIntent `json:"intents"`
}

// NewEffectBatch builds a batch with the derived identity, so callers cannot
// mint an ad-hoc batch ID that would break the idempotency guard.
func NewEffectBatch(turnID string, band vocabulary.OutcomeBand, intents []EffectIntent) *EffectBatch {
	return &EffectBatch{
		BatchID: BatchIDForTurn(turnID),
		TurnID:  turnID,
		Band:    band,
		Intents: intents,
	}
}

// Schema implements message.Payload.
func (b *EffectBatch) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryEffectBatch, Version: SchemaVersion}
}

// Validate implements message.Payload. Validation completes for every intent
// before the applier issues any write, so a batch rejected here applies
// nothing at all.
func (b *EffectBatch) Validate() error {
	if err := requireIDSegment("turn_id", b.TurnID); err != nil {
		return err
	}
	if want := BatchIDForTurn(b.TurnID); b.BatchID != want {
		return fmt.Errorf("batch_id %q is not derived from turn_id %q (expected %q)", b.BatchID, b.TurnID, want)
	}
	if _, err := vocabulary.ParseOutcomeBand(string(b.Band)); err != nil {
		return err
	}
	for idx := range b.Intents {
		if err := b.Intents[idx].Validate(); err != nil {
			return fmt.Errorf("intent %d: %w", idx, err)
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (b *EffectBatch) MarshalJSON() ([]byte, error) {
	type Alias EffectBatch
	return json.Marshal((*Alias)(b))
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *EffectBatch) UnmarshalJSON(data []byte) error {
	type Alias EffectBatch
	return json.Unmarshal(data, (*Alias)(b))
}

// Triples returns the batch's rule-matched projection as triples on the turn
// entity, plus one reference to the stored batch payload.
//
// The batch identity is the only scalar. It is what the applier's own
// idempotency guard reads and what a rule chains on ("effects landed →
// narrate"); the intent list is bulky, is consumed only by the applier and the
// ledger, and carries the adjudicator's PROPOSALS rather than the world's facts.
// The committed changes are on the target entities themselves — a reader asking
// what the world looks like should be reading the world, not the turn's
// paperwork.
//
// Same shared projection as the verdict and the roll, so the rule that keeps
// bulky and LLM-authored content off the graph's rule-matching surface is
// enforced once rather than remembered three times.
//
// These triples MUST be committed on the entity merge lane
// (entity.update_with_triples). The triple-add lanes append: a duplicate apply
// trigger through them would leave a turn holding two batch identities, and the
// guard that reads one would be reading a coin flip.
func (b *EffectBatch) Triples(turnEntityID, batchRef, source string, at time.Time) ([]message.Triple, error) {
	return tripleProjection{
		payload: b,
		subject: turnEntityID,
		turnID:  b.TurnID,
		source:  source,
		at:      at,

		registered: vocabulary.EffectScalarPredicates(),
		objects: map[vocabulary.Predicate]any{
			vocabulary.TurnEffectsBatch: b.BatchID,
		},

		refPredicate: vocabulary.TurnEffectsRef,
		refName:      "effect batch ref",
		ref:          batchRef,
	}.build()
}
