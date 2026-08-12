package graphio

import (
	"context"
	"errors"

	"github.com/c360studio/semstreams/pkg/projection"
)

// revisionConflictAttempts bounds immediate retries of a definitely
// not-committed revision race. Four attempts cover the turn fan-out's sibling
// projections without turning a persistently contested write into a busy loop.
const revisionConflictAttempts = 4

// RetryRevisionConflict reruns an operation only when beta.160 proves that its
// previous mutation was rejected at a stale authority revision. The callback
// must perform the authoritative reread and recompute its mutation on every
// invocation; projection.MutationClient.Reconcile does that for each call.
//
// Commit-unknown and every other error return immediately: retrying those could
// duplicate an operation whose commitment is not known.
func RetryRevisionConflict(ctx context.Context, operation func() error) error {
	var last error
	for attempt := 0; attempt < revisionConflictAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = operation()
		if last == nil || !isRevisionConflict(last) {
			return last
		}
	}
	return last
}

func isRevisionConflict(err error) bool {
	var mutationErr *projection.MutationError
	return errors.As(err, &mutationErr) && mutationErr.Kind == projection.MutationRevisionConflict &&
		mutationErr.Commit == projection.CommitNotCommitted
}
