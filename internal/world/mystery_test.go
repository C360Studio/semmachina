package world_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

func mysteryPackageFS(entityLines string) fstest.MapFS {
	manifest := strings.Replace(goodManifest, "id: starter", "id: mystery-test", 1)
	return fstest.MapFS{
		world.ManifestFile:       &fstest.MapFile{Data: []byte(manifest)},
		world.EntitiesFile:       &fstest.MapFile{Data: []byte(entityLines)},
		"personas/narrator.json": &fstest.MapFile{Data: []byte(stubPersona)},
	}
}

func completeMysteryLines() string {
	var lines []string
	lines = append(lines,
		`{"local_id":"village","type":"scene","triples":[{"predicate":"world.entity.name","object":"Village"}]}`,
		`{"local_id":"method","type":"item","triples":[{"predicate":"world.entity.name","object":"The method"}]}`,
		`{"local_id":"kit","type":"character","triples":[{"predicate":"world.entity.name","object":"Kit Finch"},{"predicate":"companion.candidate.policy","object":"bounded-initiative"}]}`,
	)
	for i := 1; i <= 6; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"local_id":"suspect%d","type":"character","triples":[{"predicate":"world.entity.name","object":"Suspect %d"}]}`,
			i, i))
	}
	for i := 1; i <= 12; i++ {
		status := "clue"
		if i >= 10 {
			status = "red-herring"
		}
		lines = append(lines, fmt.Sprintf(
			`{"local_id":"evidence%d","type":"evidence","triples":[{"predicate":"world.entity.name","object":"Evidence %d"},{"predicate":"evidence.truth.status","object":"%s"}]}`,
			i, i, status))
	}
	for i := 1; i <= 3; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"local_id":"event%d","type":"event","triples":[{"predicate":"world.entity.name","object":"Event %d"},{"predicate":"case.timeline.order","object":%d}]}`,
			i, i, i))
	}
	lines = append(lines,
		`{"local_id":"belief1","type":"belief","triples":[{"predicate":"belief.actor.holder","object":"local:suspect2"},{"predicate":"belief.evidence.target","object":"local:evidence10"},{"predicate":"belief.stance.current","object":"affirms"}]}`,
		`{"local_id":"knowledge1","type":"knowledge","triples":[{"predicate":"knowledge.actor.holder","object":"local:kit"},{"predicate":"knowledge.evidence.target","object":"local:evidence1"}]}`,
	)

	triples := []string{
		`{"predicate":"world.entity.name","object":"A complete case"}`,
		`{"predicate":"case.solution.culprit","object":"local:suspect1"}`,
		`{"predicate":"case.solution.method","object":"local:method"}`,
		`{"predicate":"case.solution.motive","object":"local:evidence2"}`,
		`{"predicate":"case.requirement.suspects","object":6}`,
		`{"predicate":"case.requirement.evidence","object":12}`,
	}
	for i := 1; i <= 6; i++ {
		triples = append(triples, fmt.Sprintf(
			`{"predicate":"case.member.suspect","object":"local:suspect%d"}`, i))
	}
	for i := 1; i <= 12; i++ {
		triples = append(triples, fmt.Sprintf(
			`{"predicate":"case.member.evidence","object":"local:evidence%d"}`, i))
	}
	for i := 1; i <= 3; i++ {
		triples = append(triples, fmt.Sprintf(
			`{"predicate":"case.member.timeline","object":"local:event%d"}`, i))
	}
	lines = append(lines, fmt.Sprintf(
		`{"local_id":"case1","type":"case","triples":[%s]}`, strings.Join(triples, ",")))
	return strings.Join(lines, "\n") + "\n"
}

func TestLoadPackage_ValidatesACompleteTypedMystery(t *testing.T) {
	pkg, err := world.LoadPackage(mysteryPackageFS(completeMysteryLines()), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	if pkg.Mystery == nil {
		t.Fatal("complete mystery loaded without a typed mystery record")
	}
	if len(pkg.Mystery.Suspects) != 6 || len(pkg.Mystery.Evidence) != 12 {
		t.Fatalf("mystery counts = %d suspects, %d evidence", len(pkg.Mystery.Suspects), len(pkg.Mystery.Evidence))
	}
	if pkg.Mystery.Solution.Culprit != "suspect1" || pkg.Mystery.Solution.Method != "method" ||
		pkg.Mystery.Solution.Motive != "evidence2" {
		t.Fatalf("solution = %+v", pkg.Mystery.Solution)
	}
	for index, event := range pkg.Mystery.Timeline {
		if event.Order != index+1 {
			t.Fatalf("timeline[%d].Order = %d", index, event.Order)
		}
	}
	if len(pkg.Mystery.CompanionCandidates) != 1 || pkg.Mystery.CompanionCandidates[0] != "kit" {
		t.Fatalf("companion candidates = %v", pkg.Mystery.CompanionCandidates)
	}
}

func TestLoadPackage_RejectsIncompleteMysteriesBeforeMaterialization(t *testing.T) {
	base := completeMysteryLines()
	cases := map[string]struct {
		old, replacement, field string
	}{
		"missing culprit":            {`{"predicate":"case.solution.culprit","object":"local:suspect1"},`, "", "culprit"},
		"wrong suspect cardinality":  {`{"predicate":"case.requirement.suspects","object":6}`, `{"predicate":"case.requirement.suspects","object":5}`, "suspects"},
		"wrong evidence cardinality": {`{"predicate":"case.requirement.evidence","object":12}`, `{"predicate":"case.requirement.evidence","object":11}`, "evidence"},
		"unordered timeline":         {`{"predicate":"case.timeline.order","object":2}`, `{"predicate":"case.timeline.order","object":3}`, "timeline"},
		"motive outside evidence":    {`{"predicate":"case.solution.motive","object":"local:evidence2"}`, `{"predicate":"case.solution.motive","object":"local:evidence99"}`, "motive"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lines := strings.Replace(base, tc.old, tc.replacement, 1)
			_, err := world.LoadPackage(mysteryPackageFS(lines), world.LoadOptions{})
			if err == nil {
				t.Fatal("LoadPackage accepted an incomplete mystery")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.field) {
				t.Fatalf("error %q does not name %q", err, tc.field)
			}
		})
	}
}

func TestLoadPackage_RequiresMysteryBeliefKnowledgeAndCompanionSeeds(t *testing.T) {
	base := completeMysteryLines()
	cases := map[string]struct {
		remove string
		field  string
	}{
		"missing belief": {
			remove: `{"local_id":"belief1","type":"belief","triples":[{"predicate":"belief.actor.holder","object":"local:suspect2"},{"predicate":"belief.evidence.target","object":"local:evidence10"},{"predicate":"belief.stance.current","object":"affirms"}]}` + "\n",
			field:  "belief",
		},
		"missing knowledge seed": {
			remove: `{"local_id":"knowledge1","type":"knowledge","triples":[{"predicate":"knowledge.actor.holder","object":"local:kit"},{"predicate":"knowledge.evidence.target","object":"local:evidence1"}]}` + "\n",
			field:  "knowledge",
		},
		"missing companion candidate": {
			remove: `,{"predicate":"companion.candidate.policy","object":"bounded-initiative"}`,
			field:  "companion",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lines := strings.Replace(base, tc.remove, "", 1)
			_, err := world.LoadPackage(mysteryPackageFS(lines), world.LoadOptions{})
			if err == nil {
				t.Fatal("LoadPackage accepted a mystery missing a required seed record")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.field) {
				t.Fatalf("error %q does not name %q", err, tc.field)
			}
		})
	}
}

func TestLoadPackage_RejectsDuplicateCompanionCandidatePolicyExplicitly(t *testing.T) {
	duplicate := `,{"predicate":"companion.candidate.policy","object":"bounded-initiative"}`
	lines := strings.Replace(completeMysteryLines(),
		`{"predicate":"companion.candidate.policy","object":"bounded-initiative"}`,
		`{"predicate":"companion.candidate.policy","object":"bounded-initiative"}`+duplicate, 1)
	_, err := world.LoadPackage(mysteryPackageFS(lines), world.LoadOptions{})
	if err == nil {
		t.Fatal("LoadPackage silently treated duplicate companion policy as no candidate")
	}
	for _, want := range []string{"kit", "companion", "policy", "exactly one"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestBellweatherFixture_LoadsThroughTheOrdinaryPackagePath(t *testing.T) {
	pkg, err := world.LoadPackage(os.DirFS("../../fixtures/worlds/bellweather-maze"), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage(Bellweather): %v", err)
	}
	if pkg.Manifest.ID != "bellweather-maze" || pkg.Mystery == nil {
		t.Fatalf("Bellweather package = id %q mystery %+v", pkg.Manifest.ID, pkg.Mystery)
	}
	if len(pkg.Mystery.Suspects) != 6 || len(pkg.Mystery.Evidence) != 12 {
		t.Fatalf("Bellweather has %d suspects and %d evidence", len(pkg.Mystery.Suspects), len(pkg.Mystery.Evidence))
	}
	wantSolution := world.MysterySolution{
		Culprit: "judith-bell", Method: "bell-wire", Motive: "evidence-motive",
	}
	if pkg.Mystery.Solution != wantSolution {
		t.Fatalf("Bellweather solution = %+v, want %+v", pkg.Mystery.Solution, wantSolution)
	}
	if len(pkg.Mystery.Timeline) != 5 {
		t.Fatalf("Bellweather timeline has %d events, want 5", len(pkg.Mystery.Timeline))
	}
	for index, event := range pkg.Mystery.Timeline {
		if event.Order != index+1 {
			t.Fatalf("Bellweather timeline[%d] order = %d, want %d", index, event.Order, index+1)
		}
	}
	if len(pkg.Mystery.Beliefs) == 0 || len(pkg.Mystery.KnowledgeSeeds) == 0 {
		t.Fatalf("Bellweather beliefs=%d knowledge seeds=%d, both must be nonempty",
			len(pkg.Mystery.Beliefs), len(pkg.Mystery.KnowledgeSeeds))
	}
	redHerrings := 0
	for _, evidenceID := range pkg.Mystery.Evidence {
		entity := entityByLocalID(t, pkg.Entities, evidenceID)
		for _, fact := range entity.Facts {
			if fact.Predicate == vocabulary.EvidenceTruthStatusCurrent &&
				fact.Literal == string(vocabulary.EvidenceTruthRedHerring) {
				redHerrings++
			}
		}
	}
	if redHerrings < 2 {
		t.Fatalf("Bellweather has %d red herrings, want at least 2", redHerrings)
	}
	if len(pkg.Mystery.CompanionCandidates) != 1 || pkg.Mystery.CompanionCandidates[0] != "kit-finch" {
		t.Fatalf("Bellweather companion candidates = %v, want exactly Kit", pkg.Mystery.CompanionCandidates)
	}
	if !containsEntity(pkg.Entities, "kit-finch", vocabulary.EntityKindCharacter) {
		t.Fatal("Bellweather does not author Kit Finch as a character")
	}
	kit := entityByLocalID(t, pkg.Entities, "kit-finch")
	var policies []any
	for _, fact := range kit.Facts {
		if fact.Predicate == vocabulary.CompanionCandidatePolicy {
			policies = append(policies, fact.Literal)
		}
	}
	if len(policies) != 1 || policies[0] != string(vocabulary.CompanionPolicyBoundedInitiative) {
		t.Fatalf("Kit companion policies = %v, want one bounded-initiative policy", policies)
	}
	plan, err := pkg.Resolve(world.InstanceConfig{
		Org:     "c360",
		WorldNS: "bellweather-test",
		Player: world.PlayerBinding{
			LocalID: "player1", Name: "Investigator", Character: "local:rowan-vale",
		},
	})
	if err != nil {
		t.Fatalf("Resolve(Bellweather): %v", err)
	}
	publisher := &recordingPublisher{}
	importer, err := world.NewImporter(publisher)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	if _, err := importer.Import(t.Context(), plan); err != nil {
		t.Fatalf("Import(Bellweather): %v", err)
	}
	if len(publisher.messages) != len(plan.Entities) {
		t.Fatalf("Bellweather import published %d of %d entities", len(publisher.messages), len(plan.Entities))
	}
}

func entityByLocalID(t *testing.T, entities []world.TemplateEntity, localID string) world.TemplateEntity {
	t.Helper()
	for _, entity := range entities {
		if entity.LocalID == localID {
			return entity
		}
	}
	t.Fatalf("fixture has no entity %q", localID)
	return world.TemplateEntity{}
}

func containsEntity(entities []world.TemplateEntity, localID string, kind vocabulary.EntityKind) bool {
	for _, entity := range entities {
		if entity.LocalID == localID && entity.Kind == kind {
			return true
		}
	}
	return false
}
