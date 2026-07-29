package stage_test

import (
	"context"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testCampaignID = "acme.semmachina.keep.starter.campaign.instance"

type fakeTurns struct{ state *graph.EntityState }

func (f *fakeTurns) GetEntity(_ context.Context, _ string) (*graph.EntityState, error) {
	return f.state, nil
}

type fakeVerdicts struct{ verdict *payload.Verdict }

func (f *fakeVerdicts) GetVerdict(_ context.Context, _ content.Ref) (*payload.Verdict, error) {
	return f.verdict, nil
}

type fakeRolls struct {
	puts int
	ref  content.Ref
}

func (f *fakeRolls) PutRoll(_ context.Context, _ string, _ *payload.RollResult) (content.Ref, error) {
	f.puts++
	return f.ref, nil
}

type fakeWriter struct{ merges int }

func (f *fakeWriter) MergeTriples(
	_ context.Context, _ string, _ []message.Triple, _ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	f.merges++
	return nil, nil
}

// A duplicate resolve trigger must not re-store or re-write the roll.
//
// The world converges either way — the roll is a pure function of the seed and
// the turn, the content key is derived, and the merge lane replaces — so this is
// invisible in end state and only a write count can see it. It is worth holding
// anyway: "the turn already carries its roll" is what the code claims, and a
// stage that silently rewrites a settled fact on every redelivery is one
// duplicate-delivery storm away from being the loudest writer in the system.
func TestResolverStage_ARedeliveredTriggerDoesNotRewriteASettledRoll(t *testing.T) {
	roller, err := dice.NewRoller(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		t.Fatalf("NewRoller: %v", err)
	}
	seed, err := campaign.NewSeed()
	if err != nil {
		t.Fatalf("NewSeed: %v", err)
	}
	roll, err := roller.Roll(seed, testCampaignID, testTurnID, nil)
	if err != nil {
		t.Fatalf("Roll: %v", err)
	}

	rollRef := content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/roll"}
	verdictRef := content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/verdict"}
	turns := &fakeTurns{state: settledTurn(roll, rollRef, verdictRef)}
	verdicts := &fakeVerdicts{verdict: rollingVerdict(t)}
	rolls := &fakeRolls{ref: rollRef}
	writer := &fakeWriter{}

	resolver, err := dice.NewResolver(roller, turns, campaign.Instantiation{
		CampaignID: testCampaignID, Seed: seed,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	recorder := &fakeRecorder{
		journal: &journal{},
		transition: turn.Transition{
			Previous: vocabulary.PhaseResolving, Phase: vocabulary.PhaseResolving, Outcome: turn.OutcomeResumed,
		},
	}
	diceStage, err := stage.NewResolver(recorder, turns, verdicts, rolls, writer, resolver)
	if err != nil {
		t.Fatalf("NewResolver stage: %v", err)
	}

	if err := diceStage.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rolls.puts != 0 {
		t.Errorf("a settled roll was stored again %d time(s)", rolls.puts)
	}
	if writer.merges != 0 {
		t.Errorf("a settled roll was written to the graph again %d time(s)", writer.merges)
	}
}

func settledTurn(roll *payload.RollResult, rollRef, verdictRef content.Ref) *graph.EntityState {
	at := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	fact := func(predicate vocabulary.Predicate, object any) message.Triple {
		return message.Triple{
			Subject: testTurnEntityID, Predicate: predicate.String(), Object: object,
			Source: "test", Timestamp: at, Confidence: 1.0,
		}
	}
	return &graph.EntityState{
		ID:          testTurnEntityID,
		MessageType: message.Type{Domain: payload.Domain, Category: payload.CategoryTurnState, Version: payload.SchemaVersion},
		Version:     1,
		UpdatedAt:   at,
		Triples: []message.Triple{
			fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseResolving)),
			fact(vocabulary.TurnVerdictRef, verdictRef.String()),
			fact(vocabulary.TurnVerdictRequiresRoll, true),
			fact(vocabulary.TurnRollBand, string(roll.Band)),
			fact(vocabulary.TurnRollTotal, roll.Total),
			fact(vocabulary.TurnRollRef, rollRef.String()),
		},
	}
}

func rollingVerdict(t *testing.T) *payload.Verdict {
	t.Helper()
	intents := func(status vocabulary.Status) []payload.EffectIntent {
		return []payload.EffectIntent{{
			Type: vocabulary.EffectSetStatus, Target: testSceneID, Status: status,
		}}
	}
	verdict := &payload.Verdict{
		TurnID:   testTurnID,
		ActionID: testActionID,
		SceneID:  testSceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: vocabulary.PlausibilityPlausible,
			Risk:         vocabulary.RiskModerate,
			Consequence:  vocabulary.ConsequenceHarm,
			RequiresRoll: true,
		},
		Bands: payload.EffectBands{
			vocabulary.BandMiss:    intents(vocabulary.StatusWounded),
			vocabulary.BandPartial: intents(vocabulary.StatusExhausted),
			vocabulary.BandFull:    intents(vocabulary.StatusHealthy),
		},
		Rationale: "a settled turn",
	}
	if err := verdict.Validate(); err != nil {
		t.Fatalf("the test verdict is invalid: %v", err)
	}
	return verdict
}
