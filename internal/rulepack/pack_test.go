package rulepack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/processor/rule"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The predicate registry is process-global, so nothing here is t.Parallel and
// every test declares the engine's predicates for itself.
func declare(t *testing.T) {
	t.Helper()
	t.Cleanup(ssvocab.SnapshotRegistry())
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("RegisterPredicates: %v", err)
	}
}

func TestDefinitions_LoadOnceThePredicatesAreDeclared(t *testing.T) {
	declare(t)

	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("the turn-sequencing pack did not load: %v", err)
	}
	if len(definitions) < len(rulepack.StagePhases()) {
		t.Fatalf("pack declares %d rules; the chain has %d hops before the terminal notification",
			len(definitions), len(rulepack.StagePhases()))
	}
}

// The whole reason 8.0 gates 8.1: a canonical predicate that nobody declared
// fails rule load, so the pack is unloadable until registration has run.
func TestDefinitions_RefuseToLoadBeforePredicateRegistration(t *testing.T) {
	t.Cleanup(ssvocab.SnapshotRegistry())
	ssvocab.ClearRegistry()

	_, err := rulepack.Definitions()
	if err == nil {
		t.Fatal("the pack loaded with an empty predicate registry; F10's gate would then be inert")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("the pack was refused for the wrong reason: %v", err)
	}
}

// Every hop the chain names has a rule that drives it, and every rule drives a
// hop somebody consumes.
func TestPack_CoversEveryStageHop(t *testing.T) {
	declare(t)
	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}

	published := map[string]bool{}
	for _, definition := range definitions {
		for _, action := range definition.OnEnter {
			published[action.Subject] = true
		}
	}
	for _, phase := range rulepack.StagePhases() {
		subject, subjectErr := rulepack.SubjectForPhase(phase)
		if subjectErr != nil {
			t.Fatalf("SubjectForPhase(%s): %v", phase, subjectErr)
		}
		if !published[subject] {
			t.Errorf("no rule drives a turn into %s (subject %s)", phase, subject)
		}
	}
	if !published[rulepack.SubjectResolved] {
		t.Errorf("no rule announces a resolved turn on %s", rulepack.SubjectResolved)
	}
}

// A turn is created directly in `accepted`, so the hop out of it cannot be
// guarded by the transition operator (design F3) — it would never fire.
func TestPack_TheFirstHopDoesNotUseTheTransitionOperator(t *testing.T) {
	declare(t)
	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}

	found := false
	for _, definition := range definitions {
		for _, condition := range definition.Conditions {
			if condition.Field != vocabulary.TurnPhaseCurrent.String() {
				continue
			}
			if condition.Value != string(vocabulary.PhaseAccepted) {
				continue
			}
			found = true
			if condition.Operator == "transition" {
				t.Errorf("rule %q guards the hop out of `accepted` with the transition operator, which returns "+
					"false on an entity's first evaluation", definition.ID)
			}
		}
	}
	if !found {
		t.Fatal("no rule matches an accepted turn; this test would pass vacuously")
	}
}

// The FSM is stated once. A `from` set that drifted from it fails the load.
func TestDefinitions_RefuseATransitionWhoseFromSetDriftedFromTheFSM(t *testing.T) {
	declare(t)

	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}
	mutated := false
	for index := range definitions {
		for conditionIndex := range definitions[index].Conditions {
			condition := &definitions[index].Conditions[conditionIndex]
			if condition.Operator != "transition" {
				continue
			}
			condition.From = []any{string(vocabulary.PhaseAccepted)}
			mutated = true
			break
		}
		if mutated {
			break
		}
	}
	if !mutated {
		t.Fatal("the pack uses no transition condition, so its FSM check has nothing to guard")
	}

	// Re-encode and re-load through the same gate the real pack faces.
	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("a transition whose from-set contradicts the FSM loaded")
	} else if !strings.Contains(err.Error(), "legal predecessors") {
		t.Fatalf("drifted from-set refused for the wrong reason: %v", err)
	}
}

func TestDefinitions_RefuseAnUncappedPublish(t *testing.T) {
	declare(t)

	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}
	definitions[0].OnEnter[0].MaxIterations = nil
	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("an uncapped stage trigger loaded")
	} else if !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("uncapped action refused for the wrong reason: %v", err)
	}

	unlimited := 0
	definitions[0].OnEnter[0].MaxIterations = &unlimited
	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("max_iterations=0 loaded; that is the rule engine's UNLIMITED sentinel")
	}
}

func TestDefinitions_RefuseARuleThatWritesTheTurnPhaseItself(t *testing.T) {
	declare(t)

	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}
	cap3 := 3
	definitions[0].OnEnter = []rule.Action{{
		Type:          rule.ActionTypeAddTriple,
		Predicate:     vocabulary.TurnPhaseCurrent.String(),
		Object:        string(vocabulary.PhaseAdjudicating),
		MaxIterations: &cap3,
	}}
	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("a rule writing turn.phase.current loaded; the turn recorder is its only owner")
	}
}

func TestDefinitions_RefuseAStageSubjectNobodyConsumes(t *testing.T) {
	declare(t)

	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}
	definitions[0].OnEnter[0].Subject = "semmachina.turn.adjudicate"
	definitions[0].OnRecovery[0].Subject = "semmachina.turn.adjudicate"
	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("a rule publishing to an unconsumed subject loaded")
	}
}

func TestDefinitions_RefuseARuleWithNoRecoveryActions(t *testing.T) {
	declare(t)

	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}
	definitions[0].OnRecovery = nil
	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("a rule with no on_recovery actions loaded; upstream re-fires a still-matching rule at boot ONLY " +
			"for a rule that declares one, so dropping it gives up the narrow bootstrap case that does work. " +
			"It is not the backstop for a turn parked mid-stage — nothing at boot rescues that (see " +
			"internal/resume); it is the case where the entity changed between the rule firing and the crash")
	}
}

// Two gates that exist for 8.1b(d) rather than for anything running today.
//
// The FSM-edge check reads an `eq` condition's value at load to decide which
// phase a hop is gated on, so a value that only exists at runtime makes the edge
// undeterminable — a load-time gate cannot verify an edge whose target is a
// template. And a cooldown short-circuits evaluation before any condition is
// read, so the hop fires on some turns and not others.
//
// The substitution case is checked on an ARTIFACT condition rather than a phase
// one, deliberately: the phase value is already parsed against the closed phase
// set, so a token there is refused for a reason that has nothing to do with
// substitution and this test would pass with the gate removed.
func TestDefinitions_RefuseAHopWhoseTargetOnlyExistsAtRuntime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(definitions []rule.Definition)
	}{
		{
			name: "a substitution token in an artifact condition's value",
			mutate: func(definitions []rule.Definition) {
				for idx := range definitions {
					for cidx, condition := range definitions[idx].Conditions {
						if condition.Field == vocabulary.TurnPhaseCurrent.String() {
							continue
						}
						if condition.Operator != "eq" && condition.Operator != "ne" {
							continue
						}
						definitions[idx].Conditions[cidx].Value = "$message.artifact"
						return
					}
				}
				panic("no artifact condition found to carry a substitution token")
			},
		},
		{
			name: "a substitution token in a phase condition's value",
			mutate: func(definitions []rule.Definition) {
				definitions[0].Conditions[0].Value = "$message.phase"
			},
		},
		{
			name: "a cooldown",
			mutate: func(definitions []rule.Definition) {
				definitions[0].Cooldown = "30s"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declare(t)

			var definitions []rule.Definition
			if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
				t.Fatalf("decode pack: %v", err)
			}
			// Anti-vacuity: the untouched pack must load, or the refusal below
			// would pass for any reason at all.
			if err := rulepack.Check(definitions); err != nil {
				t.Fatalf("the untouched pack does not load: %v", err)
			}
			tc.mutate(definitions)
			if err := rulepack.Check(definitions); err == nil {
				t.Fatal("the pack loaded carrying a hop whose legality nothing at load time can establish")
			}
		})
	}
}

// The stranded-turn pass derives ONE thing from this package: the stage an
// `accepted` turn is owed, as StagePhases()[0]. That derivation is only sound
// while the pack's first hop agrees with it, and nothing else checks that the
// ordered list and the JSON say the same thing.
//
// If they ever disagree, a turn whose first hop was lost is re-triggered into the
// wrong stage — which the turn recorder refuses as an illegal transition, loudly,
// on a turn that was merely waiting. Loud, but for the wrong reason and about the
// wrong component.
func TestPack_FirstHopIsTheFirstStagePhase(t *testing.T) {
	declare(t)
	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}

	stages := rulepack.StagePhases()
	if len(stages) == 0 {
		t.Fatal("the engine declares no stage phases; the derivation would have nothing to agree with")
	}
	want, err := rulepack.SubjectForPhase(stages[0])
	if err != nil {
		t.Fatalf("SubjectForPhase(%s): %v", stages[0], err)
	}

	// Any rule CARRYING the accepted-phase condition, however many others it
	// carries. Filtering on a single-condition rule would let a two-condition one
	// escape both the subject check and the count — and the count is the half
	// that says the first hop is unconditional.
	found := 0
	for _, definition := range definitions {
		carries := false
		for _, condition := range definition.Conditions {
			if condition.Field != vocabulary.TurnPhaseCurrent.String() || condition.Operator != "eq" {
				continue
			}
			if phase, ok := condition.Value.(string); ok && phase == string(vocabulary.PhaseAccepted) {
				carries = true
			}
		}
		if !carries {
			continue
		}
		found++
		if len(definition.Conditions) != 1 {
			t.Errorf("rule %q gates on the accepted phase alongside %d other condition(s); the first hop is "+
				"supposed to be unconditional, and the boot pass's derivation assumes a turn in `accepted` is "+
				"owed that stage with nothing else to satisfy", definition.ID, len(definition.Conditions)-1)
		}
		for _, action := range definition.OnEnter {
			if action.Subject != want {
				t.Errorf("the pack starts an accepted turn on %q, but StagePhases()[0] is %q (%q); the boot "+
					"pass derives the second and would re-trigger a lost first hop into the wrong stage",
					action.Subject, stages[0], want)
			}
		}
	}
	if found != 1 {
		t.Fatalf("%d rules are gated on the accepted phase alone, want exactly 1; the first hop is supposed to "+
			"be unconditional, and the boot pass's derivation assumes there is one of it", found)
	}
}

func TestSubjectForPhase_RefusesThePhasesNoTriggerEnters(t *testing.T) {
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseAccepted, vocabulary.PhaseFailed, vocabulary.TurnPhase("nonsense"),
	} {
		if subject, err := rulepack.SubjectForPhase(phase); err == nil {
			t.Errorf("phase %s reported stage subject %q; nothing is triggered to enter it", phase, subject)
		}
	}
}

func TestProcessorConfig_DeclaresEveryStageSubjectAsAJetStreamPort(t *testing.T) {
	declare(t)

	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatalf("ProcessorConfig: %v", err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if config.Ports == nil {
		t.Fatal("the rule config declares no ports, so every stage trigger would publish to core NATS")
	}

	declared := map[string]string{}
	for _, port := range config.Ports.Outputs {
		declared[port.Subject] = port.Type
	}
	for _, definition := range config.InlineRules {
		for _, action := range definition.OnEnter {
			portType, ok := declared[action.Subject]
			if !ok {
				t.Errorf("rule %q publishes to %q, which no output port declares; the rule engine matches the "+
					"subject EXACTLY and falls back to core NATS", definition.ID, action.Subject)
				continue
			}
			if portType != "jetstream" {
				t.Errorf("subject %q is declared as a %q port; the stage runners bind durable consumers",
					action.Subject, portType)
			}
		}
	}
}

// A mid-chain hop gated on the phase alone fires as the PREVIOUS stage starts,
// not when it finished — a race that usually resolves in the pack's favour and
// therefore passes an end-to-end test while being wrong. It is refused at load.
func TestDefinitions_RefuseAMidChainHopGatedOnThePhaseAlone(t *testing.T) {
	declare(t)

	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}

	mutated := false
	for index := range definitions {
		conditions := definitions[index].Conditions
		if len(conditions) < 2 || conditions[0].Field != vocabulary.TurnPhaseCurrent.String() {
			continue
		}
		if conditions[0].Value == string(vocabulary.PhaseAccepted) {
			continue
		}
		// Replace the artifact condition with a second, redundant phase test —
		// exactly the shape a careless edit produces.
		conditions[1].Field = vocabulary.TurnPhaseCurrent.String()
		conditions[1].Operator = "ne"
		conditions[1].Value = string(vocabulary.PhaseAccepted)
		mutated = true
		break
	}
	if !mutated {
		t.Fatal("no mid-chain hop found; this test would pass vacuously")
	}

	if err := rulepack.Check(definitions); err == nil {
		t.Fatal("a hop gated on the phase alone loaded")
	} else if !strings.Contains(err.Error(), "alone") {
		t.Fatalf("phase-only hop refused for the wrong reason: %v", err)
	}
}

// The first hop is the exemption, and it must stay one: `accepted` is written by
// intake's atomic create, which is a finished fact.
func TestDefinitions_AllowTheFirstHopToGateOnThePhaseAlone(t *testing.T) {
	declare(t)

	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	for _, definition := range definitions {
		if len(definition.Conditions) != 1 {
			continue
		}
		condition := definition.Conditions[0]
		if condition.Field != vocabulary.TurnPhaseCurrent.String() || condition.Operator == "transition" {
			continue
		}
		if condition.Value != string(vocabulary.PhaseAccepted) {
			t.Errorf("rule %q gates on phase %v alone and is not the first hop", definition.ID, condition.Value)
		}
	}
}
