package rulepack

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// PackID is the rule pack's projection-owner and graph-event producer identity.
const PackID = "semmachina-turn-sequencing"

// EntityPattern is the six-position pattern every turn-sequencing rule watches.
//
// Turn entities and nothing else: the pack is the turn's own state machine, and
// a rule that also matched characters or scenes would evaluate the whole world
// on every write to find the one entity it cares about.
const EntityPattern = "*.semmachina.*.*." + turnTypeSegment + ".*"

// turnTypeSegment is the turn entity's position in the six-part ID. It is
// restated here rather than imported from internal/turn because the dependency
// would run the wrong way — the stage runners import this package.
const turnTypeSegment = "turn"

//go:embed turn-sequencing.json
var packJSON []byte

// JSON returns the pack exactly as authored.
//
// The pack is DATA — a rule pack is JSON by project rule, not Go — and this is
// the byte form an operator can read, diff, and hand to a rule processor's
// rules_files. Definitions() is the same bytes decoded and checked.
func JSON() []byte { return slices.Clone(packJSON) }

// Definitions returns the turn-sequencing rules, verified against the engine's
// own vocabularies.
//
// The verification is the point, and it is what lets the pack stay data without
// becoming a SECOND copy of the turn FSM. Three facts in the JSON are
// derivable — a transition's legal `from` set, the closed roll-band set, and
// which subject drives which phase — and every one of them is a fact stated
// authoritatively somewhere else in the engine. Left unchecked, each would be a
// hand-maintained duplicate that drifts silently: a `from` set that still lists
// a phase the FSM dropped produces a rule that quietly never fires, which is
// indistinguishable from a stage that never ran.
//
// So the JSON declares them and this function REFUSES the pack when they
// disagree. The vocabulary stays the single source; the pack stays inspectable
// data; drift is a load failure rather than a turn that stalls.
//
// It also runs upstream's own rule.ValidateDefinition, which is where the
// predicate-declaration gate lives: every condition field and every
// triple-writing action predicate must be registered
// (vocabulary.RegisterPredicates), and no condition may name a rule-opaque
// predicate. Callers must have registered the engine's predicates first.
func Definitions() ([]rule.Definition, error) {
	var definitions []rule.Definition
	if err := json.Unmarshal(packJSON, &definitions); err != nil {
		return nil, fmt.Errorf("decode the turn-sequencing rule pack: %w", err)
	}
	if err := Check(definitions); err != nil {
		return nil, err
	}
	return definitions, nil
}

// Check holds a candidate turn-sequencing pack to every gate Definitions runs.
//
// It is exported because the gates are the interesting part: an operator or a
// test holding a modified pack should be able to ask the same question the
// loader asks, rather than reaching for an unexported hook that then has to stay
// in step with it.
func Check(definitions []rule.Definition) error {
	if len(definitions) == 0 {
		return fmt.Errorf("the turn-sequencing rule pack declares no rules")
	}
	seen := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.ID == "" {
			return fmt.Errorf("the turn-sequencing rule pack declares a rule with no id")
		}
		if seen[definition.ID] {
			return fmt.Errorf("the turn-sequencing rule pack declares rule %q twice", definition.ID)
		}
		seen[definition.ID] = true
		if err := checkDefinition(definition); err != nil {
			return fmt.Errorf("rule %q: %w", definition.ID, err)
		}
	}
	return nil
}

// checkDefinition holds one rule to everything the engine can decide about it
// without a running broker.
func checkDefinition(definition rule.Definition) error {
	if err := rule.ValidateDefinition(definition); err != nil {
		return err
	}
	if !definition.Enabled {
		return fmt.Errorf(
			"is disabled; a disabled rule in the engine's own sequencing pack is a hop of the turn loop that " +
				"silently never fires")
	}
	if definition.Entity.Pattern != EntityPattern {
		return fmt.Errorf("watches %q, not the turn pattern %q", definition.Entity.Pattern, EntityPattern)
	}
	if definition.Logic != "and" {
		return fmt.Errorf(
			"combines its conditions with %q; every hop in this pack is a conjunction of a phase and an "+
				"artifact, and `or` would fire a stage on either half alone", definition.Logic)
	}
	if len(definition.Conditions) == 0 {
		return fmt.Errorf("declares no conditions")
	}
	// A cooldown short-circuits the rule engine's evaluation before any condition
	// is read, so a hop carrying one fires SOMETIMES. That is the worst shape to
	// debug in a turn loop — a chain that advances on one turn and stalls on the
	// next, with nothing anywhere reporting a difference — and this pack has no
	// use for one: every hop is edge-triggered on a fact that lands exactly once.
	if definition.Cooldown != "" {
		return fmt.Errorf(
			"declares cooldown %q; a cooled-down rule is short-circuited before its conditions are read, so the "+
				"hop it guards fires on some turns and not others with nothing reporting the difference",
			definition.Cooldown)
	}
	for index, condition := range definition.Conditions {
		if err := checkCondition(condition); err != nil {
			return fmt.Errorf("condition[%d] on %s: %w", index, condition.Field, err)
		}
	}
	// The precondition BOTH gates below share, checked once and stated once: a
	// rule that pins no phase cannot be held to either of them. Nothing says
	// whose artifact should gate it, and nothing says which edge it declares, so
	// both would pass in silence — a gate that answers "fine" to a question it
	// could not read is worse than no gate.
	if len(phaseGates(definition)) == 0 {
		return fmt.Errorf(
			"names no phase, so nothing at load time can say which stage's artifact should gate it or which edge " +
				"of the turn FSM it takes. Every rule in this pack is one hop: it matches a phase with `eq` or " +
				"`transition`, and a mid-chain hop also matches an artifact that phase's stage produces")
	}
	if err := checkArtifactGate(definition); err != nil {
		return err
	}
	if err := checkActions(definition); err != nil {
		return err
	}
	// LAST, and the order is load-bearing: the edge check reads the subject an
	// action publishes, so it must run after checkActions has established that
	// the subject is one a stage consumes and that entry and recovery agree on
	// it. Otherwise a typo'd subject would be reported as an illegal FSM edge.
	return checkPhaseEdges(definition)
}

// phaseGate is a phase a rule is PINNED to, with the operator that pins it.
type phaseGate struct {
	phase    vocabulary.TurnPhase
	operator string
}

// phaseGates returns every phase a rule is pinned to.
//
// Only `eq` and `transition` PIN a phase, and this function counts only those —
// checkCondition refuses any other operator on turn.phase.current for the same
// reason, but the two are independent on purpose. A rule matching `phase != x`
// fires in every OTHER phase at once, so neither of the two gates below could say
// anything true about it; reading such a condition as a pin here would let a
// reordering of the checks turn "unpinned" into a confidently wrong phase.
func phaseGates(definition rule.Definition) []phaseGate {
	gates := make([]phaseGate, 0, len(definition.Conditions))
	for _, condition := range definition.Conditions {
		if condition.Field != vocabulary.TurnPhaseCurrent.String() {
			continue
		}
		if condition.Operator != "eq" && condition.Operator != "transition" {
			continue
		}
		phase, ok := condition.Value.(string)
		if !ok {
			// checkCondition already refused a phase compared against a non-string.
			continue
		}
		gates = append(gates, phaseGate{phase: vocabulary.TurnPhase(phase), operator: condition.Operator})
	}
	return gates
}

// checkArtifactGate refuses a mid-chain hop that is not gated on the artifact
// the stage owning its phase produces. Phases sequence; ARTIFACTS gate.
//
// This is the least obvious gate here and the one that took a surviving mutation
// to find. Every phase except `accepted` is written when a stage is ENTERED, so a
// rule matching `turn.phase.current == resolving` fires the moment the dice stage
// starts — not when it finished. The next stage is then racing the one before it
// for the artifact it needs, and the race is usually won: the previous stage
// writes its artifact in microseconds while the trigger travels through a KV
// watch, a rule evaluation, a publish and a durable consumer. So the defect
// passes an end-to-end test almost every time and fails in production under load,
// which is the worst shape a bug can have.
//
// # Why "a second condition" was not enough
//
// The first version of this gate asked whether the rule matched anything BESIDES
// the phase, and that is a weaker question than it looks. `turn.action.player` is
// written by intake's atomic create, so it is present from the turn's first
// instant; a hop conjoining it with the phase reads as a conjunction, changes
// nothing about when the rule fires, and reintroduces the race in full. An
// EARLIER stage's artifact is the same failure with more steps — the verdict
// reference has been present for two stages by the time a turn is applying.
//
// So the question is now the specific one: does the rule match a predicate the
// stage owning THAT phase writes when it finishes? vocabulary.StageArtifacts is
// the per-phase answer, composed from the projection lists the stages write
// through, and it is shared with the boot-time stranded-turn pass — which asks
// the identical question for a different reason. Matching such a predicate is
// proof its stage finished, because a scalar condition on an ABSENT predicate
// does not match — Required is refused above, so a missing field evaluates false
// rather than erroring, and presence is therefore implied by the match.
//
// # Three exemptions, all principled
//
// `accepted` is written by intake's atomic create, which is a completed fact, so
// the first hop legitimately gates on the phase alone. A `transition` condition
// fires on the phase MOVE rather than on its presence, which is likewise a
// completed fact. And the terminal phases have no owning stage at all — nothing
// is pending for a turn that has ended — so they are matchable only as a move,
// which the second exemption already covers and this function says out loud.
func checkArtifactGate(definition rule.Definition) error {
	for _, gate := range phaseGates(definition) {
		if gate.operator == "transition" {
			continue
		}
		if gate.phase == vocabulary.PhaseAccepted {
			continue
		}
		artifacts, known := vocabulary.StageArtifacts(gate.phase)
		if !known {
			// checkCondition already refused a value outside the phase set.
			continue
		}
		if len(artifacts) == 0 {
			return fmt.Errorf(
				"matches phase %q with `eq`, but no stage produces an artifact for it — a turn that has ended has "+
					"nothing pending. A terminal phase is matchable only as a MOVE, with the transition operator",
				gate.phase)
		}
		if !gatedOnArtifact(definition, artifacts) {
			return fmt.Errorf(
				"matches phase %q, but nothing else it matches is an artifact the %s stage produces (%v). Every "+
					"phase except %q is written when a stage is ENTERED, so this rule fires as that stage STARTS "+
					"and the stage it triggers races it for the artifact it needs — a race that is usually won, "+
					"which is why the defect passes a test and appears under load. A predicate that is merely "+
					"present beside the phase does not gate anything: a birth record like %s, or an earlier "+
					"stage's artifact, was already there",
				gate.phase, gate.phase, predicateNames(artifacts), vocabulary.PhaseAccepted,
				vocabulary.TurnActionPlayer)
		}
	}
	return nil
}

// gatedOnArtifact reports whether any condition matches one of the predicates the
// stage lands when it finishes.
func gatedOnArtifact(definition rule.Definition, artifacts []vocabulary.Predicate) bool {
	for _, condition := range definition.Conditions {
		for _, artifact := range artifacts {
			if condition.Field == artifact.String() {
				return true
			}
		}
	}
	return false
}

// checkPhaseEdges refuses a hop the turn FSM could never take — `accepted` to
// `narrating`, say, or any edge that runs a turn backwards.
//
// # Why reading the FSM here is not a second FSM
//
// vocabulary.PhaseRank and PhasePredecessors are deliberately unused at RUNTIME:
// the boot-time stranded-turn pass derives no hop from them, because a component
// that computed the next phase and published its trigger would be a second state
// machine executing beside the rule pack, and the two would drift.
//
// This is the opposite use and the distinction is worth stating, because the two
// look alike. Nothing here executes. Both sides are DECLARATIONS available before
// the engine starts — the pack says which phase a hop leaves and which subject it
// publishes, the vocabulary says which phase that subject enters and what may
// precede it — and comparing two declarations is what a validator is. The pack
// remains the only thing that decides a hop at runtime; it just cannot any longer
// declare one the FSM would refuse.
//
// The refusal is worth having even though the turn recorder rejects an illegal
// transition at runtime already: runtime means a player's turn is the thing that
// discovers the authoring error, and the report names the recorder rather than
// the rule that sent it there.
//
// Both pinning operators are checked, not only `eq`. A `transition` condition
// names the phase the turn has just ENTERED, so it fixes the source of the hop
// exactly as an `eq` does; the two terminal hops escape only because
// SubjectResolved enters no phase at all.
func checkPhaseEdges(definition rule.Definition) error {
	edges := []struct {
		label   string
		actions []rule.Action
	}{
		{"on_enter", definition.OnEnter},
		{"on_recovery", definition.OnRecovery},
	}
	for _, gate := range phaseGates(definition) {
		for _, edge := range edges {
			for index, action := range edge.actions {
				target, isStage := PhaseForSubject(action.Subject)
				if !isStage {
					// SubjectResolved announces a turn that has already ended; it
					// drives no stage, so it expresses no edge.
					continue
				}
				if vocabulary.PhaseFollows(gate.phase, target) {
					continue
				}
				want, _ := vocabulary.PhasePredecessors(target)
				return fmt.Errorf(
					"%s[%d] drives a turn from %q into %q, which %s: the turn FSM enters %q only from %v",
					edge.label, index, gate.phase, target, edgeDirection(gate.phase, target),
					target, phaseNames(want))
			}
		}
	}
	return nil
}

// edgeDirection names HOW an illegal edge is illegal, which is the difference
// between a hop pointed at the wrong stage and a hop pointed at an earlier one.
// Rank is what separates them: the turn's forward progression is an order, and a
// target at or below its source is a loop rather than a skip.
func edgeDirection(from, to vocabulary.TurnPhase) string {
	fromRank, fromKnown := vocabulary.PhaseRank(from)
	toRank, toKnown := vocabulary.PhaseRank(to)
	if fromKnown && toKnown && toRank <= fromRank {
		return "runs the turn backwards"
	}
	return "skips a stage"
}

// checkCondition verifies the two derivable condition shapes against their
// source of truth.
func checkCondition(condition expression.ConditionExpression) error {
	if condition.Required {
		return fmt.Errorf(
			"is marked required; a required condition whose predicate is ABSENT evaluates to an error rather " +
				"than to false, and every hop here reads a predicate that legitimately does not exist yet")
	}
	// A `$` in a condition VALUE is a substitution token, resolved against an
	// execution context that exists only inside a rule evaluation
	// (SubstituteConditionValues). It is refused because A LOAD-TIME GATE CANNOT
	// VERIFY AN EDGE WHOSE TARGET IS A RUNTIME TEMPLATE: the FSM-edge check
	// reads an `eq` condition's value to decide which phase a hop is gated on,
	// and `"$message.phase"` makes that undeterminable — the pack would load
	// carrying a hop whose legality nothing established.
	//
	// It is checked on EVERY condition rather than only on the phase one. The
	// closed-set parse below runs only for turn.phase.current, so an artifact
	// condition like {turn.verdict.requires-roll eq "$x"} would otherwise load
	// clean and become a hop that quietly never fires — the same shape, one
	// predicate over.
	//
	// This pack matches phases and artifact presence and has no use for a
	// substitution anyway; refusing one is how that stays true.
	if text, ok := condition.Value.(string); ok && strings.Contains(text, "$") {
		return fmt.Errorf(
			"compares against %q, which carries a substitution token. Substitution is resolved inside a rule "+
				"evaluation, so a load-time gate cannot see what this hop is really gated on — and a hop whose "+
				"target only exists at runtime is a hop whose legality nothing can check", text)
	}

	switch condition.Operator {
	case "transition":
		phase, ok := condition.Value.(string)
		if !ok {
			return fmt.Errorf("transitions to a %T, want a phase name", condition.Value)
		}
		want, known := vocabulary.PhasePredecessors(vocabulary.TurnPhase(phase))
		if !known {
			return fmt.Errorf("transitions to %q, which is not a turn phase", phase)
		}
		got, err := stringSlice(condition.From)
		if err != nil {
			return fmt.Errorf("declares a `from` set that is not a list of phase names: %w", err)
		}
		if !sameSet(got, phaseNames(want)) {
			return fmt.Errorf(
				"declares from=%v for the transition into %q, but the turn FSM's legal predecessors are %v; "+
					"the pack is checked against internal/vocabulary rather than restating it, so a drifted "+
					"copy fails the load instead of producing a hop that never fires", got, phase, phaseNames(want))
		}

	case "in":
		got, err := stringSlice(condition.Value)
		if err != nil {
			return fmt.Errorf("matches against a value that is not a list of strings: %w", err)
		}
		if condition.Field != vocabulary.TurnRollBand.String() {
			return fmt.Errorf("uses `in` on a predicate this pack has no closed set for")
		}
		if !sameSet(got, bandNames(vocabulary.RollBands())) {
			return fmt.Errorf(
				"matches bands %v, but the dice can only select %v", got, bandNames(vocabulary.RollBands()))
		}

	case "eq", "ne":
		if condition.Field == vocabulary.TurnPhaseCurrent.String() {
			// `ne` PINS NO PHASE: a hop matching "the phase is not x" matches every
			// other phase at once, so neither the artifact gate nor the FSM-edge
			// check can establish anything about it — the first would not know
			// whose artifact to require, the second would not know which edge is
			// being declared. Both would pass in silence, which is the exact shape
			// of gate this pack has already been burned by.
			if condition.Operator == "ne" {
				return fmt.Errorf(
					"excludes a phase rather than naming one, and `ne` pins no phase: this hop would match every " +
						"other phase at once, so neither the artifact gate nor the FSM-edge check could say " +
						"anything about it. Match the phase this hop leaves with `eq`, or the move into it with " +
						"`transition`")
			}
			phase, ok := condition.Value.(string)
			if !ok {
				return fmt.Errorf("compares the phase against a %T", condition.Value)
			}
			if _, err := vocabulary.ParseTurnPhase(phase); err != nil {
				return err
			}
		}

	default:
		return fmt.Errorf(
			"uses operator %q; this pack matches phases and artifact presence, and an operator outside "+
				"eq/ne/in/transition is doing something else", condition.Operator)
	}
	return nil
}

// checkActions holds every action to the three properties the chain depends on:
// it publishes a reference and nothing else, it names a subject some stage
// actually consumes, and it carries a cap.
//
// # What on_recovery is worth, stated accurately
//
// This gate used to be justified as the backstop for a turn interrupted
// mid-stage, and that justification was wrong. Bootstrap replay promotes a rule
// to a synthetic entry edge only when it was ALREADY matching, still matches,
// and its entity has been written since the rule last evaluated it (upstream's
// durable stale-replay guard skips the rest). A turn parked mid-stage matches
// nothing — every mid-chain rule is a phase AND an artifact, and the artifact is
// what is missing — and nothing has written to it since. Measured against a live
// broker: three turns parked in three different shapes, a rule processor
// restarted against them, twenty seconds, no re-trigger.
//
// The gate is KEPT because the narrow case it does cover is real and cheap to
// keep: a hop whose entity changed between the rule firing and the process
// dying re-fires at boot only for a rule that declares on_recovery
// (shouldFireOnRecovery returns false for a rule with neither on_recovery nor
// rerun_on_recovery). It is a safety net with a small mouth, not the backstop.
// The backstop is internal/resume.
func checkActions(definition rule.Definition) error {
	lists := map[string][]rule.Action{
		"on_enter":    definition.OnEnter,
		"on_recovery": definition.OnRecovery,
		"on_exit":     definition.OnExit,
		"while_true":  definition.WhileTrue,
		"actions":     definition.Actions,
	}
	for label, actions := range lists {
		switch label {
		case "on_enter", "on_recovery":
			if len(actions) == 0 {
				return fmt.Errorf(
					"declares no %s actions; on_enter is the hop itself, and on_recovery is the one bootstrap "+
						"case that IS rescued — a rule that was already matching, whose entity has been written "+
						"since it last evaluated, re-fires only for a rule that declares one", label)
			}
		default:
			if len(actions) > 0 {
				return fmt.Errorf(
					"declares %s actions; this pack fires only on the entry edge, and while_true in particular "+
						"would re-trigger a stage on every write to the turn", label)
			}
		}
		for index, action := range actions {
			if err := checkAction(action); err != nil {
				return fmt.Errorf("%s[%d]: %w", label, index, err)
			}
		}
	}
	if !sameSet(subjectsOf(definition.OnEnter), subjectsOf(definition.OnRecovery)) {
		return fmt.Errorf(
			"publishes %v on entry and %v on recovery; a recovery that resumed a DIFFERENT stage than the "+
				"one the entry edge started would skip a hop after every restart",
			subjectsOf(definition.OnEnter), subjectsOf(definition.OnRecovery))
	}
	return nil
}

func checkAction(action rule.Action) error {
	if action.Type != rule.ActionTypePublish {
		return fmt.Errorf(
			"is a %q action; sequencing rules only publish a reference to a stage — the phase belongs to the "+
				"turn recorder, and a rule writing it would be a second owner of a single-valued fact",
			action.Type)
	}
	if !knownSubject(action.Subject) {
		return fmt.Errorf("publishes to %q, which no declared durable path consumes", action.Subject)
	}
	if len(action.Properties) > 0 {
		return fmt.Errorf(
			"carries properties; a rule payload carries references only, and the published message already " +
				"names the turn entity")
	}
	if action.MaxIterations == nil {
		return fmt.Errorf(
			"declares no max_iterations; the framework default would apply, and a cap that arrives by " +
				"inheritance is not a decision anyone made about this turn loop")
	}
	if *action.MaxIterations < 1 {
		return fmt.Errorf(
			"declares max_iterations=%d; 0 means UNLIMITED in the rule engine, and an uncapped path that can "+
				"spawn a persona is the unbounded-cognition failure this engine refuses by construction",
			*action.MaxIterations)
	}
	if phase, ok := PhaseForSubject(action.Subject); ok && phase == vocabulary.PhaseCompanion && *action.MaxIterations != 1 {
		return fmt.Errorf("declares max_iterations=%d for the automatic companion path; it must be exactly 1",
			*action.MaxIterations)
	}
	return nil
}

// knownSubject reports whether a subject drives a stage or announces a resolved
// turn.
func knownSubject(subject string) bool {
	if subject == SubjectResolved || subject == SubjectKnowledge || subject == SubjectAccusation {
		return true
	}
	_, isStage := PhaseForSubject(subject)
	return isStage
}

func subjectsOf(actions []rule.Action) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		out = append(out, action.Subject)
	}
	return out
}

// stringSlice reads a JSON-decoded list of strings, accepting the single-string
// shorthand the transition operator documents.
func stringSlice(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, fmt.Errorf("is absent")
	case string:
		return []string{typed}, nil
	case []string:
		return slices.Clone(typed), nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("holds a %T", item)
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("is a %T", value)
	}
}

func sameSet(a, b []string) bool {
	first, second := slices.Clone(a), slices.Clone(b)
	slices.Sort(first)
	slices.Sort(second)
	return slices.Equal(slices.Compact(first), slices.Compact(second))
}

func phaseNames(phases []vocabulary.TurnPhase) []string {
	out := make([]string, 0, len(phases))
	for _, phase := range phases {
		out = append(out, string(phase))
	}
	return out
}

func predicateNames(predicates []vocabulary.Predicate) []string {
	out := make([]string, 0, len(predicates))
	for _, predicate := range predicates {
		out = append(out, predicate.String())
	}
	return out
}

func bandNames(bands []vocabulary.OutcomeBand) []string {
	out := make([]string, 0, len(bands))
	for _, band := range bands {
		out = append(out, string(band))
	}
	return out
}
