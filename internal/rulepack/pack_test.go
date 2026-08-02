package rulepack_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"
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

func TestDefinitions_AutomaticCompanionPathIsExplicitlyCappedAtOne(t *testing.T) {
	declare(t)
	definitions := mutable(t)
	hop := hopForPhase(t, definitions, vocabulary.PhaseApplying)
	for _, actions := range [][]rule.Action{hop.OnEnter, hop.OnRecovery} {
		if len(actions) != 1 || actions[0].MaxIterations == nil || *actions[0].MaxIterations != 1 {
			t.Fatalf("automatic companion action = %#v, want explicit max_iterations=1", actions)
		}
	}
	for _, tc := range []struct {
		name string
		cap  *int
	}{{"missing", nil}, {"unlimited", func() *int { v := 0; return &v }()},
		{"widened", func() *int { v := 2; return &v }()}} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := mutable(t)
			action := hopForPhase(t, candidate, vocabulary.PhaseApplying)
			action.OnEnter[0].MaxIterations = tc.cap
			if err := rulepack.Check(candidate); err == nil {
				t.Fatalf("automatic companion action loaded with cap %v", tc.cap)
			}
		})
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

func TestPack_InterpretationFansOutToAdjudicationAndKnowledge(t *testing.T) {
	declare(t)
	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	hop := hopForPhase(t, definitions, vocabulary.PhaseInterpreting)
	for _, actions := range [][]rule.Action{hop.OnEnter, hop.OnRecovery} {
		published := map[string]bool{}
		for _, action := range actions {
			published[action.Subject] = true
		}
		for _, subject := range []string{
			rulepack.StageSubjectPrefix + string(vocabulary.PhaseAdjudicating),
			rulepack.SubjectKnowledge,
			rulepack.SubjectAccusation,
		} {
			if !published[subject] {
				t.Errorf("interpretation does not publish %s in both entry and recovery", subject)
			}
		}
	}
}

func TestPack_ApplicationFansInEffectsKnowledgeAndAccusationBeforeNarration(t *testing.T) {
	declare(t)
	definitions, err := rulepack.Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	hop := hopForPhase(t, definitions, vocabulary.PhaseApplying)
	conditions := map[string]bool{}
	for _, condition := range hop.Conditions {
		if condition.Operator == "ne" && condition.Value == "" {
			conditions[condition.Field] = true
		}
	}
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.TurnEffectsBatch, vocabulary.TurnKnowledgeRef, vocabulary.TurnAccusationRef,
		vocabulary.TurnCaseProgressRef,
	} {
		if !conditions[predicate.String()] {
			t.Errorf("narration is not gated on %s", predicate)
		}
	}
}

func TestSubjectKnowledgeIsAuxiliaryRatherThanAStagePhase(t *testing.T) {
	if phase, ok := rulepack.PhaseForSubject(rulepack.SubjectKnowledge); ok {
		t.Fatalf("knowledge subject maps to stage phase %q", phase)
	}
	for _, phase := range rulepack.StagePhases() {
		subject, err := rulepack.SubjectForPhase(phase)
		if err != nil {
			t.Fatalf("SubjectForPhase(%s): %v", phase, err)
		}
		if subject == rulepack.SubjectKnowledge {
			t.Fatalf("knowledge appears in StagePhases as %s", phase)
		}
	}
}

func TestSubjectAccusationIsAuxiliaryRatherThanAStagePhase(t *testing.T) {
	if phase, ok := rulepack.PhaseForSubject(rulepack.SubjectAccusation); ok {
		t.Fatalf("accusation subject maps to stage phase %q", phase)
	}
	for _, phase := range rulepack.StagePhases() {
		subject, err := rulepack.SubjectForPhase(phase)
		if err != nil {
			t.Fatalf("SubjectForPhase(%s): %v", phase, err)
		}
		if subject == rulepack.SubjectAccusation {
			t.Fatalf("accusation appears in StagePhases as %s", phase)
		}
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
			if action.Type != rule.ActionTypePublish {
				continue
			}
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

// mutable decodes the pack and proves the UNTOUCHED copy loads.
//
// The anti-vacuity half is the point: a refusal below has to be caused by the
// mutation, and a test that only asserts "Check returned an error" passes just as
// happily when the pack was already broken for an unrelated reason.
func mutable(t *testing.T) []rule.Definition {
	t.Helper()
	var definitions []rule.Definition
	if err := json.Unmarshal(rulepack.JSON(), &definitions); err != nil {
		t.Fatalf("decode pack: %v", err)
	}
	if err := rulepack.Check(definitions); err != nil {
		t.Fatalf("the untouched pack does not load: %v", err)
	}
	return definitions
}

// refuse holds a mutated pack to Check and to the REASON it was refused.
//
// The reason matters as much as the refusal: this pack has a dozen gates, and a
// mutation that trips a different one proves nothing about the gate under test —
// it is the load-time form of a test that cannot tell "refused" from "never
// reached".
func refuse(t *testing.T, definitions []rule.Definition, want ...string) {
	t.Helper()
	err := rulepack.Check(definitions)
	if err == nil {
		t.Fatal("the mutated pack loaded")
	}
	for _, phrase := range want {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("refused for the wrong reason: wanted a message carrying %q, got: %v", phrase, err)
		}
	}
}

// hopForPhase returns the one rule gated on phase, found by SHAPE rather than by
// rule id so a renamed rule fails on the property it was renamed out of rather
// than on a string in a test.
func hopForPhase(t *testing.T, definitions []rule.Definition, phase vocabulary.TurnPhase) *rule.Definition {
	t.Helper()
	var found *rule.Definition
	for index := range definitions {
		stageHop := false
		for _, action := range definitions[index].OnEnter {
			_, entersStage := rulepack.PhaseForSubject(action.Subject)
			stageHop = stageHop || entersStage || action.Subject == rulepack.SubjectResolved
		}
		if !stageHop {
			continue
		}
		for _, condition := range definitions[index].Conditions {
			if condition.Field != vocabulary.TurnPhaseCurrent.String() || condition.Operator != "eq" {
				continue
			}
			if value, ok := condition.Value.(string); !ok || value != string(phase) {
				continue
			}
			if found != nil {
				t.Fatalf("more than one hop is gated on %s, so this test would mutate an arbitrary one", phase)
			}
			found = &definitions[index]
		}
	}
	if found == nil {
		t.Fatalf("no hop is gated on phase %s; this test would pass vacuously", phase)
	}
	return found
}

// hopPublishing returns the one rule whose ENTRY edge drives a turn into phase.
func hopPublishing(t *testing.T, definitions []rule.Definition, phase vocabulary.TurnPhase) *rule.Definition {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("SubjectForPhase(%s): %v", phase, err)
	}
	var found *rule.Definition
	for index := range definitions {
		for _, action := range definitions[index].OnEnter {
			if action.Subject != subject {
				continue
			}
			if found != nil {
				t.Fatalf("more than one hop publishes %s, so this test would mutate an arbitrary one", subject)
			}
			found = &definitions[index]
		}
	}
	if found == nil {
		t.Fatalf("no hop publishes %s; this test would pass vacuously", subject)
	}
	return found
}

// transitionHopInto returns the one rule guarded by a transition INTO phase.
//
// A separate finder from hopForPhase rather than a flag on it: the two pin a
// phase in different senses — `eq` names the phase a turn is SITTING in, a
// transition names the one it just MOVED into — and a test that conflated them
// would silently mutate whichever the pack happened to carry.
func transitionHopInto(t *testing.T, definitions []rule.Definition, phase vocabulary.TurnPhase) *rule.Definition {
	t.Helper()
	var found *rule.Definition
	for index := range definitions {
		for _, condition := range definitions[index].Conditions {
			if condition.Field != vocabulary.TurnPhaseCurrent.String() || condition.Operator != "transition" {
				continue
			}
			if value, ok := condition.Value.(string); !ok || value != string(phase) {
				continue
			}
			if found != nil {
				t.Fatalf("more than one hop transitions into %s, so this test would mutate an arbitrary one", phase)
			}
			found = &definitions[index]
		}
	}
	if found == nil {
		t.Fatalf("no hop transitions into phase %s; this test would pass vacuously", phase)
	}
	return found
}

// artifactCondition returns the condition written by the stage owning the
// gated phase. Auxiliary fan-in witnesses are allowed beside it.
func artifactCondition(t *testing.T, definition *rule.Definition) *expression.ConditionExpression {
	t.Helper()
	var phase vocabulary.TurnPhase
	for _, condition := range definition.Conditions {
		if condition.Field == vocabulary.TurnPhaseCurrent.String() {
			phase = vocabulary.TurnPhase(condition.Value.(string))
		}
	}
	artifacts, _ := vocabulary.StageArtifacts(phase)
	var found *expression.ConditionExpression
	for index := range definition.Conditions {
		if !slices.ContainsFunc(artifacts, func(p vocabulary.Predicate) bool {
			return p.String() == definition.Conditions[index].Field
		}) {
			continue
		}
		if found != nil {
			t.Fatalf("rule %q carries more than one non-phase condition", definition.ID)
		}
		found = &definition.Conditions[index]
	}
	if found == nil {
		t.Fatalf("rule %q carries no artifact condition; this test would pass vacuously", definition.ID)
	}
	return found
}

// retarget points a hop's entry AND recovery edges at another stage, which is the
// only way to move an edge without tripping the subject-agreement gate first.
func retarget(t *testing.T, definition *rule.Definition, phase vocabulary.TurnPhase) {
	t.Helper()
	subject, err := rulepack.SubjectForPhase(phase)
	if err != nil {
		t.Fatalf("SubjectForPhase(%s): %v", phase, err)
	}
	for index := range definition.OnEnter {
		definition.OnEnter[index].Subject = subject
	}
	for index := range definition.OnRecovery {
		definition.OnRecovery[index].Subject = subject
	}
}

// Phases sequence; ARTIFACTS gate (design F21).
//
// Every phase but `accepted` is written when a stage is ENTERED, so a mid-chain
// hop that does not also match the artifact that stage produces fires as the
// previous stage STARTS and races it — a race that is usually won, so the defect
// passes an end-to-end test and appears under load.
//
// Each mutation below is a rule that LOOKS gated and is not. The second one is
// the reason this gate had to be tightened rather than merely kept: "gated on
// something besides the phase" accepted a birth-record fact and reintroduced the
// whole race while reading as a conjunction.
func TestDefinitions_RefuseAMidChainHopNotGatedOnItsStagesArtifact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, definitions []rule.Definition)
		want   []string
	}{
		{
			name: "the artifact condition dropped, leaving the phase alone",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				hop := hopForPhase(t, definitions, vocabulary.PhaseResolving)
				artifact := artifactCondition(t, hop)
				hop.Conditions = slices.DeleteFunc(hop.Conditions,
					func(condition expression.ConditionExpression) bool {
						return condition.Field == artifact.Field
					})
			},
			want: []string{"matches phase \"resolving\"", "artifact the resolving stage produces"},
		},
		{
			// turn.action.player is written by intake's atomic create, so it is
			// present from the turn's first instant. Conjoining it with the phase
			// changes nothing about WHEN the rule fires: it is not a gate at all,
			// and this is precisely the shape the pre-8.1b check accepted.
			name: "a birth-record fact standing in for the artifact",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				artifact := artifactCondition(t, hopForPhase(t, definitions, vocabulary.PhaseResolving))
				artifact.Field = vocabulary.TurnActionPlayer.String()
				artifact.Operator = "ne"
				artifact.Value = ""
			},
			want: []string{"artifact the resolving stage produces", vocabulary.TurnRollRef.String()},
		},
		{
			// An artifact of an EARLIER stage is a birth record with extra steps:
			// the verdict reference landed two stages before the turn reached
			// `applying`, so it too is present the whole time the phase is.
			name: "an earlier stage's artifact standing in for this one's",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				artifact := artifactCondition(t, hopForPhase(t, definitions, vocabulary.PhaseApplying))
				artifact.Field = vocabulary.TurnVerdictRef.String()
			},
			want: []string{"artifact the applying stage produces", vocabulary.TurnEffectsBatch.String()},
		},
		{
			// The shape a careless edit produces: a second, redundant phase test
			// where the artifact condition used to be.
			name: "a second phase test standing in for the artifact",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				artifact := artifactCondition(t, hopForPhase(t, definitions, vocabulary.PhaseApplying))
				artifact.Field = vocabulary.TurnPhaseCurrent.String()
				artifact.Operator = "eq"
				artifact.Value = string(vocabulary.PhaseApplying)
			},
			want: []string{"artifact the applying stage produces"},
		},
		{
			// A terminal phase has no owning stage, so no artifact could ever
			// satisfy the gate: a turn that has ended has nothing pending. Matching
			// one with `eq` is refused as a category error rather than reported as
			// a missing artifact nobody could supply.
			name: "a hop gated on a terminal phase with eq",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				hop := hopForPhase(t, definitions, vocabulary.PhaseNarrating)
				for index := range hop.Conditions {
					if hop.Conditions[index].Field == vocabulary.TurnPhaseCurrent.String() {
						hop.Conditions[index].Value = string(vocabulary.PhaseComplete)
					}
				}
			},
			want: []string{"no stage produces an artifact for it", "matchable only as a MOVE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declare(t)
			definitions := mutable(t)
			tc.mutate(t, definitions)
			refuse(t, definitions, tc.want...)
		})
	}
}

// A rule that names no phase at all cannot be checked by either gate: nothing
// says which stage's artifact should gate it, and nothing says which edge it
// takes. It is refused rather than exempted.
func TestDefinitions_RefuseAHopThatNamesNoPhase(t *testing.T) {
	declare(t)
	definitions := mutable(t)

	hop := hopForPhase(t, definitions, vocabulary.PhaseNarrating)
	hop.Conditions = slices.DeleteFunc(hop.Conditions, func(condition expression.ConditionExpression) bool {
		return condition.Field == vocabulary.TurnPhaseCurrent.String()
	})
	refuse(t, definitions, "names no phase")
}

// `eq` and `transition` are the only operators that PIN a phase. A hop matching
// `phase != something` fires in every other phase at once, so neither the
// artifact gate nor the FSM-edge check can establish anything about it.
func TestDefinitions_RefuseAPhaseConditionThatPinsNoPhase(t *testing.T) {
	declare(t)
	definitions := mutable(t)

	hop := hopForPhase(t, definitions, vocabulary.PhaseNarrating)
	for index := range hop.Conditions {
		if hop.Conditions[index].Field == vocabulary.TurnPhaseCurrent.String() {
			hop.Conditions[index].Operator = "ne"
			hop.Conditions[index].Value = string(vocabulary.PhaseAccepted)
		}
	}
	refuse(t, definitions, "pins no phase")
}

// A pack must not be able to EXPRESS an edge the turn FSM refuses.
//
// Every mutation below is loud at runtime today — the turn recorder rejects the
// transition — but loud at runtime means a player's turn is the thing that
// discovers it. The declaration and the FSM are both data at load time, so the
// comparison costs nothing and moves the failure to the person editing the pack.
//
// The third case exists because the first two cannot reach half the check. Both
// of the pack's transition-pinned rules publish SubjectResolved, which enters no
// phase — so `PhaseForSubject` declines and the edge is never consulted for them.
// Skipping transition-pinned hops entirely therefore left the whole suite green:
// the branch was real code no rule in the pack could exercise, which is the
// "looks gated, isn't" shape this task exists to close. Giving one a real stage
// subject is what reaches it.
func TestDefinitions_RefuseAHopTheTurnFSMCannotTake(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, definitions []rule.Definition)
		want   []string
	}{
		{
			// The edge 8.1b names: accepted → narrating, skipping adjudication,
			// the dice and the applier.
			name: "a hop that skips three stages",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				retarget(t, hopForPhase(t, definitions, vocabulary.PhaseAccepted), vocabulary.PhaseNarrating)
			},
			want: []string{`from "accepted" into "narrating"`, "skips"},
		},
		{
			name: "a hop that runs the turn backwards",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				retarget(t, hopForPhase(t, definitions, vocabulary.PhaseNarrating), vocabulary.PhaseAdjudicating)
			},
			want: []string{`from "narrating" into "adjudicating"`, "backwards"},
		},
		{
			// A transition names the phase the turn has just ENTERED, so it fixes
			// the source of a hop exactly as `eq` does and is held to the same
			// legality. The source phase in the message is the proof this case
			// reaches the transition branch and not another: no `eq`-pinned hop can
			// carry `complete`, because the artifact gate refuses a terminal phase
			// matched with `eq` before the edge check ever runs.
			name: "a transition-pinned hop retargeted at a stage it cannot reach",
			mutate: func(t *testing.T, definitions []rule.Definition) {
				retarget(t, transitionHopInto(t, definitions, vocabulary.PhaseComplete), vocabulary.PhaseApplying)
			},
			want: []string{`from "complete" into "applying"`, "backwards"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declare(t)
			definitions := mutable(t)
			tc.mutate(t, definitions)
			refuse(t, definitions, append([]string{"the turn FSM enters"}, tc.want...)...)
		})
	}
}

// `or` would fire a stage on either half of a hop alone — including on the phase
// alone, which is F21's race with the gate that catches it bypassed.
func TestDefinitions_RefuseAPackThatCombinesAHopsConditionsWithOr(t *testing.T) {
	declare(t)
	definitions := mutable(t)

	hopForPhase(t, definitions, vocabulary.PhaseApplying).Logic = "or"
	refuse(t, definitions, "combines its conditions with")
}

// A recovery edge that resumed a DIFFERENT stage than the entry edge started
// would skip a hop after every restart. Both subjects below are real stage
// subjects and the edge is FSM-legal, so nothing but the agreement gate can
// refuse this.
func TestDefinitions_RefuseARecoveryThatResumesADifferentStage(t *testing.T) {
	declare(t)
	definitions := mutable(t)

	// The hop into the dice, whose phase (`adjudicating`) legally reaches the
	// applier too — so pointing its recovery edge there is a divergence and
	// NOTHING else. An FSM-illegal recovery subject would be refused by the edge
	// check instead, and this test would pass without the agreement gate.
	hop := hopPublishing(t, definitions, vocabulary.PhaseResolving)
	subject, err := rulepack.SubjectForPhase(vocabulary.PhaseApplying)
	if err != nil {
		t.Fatalf("SubjectForPhase: %v", err)
	}
	hop.OnRecovery[0].Subject = subject
	refuse(t, definitions, "on entry and")
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
