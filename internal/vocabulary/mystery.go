package vocabulary

// EvidenceTruthStatus is the authored classification of an evidence entity.
// It is protected canonical truth, never a model-authored judgment.
type EvidenceTruthStatus string

const (
	// EvidenceTruthClue means the evidence is part of the fair solution path.
	EvidenceTruthClue EvidenceTruthStatus = "clue"
	// EvidenceTruthRedHerring means the evidence is intentionally misleading.
	EvidenceTruthRedHerring EvidenceTruthStatus = "red-herring"
)

var evidenceTruthStatusEnum = newEnum(KindEvidenceTruthStatus,
	EvidenceTruthClue, EvidenceTruthRedHerring)

// EvidenceTruthStatuses returns the closed evidence classification set.
func EvidenceTruthStatuses() []EvidenceTruthStatus { return evidenceTruthStatusEnum.all() }

// Valid reports whether s belongs to the closed evidence classification set.
func (s EvidenceTruthStatus) Valid() bool { return evidenceTruthStatusEnum.valid(s) }

// ParseEvidenceTruthStatus accepts only registered evidence classifications.
func ParseEvidenceTruthStatus(s string) (EvidenceTruthStatus, error) {
	return evidenceTruthStatusEnum.parse(s)
}

// EvidenceRevealKind is an authored structural authorization mode. Question
// testimony is intentionally absent: its authority is the belief record whose
// holder was actually questioned, not an evidence-level permission.
type EvidenceRevealKind string

const (
	// EvidenceRevealObserve admits evidence through an eligible observation.
	EvidenceRevealObserve EvidenceRevealKind = "observe"
	// EvidenceRevealInvestigate admits evidence through an eligible investigation.
	EvidenceRevealInvestigate EvidenceRevealKind = "investigate"
)

var evidenceRevealKindEnum = newEnum(KindEvidenceRevealKind,
	EvidenceRevealObserve, EvidenceRevealInvestigate)

// EvidenceRevealKinds returns the closed authored reveal modes.
func EvidenceRevealKinds() []EvidenceRevealKind { return evidenceRevealKindEnum.all() }

// Valid reports whether k is an authored reveal mode.
func (k EvidenceRevealKind) Valid() bool { return evidenceRevealKindEnum.valid(k) }

// ParseEvidenceRevealKind accepts only declared reveal modes.
func ParseEvidenceRevealKind(s string) (EvidenceRevealKind, error) {
	return evidenceRevealKindEnum.parse(s)
}

// BeliefStance records what a named actor believes about referenced evidence.
type BeliefStance string

const (
	// BeliefAffirms means the actor accepts the proposition represented by the evidence.
	BeliefAffirms BeliefStance = "affirms"
	// BeliefDenies means the actor rejects the proposition represented by the evidence.
	BeliefDenies BeliefStance = "denies"
	// BeliefUncertain means the actor has not committed either way.
	BeliefUncertain BeliefStance = "uncertain"
)

var beliefStanceEnum = newEnum(KindBeliefStance,
	BeliefAffirms, BeliefDenies, BeliefUncertain)

// BeliefStances returns the closed belief stance set.
func BeliefStances() []BeliefStance { return beliefStanceEnum.all() }

// Valid reports whether s belongs to the closed belief stance set.
func (s BeliefStance) Valid() bool { return beliefStanceEnum.valid(s) }

// ParseBeliefStance accepts only registered belief stances.
func ParseBeliefStance(s string) (BeliefStance, error) { return beliefStanceEnum.parse(s) }

// CompanionPolicy bounds whether a companion may initiate an intervention.
type CompanionPolicy string

const (
	// CompanionPolicyReactive permits responses only to explicit requests.
	CompanionPolicyReactive CompanionPolicy = "reactive"
	// CompanionPolicyBoundedInitiative permits rule-triggered work under the
	// engine-owned nonzero iteration ceiling.
	CompanionPolicyBoundedInitiative CompanionPolicy = "bounded-initiative"
)

var companionPolicyEnum = newEnum(KindCompanionPolicy,
	CompanionPolicyReactive, CompanionPolicyBoundedInitiative)

// CompanionPolicies returns the closed companion policy set.
func CompanionPolicies() []CompanionPolicy { return companionPolicyEnum.all() }

// Valid reports whether p belongs to the closed companion policy set.
func (p CompanionPolicy) Valid() bool { return companionPolicyEnum.valid(p) }

// ParseCompanionPolicy accepts only registered companion policies.
func ParseCompanionPolicy(s string) (CompanionPolicy, error) { return companionPolicyEnum.parse(s) }

// HintLevel is the deterministic, bounded companion hint ladder.
type HintLevel string

const (
	// HintLevelNudge is the first and lightest hint level.
	HintLevelNudge HintLevel = "nudge"
	// HintLevelConnect links evidence the companion already knows.
	HintLevelConnect HintLevel = "connect"
	// HintLevelNextStep suggests a bounded next investigative action.
	HintLevelNextStep HintLevel = "next-step"
)

var hintLevelEnum = newEnum(KindHintLevel,
	HintLevelNudge, HintLevelConnect, HintLevelNextStep)

// HintLevels returns the closed hint ladder.
func HintLevels() []HintLevel { return hintLevelEnum.all() }

// Valid reports whether l belongs to the closed hint ladder.
func (l HintLevel) Valid() bool { return hintLevelEnum.valid(l) }

// ParseHintLevel accepts only registered hint levels.
func ParseHintLevel(s string) (HintLevel, error) { return hintLevelEnum.parse(s) }

// CompanionTriggerKind is the closed structural reason the companion stage may intervene.
type CompanionTriggerKind string

const (
	// CompanionTriggerNone means the stage has no authorized intervention.
	CompanionTriggerNone CompanionTriggerKind = "none"
	// CompanionTriggerPlayerHint is an explicit closed request_hint case decision.
	CompanionTriggerPlayerHint CompanionTriggerKind = "player-hint"
	// CompanionTriggerWarning is a bounded-initiative high-risk resolved warning.
	CompanionTriggerWarning CompanionTriggerKind = "warning"
)

var companionTriggerEnum = newEnum(KindCompanionTrigger,
	CompanionTriggerNone, CompanionTriggerPlayerHint, CompanionTriggerWarning)

// CompanionTriggers returns the closed trigger set in priority order.
func CompanionTriggers() []CompanionTriggerKind { return companionTriggerEnum.all() }

// ParseCompanionTrigger accepts only the closed trigger set.
func ParseCompanionTrigger(s string) (CompanionTriggerKind, error) {
	return companionTriggerEnum.parse(s)
}

// CompanionTriggerSource identifies the structural source of a trigger decision.
type CompanionTriggerSource string

const (
	// CompanionTriggerSourceNone means no structural source authorized an intervention.
	CompanionTriggerSourceNone CompanionTriggerSource = "none"
	// CompanionTriggerSourceCaseDecision identifies an explicit request_hint decision.
	CompanionTriggerSourceCaseDecision CompanionTriggerSource = "case-decision"
	// CompanionTriggerSourceResolvedRisk identifies the bounded automatic warning policy.
	CompanionTriggerSourceResolvedRisk CompanionTriggerSource = "resolved-risk"
)

var companionTriggerSourceEnum = newEnum(KindCompanionTriggerSource,
	CompanionTriggerSourceNone, CompanionTriggerSourceCaseDecision, CompanionTriggerSourceResolvedRisk)

// CompanionTriggerSources returns every structural trigger source.
func CompanionTriggerSources() []CompanionTriggerSource { return companionTriggerSourceEnum.all() }

// ParseCompanionTriggerSource accepts only the closed source set.
func ParseCompanionTriggerSource(s string) (CompanionTriggerSource, error) {
	return companionTriggerSourceEnum.parse(s)
}
