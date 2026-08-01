package world_test

import (
	"strings"
	"testing"

	"github.com/c360studio/semstreams/pkg/types"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

func parse(t *testing.T, lines ...string) []world.TemplateEntity {
	t.Helper()
	entities, err := world.ParseEntities(
		strings.NewReader(strings.Join(lines, "\n")+"\n"), vocabulary.DefaultAttributeSpecs())
	if err != nil {
		t.Fatalf("ParseEntities: %v", err)
	}
	return entities
}

func parseErr(t *testing.T, lines ...string) error {
	t.Helper()
	_, err := world.ParseEntities(
		strings.NewReader(strings.Join(lines, "\n")+"\n"), vocabulary.DefaultAttributeSpecs())
	if err == nil {
		t.Fatalf("ParseEntities accepted:\n%s", strings.Join(lines, "\n"))
	}
	return err
}

const (
	rookLine = `{"local_id":"rook","type":"character","triples":[` +
		`{"predicate":"world.entity.name","object":"Rook"},` +
		`{"predicate":"character.attribute.health","object":8},` +
		`{"predicate":"character.status.current","object":"healthy"},` +
		`{"predicate":"world.location.current","object":"local:gatehouse"}]}`

	gatehouseLine = `{"local_id":"gatehouse","type":"scene","triples":[` +
		`{"predicate":"world.entity.name","object":"The Gatehouse"},` +
		`{"predicate":"scene.attribute.tension","object":3}]}`
)

func TestParseEntities_ParsesTheAuthoredShape(t *testing.T) {
	entities := parse(t, rookLine, gatehouseLine)

	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2", len(entities))
	}

	rook := entities[0]
	if rook.LocalID != "rook" || rook.Kind != vocabulary.EntityKindCharacter {
		t.Fatalf("first entity = %+v", rook)
	}
	if rook.Line != 1 {
		t.Fatalf("first entity line = %d, want 1", rook.Line)
	}
	if len(rook.Facts) != 4 {
		t.Fatalf("rook has %d facts, want 4", len(rook.Facts))
	}
	if got := rook.Facts[1].Literal; got != 8 {
		t.Fatalf("health literal decoded as %T(%v), want int(8)", got, got)
	}
	if !rook.Facts[3].IsReference() || rook.Facts[3].LocalRef != "gatehouse" {
		t.Fatalf("location fact = %+v, want a reference to gatehouse", rook.Facts[3])
	}
	if entities[1].Line != 2 {
		t.Fatalf("second entity line = %d, want 2", entities[1].Line)
	}
}

func TestParseEntities_SkipsBlankLinesWithoutLosingLineNumbers(t *testing.T) {
	entities, err := world.ParseEntities(
		strings.NewReader("\n"+rookLine+"\n\n"+gatehouseLine+"\n"), vocabulary.DefaultAttributeSpecs())
	if err != nil {
		t.Fatalf("ParseEntities: %v", err)
	}
	if entities[0].Line != 2 {
		t.Fatalf("rook line = %d, want 2", entities[0].Line)
	}
	if entities[1].Line != 4 {
		t.Fatalf("gatehouse line = %d, want 4", entities[1].Line)
	}
}

// F9's exact trap: a legal local_id and an illegal predicate in the same
// record. `rusty_sword` composes a perfectly valid entity ID; the same
// underscore in a predicate is rejected by the write gate. The import must say
// so, and must say WHERE.
func TestParseEntities_RejectsAnUnderscorePredicateBesideALegalUnderscoreLocalID(t *testing.T) {
	bad := `{"local_id":"rusty_sword","type":"item","triples":[` +
		`{"predicate":"item.condition.rust_level","object":3}]}`

	err := parseErr(t, gatehouseLine, bad)

	var lineError *world.LineError
	if !errorAs(err, &lineError) {
		t.Fatalf("expected a *world.LineError, got %T: %v", err, err)
	}
	if lineError.Line != 2 {
		t.Fatalf("error names line %d, want 2", lineError.Line)
	}
	if lineError.File != world.EntitiesFile {
		t.Fatalf("error names file %q, want %q", lineError.File, world.EntitiesFile)
	}
	if !strings.Contains(err.Error(), "item.condition.rust_level") {
		t.Fatalf("rejection reason %q does not name the offending predicate", err)
	}

	// The reason must be the WRITE GATE'S reason, not merely "we do not know
	// that predicate". Those two rejections read alike and mean opposite
	// things: one says the author invented vocabulary, the other says the
	// author used an alphabet the graph will refuse. Requiring the upstream
	// contract error to survive is what pins the alphabet check as a distinct,
	// earlier gate rather than an accident of the registry lookup.
	var predicateError *ssvocab.PredicateValidationError
	if !errorAs(err, &predicateError) {
		t.Fatalf("expected the upstream *vocabulary.PredicateValidationError to survive, got %T: %v", err, err)
	}
	if predicateError.Reason != ssvocab.PredicateReasonSegmentCharacter {
		t.Fatalf("upstream reason = %q, want %q", predicateError.Reason, ssvocab.PredicateReasonSegmentCharacter)
	}

	// The same underscore is fine in the local id, which is the whole point of
	// checking the two alphabets separately.
	ok := `{"local_id":"rusty_sword","type":"item","triples":[` +
		`{"predicate":"world.entity.name","object":"A rusty sword"}]}`
	parse(t, ok)
}

// An oversized local_id must be rejected HERE, with a file and a line.
//
// This is the failure the one-rule/one-bound consolidation closes, and it was
// measured rather than imagined: a 129-byte local_id passed LoadPackage, passed
// Resolve — the composed ID is well inside the 256-byte limit, as the assertion
// below re-proves — and then failed inside Import at the payload's own segment
// bound, from the one step that touches the network, with no file and no line.
// Because encoding happens inside the publish loop, earlier entities were
// already in the stream: a permanently partial world that no retry completes,
// from a fault the package alone could have named.
func TestParseEntities_RejectsAnOversizedLocalIDWithAFileAndALine(t *testing.T) {
	oversized := strings.Repeat("a", vocabulary.MaxIDSegmentBytes+1)
	line := `{"local_id":"` + oversized + `","type":"item","triples":[` +
		`{"predicate":"world.entity.name","object":"A very long name"}]}`

	err := parseErr(t, gatehouseLine, line)

	var lineError *world.LineError
	if !errorAs(err, &lineError) {
		t.Fatalf("expected a *world.LineError so the author can find the line, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), world.EntitiesFile+":2") {
		t.Fatalf("rejection reason %q does not name the file and line", err)
	}
	if !strings.Contains(err.Error(), "local_id") {
		t.Fatalf("rejection reason %q does not name the offending field", err)
	}

	// The pin: composition would NOT have caught this. If this ID ever becomes
	// invalid upstream, the test has stopped proving that parse is the only
	// place the fault is visible.
	composed, composeErr := vocabulary.ComposeEntityID("c360", "world1", "starter", "item", oversized)
	if composeErr != nil {
		t.Fatalf("the oversized local_id fails composition too (%v); "+
			"this test no longer proves parse is the earliest gate", composeErr)
	}
	if err := types.ValidateEntityID(composed); err != nil {
		t.Fatalf("the composed ID is already invalid upstream: %v", err)
	}
}

func TestParseEntities_RejectsMalformedRecords(t *testing.T) {
	cases := map[string]struct {
		line  string
		names string
	}{
		"not json": {
			line:  `{oops`,
			names: "entities.jsonl:1",
		},
		"two json values on one line": {
			line:  `{"local_id":"a","type":"item","triples":[]} {"local_id":"b"}`,
			names: "one entity per line",
		},
		"unknown record field": {
			line: `{"local_id":"rook","type":"character","colour":"grey","triples":[` +
				`{"predicate":"world.entity.name","object":"Rook"}]}`,
			names: "colour",
		},
		"missing local_id": {
			line:  `{"type":"character","triples":[{"predicate":"world.entity.name","object":"Rook"}]}`,
			names: "local_id",
		},
		"local_id is not an entity-ID segment": {
			line: `{"local_id":"rook.the.courier","type":"character","triples":[` +
				`{"predicate":"world.entity.name","object":"Rook"}]}`,
			names: "local_id",
		},
		"unregistered type": {
			line:  `{"local_id":"smaug","type":"dragon","triples":[{"predicate":"world.entity.name","object":"S"}]}`,
			names: "dragon",
		},
		"no triples": {
			line:  `{"local_id":"rook","type":"character","triples":[]}`,
			names: "declares no triples",
		},
		"two-segment predicate": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"world.name","object":"Rook"}]}`,
			names: "world.name",
		},
		"engine-owned turn predicate": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"turn.phase.current","object":"accepted"}]}`,
			names: "author-writable",
		},
		"template declaring the player binding": {
			line: `{"local_id":"p1","type":"player","triples":[` +
				`{"predicate":"player.character.current","object":"local:rook"}]}`,
			names: "author-writable",
		},
		"template declaring its own kind triple": {
			line: `{"local_id":"rook","type":"character","triples":[` +
				`{"predicate":"world.entity.kind","object":"character"}]}`,
			names: "author-writable",
		},
		"fact the kind cannot carry": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"scene.attribute.tension","object":3}]}`,
			names: "cannot be carried",
		},
		"attribute above its bound": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"character.attribute.health","object":900}]}`,
			names: "bounds",
		},
		"attribute below its bound": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"character.attribute.health","object":-1}]}`,
			names: "bounds",
		},
		"attribute is not a whole number": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"character.attribute.health","object":3.5}]}`,
			names: "whole number",
		},
		"attribute is a string": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"character.attribute.health","object":"8"}]}`,
			names: "number",
		},
		"unregistered status": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"character.status.current","object":"poisoned"}]}`,
			names: "poisoned",
		},
		"reference written without the local prefix": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"world.location.current","object":"gatehouse"}]}`,
			names: "local:",
		},
		"reference target is not a legal segment": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"world.location.current","object":"local:a.b"}]}`,
			names: "reference target",
		},
		"text object that looks like a reference": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"world.entity.name","object":"local:rook"}]}`,
			names: "mistyped reference",
		},
		"empty text object": {
			line:  `{"local_id":"rook","type":"character","triples":[{"predicate":"world.entity.name","object":""}]}`,
			names: "empty object",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := parseErr(t, tc.line)
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("rejection reason %q does not mention %q", err, tc.names)
			}
		})
	}
}

func TestParseEntities_RejectsADuplicateLocalIDAndNamesBothLines(t *testing.T) {
	err := parseErr(t, rookLine, gatehouseLine, rookLine)
	if !strings.Contains(err.Error(), "entities.jsonl:3") {
		t.Fatalf("rejection reason %q does not name the duplicate's line", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("rejection reason %q does not name the original declaration's line", err)
	}
}

func TestParseEntities_RejectsAnEmptyFile(t *testing.T) {
	if _, err := world.ParseEntities(strings.NewReader("\n\n"), vocabulary.DefaultAttributeSpecs()); err == nil {
		t.Fatal("ParseEntities accepted a file declaring no entities")
	}
}

// Bounds are a world's data, not the engine's law. A package whose values sit
// inside its OWN declared bounds must load even when the engine defaults would
// have rejected them.
func TestParseEntities_HonorsCallerSuppliedAttributeBounds(t *testing.T) {
	heroic, err := vocabulary.NewAttributeSpecSet(map[vocabulary.Attribute]vocabulary.AttributeBounds{
		vocabulary.AttributeHealth:   {Min: 0, Max: 40},
		vocabulary.AttributeStamina:  {Min: 0, Max: 40},
		vocabulary.AttributeResolve:  {Min: 0, Max: 40},
		vocabulary.AttributeQuantity: {Min: 0, Max: 99},
		vocabulary.AttributeTension:  {Min: 0, Max: 10},
	})
	if err != nil {
		t.Fatalf("NewAttributeSpecSet: %v", err)
	}

	line := `{"local_id":"rook","type":"character","triples":[{"predicate":"character.attribute.health","object":32}]}`

	if _, err := world.ParseEntities(strings.NewReader(line+"\n"), heroic); err != nil {
		t.Fatalf("a value inside the world's own bounds was rejected: %v", err)
	}
	if _, err := world.ParseEntities(
		strings.NewReader(line+"\n"), vocabulary.DefaultAttributeSpecs()); err == nil {
		t.Fatal("the engine defaults accepted 32 health; the bounds check is not reading the supplied set")
	}
}

// AuthorWritable is the closure boundary a world author actually faces. It has
// to be exactly the world facts minus engine-owned runtime state, or the error
// message listing "what you may declare" is lying.
func TestAuthorWritablePredicates_AreWorldFactsMinusEngineOwnedState(t *testing.T) {
	writable := make(map[vocabulary.Predicate]bool)
	for _, p := range world.AuthorWritablePredicates() {
		writable[p] = true
		if world.ObjectShapeFor(p) == world.ShapeNone {
			t.Fatalf("author-writable predicate %q has no object shape", p)
		}
	}

	if writable[vocabulary.WorldEntityKind] {
		t.Fatal("world.entity.kind is author-writable; a template could contradict its own entity ID")
	}
	if writable[vocabulary.PlayerCharacterCurrent] {
		t.Fatal("player.character.current is author-writable; the template could only be instantiated once")
	}

	engineOwned := map[vocabulary.Predicate]bool{
		vocabulary.WorldEntityKind:                 true,
		vocabulary.PlayerCharacterCurrent:          true,
		vocabulary.RevelationEvidenceRef:           true,
		vocabulary.RevelationActorHolder:           true,
		vocabulary.RevelationTurnID:                true,
		vocabulary.RevelationSourceActor:           true,
		vocabulary.RevelationTestimonyRef:          true,
		vocabulary.CompanionBondPlayer:             true,
		vocabulary.CompanionBondCharacter:          true,
		vocabulary.CompanionBondPolicy:             true,
		vocabulary.CompanionBondHintLevel:          true,
		vocabulary.CaseLifecyclePhase:              true,
		vocabulary.CaseLifecycleEventID:            true,
		vocabulary.CaseLifecycleEventKindPredicate: true,
		vocabulary.CaseLifecycleFromPhase:          true,
		vocabulary.CaseLifecycleToPhase:            true,
		vocabulary.CaseTransitionSource:            true,
		vocabulary.CaseTransitionAt:                true,
		vocabulary.CaseTransitionFrom:              true,
	}
	for predicate := range engineOwned {
		if writable[predicate] {
			t.Fatalf("engine-owned predicate %q is author-writable", predicate)
		}
	}

	for _, p := range vocabulary.WorldFactPredicates() {
		if !engineOwned[p] && !writable[p] {
			t.Fatalf("world fact %q is neither engine-owned nor author-writable; nothing can ever set it", p)
		}
	}

	for _, p := range []vocabulary.Predicate{
		vocabulary.TurnPhaseCurrent, vocabulary.TurnVerdictRef, vocabulary.CampaignSeedValue,
	} {
		if world.AuthorWritable(p) {
			t.Fatalf("engine bookkeeping predicate %q is author-writable", p)
		}
	}
}

// A line past the scanner budget must still name its line. bufio's own error
// is "token too long" with no position, which is precisely the "your world is
// broken somewhere" message the line numbers exist to prevent.
func TestParseEntities_RejectsAnOversizedLineAndNamesIt(t *testing.T) {
	huge := `{"local_id":"rook","type":"character","triples":[` +
		`{"predicate":"world.entity.name","object":"` + strings.Repeat("a", world.MaxEntityLineBytes) + `"}]}`

	err := parseErr(t, gatehouseLine, huge)

	var lineError *world.LineError
	if !errorAs(err, &lineError) {
		t.Fatalf("expected a *world.LineError, got %T: %v", err, err)
	}
	if lineError.Line != 2 {
		t.Fatalf("error names line %d, want 2", lineError.Line)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("rejection reason %q does not explain the size violation", err)
	}
}
