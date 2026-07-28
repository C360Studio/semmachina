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

// The applied batch reaches the turn entity through the SAME projection the
// verdict and the roll use: one rule-matched scalar — the batch identity the
// applier's idempotency guard reads — plus one reference to the payload. The
// intent list never becomes triples; the committed changes live on the target
// entities, which is where a reader asking what the world looks like belongs.
func TestEffectBatch_TriplesProjectTheIdentityAndTheReference(t *testing.T) {
	const ref = "obj://effects/batch-turn-act-1"
	batch := validEffectBatch()

	triples, err := batch.Triples(testTurnEntity, ref, "effect-applier", testTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if len(triples) != len(vocabulary.EffectScalarPredicates())+1 {
		t.Fatalf("projected %d triples, want %d scalars plus one reference",
			len(triples), len(vocabulary.EffectScalarPredicates()))
	}

	got := make(map[string]any, len(triples))
	for _, triple := range triples {
		if triple.Subject != testTurnEntity {
			t.Fatalf("triple %q has subject %q, want the turn entity", triple.Predicate, triple.Subject)
		}
		if triple.Context != batch.TurnID {
			t.Fatalf("triple %q carries context %q, want the turn id", triple.Predicate, triple.Context)
		}
		if !triple.Timestamp.Equal(testTime) {
			t.Fatalf("triple %q is stamped %v, want %v", triple.Predicate, triple.Timestamp, testTime)
		}
		got[triple.Predicate] = triple.Object
	}

	if got[vocabulary.TurnEffectsBatch.String()] != batch.BatchID {
		t.Fatalf("batch identity triple = %v, want %q", got[vocabulary.TurnEffectsBatch.String()], batch.BatchID)
	}
	if got[vocabulary.TurnEffectsRef.String()] != ref {
		t.Fatalf("reference triple = %v, want %q", got[vocabulary.TurnEffectsRef.String()], ref)
	}
	for predicate := range got {
		switch predicate {
		case vocabulary.TurnEffectsBatch.String(), vocabulary.TurnEffectsRef.String():
		default:
			t.Fatalf("the projection wrote %q; the intent list must ride behind the reference", predicate)
		}
	}
}

// An invalid batch must not reach the graph in fragments: the projection
// validates the whole payload before it emits a single triple.
func TestEffectBatch_TriplesRefuseAnInvalidBatchAndAMissingReference(t *testing.T) {
	broken := validEffectBatch()
	broken.Intents[0].Value = intPtr(9999)
	if _, err := broken.Triples(testTurnEntity, "obj://x", "effect-applier", testTime); err == nil {
		t.Fatal("an out-of-bounds intent was projected into triples")
	}

	if _, err := validEffectBatch().Triples(testTurnEntity, "", "effect-applier", testTime); err == nil {
		t.Fatal("a batch with no stored reference was projected; the payload would be unreachable")
	}
	if _, err := validEffectBatch().Triples("not-an-entity", "obj://x", "effect-applier", testTime); err == nil {
		t.Fatal("a non-canonical turn entity id was accepted as a triple subject")
	}
}
