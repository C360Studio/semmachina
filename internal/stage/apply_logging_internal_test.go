package stage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type loggingDetailStore struct {
	ref content.Ref
	err error
}

func (s loggingDetailStore) PutFailureDetail(
	context.Context, string, *content.FailureDetail,
) (content.Ref, error) {
	return s.ref, s.err
}

type loggingTurnFailer struct{}

func (loggingTurnFailer) Fail(
	context.Context, string, string, vocabulary.FailureReason, content.Ref,
) (turn.Transition, error) {
	return turn.Transition{Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced}, nil
}

func TestEffectorFailureLogsExcludeOpenDiagnostics(t *testing.T) {
	const (
		messageSecret = "MESSAGE-SECRET-CANARY"
		storeSecret   = "STORE-SECRET-CANARY"
		refSecret     = "REF-SECRET-CANARY"
	)
	for _, test := range []struct {
		name  string
		store loggingDetailStore
	}{
		{name: "stored", store: loggingDetailStore{ref: content.Ref{
			Instance: refSecret, Key: "turn/turn-42/failure",
		}}},
		{name: "store failure", store: loggingDetailStore{err: errors.New(storeSecret)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			effector := &Effector{
				failer: loggingTurnFailer{}, details: test.store,
				logger: slog.New(slog.NewJSONHandler(&logs, nil)),
			}
			err := effector.refuse(t.Context(), Trigger{TurnID: "turn-42", TurnEntityID: "prefix.turn.turn-42"},
				&effect.RejectionError{Intent: -1, Code: vocabulary.FailureEffectInvalid, Err: errors.New(messageSecret)})
			if err != nil {
				t.Fatalf("refuse: %v", err)
			}
			for _, forbidden := range []string{messageSecret, storeSecret, refSecret, "message", `"error"`} {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatalf("logs leaked %q: %s", forbidden, logs.String())
				}
			}
		})
	}
}
