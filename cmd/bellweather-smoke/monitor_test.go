package main

import (
	"context"
	"errors"
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

func TestMonitorStopsAfterSixtySecondsWithoutPhaseOrQueueProgress(t *testing.T) {
	observer := &scriptedObserver{snapshots: []turnSnapshot{
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1},
	}}

	err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
		PollInterval: 30 * time.Second,
		StallAfter:   60 * time.Second,
		Wait:         immediateWait,
	})
	if err == nil || !strings.Contains(err.Error(), "no phase or queue progress") {
		t.Fatalf("monitor error = %v, want a proved 60s queue wedge", err)
	}
	if observer.calls != 3 {
		t.Fatalf("observer calls = %d, want initial plus two 30s polls", observer.calls)
	}
}

func TestMonitorAcceptsEitherPhaseOrQueueProgress(t *testing.T) {
	observer := &scriptedObserver{snapshots: []turnSnapshot{
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/20/1/2"},
		{Phase: vocabulary.PhaseAdjudicating, Pending: 1, QueuePosition: "10/21/1/2"},
		{Phase: vocabulary.PhaseResolving, Pending: 1, QueuePosition: "11/21/1/2"},
		{Phase: vocabulary.PhaseComplete, Pending: 0, QueuePosition: "12/21/1/2"},
	}}

	if err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
		PollInterval: 30 * time.Second,
		StallAfter:   60 * time.Second,
		Wait:         immediateWait,
	}); err != nil {
		t.Fatalf("monitor rejected progressing turn: %v", err)
	}
}

func TestMonitorAbortsFailedAndUnreadableStateImmediately(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		observer := &scriptedObserver{snapshots: []turnSnapshot{{Phase: vocabulary.PhaseFailed}}}
		err := monitorTurn(t.Context(), observer, "turn-entity", monitorPolicy{
			PollInterval: 30 * time.Second, StallAfter: 60 * time.Second, Wait: immediateWait,
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
			PollInterval: 30 * time.Second, StallAfter: 60 * time.Second, Wait: immediateWait,
		})
		if err == nil || !strings.Contains(err.Error(), "queue unavailable") {
			t.Fatalf("monitor error = %v, want authoritative read failure", err)
		}
		if observer.calls != 0 {
			t.Fatalf("observer recorded %d successful calls, want 0", observer.calls)
		}
	})
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
