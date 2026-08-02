package main

import (
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/playersocket"
)

func TestCompleteTurnWithoutWebSocketDeliveryKeepsTheSixtySecondBound(t *testing.T) {
	if stallTimeout != 60*time.Second {
		t.Fatalf("production post-complete delivery bound = %s, want 60s", stallTimeout)
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
