package boot

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/c360studio/semstreams/model"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	sspersona "github.com/c360studio/semstreams/persona"
	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/payload"
	enginepersona "github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

func TestRuleProcessorConfig_ComposesOnlySelectedMechanicsAfterFixedEngineRules(t *testing.T) {
	registerRuleSelectionPredicates(t)
	engine := ruleSelectionEngine(t,
		[]string{"rules/20-second.json", "rules/10-first.json"},
		map[string]string{
			"rules/10-first.json":  worldRuleJSON("world-first", "item.attribute.quantity"),
			"rules/20-second.json": worldRuleJSON("world-second", "item.attribute.quantity"),
			// Canonical but undeclared: package preflight accepts its syntax and
			// scope, while rule.ValidateDefinition would reject it if boot read the
			// unselected file into the runtime configuration.
			"rules/unselected.json": worldRuleJSON("unselected-runtime-invalid", "weather.condition.storm"),
		},
	)

	baseline := fixedRuleConfig(t)
	raw, err := engine.ruleProcessorConfig()
	if err != nil {
		t.Fatalf("ruleProcessorConfig: %v", err)
	}
	var got rule.Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode composed rule config: %v", err)
	}
	if len(got.InlineRules) != len(baseline.InlineRules)+2 {
		t.Fatalf("inline rules = %d, want %d engine + 2 selected", len(got.InlineRules), len(baseline.InlineRules))
	}
	if !reflect.DeepEqual(got.InlineRules[:len(baseline.InlineRules)], baseline.InlineRules) {
		t.Fatal("composing world mechanics changed or reordered the fixed engine definitions")
	}
	wantPatterns := append(
		append([]string(nil), baseline.EntityWatchBuckets[rulepack.EntityStatesBucket]...),
		"c360.semmachina.mechanics.starter.item.*",
	)
	if gotPatterns := got.EntityWatchBuckets[rulepack.EntityStatesBucket]; !reflect.DeepEqual(gotPatterns, wantPatterns) {
		t.Fatalf("entity watch patterns = %v, want fixed patterns then selected world pattern %v",
			gotPatterns, wantPatterns)
	}
	normalized := got
	normalized.InlineRules = baseline.InlineRules
	normalized.EntityWatchBuckets = baseline.EntityWatchBuckets
	if !reflect.DeepEqual(normalized, baseline) {
		t.Fatal("world mechanics changed fixed processor settings outside inline rules and watch patterns")
	}
	if gotIDs := []string{
		got.InlineRules[len(baseline.InlineRules)].ID,
		got.InlineRules[len(baseline.InlineRules)+1].ID,
	}; !reflect.DeepEqual(gotIDs, []string{"world-first", "world-second"}) {
		t.Fatalf("selected world rule order = %v", gotIDs)
	}
	for _, definition := range got.InlineRules {
		if definition.ID == "unselected-runtime-invalid" {
			t.Fatal("an unselected mechanics file entered the runtime rule configuration")
		}
	}
}

func TestRuleProcessorConfig_UsesConstructionBoundMechanicsAfterFileMutation(t *testing.T) {
	registerRuleSelectionPredicates(t)
	engine := ruleSelectionEngine(t,
		[]string{"rules/selected.json"},
		map[string]string{
			"rules/selected.json": worldRuleJSON("selected-safe", "item.attribute.quantity"),
		},
	)
	worldFS := engine.cfg.World.(fstest.MapFS)
	worldFS["rules/selected.json"] = &fstest.MapFile{Data: []byte(
		worldRuleJSON("selected-mutated", "item.attribute.quantity"))}

	raw, err := engine.ruleProcessorConfig()
	if err != nil {
		t.Fatalf("ruleProcessorConfig: %v", err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode composed config: %v", err)
	}
	seen := make(map[string]bool)
	for _, definition := range config.InlineRules {
		seen[definition.ID] = true
	}
	if !seen["selected-safe"] || seen["selected-mutated"] {
		t.Fatalf("construction-bound rules changed after file mutation: ids=%v", seen)
	}
}

func TestRuleProcessorConfig_RejectsRuntimeInvalidSelectedDefinitionWithFileContext(t *testing.T) {
	registerRuleSelectionPredicates(t)
	const file = "rules/runtime-invalid.json"
	const ruleID = "selected-runtime-invalid"
	engine := ruleSelectionEngine(t, []string{file}, map[string]string{
		file: worldRuleJSON(ruleID, "weather.condition.storm"),
	})

	_, err := engine.ruleProcessorConfig()
	if err == nil {
		t.Fatal("ruleProcessorConfig accepted a runtime-invalid selected definition")
	}
	for _, want := range []string{file, ruleID, "ValidateDefinition", "not declared"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("runtime validation refusal %q does not contain %q", err, want)
		}
	}
}

func TestRuleProcessorConfig_RefusesSelectedRuleIDCollisions(t *testing.T) {
	registerRuleSelectionPredicates(t)
	engineRuleID := fixedRuleConfig(t).InlineRules[0].ID
	for name, tc := range map[string]struct {
		selected []string
		files    map[string]string
		id       string
	}{
		"between selected files": {
			selected: []string{"rules/a.json", "rules/b.json"},
			files: map[string]string{
				"rules/a.json": worldRuleJSON("duplicate-world-rule", "item.attribute.quantity"),
				"rules/b.json": worldRuleJSON("duplicate-world-rule", "item.attribute.quantity"),
			},
			id: "duplicate-world-rule",
		},
		"against fixed engine rule": {
			selected: []string{"rules/collision.json"},
			files: map[string]string{
				"rules/collision.json": worldRuleJSON(engineRuleID, "item.attribute.quantity"),
			},
			id: engineRuleID,
		},
	} {
		t.Run(name, func(t *testing.T) {
			engine := ruleSelectionEngine(t, tc.selected, tc.files)
			_, err := engine.ruleProcessorConfig()
			if err == nil {
				t.Fatal("ruleProcessorConfig accepted a duplicate selected rule id")
			}
			for _, want := range []string{"duplicate", tc.id} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("collision refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

func TestRuleProcessorConfig_AcceptsEmptySelectedMechanicsPack(t *testing.T) {
	registerRuleSelectionPredicates(t)
	engine := ruleSelectionEngine(t, nil, map[string]string{
		"rules/unselected.json": worldRuleJSON("unselected-runtime-invalid", "weather.condition.storm"),
	})

	raw, err := engine.ruleProcessorConfig()
	if err != nil {
		t.Fatalf("empty selected mechanics pack: %v", err)
	}
	var got rule.Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode composed rule config: %v", err)
	}
	if want := fixedRuleConfig(t); !reflect.DeepEqual(got, want) {
		t.Fatal("empty mechanics pack changed the fixed engine rule configuration")
	}
}

func TestNarrowWorldDefinition_ProducesDisjointInstancePatterns(t *testing.T) {
	authored := rule.Definition{
		ID:   "world-scoped",
		Type: "expression",
		Entity: rule.EntityConfig{
			Pattern: "*.*.*.*.item.*",
		},
		RelatedPatterns: []string{
			"*.semmachina.*.*.location.*",
			"c360.*.*.starter.character.rook",
		},
	}

	for _, tc := range []struct {
		worldNS string
		primary string
		related []string
	}{
		{
			worldNS: "one",
			primary: "c360.semmachina.one.starter.item.*",
			related: []string{
				"c360.semmachina.one.starter.location.*",
				"c360.semmachina.one.starter.character.rook",
			},
		},
		{
			worldNS: "two",
			primary: "c360.semmachina.two.starter.item.*",
			related: []string{
				"c360.semmachina.two.starter.location.*",
				"c360.semmachina.two.starter.character.rook",
			},
		},
	} {
		t.Run(tc.worldNS, func(t *testing.T) {
			plan := &world.Plan{Org: "c360", WorldNS: tc.worldNS, TemplateID: "starter"}
			got, err := narrowWorldDefinition(plan, authored)
			if err != nil {
				t.Fatalf("narrowWorldDefinition: %v", err)
			}
			if got.Entity.Pattern != tc.primary || !reflect.DeepEqual(got.RelatedPatterns, tc.related) {
				t.Fatalf("narrowed patterns = primary %q related %v, want %q and %v",
					got.Entity.Pattern, got.RelatedPatterns, tc.primary, tc.related)
			}
		})
	}
}

func TestRuleProcessorConfig_WatchesOnlyNarrowedPrimaryWorldPatterns(t *testing.T) {
	registerRuleSelectionPredicates(t)
	definition := strings.Replace(
		worldRuleJSON("world-related", "item.attribute.quantity"),
		`"conditions":`,
		`"related_patterns":["*.*.*.*.location.*"],"conditions":`,
		1,
	)
	engine := ruleSelectionEngine(t, []string{"rules/selected.json"}, map[string]string{
		"rules/selected.json": definition,
	})
	raw, err := engine.ruleProcessorConfig()
	if err != nil {
		t.Fatalf("ruleProcessorConfig: %v", err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	worldDefinition := config.InlineRules[len(config.InlineRules)-1]
	if got, want := worldDefinition.RelatedPatterns,
		[]string{"c360.semmachina.mechanics.starter.location.*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowed related patterns = %v, want %v", got, want)
	}
	patterns := config.EntityWatchBuckets[rulepack.EntityStatesBucket]
	if slices.Contains(patterns, "c360.semmachina.mechanics.starter.location.*") {
		t.Fatalf("related pattern entered primary entity watchers: %v", patterns)
	}
	if !slices.Contains(patterns, "c360.semmachina.mechanics.starter.item.*") {
		t.Fatalf("narrowed primary pattern missing from entity watchers: %v", patterns)
	}
}

func TestBindSelectedExperience_RechecksAggregatePersonaRoles(t *testing.T) {
	plan := &world.Plan{Experience: world.ResolvedExperience{
		PersonaPack:  "voices",
		PersonaFiles: []string{"personas/a.json", "personas/b.json"},
	}}
	fsys := fstest.MapFS{
		"personas/a.json": {Data: []byte(
			`{"id":"a","category":100,"roles":["adjudicator"],"content":"Judge A."}`)},
		"personas/b.json": {Data: []byte(
			`{"id":"b","category":100,"roles":["adjudicator"],"content":"Judge B."}`)},
	}
	_, _, err := bindSelectedExperience(fsys, plan)
	if err == nil {
		t.Fatal("bindSelectedExperience accepted a cache without a narrator")
	}
	for _, want := range []string{"voices", "narrator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("aggregate role refusal %q does not name %q", err, want)
		}
	}
}

func TestValidateCachedPersonas_RejectsEveryRecordBeforePersistence(t *testing.T) {
	plan := &world.Plan{Experience: world.ResolvedExperience{
		PersonaFiles: []string{"personas/valid.json", "personas/invalid.json"},
	}}
	records := []sspersona.Persona{
		{ID: "valid", Roles: []string{"narrator"}, Content: "Narrate."},
		{ID: "invalid", Content: "Global prompt injection."},
	}
	err := validateCachedPersonas(plan, records)
	if err == nil || !strings.Contains(err.Error(), "personas/invalid.json") ||
		!strings.Contains(err.Error(), "declares no roles") {
		t.Fatalf("cached persona validation = %v", err)
	}
}

func TestNarrowWorldDefinition_RefusesUnscopeableOrIncompatibleRules(t *testing.T) {
	plan := &world.Plan{Org: "c360", WorldNS: "one", TemplateID: "starter"}
	for name, definition := range map[string]rule.Definition{
		"incompatible primary literal": {
			ID: "bad-primary", Type: "expression",
			Entity: rule.EntityConfig{Pattern: "other.semmachina.*.*.item.*"},
		},
		"invalid primary shape": {
			ID: "invalid-primary", Type: "expression",
			Entity: rule.EntityConfig{Pattern: "*.*.*.item.*"},
		},
		"incompatible related literal": {
			ID: "bad-related", Type: "expression",
			Entity:          rule.EntityConfig{Pattern: "*.*.*.*.item.*"},
			RelatedPatterns: []string{"*.semmachina.other.*.location.*"},
		},
		"invalid related shape": {
			ID: "invalid-related", Type: "expression",
			Entity:          rule.EntityConfig{Pattern: "*.*.*.*.item.*"},
			RelatedPatterns: []string{"*.*.*.location.*"},
		},
		"expression without entity pattern": {
			ID: "global-expression", Type: "expression",
		},
		"cron has no instance entity scope": {
			ID: "global-cron", Type: "cron",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := narrowWorldDefinition(plan, definition)
			if err == nil {
				t.Fatal("narrowWorldDefinition accepted an unscoped world rule")
			}
			if !strings.Contains(err.Error(), definition.ID) {
				t.Fatalf("scope refusal %q does not name rule %q", err, definition.ID)
			}
		})
	}
}

func registerRuleSelectionPredicates(t *testing.T) {
	t.Helper()
	if err := vocabulary.RegisterPredicates(); err != nil {
		t.Fatalf("register predicates: %v", err)
	}
}

func fixedRuleConfig(t *testing.T) rule.Config {
	t.Helper()
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatalf("fixed rule config: %v", err)
	}
	var config rule.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode fixed rule config: %v", err)
	}
	return config
}

func ruleSelectionEngine(t *testing.T, selected []string, files map[string]string) *Engine {
	t.Helper()
	source, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("StarterWorld: %v", err)
	}
	worldFS := make(fstest.MapFS)
	if err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := fs.ReadFile(source, name)
		if readErr != nil {
			return readErr
		}
		worldFS[name] = &fstest.MapFile{Data: data}
		return nil
	}); err != nil {
		t.Fatalf("copy starter world: %v", err)
	}
	for name, data := range files {
		worldFS[name] = &fstest.MapFile{Data: []byte(data)}
	}
	worldFS[world.PacksFile] = &fstest.MapFile{Data: []byte(ruleSelectionCatalog(selected, files))}

	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		t.Fatalf("register framework payloads: %v", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		t.Fatalf("register SemMachina payloads: %v", err)
	}
	models := &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"stub": {
				Provider: "openai", URL: "http://127.0.0.1:1/v1", Model: "stub",
				MaxTokens: 128_000, SupportsTools: true,
			},
		},
		Capabilities: make(map[string]*model.CapabilityConfig),
		Defaults:     model.DefaultsConfig{Model: "stub"},
	}
	for _, spec := range enginepersona.Specs() {
		models.Capabilities[spec.Capability] = &model.CapabilityConfig{Preferred: []string{"stub"}}
	}
	engine, err := New(Config{
		Org: "c360", WorldNS: "mechanics",
		Player: PlayerConfig{
			LocalID: "one", Name: "Player", Character: "local:rook", Credential: "secret",
		},
		Models: models, World: worldFS, Registry: registry,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func ruleSelectionCatalog(selected []string, files map[string]string) string {
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}
	var unselected []string
	for name := range files {
		if !selectedSet[name] {
			unselected = append(unselected, name)
		}
	}
	// Catalog loading sorts each file list, so intentionally leave the selected
	// input in caller order: the resolved Plan, not map or authoring order, is the
	// deterministic runtime contract under test.
	var catalog strings.Builder
	catalog.WriteString("version: 1\ndefaults:\n  persona_pack: selected\n  mechanics_pack: selected\n")
	catalog.WriteString("persona_packs:\n  selected:\n    files:\n")
	catalog.WriteString("      - personas/adjudicator.json\n      - personas/narrator.json\n")
	catalog.WriteString("mechanics_packs:\n  selected:\n    files:")
	if len(selected) == 0 {
		catalog.WriteString(" []\n")
	} else {
		catalog.WriteByte('\n')
		for _, name := range selected {
			fmt.Fprintf(&catalog, "      - %s\n", name)
		}
	}
	if len(unselected) != 0 {
		catalog.WriteString("  unselected:\n    files:\n")
		for _, name := range unselected {
			fmt.Fprintf(&catalog, "      - %s\n", name)
		}
	}
	return catalog.String()
}

func worldRuleJSON(id, condition string) string {
	return fmt.Sprintf(`{"id":%q,"type":"expression","name":%q,"enabled":true,`+
		`"entity":{"pattern":"*.semmachina.*.*.item.*"},"conditions":[`+
		`{"field":%q,"operator":"lte","value":0}],"logic":"and","on_enter":[`+
		`{"type":"remove_triple","predicate":"world.location.current","max_iterations":1}]}`,
		id, id, condition)
}
