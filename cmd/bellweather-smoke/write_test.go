package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type deadlineWriter struct {
	deadlines []time.Time
	message   []byte
	typeCode  int
	setErr    error
	writeErr  error
	onWrite   func()
}

func (w *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	if len(w.deadlines) == 1 {
		return w.setErr
	}
	return nil
}

func (w *deadlineWriter) WriteMessage(typeCode int, message []byte) error {
	w.typeCode = typeCode
	w.message = append([]byte(nil), message...)
	if w.onWrite != nil {
		w.onWrite()
	}
	return w.writeErr
}

func TestTurnWriteUsesAndClearsTheContextDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	writer := &deadlineWriter{}

	if err := writeTurnRequest(ctx, writer, []byte(`{"fixed":true}`)); err != nil {
		t.Fatalf("write turn request: %v", err)
	}
	if len(writer.deadlines) != 2 || !writer.deadlines[0].Equal(deadline) || !writer.deadlines[1].IsZero() {
		t.Fatalf("write deadlines = %v, want context deadline then cleared deadline", writer.deadlines)
	}
	if writer.typeCode != websocket.TextMessage || string(writer.message) != `{"fixed":true}` {
		t.Fatalf("written frame = type %d body %q", writer.typeCode, writer.message)
	}
}

func TestTurnWriteClearsDeadlineAfterWriteFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	writer := &deadlineWriter{writeErr: errors.New("write refused")}

	err := writeTurnRequest(ctx, writer, []byte(`{}`))
	if err == nil || len(writer.deadlines) != 2 || !writer.deadlines[1].IsZero() {
		t.Fatalf("failed write error/deadlines = %v/%v, want failure and cleared deadline", err, writer.deadlines)
	}
}

func TestTurnWritePreservesPaidCapAndStillClearsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	writer := &deadlineWriter{
		writeErr: errors.New("incidental write timeout"),
		onWrite: func() {
			cancel(errTurnPaidCap)
		},
	}
	deadlineCtx, cancelDeadline := context.WithDeadline(ctx, time.Now().Add(time.Minute))
	defer cancelDeadline()

	err := writeTurnRequest(deadlineCtx, writer, []byte(`{}`))
	if !errors.Is(err, errTurnPaidCap) {
		t.Fatalf("cancelled write error = %v, want preserved per-turn paid cap", err)
	}
	if len(writer.deadlines) != 2 || !writer.deadlines[1].IsZero() {
		t.Fatalf("cancelled write deadlines = %v, want cleared deadline", writer.deadlines)
	}
}
