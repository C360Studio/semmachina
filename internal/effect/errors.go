package effect

import (
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// coded is the interface a failure implements when it carries a closed reason
// code the stage can record if the failure is terminal.
//
// The code, not the message, is the contract. A CommitError also carries the
// cause's handling class: transient causes retry without recording this code;
// invalid or fatal ones end the turn with it. When a reason lands on the turn
// entity, the shared triple projection gates an object's shape but not its
// closure — so explanatory detail belongs behind a reference.
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
// per-entity, so a batch touching several entities is several independent
// writes and any response can fail after its mutation landed. Target and
// Committed therefore describe RESPONSE knowledge, not an assertion about
// storage: Committed writes returned success; Target is the first write whose
// outcome could not be confirmed. Re-application must include both classes.
type CommitError struct {
	// BatchID is the batch identity derived from the turn.
	BatchID string
	// Target is the entity whose write did not return success. Its mutation may
	// already have landed before the response failed.
	Target string
	// Committed are the entities whose earlier writes returned success, in
	// commit order. They are a confirmed lower bound, not the complete set of
	// mutations that may have landed.
	Committed []string
	// Err is the transport or handler failure.
	Err error
}

// Error implements error.
func (e *CommitError) Error() string {
	return fmt.Sprintf(
		"effect batch %s confirmed %d target write(s) %v and then could not confirm writing %s: %v; "+
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
// It is the single call the turn's phase management makes after deciding a
// failure is terminal, so the mapping from failure to recorded code lives with
// the failures rather than at the call site. A transient CommitError is coded
// for diagnostics but is retried before this mapping is recorded.
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
