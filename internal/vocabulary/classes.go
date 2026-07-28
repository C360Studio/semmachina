package vocabulary

// Plausibility is the adjudicator's judgment of whether the fiction admits
// the proposed action. It is one of the two inputs to RequiresRoll.
type Plausibility string

// The closed plausibility set.
const (
	// PlausibilityImpossible means the fiction forbids the action outright.
	PlausibilityImpossible Plausibility = "impossible"
	// PlausibilityUnlikely means the fiction allows the action but weighs
	// against it.
	PlausibilityUnlikely Plausibility = "unlikely"
	// PlausibilityPlausible means the fiction neither favors nor forbids it.
	PlausibilityPlausible Plausibility = "plausible"
	// PlausibilityCertain means the fiction already grants the action.
	PlausibilityCertain Plausibility = "certain"
)

var plausibilityEnum = newEnum(KindPlausibility,
	PlausibilityImpossible, PlausibilityUnlikely, PlausibilityPlausible, PlausibilityCertain)

// Plausibilities returns the closed plausibility set.
func Plausibilities() []Plausibility { return plausibilityEnum.all() }

// Valid reports whether p is in the closed plausibility set.
func (p Plausibility) Valid() bool { return plausibilityEnum.valid(p) }

// ParsePlausibility accepts only registered plausibility classes.
func ParsePlausibility(s string) (Plausibility, error) { return plausibilityEnum.parse(s) }

// Risk is the adjudicator's judgment of what is at stake. It is the second
// input to RequiresRoll and shapes consequence severity.
type Risk string

// The closed risk set.
const (
	// RiskNone means nothing is at stake; the fiction simply resolves.
	RiskNone Risk = "none"
	// RiskLow means a minor cost or delay is at stake.
	RiskLow Risk = "low"
	// RiskModerate means a real setback is at stake.
	RiskModerate Risk = "moderate"
	// RiskHigh means severe or lasting harm is at stake.
	RiskHigh Risk = "high"
)

var riskEnum = newEnum(KindRisk, RiskNone, RiskLow, RiskModerate, RiskHigh)

// Risks returns the closed risk set.
func Risks() []Risk { return riskEnum.all() }

// Valid reports whether r is in the closed risk set.
func (r Risk) Valid() bool { return riskEnum.valid(r) }

// ParseRisk accepts only registered risk classes.
func ParseRisk(s string) (Risk, error) { return riskEnum.parse(s) }

// ConsequenceClass names the shape of the consequence a miss or partial
// success carries. It is the third rule-matched verdict scalar.
type ConsequenceClass string

// The closed consequence set (PbtA GM-move shapes).
const (
	// ConsequenceNone means nothing bad follows a failure here.
	ConsequenceNone ConsequenceClass = "none"
	// ConsequenceSetback means progress is denied or reversed.
	ConsequenceSetback ConsequenceClass = "setback"
	// ConsequenceHarm means the acting character is hurt.
	ConsequenceHarm ConsequenceClass = "harm"
	// ConsequenceCost means the action succeeds but is paid for.
	ConsequenceCost ConsequenceClass = "cost"
	// ConsequenceComplication means a new problem enters the fiction.
	ConsequenceComplication ConsequenceClass = "complication"
	// ConsequenceEscalation means the existing situation gets worse.
	ConsequenceEscalation ConsequenceClass = "escalation"
)

var consequenceEnum = newEnum(KindConsequence,
	ConsequenceNone, ConsequenceSetback, ConsequenceHarm,
	ConsequenceCost, ConsequenceComplication, ConsequenceEscalation)

// Consequences returns the closed consequence set.
func Consequences() []ConsequenceClass { return consequenceEnum.all() }

// Valid reports whether c is in the closed consequence set.
func (c ConsequenceClass) Valid() bool { return consequenceEnum.valid(c) }

// ParseConsequence accepts only registered consequence classes.
func ParseConsequence(s string) (ConsequenceClass, error) { return consequenceEnum.parse(s) }

// OutcomeBand names an effect-intent group. A roll-requiring verdict declares
// intents for the three roll bands; a no-roll verdict declares exactly one
// auto band.
type OutcomeBand string

// The closed outcome-band set.
const (
	// BandAuto is the single band of a no-roll verdict. It is NOT
	// "automatic success" — it is whatever the fiction already determined,
	// which for an impossible action is a failure-shaped intent set.
	BandAuto OutcomeBand = "auto"
	// BandMiss is a 2d6 total of 6 or less.
	BandMiss OutcomeBand = "miss"
	// BandPartial is a 2d6 total of 7 to 9.
	BandPartial OutcomeBand = "partial"
	// BandFull is a 2d6 total of 10 or more.
	BandFull OutcomeBand = "full"
)

var outcomeBandEnum = newEnum(KindOutcomeBand, BandAuto, BandMiss, BandPartial, BandFull)

// OutcomeBands returns every band, including auto.
func OutcomeBands() []OutcomeBand { return outcomeBandEnum.all() }

// RollBands returns only the bands the dice can select. A roll result may
// never carry BandAuto, and a roll-requiring verdict must declare exactly
// these three bands.
func RollBands() []OutcomeBand { return []OutcomeBand{BandMiss, BandPartial, BandFull} }

// Valid reports whether b is in the closed band set.
func (b OutcomeBand) Valid() bool { return outcomeBandEnum.valid(b) }

// IsRollBand reports whether b is selectable by a dice roll.
func (b OutcomeBand) IsRollBand() bool {
	return b == BandMiss || b == BandPartial || b == BandFull
}

// ParseOutcomeBand accepts only registered bands.
func ParseOutcomeBand(s string) (OutcomeBand, error) { return outcomeBandEnum.parse(s) }

// ModifierSource is the typed provenance of a roll modifier. Modifiers are
// typed rather than free-text so the resolution card can explain a roll and
// so the adjudicator cannot invent unbounded justification.
type ModifierSource string

// The closed modifier-source set.
const (
	// ModifierTrait is an enduring quality of the acting character.
	ModifierTrait ModifierSource = "trait"
	// ModifierEquipment is a carried item that bears on the action.
	ModifierEquipment ModifierSource = "equipment"
	// ModifierPosition is the character's standing in the scene.
	ModifierPosition ModifierSource = "position"
	// ModifierAssistance is help from another entity.
	ModifierAssistance ModifierSource = "assistance"
	// ModifierCondition is a status effect bearing on the action.
	ModifierCondition ModifierSource = "condition"
)

var modifierSourceEnum = newEnum(KindModifierSource,
	ModifierTrait, ModifierEquipment, ModifierPosition, ModifierAssistance, ModifierCondition)

// ModifierSources returns the closed modifier-source set.
func ModifierSources() []ModifierSource { return modifierSourceEnum.all() }

// Valid reports whether m is in the closed modifier-source set.
func (m ModifierSource) Valid() bool { return modifierSourceEnum.valid(m) }

// ParseModifierSource accepts only registered modifier sources.
func ParseModifierSource(s string) (ModifierSource, error) { return modifierSourceEnum.parse(s) }

// EffectType is the v1 effect vocabulary the applier commits.
type EffectType string

// The closed effect-type set (design D5).
const (
	// EffectSetAttribute sets a bounded numeric attribute on an entity.
	EffectSetAttribute EffectType = "set_attribute"
	// EffectMoveEntity relocates an entity to a location entity.
	EffectMoveEntity EffectType = "move_entity"
	// EffectAddRelationship asserts a registered relationship.
	EffectAddRelationship EffectType = "add_relationship"
	// EffectRemoveRelationship retracts a registered relationship.
	EffectRemoveRelationship EffectType = "remove_relationship"
	// EffectSetStatus sets a condition on an entity.
	EffectSetStatus EffectType = "set_status"
)

var effectTypeEnum = newEnum(KindEffectType,
	EffectSetAttribute, EffectMoveEntity, EffectAddRelationship,
	EffectRemoveRelationship, EffectSetStatus)

// EffectTypes returns the closed effect-type set.
func EffectTypes() []EffectType { return effectTypeEnum.all() }

// Valid reports whether e is in the closed effect-type set.
func (e EffectType) Valid() bool { return effectTypeEnum.valid(e) }

// ParseEffectType accepts only registered effect types.
func ParseEffectType(s string) (EffectType, error) { return effectTypeEnum.parse(s) }

// Status is the condition enum written by a set_status effect. All statuses
// land on one single-valued predicate (CharacterStatusCurrent) so a status
// write replaces rather than accumulates.
type Status string

// The closed status set, sized to the starter scene.
const (
	// StatusHealthy is the baseline condition.
	StatusHealthy Status = "healthy"
	// StatusWounded means the character has taken lasting harm.
	StatusWounded Status = "wounded"
	// StatusExhausted means the character is spent.
	StatusExhausted Status = "exhausted"
	// StatusFrightened means fear is shaping the character's choices.
	StatusFrightened Status = "frightened"
	// StatusHidden means the character is concealed from the scene.
	StatusHidden Status = "hidden"
	// StatusRestrained means the character cannot move freely.
	StatusRestrained Status = "restrained"
	// StatusUnconscious means the character cannot act.
	StatusUnconscious Status = "unconscious"
	// StatusDead is terminal.
	StatusDead Status = "dead"
)

var statusEnum = newEnum(KindStatus,
	StatusHealthy, StatusWounded, StatusExhausted, StatusFrightened,
	StatusHidden, StatusRestrained, StatusUnconscious, StatusDead)

// Statuses returns the closed status set.
func Statuses() []Status { return statusEnum.all() }

// Valid reports whether s is in the closed status set.
func (s Status) Valid() bool { return statusEnum.valid(s) }

// ParseStatus accepts only registered statuses.
func ParseStatus(s string) (Status, error) { return statusEnum.parse(s) }

// TurnPhase is the durable turn-phase vocabulary written to the single-valued
// TurnPhaseCurrent predicate. The names are chosen to stay ADR-049 compatible
// so promoting scenes and campaigns to lifecycle Participants later does not
// rename turn phases.
type TurnPhase string

// The closed turn-phase set.
const (
	// PhaseAccepted means the action is durably recorded as one turn.
	PhaseAccepted TurnPhase = "accepted"
	// PhaseAdjudicating means the adjudicator persona is running.
	PhaseAdjudicating TurnPhase = "adjudicating"
	// PhaseResolving means the dice component is running.
	PhaseResolving TurnPhase = "resolving"
	// PhaseApplying means the effect applier is running.
	PhaseApplying TurnPhase = "applying"
	// PhaseNarrating means the narrator persona is running.
	PhaseNarrating TurnPhase = "narrating"
	// PhaseComplete is a terminal success.
	PhaseComplete TurnPhase = "complete"
	// PhaseFailed is a terminal, explicitly-reasoned failure. Never a silent
	// stall.
	PhaseFailed TurnPhase = "failed"
)

var turnPhaseEnum = newEnum(KindTurnPhase,
	PhaseAccepted, PhaseAdjudicating, PhaseResolving, PhaseApplying,
	PhaseNarrating, PhaseComplete, PhaseFailed)

// TurnPhases returns the closed turn-phase set.
func TurnPhases() []TurnPhase { return turnPhaseEnum.all() }

// Valid reports whether p is in the closed turn-phase set.
func (p TurnPhase) Valid() bool { return turnPhaseEnum.valid(p) }

// IsTerminal reports whether p ends the turn and therefore ledgers it.
func (p TurnPhase) IsTerminal() bool { return p == PhaseComplete || p == PhaseFailed }

// ParseTurnPhase accepts only registered turn phases.
func ParseTurnPhase(s string) (TurnPhase, error) { return turnPhaseEnum.parse(s) }

// ChannelAdapter names a player-I/O transport. Player identity is a graph
// entity; the adapter is delivery metadata only, and no engine component
// downstream of the action stream may branch on it except the egress adapter
// resolving the reply-to binding.
type ChannelAdapter string

// The closed adapter set. Slack, email, and SMS are allowed-for by design and
// join this set when their ingress/egress components are built.
const (
	// AdapterWebSocket is this slice's only adapter.
	AdapterWebSocket ChannelAdapter = "websocket"
)

var channelAdapterEnum = newEnum(KindChannelAdapter, AdapterWebSocket)

// ChannelAdapters returns the closed adapter set.
func ChannelAdapters() []ChannelAdapter { return channelAdapterEnum.all() }

// Valid reports whether a is in the closed adapter set.
func (a ChannelAdapter) Valid() bool { return channelAdapterEnum.valid(a) }

// ParseChannelAdapter accepts only registered adapters.
func ParseChannelAdapter(s string) (ChannelAdapter, error) { return channelAdapterEnum.parse(s) }

// Mechanic names a versioned resolution mechanic. The version is part of the
// value so a roll recorded under one mechanic can never be replayed under
// another and silently produce a different band.
type Mechanic string

// The closed mechanic set.
const (
	// Mechanic2d6PbtaV1 is 2d6 plus summed modifiers, banded 6-/7-9/10+.
	Mechanic2d6PbtaV1 Mechanic = "2d6-pbta/v1"
)

var mechanicEnum = newEnum(KindMechanic, Mechanic2d6PbtaV1)

// Mechanics returns the closed mechanic set.
func Mechanics() []Mechanic { return mechanicEnum.all() }

// Valid reports whether m is in the closed mechanic set.
func (m Mechanic) Valid() bool { return mechanicEnum.valid(m) }

// ParseMechanic accepts only registered mechanics.
func ParseMechanic(s string) (Mechanic, error) { return mechanicEnum.parse(s) }

// RNG names a versioned pseudo-random generator. Recorded on every roll so
// replay can prove it is reproducing under the same generator.
type RNG string

// The closed RNG set.
const (
	// RNGPCGV1 is math/rand/v2's PCG seeded from SHA-256(campaign_seed ‖
	// turn_id).
	RNGPCGV1 RNG = "pcg/v1"
)

var rngEnum = newEnum(KindRNG, RNGPCGV1)

// RNGs returns the closed RNG set.
func RNGs() []RNG { return rngEnum.all() }

// Valid reports whether r is in the closed RNG set.
func (r RNG) Valid() bool { return rngEnum.valid(r) }

// ParseRNG accepts only registered RNG versions.
func ParseRNG(s string) (RNG, error) { return rngEnum.parse(s) }
