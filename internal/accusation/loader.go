package accusation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// IntegrityError marks malformed or foreign durable state. Redelivery cannot
// repair it and callers must close the turn without exposing private detail.
type IntegrityError struct{ err error }

func (e *IntegrityError) Error() string { return e.err.Error() }
func (e *IntegrityError) Unwrap() error { return e.err }

func integrity(format string, args ...any) error {
	return &IntegrityError{err: fmt.Errorf(format, args...)}
}

// MissingTriggeredTurnError identifies a delivery whose named turn no longer
// exists. There is no entity on which to record a failure, so consumers must
// terminate that delivery without calling the turn recorder.
type MissingTriggeredTurnError struct {
	TurnEntityID string
	Err          error
}

func (e *MissingTriggeredTurnError) Error() string {
	return fmt.Sprintf("accusation trigger turn %s is missing: %v", e.TurnEntityID, e.Err)
}

func (e *MissingTriggeredTurnError) Unwrap() error { return e.Err }

// ReadGraph is the accusation preflight's authoritative graph surface.
type ReadGraph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
}

// DecisionStore resolves the private casekeeper artifact.
type DecisionStore interface {
	GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error)
}

// SolutionProjector is the sole authorized canonical-solution boundary.
type SolutionProjector interface {
	Project(context.Context, epistemic.AuthenticatedAudience) (*epistemic.Projection, error)
}

// Preflight is a complete, identity-rebound verification snapshot.
type Preflight struct {
	TurnEntityID string
	TurnID       string
	Applicable   bool
	CaseID       string
	CasePhase    vocabulary.CasePhase
	Decision     *payload.CaseDecision
	Solution     epistemic.Solution
}

// Loader resolves and validates all inputs before any accusation write.
type Loader struct {
	graph     ReadGraph
	decisions DecisionStore
	projector SolutionProjector
	caseID    string
}

// NewLoader builds a preflight loader pinned to the authored case.
func NewLoader(graphReader ReadGraph, decisions DecisionStore, projector SolutionProjector, caseID string) (*Loader, error) {
	if graphReader == nil || decisions == nil {
		return nil, errors.New("accusation loader requires graph and decision store")
	}
	if strings.TrimSpace(caseID) != "" && projector == nil {
		return nil, errors.New("scoped accusation loader requires an epistemic projector")
	}
	return &Loader{graph: graphReader, decisions: decisions, projector: projector, caseID: caseID}, nil
}

// Load binds the trigger, reference slot, record, decision, and case identities
// before requesting the verifier-only canonical solution projection.
func (l *Loader) Load(ctx context.Context, turnEntityID string) (Preflight, error) {
	parts := strings.Split(turnEntityID, ".")
	if len(parts) != 6 || parts[4] != "turn" {
		return Preflight{}, integrity("accusation trigger names non-turn entity %q", turnEntityID)
	}
	turnID := parts[5]
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return Preflight{}, integrity("accusation trigger identity: %v", err)
	}
	turnState, err := l.graph.GetEntity(ctx, turnEntityID)
	if err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return Preflight{}, &MissingTriggeredTurnError{TurnEntityID: turnEntityID, Err: err}
		}
		return Preflight{}, fmt.Errorf("read accusation turn: %w", err)
	}
	refText, err := soleObject(turnState, vocabulary.TurnCaseDecisionRef)
	if err != nil {
		return Preflight{}, integrity("accusation decision reference: %v", err)
	}
	ref, err := content.ParseRef(refText)
	if err != nil {
		return Preflight{}, integrity("accusation decision reference: %v", err)
	}
	expectedKey, err := content.KeyFor(vocabulary.TurnCaseDecisionRef, content.SubjectTurn, turnID)
	if err != nil || ref.Key != expectedKey {
		return Preflight{}, integrity("decision reference %s is not the triggered turn's deterministic slot", ref)
	}
	record, err := l.decisions.GetCaseDecisionRecord(ctx, ref)
	if err != nil {
		if permanentArtifactError(err) {
			return Preflight{}, integrity("read accusation decision: %v", err)
		}
		return Preflight{}, fmt.Errorf("read accusation decision: %w", err)
	}
	if record == nil {
		return Preflight{}, integrity("decision store returned nil record")
	}
	if err := record.Validate(); err != nil {
		return Preflight{}, integrity("invalid accusation decision record: %v", err)
	}
	if record.TurnID != turnID {
		return Preflight{}, integrity("decision record belongs to a foreign turn")
	}
	if record.Status == payload.CaseDecisionStatusNotApplicable {
		return Preflight{TurnEntityID: turnEntityID, TurnID: turnID}, nil
	}
	if record.Decision == nil || record.Decision.TurnID != turnID {
		return Preflight{}, integrity("decision record belongs to a foreign turn")
	}
	decision := record.Decision
	if record.ActionID != decision.ActionID {
		return Preflight{}, integrity("decision record and decision name different actions")
	}
	if decision.Kind != payload.CaseDecisionAccuse {
		return Preflight{TurnEntityID: turnEntityID, TurnID: turnID, Decision: decision}, nil
	}
	if l.caseID == "" || l.projector == nil || decision.CaseID != l.caseID {
		return Preflight{}, integrity("accusation decision belongs to a foreign or unscoped case")
	}
	caseState, err := l.graph.GetEntity(ctx, l.caseID)
	if err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return Preflight{}, integrity("accusation case is missing: %v", err)
		}
		return Preflight{}, fmt.Errorf("read accusation case: %w", err)
	}
	phaseText, err := soleObject(caseState, vocabulary.CaseLifecyclePhase)
	if err != nil {
		return Preflight{}, integrity("accusation case phase: %v", err)
	}
	phase, err := vocabulary.ParseCasePhase(phaseText)
	if err != nil {
		return Preflight{}, integrity("accusation case phase: %v", err)
	}
	projection, err := l.projector.Project(ctx, epistemic.VerifierAudience(l.caseID))
	if err != nil {
		return Preflight{}, fmt.Errorf("project canonical accusation solution: %w", err)
	}
	if projection == nil || projection.Purpose != epistemic.PurposeVerifier || !projection.HasSolution {
		return Preflight{}, integrity("verifier projection returned no canonical solution")
	}
	return Preflight{TurnEntityID: turnEntityID, TurnID: turnID, Applicable: true, CaseID: l.caseID,
		CasePhase: phase, Decision: decision, Solution: projection.Solution}, nil
}

func permanentArtifactError(err error) bool {
	return errors.Is(err, content.ErrArtifactNotFound) ||
		errors.Is(err, content.ErrArtifactReference) ||
		errors.Is(err, content.ErrArtifactCorrupt)
}

func soleObject(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	if state == nil {
		return "", errors.New("nil entity state")
	}
	var values []string
	for _, triple := range state.Triples {
		if triple.Subject == state.ID && triple.Predicate == predicate.String() {
			value, ok := triple.Object.(string)
			if !ok {
				return "", fmt.Errorf("%s is not a string", predicate)
			}
			values = append(values, value)
		}
	}
	if len(values) != 1 {
		return "", fmt.Errorf("entity %s holds %d values for %s; want exactly one", state.ID, len(values), predicate)
	}
	return values[0], nil
}
