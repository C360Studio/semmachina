package world_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

const companionLine = `{"local_id":"wren","type":"character","triples":[` +
	`{"predicate":"world.entity.name","object":"Wren"},` +
	`{"predicate":"world.location.current","object":"local:gatehouse"},` +
	`{"predicate":"companion.candidate.policy","object":"bounded-initiative"}]}`

func companionInstance() world.InstanceConfig {
	instance := testInstance()
	instance.Companion = &world.CompanionBinding{
		Character: "local:wren", Policy: vocabulary.CompanionPolicyReactive,
	}
	return instance
}

func TestResolve_MaterializesDeterministicCompleteCompanionBond(t *testing.T) {
	pkg := testPackage(t, rookLine, gatehouseLine, companionLine)
	first, err := pkg.Resolve(companionInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	second, err := pkg.Resolve(companionInstance())
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}

	var bond *world.PlannedEntity
	for index := range first.Entities {
		if first.Entities[index].Kind == vocabulary.EntityKindCompanionBond {
			bond = &first.Entities[index]
		}
	}
	if bond == nil {
		t.Fatal("resolved plan has no companion bond")
	}
	if bond.ID != second.Entities[len(second.Entities)-1].ID {
		t.Fatalf("bond identity is not deterministic: %s vs %s", bond.ID, second.Entities[len(second.Entities)-1].ID)
	}
	want := map[vocabulary.Predicate]any{
		vocabulary.CompanionBondPlayer:    "c360.semmachina.world1.starter.player.p1",
		vocabulary.CompanionBondCharacter: "c360.semmachina.world1.starter.character.wren",
		vocabulary.CompanionBondPolicy:    string(vocabulary.CompanionPolicyReactive),
		vocabulary.CompanionBondHintLevel: string(vocabulary.HintLevelNudge),
	}
	if len(bond.Facts) != len(want) {
		t.Fatalf("bond has %d facts, want exactly %d", len(bond.Facts), len(want))
	}
	for _, fact := range bond.Facts {
		if got, ok := want[fact.Predicate]; !ok || got != fact.Object {
			t.Fatalf("unexpected bond fact: %+v", fact)
		}
	}
}

func TestResolve_CompanionPolicyMustBeCandidateAndMayOnlyTighten(t *testing.T) {
	tests := map[string]struct {
		line   string
		mutate func(*world.InstanceConfig)
		want   string
	}{
		"not candidate": {line: strings.ReplaceAll(companionLine,
			`,{"predicate":"companion.candidate.policy","object":"bounded-initiative"}`, ""), want: "candidate"},
		"unknown companion":            {line: companionLine, mutate: func(i *world.InstanceConfig) { i.Companion.Character = "local:nobody" }, want: "names no entity"},
		"same as controlled character": {line: companionLine, mutate: func(i *world.InstanceConfig) { i.Companion.Character = "local:rook" }, want: "controlled character"},
		"wider than candidate": {line: strings.Replace(companionLine, "bounded-initiative", "reactive", 1),
			mutate: func(i *world.InstanceConfig) { i.Companion.Policy = vocabulary.CompanionPolicyBoundedInitiative }, want: "wider"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			instance := companionInstance()
			if tc.mutate != nil {
				tc.mutate(&instance)
			}
			_, err := testPackage(t, rookLine, gatehouseLine, tc.line).Resolve(instance)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resolve error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolve_NoCompanionBindingMaterializesNoBond(t *testing.T) {
	plan, err := testPackage(t, rookLine, gatehouseLine, companionLine).Resolve(testInstance())
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range plan.Entities {
		if entity.Kind == vocabulary.EntityKindCompanionBond {
			t.Fatal("unselected candidate became active")
		}
	}
}
