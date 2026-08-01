package vocabulary

// CasePhase is the durable phase of a mystery case.
type CasePhase string

const (
	// CasePhaseColdOpen is the authored mystery before the body is observed.
	CasePhaseColdOpen CasePhase = "cold_open"
	// CasePhaseDiscovery begins when the body has been structurally observed.
	CasePhaseDiscovery CasePhase = "discovery"
	// CasePhaseInvestigation is active structured clue work.
	CasePhaseInvestigation CasePhase = "investigation"
	// CasePhaseAccusation accepts an exact culprit, method, and motive claim.
	CasePhaseAccusation CasePhase = "accusation"
	// CasePhaseDenouement is the terminal resolved phase.
	CasePhaseDenouement CasePhase = "denouement"
)

var casePhaseEnum = newEnum(KindCasePhase,
	CasePhaseColdOpen,
	CasePhaseDiscovery,
	CasePhaseInvestigation,
	CasePhaseAccusation,
	CasePhaseDenouement,
)

// CasePhases returns the closed case-phase set in lifecycle order.
func CasePhases() []CasePhase { return casePhaseEnum.all() }

// Valid reports whether p is a declared case phase.
func (p CasePhase) Valid() bool { return casePhaseEnum.valid(p) }

// ParseCasePhase accepts only declared case phases.
func ParseCasePhase(value string) (CasePhase, error) { return casePhaseEnum.parse(value) }

// CaseLifecycleEventKind names the structural receipt that requests one fixed edge.
type CaseLifecycleEventKind string

const (
	// CaseEventBodyObserved requests cold_open to discovery.
	CaseEventBodyObserved CaseLifecycleEventKind = "body-observed"
	// CaseEventInvestigationStarted requests discovery to investigation.
	CaseEventInvestigationStarted CaseLifecycleEventKind = "investigation-started"
	// CaseEventAccusationSubmitted requests investigation to accusation.
	CaseEventAccusationSubmitted CaseLifecycleEventKind = "accusation-submitted"
	// CaseEventAccusationCorrect requests accusation to denouement.
	CaseEventAccusationCorrect CaseLifecycleEventKind = "accusation-correct"
)

var caseLifecycleEventKindEnum = newEnum(KindCaseLifecycleEventKind,
	CaseEventBodyObserved,
	CaseEventInvestigationStarted,
	CaseEventAccusationSubmitted,
	CaseEventAccusationCorrect,
)

// CaseLifecycleEventKinds returns the closed structural receipt set.
func CaseLifecycleEventKinds() []CaseLifecycleEventKind { return caseLifecycleEventKindEnum.all() }

// Valid reports whether k is a declared case lifecycle event kind.
func (k CaseLifecycleEventKind) Valid() bool { return caseLifecycleEventKindEnum.valid(k) }

// ParseCaseLifecycleEventKind accepts only declared structural receipts.
func ParseCaseLifecycleEventKind(value string) (CaseLifecycleEventKind, error) {
	return caseLifecycleEventKindEnum.parse(value)
}
