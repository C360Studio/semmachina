package dice_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testCharacter = "c360.semmachina.world1.starter.character.rook"
	testSceneID   = "c360.semmachina.world1.starter.scene.gatehouse"
	testActionID  = "act-1"
	testRollRef   = "obj://rolls/turn-act-1"
)

var resolveTime = time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)

// fakeReader is an in-memory turn-entity read surface. The at-most-one guard is
// ALSO proven against real graph-ingest (see dice_integration_test.go); this
// exists for the damaged-entity shapes a healthy broker will not produce.
type fakeReader struct {
	entities map[string]*graph.EntityState
	err      error
}

func newReader() *fakeReader {
	return &fakeReader{entities: make(map[string]*graph.EntityState)}
}

func (r *fakeReader) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	if r.err != nil {
		return nil, r.err
	}
	entity, ok := r.entities[id]
	if !ok {
		return nil, fmt.Errorf("get entity %s: %w", id, graphio.ErrEntityNotFound)
	}
	return entity, nil
}

// turnEntity is a turn in progress carrying whatever triples a case needs.
func turnEntity(triples ...message.Triple) *graph.EntityState {
	return &graph.EntityState{
		ID:          testTurnEntity,
		MessageType: message.Type{Domain: payload.Domain, Category: "turn_entity", Version: payload.SchemaVersion},
		UpdatedAt:   resolveTime,
		Triples:     triples,
	}
}

// rollingVerdict is a verdict whose reported gate calls for the dice AND whose
// classes make the advisory mapping agree — the ordinary case.
func rollingVerdict() *payload.Verdict {
	return &payload.Verdict{
		TurnID:   testTurnID,
		ActionID: testActionID,
		SceneID:  testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskHigh,
			Consequence:  vocabulary.ConsequenceHarm,
			RequiresRoll: true,
		},
		Modifiers: []payload.Modifier{{Source: vocabulary.ModifierEquipment, Value: 1, Note: "crowbar"}},
		Bands:     bandsFor(true),
	}
}

func bandsFor(requiresRoll bool) payload.EffectBands {
	bands := payload.EffectBands{}
	for _, band := range vocabulary.BandsForVerdict(requiresRoll) {
		bands[band] = []payload.EffectIntent{
			{Type: vocabulary.EffectSetStatus, Target: testCharacter, Status: vocabulary.StatusWounded},
		}
	}
	return bands
}

func newResolver(t *testing.T, reader dice.TurnReader, seed campaign.Seed) *dice.Resolver {
	t.Helper()
	resolver, err := dice.NewResolver(newRoller(t), reader, campaign.Instantiation{
		CampaignID: testCampaignID,
		Seed:       seed,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func TestResolve_RollsATurnThatHasNoRecordedRoll(t *testing.T) {
	reader := newReader()
	reader.entities[testTurnEntity] = turnEntity()
	seed := seedOf(0x05)

	resolution, err := newResolver(t, reader, seed).Resolve(t.Context(), rollingVerdict(), testTurnEntity)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !resolution.Rolled {
		t.Fatal("a turn with no recorded roll was reported as already rolled")
	}

	direct := mustRoll(t, newRoller(t), seed, testTurnID, rollingVerdict().Modifiers...)
	if fmt.Sprint(resolution.Roll.Dice) != fmt.Sprint(direct.Dice) || resolution.Roll.Total != direct.Total {
		t.Fatalf("the resolver produced %v/%d, a direct roll gives %v/%d",
			resolution.Roll.Dice, resolution.Roll.Total, direct.Dice, direct.Total)
	}
}

// The spec scenario: a turn's roll trigger delivered more than once leaves
// exactly one roll. The recorded roll is built by the PRODUCTION projection, so
// the guard and the writer cannot drift apart on which predicates a roll lands
// on.
func TestResolve_ATurnThatAlreadyRolledIsNotRolledAgain(t *testing.T) {
	seed := seedOf(0x05)
	verdict := rollingVerdict()

	first := mustRoll(t, newRoller(t), seed, testTurnID, verdict.Modifiers...)
	triples, err := first.Triples(testTurnEntity, testRollRef, dice.Source, resolveTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	reader := newReader()
	reader.entities[testTurnEntity] = turnEntity(triples...)

	resolution, err := newResolver(t, reader, seed).Resolve(t.Context(), verdict, testTurnEntity)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Rolled {
		t.Fatal("a turn that already carried a roll was rolled a second time")
	}
	if resolution.Roll.Total != first.Total || resolution.Roll.Band != first.Band {
		t.Fatalf("the re-derived roll (%d/%s) differs from the recorded one (%d/%s)",
			resolution.Roll.Total, resolution.Roll.Band, first.Total, first.Band)
	}
}

// The roll gate belongs to the adjudicator (D12). These two cases are the whole
// decision: the mapping's advice is ignored in BOTH directions.
func TestResolve_HonorsTheReportedRollGateAndNotTheAdvisoryMapping(t *testing.T) {
	t.Run("the persona declines the dice on an action the mapping would roll", func(t *testing.T) {
		verdict := rollingVerdict()
		verdict.Scalars.RequiresRoll = false
		verdict.Bands = bandsFor(false)

		advised, err := vocabulary.RequiresRoll(verdict.Scalars.Plausibility, verdict.Scalars.Risk)
		if err != nil {
			t.Fatalf("RequiresRoll: %v", err)
		}
		if !advised {
			t.Fatal("the fixture's classes do not disagree with the mapping; this case would prove nothing")
		}

		reader := newReader()
		reader.entities[testTurnEntity] = turnEntity()

		resolution, err := newResolver(t, reader, seedOf(0x09)).Resolve(t.Context(), verdict, testTurnEntity)
		if !errors.Is(err, dice.ErrNoRollRequired) {
			t.Fatalf("Resolve returned %v, want ErrNoRollRequired: the mapping must not make the dice run", err)
		}
		if resolution.Roll != nil || resolution.Rolled {
			t.Fatalf("a refused resolve returned a roll: %+v", resolution)
		}
	})

	t.Run("the persona calls for the dice on an action the mapping would not roll", func(t *testing.T) {
		verdict := rollingVerdict()
		verdict.Scalars.Plausibility = vocabulary.PlausibilityImpossible // mapping: no roll
		verdict.Scalars.RequiresRoll = true                              // persona: roll

		advised, err := vocabulary.RequiresRoll(verdict.Scalars.Plausibility, verdict.Scalars.Risk)
		if err != nil {
			t.Fatalf("RequiresRoll: %v", err)
		}
		if advised {
			t.Fatal("the fixture's classes do not disagree with the mapping; this case would prove nothing")
		}

		reader := newReader()
		reader.entities[testTurnEntity] = turnEntity()

		resolution, err := newResolver(t, reader, seedOf(0x09)).Resolve(t.Context(), verdict, testTurnEntity)
		if err != nil {
			t.Fatalf("Resolve refused a verdict that reported requires_roll: %v", err)
		}
		if !resolution.Rolled || resolution.Roll == nil {
			t.Fatal("the dice did not run for a verdict whose reported gate called for them")
		}
	})
}

// Everything a damaged or half-written turn entity can say about its roll must
// stop the turn rather than resolve into a plausible answer.
func TestResolve_RefusesAnUnreadableOrDamagedTurnEntity(t *testing.T) {
	seed := seedOf(0x05)
	verdict := rollingVerdict()
	roll := mustRoll(t, newRoller(t), seed, testTurnID, verdict.Modifiers...)
	good, err := roll.Triples(testTurnEntity, testRollRef, dice.Source, resolveTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	tripleFor := func(predicate vocabulary.Predicate, object any) message.Triple {
		return message.Triple{
			Subject: testTurnEntity, Predicate: predicate.String(), Object: object,
			Source: dice.Source, Timestamp: resolveTime, Confidence: 1.0, Context: testTurnID,
		}
	}

	cases := []struct {
		name    string
		entity  *graph.EntityState
		wantErr string
	}{
		{
			name: "a referential stub, which holds no facts at all",
			entity: &graph.EntityState{
				ID:          testTurnEntity,
				MessageType: graph.StubMessageType,
				Triples: []message.Triple{
					tripleFor(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseResolving)),
				},
			},
			wantErr: "referential stub",
		},
		{
			// The signature of a write that took the APPEND lane. A reader
			// that took the first value would pick one band at random.
			name: "two bands, from an append-lane write",
			entity: turnEntity(append(append([]message.Triple{}, good...),
				tripleFor(vocabulary.TurnRollBand, string(vocabulary.BandMiss)))...),
			wantErr: "replace lane",
		},
		{
			name:    "a band with no reference",
			entity:  turnEntity(good[0]),
			wantErr: "of the 3 roll predicates",
		},
		{
			name: "a band that its own inputs no longer produce",
			entity: turnEntity(
				tripleFor(vocabulary.TurnRollBand, string(vocabulary.BandFull)),
				tripleFor(vocabulary.TurnRollTotal, 11),
				tripleFor(vocabulary.TurnRollRef, testRollRef),
			),
			wantErr: "no longer reproducible",
		},
		{
			name: "a non-string band",
			entity: turnEntity(
				tripleFor(vocabulary.TurnRollBand, 3),
				tripleFor(vocabulary.TurnRollTotal, 8),
				tripleFor(vocabulary.TurnRollRef, testRollRef),
			),
			wantErr: "band, want a string",
		},
		{
			name: "a non-numeric total",
			entity: turnEntity(
				tripleFor(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				tripleFor(vocabulary.TurnRollTotal, "eight"),
				tripleFor(vocabulary.TurnRollRef, testRollRef),
			),
			wantErr: "total, want a number",
		},
		{
			name: "a fractional total",
			entity: turnEntity(
				tripleFor(vocabulary.TurnRollBand, string(vocabulary.BandPartial)),
				tripleFor(vocabulary.TurnRollTotal, 8.5),
				tripleFor(vocabulary.TurnRollRef, testRollRef),
			),
			wantErr: "total, want a number",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := newReader()
			reader.entities[testTurnEntity] = tc.entity

			resolution, err := newResolver(t, reader, seed).Resolve(t.Context(), verdict, testTurnEntity)
			if err == nil {
				t.Fatalf("Resolve accepted a damaged turn entity: %+v", resolution)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("failure reason %q does not mention %q", err.Error(), tc.wantErr)
			}
			if resolution.Roll != nil || resolution.Rolled {
				t.Fatalf("a refused resolve returned a roll: %+v", resolution)
			}
		})
	}
}

// A total that has been through the graph's JSON round trip comes back as
// float64. Comparing it as an int would fail for every roll ever recorded — the
// guard would think each recorded roll had become unreproducible and stop every
// duplicate trigger with an integrity error.
func TestResolve_AcceptsATotalThatCameBackThroughJSONAsAFloat(t *testing.T) {
	seed := seedOf(0x05)
	verdict := rollingVerdict()
	roll := mustRoll(t, newRoller(t), seed, testTurnID, verdict.Modifiers...)
	triples, err := roll.Triples(testTurnEntity, testRollRef, dice.Source, resolveTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	for idx := range triples {
		if triples[idx].Predicate == vocabulary.TurnRollTotal.String() {
			triples[idx].Object = float64(roll.Total)
		}
	}

	reader := newReader()
	reader.entities[testTurnEntity] = turnEntity(triples...)

	resolution, err := newResolver(t, reader, seed).Resolve(t.Context(), verdict, testTurnEntity)
	if err != nil {
		t.Fatalf("Resolve rejected a total that round-tripped through JSON: %v", err)
	}
	if resolution.Rolled {
		t.Fatal("a recorded roll read back as a float was treated as absent")
	}
}

func TestResolve_PropagatesAnUnreadableTurnEntity(t *testing.T) {
	reader := newReader()
	reader.err = errors.New("nats: no responders")

	_, err := newResolver(t, reader, seedOf(0x05)).Resolve(t.Context(), rollingVerdict(), testTurnEntity)
	if err == nil {
		t.Fatal("a failed read resolved into a roll")
	}
}

func TestResolve_RefusesAnAbsentTurnEntity(t *testing.T) {
	_, err := newResolver(t, newReader(), seedOf(0x05)).Resolve(t.Context(), rollingVerdict(), testTurnEntity)
	if !errors.Is(err, graphio.ErrEntityNotFound) {
		t.Fatalf("Resolve on an absent turn returned %v, want ErrEntityNotFound", err)
	}
}

// The verdict and the turn entity arrive as independent arguments, and the
// resolver reads one of each: the roll is re-derived from the VERDICT's turn id
// while the at-most-one guard reads the ENTITY's recorded roll. A mismatched
// pair therefore defeats the guard in both directions — the entity's own turn
// gets a roll derived from somebody else's inputs, and the verdict's turn is
// free to roll again through its own entity.
func TestResolve_RefusesAVerdictPairedWithAnotherTurnsEntity(t *testing.T) {
	foreign := "c360.semmachina.world1.starter.turn.turn-act-9"
	reader := newReader()
	reader.entities[foreign] = &graph.EntityState{
		ID:          foreign,
		MessageType: message.Type{Domain: payload.Domain, Category: "turn_entity", Version: payload.SchemaVersion},
		UpdatedAt:   resolveTime,
	}

	_, err := newResolver(t, reader, seedOf(0x05)).Resolve(t.Context(), rollingVerdict(), foreign)
	if err == nil {
		t.Fatal("a verdict was rolled against another turn's entity")
	}
	if !strings.Contains(err.Error(), testTurnID) || !strings.Contains(err.Error(), "turn-act-9") {
		t.Fatalf("failure %q does not name both turns", err)
	}
}

func TestResolve_RefusesAnInvalidVerdict(t *testing.T) {
	reader := newReader()
	reader.entities[testTurnEntity] = turnEntity()

	verdict := rollingVerdict()
	verdict.Scalars.Risk = "severe"

	if _, err := newResolver(t, reader, seedOf(0x05)).Resolve(t.Context(), verdict, testTurnEntity); err == nil {
		t.Fatal("a verdict outside the closed vocabulary produced a roll")
	}
}

func TestNewResolver_RefusesAnIncompleteCampaign(t *testing.T) {
	roller := newRoller(t)
	reader := newReader()
	good := campaign.Instantiation{CampaignID: testCampaignID, Seed: seedOf(0x05)}

	cases := []struct {
		name          string
		roller        *dice.Roller
		reader        dice.TurnReader
		instantiation campaign.Instantiation
	}{
		{"no roller", nil, reader, good},
		{"no reader", roller, nil, good},
		{"no seed", roller, reader, campaign.Instantiation{CampaignID: testCampaignID}},
		{"no campaign id", roller, reader, campaign.Instantiation{Seed: seedOf(0x05)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dice.NewResolver(tc.roller, tc.reader, tc.instantiation); err == nil {
				t.Fatal("an incomplete resolver was built")
			}
		})
	}
}
