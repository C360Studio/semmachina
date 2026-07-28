package payload_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestEffectBatch_ValidFixtureIsAccepted(t *testing.T) {
	if err := validEffectBatch().Validate(); err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}
}

// Batch identity is derived from the turn, which is the whole idempotency
// guard: recompute it after a crash or a duplicate trigger and you get the
// same ID, find it recorded on the turn entity, and do not apply twice.
func TestEffectBatch_IdentityIsDerivedFromTheTurnAndNothingElse(t *testing.T) {
	first := payload.BatchIDForTurn(testTurnID)
	second := payload.BatchIDForTurn(testTurnID)
	if first != second {
		t.Fatalf("derivation is not deterministic: %q then %q", first, second)
	}
	if first == "" {
		t.Fatal("a present turn must derive a present batch id")
	}
	if payload.BatchIDForTurn("turn-act-1") == payload.BatchIDForTurn("turn-act-2") {
		t.Fatal("two turns collided onto one batch id")
	}
	if payload.BatchIDForTurn("") != "" {
		t.Fatal("an absent turn must not derive a batch id")
	}

	built := payload.NewEffectBatch(testTurnID, vocabulary.BandMiss, nil)
	if built.BatchID != first {
		t.Fatalf("constructor produced batch id %q, want the derived %q", built.BatchID, first)
	}
}

func TestEffectBatch_RejectsAnAdHocBatchIdentity(t *testing.T) {
	batch := validEffectBatch()
	batch.BatchID = "batch-whatever"

	decoded := decode(t, publishUnvalidated(t, batch))
	err := decoded.Validate()
	if err == nil {
		t.Fatal("a batch id not derived from the turn breaks idempotency and must be rejected")
	}
	if !strings.Contains(err.Error(), "batch_id") {
		t.Fatalf("rejection reason %q does not mention batch_id", err.Error())
	}
}

func TestEffectBatch_RejectsOutOfVocabularyContent(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*payload.EffectBatch)
		wantErr string
	}{
		{
			name:    "band outside the vocabulary",
			mutate:  func(b *payload.EffectBatch) { b.Band = "critical" },
			wantErr: "outcome_band",
		},
		{
			name: "an intent outside the effect vocabulary",
			mutate: func(b *payload.EffectBatch) {
				b.Intents = append(b.Intents, payload.EffectIntent{Type: "delete_entity", Target: testCharacter})
			},
			wantErr: "effect_type",
		},
		{
			name: "an intent outside registered bounds",
			mutate: func(b *payload.EffectBatch) {
				b.Intents[0].Value = intPtr(900)
			},
			wantErr: "bounds",
		},
		{
			name:    "turn id missing",
			mutate:  func(b *payload.EffectBatch) { b.TurnID = "" },
			wantErr: "turn_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := validEffectBatch()
			tc.mutate(batch)

			decoded := decode(t, publishUnvalidated(t, batch))
			err := decoded.Validate()
			if err == nil {
				t.Fatal("Validate accepted a batch outside the closed effect vocabulary")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// A band that declares no world change is a normal outcome, not an error.
func TestEffectBatch_EmptyIntentSetIsLegal(t *testing.T) {
	batch := payload.NewEffectBatch(testTurnID, vocabulary.BandMiss, nil)
	if err := batch.Validate(); err != nil {
		t.Fatalf("an empty batch was rejected: %v", err)
	}
}

// The auto band exists only for no-roll verdicts, and a batch is how it
// reaches the applier, so it must be accepted here.
func TestEffectBatch_AcceptsEveryBandIncludingAuto(t *testing.T) {
	for _, band := range vocabulary.OutcomeBands() {
		batch := payload.NewEffectBatch(testTurnID, band, []payload.EffectIntent{
			{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusHidden},
		})
		if err := batch.Validate(); err != nil {
			t.Fatalf("a batch for band %q was rejected: %v", band, err)
		}
	}
}
