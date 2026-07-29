package ledger_test

import (
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Position order is org.platform.domain.system.type.instance: the world
// namespace sits in the domain slot and the template in the system slot.
const (
	testCampaignID = "c360.semmachina.world1.starter.campaign.main"
	testSceneID    = "c360.semmachina.world1.starter.scene.gatehouse"
	testPlayerID   = "c360.semmachina.world1.starter.player.one"
	testTurnEntity = "c360.semmachina.world1.starter.turn.turn-act-1"
	testTurnID     = "turn-act-1"
	testActionID   = "act-1"
)

var (
	// resolvedAt is the moment the turn's terminal phase was written. The
	// manifest's real-time stamp is read from that triple, so this value is what
	// a composed manifest must carry.
	resolvedAt = time.Date(2026, 7, 29, 14, 30, 15, 250000000, time.UTC)
	// writtenAt is any other triple's stamp, deliberately different, so a
	// manifest that read the wrong triple's timestamp is visible.
	writtenAt = time.Date(2026, 7, 29, 14, 30, 10, 0, time.UTC)
)

// turnBuilder assembles a turn entity's triples the way the loop's stages do:
// one single-valued predicate per fact, each stamped.
type turnBuilder struct {
	id       string
	envelope message.Type
	triples  []message.Triple
}

func newTurn(id string) *turnBuilder {
	return &turnBuilder{
		id: id,
		// A real turn entity carries its own provenance envelope; a zero one
		// would put the fixture in the not-really-born class the loop refuses.
		envelope: message.Type{
			Domain: payload.Domain, Category: payload.CategoryTurnState, Version: payload.SchemaVersion,
		},
	}
}

func (b *turnBuilder) with(predicate vocabulary.Predicate, object any, at time.Time) *turnBuilder {
	b.triples = append(b.triples, message.Triple{
		Subject: b.id, Predicate: predicate.String(), Object: object,
		Source: "test", Timestamp: at, Confidence: 1.0,
	})
	return b
}

func (b *turnBuilder) build() *graph.EntityState {
	return &graph.EntityState{
		ID: b.id, MessageType: b.envelope, Version: 1, UpdatedAt: resolvedAt, Triples: b.triples,
	}
}

// completedTurn is the shape a turn entity has after walking the whole loop.
func completedTurn() *turnBuilder {
	return newTurn(testTurnEntity).
		with(vocabulary.TurnActionPlayer, testPlayerID, writtenAt).
		with(vocabulary.TurnActionScene, testSceneID, writtenAt).
		with(vocabulary.TurnActionRef, "obj://CONTENT/turn/turn-act-1/action", writtenAt).
		with(vocabulary.TurnVerdictPlausibility, string(vocabulary.PlausibilityPlausible), writtenAt).
		with(vocabulary.TurnVerdictRisk, string(vocabulary.RiskHigh), writtenAt).
		with(vocabulary.TurnVerdictConsequence, string(vocabulary.ConsequenceHarm), writtenAt).
		with(vocabulary.TurnVerdictRequiresRoll, true, writtenAt).
		with(vocabulary.TurnVerdictRef, "obj://CONTENT/turn/turn-act-1/verdict", writtenAt).
		with(vocabulary.TurnRollBand, string(vocabulary.BandPartial), writtenAt).
		with(vocabulary.TurnRollTotal, 8, writtenAt).
		with(vocabulary.TurnRollRef, "obj://CONTENT/turn/turn-act-1/roll", writtenAt).
		with(vocabulary.TurnEffectsBatch, payload.BatchIDForTurn(testTurnID), writtenAt).
		with(vocabulary.TurnEffectsRef, "obj://CONTENT/turn/turn-act-1/effects", writtenAt).
		with(vocabulary.TurnNarrationRef, "obj://CONTENT/turn/turn-act-1/narration", writtenAt).
		with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseComplete), resolvedAt)
}

func compose(t *testing.T, builder *turnBuilder) *payload.TurnManifest {
	t.Helper()
	manifest, err := ledger.Compose(testTurnID, builder.build(), testCampaignID)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return manifest
}

// Every reference on the manifest must be the one the TURN carries. This is the
// composition contract: the archive is an index into the turn's own record, so a
// manifest that invented, defaulted, or dropped a reference would point a future
// reader at the wrong artifact or none.
func TestCompose_CarriesEveryReferenceTheTurnRecorded(t *testing.T) {
	manifest := compose(t, completedTurn())

	want := &payload.TurnManifest{
		TurnID:         testTurnID,
		ActionID:       testActionID,
		CampaignID:     testCampaignID,
		SceneID:        testSceneID,
		PlayerID:       testPlayerID,
		Phase:          vocabulary.PhaseComplete,
		ActionRef:      "obj://CONTENT/turn/turn-act-1/action",
		VerdictRef:     "obj://CONTENT/turn/turn-act-1/verdict",
		RollRef:        "obj://CONTENT/turn/turn-act-1/roll",
		EffectBatchRef: "obj://CONTENT/turn/turn-act-1/effects",
		NarrationRef:   "obj://CONTENT/turn/turn-act-1/narration",
		RecordedAt:     resolvedAt,
		WorldTime:      0,
		RollGate: &payload.RollGateExpectation{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskHigh,
			Reported:     true,
			Advised:      true,
			Mapping:      vocabulary.RollGateMappingVersion(),
		},
	}
	assertSameManifest(t, manifest, want)
}

// The real-time stamp is the moment the turn RESOLVED, read off its terminal
// phase triple — not the moment the archiver ran, and not some other triple's
// stamp. Two properties depend on it: the archive says when a turn happened, and
// recomposition is exact, which is what makes the duplicate guard a comparison
// rather than a guess.
func TestCompose_TakesItsTimestampFromTheTerminalPhaseTriple(t *testing.T) {
	manifest := compose(t, completedTurn())
	if !manifest.RecordedAt.Equal(resolvedAt) {
		t.Fatalf("recorded_at = %s, want the phase triple's %s (other triples carry %s)",
			manifest.RecordedAt, resolvedAt, writtenAt)
	}
	if manifest.RecordedAt.Equal(writtenAt) {
		t.Fatal("recorded_at came from a non-phase triple")
	}
}

// Composition is pure, which is what lets the writer tell "already archived"
// from "the archive disagrees with the turn" exactly rather than modulo a clock.
func TestCompose_IsPureSoRecompositionIsExact(t *testing.T) {
	first := compose(t, completedTurn())
	second := compose(t, completedTurn())
	assertSameManifest(t, second, first)
}

// A failed turn is part of the campaign's history and is archived with the
// closed reason it failed for.
func TestCompose_ArchivesAFailedTurnWithItsReason(t *testing.T) {
	builder := newTurn(testTurnEntity).
		with(vocabulary.TurnActionPlayer, testPlayerID, writtenAt).
		with(vocabulary.TurnActionScene, testSceneID, writtenAt).
		with(vocabulary.TurnActionRef, "obj://CONTENT/turn/turn-act-1/action", writtenAt).
		with(vocabulary.TurnVerdictPlausibility, string(vocabulary.PlausibilityCertain), writtenAt).
		with(vocabulary.TurnVerdictRisk, string(vocabulary.RiskNone), writtenAt).
		with(vocabulary.TurnVerdictRequiresRoll, false, writtenAt).
		with(vocabulary.TurnVerdictRef, "obj://CONTENT/turn/turn-act-1/verdict", writtenAt).
		with(vocabulary.TurnFailureReason, string(vocabulary.FailureEffectEntityKind), resolvedAt).
		with(vocabulary.TurnFailureRef, "obj://CONTENT/turn/turn-act-1/failure", resolvedAt).
		with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseFailed), resolvedAt)

	manifest := compose(t, builder)
	if manifest.Phase != vocabulary.PhaseFailed {
		t.Fatalf("phase = %q, want %q", manifest.Phase, vocabulary.PhaseFailed)
	}
	if manifest.FailureReason != string(vocabulary.FailureEffectEntityKind) {
		t.Fatalf("failure_reason = %q, want %q", manifest.FailureReason, vocabulary.FailureEffectEntityKind)
	}
	if manifest.NarrationRef != "" {
		t.Fatalf("a failed turn archived narration %q; a failed turn is never narrated", manifest.NarrationRef)
	}
	// The gate is still recorded: this turn WAS adjudicated, it just failed
	// afterwards, and the adjudicator's judgment about the dice is part of what
	// happened.
	if manifest.RollGate == nil {
		t.Fatal("an adjudicated turn that failed later archived no roll gate")
	}
	if manifest.RollGate.Reported {
		t.Fatal("the archived gate reports a roll the verdict declined")
	}
}

// A turn that failed before adjudication carries no verdict scalars, and the
// manifest must say so by carrying no gate — not by defaulting one.
func TestCompose_RecordsNoRollGateForATurnThatWasNeverAdjudicated(t *testing.T) {
	builder := newTurn(testTurnEntity).
		with(vocabulary.TurnActionPlayer, testPlayerID, writtenAt).
		with(vocabulary.TurnActionScene, testSceneID, writtenAt).
		with(vocabulary.TurnActionRef, "obj://CONTENT/turn/turn-act-1/action", writtenAt).
		with(vocabulary.TurnFailureReason, string(vocabulary.FailurePersonaCapExhausted), resolvedAt).
		with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseFailed), resolvedAt)

	manifest := compose(t, builder)
	if manifest.RollGate != nil {
		t.Fatalf("an unadjudicated turn archived a roll gate: %+v", manifest.RollGate)
	}
	if manifest.VerdictRef != "" {
		t.Fatalf("an unadjudicated turn archived verdict ref %q", manifest.VerdictRef)
	}
}

// The gate records what the ADJUDICATOR said beside what the mapping advises,
// and a disagreement is archived rather than refused (D12).
func TestCompose_ArchivesAGateThatDisagreesWithTheMapping(t *testing.T) {
	builder := completedTurn()
	// Rebuild with classes the mapping says need no roll, against a verdict
	// that called for the dice anyway.
	builder.triples = nil
	builder.
		with(vocabulary.TurnActionPlayer, testPlayerID, writtenAt).
		with(vocabulary.TurnActionScene, testSceneID, writtenAt).
		with(vocabulary.TurnActionRef, "obj://CONTENT/turn/turn-act-1/action", writtenAt).
		with(vocabulary.TurnVerdictPlausibility, string(vocabulary.PlausibilityCertain), writtenAt).
		with(vocabulary.TurnVerdictRisk, string(vocabulary.RiskNone), writtenAt).
		with(vocabulary.TurnVerdictRequiresRoll, true, writtenAt).
		with(vocabulary.TurnVerdictRef, "obj://CONTENT/turn/turn-act-1/verdict", writtenAt).
		with(vocabulary.TurnNarrationRef, "obj://CONTENT/turn/turn-act-1/narration", writtenAt).
		with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseComplete), resolvedAt)

	manifest := compose(t, builder)
	gate := manifest.RollGate
	if gate == nil {
		t.Fatal("no roll gate was archived")
	}
	if gate.Agrees() {
		t.Fatalf("the gate reports agreement: %+v", gate)
	}
	if !gate.Reported || gate.Advised {
		t.Fatalf("gate = %+v, want reported=true advised=false", gate)
	}
	if gate.Mapping != vocabulary.RollGateMappingVersion() {
		t.Fatalf("gate mapping = %q, want %q", gate.Mapping, vocabulary.RollGateMappingVersion())
	}
}

func TestCompose_RefusesAnIncoherentTurnRecord(t *testing.T) {
	cases := []struct {
		name    string
		build   func() *graph.EntityState
		wantErr string
	}{
		{
			name: "still in flight",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = b.triples[:len(b.triples)-1]
				return b.with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseNarrating), resolvedAt).build()
			},
			wantErr: "not terminal",
		},
		{
			name:    "no phase at all",
			build:   func() *graph.EntityState { return newTurn(testTurnEntity).build() },
			wantErr: "carries no turn.phase.current",
		},
		{
			// Two values for a single-valued predicate is the signature of a
			// write that took an appending lane, and archiving one of them
			// would make the campaign's permanent record a coin flip.
			name: "two phases",
			build: func() *graph.EntityState {
				return completedTurn().
					with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseFailed), resolvedAt).build()
			},
			wantErr: "appending lane",
		},
		{
			name: "a referential stub",
			build: func() *graph.EntityState {
				state := completedTurn().build()
				state.MessageType = graph.StubMessageType
				return state
			},
			wantErr: "referential stub",
		},
		{
			name: "no action reference",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnActionRef)
				return b.build()
			},
			wantErr: "turn.action.ref",
		},
		{
			name: "no player",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnActionPlayer)
				return b.build()
			},
			wantErr: "turn.action.player",
		},
		{
			name: "half a verdict",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnVerdictRisk)
				return b.build()
			},
			wantErr: "2 of the 3 verdict scalars",
		},
		{
			name: "completed with no narration",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnNarrationRef)
				return b.build()
			},
			wantErr: "narration_ref",
		},
		{
			name: "phase written with no timestamp",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnPhaseCurrent)
				return b.with(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseComplete), time.Time{}).build()
			},
			wantErr: "no timestamp",
		},
		{
			name: "the graph answered with another turn",
			build: func() *graph.EntityState {
				state := completedTurn().build()
				state.ID = "c360.semmachina.world1.starter.turn.turn-act-9"
				return state
			},
			wantErr: "turn-act-9",
		},
		{
			name: "a phase outside the vocabulary",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnPhaseCurrent)
				return b.with(vocabulary.TurnPhaseCurrent, "done", resolvedAt).build()
			},
			wantErr: "turn_phase",
		},
		{
			name: "a non-string phase",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnPhaseCurrent)
				return b.with(vocabulary.TurnPhaseCurrent, 7, resolvedAt).build()
			},
			wantErr: "want a string",
		},
		{
			name: "a non-bool roll gate",
			build: func() *graph.EntityState {
				b := completedTurn()
				b.triples = slicesWithout(b.triples, vocabulary.TurnVerdictRequiresRoll)
				return b.with(vocabulary.TurnVerdictRequiresRoll, "true", resolvedAt).build()
			},
			wantErr: "want a bool",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := ledger.Compose(testTurnID, tc.build(), testCampaignID)
			if err == nil {
				t.Fatalf("Compose accepted an incoherent turn record: %+v", manifest)
			}
			if manifest != nil {
				t.Fatalf("a refused composition still returned a manifest: %+v", manifest)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCompose_RefusesANilEntity(t *testing.T) {
	if _, err := ledger.Compose(testTurnID, nil, testCampaignID); err == nil {
		t.Fatal("Compose accepted a nil entity")
	}
}

// slicesWithout drops every triple for a predicate, so a case can build "the
// turn that is missing exactly this fact".
func slicesWithout(triples []message.Triple, predicate vocabulary.Predicate) []message.Triple {
	out := make([]message.Triple, 0, len(triples))
	for _, triple := range triples {
		if triple.Predicate == predicate.String() {
			continue
		}
		out = append(out, triple)
	}
	return out
}

func assertSameManifest(t *testing.T, got, want *payload.TurnManifest) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("manifest comparison against nil: got %v want %v", got, want)
	}
	if !got.RecordedAt.Equal(want.RecordedAt) {
		t.Fatalf("recorded_at = %s, want %s", got.RecordedAt, want.RecordedAt)
	}
	gotCopy, wantCopy := *got, *want
	gotCopy.RecordedAt, wantCopy.RecordedAt = time.Time{}, time.Time{}

	gotGate, wantGate := gotCopy.RollGate, wantCopy.RollGate
	gotCopy.RollGate, wantCopy.RollGate = nil, nil
	if gotCopy != wantCopy {
		t.Fatalf("manifest mismatch:\n got %+v\nwant %+v", gotCopy, wantCopy)
	}
	switch {
	case gotGate == nil && wantGate == nil:
	case gotGate == nil || wantGate == nil:
		t.Fatalf("roll gate mismatch: got %+v want %+v", gotGate, wantGate)
	case *gotGate != *wantGate:
		t.Fatalf("roll gate mismatch:\n got %+v\nwant %+v", *gotGate, *wantGate)
	}
}
