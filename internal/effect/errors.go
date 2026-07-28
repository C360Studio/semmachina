package effect

import (
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// coded is the interface a failure implements when it carries a closed reason
// code for the turn's recorded failure.
//
// The code, not the message, is the contract. The failure reason lands on the
// turn entity where rules match, and the shared triple projection gates an
// object's shape but not its closure — so a sentence would pass every gate and
// put free text on the rule-matching surface. Explanatory detail belongs behind
// a reference; the graph gets the code.
type coded interface {
	error
	// Reason returns the closed failure code for this failure.
	Reason() vocabulary.FailureReason
}

// RejectionError reports an intent the applier refused. Rejection is a normal,
// tested path — the turn fails explicitly with a reason — not an exception.
type RejectionError struct {
	// Intent is the index of the offending intent in the batch, or -1 when the
	// fault is the batch's as a whole (two values for one single-valued fact).
	Intent int
	// Type and Target identify the offending intent for a reader who has the
	// error and not the batch.
	Type   vocabulary.EffectType
	Target string
	// Code is the closed reason recorded on the turn entity.
	Code vocabulary.FailureReason
	// Err is the specific violation.
	Err error
}

// Error implements error.
func (e *RejectionError) Error() string {
	if e.Intent < 0 {
		return fmt.Sprintf("effect batch rejected (%s): %v", e.Code, e.Err)
	}
	return fmt.Sprintf("effect intent %d (%s on %s) rejected (%s): %v",
		e.Intent, e.Type, e.Target, e.Code, e.Err)
}

// Unwrap exposes the underlying violation.
func (e *RejectionError) Unwrap() error { return e.Err }

// Reason implements coded.
func (e *RejectionError) Reason() vocabulary.FailureReason { return e.Code }

// CommitError reports a batch whose per-entity writes partially landed.
//
// It exists because the transport cannot promise otherwise: the merge lane is
// per-entity and its response carries no failed-subject list, so a batch
// touching several entities is several independent writes and any of them can
// fail after its predecessors committed. Naming what landed and what did not is
// the difference between a recoverable turn and a world nobody can explain.
type CommitError struct {
	// BatchID is the batch identity derived from the turn.
	BatchID string
	// Target is the entity whose write did not land.
	Target string
	// Committed are the entities written before the failure, in commit order.
	// Re-application under the same batch identity converges them, since every
	// write replaces rather than accumulates.
	Committed []string
	// Err is the transport or handler failure.
	Err error
}

// Error implements error.
func (e *CommitError) Error() string {
	return fmt.Sprintf(
		"effect batch %s committed %d target(s) %v and then failed writing %s: %v; "+
			"the batch is not applied, and recovery is re-application under the same batch identity",
		e.BatchID, len(e.Committed), e.Committed, e.Target, e.Err)
}

// Unwrap exposes the underlying failure.
func (e *CommitError) Unwrap() error { return e.Err }

// Reason implements coded.
func (e *CommitError) Reason() vocabulary.FailureReason {
	return vocabulary.FailureEffectCommitIncomplete
}

// FailureReasonFor returns the closed failure code an error carries, if any.
//
// It is the single call the turn's phase management makes to turn an applier
// failure into the code recorded on the turn entity, so the mapping from
// failure to code lives with the failures rather than at the call site.
//
// # The false branch is a real answer, not a gap
//
// Apply returns UNCODED errors, deliberately, and a caller must handle them as
// a different class rather than as an unclassified turn failure. Two kinds land
// here:
//
//   - Turn-record anomalies: a turn holding two batch identities, a half-written
//     marker, a record naming a foreign batch or a foreign payload reference, a
//     turn entity that is only a referential stub. These say the turn's own
//     record is not trustworthy — and recording a failure reason is a WRITE to
//     that same record. Marking it `failed` would state a game outcome on
//     evidence that the paperwork is corrupt, and could contradict a world the
//     batch may already have changed.
//   - Transport failures reading the turn. A NATS timeout is retryable and says
//     nothing about the turn; coding it would burn a player's turn for a broken
//     connection.
//
// So false means "this is not a turn-failure classification: escalate or retry,
// do not record it on the turn". A code for either class would put the applier
// in the position of writing a verdict onto the entity whose readability it just
// disputed. If a future failure genuinely IS a turn outcome, give it a
// vocabulary.FailureReason and a coded error — never widen this fallback.
func FailureReasonFor(err error) (vocabulary.FailureReason, bool) {
	var carrier coded
	if errors.As(err, &carrier) {
		return carrier.Reason(), true
	}
	return "", false
}
