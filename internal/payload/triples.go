package payload

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The storage-reference grammar, stated where it is ENFORCED.
//
// vocabulary/references.go names the hazard exactly — "a sentence handed to
// turn.failure.ref passes every shape gate on the way to the graph" — and until
// now the only thing that actually stopped one was the content store, which
// validates the references IT mints. That is a runtime validator over one caller,
// not an enforcement point (F16): TurnState is a decodable wire payload, so a
// decoded one could carry a sentence in action_ref, project it onto
// turn.action.ref, and pass every gate between here and ENTITY_STATES.
//
// So the grammar lives here, in the package the projection is in, and the
// projection applies it to every reference predicate. The content store consumes
// it rather than restating it — one statement of "what a reference is", checked
// on the way to the graph rather than only on the way out of a writer.
//
// It deliberately checks SHAPE and not resolvability: whether an object exists
// behind a reference is a question for the store that holds it, and this layer
// has no store. What it can prove is that the value is a pointer at all.
const (
	// RefScheme prefixes every storage reference the engine writes. It makes a
	// reference recognizable on sight — in a triple, in a ledger manifest, in a
	// log line — and makes anything else obviously not one.
	RefScheme = "obj://"
	// RefSeparator divides the storage instance from the key inside a reference.
	// The key contains separators of its own, so the split is at the FIRST one.
	//
	// Exported alongside the scheme because a reference is COMPOSED by the store
	// that mints it and CHECKED here: two separators would split a reference
	// somewhere other than where it was joined, and the instance and key would
	// come back subtly wrong rather than obviously so.
	RefSeparator = "/"
)

// RequireStorageRef rejects anything that is not a storage reference.
//
// Exported because the content store's parser is the other half of this contract
// and must not re-derive it: a writer that minted references by one grammar and a
// projection that accepted another would disagree exactly where it costs the
// most — a reference on the graph that nothing can resolve.
func RequireStorageRef(field, value string) error {
	rest, ok := strings.CutPrefix(value, RefScheme)
	if !ok {
		return fmt.Errorf(
			"%s is %q, which is not a storage reference: it does not begin with %q. A reference predicate "+
				"whose object is a sentence passes every shape gate on the way to the graph, so the scheme is "+
				"the check that it is a pointer at all", field, value, RefScheme)
	}
	instance, key, found := strings.Cut(rest, RefSeparator)
	if !found || key == "" {
		return fmt.Errorf(
			"%s is %q, which carries no key; a reference addresses ONE object inside a store, and one that "+
				"names only the store addresses nothing", field, value)
	}
	if instance == "" {
		return fmt.Errorf(
			"%s is %q, which names no storage instance; a reference resolves through the instance that wrote "+
				"it, so one without an instance resolves nowhere", field, value)
	}
	return nil
}

// MaxProseBytes bounds one turn's narration.
//
// Prose lives in ObjectStore rather than on a triple, so the triple-object
// budget does not apply and this bound is doing a different job: it is the
// narrator's half of the same argument every other LLM-authored field makes.
// Unbounded, the first thing that would stop a runaway generation is NATS's 1 MB
// max payload — a transport failure, opaque, after the tokens are already spent
// — and the object it would leave behind is read back by the egress path, the
// ledger reader, and the chronicler.
//
// 16 KiB is roughly 2,700 words: an order of magnitude past the "two to four
// sentences for an ordinary turn" the starter world's narrator is told to write,
// and an order of magnitude short of a chapter. A narrator that needs more than
// this is not narrating a turn.
//
// It lives HERE rather than in the content store because it now bounds two
// things: the artifact the store writes, and the prose a delivered TurnDelivery
// carries to a player. Two numbers would mean prose that stores durably and then
// cannot be delivered — the failure that only appears on the turn a narrator
// finally runs long.
const MaxProseBytes = 16 * 1024

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
	// subject is the entity the triples land on — by default the turn entity.
	subject string
	// turnID is the turn these triples describe. It is stamped on every triple
	// as the correlation context AND checked against subject, because the two
	// are one fact wearing two shapes — turn_id is the instance segment of the
	// turn entity's id — and every per-turn stage receives them as two
	// independent arguments. Checking the pairing here rather than per caller is
	// what makes a stage handed turn A's entity with turn B's payload a refusal
	// instead of a world change filed under the wrong turn's paperwork.
	turnID string
	// playerSubject NAMES the player entity when a projection lands on the
	// player rather than on the turn, and it is the whole of that mode.
	//
	// Exactly one projection needs it: the pointer the turn recorder writes on a
	// player saying which turn they currently hold. Its subject is a player and
	// its turnID names the turn it points AT, so the default pairing —
	// "the subject's instance segment is this turn id" — is false of a perfectly
	// correct write and would refuse it.
	//
	// It is a NAME rather than a boolean, and the polarity is deliberate on both
	// counts. A boolean would say only "skip the turn check", leaving the subject
	// unpaired with anything, so a projection handed player A's entity with
	// player B's payload would land on A and look right. Naming the player makes
	// the check the same SHAPE as the turn's — the payload states who it is
	// about, the caller states where it goes, and disagreeing is a refusal. And
	// because the zero value is empty, a caller who forgets the field gets the
	// STRICT turn pairing and a loud failure, never a silently unchecked subject.
	playerSubject string
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
	// scalarless DECLARES that this payload has no rule-matched half, so there
	// are no registered predicates for it to project.
	//
	// It is refless's mirror image and exists for the same reason. The narrator
	// produces prose and nothing else: everything structural about the turn was
	// decided and landed by an earlier stage, so the narration's only mark on the
	// graph is the reference to its prose. Inferring that from an empty
	// `registered` list would make a payload whose scalars were FORGOTTEN
	// indistinguishable from one that has none — the same failure "may have a
	// reference" would have introduced at the other end — so the claim is
	// explicit and the two modes cannot both be set: a projection that is neither
	// scalar nor reference projects nothing at all.
	scalarless bool
}

// build runs the discipline and returns the triples, or the first violation.
func (p tripleProjection) build() ([]message.Triple, error) {
	if p.payload == nil {
		return nil, fmt.Errorf("triple projection has no payload to validate")
	}
	if err := p.payload.Validate(); err != nil {
		return nil, err
	}
	if err := p.checkSubject(); err != nil {
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

// checkSubject pairs the entity these triples land on with the payload that
// claims them. There is no unpaired mode: a projection either lands on the turn
// its turn_id names or on the player its payload names.
func (p tripleProjection) checkSubject() error {
	if p.playerSubject == "" {
		return RequireTurnEntityID(p.turnID, p.subject)
	}
	// The turn id is still checked, because it is stamped on every triple as
	// the correlation context and an unchecked one would put a malformed
	// correlation on a perfectly valid fact.
	if err := requireIDSegment("turn_id", p.turnID); err != nil {
		return err
	}
	if err := requireEntityID("player entity id", p.subject); err != nil {
		return err
	}
	if p.subject != p.playerSubject {
		return fmt.Errorf(
			"triples land on entity %q but the payload is about player %q; a projection writes the facts one "+
				"payload states onto the entity that payload names, or it writes one player's state onto another",
			p.subject, p.playerSubject)
	}
	return nil
}

// checkPredicateSet proves the projection is exactly the registered one.
func (p tripleProjection) checkPredicateSet() error {
	if p.scalarless && p.refless {
		return fmt.Errorf(
			"triple projection declares itself both scalar-less and reference-less, so it would write nothing; " +
				"a payload that puts nothing on the graph must not be projected at all")
	}
	switch {
	case p.scalarless:
		if len(p.registered) != 0 {
			return fmt.Errorf(
				"triple projection declares itself scalar-less and still registers %v; a payload either has a "+
					"rule-matched half or it does not", p.registered)
		}
	case len(p.registered) == 0:
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

// checkTripleObject is the shape gate: closed-vocabulary scalars only, bounded,
// and a POINTER wherever the predicate says it points.
//
// The type switch is the structural half of "no bulky field reaches a triple".
// A slice of effect intents, a band map, a modifier list, or a nested struct
// cannot be projected at all — not because a reviewer noticed, but because
// there is no case for it. The byte bound catches the remaining shape a bulky
// field can wear: a long string.
//
// The reference check closes the hole those two leave open. A `*.ref` predicate
// carries the engine's escape hatch from the triple budget, and a SHORT sentence
// is a bounded scalar string: it passes the type switch, passes the byte bound,
// and lands free text on the surface rules match — which is the exact failure the
// closed failure code exists to prevent, relocated one predicate over. It is
// applied to every projected predicate rather than only to the projection's
// designated reference slot, because a reference predicate appearing in a
// payload's registered list would otherwise skip it entirely.
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
		if vocabulary.IsStorageRef(predicate) {
			return RequireStorageRef(fmt.Sprintf("the object of %s", predicate), value)
		}
		return nil
	case bool, int:
		if vocabulary.IsStorageRef(predicate) {
			return fmt.Errorf(
				"predicate %q carries a %T object; a storage reference is a %q pointer, and nothing else may "+
					"be written there", predicate, object, RefScheme)
		}
		return nil
	case nil:
		return fmt.Errorf("predicate %q has no object", predicate)
	default:
		return fmt.Errorf(
			"predicate %q carries a %T object; only closed-vocabulary scalars (string, bool, int) may become "+
				"triples — bulky and rule-opaque fields travel by reference", predicate, object)
	}
}
