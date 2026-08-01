package accusation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/storage"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	consumerTurnEntity = "c360.semmachina.bellweather.campaign.turn.turn-action-1"
	consumerCase       = "c360.semmachina.bellweather.campaign.case.murder"
	consumerActor      = "c360.semmachina.bellweather.campaign.character.player"
	consumerCulprit    = "c360.semmachina.bellweather.campaign.character.culprit"
	consumerMethod     = "c360.semmachina.bellweather.campaign.item.method"
	consumerMotive     = "c360.semmachina.bellweather.campaign.evidence.motive"
)

type fixedLoader struct {
	preflight Preflight
	err       error
	calls     int
}

func (l *fixedLoader) Load(context.Context, string) (Preflight, error) {
	l.calls++
	return l.preflight, l.err
}

type captureCommitter struct {
	results       []*payload.AccusationResult
	notApplicable int
}

func (c *captureCommitter) CommitResult(_ context.Context, _ string, result *payload.AccusationResult) (content.Ref, error) {
	resultSnapshot := *result
	c.results = append(c.results, &resultSnapshot)
	return content.Ref{Instance: "TEST", Key: "turn/turn-action-1/accusation"}, nil
}

func (c *captureCommitter) CommitNotApplicable(context.Context, string, string) (content.Ref, error) {
	c.notApplicable++
	return content.Ref{Instance: "TEST", Key: "turn/turn-action-1/accusation"}, nil
}

type captureLifecycle struct{ requests []caseflow.TransitionRequest }

func (l *captureLifecycle) Record(_ context.Context, request caseflow.TransitionRequest) (caseflow.ReceiptOutcome, error) {
	l.requests = append(l.requests, request)
	return caseflow.ReceiptOutcome{Recorded: true}, nil
}

type captureFailer struct {
	reasons []vocabulary.FailureReason
	err     error
}

func (f *captureFailer) Fail(_ context.Context, _, _ string, reason vocabulary.FailureReason, _ content.Ref) (turn.Transition, error) {
	f.reasons = append(f.reasons, reason)
	return turn.Transition{}, f.err
}

type durableCapture struct {
	cfg natsclient.StreamConsumerConfig
}

type productionFailureGraph struct {
	turn *graph.EntityState
	err  error
}

func (g productionFailureGraph) GetEntity(context.Context, string) (*graph.EntityState, error) {
	return g.turn, g.err
}

type productionFailureStore struct{ err error }

func (s productionFailureStore) GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error) {
	return nil, s.err
}

type productionArtifactBackend struct {
	data []byte
	err  error
}

func (productionArtifactBackend) InstanceName() string                      { return "TEST" }
func (productionArtifactBackend) Put(context.Context, string, []byte) error { return nil }
func (b productionArtifactBackend) Get(context.Context, string) ([]byte, error) {
	return b.data, b.err
}

func (d *durableCapture) ConsumeDurable(_ context.Context, cfg natsclient.StreamConsumerConfig,
	_ time.Duration, _ func(context.Context, []byte) error) error {
	d.cfg = cfg
	return nil
}

func consumerDecision(culprit string) *payload.CaseDecision {
	d := &payload.CaseDecision{
		TurnID: "turn-action-1", ActionID: "action-1", CaseID: consumerCase, ActorID: consumerActor,
		Kind: payload.CaseDecisionAccuse, CulpritRef: culprit, MethodRef: consumerMethod, MotiveRef: consumerMotive,
		TargetRefs: []string{}, RevealRefs: []string{},
	}
	d.DecisionID = payload.CaseDecisionID(d.TurnID, d.ActionID, d.CaseID, d.ActorID)
	return d
}

func consumerTrigger(t *testing.T) []byte {
	t.Helper()
	wire, err := json.Marshal(map[string]any{
		"entity_id": consumerTurnEntity, "subject": rulepack.SubjectAccusation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func newHandleConsumer(t *testing.T, phase vocabulary.CasePhase, culprit string) (*Consumer, *fixedLoader, *captureCommitter, *captureLifecycle, *captureFailer) {
	t.Helper()
	loader := &fixedLoader{preflight: Preflight{
		TurnEntityID: consumerTurnEntity, TurnID: "turn-action-1", Applicable: true, CaseID: consumerCase,
		CasePhase: phase, Decision: consumerDecision(culprit),
		Solution: epistemic.Solution{Culprit: consumerCulprit, Method: consumerMethod, Motive: consumerMotive},
	}}
	committer := &captureCommitter{}
	lifecycle := &captureLifecycle{}
	failer := &captureFailer{}
	return &Consumer{loader: loader, committer: committer, lifecycle: lifecycle, failer: failer},
		loader, committer, lifecycle, failer
}

func TestConsumerLifecycleTimingWrongStayAndCorrectTransition(t *testing.T) {
	t.Run("non-accuse completes universal barrier", func(t *testing.T) {
		consumer, loader, committer, lifecycle, _ := newHandleConsumer(
			t, vocabulary.CasePhaseInvestigation, consumerCulprit)
		loader.preflight.Applicable = false
		loader.preflight.CaseID = ""
		loader.preflight.Decision = nil
		if err := consumer.Handle(t.Context(), consumerTrigger(t)); err != nil {
			t.Fatal(err)
		}
		if committer.notApplicable != 1 || len(committer.results) != 0 || len(lifecycle.requests) != 0 {
			t.Fatalf("not-applicable=%d results=%d lifecycle=%#v",
				committer.notApplicable, len(committer.results), lifecycle.requests)
		}
	})

	t.Run("transition lag retries before verification", func(t *testing.T) {
		consumer, _, committer, lifecycle, _ := newHandleConsumer(t, vocabulary.CasePhaseInvestigation, consumerCulprit)
		if err := consumer.Handle(t.Context(), consumerTrigger(t)); err == nil {
			t.Fatal("investigation-phase delivery did not retry for lifecycle transition")
		}
		if len(committer.results) != 0 || len(lifecycle.requests) != 1 ||
			lifecycle.requests[0].Kind != vocabulary.CaseEventAccusationSubmitted {
			t.Fatalf("commits=%d lifecycle=%#v", len(committer.results), lifecycle.requests)
		}
	})

	t.Run("wrong stays accusation", func(t *testing.T) {
		consumer, _, committer, lifecycle, _ := newHandleConsumer(t, vocabulary.CasePhaseAccusation, consumerActor)
		if err := consumer.Handle(t.Context(), consumerTrigger(t)); err != nil {
			t.Fatal(err)
		}
		if len(committer.results) != 1 || committer.results[0].Outcome != payload.AccusationIncorrect ||
			len(lifecycle.requests) != 1 {
			t.Fatalf("results=%#v lifecycle=%#v", committer.results, lifecycle.requests)
		}
	})

	t.Run("correct requests denouement", func(t *testing.T) {
		consumer, _, committer, lifecycle, _ := newHandleConsumer(t, vocabulary.CasePhaseAccusation, consumerCulprit)
		if err := consumer.Handle(t.Context(), consumerTrigger(t)); err != nil {
			t.Fatal(err)
		}
		if len(committer.results) != 1 || committer.results[0].Outcome != payload.AccusationCorrect ||
			len(lifecycle.requests) != 2 || lifecycle.requests[1].Kind != vocabulary.CaseEventAccusationCorrect ||
			lifecycle.requests[1].EventID != committer.results[0].ResultID {
			t.Fatalf("results=%#v lifecycle=%#v", committer.results, lifecycle.requests)
		}
	})
}

func TestConsumerMalformedBeforeReadsAndPermanentClosedFailure(t *testing.T) {
	consumer, loader, _, _, failer := newHandleConsumer(t, vocabulary.CasePhaseAccusation, consumerCulprit)
	if err := consumer.Handle(t.Context(), []byte("not-json")); err == nil {
		t.Fatal("malformed trigger was acknowledged")
	}
	if loader.calls != 0 {
		t.Fatal("malformed trigger caused reads")
	}
	loader.err = &IntegrityError{err: errors.New("foreign identity")}
	if err := consumer.Handle(t.Context(), consumerTrigger(t)); err != nil {
		t.Fatalf("permanent failure was not closed: %v", err)
	}
	if len(failer.reasons) != 1 || failer.reasons[0] != vocabulary.FailureAccusationInvalid {
		t.Fatalf("failure reasons = %v", failer.reasons)
	}
}

func TestConsumerTerminatesMissingTriggeredTurnWithoutRecordingFailure(t *testing.T) {
	loader, err := NewLoader(
		productionFailureGraph{err: graphio.ErrEntityNotFound},
		productionFailureStore{}, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	failer := &captureFailer{}
	consumer := &Consumer{
		loader: loader, committer: &captureCommitter{}, lifecycle: &captureLifecycle{}, failer: failer,
	}

	handleErr := consumer.Handle(t.Context(), consumerTrigger(t))
	var missing *MissingTriggeredTurnError
	var permanent *natsclient.PermanentDeliveryError
	if !errors.As(handleErr, &missing) || !errors.As(handleErr, &permanent) {
		t.Fatalf("Handle error = %T %v, want terminating MissingTriggeredTurnError", handleErr, handleErr)
	}
	if len(failer.reasons) != 0 {
		t.Fatalf("missing trigger called Recorder.Fail with %v", failer.reasons)
	}
}

func TestConsumerTerminatesWhenTurnDisappearsWhileRecordingInvalidFailure(t *testing.T) {
	consumer, loader, _, _, failer := newHandleConsumer(
		t, vocabulary.CasePhaseAccusation, consumerCulprit)
	loader.err = &IntegrityError{err: errors.New("foreign artifact")}
	failer.err = fmt.Errorf("turn disappeared before failure commit: %w", graphio.ErrEntityNotFound)

	handleErr := consumer.Handle(t.Context(), consumerTrigger(t))
	var permanent *natsclient.PermanentDeliveryError
	if !errors.As(handleErr, &permanent) || !errors.Is(handleErr, graphio.ErrEntityNotFound) {
		t.Fatalf("Handle error = %T %v, want terminating ErrEntityNotFound", handleErr, handleErr)
	}
	if len(failer.reasons) != 1 || failer.reasons[0] != vocabulary.FailureAccusationInvalid {
		t.Fatalf("failure attempts = %v, want one accusation-invalid attempt", failer.reasons)
	}
}

func TestConsumerRetriesTransientFailureRecordingError(t *testing.T) {
	consumer, loader, _, _, failer := newHandleConsumer(
		t, vocabulary.CasePhaseAccusation, consumerCulprit)
	loader.err = &IntegrityError{err: errors.New("foreign artifact")}
	failer.err = errors.New("graph transport timeout")

	handleErr := consumer.Handle(t.Context(), consumerTrigger(t))
	var permanent *natsclient.PermanentDeliveryError
	if handleErr == nil || errors.As(handleErr, &permanent) {
		t.Fatalf("Handle error = %T %v, want retryable failure", handleErr, handleErr)
	}
}

func TestConsumerProductionLoaderClassifiesArtifactFailuresWithoutStringMatching(t *testing.T) {
	validRef := "obj://TEST/turn/turn-action-1/case-decision"
	triple := func(value any) message.Triple {
		return message.Triple{Subject: consumerTurnEntity,
			Predicate: vocabulary.TurnCaseDecisionRef.String(), Object: value}
	}
	for _, tc := range []struct {
		name      string
		triples   []message.Triple
		storeErr  error
		backend   *productionArtifactBackend
		permanent bool
	}{
		{name: "missing", triples: []message.Triple{triple(validRef)},
			backend: &productionArtifactBackend{err: storage.ErrObjectNotFound}, permanent: true},
		{name: "corrupt", triples: []message.Triple{triple(validRef)},
			backend: &productionArtifactBackend{data: []byte("bad-json")}, permanent: true},
		{name: "wrong store", triples: []message.Triple{triple(validRef)},
			storeErr: errors.Join(content.ErrArtifactReference, errors.New("foreign")), permanent: true},
		{name: "malformed ref", triples: []message.Triple{triple("not-a-reference")}, permanent: true},
		{name: "ambiguous ref", triples: []message.Triple{triple(validRef), triple(validRef)}, permanent: true},
		{name: "transport", triples: []message.Triple{triple(validRef)},
			storeErr: errors.New("transport timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &graph.EntityState{ID: consumerTurnEntity, Triples: tc.triples}
			var decisions DecisionStore = productionFailureStore{err: tc.storeErr}
			if tc.backend != nil {
				actualStore, storeErr := content.NewStore(*tc.backend)
				if storeErr != nil {
					t.Fatal(storeErr)
				}
				decisions = actualStore
			}
			loader, err := NewLoader(productionFailureGraph{turn: state}, decisions, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			committer := &captureCommitter{}
			lifecycle := &captureLifecycle{}
			failer := &captureFailer{}
			consumer := &Consumer{loader: loader, committer: committer, lifecycle: lifecycle, failer: failer}
			handleErr := consumer.Handle(t.Context(), consumerTrigger(t))
			if tc.permanent {
				if handleErr != nil || len(failer.reasons) != 1 ||
					failer.reasons[0] != vocabulary.FailureAccusationInvalid {
					t.Fatalf("permanent result: err=%v failures=%v", handleErr, failer.reasons)
				}
			} else if handleErr == nil || len(failer.reasons) != 0 {
				t.Fatalf("transient result: err=%v failures=%v", handleErr, failer.reasons)
			}
		})
	}
}

func TestConsumerStartBindsSeparateAccusationDurableOnTurnStages(t *testing.T) {
	durable := &durableCapture{}
	consumer, _, _, _, _ := newHandleConsumer(
		t, vocabulary.CasePhaseAccusation, consumerCulprit)
	consumer.durable = durable
	if err := consumer.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if durable.cfg.StreamName != rulepack.StageStream ||
		durable.cfg.ConsumerName != rulepack.AccusationConsumerName ||
		durable.cfg.FilterSubject != rulepack.SubjectAccusation || durable.cfg.DeliverPolicy != "all" {
		t.Fatalf("consumer config = %+v", durable.cfg)
	}
}
