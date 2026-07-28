package world_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

func testPackage(t *testing.T, lines ...string) *world.Package {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{rookLine, gatehouseLine}
	}
	manifest, err := world.ParseManifest([]byte(goodManifest), world.EngineVersion)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return &world.Package{Manifest: manifest, Entities: parse(t, lines...)}
}

func testInstance() world.InstanceConfig {
	return world.InstanceConfig{
		Org:     "c360",
		WorldNS: "world1",
		Player:  world.PlayerBinding{LocalID: "p1", Name: "Coby", Character: "local:rook"},
	}
}

func TestResolve_ComposesTheDocumentedSixPartID(t *testing.T) {
	plan, err := testPackage(t).Resolve(testInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := map[string]string{
		"rook":      "c360.semmachina.world1.starter.character.rook",
		"gatehouse": "c360.semmachina.world1.starter.scene.gatehouse",
		"p1":        "c360.semmachina.world1.starter.player.p1",
	}
	got := make(map[string]string, len(plan.Entities))
	for _, entity := range plan.Entities {
		got[entity.Template.LocalID] = entity.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composed IDs:\n got %v\nwant %v", got, want)
	}

	for _, id := range plan.IDs() {
		if err := types.ValidateEntityID(id); err != nil {
			t.Fatalf("composed id %q is not a canonical entity ID: %v", id, err)
		}
	}
}

// The spec's "Mapping is deterministic" scenario. Asserted by comparing whole
// plans rather than spot-checking IDs, so ordering, facts, and provenance are
// all covered by the same claim.
func TestResolve_IsDeterministic(t *testing.T) {
	pkg := testPackage(t)

	first, err := pkg.Resolve(testInstance())
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	second, err := pkg.Resolve(testInstance())
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("resolving the same package twice produced different plans:\n%#v\n%#v", first, second)
	}
}

// The spec's "Same template, two worlds" scenario.
func TestResolve_IntoTwoNamespacesSharesNoEntityIDs(t *testing.T) {
	pkg := testPackage(t)

	one := testInstance()
	two := testInstance()
	two.WorldNS = "world2"

	planOne, err := pkg.Resolve(one)
	if err != nil {
		t.Fatalf("Resolve world1: %v", err)
	}
	planTwo, err := pkg.Resolve(two)
	if err != nil {
		t.Fatalf("Resolve world2: %v", err)
	}

	if len(planOne.Entities) != len(planTwo.Entities) {
		t.Fatalf("the two worlds are not the same size: %d vs %d",
			len(planOne.Entities), len(planTwo.Entities))
	}

	seen := make(map[string]bool, len(planOne.Entities))
	for _, id := range planOne.IDs() {
		seen[id] = true
	}
	for _, id := range planTwo.IDs() {
		if seen[id] {
			t.Fatalf("both worlds contain entity %q; the namespaces are not disjoint", id)
		}
	}

	// Disjoint identity is only half the claim: each world must also be
	// COMPLETE, including every reference rewritten into its own namespace.
	for _, entity := range planTwo.Entities {
		for _, fact := range entity.Facts {
			if !fact.Reference {
				continue
			}
			object, ok := fact.Object.(string)
			if !ok {
				t.Fatalf("reference fact %q carries a %T object", fact.Predicate, fact.Object)
			}
			if !strings.Contains(object, ".world2.") {
				t.Fatalf("entity %q references %q, which is not in its own world namespace",
					entity.ID, object)
			}
		}
	}
}

func TestResolve_RewritesEveryLocalReference(t *testing.T) {
	plan, err := testPackage(t).Resolve(testInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var checked int
	for _, entity := range plan.Entities {
		for _, fact := range entity.Facts {
			object, isString := fact.Object.(string)
			if isString && strings.HasPrefix(object, world.LocalRefPrefix) {
				t.Fatalf("entity %q kept an unrewritten reference %q", entity.ID, object)
			}
			if !fact.Reference {
				continue
			}
			checked++
			if err := types.ValidateEntityID(object); err != nil {
				t.Fatalf("rewritten reference %q is not a canonical entity ID: %v", object, err)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no reference facts were checked; the fixture has nothing to rewrite")
	}
}

// The spec's "Dangling local reference fails the import" scenario.
func TestResolve_RejectsADanglingReferenceAndNamesIt(t *testing.T) {
	dangling := `{"local_id":"rook","type":"character","triples":[` +
		`{"predicate":"world.location.current","object":"local:nowhere"}]}`

	_, err := testPackage(t, dangling, gatehouseLine).Resolve(testInstance())
	if err == nil {
		t.Fatal("Resolve accepted a reference to an entity the package never declares")
	}
	if !strings.Contains(err.Error(), "local:nowhere") {
		t.Fatalf("rejection reason %q does not name the dangling reference", err)
	}

	var lineError *world.LineError
	if !errorAs(err, &lineError) {
		t.Fatalf("expected a *world.LineError so the author can find the line, got %T", err)
	}
	if lineError.Line != 1 {
		t.Fatalf("error names line %d, want 1", lineError.Line)
	}
}

// A reference to a LATER line must resolve exactly like a reference to an
// earlier one, or declaration order silently becomes part of the format.
func TestResolve_ResolvesForwardReferences(t *testing.T) {
	forward := `{"local_id":"rook","type":"character","triples":[` +
		`{"predicate":"world.relation.carries","object":"local:crowbar"}]}`
	crowbar := `{"local_id":"crowbar","type":"item","triples":[` +
		`{"predicate":"world.entity.name","object":"A bent crowbar"}]}`

	plan, err := testPackage(t, forward, crowbar).Resolve(testInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	object := plan.Entities[0].Facts[0].Object
	if object != "c360.semmachina.world1.starter.item.crowbar" {
		t.Fatalf("forward reference resolved to %v", object)
	}
}

// A reference that RESOLVES can still point at the wrong kind of thing, and
// resolution is the only thing the import path used to check. The object-kind
// rule has one enforcement path — the effect applier — so a template could
// author a character standing inside a crowbar, import cleanly, and then have
// the applier REPUBLISH that fact as part of the complete value set the merge
// lane demands: the runtime gate stamping its own provenance on state it would
// have rejected as an intent.
func TestResolve_RejectsAReferenceAtTheWrongKindOfEntity(t *testing.T) {
	crowbarLine := `{"local_id":"crowbar","type":"item","triples":[` +
		`{"predicate":"world.entity.name","object":"A bent crowbar"}]}`
	wrenLine := `{"local_id":"wren","type":"character","triples":[` +
		`{"predicate":"world.entity.name","object":"Wren"}]}`

	cases := map[string]struct {
		rook  string
		extra string
		names string
	}{
		"a character located inside an item": {
			rook: `{"local_id":"rook","type":"character","triples":[` +
				`{"predicate":"world.location.current","object":"local:crowbar"}]}`,
			extra: crowbarLine,
			names: "local:crowbar",
		},
		"a character carrying another character": {
			rook: `{"local_id":"rook","type":"character","triples":[` +
				`{"predicate":"world.relation.carries","object":"local:wren"}]}`,
			extra: wrenLine,
			names: "local:wren",
		},
		"an alliance with a scene": {
			rook: `{"local_id":"rook","type":"character","triples":[` +
				`{"predicate":"world.relation.allied-with","object":"local:gatehouse"}]}`,
			extra: gatehouseLine,
			names: "local:gatehouse",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := testPackage(t, tc.rook, tc.extra).Resolve(testInstance())
			if err == nil {
				t.Fatal("Resolve accepted a reference the effect applier would have rejected at runtime")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection reason %q does not name the offending reference", err)
			}

			// The author has to be told WHICH LINE, the same way every other
			// package fault names one.
			var lineError *world.LineError
			if !errorAs(err, &lineError) {
				t.Fatalf("expected a *world.LineError so the author can find the line, got %T", err)
			}
			if lineError.Line != 1 {
				t.Fatalf("error names line %d, want 1", lineError.Line)
			}
		})
	}
}

// The import path and the runtime path must apply ONE statement of the
// object-kind rule. This asserts the coupling directly: every entity-valued
// predicate the vocabulary registers is refused at import for every kind the
// vocabulary does not allow as its object.
func TestResolve_RefusesEveryObjectKindTheVocabularyRefuses(t *testing.T) {
	// One declared entity per kind, so any (predicate, kind) pair is expressible.
	byKind := map[vocabulary.EntityKind]string{
		vocabulary.EntityKindCharacter: "wren",
		vocabulary.EntityKindItem:      "crowbar",
		vocabulary.EntityKindScene:     "gatehouse",
	}
	lines := []string{
		`{"local_id":"wren","type":"character","triples":[{"predicate":"world.entity.name","object":"Wren"}]}`,
		`{"local_id":"crowbar","type":"item","triples":[{"predicate":"world.entity.name","object":"A crowbar"}]}`,
		gatehouseLine,
	}

	var checked int
	for _, predicate := range vocabulary.WorldFactPredicates() {
		if !vocabulary.IsEntityReference(predicate) {
			continue
		}
		// The subject is always Rook, so a predicate a character may not carry
		// (player.character.current) is not expressible here and is covered by
		// the player-binding path instead.
		if !vocabulary.AllowsSubjectKind(predicate, vocabulary.EntityKindCharacter) {
			continue
		}
		for kind, local := range byKind {
			if vocabulary.AllowsObjectKind(predicate, kind) {
				continue
			}
			checked++
			rook := fmt.Sprintf(
				`{"local_id":"rook","type":"character","triples":[{"predicate":%q,"object":"local:%s"}]}`,
				predicate, local)
			if _, err := testPackage(t, append([]string{rook}, lines...)...).Resolve(testInstance()); err == nil {
				t.Fatalf("Resolve accepted %s pointing at a %q, which the vocabulary refuses", predicate, kind)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no disallowed pairs were exercised; the test proves nothing")
	}
}

func TestResolve_RejectsASelfReference(t *testing.T) {
	self := `{"local_id":"rook","type":"character","triples":[` +
		`{"predicate":"world.relation.knows","object":"local:rook"}]}`

	_, err := testPackage(t, self, gatehouseLine).Resolve(testInstance())
	if err == nil {
		t.Fatal("Resolve accepted an entity referencing itself")
	}
	if !strings.Contains(err.Error(), "itself") {
		t.Fatalf("rejection reason %q does not explain the self-reference", err)
	}
}

// Player binding is instance configuration, so the plan must materialize a
// player entity the template never mentioned, bound to a template character.
func TestResolve_MaterializesThePlayerFromInstanceConfig(t *testing.T) {
	plan, err := testPackage(t).Resolve(testInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	player := plan.Entities[len(plan.Entities)-1]
	if player.Kind != vocabulary.EntityKindPlayer {
		t.Fatalf("last planned entity is a %q, want the player", player.Kind)
	}
	if player.ID != "c360.semmachina.world1.starter.player.p1" {
		t.Fatalf("player id = %q", player.ID)
	}

	facts := make(map[vocabulary.Predicate]any, len(player.Facts))
	for _, fact := range player.Facts {
		facts[fact.Predicate] = fact.Object
	}
	if facts[vocabulary.WorldEntityName] != "Coby" {
		t.Fatalf("player name = %v, want the instance-configured name", facts[vocabulary.WorldEntityName])
	}
	if facts[vocabulary.PlayerCharacterCurrent] != "c360.semmachina.world1.starter.character.rook" {
		t.Fatalf("played character = %v", facts[vocabulary.PlayerCharacterCurrent])
	}

	// The package itself must be unchanged by instantiation — that is what
	// makes the SAME template instantiable into another world.
	for _, entity := range testPackage(t).Entities {
		if entity.Kind == vocabulary.EntityKindPlayer {
			t.Fatal("the template declares a player entity; it would not be reusable")
		}
	}
}

// One template, two campaigns, two different players, no template edit.
func TestResolve_BindsADifferentPlayerInEachWorldWithoutTouchingTheTemplate(t *testing.T) {
	pkg := testPackage(t)

	one := testInstance()
	two := testInstance()
	two.WorldNS = "world2"
	two.Player = world.PlayerBinding{LocalID: "p9", Name: "Someone Else", Character: "local:rook"}

	planOne, err := pkg.Resolve(one)
	if err != nil {
		t.Fatalf("Resolve world1: %v", err)
	}
	planTwo, err := pkg.Resolve(two)
	if err != nil {
		t.Fatalf("Resolve world2: %v", err)
	}

	if planOne.Entities[len(planOne.Entities)-1].ID == planTwo.Entities[len(planTwo.Entities)-1].ID {
		t.Fatal("both campaigns produced the same player entity id")
	}
}

func TestResolve_RejectsBadInstanceConfiguration(t *testing.T) {
	cases := map[string]struct {
		mutate func(*world.InstanceConfig)
		names  string
	}{
		"empty org":              {mutate: func(c *world.InstanceConfig) { c.Org = "" }, names: "org"},
		"dotted org":             {mutate: func(c *world.InstanceConfig) { c.Org = "a.b" }, names: "org"},
		"empty world namespace":  {mutate: func(c *world.InstanceConfig) { c.WorldNS = "" }, names: "world namespace"},
		"dotted world namespace": {mutate: func(c *world.InstanceConfig) { c.WorldNS = "a.b" }, names: "world namespace"},
		"empty player id":        {mutate: func(c *world.InstanceConfig) { c.Player.LocalID = "" }, names: "player id"},
		"blank player name":      {mutate: func(c *world.InstanceConfig) { c.Player.Name = "  " }, names: "player name"},
		"character is not a reference": {
			mutate: func(c *world.InstanceConfig) { c.Player.Character = "rook" },
			names:  "local:",
		},
		"character the template does not declare": {
			mutate: func(c *world.InstanceConfig) { c.Player.Character = "local:nobody" },
			names:  "names no entity",
		},
		"character that is not a character": {
			mutate: func(c *world.InstanceConfig) { c.Player.Character = "local:gatehouse" },
			names:  "is a \"scene\"",
		},
		"player id colliding with a template entity": {
			mutate: func(c *world.InstanceConfig) { c.Player.LocalID = "rook" },
			names:  "collides",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			instance := testInstance()
			tc.mutate(&instance)

			_, err := testPackage(t).Resolve(instance)
			if err == nil {
				t.Fatal("Resolve accepted invalid instance configuration")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection reason %q does not mention %q", err, tc.names)
			}
		})
	}
}

// The 256-byte limit is a property of the WHOLE composed ID, so six
// individually legal positions can still compose an ID the graph refuses. That
// has to fail at resolve, before anything is published.
//
// The oversized positions are INSTANCE configuration rather than template
// content, because a template's own positions are now bounded per segment at
// parse (see TestParseEntities_RejectsAnOversizedLocalIDWithAFileAndALine) —
// which is exactly why the whole-ID check still has work to do: two positions
// that each sit at the segment budget compose past the upstream limit together.
func TestResolve_RejectsAnOversizedComposedID(t *testing.T) {
	long := strings.Repeat("x", vocabulary.MaxIDSegmentBytes)
	if err := vocabulary.ValidateIDSegment(long); err != nil {
		t.Fatalf("the test's positions are not individually legal, so it proves nothing: %v", err)
	}

	instance := testInstance()
	instance.Org = long
	instance.WorldNS = long

	_, err := testPackage(t).Resolve(instance)
	if err == nil {
		t.Fatal("Resolve composed an entity ID past the 256-byte limit")
	}
	if !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("rejection reason %q does not explain the ID violation", err)
	}
	if !strings.Contains(err.Error(), long) {
		t.Fatalf("rejection reason %q does not name the offending id", err)
	}
}

// Every planned entity must survive the payload contract, since the plan's
// only purpose is to become one. Catching it here means a bad plan never
// reaches a publisher.
func TestResolve_ProducesPayloadsThatValidate(t *testing.T) {
	plan, err := testPackage(t, rookLine, gatehouseLine).Resolve(testInstance())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, entity := range plan.Entities {
		if err := entity.Payload(testStamp).Validate(); err != nil {
			t.Fatalf("planned entity %q does not produce a valid payload: %v", entity.ID, err)
		}
	}
}
