package caseflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ReceiptSource identifies structural case-event receipts in graph history.
const ReceiptSource = "caseflow-receipt"

// Store is the graph surface needed to read a case and submit one replace
// update containing all four receipt predicates.
type Store interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	MergeTriples(context.Context, string, []message.Triple, ...graphio.MergeOption) (*graph.EntityState, error)
}

var _ Store = (*graphio.Store)(nil)

// TransitionRequest records a durable event receipt whose closed kind maps to
// one built-in lifecycle-transition rule.
type TransitionRequest struct {
	CaseEntityID string
	EventID      string
	Kind         vocabulary.CaseLifecycleEventKind
}

// ReceiptOutcome describes whether this call recorded new structural evidence.
type ReceiptOutcome struct {
	Recorded bool
	From     vocabulary.CasePhase
	To       vocabulary.CasePhase
}

// Recorder projects component-owned events onto the case entity. It never
// writes case.lifecycle.phase; the lifecycle Manager is the sole phase writer.
// Record's duplicate check is a read followed by a merge, not a CAS condition,
// so this seam does not claim exactly-once behavior under concurrent delivery.
type Recorder struct {
	store Store
	now   func() time.Time
}

// NewRecorder constructs the structural event receipt seam over graph state.
func NewRecorder(store Store) (*Recorder, error) {
	if store == nil {
		return nil, errors.New("caseflow recorder requires a graph store")
	}
	return &Recorder{store: store, now: time.Now}, nil
}

// Record validates ordering and puts event id, kind, from, and to into one graph
// replace update. Those four fields cannot land as separate partial writes, but
// the preceding read is not CAS-bound to the update: concurrent duplicate
// deliveries can both pass the local check. Durable production deduplication
// remains task 2.3 with the groups 4 and 6 delivery paths.
func (r *Recorder) Record(ctx context.Context, request TransitionRequest) (ReceiptOutcome, error) {
	if request.CaseEntityID == "" {
		return ReceiptOutcome{}, errors.New("caseflow transition request requires a case entity id")
	}
	if request.EventID == "" {
		return ReceiptOutcome{}, errors.New("caseflow transition request requires a nonempty event id")
	}
	from, to, ok := edgeFor(request.Kind)
	if !ok {
		_, parseErr := vocabulary.ParseCaseLifecycleEventKind(string(request.Kind))
		return ReceiptOutcome{}, fmt.Errorf("caseflow transition request event kind: %w", parseErr)
	}
	state, err := r.store.GetEntity(ctx, request.CaseEntityID)
	if err != nil {
		return ReceiptOutcome{}, fmt.Errorf("read case %s before recording event %s: %w",
			request.CaseEntityID, request.EventID, err)
	}
	if literal(state, vocabulary.CaseLifecycleEventID) == request.EventID {
		return ReceiptOutcome{From: from, To: to}, nil
	}
	currentText := literal(state, vocabulary.CaseLifecyclePhase)
	current, err := vocabulary.ParseCasePhase(currentText)
	if err != nil {
		return ReceiptOutcome{}, fmt.Errorf("case %s current lifecycle phase: %w", request.CaseEntityID, err)
	}
	currentRank := phaseRank(current)
	fromRank := phaseRank(from)
	if currentRank > fromRank {
		return ReceiptOutcome{From: from, To: to}, nil
	}
	if currentRank < fromRank {
		return ReceiptOutcome{}, fmt.Errorf(
			"case %s event %s (%s) is out of order: current phase %s, required phase %s",
			request.CaseEntityID, request.EventID, request.Kind, current, from)
	}

	at := r.now().UTC()
	triples := []message.Triple{
		receiptTriple(request.CaseEntityID, vocabulary.CaseLifecycleEventID, request.EventID, at),
		receiptTriple(request.CaseEntityID, vocabulary.CaseLifecycleEventKindPredicate, string(request.Kind), at),
		receiptTriple(request.CaseEntityID, vocabulary.CaseLifecycleFromPhase, string(from), at),
		receiptTriple(request.CaseEntityID, vocabulary.CaseLifecycleToPhase, string(to), at),
	}
	if _, err := r.store.MergeTriples(ctx, request.CaseEntityID, triples); err != nil {
		return ReceiptOutcome{}, fmt.Errorf("record case event %s on %s: %w",
			request.EventID, request.CaseEntityID, err)
	}
	return ReceiptOutcome{Recorded: true, From: from, To: to}, nil
}

func edgeFor(kind vocabulary.CaseLifecycleEventKind) (vocabulary.CasePhase, vocabulary.CasePhase, bool) {
	switch kind {
	case vocabulary.CaseEventBodyObserved:
		return vocabulary.CasePhaseColdOpen, vocabulary.CasePhaseDiscovery, true
	case vocabulary.CaseEventInvestigationStarted:
		return vocabulary.CasePhaseDiscovery, vocabulary.CasePhaseInvestigation, true
	case vocabulary.CaseEventAccusationSubmitted:
		return vocabulary.CasePhaseInvestigation, vocabulary.CasePhaseAccusation, true
	case vocabulary.CaseEventAccusationCorrect:
		return vocabulary.CasePhaseAccusation, vocabulary.CasePhaseDenouement, true
	default:
		return "", "", false
	}
}

func phaseRank(phase vocabulary.CasePhase) int {
	for index, candidate := range vocabulary.CasePhases() {
		if candidate == phase {
			return index
		}
	}
	return -1
}

func literal(state *graph.EntityState, predicate vocabulary.Predicate) string {
	for _, triple := range state.Triples {
		if triple.Subject == state.ID && triple.Predicate == predicate.String() {
			value, _ := triple.Object.(string)
			return value
		}
	}
	return ""
}

func receiptTriple(subject string, predicate vocabulary.Predicate, object string, at time.Time) message.Triple {
	return message.Triple{
		Subject: subject, Predicate: predicate.String(), Object: object,
		Source: ReceiptSource, Timestamp: at, Confidence: 1,
	}
}
