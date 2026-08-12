package graphio_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semmachina/internal/graphio"
)

func revisionConflict() error {
	return fmt.Errorf("record barrier: %w", &projection.MutationError{
		Operation: projection.MutationOperationReconcile,
		Kind:      projection.MutationRevisionConflict,
		Commit:    projection.CommitNotCommitted,
		Err:       errors.New("stale authority revision"),
	})
}

func TestRetryRevisionConflictRereadsUntilTheSiblingProjectionSettles(t *testing.T) {
	calls := 0
	err := graphio.RetryRevisionConflict(t.Context(), func() error {
		calls++
		if calls < 3 {
			return revisionConflict()
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("retry result = %v after %d calls, want success after 3", err, calls)
	}
}

func TestRetryRevisionConflictNeverRetriesUnknownCommitOrForever(t *testing.T) {
	t.Run("commit unknown", func(t *testing.T) {
		calls := 0
		unknown := &projection.MutationError{
			Operation: projection.MutationOperationReconcile,
			Kind:      projection.MutationCommitUnknown,
			Commit:    projection.CommitUnknown,
			Err:       errors.New("reply lost"),
		}
		err := graphio.RetryRevisionConflict(t.Context(), func() error {
			calls++
			return unknown
		})
		if !errors.Is(err, unknown) || calls != 1 {
			t.Fatalf("unknown-commit result = %v after %d calls, want the original error after 1", err, calls)
		}
	})

	t.Run("bounded conflict", func(t *testing.T) {
		calls := 0
		err := graphio.RetryRevisionConflict(t.Context(), func() error {
			calls++
			return revisionConflict()
		})
		if err == nil || calls != 4 {
			t.Fatalf("persistent-conflict result = %v after %d calls, want failure after 4", err, calls)
		}
	})
}
