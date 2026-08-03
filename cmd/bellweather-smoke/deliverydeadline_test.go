package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/playersocket"
)

func TestCompleteTurnWithoutWebSocketDeliveryKeepsTheSixtySecondBound(t *testing.T) {
	if terminalDeliveryTimeout != 60*time.Second {
		t.Fatalf("production post-complete delivery bound = %s, want 60s", terminalDeliveryTimeout)
	}
	frames := make(chan *playersocket.Frame)
	socketErrors := make(chan error)
	monitorDone := make(chan error, 1)
	monitorDone <- nil

	_, err := awaitTerminalDelivery(
		t.Context(), frames, socketErrors, monitorDone, "turn-act-hint", nil, boot.Config{}, time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "terminal WebSocket delivery") {
		t.Fatalf("delivery wait error = %v, want bounded post-complete egress failure", err)
	}
}

func TestPaidAcceptanceCapsAreDistinctAndBounded(t *testing.T) {
	if turnPaidCap != 180*time.Second {
		t.Fatalf("per-turn paid cap = %s, want 180s", turnPaidCap)
	}
	if smokePaidCap != 390*time.Second {
		t.Fatalf("whole-smoke paid cap = %s, want 390s", smokePaidCap)
	}
	if errors.Is(errTurnPaidCap, errSmokePaidCap) {
		t.Fatal("per-turn and whole-smoke paid caps must remain distinguishable")
	}
}

func TestPaidContextsCarryIndependentWholeSmokeAndPerTurnDeadlines(t *testing.T) {
	started := time.Now()
	smokeCtx, cancelSmoke := paidSmokeContext()
	defer cancelSmoke()
	assertDeadlineNear(smokeCtx, t, started.Add(smokePaidCap))

	firstCtx, cancelFirst := paidTurnContext(smokeCtx)
	assertDeadlineNear(firstCtx, t, time.Now().Add(turnPaidCap))
	cancelFirst()

	secondStarted := time.Now()
	secondCtx, cancelSecond := paidTurnContext(smokeCtx)
	defer cancelSecond()
	assertDeadlineNear(secondCtx, t, secondStarted.Add(turnPaidCap))
}

func assertDeadlineNear(ctx context.Context, t *testing.T, want time.Time) {
	t.Helper()
	got, ok := ctx.Deadline()
	if !ok {
		t.Fatal("paid context carries no deadline")
	}
	if delta := got.Sub(want); delta < -time.Second || delta > time.Second {
		t.Fatalf("paid context deadline = %s, want within one second of %s", got, want)
	}
}

func TestTerminalWaitReportsTheExactPaidCapThatCancelledIt(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		want  string
	}{
		{"turn", errTurnPaidCap, "paid per-turn cap of 3m0s reached before terminal delivery"},
		{"smoke", errSmokePaidCap, "paid whole-smoke cap of 6m30s reached before terminal delivery"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(test.cause)
			_, err := awaitTerminalDelivery(
				ctx, make(chan *playersocket.Frame), make(chan error), make(chan error),
				"turn-act-hint", nil, boot.Config{}, time.Second,
			)
			if !errors.Is(err, test.cause) || err.Error() != test.want {
				t.Fatalf("terminal wait error = %v, want exact %q and matching cause", err, test.want)
			}
		})
	}
}

func TestTerminalWaitPaidCapWinsWhenMonitorErrorIsAlsoReady(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errTurnPaidCap)
	monitorDone := make(chan error, 1)
	monitorDone <- errors.New("incidental monitor cancellation")

	_, err := awaitTerminalDelivery(
		ctx, make(chan *playersocket.Frame), make(chan error), monitorDone,
		"turn-act-hint", nil, boot.Config{}, time.Second,
	)
	if !errors.Is(err, errTurnPaidCap) {
		t.Fatalf("competing terminal wait error = %v, want paid cap to win", err)
	}
}
