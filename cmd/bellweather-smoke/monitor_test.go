package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

type scriptedObserver struct {
	snapshots []turnSnapshot
	err       error
	calls     int
}

type scriptedCaseObserver struct {
	phases []vocabulary.CasePhase
	calls  int
}

type contextCaseObserver struct {
	phase vocabulary.CasePhase
	err   error
}

func (o contextCaseObserver) casePhase(ctx context.Context, _ string) (vocabulary.CasePhase, error) {
	<-ctx.Done()
	if o.err != nil {
		return "", o.err
	}
	return o.phase, nil
}

func (o *scriptedCaseObserver) casePhase(context.Context, string) (vocabulary.CasePhase, error) {
	idx := o.calls
	if idx >= len(o.phases) {
		idx = len(o.phases) - 1
	}
	o.calls++
	return o.phases[idx], nil
}

func (o *scriptedObserver) snapshot(context.Context, string) (turnSnapshot, error) {
	if o.err != nil {
		return turnSnapshot{}, o.err
	}
	idx := o.calls
	if idx >= len(o.snapshots) {
		idx = len(o.snapshots) - 1
	}
	o.calls++
	return o.snapshots[idx], nil
}

func immediateWait(context.Context, time.Duration) error { return nil }

func TestMonitorUsesTheClosedPhaseObservationBudgets(t *testing.T) {
	tests := []struct {
		phase  vocabulary.TurnPhase
		budget time.Duration
	}{
		{vocabulary.PhaseAccepted, 60 * time.Second},
		{vocabulary.PhaseResolving, 60 * time.Second},
		{vocabulary.PhaseApplying, 60 * time.Second},
		{vocabulary.PhaseInterpreting, 120 * time.Second},
		{vocabulary.PhaseAdjudicating, 120 * time.Second},
		{vocabulary.PhaseCompanion, 120 * time.Second},
		{vocabulary.PhaseNarrating, 150 * time.Second},
	}
	for _, test := range tests {
		t.Run(string(test.phase), func(t *testing.T) {
			observer := &scriptedObserver{snapshots: []turnSnapshot{{Phase: test.phase, Pending: 1}}}
			err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
				PollInterval: 30 * time.Second,
				Wait:         immediateWait,
			})
			want := fmt.Sprintf(
				"agentic observation budget reached after %s without phase or queue movement", test.budget)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("monitor error = %v, want %q", err, want)
			}
			if strings.Contains(err.Error(), "wedge") || strings.Contains(err.Error(), "engine failure") {
				t.Fatalf("acceptance-cost error overclaimed engine state: %v", err)
			}
			wantCalls := int(test.budget/(30*time.Second)) + 1
			if observer.calls != wantCalls {
				t.Fatalf("observer calls = %d, want initial plus budget polls (%d)", observer.calls, wantCalls)
			}
		})
	}
}

func TestMonitorAcceptsPhaseProgress(t *testing.T) {
	observer := &scriptedObserver{snapshots: []turnSnapshot{
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/20/1/2"},
		{Phase: vocabulary.PhaseResolving, Pending: 1, QueuePosition: "11/21/1/2"},
		{Phase: vocabulary.PhaseComplete, Pending: 0, QueuePosition: "12/21/1/2"},
	}}

	if err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
		PollInterval: 30 * time.Second,
		Wait:         immediateWait,
	}); err != nil {
		t.Fatalf("monitor rejected progressing turn: %v", err)
	}
}

func TestMonitorQueueMovementResetsTheCurrentPhaseBudget(t *testing.T) {
	observer := &scriptedObserver{snapshots: []turnSnapshot{
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/20/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/20/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/20/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/20/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/21/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/21/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/21/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/21/1/2"},
		{Phase: vocabulary.PhaseComplete, Pending: 0, QueuePosition: "11/22/1/2"},
	}}

	if err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
		PollInterval: 30 * time.Second,
		Wait:         immediateWait,
	}); err != nil {
		t.Fatalf("monitor rejected a correction whose agent queue moved: %v", err)
	}
}

func TestMonitorAbortsFailedAndUnreadableStateImmediately(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		observer := &scriptedObserver{snapshots: []turnSnapshot{{Phase: vocabulary.PhaseFailed}}}
		err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
			PollInterval: 30 * time.Second, Wait: immediateWait,
		})
		if !errors.Is(err, errTurnFailed) {
			t.Fatalf("monitor error = %v, want errTurnFailed", err)
		}
		if observer.calls != 1 {
			t.Fatalf("failed phase needed %d observations, want 1", observer.calls)
		}
	})

	t.Run("observer error", func(t *testing.T) {
		observer := &scriptedObserver{err: errors.New("queue unavailable")}
		err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
			PollInterval: 30 * time.Second, Wait: immediateWait,
		})
		if err == nil || !strings.Contains(err.Error(), "queue unavailable") {
			t.Fatalf("monitor error = %v, want authoritative read failure", err)
		}
		if observer.calls != 0 {
			t.Fatalf("observer recorded %d successful calls, want 0", observer.calls)
		}
	})
}

func TestMonitorPreservesPaidCapCausesFromObserverAndWaitPaths(t *testing.T) {
	t.Run("observer", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		observer := &cancellingTurnObserver{cancel: cancel, err: context.DeadlineExceeded}
		err := monitorTurn(ctx, observer, "turn-entity", monitorPolicy{
			PollInterval: time.Second,
			Wait:         immediateWait,
		})
		if !errors.Is(err, errTurnPaidCap) {
			t.Fatalf("monitor observer error = %v, want preserved per-turn paid cap", err)
		}
	})

	t.Run("wait", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		observer := &scriptedObserver{snapshots: []turnSnapshot{{Phase: vocabulary.PhaseAccepted}}}
		err := monitorTurn(ctx, observer, "turn-entity", monitorPolicy{
			PollInterval: time.Second,
			Wait: func(context.Context, time.Duration) error {
				cancel(errSmokePaidCap)
				return context.DeadlineExceeded
			},
		})
		if !errors.Is(err, errSmokePaidCap) {
			t.Fatalf("monitor wait error = %v, want preserved whole-smoke paid cap", err)
		}
	})
}

type cancellingTurnObserver struct {
	cancel context.CancelCauseFunc
	err    error
}

func (o *cancellingTurnObserver) snapshot(context.Context, string) (turnSnapshot, error) {
	o.cancel(errTurnPaidCap)
	return turnSnapshot{}, o.err
}

func TestBodyObservationWaitsForAuthoritativeDiscoveryBeforeSecondTurn(t *testing.T) {
	observer := &scriptedCaseObserver{phases: []vocabulary.CasePhase{
		vocabulary.CasePhaseColdOpen,
		vocabulary.CasePhaseDiscovery,
	}}
	if err := awaitCasePhase(t.Context(), observer, "case-entity", vocabulary.CasePhaseDiscovery, casePhasePolicy{
		PollInterval: 500 * time.Millisecond,
		Timeout:      30 * time.Second,
		Wait:         immediateWait,
	}); err != nil {
		t.Fatalf("await discovery: %v", err)
	}
	if observer.calls != 2 {
		t.Fatalf("case phase reads = %d, want cold_open then discovery", observer.calls)
	}
}

func TestCasePhaseProofDeadlineIncludesObserverReadAndWait(t *testing.T) {
	t.Run("blocked read", func(t *testing.T) {
		err := awaitCasePhase(t.Context(), contextCaseObserver{err: context.DeadlineExceeded},
			"case-entity", vocabulary.CasePhaseDiscovery, casePhasePolicy{
				PollInterval: time.Millisecond,
				Timeout:      10 * time.Millisecond,
				Wait:         waitContext,
			})
		if !errors.Is(err, errCasePhaseProofCap) {
			t.Fatalf("blocked case read error = %v, want case proof cap", err)
		}
	})

	t.Run("read completes after deadline", func(t *testing.T) {
		err := awaitCasePhase(t.Context(), contextCaseObserver{phase: vocabulary.CasePhaseDiscovery},
			"case-entity", vocabulary.CasePhaseDiscovery, casePhasePolicy{
				PollInterval: time.Millisecond,
				Timeout:      10 * time.Millisecond,
				Wait:         waitContext,
			})
		if !errors.Is(err, errCasePhaseProofCap) {
			t.Fatalf("late successful case read error = %v, want case proof cap", err)
		}
	})

	t.Run("blocked wait", func(t *testing.T) {
		observer := &scriptedCaseObserver{phases: []vocabulary.CasePhase{vocabulary.CasePhaseColdOpen}}
		err := awaitCasePhase(t.Context(), observer, "case-entity", vocabulary.CasePhaseDiscovery,
			casePhasePolicy{
				PollInterval: time.Second,
				Timeout:      10 * time.Millisecond,
				Wait:         waitContext,
			})
		if !errors.Is(err, errCasePhaseProofCap) {
			t.Fatalf("blocked case wait error = %v, want case proof cap", err)
		}
	})
}

func TestCasePhaseProofPreservesParentPaidCap(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errSmokePaidCap)
	err := awaitCasePhase(ctx, contextCaseObserver{err: context.Canceled},
		"case-entity", vocabulary.CasePhaseDiscovery, casePhasePolicy{
			PollInterval: time.Millisecond,
			Timeout:      30 * time.Second,
			Wait:         waitContext,
		})
	if !errors.Is(err, errSmokePaidCap) {
		t.Fatalf("case proof error = %v, want preserved whole-smoke paid cap", err)
	}
}
