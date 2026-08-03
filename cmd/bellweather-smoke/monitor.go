package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/resume"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

var errTurnFailed = errors.New("turn reached the failed terminal phase")

type turnSnapshot struct {
	Phase         vocabulary.TurnPhase
	Pending       int
	QueuePosition string
}

type turnObserver interface {
	snapshot(context.Context, string) (turnSnapshot, error)
}

type caseObserver interface {
	casePhase(context.Context, string) (vocabulary.CasePhase, error)
}

type hintProofObserver interface {
	turnState(context.Context, string) (*graph.EntityState, error)
}

type monitorPolicy struct {
	PollInterval time.Duration
	Wait         func(context.Context, time.Duration) error
	OnSnapshot   func(turnSnapshot)
}

func phaseObservationBudget(phase vocabulary.TurnPhase) (time.Duration, error) {
	switch phase {
	case "", vocabulary.PhaseAccepted, vocabulary.PhaseResolving, vocabulary.PhaseApplying:
		return 60 * time.Second, nil
	case vocabulary.PhaseInterpreting, vocabulary.PhaseAdjudicating, vocabulary.PhaseCompanion:
		return 120 * time.Second, nil
	case vocabulary.PhaseNarrating:
		return 150 * time.Second, nil
	case vocabulary.PhaseComplete, vocabulary.PhaseFailed:
		return 0, nil
	default:
		return 0, fmt.Errorf("turn monitor has no observation budget for phase %q", phase)
	}
}

func monitorTurn(ctx context.Context, observer turnObserver, turnEntityID string, policy monitorPolicy) error {
	if observer == nil {
		return errors.New("turn monitor requires an authoritative observer")
	}
	if policy.PollInterval <= 0 || policy.Wait == nil {
		return errors.New("turn monitor requires a positive poll interval plus a wait function")
	}

	var (
		previous turnSnapshot
		have     bool
		stalled  time.Duration
	)
	for {
		if ctx.Err() != nil {
			return paidCapContextError(ctx, "authoritative turn observation")
		}
		current, err := observer.snapshot(ctx, turnEntityID)
		if err != nil {
			return normalizeContextError(ctx,
				fmt.Errorf("read authoritative turn and queue state: %w", err),
				"authoritative turn observation")
		}
		if ctx.Err() != nil {
			return paidCapContextError(ctx, "authoritative turn observation")
		}
		if policy.OnSnapshot != nil {
			policy.OnSnapshot(current)
		}
		switch current.Phase {
		case vocabulary.PhaseFailed:
			return errTurnFailed
		case vocabulary.PhaseComplete:
			return nil
		}
		budget, err := phaseObservationBudget(current.Phase)
		if err != nil {
			return err
		}

		if have && current == previous {
			stalled += policy.PollInterval
			if stalled >= budget {
				return fmt.Errorf(
					"agentic observation budget reached after %s without phase or queue movement (phase=%q pending=%d)",
					stalled, current.Phase, current.Pending)
			}
		} else {
			stalled = 0
		}
		previous, have = current, true
		if err := policy.Wait(ctx, policy.PollInterval); err != nil {
			return normalizeContextError(ctx, err, "authoritative turn observation")
		}
	}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type casePhasePolicy struct {
	PollInterval time.Duration
	Timeout      time.Duration
	Wait         func(context.Context, time.Duration) error
}

func awaitCasePhase(
	ctx context.Context,
	observer caseObserver,
	caseEntityID string,
	want vocabulary.CasePhase,
	policy casePhasePolicy,
) error {
	if observer == nil || policy.PollInterval <= 0 || policy.Timeout <= 0 || policy.Wait == nil {
		return errors.New("case phase monitor requires an observer and positive polling policy")
	}
	deadlineCause := fmt.Errorf("%w after %s", errCasePhaseProofCap, policy.Timeout)
	proofCtx, cancel := context.WithTimeoutCause(ctx, policy.Timeout, deadlineCause)
	defer cancel()
	for {
		if proofCtx.Err() != nil {
			return paidCapContextError(proofCtx, "the case reached the required phase")
		}
		phase, err := observer.casePhase(proofCtx, caseEntityID)
		if err != nil {
			return normalizeContextError(proofCtx,
				fmt.Errorf("read authoritative case phase: %w", err),
				"the case reached the required phase")
		}
		if proofCtx.Err() != nil {
			return paidCapContextError(proofCtx, "the case reached the required phase")
		}
		if phase == want {
			return nil
		}
		if err := policy.Wait(proofCtx, policy.PollInterval); err != nil {
			return normalizeContextError(proofCtx, err, "the case reached the required phase")
		}
	}
}

type productionObserver struct {
	graph  *graphio.Store
	queues *resume.WorkQueues
	intake jetstream.Consumer
	stages jetstream.Stream
	agent  jetstream.Stream
}

func newProductionObserver(ctx context.Context, client *natsclient.Client) (*productionObserver, error) {
	store, err := graphio.NewStore(client)
	if err != nil {
		return nil, err
	}
	stages, err := client.GetStream(ctx, rulepack.StageStream)
	if err != nil {
		return nil, fmt.Errorf("open stage stream: %w", err)
	}
	agent, err := client.GetStream(ctx, persona.TaskStream)
	if err != nil {
		return nil, fmt.Errorf("open agent stream: %w", err)
	}
	queues, err := resume.NewWorkQueues(stages, agent)
	if err != nil {
		return nil, err
	}
	js, err := client.JetStream()
	if err != nil {
		return nil, err
	}
	intake, err := js.Consumer(ctx, turn.ActionStream, turn.IntakeConsumer)
	if err != nil {
		return nil, fmt.Errorf("open player-action intake consumer: %w", err)
	}
	return &productionObserver{graph: store, queues: queues, intake: intake, stages: stages, agent: agent}, nil
}

func (o *productionObserver) snapshot(ctx context.Context, turnEntityID string) (turnSnapshot, error) {
	var phase vocabulary.TurnPhase
	state, err := o.graph.GetEntity(ctx, turnEntityID)
	if err == nil {
		object, ok := state.GetPropertyValue(vocabulary.TurnPhaseCurrent.String())
		if !ok {
			return turnSnapshot{}, errors.New("turn entity carries no current phase")
		}
		parsed, parseErr := vocabulary.ParseTurnPhase(fmt.Sprint(object))
		if parseErr != nil {
			return turnSnapshot{}, parseErr
		}
		phase = parsed
	} else if !errors.Is(err, graphio.ErrEntityNotFound) {
		return turnSnapshot{}, fmt.Errorf("read turn entity: %w", err)
	}

	pending, err := o.queues.Pending(ctx)
	if err != nil {
		return turnSnapshot{}, fmt.Errorf("measure turn work queues: %w", err)
	}
	intakeInfo, err := o.intake.Info(ctx)
	if err != nil {
		return turnSnapshot{}, fmt.Errorf("measure player-action intake queue: %w", err)
	}
	stageInfo, err := o.stages.Info(ctx)
	if err != nil {
		return turnSnapshot{}, fmt.Errorf("measure stage stream progress: %w", err)
	}
	agentInfo, err := o.agent.Info(ctx)
	if err != nil {
		return turnSnapshot{}, fmt.Errorf("measure agent stream progress: %w", err)
	}
	return turnSnapshot{
		Phase:   phase,
		Pending: pending[turnEntityID] + int(intakeInfo.NumAckPending) + int(intakeInfo.NumPending),
		QueuePosition: fmt.Sprintf("%d/%d/%d/%d", stageInfo.State.LastSeq, agentInfo.State.LastSeq,
			intakeInfo.AckFloor.Stream, intakeInfo.Delivered.Stream),
	}, nil
}

func (o *productionObserver) casePhase(ctx context.Context, caseEntityID string) (vocabulary.CasePhase, error) {
	state, err := o.graph.GetEntity(ctx, caseEntityID)
	if err != nil {
		return "", err
	}
	object, ok := state.GetPropertyValue(vocabulary.CaseLifecyclePhase.String())
	if !ok {
		return "", errors.New("case entity carries no lifecycle phase")
	}
	return vocabulary.ParseCasePhase(fmt.Sprint(object))
}

func (o *productionObserver) turnState(ctx context.Context, turnEntityID string) (*graph.EntityState, error) {
	return o.graph.GetEntity(ctx, turnEntityID)
}

func proveHintTrigger(ctx context.Context, observer hintProofObserver, turnEntityID string) error {
	if observer == nil {
		return errors.New("hint trigger proof requires an authoritative observer")
	}
	state, err := observer.turnState(ctx, turnEntityID)
	if err != nil {
		return fmt.Errorf("read authoritative hint turn: %w", err)
	}
	return requireHintTriggerProof(state)
}

func requireHintTriggerProof(state *graph.EntityState) error {
	if state == nil {
		return errors.New("authoritative hint turn is absent")
	}
	expectations := []struct {
		predicate vocabulary.Predicate
		want      string
	}{
		{vocabulary.TurnCaseDecisionKind, string(payload.CaseDecisionRequestHint)},
		{vocabulary.TurnCompanionTriggerKind, string(vocabulary.CompanionTriggerPlayerHint)},
		{vocabulary.TurnCompanionTriggerSource, string(vocabulary.CompanionTriggerSourceCaseDecision)},
	}
	for _, expectation := range expectations {
		value, err := exactPersistedString(state, expectation.predicate)
		if err != nil {
			return err
		}
		if value != expectation.want {
			return fmt.Errorf("authoritative %s = %q, want %q", expectation.predicate, value, expectation.want)
		}
	}
	return nil
}

func exactPersistedString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var values []string
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok || value == "" {
			return "", fmt.Errorf("authoritative %s is not a non-empty string", predicate)
		}
		values = append(values, value)
	}
	if len(values) != 1 {
		return "", fmt.Errorf("authoritative %s has %d values, want exactly one", predicate, len(values))
	}
	return values[0], nil
}
