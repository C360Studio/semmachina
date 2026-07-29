package rulepack

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"

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
	for index, condition := range definition.Conditions {
		if err := checkCondition(condition); err != nil {
			return fmt.Errorf("condition[%d] on %s: %w", index, condition.Field, err)
		}
	}
	if err := checkArtifactGate(definition); err != nil {
		return err
	}
	return checkActions(definition)
}

// checkArtifactGate refuses a mid-chain hop that is gated on the PHASE alone.
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
// A hop must therefore also match the ARTIFACT the previous stage produces —
// the verdict's roll gate, the roll's band, the applied batch, the narration
// reference — because an artifact is written when a stage is DONE.
//
// Two exemptions, both principled. `accepted` is written by intake's atomic
// create, which is a completed fact, so the first hop legitimately gates on the
// phase alone. And a `transition` condition fires on the phase MOVE rather than
// on the phase value, which is likewise a completed fact.
func checkArtifactGate(definition rule.Definition) error {
	gatedOnPhase := false
	gatedOnSomethingElse := false
	for _, condition := range definition.Conditions {
		if condition.Field != vocabulary.TurnPhaseCurrent.String() {
			gatedOnSomethingElse = true
			continue
		}
		if condition.Operator == "transition" {
			return nil
		}
		if phase, ok := condition.Value.(string); ok && phase != string(vocabulary.PhaseAccepted) {
			gatedOnPhase = true
		}
	}
	if gatedOnPhase && !gatedOnSomethingElse {
		return fmt.Errorf(
			"is gated on %s alone. Every phase except %q is written when a stage is ENTERED, so this rule fires "+
				"as the previous stage STARTS and the stage it triggers races the one before it for the artifact "+
				"it needs — a race that is usually won, which is why it passes a test and fails under load. Match "+
				"the artifact the previous stage produces as well",
			vocabulary.TurnPhaseCurrent, vocabulary.PhaseAccepted)
	}
	return nil
}

// checkCondition verifies the two derivable condition shapes against their
// source of truth.
func checkCondition(condition expression.ConditionExpression) error {
	if condition.Required {
		return fmt.Errorf(
			"is marked required; a required condition whose predicate is ABSENT evaluates to an error rather " +
				"than to false, and every hop here reads a predicate that legitimately does not exist yet")
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
					"declares no %s actions; without on_recovery a turn interrupted mid-stage stays matching "+
						"across the restart, produces no transition, and never fires again", label)
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
		return fmt.Errorf("publishes to %q, which no stage consumes", action.Subject)
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
	return nil
}

// knownSubject reports whether a subject drives a stage or announces a resolved
// turn.
func knownSubject(subject string) bool {
	if subject == SubjectResolved {
		return true
	}
	for _, phase := range stagePhases {
		if got, err := SubjectForPhase(phase); err == nil && got == subject {
			return true
		}
	}
	return false
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

func bandNames(bands []vocabulary.OutcomeBand) []string {
	out := make([]string, 0, len(bands))
	for _, band := range bands {
		out = append(out, string(band))
	}
	return out
}
