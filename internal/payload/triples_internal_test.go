package payload

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The projection is unexported on purpose — no caller outside this package can
// assemble triples that skip the gate — so its failure modes are exercised
// here, in-package, rather than through a payload whose object map is
// hard-coded correct and therefore cannot express the mistakes the gate exists
// to catch.

const (
	projTurnEntity = "c360.semmachina.world1.starter.turn.turn-act-1"
	projTurnID     = "turn-act-1"
	projRef        = "obj://rolls/turn-act-1"
)

var projTime = time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)

type projectionTestPayload struct{ err error }

func (p *projectionTestPayload) Schema() message.Type {
	return message.Type{Domain: Domain, Category: "projection_test", Version: SchemaVersion}
}

func (p *projectionTestPayload) Validate() error { return p.err }

// MarshalJSON and UnmarshalJSON complete message.Payload. The projection never
// serializes its payload — it only validates it — so an empty body is honest.
func (p *projectionTestPayload) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (p *projectionTestPayload) UnmarshalJSON(_ []byte) error { return nil }

// validProjection is the shape every case below mutates one field of.
func validProjection() tripleProjection {
	return tripleProjection{
		payload:    &projectionTestPayload{},
		subject:    projTurnEntity,
		turnID:     projTurnID,
		source:     "dice",
		at:         projTime,
		registered: []vocabulary.Predicate{vocabulary.TurnRollBand, vocabulary.TurnRollTotal},
		objects: map[vocabulary.Predicate]any{
			vocabulary.TurnRollBand:  string(vocabulary.BandPartial),
			vocabulary.TurnRollTotal: 9,
		},
		refPredicate: vocabulary.TurnRollRef,
		refName:      "roll ref",
		ref:          projRef,
	}
}

func TestTripleProjection_ValidFixtureProducesTheRegisteredSetPlusOneReference(t *testing.T) {
	triples, err := validProjection().build()
	if err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}
	if len(triples) != 3 {
		t.Fatalf("emitted %d triples, want two scalars plus one reference", len(triples))
	}

	wantOrder := []string{
		vocabulary.TurnRollBand.String(),
		vocabulary.TurnRollTotal.String(),
		vocabulary.TurnRollRef.String(),
	}
	for idx, triple := range triples {
		if triple.Predicate != wantOrder[idx] {
			t.Fatalf("triple %d is %q, want %q; emission order must be stable", idx, triple.Predicate, wantOrder[idx])
		}
		if triple.Subject != projTurnEntity {
			t.Fatalf("triple %q lands on %q, want %q", triple.Predicate, triple.Subject, projTurnEntity)
		}
		if triple.Context != projTurnID {
			t.Fatalf("triple %q carries context %q, want %q", triple.Predicate, triple.Context, projTurnID)
		}
		if triple.Source != "dice" {
			t.Fatalf("triple %q carries source %q", triple.Predicate, triple.Source)
		}
		if !triple.Timestamp.Equal(projTime) {
			t.Fatalf("triple %q is stamped %v, want %v", triple.Predicate, triple.Timestamp, projTime)
		}
		if triple.Confidence != 1.0 {
			t.Fatalf("triple %q carries confidence %v; a structured exit is recorded as stated", triple.Predicate, triple.Confidence)
		}
	}
	if triples[len(triples)-1].Object != projRef {
		t.Fatalf("the last triple is not the reference: %+v", triples[len(triples)-1])
	}
}

// The rejection table IS the discipline. Each case is a way a payload author
// could put something into the graph's rule-matching surface that does not
// belong there, or leave something out that does.
func TestTripleProjection_RejectsEveryWayToBreakTheDiscipline(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*tripleProjection)
		wantErr string
	}{
		{
			name: "an object for a predicate this payload never registered",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnVerdictConsequence] = "harm"
			},
			wantErr: "must travel by reference",
		},
		{
			name: "a registered predicate with no object",
			mutate: func(p *tripleProjection) {
				delete(p.objects, vocabulary.TurnRollTotal)
			},
			wantErr: "no field supplies registered predicate",
		},
		{
			name: "a slice object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollTotal] = []int{4, 5}
			},
			wantErr: "travel by reference",
		},
		{
			name: "a struct object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollBand] = SeedSource{CampaignID: "c", TurnID: "t"}
			},
			wantErr: "travel by reference",
		},
		{
			name: "a map object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollBand] = map[string]int{"miss": 1}
			},
			wantErr: "travel by reference",
		},
		{
			name: "a float object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollTotal] = 9.0
			},
			wantErr: "travel by reference",
		},
		{
			name: "a nil object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollBand] = nil
			},
			wantErr: "has no object",
		},
		{
			name: "an empty string object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollBand] = ""
			},
			wantErr: "empty object",
		},
		{
			// The hazard vocabulary/references.go names: a reference predicate is
			// the one place a SHORT sentence passes every other gate — it is a
			// bounded scalar string — and lands free text where rules match.
			name: "a sentence where the reference goes",
			mutate: func(p *tripleProjection) {
				p.ref = "the batch was refused because health would go to 43"
			},
			wantErr: "not a storage reference",
		},
		{
			name: "a reference naming a store and no object in it",
			mutate: func(p *tripleProjection) {
				p.ref = "obj://SEMMACHINA_CONTENT"
			},
			wantErr: "carries no key",
		},
		{
			// The gate applies to every projected predicate, not only to the
			// projection's designated reference slot: a *.ref predicate in a
			// payload's registered list would otherwise walk past it.
			name: "a non-pointer on a reference predicate in the registered set",
			mutate: func(p *tripleProjection) {
				p.registered = append(p.registered, vocabulary.TurnNarrationRef)
				p.objects[vocabulary.TurnNarrationRef] = true
			},
			wantErr: "nothing else may be written there",
		},
		{
			name: "a sentence on a reference predicate in the registered set",
			mutate: func(p *tripleProjection) {
				p.registered = append(p.registered, vocabulary.TurnNarrationRef)
				p.objects[vocabulary.TurnNarrationRef] = "the courier levers the gate open, and it gives"
			},
			wantErr: "not a storage reference",
		},
		{
			name: "a string object past the triple-object budget",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnRollBand] = strings.Repeat("a", MaxTripleObjectBytes+1)
			},
			wantErr: "triple-object budget",
		},
		{
			name: "the reference predicate is also a scalar predicate",
			mutate: func(p *tripleProjection) {
				p.refPredicate = vocabulary.TurnRollBand
			},
			wantErr: "also a scalar predicate",
		},
		{
			name: "a predicate registered twice",
			mutate: func(p *tripleProjection) {
				p.registered = append(p.registered, vocabulary.TurnRollBand)
			},
			wantErr: "registered twice",
		},
		{
			name: "a predicate the graph write gate would reject",
			mutate: func(p *tripleProjection) {
				p.registered = []vocabulary.Predicate{"turn.roll.total_value"}
				p.objects = map[vocabulary.Predicate]any{"turn.roll.total_value": 9}
			},
			wantErr: "write gate",
		},
		{
			name:    "an invalid payload",
			mutate:  func(p *tripleProjection) { p.payload = &projectionTestPayload{err: errors.New("bad verdict")} },
			wantErr: "bad verdict",
		},
		{
			name:    "no payload at all",
			mutate:  func(p *tripleProjection) { p.payload = nil },
			wantErr: "no payload",
		},
		{
			name:    "a subject that is not a canonical entity id",
			mutate:  func(p *tripleProjection) { p.subject = "turn-act-1" },
			wantErr: "turn entity id",
		},
		{
			name:    "an empty reference",
			mutate:  func(p *tripleProjection) { p.ref = "" },
			wantErr: "roll ref is required",
		},
		{
			name:    "no reference predicate",
			mutate:  func(p *tripleProjection) { p.refPredicate = "" },
			wantErr: "no reference predicate",
		},
		{
			name:    "no source",
			mutate:  func(p *tripleProjection) { p.source = "" },
			wantErr: "triple source",
		},
		{
			name:    "no timestamp",
			mutate:  func(p *tripleProjection) { p.at = time.Time{} },
			wantErr: "timestamp",
		},
		{
			name:    "nothing registered",
			mutate:  func(p *tripleProjection) { p.registered = nil; p.objects = nil },
			wantErr: "registers no predicates",
		},
		{
			name:    "a turn entity that addresses a different turn",
			mutate:  func(p *tripleProjection) { p.turnID = "turn-act-2" },
			wantErr: "the two name one turn or neither does",
		},
		{
			name:    "no turn id at all",
			mutate:  func(p *tripleProjection) { p.turnID = "" },
			wantErr: "turn_id",
		},
		{
			name:    "reference-less and still carrying a reference",
			mutate:  func(p *tripleProjection) { p.refless = true },
			wantErr: "either has a bulky half",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projection := validProjection()
			tc.mutate(&projection)

			triples, err := projection.build()
			if err == nil {
				t.Fatalf("the projection accepted %s and emitted %d triples", tc.name, len(triples))
			}
			if triples != nil {
				t.Fatalf("a refused projection returned %d triples; a partial set is a half-written entity", len(triples))
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// reflessProjection is a projection whose payload genuinely has no bulky half:
// every fact it carries is closed-vocabulary rule-matching surface.
func reflessProjection() tripleProjection {
	return tripleProjection{
		payload:    &projectionTestPayload{},
		subject:    projTurnEntity,
		turnID:     projTurnID,
		source:     "turn-recorder",
		at:         projTime,
		registered: []vocabulary.Predicate{vocabulary.TurnPhaseCurrent},
		objects: map[vocabulary.Predicate]any{
			vocabulary.TurnPhaseCurrent: string(vocabulary.PhaseApplying),
		},
		refless: true,
	}
}

func TestTripleProjection_ReflessShapeEmitsTheRegisteredSetAndNothingElse(t *testing.T) {
	triples, err := reflessProjection().build()
	if err != nil {
		t.Fatalf("the reference-less fixture was rejected: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("emitted %d triples, want exactly the one registered predicate: %+v", len(triples), triples)
	}
	if triples[0].Predicate != vocabulary.TurnPhaseCurrent.String() {
		t.Fatalf("emitted %q, want %q", triples[0].Predicate, vocabulary.TurnPhaseCurrent)
	}
	if triples[0].Context != projTurnID {
		t.Fatalf("triple carries context %q, want the turn id %q", triples[0].Context, projTurnID)
	}
}

// The extension must not open a hole: a reference-BEARING projection that
// forgets its reference is still the same rejection it always was, and every
// object gate still applies to a reference-less one.
func TestTripleProjection_ReflessShapeKeepsEveryOtherGate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*tripleProjection)
		wantErr string
	}{
		{
			name:    "a forgotten reference is still an error, not a refless projection",
			mutate:  func(p *tripleProjection) { p.refless = false },
			wantErr: "no reference predicate",
		},
		{
			name: "an unregistered predicate",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnFailureReason] = "effect-invalid"
			},
			wantErr: "must travel by reference",
		},
		{
			name: "a non-scalar object",
			mutate: func(p *tripleProjection) {
				p.objects[vocabulary.TurnPhaseCurrent] = []string{"applying"}
			},
			wantErr: "travel by reference",
		},
		{
			name:    "a mismatched turn entity",
			mutate:  func(p *tripleProjection) { p.subject = "c360.semmachina.world1.starter.turn.turn-act-9" },
			wantErr: "the two name one turn or neither does",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projection := reflessProjection()
			tc.mutate(&projection)

			triples, err := projection.build()
			if err == nil {
				t.Fatalf("the reference-less projection accepted %s and emitted %d triples", tc.name, len(triples))
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection reason %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// The reference gate is DERIVED from the predicate name, so it must hold for
// every predicate that name selects — not for the two the table above happens to
// use. A new *.ref predicate inherits it or this fails.
func TestCheckTripleObject_EveryReferencePredicateRefusesEverythingButAPointer(t *testing.T) {
	const sentence = "the courier levers the gate open, and it gives"

	// Anti-vacuity: the sentence is a bounded scalar string, so every OTHER gate
	// in this file accepts it. Only the reference check stops it.
	if len(sentence) > MaxTripleObjectBytes {
		t.Fatal("the sentence is past the shape budget, so this proves nothing about the reference check")
	}
	if err := checkTripleObject(vocabulary.WorldEntityName, sentence); err != nil {
		t.Fatalf("the sentence is refused on an ordinary predicate too (%v); this test would pass for the "+
			"wrong reason", err)
	}

	predicates := vocabulary.StorageRefPredicates()
	if len(predicates) == 0 {
		t.Fatal("no reference predicates are registered; this test proves nothing")
	}
	for _, predicate := range predicates {
		if err := checkTripleObject(predicate, sentence); err == nil {
			t.Fatalf("%q accepted a sentence; free text on a reference predicate passes every other gate on "+
				"the way to the graph", predicate)
		}
		if err := checkTripleObject(predicate, 7); err == nil {
			t.Fatalf("%q accepted an integer; a reference is a pointer", predicate)
		}
		if err := checkTripleObject(predicate, "obj://SEMMACHINA_CONTENT/turn/turn-act-1/x"); err != nil {
			t.Fatalf("%q refused a well-formed reference: %v", predicate, err)
		}
	}
}

// A rejection naming two unregistered predicates must name them in the same
// order every run: ranging a map for the message would make an operator chasing
// a rejection see a different cause per run.
func TestTripleProjection_UnregisteredPredicateMessageIsStable(t *testing.T) {
	const attempts = 20
	messages := make(map[string]bool)
	for range attempts {
		projection := validProjection()
		projection.objects[vocabulary.TurnVerdictRisk] = "high"
		projection.objects[vocabulary.TurnNarrationRef] = "obj://prose/x"

		_, err := projection.build()
		if err == nil {
			t.Fatal("two unregistered predicates were accepted")
		}
		messages[err.Error()] = true
	}
	if len(messages) != 1 {
		t.Fatalf("%d attempts produced %d different rejection reasons: %v", attempts, len(messages), messages)
	}
}
