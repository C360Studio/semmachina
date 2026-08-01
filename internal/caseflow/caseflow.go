// Package caseflow owns mystery-case lifecycle declarations and event receipts.
package caseflow

import (
	"reflect"

	"github.com/c360studio/semstreams/pkg/lifecycle"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// WorkflowName is the stable lifecycle registration key for mystery cases.
const WorkflowName = "mystery-case"

// CaseState is the lifecycle projection of a case graph entity.
type CaseState struct {
	ID           string               `json:"id" lifecycle:"id"`
	CurrentPhase vocabulary.CasePhase `json:"phase" lifecycle:"phase,predicate=case.lifecycle.phase"`
}

// EntityID returns the case's federated graph identity.
func (s *CaseState) EntityID() string { return s.ID }

// Workflow returns the stable mystery-case workflow name.
func (*CaseState) Workflow() string { return WorkflowName }

// Phase returns the current durable case phase.
func (s *CaseState) Phase() string { return string(s.CurrentPhase) }

// IsTerminal reports whether the case has entered denouement.
func (s *CaseState) IsTerminal() bool { return s.CurrentPhase == vocabulary.CasePhaseDenouement }

// ParentEntityID returns empty because a case is a root workflow.
func (*CaseState) ParentEntityID() string { return "" }

// Workflow declares the exact linear case lifecycle. No field is operator-writable.
func Workflow() lifecycle.Workflow {
	return lifecycle.Workflow{
		Name:            WorkflowName,
		EntityIDPattern: "*.semmachina.*.*.case.*",
		Phases: []string{
			string(vocabulary.CasePhaseColdOpen),
			string(vocabulary.CasePhaseDiscovery),
			string(vocabulary.CasePhaseInvestigation),
			string(vocabulary.CasePhaseAccusation),
			string(vocabulary.CasePhaseDenouement),
		},
		Transitions: lifecycle.Transitions{
			string(vocabulary.CasePhaseColdOpen):      {string(vocabulary.CasePhaseDiscovery)},
			string(vocabulary.CasePhaseDiscovery):     {string(vocabulary.CasePhaseInvestigation)},
			string(vocabulary.CasePhaseInvestigation): {string(vocabulary.CasePhaseAccusation)},
			string(vocabulary.CasePhaseAccusation):    {string(vocabulary.CasePhaseDenouement)},
			string(vocabulary.CasePhaseDenouement):    {},
		},
		PhasePredicate: vocabulary.CaseLifecyclePhase.String(),
		Schema:         reflect.TypeOf(CaseState{}),
		AuditPredicates: lifecycle.AuditSpec{
			Source: vocabulary.CaseTransitionSource.String(),
			At:     vocabulary.CaseTransitionAt.String(),
			From:   vocabulary.CaseTransitionFrom.String(),
		},
	}
}
