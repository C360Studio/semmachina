package resume

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
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

type loggingTurnStore struct{}

func (loggingTurnStore) EntitiesWithPrefix(
	context.Context, string, string, int,
) (graphio.PrefixPage, error) {
	return graphio.PrefixPage{}, nil
}

func (loggingTurnStore) MergeTriples(
	context.Context, string, []message.Triple, ...graphio.MergeOption,
) (*graph.EntityState, error) {
	return &graph.EntityState{}, nil
}

func TestAbandonLogsExcludeOpenDiagnostics(t *testing.T) {
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
			reconciler := &Reconciler{
				failer: loggingTurnFailer{}, details: test.store, attempts: 3,
				logger: slog.New(slog.NewJSONHandler(&logs, nil)),
			}
			report := &Reconciliation{}
			reconciler.abandon(t.Context(), "turn-42", &graph.EntityState{ID: "prefix.turn.turn-42"},
				vocabulary.PhaseAdjudicating, messageSecret, report)
			if report.Abandoned != 1 {
				t.Fatalf("report = %+v", report)
			}
			for _, forbidden := range []string{messageSecret, storeSecret, refSecret, "obj://", `"detail"`, `"why"`, `"error"`} {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatalf("logs leaked %q: %s", forbidden, logs.String())
				}
			}
		})
	}
}

func TestRecoverySightingLogExcludesOpenExplanation(t *testing.T) {
	const explanationSecret = "RECOVERY-EXPLANATION-SECRET-CANARY"
	var logs bytes.Buffer
	reconciler := &Reconciler{
		turns: loggingTurnStore{}, attempts: 3, now: func() time.Time { return time.Unix(1, 0) },
		logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	report := &Reconciliation{}
	reconciler.countTowardAbandonment(t.Context(), "turn-42", &graph.EntityState{
		ID: "c360.semmachina.world.template.turn.turn-42",
	},
		vocabulary.PhaseAdjudicating, explanationSecret, report)
	if report.Unadvanceable != 1 {
		t.Fatalf("report = %+v", report)
	}
	if strings.Contains(logs.String(), explanationSecret) || strings.Contains(logs.String(), `"why"`) {
		t.Fatalf("logs leaked recovery explanation: %s", logs.String())
	}
}
