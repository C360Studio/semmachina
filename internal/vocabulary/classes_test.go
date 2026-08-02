package vocabulary_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// closedSet describes one vocabulary kind for the shared behavior table:
// what it contains, how it parses, and which near-miss values must be
// rejected. The near-misses are deliberately the plausible ones — case
// variants, whitespace, separator swaps, and reasonable-sounding words a
// model would actually emit.
type closedSet struct {
	kind      vocabulary.Kind
	members   []string
	parse     func(string) (string, error)
	nearMiss  []string
	sampleBad string
}

func strs[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}

func asStringParser[T ~string](fn func(string) (T, error)) func(string) (string, error) {
	return func(s string) (string, error) {
		v, err := fn(s)
		return string(v), err
	}
}

func closedSets() []closedSet {
	return []closedSet{
		{
			kind:      vocabulary.KindPlausibility,
			members:   strs(vocabulary.Plausibilities()),
			parse:     asStringParser(vocabulary.ParsePlausibility),
			nearMiss:  []string{"Plausible", "PLAUSIBLE", " plausible", "plausible ", "possible", "likely", "maybe"},
			sampleBad: "likely",
		},
		{
			kind:      vocabulary.KindRisk,
			members:   strs(vocabulary.Risks()),
			parse:     asStringParser(vocabulary.ParseRisk),
			nearMiss:  []string{"None", "medium", "severe", "extreme", "no", "0"},
			sampleBad: "severe",
		},
		{
			kind:      vocabulary.KindConsequence,
			members:   strs(vocabulary.Consequences()),
			parse:     asStringParser(vocabulary.ParseConsequence),
			nearMiss:  []string{"Harm", "damage", "injury", "consequence", "set-back", "set_back"},
			sampleBad: "damage",
		},
		{
			kind:      vocabulary.KindOutcomeBand,
			members:   strs(vocabulary.OutcomeBands()),
			parse:     asStringParser(vocabulary.ParseOutcomeBand),
			nearMiss:  []string{"Miss", "success", "failure", "partial_success", "critical", "fail"},
			sampleBad: "success",
		},
		{
			kind:      vocabulary.KindModifierSource,
			members:   strs(vocabulary.ModifierSources()),
			parse:     asStringParser(vocabulary.ParseModifierSource),
			nearMiss:  []string{"Trait", "gear", "item", "help", "luck", "bonus"},
			sampleBad: "luck",
		},
		{
			kind:      vocabulary.KindEffectType,
			members:   strs(vocabulary.EffectTypes()),
			parse:     asStringParser(vocabulary.ParseEffectType),
			nearMiss:  []string{"set-attribute", "setAttribute", "SET_ATTRIBUTE", "delete_entity", "create_entity", "damage"},
			sampleBad: "delete_entity",
		},
		{
			kind:      vocabulary.KindStatus,
			members:   strs(vocabulary.Statuses()),
			parse:     asStringParser(vocabulary.ParseStatus),
			nearMiss:  []string{"Dead", "injured", "hurt", "poisoned", "ok", "alive"},
			sampleBad: "poisoned",
		},
		{
			kind:      vocabulary.KindAttribute,
			members:   strs(vocabulary.Attributes()),
			parse:     asStringParser(vocabulary.ParseAttribute),
			nearMiss:  []string{"Health", "hp", "hit_points", "mana", "strength", "gold"},
			sampleBad: "hp",
		},
		{
			kind:      vocabulary.KindRelation,
			members:   strs(vocabulary.Relations()),
			parse:     asStringParser(vocabulary.ParseRelation),
			nearMiss:  []string{"allied_with", "alliedWith", "AlliedWith", "friend_of", "loves", "owns"},
			sampleBad: "owns",
		},
		{
			kind:      vocabulary.KindEntityKind,
			members:   strs(vocabulary.EntityKinds()),
			parse:     asStringParser(vocabulary.ParseEntityKind),
			nearMiss:  []string{"Character", "npc", "monster", "region", "place", "creature"},
			sampleBad: "npc",
		},
		{
			kind:      vocabulary.KindTurnPhase,
			members:   strs(vocabulary.TurnPhases()),
			parse:     asStringParser(vocabulary.ParseTurnPhase),
			nearMiss:  []string{"Accepted", "pending", "done", "error", "in_progress", "narrated"},
			sampleBad: "done",
		},
		{
			kind:      vocabulary.KindChannelAdapter,
			members:   strs(vocabulary.ChannelAdapters()),
			parse:     asStringParser(vocabulary.ParseChannelAdapter),
			nearMiss:  []string{"WebSocket", "ws", "http", "slack", "email", "sms"},
			sampleBad: "slack",
		},
		{
			kind:      vocabulary.KindMechanic,
			members:   strs(vocabulary.Mechanics()),
			parse:     asStringParser(vocabulary.ParseMechanic),
			nearMiss:  []string{"2d6-pbta", "2d6-pbta/v2", "2d6", "d20", "2D6-PBTA/V1"},
			sampleBad: "2d6-pbta/v2",
		},
		{
			kind:      vocabulary.KindRNG,
			members:   strs(vocabulary.RNGs()),
			parse:     asStringParser(vocabulary.ParseRNG),
			nearMiss:  []string{"pcg", "pcg/v2", "mt19937", "crypto/rand", "PCG/V1"},
			sampleBad: "pcg/v2",
		},
		{
			kind:    vocabulary.KindFailureReason,
			members: strs(vocabulary.FailureReasons()),
			parse:   asStringParser(vocabulary.ParseFailureReason),
			// The near-misses that matter here are PROSE: a reason recorded as a
			// sentence would pass the projection's shape gate and land free text
			// on the graph's rule-matching surface.
			nearMiss: []string{
				"effect_invalid", "Effect-Invalid", "invalid",
				"the target entity does not exist", "rejected",
			},
			sampleBad: "the target entity does not exist",
		},
		{
			kind:    vocabulary.KindRollGateMapping,
			members: strs(vocabulary.RollGateMappings()),
			parse:   asStringParser(vocabulary.ParseRollGateMapping),
			// A version this engine never had is a corrupt or foreign ledger
			// record, not an old one — so the near-misses are the plausible
			// spellings of "some other table decided this".
			nearMiss:  []string{"roll-gate", "roll-gate/v2", "v1", "ROLL-GATE/V1", "requires_roll"},
			sampleBad: "roll-gate/v2",
		},
		{
			kind:      vocabulary.KindEvidenceTruthStatus,
			members:   strs(vocabulary.EvidenceTruthStatuses()),
			parse:     asStringParser(vocabulary.ParseEvidenceTruthStatus),
			nearMiss:  []string{"true", "false", "red_herring", "Red-Herring", "probably"},
			sampleBad: "probably",
		},
		{
			kind:      vocabulary.KindEvidenceRevealKind,
			members:   strs(vocabulary.EvidenceRevealKinds()),
			parse:     asStringParser(vocabulary.ParseEvidenceRevealKind),
			nearMiss:  []string{"question", "share", "Observe", "investigate-ish"},
			sampleBad: "question",
		},
		{
			kind:      vocabulary.KindBeliefStance,
			members:   strs(vocabulary.BeliefStances()),
			parse:     asStringParser(vocabulary.ParseBeliefStance),
			nearMiss:  []string{"believes", "rejects", "guilty", "Affirms", "not-sure"},
			sampleBad: "guilty",
		},
		{
			kind:      vocabulary.KindCompanionPolicy,
			members:   strs(vocabulary.CompanionPolicies()),
			parse:     asStringParser(vocabulary.ParseCompanionPolicy),
			nearMiss:  []string{"proactive", "always", "bounded_initiative", "Reactive"},
			sampleBad: "always",
		},
		{
			kind:      vocabulary.KindHintLevel,
			members:   strs(vocabulary.HintLevels()),
			parse:     asStringParser(vocabulary.ParseHintLevel),
			nearMiss:  []string{"answer", "solution", "next_step", "Nudge"},
			sampleBad: "solution",
		},
		{
			kind:      vocabulary.KindCompanionTrigger,
			members:   strs(vocabulary.CompanionTriggers()),
			parse:     asStringParser(vocabulary.ParseCompanionTrigger),
			nearMiss:  []string{"hint", "player_hint", "Warning"},
			sampleBad: "hint",
		},
		{
			kind:      vocabulary.KindCompanionTriggerSource,
			members:   strs(vocabulary.CompanionTriggerSources()),
			parse:     asStringParser(vocabulary.ParseCompanionTriggerSource),
			nearMiss:  []string{"case_decision", "risk", "Resolved-Risk"},
			sampleBad: "risk",
		},
		{
			kind:      vocabulary.KindCasePhase,
			members:   strs(vocabulary.CasePhases()),
			parse:     asStringParser(vocabulary.ParseCasePhase),
			nearMiss:  []string{"cold-open", "investigating", "resolved", "Discovery"},
			sampleBad: "resolved",
		},
		{
			kind:      vocabulary.KindCaseLifecycleEventKind,
			members:   strs(vocabulary.CaseLifecycleEventKinds()),
			parse:     asStringParser(vocabulary.ParseCaseLifecycleEventKind),
			nearMiss:  []string{"body_observed", "accusation", "case-solved", "Body-Observed"},
			sampleBad: "case-solved",
		},
	}
}

// The defining behavior of a closed set: every member parses back to itself,
// and nothing else parses at all.
func TestClosedSets_AcceptEveryMemberAndRoundTrip(t *testing.T) {
	for _, set := range closedSets() {
		t.Run(string(set.kind), func(t *testing.T) {
			if len(set.members) == 0 {
				t.Fatal("closed set is empty")
			}
			for _, member := range set.members {
				got, err := set.parse(member)
				if err != nil {
					t.Fatalf("member %q was rejected by its own parser: %v", member, err)
				}
				if got != member {
					t.Fatalf("parse did not round-trip: got %q want %q", got, member)
				}
			}
		})
	}
}

func TestClosedSets_RejectEverythingElse(t *testing.T) {
	for _, set := range closedSets() {
		t.Run(string(set.kind), func(t *testing.T) {
			members := make(map[string]bool, len(set.members))
			for _, m := range set.members {
				members[m] = true
			}

			candidates := append([]string{"", " ", "unknown", "null", "0"}, set.nearMiss...)
			for _, candidate := range candidates {
				if members[candidate] {
					t.Fatalf("near-miss corpus entry %q is actually a member; the rejection case is vacuous", candidate)
				}
				if _, err := set.parse(candidate); err == nil {
					t.Fatalf("parser accepted out-of-vocabulary value %q", candidate)
				}
			}
		})
	}
}

// Rejection has to be actionable at the adjudicator tool boundary: the
// caller needs the offending value and the allowed set to hand back to the
// model for self-correction.
func TestClosedSets_RejectionNamesTheKindValueAndAllowedSet(t *testing.T) {
	for _, set := range closedSets() {
		t.Run(string(set.kind), func(t *testing.T) {
			_, err := set.parse(set.sampleBad)
			if err == nil {
				t.Fatalf("expected %q to be rejected", set.sampleBad)
			}

			var unknown *vocabulary.UnknownValueError
			if !errors.As(err, &unknown) {
				t.Fatalf("expected *vocabulary.UnknownValueError, got %T", err)
			}
			if unknown.Kind != set.kind {
				t.Fatalf("error kind: got %q want %q", unknown.Kind, set.kind)
			}
			if unknown.Value != set.sampleBad {
				t.Fatalf("error value: got %q want %q", unknown.Value, set.sampleBad)
			}
			if len(unknown.Allowed) != len(set.members) {
				t.Fatalf("error allowed set: got %v want %v", unknown.Allowed, set.members)
			}
			for _, member := range set.members {
				if !strings.Contains(err.Error(), member) {
					t.Fatalf("error message %q omits allowed member %q", err.Error(), member)
				}
			}
		})
	}
}

// Sets() is what generates the adjudicator's JSON-Schema enums and the rule
// pack's allowed values. If it drifts from the typed accessors, those two
// consumers silently disagree with the Go validator.
func TestSets_MatchTheTypedAccessorsExactly(t *testing.T) {
	sets := vocabulary.Sets()
	if len(sets) != len(closedSets()) {
		t.Fatalf("Sets() has %d kinds but the behavior table covers %d", len(sets), len(closedSets()))
	}

	for _, set := range closedSets() {
		got, ok := sets[set.kind]
		if !ok {
			t.Fatalf("Sets() is missing kind %q", set.kind)
		}
		if len(got) != len(set.members) {
			t.Fatalf("kind %q: Sets() has %v, accessor has %v", set.kind, got, set.members)
		}
		for i := range got {
			if got[i] != set.members[i] {
				t.Fatalf("kind %q index %d: Sets() has %q, accessor has %q", set.kind, i, got[i], set.members[i])
			}
		}
	}

	if len(vocabulary.Kinds()) != len(sets) {
		t.Fatalf("Kinds() reports %d kinds, Sets() has %d", len(vocabulary.Kinds()), len(sets))
	}
}

func TestOutcomeBand_AutoIsNotRollSelectable(t *testing.T) {
	if vocabulary.BandAuto.IsRollBand() {
		t.Fatal("auto must never be selectable by the dice; it is the no-roll band")
	}
	rollBands := vocabulary.RollBands()
	if len(rollBands) != 3 {
		t.Fatalf("roll bands: got %v want three", rollBands)
	}
	for _, b := range rollBands {
		if !b.IsRollBand() {
			t.Fatalf("band %q is listed as a roll band but IsRollBand() is false", b)
		}
		if !b.Valid() {
			t.Fatalf("roll band %q is not in the closed band set", b)
		}
	}
}

func TestTurnPhase_TerminalPhasesAreExactlyCompleteAndFailed(t *testing.T) {
	terminal := map[vocabulary.TurnPhase]bool{
		vocabulary.PhaseComplete: true,
		vocabulary.PhaseFailed:   true,
	}
	for _, phase := range vocabulary.TurnPhases() {
		if got := phase.IsTerminal(); got != terminal[phase] {
			t.Fatalf("phase %q: IsTerminal()=%v want %v", phase, got, terminal[phase])
		}
	}
}
