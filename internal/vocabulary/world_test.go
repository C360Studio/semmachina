package vocabulary_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// A world package is data, and the engine writes only registered predicates.
// So the set of predicates a world author can express with is exactly the set
// enumerated here — if a plausible starter scene needs a fact this list cannot
// carry, the world is not expressible and the gap is an engine gap, not an
// authoring mistake.
func TestWorldDescriptivePredicates_AreRegistered(t *testing.T) {
	registered := make(map[vocabulary.Predicate]bool)
	for _, p := range vocabulary.AllPredicates() {
		registered[p] = true
	}

	for _, p := range []vocabulary.Predicate{
		vocabulary.WorldEntityName,
		vocabulary.WorldEntityKind,
		vocabulary.WorldEntityDescription,
		vocabulary.PlayerCharacterCurrent,
	} {
		if !registered[p] {
			t.Fatalf("predicate %q is not in AllPredicates(); the write gate would accept it "+
				"but no consumer knows it exists", p)
		}
		if err := vocabulary.ValidatePredicate(p); err != nil {
			t.Fatalf("predicate %q is rejected by the write-gate parser: %v", p, err)
		}
	}
}

// EntityKind is the declared type of a world entity. It doubles as the `type`
// segment of the six-part ID the importer composes, so it must be a legal
// entity-ID segment as well as a legal triple object.
func TestEntityKinds_AreLegalEntityIDSegments(t *testing.T) {
	kinds := vocabulary.EntityKinds()
	if len(kinds) == 0 {
		t.Fatal("EntityKinds() is empty; no world entity could declare a type")
	}
	for _, k := range kinds {
		if err := vocabulary.ValidateIDSegment(string(k)); err != nil {
			t.Fatalf("entity kind %q is not a legal entity-ID segment: %v", k, err)
		}
	}
}

func TestParseEntityKind_RejectsUnregisteredKinds(t *testing.T) {
	if _, err := vocabulary.ParseEntityKind("dragon"); err == nil {
		t.Fatal("ParseEntityKind accepted an unregistered kind")
	}
	if got, err := vocabulary.ParseEntityKind(string(vocabulary.EntityKindCharacter)); err != nil ||
		got != vocabulary.EntityKindCharacter {
		t.Fatalf("ParseEntityKind(%q) = %q, %v", vocabulary.EntityKindCharacter, got, err)
	}
}

// The applier and the world loader both need to go from a predicate back to
// the attribute whose bounds govern it. Without the reverse mapping each
// caller would re-derive it by hand and the two copies would drift.
func TestAttributeForPredicate_InvertsTheRegisteredMapping(t *testing.T) {
	for _, a := range vocabulary.Attributes() {
		spec, ok := vocabulary.AttributeSpecFor(a)
		if !ok {
			t.Fatalf("attribute %q has no registered spec", a)
		}
		got, ok := vocabulary.AttributeForPredicate(spec.Predicate)
		if !ok {
			t.Fatalf("predicate %q does not map back to any attribute", spec.Predicate)
		}
		if got != a {
			t.Fatalf("predicate %q mapped back to attribute %q, want %q", spec.Predicate, got, a)
		}
	}

	if _, ok := vocabulary.AttributeForPredicate(vocabulary.TurnPhaseCurrent); ok {
		t.Fatalf("%q is not an attribute predicate but the reverse mapping claimed it is",
			vocabulary.TurnPhaseCurrent)
	}
}

func TestRelationForPredicate_InvertsTheRegisteredMapping(t *testing.T) {
	for _, r := range vocabulary.Relations() {
		p, ok := vocabulary.RelationPredicate(r)
		if !ok {
			t.Fatalf("relation %q has no registered predicate", r)
		}
		got, ok := vocabulary.RelationForPredicate(p)
		if !ok {
			t.Fatalf("predicate %q does not map back to any relation", p)
		}
		if got != r {
			t.Fatalf("predicate %q mapped back to relation %q, want %q", p, got, r)
		}
	}

	if _, ok := vocabulary.RelationForPredicate(vocabulary.WorldLocationCurrent); ok {
		t.Fatalf("%q is a location predicate, not a relation predicate", vocabulary.WorldLocationCurrent)
	}
}

// Every world fact must say which kinds of entity may carry it, and every kind
// it names must be a registered kind. A predicate whose kind list is empty (or
// names a kind that does not exist) is a fact nothing can ever hold — a silent
// dead predicate rather than a loud one.
func TestSubjectKinds_AreCompleteAndRegistered(t *testing.T) {
	registeredKinds := make(map[vocabulary.EntityKind]bool)
	for _, k := range vocabulary.EntityKinds() {
		registeredKinds[k] = true
	}

	facts := vocabulary.WorldFactPredicates()
	if len(facts) == 0 {
		t.Fatal("no world-fact predicates registered")
	}
	for _, p := range facts {
		kinds, ok := vocabulary.SubjectKindsFor(p)
		if !ok {
			t.Fatalf("WorldFactPredicates() lists %q but SubjectKindsFor does not know it", p)
		}
		if len(kinds) == 0 {
			t.Fatalf("world fact %q names no subject kinds; no entity could ever carry it", p)
		}
		for _, k := range kinds {
			if !registeredKinds[k] {
				t.Fatalf("world fact %q names unregistered kind %q", p, k)
			}
			if !vocabulary.AllowsSubjectKind(p, k) {
				t.Fatalf("SubjectKindsFor(%q) lists %q but AllowsSubjectKind disagrees", p, k)
			}
		}
	}
}

// The example the design names by hand: character health must not land on a
// scene. If this ever passes, the applier's target-type check has no teeth.
func TestAllowsSubjectKind_KeepsCharacterHealthOffScenes(t *testing.T) {
	if vocabulary.AllowsSubjectKind(vocabulary.CharacterAttributeHealth, vocabulary.EntityKindScene) {
		t.Fatal("character health is allowed on a scene entity")
	}
	if !vocabulary.AllowsSubjectKind(vocabulary.CharacterAttributeHealth, vocabulary.EntityKindCharacter) {
		t.Fatal("character health is not allowed on a character entity")
	}
	if vocabulary.AllowsSubjectKind(vocabulary.SceneAttributeTension, vocabulary.EntityKindCharacter) {
		t.Fatal("scene tension is allowed on a character entity")
	}
}

// Turn and campaign bookkeeping entities have no EntityKind, so asking which
// kind may carry their predicates is a category error and must be reported as
// "not a world fact" rather than as an empty allow-list.
func TestSubjectKindsFor_RejectsNonWorldFacts(t *testing.T) {
	for _, p := range []vocabulary.Predicate{
		vocabulary.TurnPhaseCurrent,
		vocabulary.TurnVerdictRef,
		vocabulary.CampaignSeedValue,
	} {
		if _, ok := vocabulary.SubjectKindsFor(p); ok {
			t.Fatalf("%q is engine bookkeeping, not a world fact, but SubjectKindsFor claimed it", p)
		}
		for _, k := range vocabulary.EntityKinds() {
			if vocabulary.AllowsSubjectKind(p, k) {
				t.Fatalf("AllowsSubjectKind(%q, %q) is true for a non-world-fact predicate", p, k)
			}
		}
	}
}

func TestWorldFactPredicates_AreAllRegisteredPredicates(t *testing.T) {
	registered := make(map[vocabulary.Predicate]bool)
	for _, p := range vocabulary.AllPredicates() {
		registered[p] = true
	}
	for _, p := range vocabulary.WorldFactPredicates() {
		if !registered[p] {
			t.Fatalf("world fact %q is not in AllPredicates()", p)
		}
	}
}

// ValidateIDSegment exists so every producer of an entity-ID segment — the
// world importer's local ids, the ingress adapter's action ids — faces the
// SAME alphabet the composed ID will face, and so the F9 trap (segments allow
// underscores and uppercase, predicates do not) is decided in one place.
func TestValidateIDSegment_MatchesTheEntityIDAlphabet(t *testing.T) {
	accepted := []string{"rook", "rusty_sword", "Gatehouse", "npc-7", "p1", "0"}
	for _, s := range accepted {
		if err := vocabulary.ValidateIDSegment(s); err != nil {
			t.Fatalf("ValidateIDSegment(%q) rejected a legal segment: %v", s, err)
		}
	}

	rejected := map[string]string{
		"empty":          "",
		"dotted":         "a.b",
		"leading hyphen": "-rook",
		"space":          "rusty sword",
		"slash":          "a/b",
	}
	for name, s := range rejected {
		t.Run(name, func(t *testing.T) {
			if err := vocabulary.ValidateIDSegment(s); err == nil {
				t.Fatalf("ValidateIDSegment(%q) accepted an illegal segment", s)
			}
		})
	}
}

// The length bound belongs to the ONE segment rule, not to whichever caller
// happened to think of it. A segment past the reserve composes a legal ID under
// a short prefix and an illegal one under a long prefix, so upstream cannot
// reject it and every caller that skips the bound ships the failure to the
// first step that touches the network.
func TestValidateIDSegment_BoundsSegmentLength(t *testing.T) {
	if err := vocabulary.ValidateIDSegment(strings.Repeat("a", vocabulary.MaxIDSegmentBytes)); err != nil {
		t.Fatalf("a segment exactly at the budget was rejected: %v", err)
	}

	oversized := strings.Repeat("a", vocabulary.MaxIDSegmentBytes+1)
	err := vocabulary.ValidateIDSegment(oversized)
	if err == nil {
		t.Fatalf("ValidateIDSegment accepted a %d-byte segment", len(oversized))
	}
	// The length check runs before every check that quotes its input, so a
	// rejection never echoes an oversized value back into a log line.
	if strings.Contains(err.Error(), oversized) {
		t.Fatalf("the rejection echoes the whole oversized segment: %q", err)
	}

	// The bound is a RESERVE, not the upstream failure point: this segment
	// composes an entity ID well inside types.MaxEntityIDBytes, which is exactly
	// why upstream cannot be the one to catch it.
	composed := "c360.semmachina.world1.starter.item." + oversized
	if err := types.ValidateEntityID(composed); err != nil {
		t.Fatalf("the oversized segment already fails upstream composition (%d bytes); "+
			"this test proves nothing about the reserve: %v", len(composed), err)
	}
}

// Two capabilities outside the world importer — turn entities (group 6) and
// campaign entities (group 8) — compose six-part IDs whose type segment is
// deliberately not an EntityKind. They get the composer, not a second copy of
// the join-and-validate rule.
func TestComposeEntityID_ComposesTheDocumentedSixPartOrder(t *testing.T) {
	id, err := vocabulary.ComposeEntityID("c360", "world1", "starter", "character", "rook")
	if err != nil {
		t.Fatalf("ComposeEntityID: %v", err)
	}
	// world_ns lands in the domain slot and the template in the system slot:
	// org.platform.domain.system.type.instance.
	if want := "c360.semmachina.world1.starter.character.rook"; id != want {
		t.Fatalf("composed %q, want %q", id, want)
	}

	parsed, err := types.ParseEntityID(id)
	if err != nil {
		t.Fatalf("the composed ID is not parseable upstream: %v", err)
	}
	if parsed.Domain != "world1" || parsed.System != "starter" {
		t.Fatalf("world namespace landed in %q and the template in %q", parsed.Domain, parsed.System)
	}

	// A type segment that is not an EntityKind is the whole reason this is
	// exported with a plain string.
	for _, typeSegment := range []string{"turn", "campaign"} {
		if _, err := vocabulary.ComposeEntityID("c360", "world1", "starter", typeSegment, "x1"); err != nil {
			t.Fatalf("ComposeEntityID rejected the %q type segment: %v", typeSegment, err)
		}
	}
}

// The 256-byte limit is a property of the WHOLE composed ID, so positions that
// are individually legal can still compose an ID the graph refuses. Validating
// the composed result is what makes that a caller-visible error rather than a
// write-time one.
func TestComposeEntityID_ValidatesTheComposedResult(t *testing.T) {
	long := strings.Repeat("w", vocabulary.MaxIDSegmentBytes)
	for _, tc := range []struct {
		name                                        string
		org, worldNS, template, typeSeg, instanceID string
	}{
		{name: "dotted position", org: "c360", worldNS: "a.b", template: "starter",
			typeSeg: "character", instanceID: "rook"},
		{name: "empty position", org: "", worldNS: "world1", template: "starter",
			typeSeg: "character", instanceID: "rook"},
		{name: "legal positions composing an oversized ID", org: long, worldNS: long,
			template: "starter", typeSeg: "character", instanceID: "rook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := vocabulary.ComposeEntityID(
				tc.org, tc.worldNS, tc.template, tc.typeSeg, tc.instanceID); err == nil {
				t.Fatal("ComposeEntityID returned an ID the graph would refuse")
			}
		})
	}
}
