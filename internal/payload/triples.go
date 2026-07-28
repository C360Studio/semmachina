package payload

import (
	"fmt"
	"slices"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxTripleObjectBytes bounds one triple object the engine writes.
//
// The largest LEGITIMATE object in the turn vocabulary is a six-part entity ID,
// so the upstream ID budget is the natural ceiling: every closed-vocabulary
// value, every storage reference, and every entity reference fits comfortably
// under it, and prose does not. It bounds the SHAPE of what reaches the graph's
// per-key CAS path; the registered-predicate list below bounds the MEANING. Two
// different questions, both of which have to be answered for "no bulky field
// reaches a triple" to be structural rather than conventional.
const MaxTripleObjectBytes = types.MaxEntityIDBytes

// tripleProjection is the ONE statement of the payload-to-triple discipline:
// validate the payload, project ONLY registered predicates, append EXACTLY one
// reference.
//
// It exists because that discipline was, until now, enforced in exactly one
// place — Verdict.Triples — and re-implemented by hand everywhere else it was
// needed. The rule it protects (F6, and M1 behind it) is that the graph's
// rule-matching surface carries small closed-vocabulary scalars and nothing
// else: bulky fields and LLM-authored prose travel by reference, because a rule
// that can see them is a rule that can branch on fiction. A discipline restated
// per payload is a discipline that holds for the payloads whose authors
// remembered it.
//
// The projection is deliberately unexported. Callers outside this package
// cannot assemble a triple set that skips the gate; they get a payload's
// Triples method or nothing.
type tripleProjection struct {
	// payload is validated before anything is projected. A payload that
	// cannot pass its own contract must not reach the graph in fragments.
	payload message.Payload
	// subject is the entity the triples land on (the turn entity).
	subject string
	// turnID is the turn these triples describe. It is stamped on every triple
	// as the correlation context AND checked against subject, because the two
	// are one fact wearing two shapes — turn_id is the instance segment of the
	// turn entity's id — and every per-turn stage receives them as two
	// independent arguments. Checking the pairing here rather than per caller is
	// what makes a stage handed turn A's entity with turn B's payload a refusal
	// instead of a world change filed under the wrong turn's paperwork.
	turnID string
	// source names the producing component.
	source string
	// at stamps every triple. Passed in rather than read from the clock so a
	// projection's output is an assertable value.
	at time.Time

	// registered is the exact predicate list this payload may project, in
	// emission order. It comes from internal/vocabulary, never from the
	// caller's imagination.
	registered []vocabulary.Predicate
	// objects supplies one object per registered predicate. A missing
	// registered predicate and an object for an unregistered one are both
	// errors: the first is a silently dropped fact, the second is a fact
	// nobody registered reaching the rule-matching surface.
	objects map[vocabulary.Predicate]any

	// refPredicate, refName, and ref carry the single reference to the stored
	// payload — the escape hatch that makes the bulky half reachable without
	// putting it in the graph.
	refPredicate vocabulary.Predicate
	refName      string
	ref          string
	// refless DECLARES that this payload has no bulky half, so there is nothing
	// for a reference to point at.
	//
	// It is an explicit claim rather than an inferred one, and that distinction
	// is the whole reason it is a field. The turn's own state — a phase, a
	// player, a scene, a closed failure code — is entirely rule-matching surface
	// with no stored payload behind it, and forcing it to invent a reference
	// would have turned the gate into a formality. But relaxing "a projection
	// must carry a reference" into "a projection may carry one" would make a
	// FORGOTTEN reference indistinguishable from a deliberate absence, which is
	// exactly how the discipline this gate exists to hold gets lost. So the two
	// shapes are mutually exclusive and both are checked: refless with a
	// reference set is a contradiction, and reference-bearing with no reference
	// predicate is still the same error it always was.
	refless bool
}

// build runs the discipline and returns the triples, or the first violation.
func (p tripleProjection) build() ([]message.Triple, error) {
	if p.payload == nil {
		return nil, fmt.Errorf("triple projection has no payload to validate")
	}
	if err := p.payload.Validate(); err != nil {
		return nil, err
	}
	if err := RequireTurnEntityID(p.turnID, p.subject); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("triple source", p.source); err != nil {
		return nil, err
	}
	if p.at.IsZero() {
		return nil, fmt.Errorf("triple projection requires a timestamp")
	}
	// The predicate-set contract runs BEFORE the reference value is checked,
	// because it is what decides whether a reference is expected at all. The
	// other order reported a missing reference by its (also missing) field name,
	// so a projection that forgot its reference entirely was rejected as
	// " is required" — true, and useless.
	if err := p.checkPredicateSet(); err != nil {
		return nil, err
	}
	if !p.refless {
		if err := requireNonEmpty(p.refName, p.ref); err != nil {
			return nil, err
		}
	}

	triples := make([]message.Triple, 0, len(p.registered)+1)
	for _, predicate := range p.registered {
		object := p.objects[predicate]
		if err := checkTripleObject(predicate, object); err != nil {
			return nil, err
		}
		triples = append(triples, p.triple(predicate, object))
	}
	if p.refless {
		return triples, nil
	}
	if err := checkTripleObject(p.refPredicate, p.ref); err != nil {
		return nil, err
	}
	return append(triples, p.triple(p.refPredicate, p.ref)), nil
}

// checkPredicateSet proves the projection is exactly the registered one.
func (p tripleProjection) checkPredicateSet() error {
	if len(p.registered) == 0 {
		return fmt.Errorf("triple projection registers no predicates")
	}
	seen := make(map[vocabulary.Predicate]bool, len(p.registered))
	for _, predicate := range p.registered {
		if seen[predicate] {
			return fmt.Errorf(
				"predicate %q is registered twice; a single-valued predicate written twice is one entity with two values",
				predicate)
		}
		seen[predicate] = true
		if _, ok := p.objects[predicate]; !ok {
			return fmt.Errorf("no field supplies registered predicate %q", predicate)
		}
	}
	if p.refless {
		// A refless projection that also names a reference is a contradiction,
		// and the one that matters: it would emit the reference triple's
		// predicate nowhere while looking, at the call site, like it had.
		if p.refPredicate != "" || p.ref != "" || p.refName != "" {
			return fmt.Errorf(
				"triple projection declares itself reference-less and still carries reference %q/%q; "+
					"a payload either has a bulky half that travels by reference or it does not",
				p.refPredicate, p.ref)
		}
	} else {
		if p.refPredicate == "" {
			return fmt.Errorf("triple projection has no reference predicate")
		}
		if p.refName == "" {
			return fmt.Errorf("reference predicate %q has no field name to report a missing reference by",
				p.refPredicate)
		}
		if seen[p.refPredicate] {
			return fmt.Errorf("reference predicate %q is also a scalar predicate", p.refPredicate)
		}
	}
	// Ranging the map only to report, and only after every registered
	// predicate is accounted for, so the message is deterministic: an
	// unregistered key can only appear alongside a count mismatch.
	if len(p.objects) != len(p.registered) {
		extra := make([]string, 0, len(p.objects))
		for predicate := range p.objects {
			if !seen[predicate] {
				extra = append(extra, string(predicate))
			}
		}
		slices.Sort(extra)
		return fmt.Errorf(
			"triple projection supplies %v, which no registered predicate list includes; "+
				"a field that is not registered rule-matching surface must travel by reference", extra)
	}
	return nil
}

func (p tripleProjection) triple(predicate vocabulary.Predicate, object any) message.Triple {
	return message.Triple{
		Subject:   p.subject,
		Predicate: predicate.String(),
		Object:    object,
		Source:    p.source,
		Timestamp: p.at,
		// Everything projected here is a structured exit the engine recorded
		// as stated, never an inference about the world.
		Confidence: 1.0,
		Context:    p.turnID,
	}
}

// checkTripleObject is the shape gate: closed-vocabulary scalars only, bounded.
//
// The type switch is the structural half of "no bulky field reaches a triple".
// A slice of effect intents, a band map, a modifier list, or a nested struct
// cannot be projected at all — not because a reviewer noticed, but because
// there is no case for it. The byte bound catches the remaining shape a bulky
// field can wear: a long string.
func checkTripleObject(predicate vocabulary.Predicate, object any) error {
	if _, err := ssvocab.ParsePredicate(predicate.String()); err != nil {
		return fmt.Errorf("predicate %q would be rejected by the graph write gate: %w", predicate, err)
	}

	switch value := object.(type) {
	case string:
		if value == "" {
			return fmt.Errorf("predicate %q has an empty object", predicate)
		}
		if len(value) > MaxTripleObjectBytes {
			return fmt.Errorf("predicate %q carries a %d-byte object, which exceeds the %d-byte triple-object budget",
				predicate, len(value), MaxTripleObjectBytes)
		}
		return nil
	case bool, int:
		return nil
	case nil:
		return fmt.Errorf("predicate %q has no object", predicate)
	default:
		return fmt.Errorf(
			"predicate %q carries a %T object; only closed-vocabulary scalars (string, bool, int) may become "+
				"triples — bulky and rule-opaque fields travel by reference", predicate, object)
	}
}
