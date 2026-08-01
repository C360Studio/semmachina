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
