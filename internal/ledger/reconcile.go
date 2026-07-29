package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// DefaultPageLimit is how many turn entities one reconciliation page asks for.
// Zero would take the server's own default; asking explicitly means the page
// size is a decision rather than an inheritance.
const DefaultPageLimit = 200

// maxReconcilePages bounds the paging loop.
//
// A cursor that stops advancing would otherwise spin forever against a live
// broker, and "the boot hung" is the worst diagnosis in the catalogue. At the
// default page size this admits two hundred thousand turns before it refuses,
// which is far past anything an MVP campaign reaches and far short of forever.
const maxReconcilePages = 1000

// TurnPrefixForCampaign returns the five-part entity-ID prefix every turn in
// one world instance shares.
//
// Derived from the campaign entity rather than configured separately, and that
// is not a convenience. The writer already validates the campaign id and stamps
// it on every manifest; deriving the scan prefix from the same value makes it
// impossible to reconcile one world's turns into another world's ledger, which
// a second configuration field would make merely unlikely.
//
// The arithmetic itself is vocabulary's, because the stranded-turn pass
// enumerates the same set for a different reason and two copies of "which
// world's turns" is exactly the kind of duplication that ends up answering the
// question differently on one of the two boot paths.
func TurnPrefixForCampaign(campaignID string) (string, error) {
	return vocabulary.SiblingTypePrefix(campaignID, TurnTypeSegment)
}

// TurnFailure is one turn the reconciliation could not archive.
type TurnFailure struct {
	// TurnEntityID names the turn.
	TurnEntityID string
	// Err is why it could not be archived.
	Err error
}

// Reconciliation is what one reconciliation pass found.
type Reconciliation struct {
	// Scanned is how many turn entities were examined.
	Scanned int
	// Archived is how many terminal turns this pass added to the ledger — the
	// count that should be zero on a healthy restart and is not on the boot
	// after a lost notification.
	Archived int
	// AlreadyArchived is how many terminal turns were already in the ledger.
	AlreadyArchived int
	// InFlight is how many turns had not resolved yet. They are not the
	// ledger's business: the notification (or a later reconciliation) archives
	// them when they end.
	InFlight int
	// Failures names every turn that could not be archived, with the reason.
	Failures []TurnFailure
}

// Reconcile archives every terminal turn in ENTITY_STATES that the ledger has
// no record of.
//
// WHY THIS EXISTS, because it looks like belt-and-braces over a working
// notification and is not. The two rules that publish `semmachina.turn.resolved`
// use the rule engine's `transition` operator, whose previous value is read from
// MatchState.FieldValues in the RULE_STATE KV bucket — NOT from the graph. A
// transition condition with no recorded previous value returns false
// unconditionally, so a turn that reaches `complete` while RULE_STATE holds
// nothing for it never publishes the notification at all. A cleared or fresh
// RULE_STATE is explicitly permitted pre-v1, and bootstrap replay does not
// rescue it either: the durable stale-replay guard skips an evaluation whose
// source revision it has already recorded, which is exactly a turn nobody has
// written to since. (Measured, not inferred — see internal/resume.) Without this
// pass, those turns are simply absent from the campaign's history and nothing
// anywhere reports it.
//
// So the notification is the FAST PATH and ENTITY_STATES is the source of
// truth — which is the right way round anyway: the graph is where a turn's
// terminal phase is a fact, and the notification is only a message about it.
// The two converge without coordination because Append's duplicate guard is
// durable: whichever arrives second reads the subject, finds an identical
// manifest, and writes nothing.
//
// The cost is honest and stated: one prefix page per DefaultPageLimit turns,
// plus one archive read per terminal turn, at every boot. That is O(campaign
// history) and it is accepted at MVP scale rather than optimised away, because
// every cheaper shape trades correctness for it — a "since last boot" watermark
// cannot be derived from turn ids, which carry no order, and reading the whole
// archived subject list in one shot swaps N round trips for a single reply that
// eventually exceeds NATS's max payload and fails outright.
func (w *Writer) Reconcile(ctx context.Context) (Reconciliation, error) {
	var (
		report Reconciliation
		cursor string
	)
	for page := 0; ; page++ {
		if page >= maxReconcilePages {
			return report, fmt.Errorf(
				"reconciling the campaign ledger read %d pages of turns without exhausting the cursor; "+
					"treating that as a non-advancing cursor rather than hanging the boot", page)
		}
		result, err := w.turns.EntitiesWithPrefix(ctx, w.turnPrefix, cursor, w.pageLimit)
		if err != nil {
			return report, fmt.Errorf("enumerate turns under %s: %w", w.turnPrefix, err)
		}
		for idx := range result.Entities {
			w.reconcileTurn(ctx, &result.Entities[idx], &report)
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor {
			return report, fmt.Errorf(
				"the turn enumeration returned the same cursor twice under %s; the scan is not advancing",
				w.turnPrefix)
		}
		cursor = result.NextCursor
	}

	if len(report.Failures) == 0 {
		return report, nil
	}
	errs := make([]error, 0, len(report.Failures))
	for _, failure := range report.Failures {
		errs = append(errs, fmt.Errorf("turn %s: %w", failure.TurnEntityID, failure.Err))
	}
	return report, errors.Join(errs...)
}

// reconcileTurn archives one enumerated turn if it has resolved.
//
// A failure is COLLECTED rather than returned, so one corrupt turn record does
// not stop the pass from archiving every other turn behind it. That matters more
// here than anywhere else in the loop: this pass exists because a turn can be
// missing from the archive, and aborting halfway would leave a different set of
// turns missing for a different reason.
func (w *Writer) reconcileTurn(ctx context.Context, state *graph.EntityState, report *Reconciliation) {
	report.Scanned++

	// A stub is queryable and factless. It is not a failure — nothing has gone
	// wrong when an entity is referenced before it is born — but it has no phase
	// and therefore nothing to archive.
	if state.IsStub() {
		report.InFlight++
		return
	}

	turnID, err := turnIDOf(state.ID)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	phase, err := recordedPhase(state)
	if err != nil {
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	if !phase.IsTerminal() {
		report.InFlight++
		return
	}

	outcome, err := w.archiveTurn(ctx, turnID, state)
	if err != nil {
		w.logger.Error("a resolved turn could not be reconciled into the campaign ledger",
			"turn", state.ID, "phase", phase, "error", err)
		report.Failures = append(report.Failures, TurnFailure{TurnEntityID: state.ID, Err: err})
		return
	}
	if outcome.Appended {
		// Loud on purpose. Reaching here means the notification for this turn
		// never arrived, which is a fact about the rule engine's state and not
		// about this turn.
		w.logger.Warn("archived a resolved turn the ledger had no record of; its resolved notification never arrived",
			"turn", state.ID, "phase", phase)
		report.Archived++
		return
	}
	report.AlreadyArchived++
}

// recordedPhase reads a turn's phase without demanding that it be terminal.
//
// Compose refuses a non-terminal turn, which is right for archiving and wrong
// for scanning: the reconciliation walks every turn the world has ever had and
// most of the interesting ones are finished, but an in-flight turn is an
// ordinary thing to meet and not an error to report.
func recordedPhase(state *graph.EntityState) (vocabulary.TurnPhase, error) {
	triple, err := soleTriple(state, vocabulary.TurnPhaseCurrent)
	if err != nil {
		return "", err
	}
	if triple == nil {
		return "", fmt.Errorf(
			"turn entity %s carries no %s; a turn without a phase can be neither resumed nor archived",
			state.ID, vocabulary.TurnPhaseCurrent)
	}
	value, ok := triple.Object.(string)
	if !ok {
		return "", fmt.Errorf("turn entity %s records a %T phase, want a string", state.ID, triple.Object)
	}
	phase, err := vocabulary.ParseTurnPhase(value)
	if err != nil {
		return "", fmt.Errorf("turn entity %s: %w", state.ID, err)
	}
	return phase, nil
}
