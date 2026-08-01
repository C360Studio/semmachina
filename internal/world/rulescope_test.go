package world_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	agenticloop "github.com/c360studio/semstreams/processor/agentic-loop"
	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/world"
)

// ruleJSON renders one rule file so a case shows only the field under test.
// Everything else is inert boilerplate the loader needs to decode the rule at
// all.
func ruleJSON(id, conditions, actionList, actions string) string {
	return fmt.Sprintf(
		`{"id":%q,"type":"expression","name":"case","enabled":true,`+
			`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[%s],"logic":"and",%q:[%s]}`,
		id, conditions, actionList, actions)
}

func TestWorldRuleScopeRejectsLifecycleActions(t *testing.T) {
	for _, tc := range []struct {
		actionType string
		fields     string
	}{
		{rule.ActionTypeLifecycleTransition, `,"workflow":"mystery-case","phase":"discovery"`},
		{rule.ActionTypeLifecycleComplete, `,"workflow":"mystery-case"`},
		{rule.ActionTypeLifecycleFail, `,"workflow":"mystery-case","reason":"authored"`},
	} {
		t.Run(tc.actionType, func(t *testing.T) {
			action := fmt.Sprintf(`{"type":%q%s}`, tc.actionType, tc.fields)
			err := loadWithRule(t, ruleJSON("downloaded-lifecycle", "", "on_enter", action))
			if err == nil {
				t.Fatalf("downloadable rule using %s was accepted", tc.actionType)
			}
		})
	}
}

// loadWithRule loads a minimal package whose only rule is the supplied JSON.
func loadWithRule(t *testing.T, ruleFile string) error {
	t.Helper()
	fsys := minimalPackageFS()
	fsys["rules/00-stub.json"] = &fstest.MapFile{Data: []byte(ruleFile)}
	_, err := world.LoadPackage(fsys, world.LoadOptions{})
	return err
}

func TestLoadPackage_RefusesLifecycleProjectionInConditionsAndActionGuards(t *testing.T) {
	lifecycleCondition := `{"field":"$entity.lifecycle.phase","operator":"eq","value":"discovery"}`
	cases := map[string]string{
		"top-level condition": ruleJSON("reads-lifecycle-phase", lifecycleCondition, "on_enter",
			`{"type":"publish","subject":"world.bell.ring","max_iterations":1}`),
		"action when guard": ruleJSON("guards-on-lifecycle-phase", "", "on_enter",
			`{"type":"publish","subject":"world.bell.ring","max_iterations":1,"when":[`+lifecycleCondition+`]}`),
	}
	for name, definition := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadWithRule(t, definition)
			if err == nil {
				t.Fatal("LoadPackage accepted a downloadable rule reading $entity.lifecycle.phase")
			}
			if !strings.Contains(err.Error(), "$entity.lifecycle.phase") {
				t.Fatalf("refusal %q does not name the protected projection", err)
			}
		})
	}
}

func TestLoadPackage_AllowsNonLifecycleRuntimeProjections(t *testing.T) {
	conditions := []string{
		`{"field":"$state.iteration","operator":"gte","value":0}`,
		`{"field":"$prev.scene.attribute.tension","operator":"gte","value":0}`,
		`{"field":"$message.kind","operator":"eq","value":"bell"}`,
	}
	for _, condition := range conditions {
		var decoded struct {
			Field string `json:"field"`
		}
		if err := json.Unmarshal([]byte(condition), &decoded); err != nil {
			t.Fatal(err)
		}
		for _, position := range []string{"top-level", "action-when"} {
			t.Run(decoded.Field+"/"+position, func(t *testing.T) {
				var definition string
				if position == "top-level" {
					definition = ruleJSON("allowed-runtime-projection", condition, "on_enter",
						`{"type":"publish","subject":"world.bell.ring","max_iterations":1}`)
				} else {
					definition = ruleJSON("allowed-runtime-projection", "", "on_enter",
						`{"type":"publish","subject":"world.bell.ring","max_iterations":1,"when":[`+condition+`]}`)
				}
				if err := loadWithRule(t, definition); err != nil {
					t.Fatalf("LoadPackage rejected %s in %s: %v", decoded.Field, position, err)
				}
			})
		}
	}
}

// A world package may not author the turn loop. The turn's facts are the
// ENGINE's state machine: a rule that branches on them is turn sequencing
// written by a downloaded world, and a rule that WRITES them drives the state
// machine directly, past every stage guard the engine has.
//
// Every position a predicate can occupy in a rule is checked, because a gate
// that covered only rule-level conditions would refuse the obvious rule and
// admit the same predicate one field over.
func TestLoadPackage_RefusesAWorldRuleThatReachesTheEnginesTurnFacts(t *testing.T) {
	cases := map[string]struct {
		rule  string
		names []string
	}{
		"condition on the turn phase": {
			rule: ruleJSON("world_reads_phase",
				`{"field":"turn.phase.current","operator":"eq","value":"accepted"}`, "on_enter", ``),
			names: []string{"world_reads_phase", "turn.phase.current", "condition[0]"},
		},
		"action guard on the turn phase": {
			rule: ruleJSON("world_guards_on_phase", `{"field":"scene.attribute.tension","operator":"gte","value":8}`, "on_enter",
				`{"type":"publish","subject":"world.gatehouse.bell",`+
					`"when":[{"field":"turn.verdict.requires-roll","operator":"eq","value":true}]}`),
			names: []string{"world_guards_on_phase", "turn.verdict.requires-roll", "on_enter[0]", "when[0]"},
		},
		"add_triple writing the turn phase": {
			rule: ruleJSON("world_writes_phase", ``, "on_enter",
				`{"type":"add_triple","predicate":"turn.phase.current","object":"complete"}`),
			names: []string{"world_writes_phase", "turn.phase.current", "on_enter[0]"},
		},
		"remove_triple deleting the turn's narration ref": {
			rule: ruleJSON("world_removes_ref", ``, "on_exit",
				`{"type":"remove_triple","predicate":"turn.narration.ref"}`),
			names: []string{"world_removes_ref", "turn.narration.ref", "on_exit[0]"},
		},
		"update_triple on a turn scalar": {
			rule: ruleJSON("world_updates_band", ``, "while_true",
				`{"type":"update_triple","predicate":"turn.roll.band","object":"triumph"}`),
			names: []string{"world_updates_band", "turn.roll.band", "while_true[0]"},
		},
		"replace_owned on the turn phase": {
			rule: ruleJSON("world_replaces_phase", ``, "on_recovery",
				`{"type":"replace_owned","predicate":"turn.phase.current","object":"narrating"}`),
			names: []string{"world_replaces_phase", "turn.phase.current", "on_recovery[0]"},
		},
		"a turn-domain predicate the engine does not even write": {
			rule: ruleJSON("world_invents_turn_fact", ``, "on_enter",
				`{"type":"add_triple","predicate":"turn.gatehouse.bribe","object":"paid"}`),
			names: []string{"world_invents_turn_fact", "turn.gatehouse.bribe"},
		},
		// Not the turn loop, the same boundary: every roll derives from the
		// campaign seed, so a world rule that could read or rewrite it could
		// make replay stop reproducing.
		"add_triple rewriting the campaign seed": {
			rule: ruleJSON("world_reseeds_campaign", ``, "on_enter",
				`{"type":"add_triple","predicate":"campaign.seed.value","object":"7"}`),
			names: []string{"world_reseeds_campaign", "campaign.seed.value", "on_enter[0]"},
		},
		"condition on the campaign seed": {
			rule: ruleJSON("world_reads_seed",
				`{"field":"campaign.seed.value","operator":"eq","value":"7"}`, "on_enter", ``),
			names: []string{"world_reads_seed", "campaign.seed.value", "condition[0]"},
		},
		// Not the turn loop either, and the harm is sharper than the seed's:
		// player.turn.current is the INGRESS admission gate's pointer, so a world
		// rule that could write it could hand a player a second live turn or lock
		// them out of their own campaign — with no player-visible refusal, because
		// the gate would be answering from data the world authored.
		"add_triple repointing a player's current turn": {
			rule: ruleJSON("world_repoints_player", ``, "on_enter",
				`{"type":"add_triple","predicate":"player.turn.current","object":"$entity.id"}`),
			names: []string{"world_repoints_player", "player.turn.current", "on_enter[0]"},
		},
		"condition on a player's current turn": {
			rule: ruleJSON("world_reads_player_turn",
				`{"field":"player.turn.current","operator":"exists","value":true}`, "on_enter", ``),
			names: []string{"world_reads_player_turn", "player.turn.current", "condition[0]"},
		},
		"cron rule writing the turn phase": {
			rule: `{"id":"world_cron_writes_phase","type":"cron","name":"case","enabled":true,` +
				`"schedule":"@hourly","actions":[` +
				`{"type":"add_triple","predicate":"turn.phase.current","object":"failed"}]}`,
			names: []string{"world_cron_writes_phase", "turn.phase.current", "actions[0]"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadWithRule(t, tc.rule)
			if err == nil {
				t.Fatal("LoadPackage accepted a world rule that reaches the engine's turn facts")
			}
			// The file, so the author knows what to open; and the word
			// "reserved", so the message reads as a boundary rather than a bug.
			for _, want := range append(tc.names, "rules/00-stub.json", "reserved") {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

// A rule file may hold an array, and every rule in it faces the boundary. A
// gate that checked the first definition would be passed by a package that put
// its innocent rule first — which is what a package trying to get past a gate
// would do.
func TestLoadPackage_ChecksTheScopeOfEveryRuleInAnArrayValuedFile(t *testing.T) {
	pack := "[" +
		ruleJSON("world_ok", `{"field":"item.attribute.quantity","operator":"lte","value":0}`, "on_enter",
			`{"type":"remove_triple","predicate":"world.location.current"}`) + "," +
		ruleJSON("world_second_rule_drives_the_loop", ``, "on_enter",
			`{"type":"add_triple","predicate":"turn.phase.current","object":"complete"}`) + "]"

	err := loadWithRule(t, pack)
	if err == nil {
		t.Fatal("LoadPackage accepted a turn-loop rule in the second position of an array-valued file")
	}
	for _, want := range []string{"world_second_rule_drives_the_loop", "turn.phase.current"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// The other half of the same boundary: a world rule may not hand work to the
// engine over NATS. A stage subject drives a turn's next hop — and a billed
// persona call — so a world that could publish one could advance, replay, or
// pay for a turn it does not own.
func TestLoadPackage_RefusesAWorldRuleThatPublishesIntoTheEnginesSubjects(t *testing.T) {
	// Both subjects are READ from the constants that define them. A restated
	// string would keep passing after a rename while the gate stopped matching
	// anything.
	stageSubject := rulepack.StageSubjectPrefix + "adjudicating"
	personaTaskSubject := persona.TaskSubjectFor(persona.RoleAdjudicator)

	// The two roots are reserved for DIFFERENT harms, so each case asserts the
	// reason it was refused for and not merely that it was refused. Without
	// this, a gate that matched every subject against one root would look
	// identical to a gate that knew why.
	const (
		engineHarm   = rulepack.StageSubjectFilter // named in the engine root's reason
		spendingHarm = "calling a MODEL"           // named in the agentic root's reason
	)

	cases := map[string]struct {
		rule   string
		names  []string
		reason string
	}{
		"publish to a stage trigger": {
			rule: ruleJSON("world_triggers_stage", ``, "on_enter",
				fmt.Sprintf(`{"type":"publish","subject":%q}`, stageSubject)),
			names:  []string{"world_triggers_stage", stageSubject, "on_enter[0]"},
			reason: engineHarm,
		},
		"publish_agent to a stage trigger": {
			rule: ruleJSON("world_spawns_on_stage", ``, "on_enter",
				fmt.Sprintf(`{"type":"publish_agent","subject":%q,"role":"narrator","prompt":"go"}`, stageSubject)),
			names:  []string{"world_spawns_on_stage", stageSubject, "on_enter[0]"},
			reason: engineHarm,
		},
		"approve routed onto a stage trigger": {
			rule: ruleJSON("world_approves_onto_stage", ``, "on_enter",
				fmt.Sprintf(`{"type":"approve","subject":%q,"reason":"fine"}`, stageSubject)),
			names:  []string{"world_approves_onto_stage", stageSubject, "on_enter[0]"},
			reason: engineHarm,
		},
		// Not a stage subject, still the engine's: the ledger stream is the
		// campaign's archive, and a forged manifest is a forged campaign
		// record. The root check is what covers subjects task 9 has not
		// written yet.
		"publish into another engine subject space": {
			rule: ruleJSON("world_forges_ledger", ``, "on_enter",
				`{"type":"publish","subject":"semmachina.ledger.turn.t1"}`),
			names:  []string{"world_forges_ledger", "semmachina.ledger.turn.t1"},
			reason: engineHarm,
		},
		// Not the engine's namespace at all — upstream's — and reserved for a
		// different harm: a message there is a task the agentic loop answers by
		// calling a model. BOTH action types are asserted, because the SUBJECT
		// is what is refused; a gate that only refused publish_agent would leave
		// the identical spend one action type over.
		"publish_agent dispatching a persona task": {
			rule: ruleJSON("world_spawns_persona", ``, "on_enter",
				fmt.Sprintf(`{"type":"publish_agent","subject":%q,"role":"adjudicator","prompt":"go"}`,
					personaTaskSubject)),
			names:  []string{"world_spawns_persona", personaTaskSubject},
			reason: spendingHarm,
		},
		"plain publish onto the persona task subject": {
			rule: ruleJSON("world_publishes_persona_task", ``, "on_enter",
				fmt.Sprintf(`{"type":"publish","subject":%q}`, personaTaskSubject)),
			names:  []string{"world_publishes_persona_task", personaTaskSubject},
			reason: spendingHarm,
		},
		// The task lane is one of the loop's inputs, not all of them. A signal
		// carries `retry` (spends again) and `feedback` (author text into a
		// running persona's context); a response impersonates model output; an
		// approval answers a gated tool call. Reserving only the task lane
		// would leave these behind "an author cannot guess a loop id", which is
		// secrecy, not structure.
		"publish onto a running loop's control signal": {
			rule: ruleJSON("world_signals_loop", ``, "on_enter",
				`{"type":"publish","subject":"agent.signal.loop-1"}`),
			names:  []string{"world_signals_loop", "agent.signal.loop-1"},
			reason: spendingHarm,
		},
		"publish forged model output": {
			rule: ruleJSON("world_forges_response", ``, "on_enter",
				`{"type":"publish","subject":"agent.response.loop-1"}`),
			names:  []string{"world_forges_response", "agent.response.loop-1"},
			reason: spendingHarm,
		},
		"publish an approval for a gated tool call": {
			rule: ruleJSON("world_approves_tool_call", ``, "on_enter",
				`{"type":"publish","subject":"agent.approval_response.loop-1"}`),
			names:  []string{"world_approves_tool_call", "agent.approval_response.loop-1"},
			reason: spendingHarm,
		},
		// The loop's fifth input, and the one that sits outside the agentic
		// root: a forged tool result is fabricated evidence handed to a running
		// adjudicator, and the iteration it provokes is billed.
		"publish a forged tool result": {
			rule: ruleJSON("world_forges_tool_result", ``, "on_enter",
				`{"type":"publish","subject":"tool.result.call-1"}`),
			names:  []string{"world_forges_tool_result", "tool.result.call-1"},
			reason: "fabricated evidence",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadWithRule(t, tc.rule)
			if err == nil {
				t.Fatal("LoadPackage accepted a world rule publishing into the engine's subject space")
			}
			for _, want := range append(tc.names, "rules/00-stub.json", "reserved", tc.reason) {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

// The fourth reserved position. RULE_STATE is where the rule engine keeps its
// own MatchState — the per-action firing counters, the caps, the revision the
// bootstrap replay guard reads — and upstream's runtime bucket guard does NOT
// cover it: graph.FrameworkOwnedBuckets() is graph buckets only. So a world rule
// merging an empty `action_iterations` map into it would switch off the cap
// discipline on the engine's own rules, at fire time, with no error anywhere.
func TestLoadPackage_RefusesAWorldRuleThatWritesAnEngineKVBucket(t *testing.T) {
	cases := map[string]struct {
		bucket string
		reason string
	}{
		"the rule engine's own match state": {
			bucket: "RULE_STATE",
			reason: "firing counters",
		},
		"a framework-owned graph bucket": {
			bucket: "ENTITY_STATES",
			reason: "single-writer",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadWithRule(t, ruleJSON("world_writes_engine_kv", ``, "on_enter",
				fmt.Sprintf(`{"type":"update_kv","bucket":%q,"key":"k","merge":true,`+
					`"payload":{"action_iterations":{}}}`, tc.bucket)))
			if err == nil {
				t.Fatalf("LoadPackage accepted a world rule writing %s", tc.bucket)
			}
			for _, want := range []string{"world_writes_engine_kv", tc.bucket, "on_enter[0]", "reserved", tc.reason} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

// The bucket is a reserved POSITION, so it faces the literal rule too — and it
// needs it more than the others do, because upstream's runtime guard re-checks
// the bucket only against the graph list. A templated bucket resolving to
// RULE_STATE at fire time is refused by nobody else.
func TestLoadPackage_RefusesATemplatedUpdateKVBucket(t *testing.T) {
	err := loadWithRule(t, ruleJSON("world_templates_bucket", ``, "on_enter",
		`{"type":"update_kv","bucket":"$message.bucket","key":"k","payload":{}}`))
	if err == nil {
		t.Fatal("LoadPackage accepted an update_kv bucket assembled at fire time")
	}
	for _, want := range []string{"world_templates_bucket", "substitution", "bucket"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// A world keeping its own state in its own bucket is ordinary world content.
// The bucket check must refuse the engine's buckets, not update_kv.
func TestLoadPackage_AdmitsAWorldRuleWritingItsOwnKVBucket(t *testing.T) {
	err := loadWithRule(t, ruleJSON("world_keeps_its_own_tally", ``, "on_enter",
		`{"type":"update_kv","bucket":"GATEHOUSE_TALLY","key":"bells","merge":true,"payload":{"rung":1}}`))
	if err != nil {
		t.Fatalf("LoadPackage refused a world rule writing its own KV bucket: %v", err)
	}
}

// The campaign namespace is reserved at SEED granularity, not domain
// granularity, and this pair is the whole reason that distinction exists.
//
// The seed is engine state: minted once, and every roll derives from it, so a
// world rule touching it breaks replay. The campaign CLOCK is the opposite —
// the project pins world time as a world fact whose deadlines are threshold
// rules, which is the canonical world reaction. A gate that reserved `campaign.`
// whole would refuse the clock rule before any world shipped, and the naming
// would then settle around the refusal.
func TestLoadPackage_ReservesTheCampaignSeedWithoutReservingTheCampaignClock(t *testing.T) {
	seedRule := ruleJSON("world_reads_the_seed",
		`{"field":"campaign.seed.value","operator":"eq","value":"7"}`, "on_enter", ``)
	if err := loadWithRule(t, seedRule); err == nil {
		t.Fatal("LoadPackage accepted a world rule branching on the campaign seed")
	}

	// A deadline reaction, which is what CLAUDE.md calls the canonical shape.
	clockRule := ruleJSON("world_gate_bars_at_dusk",
		`{"field":"campaign.clock.hour","operator":"gte","value":20}`, "on_enter",
		`{"type":"add_triple","predicate":"world.relation.hostile-to","object":"$entity.id",`+
			`"max_iterations":1}`)
	if err := loadWithRule(t, clockRule); err != nil {
		t.Fatalf("LoadPackage refused a world reaction on the campaign clock, which is world content: %v", err)
	}
}

// The player namespace is reserved at TURN granularity for the campaign seed's
// reason, and this pair is what makes the distinction real rather than stated.
//
// player.turn.current is the ingress admission gate's state. player.character.*
// is the played-character binding — an ordinary world fact, and exactly the kind
// of thing a world reaction should be able to read. A gate that reserved
// `player.` whole would refuse a world for reacting to who somebody is playing.
func TestLoadPackage_ReservesThePlayersTurnWithoutReservingTheirCharacter(t *testing.T) {
	turnRule := ruleJSON("world_reads_player_turn",
		`{"field":"player.turn.current","operator":"exists","value":true}`, "on_enter", ``)
	if err := loadWithRule(t, turnRule); err == nil {
		t.Fatal("LoadPackage accepted a world rule branching on the ingress admission gate's pointer")
	}

	characterRule := ruleJSON("world_greets_the_played_character",
		`{"field":"player.character.current","operator":"exists","value":true}`, "on_enter",
		`{"type":"add_triple","predicate":"world.relation.knows","object":"$entity.id",`+
			`"max_iterations":1}`)
	if err := loadWithRule(t, characterRule); err != nil {
		t.Fatalf("LoadPackage refused a world reaction on the played-character binding, "+
			"which is world content: %v", err)
	}
}

// The subject reservation is only as good as its coverage of what actually
// consumes a message, so this reads the agentic loop's OWN declared input ports
// and requires every one of them to be inside a reserved root.
//
// It is the drift alarm for both agentic roots: upstream adding a sixth input
// port — or moving one out from under `agent.` the way tool.result already sits
// — fails here rather than silently reopening a spend path.
func TestReservedSubjectRoots_CoverEveryAgenticLoopInput(t *testing.T) {
	ports := agenticloop.DefaultConfig().Ports
	if ports == nil || len(ports.Inputs) == 0 {
		t.Fatal("the agentic loop declares no input ports; this test can no longer see what it checks")
	}

	for _, port := range ports.Inputs {
		subject := strings.TrimSuffix(strings.TrimSuffix(port.Subject, ">"), "*")
		if subject == "" {
			t.Fatalf("input port %q has no subject to check", port.Name)
		}
		// Publishing onto the port's own subject space must be refused. The
		// literal below is a concrete subject inside it, so the check runs the
		// production path rather than inspecting the root list.
		err := loadWithRule(t, ruleJSON("world_publishes_to_a_loop_input", ``, "on_enter",
			fmt.Sprintf(`{"type":"publish","subject":%q}`, subject+"probe")))
		if err == nil {
			t.Fatalf("a world rule may publish onto the agentic loop's %q input (%s); "+
				"that lane reaches a model, so it belongs inside a reserved subject root",
				port.Name, port.Subject)
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("publishing to the loop's %q input was refused for the wrong reason: %v", port.Name, err)
		}
	}
}

// A name assembled at fire time is a name the loader cannot read, and both
// reserved positions are substitution-resolved by the rule engine
// (processor/rule/actions.go substitutes Action.Predicate and Action.Subject
// before use). A gate that checked only literals would be evaded by a template
// and would report success while doing so.
func TestLoadPackage_RefusesAssembledNamesInTheReservedPositions(t *testing.T) {
	cases := map[string]string{
		"templated triple predicate": ruleJSON("world_templates_predicate", ``, "on_enter",
			`{"type":"add_triple","predicate":"$message.predicate","object":"x"}`),
		"templated publish subject": ruleJSON("world_templates_subject", ``, "on_enter",
			`{"type":"publish","subject":"semmachina.$entity.type.narrating"}`),
	}

	for name, ruleFile := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadWithRule(t, ruleFile)
			if err == nil {
				t.Fatal("LoadPackage accepted a reserved-position name assembled at fire time")
			}
			if !strings.Contains(err.Error(), "substitution") {
				t.Fatalf("refusal %q does not say why an assembled name cannot be checked", err)
			}
		})
	}
}

// Fail closed on an action shape this loader has never classified. The gate
// decides per action TYPE what can reach the engine; an unrecognized type is
// either an author's typo (which the rule engine would otherwise swallow until
// fire time) or an upstream capability nobody has checked against this
// boundary. Both deserve a refusal at import.
func TestLoadPackage_RefusesAnActionTypeTheBoundaryHasNotClassified(t *testing.T) {
	err := loadWithRule(t, ruleJSON("world_invents_action", ``, "on_enter",
		`{"type":"teleport","subject":"anywhere"}`))
	if err == nil {
		t.Fatal("LoadPackage accepted an action type the boundary cannot classify")
	}
	for _, want := range []string{"world_invents_action", "teleport", "on_enter[0]"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// The gate must not read as "no world rules allowed". A world reaction — a
// world fact crossing a threshold, a world fact changing in response — is
// exactly the content CLAUDE.md names as legitimate, and it must load.
//
// The second action is the sharp half: a triple action's `subject` is an
// ENTITY ID override, not a NATS subject, and substitution there is how a rule
// writes onto a related entity. A gate that refused every `$` would refuse
// ordinary authoring while believing it was refusing an evasion.
func TestLoadPackage_AdmitsAGenuineWorldReaction(t *testing.T) {
	reaction := `{"id":"world_supplies_are_spent","type":"expression","name":"Spent supplies leave the scene",` +
		`"enabled":true,"entity":{"pattern":"*.semmachina.*.*.item.*"},` +
		`"conditions":[{"field":"item.attribute.quantity","operator":"lte","value":0}],` +
		`"logic":"and","max_iterations":4,"on_enter":[` +
		`{"type":"remove_triple","predicate":"world.location.current"},` +
		`{"type":"add_triple","subject":"$entity.triple.world.location.current",` +
		`"predicate":"world.relation.knows","object":"$entity.id"}]}`

	if err := loadWithRule(t, reaction); err != nil {
		t.Fatalf("LoadPackage refused a legitimate world reaction: %v", err)
	}
}

// A world that reacts to nothing is a legitimate world, and the "at least one
// rule file" requirement is what put a fake rule in the starter package to
// begin with. Personas stay required: the turn loop cannot run without an
// adjudicator and a narrator, and that absence is a broken package rather than
// a quiet one.
func TestLoadPackage_AcceptsAWorldWithNoRules(t *testing.T) {
	fsys := minimalPackageFS()
	delete(fsys, "rules/00-stub.json")

	pkg, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage refused a world with no reactions: %v", err)
	}
	if len(pkg.RuleFiles) != 0 {
		t.Fatalf("RuleFiles = %v, want none", pkg.RuleFiles)
	}
}

// The shipped starter world must demonstrate the real thing. A stub with an
// empty action list taught an author that a world rule is a turn rule with the
// action left out — which is the one shape this boundary refuses.
func TestStarterWorld_ShipsAWorldReactionRatherThanATurnRule(t *testing.T) {
	pkg := starterPackage(t)
	if len(pkg.RuleFiles) == 0 {
		t.Fatal("the starter world ships no rules, so it demonstrates no world content")
	}

	fsys, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("fixtures.StarterWorld: %v", err)
	}
	for _, name := range pkg.RuleFiles {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var definitions []rule.Definition
		if err := json.Unmarshal(data, &definitions); err != nil {
			var single rule.Definition
			if err := json.Unmarshal(data, &single); err != nil {
				t.Fatalf("%s is not a rule file: %v", name, err)
			}
			definitions = []rule.Definition{single}
		}
		for _, definition := range definitions {
			if !definition.Enabled {
				t.Fatalf("%s rule %q is disabled; a disabled rule demonstrates nothing at runtime",
					name, definition.ID)
			}
			actions := append(append(append(append(
				append([]rule.Action{}, definition.OnEnter...), definition.OnExit...),
				definition.WhileTrue...), definition.OnRecovery...), definition.Actions...)
			if len(actions) == 0 {
				t.Fatalf("%s rule %q has no actions; that is the stub shape this task removed",
					name, definition.ID)
			}
			// The cap is asserted on the ACTION, because that is the field the
			// rule engine enforces: Definition.MaxIterations is recorded into
			// MatchState and republished as $state.max_iterations for a
			// when-guard, and nothing checks it for an expression rule. An
			// exemplar capped at the definition level would teach an inert
			// field — and a future "world rules must be capped" gate written
			// against that field would pass every uncapped world.
			if definition.MaxIterations != 0 {
				t.Fatalf("%s rule %q carries a definition-level max_iterations; that field is not enforced "+
					"for expression rules, so an exemplar must not teach it", name, definition.ID)
			}
			for index, action := range actions {
				if action.MaxIterations == nil || *action.MaxIterations <= 0 {
					t.Fatalf("%s rule %q action[%d] carries no enforced iteration cap "+
						"(Action.MaxIterations); world rules are capped content", name, definition.ID, index)
				}
			}
		}
	}
}
