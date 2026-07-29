package world_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

const (
	stubRule = `{"id":"stub","type":"expression","name":"stub","enabled":false,` +
		`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[],"logic":"and"}`
	stubPersona = `{"id":"stub/narrator","category":100,"roles":["narrator"],"content":"Narrate."}`
)

func minimalPackageFS() fstest.MapFS {
	return fstest.MapFS{
		world.ManifestFile:       &fstest.MapFile{Data: []byte(goodManifest)},
		world.EntitiesFile:       &fstest.MapFile{Data: []byte(rookLine + "\n" + gatehouseLine + "\n")},
		"rules/00-stub.json":     &fstest.MapFile{Data: []byte(stubRule)},
		"personas/narrator.json": &fstest.MapFile{Data: []byte(stubPersona)},
	}
}

func TestLoadPackage_LoadsAWholePackage(t *testing.T) {
	pkg, err := world.LoadPackage(minimalPackageFS(), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	if pkg.Manifest.ID != "starter" {
		t.Fatalf("manifest id = %q", pkg.Manifest.ID)
	}
	if len(pkg.Entities) != 2 {
		t.Fatalf("loaded %d entities, want 2", len(pkg.Entities))
	}
	if len(pkg.RuleFiles) != 1 || len(pkg.PersonaFiles) != 1 {
		t.Fatalf("rules=%v personas=%v", pkg.RuleFiles, pkg.PersonaFiles)
	}
}

// A package missing a required member is not a package, and the reason has to
// say which one. `rules/` is deliberately absent from this list: a world with
// no reactions is legitimate, and TestLoadPackage_AcceptsAWorldWithNoRules
// holds that line from the other side.
func TestLoadPackage_RequiresEveryPackageMember(t *testing.T) {
	for _, missing := range []string{
		world.ManifestFile, world.EntitiesFile, "personas/narrator.json",
	} {
		t.Run(missing, func(t *testing.T) {
			fsys := minimalPackageFS()
			delete(fsys, missing)

			_, err := world.LoadPackage(fsys, world.LoadOptions{})
			if err == nil {
				t.Fatalf("LoadPackage accepted a package with no %s", missing)
			}
			member := missing
			if dir, _, found := strings.Cut(missing, "/"); found {
				member = dir
			}
			if !strings.Contains(err.Error(), member) {
				t.Fatalf("rejection reason %q does not name the missing member %q", err, member)
			}
		})
	}
}

func TestLoadPackage_RejectsMalformedPackageContent(t *testing.T) {
	cases := map[string]struct {
		file  string
		data  string
		names string
	}{
		"rule that is not JSON": {
			file: "rules/00-stub.json", data: "{oops", names: "well-formed JSON",
		},
		"persona that is not a persona record": {
			file: "personas/narrator.json", data: `{"id":"x","category":100,"content":"c","model":"gpt"}`,
			names: "model",
		},
		"persona with no content": {
			file: "personas/narrator.json", data: `{"id":"x","category":100}`,
			names: "content",
		},
		"persona with no id": {
			file: "personas/narrator.json", data: `{"category":100,"content":"c"}`,
			names: "id",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := minimalPackageFS()
			fsys[tc.file] = &fstest.MapFile{Data: []byte(tc.data)}

			_, err := world.LoadPackage(fsys, world.LoadOptions{})
			if err == nil {
				t.Fatal("LoadPackage accepted malformed package content")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection reason %q does not mention %q", err, tc.names)
			}
			if !strings.Contains(err.Error(), tc.file) {
				t.Fatalf("rejection reason %q does not name the offending file", err)
			}
		})
	}
}

// F9 again, one file over: a condition field is a PREDICATE, so it faces the
// lower-kebab three-segment alphabet that an entity-ID segment does not. Without
// this gate `item.condition.rust_level` imports cleanly and fails when the rule
// processor loads the pack — an error naming a rule id, from a component that
// has never heard of the author's file.
func TestLoadPackage_RejectsARulePredicateThatTheWriteGateWouldRefuse(t *testing.T) {
	badRule := `{"id":"starter_rusty","type":"expression","name":"rusty","enabled":false,` +
		`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[` +
		`{"field":"item.attribute.quantity","operator":"gt","value":0},` +
		`{"field":"item.condition.rust_level","operator":"gt","value":3}],"logic":"and"}`

	fsys := minimalPackageFS()
	fsys["rules/00-stub.json"] = &fstest.MapFile{Data: []byte(badRule)}

	_, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err == nil {
		t.Fatal("LoadPackage accepted a rule condition on a predicate the write gate refuses")
	}
	for _, want := range []string{"rules/00-stub.json", "item.condition.rust_level", "condition[1]"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection reason %q does not name %q", err, want)
		}
	}

	// The upstream contract error must survive, for the same reason it must
	// survive in entities.jsonl: "we do not know that predicate" and "the graph
	// will refuse that alphabet" read alike and mean opposite things.
	var predicateError *ssvocab.PredicateValidationError
	if !errorAs(err, &predicateError) {
		t.Fatalf("expected the upstream *vocabulary.PredicateValidationError to survive, got %T: %v", err, err)
	}
}

// The gate is SYNTAX, not declaration. RequireDeclaredPredicate additionally
// demands registration in semstreams' process-global vocabulary registry, which
// is task 8.0's wiring — running it here would reject SemMachina's own rule pack
// for a reason that has nothing to do with the world, so a world author would be
// told their package is broken because the engine has not finished booting.
func TestLoadPackage_ChecksRulePredicateSyntaxWithoutRequiringDeclaration(t *testing.T) {
	// Both halves are asserted against the starter pack's own condition field,
	// because the gate's design rests entirely on the two being separable.
	const undeclared = "item.attribute.quantity"
	if _, err := ssvocab.ParsePredicate(undeclared); err != nil {
		t.Fatalf("the starter pack's own condition field is not canonical: %v", err)
	}
	if err := ssvocab.RequireDeclaredPredicate(undeclared); err == nil {
		t.Fatalf("%q is now DECLARED upstream, so this test no longer shows the syntax check "+
			"is separable from the declaration check; re-point it at a still-undeclared "+
			"canonical predicate and re-check whether task 8.0 still owns registration", undeclared)
	}

	// The shipped starter pack carries exactly that undeclared-but-canonical
	// field, so loading it IS the assertion.
	starterPackage(t)

	// A rule may also name message/state fields, which are not predicates at
	// all. Upstream escapes them with a `$` prefix; so does this gate.
	dollarRule := `{"id":"stub","type":"expression","name":"stub","enabled":false,` +
		`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[` +
		`{"field":"$message.payload.kind","operator":"eq","value":"x"}],"logic":"and"}`

	fsys := minimalPackageFS()
	fsys["rules/00-stub.json"] = &fstest.MapFile{Data: []byte(dollarRule)}
	if _, err := world.LoadPackage(fsys, world.LoadOptions{}); err != nil {
		t.Fatalf("LoadPackage rejected a $-escaped message field: %v", err)
	}
}

// A rule file may hold one rule or an array of them — the rule loader accepts
// both, so a gate that only understood one shape would silently pass every
// predicate in the other.
func TestLoadPackage_ChecksEveryRuleInAnArrayValuedRuleFile(t *testing.T) {
	pack := `[{"id":"ok","type":"expression","name":"ok","enabled":false,` +
		`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[` +
		`{"field":"item.attribute.quantity","operator":"lte","value":0}],"logic":"and"},` +
		`{"id":"bad","type":"expression","name":"bad","enabled":false,` +
		`"entity":{"pattern":"*.*.*.*.*.*"},"conditions":[` +
		`{"field":"item.condition.rust_level","operator":"gt","value":3}],"logic":"and"}]`

	fsys := minimalPackageFS()
	fsys["rules/00-stub.json"] = &fstest.MapFile{Data: []byte(pack)}

	_, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err == nil {
		t.Fatal("LoadPackage accepted a bad predicate in the second rule of an array-valued file")
	}
	if !strings.Contains(err.Error(), "item.condition.rust_level") {
		t.Fatalf("rejection reason %q does not name the offending predicate", err)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("rejection reason %q does not name the offending rule", err)
	}
}

func TestLoadPackage_DefaultsToTheEngineVersionThisBuildLinks(t *testing.T) {
	fsys := minimalPackageFS()
	fsys[world.ManifestFile] = &fstest.MapFile{Data: []byte(
		strings.Replace(goodManifest,
			`engine_compat: ">=v1.0.0-beta.150 <v2.0.0"`, `engine_compat: ">=v9.9.9"`, 1))}

	_, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err == nil {
		t.Fatal("LoadPackage did not check engine_compat")
	}
	if !strings.Contains(err.Error(), world.EngineVersion) {
		t.Fatalf("rejection reason %q does not name the engine version it checked against", err)
	}
}

// ---------------------------------------------------------------------------
// The starter fixture. These are the tests that make fixtures/worlds/starter a
// checked artifact rather than a directory of hopeful JSON.
// ---------------------------------------------------------------------------

func starterPackage(t *testing.T) *world.Package {
	t.Helper()
	fsys, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("fixtures.StarterWorld: %v", err)
	}
	pkg, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err != nil {
		t.Fatalf("the shipped starter world does not load: %v", err)
	}
	return pkg
}

func TestStarterWorld_LoadsAndIsScenedAsSpecified(t *testing.T) {
	pkg := starterPackage(t)

	counts := make(map[vocabulary.EntityKind]int)
	for _, entity := range pkg.Entities {
		counts[entity.Kind]++
	}

	if total := len(pkg.Entities); total < 6 || total > 10 {
		t.Fatalf("starter world has %d entities; the slice calls for 6 to 10", total)
	}
	if counts[vocabulary.EntityKindScene] != 1 {
		t.Fatalf("starter world has %d scenes, want exactly 1", counts[vocabulary.EntityKindScene])
	}
	if counts[vocabulary.EntityKindItem] < 2 || counts[vocabulary.EntityKindItem] > 3 {
		t.Fatalf("starter world has %d items, want 2 or 3", counts[vocabulary.EntityKindItem])
	}
	// One player character plus one or two NPC-shaped set pieces.
	if counts[vocabulary.EntityKindCharacter] < 2 || counts[vocabulary.EntityKindCharacter] > 3 {
		t.Fatalf("starter world has %d characters, want the PC plus 1-2 NPCs",
			counts[vocabulary.EntityKindCharacter])
	}
	// A template must not ship a player entity; the player is instance config.
	if counts[vocabulary.EntityKindPlayer] != 0 {
		t.Fatal("the starter template declares a player entity, so it could be instantiated only once")
	}

	if len(pkg.PersonaFiles) < 2 {
		t.Fatalf("starter world ships %d persona files; the loop needs an adjudicator and a narrator",
			len(pkg.PersonaFiles))
	}
}

// Every fact in the shipped world must come from the vocabulary constants, and
// every attribute value must sit inside the declared bounds. This is the check
// that makes the fixture a test OF internal/vocabulary rather than a consumer
// that happens to agree with it.
func TestStarterWorld_UsesOnlyRegisteredVocabularyWithinBounds(t *testing.T) {
	pkg := starterPackage(t)
	specs := vocabulary.DefaultAttributeSpecs()

	var attributeFacts, referenceFacts int
	for _, entity := range pkg.Entities {
		for _, fact := range entity.Facts {
			if !world.AuthorWritable(fact.Predicate) {
				t.Fatalf("entity %q declares %q, which is not author-writable",
					entity.LocalID, fact.Predicate)
			}
			if !vocabulary.AllowsSubjectKind(fact.Predicate, entity.Kind) {
				t.Fatalf("entity %q is a %q and cannot carry %q",
					entity.LocalID, entity.Kind, fact.Predicate)
			}
			if fact.IsReference() {
				referenceFacts++
				continue
			}
			attribute, isAttribute := vocabulary.AttributeForPredicate(fact.Predicate)
			if !isAttribute {
				continue
			}
			attributeFacts++
			value, ok := fact.Literal.(int)
			if !ok {
				t.Fatalf("entity %q attribute %q carries a %T literal", entity.LocalID, fact.Predicate, fact.Literal)
			}
			if err := specs.Check(attribute, value); err != nil {
				t.Fatalf("entity %q attribute %q is outside the engine bounds: %v",
					entity.LocalID, fact.Predicate, err)
			}
		}
	}

	if attributeFacts == 0 {
		t.Fatal("the starter world declares no attributes; the bounds check proved nothing")
	}
	if referenceFacts == 0 {
		t.Fatal("the starter world declares no references; the rewrite path is untested by this fixture")
	}
}

// The shipped constraint must actually admit the engine this repo pins.
// Without this, the fixture only proves the constraint parser works.
func TestStarterWorld_EngineCompatAdmitsThePinnedEngine(t *testing.T) {
	pkg := starterPackage(t)
	if err := world.CheckEngineCompat(pkg.Manifest.EngineCompat, world.EngineVersion); err != nil {
		t.Fatalf("the shipped starter world excludes the engine this build links: %v", err)
	}
}

func TestStarterWorld_ResolvesIntoAValidPlan(t *testing.T) {
	plan, err := starterPackage(t).Resolve(starterInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, entity := range plan.Entities {
		if err := entity.Payload(testStamp).Validate(); err != nil {
			t.Fatalf("starter entity %q does not produce a valid payload: %v", entity.ID, err)
		}
	}
}

// Embedding is the reason a binary can ship a world at all, so the embed must
// carry the whole package and not, say, silently drop a dotfile-adjacent
// directory.
func TestFixtures_EmbedCarriesTheWholeStarterPackage(t *testing.T) {
	fsys, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("fixtures.StarterWorld: %v", err)
	}
	for _, name := range []string{
		world.ManifestFile,
		world.EntitiesFile,
		"rules/00-spent-supplies.json",
		"personas/adjudicator.json",
		"personas/narrator.json",
	} {
		if _, err := fs.Stat(fsys, name); err != nil {
			t.Fatalf("embedded starter world is missing %s: %v", name, err)
		}
	}
}

func starterInstance() world.InstanceConfig {
	return world.InstanceConfig{
		Org:     "c360",
		WorldNS: "world1",
		Player:  world.PlayerBinding{LocalID: "p1", Name: "Coby", Character: "local:rook"},
	}
}
