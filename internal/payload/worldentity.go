package payload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semstreams/message"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxFactTextBytes bounds an author-written string fact.
//
// Descriptions are the retrieval surface a persona reads, so they belong in
// the graph; a chapter does not. Bulky prose has a home already — ObjectStore
// behind a ref-triple — and letting a world package put arbitrary length into
// ENTITY_STATES would put unbounded bytes on the per-key CAS path that every
// turn's writes contend for.
const MaxFactTextBytes = 512

// TemplateRef is a materialized entity's provenance: which template, at which
// version, and under which template-local id it was authored.
//
// It travels on the payload rather than being reconstructed from the entity ID
// because the ID cannot carry the template VERSION — and "which version of the
// template produced this campaign's starting state" is the question an author
// asks first when a world plays wrong.
type TemplateRef struct {
	// ID is the manifest id.
	ID string `json:"id"`
	// Version is the manifest version.
	Version string `json:"version"`
	// LocalID is the entity's template-local identity.
	LocalID string `json:"local_id"`
}

// Validate checks the reference is a complete, well-formed provenance record.
func (r TemplateRef) Validate() error {
	if err := requireNonEmpty("template.version", r.Version); err != nil {
		return err
	}
	if err := requireIDSegment("template.id", r.ID); err != nil {
		return err
	}
	return requireIDSegment("template.local_id", r.LocalID)
}

// WorldFact is one authored fact about a world entity: a registered predicate
// and its object.
//
// The object is deliberately NOT `any`-shaped on the wire. Every fact in the
// v1 world vocabulary is a bounded string or an integer, and a JSON round-trip
// through `any` turns 4 into 4.0 — so a fact written as an attribute value
// would come back as a float and compare unequal to the one that was
// published. UnmarshalJSON narrows the wire types to exactly the two the
// vocabulary defines, and rejects the rest at the boundary instead of letting
// a float64 reach the graph as an attribute.
type WorldFact struct {
	// Predicate is a registered world-fact predicate.
	Predicate vocabulary.Predicate `json:"predicate"`
	// Object is a string or an int.
	Object any `json:"object"`
	// Reference marks the object as an entity ID rather than a literal. It
	// becomes message.EntityReferenceDatatype on the triple, which makes the
	// graph's contract validator check the object as a canonical ID instead of
	// guessing from its shape.
	Reference bool `json:"reference,omitempty"`
}

// UnmarshalJSON narrows the object to the wire types the vocabulary defines.
func (f *WorldFact) UnmarshalJSON(data []byte) error {
	var wire struct {
		Predicate vocabulary.Predicate `json:"predicate"`
		Object    json.RawMessage      `json:"object"`
		Reference bool                 `json:"reference"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}

	object, err := decodeFactObject(wire.Object)
	if err != nil {
		return fmt.Errorf("fact %q: %w", wire.Predicate, err)
	}

	f.Predicate = wire.Predicate
	f.Object = object
	f.Reference = wire.Reference
	return nil
}

// decodeFactObject accepts exactly a JSON string or an integral JSON number.
func decodeFactObject(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("object is required")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("object: %w", err)
	}

	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		integer, err := typed.Int64()
		if err != nil {
			return nil, fmt.Errorf(
				"object %s is not an integer; the v1 world vocabulary has no fractional facts", typed)
		}
		return int(integer), nil
	default:
		return nil, fmt.Errorf(
			"object %s is neither a string nor an integer; the v1 world vocabulary defines no other shape", raw)
	}
}

// Validate checks the predicate is a registered world fact and the object
// matches the shape that predicate carries.
//
// It deliberately does NOT range-check an attribute value. Bounds are a
// WORLD's data (vocabulary.AttributeSpecSet) — a grim survival world wants
// health 0-4 and a heroic one wants 0-40 — and a payload carries no world
// configuration to check against. Enforcing engine defaults here would silently
// make the defaults the law and the AttributeSpecSet decorative. Bounds are
// checked where the world's own numbers are in hand: at package load, and by
// the effect applier at runtime.
func (f WorldFact) Validate() error {
	if _, ok := vocabulary.SubjectKindsFor(f.Predicate); !ok {
		return fmt.Errorf("predicate %q is not a registered world fact", f.Predicate)
	}
	// Defense in depth behind the registry check: the write gate parses every
	// predicate, and a registered constant that stopped parsing would fail
	// there, far from here.
	if _, err := ssvocab.ParsePredicate(f.Predicate.String()); err != nil {
		return fmt.Errorf("predicate %q: %w", f.Predicate, err)
	}

	switch object := f.Object.(type) {
	case string:
		if f.Reference {
			return requireEntityID(f.Predicate.String()+" object", object)
		}
		if object == "" {
			return fmt.Errorf("fact %q has an empty object", f.Predicate)
		}
		if len(object) > MaxFactTextBytes {
			return fmt.Errorf("fact %q is %d bytes, which exceeds the %d-byte fact-text budget",
				f.Predicate, len(object), MaxFactTextBytes)
		}
		return nil
	case int:
		if f.Reference {
			return fmt.Errorf("fact %q is marked as an entity reference but its object is a number", f.Predicate)
		}
		return nil
	case nil:
		return fmt.Errorf("fact %q has no object", f.Predicate)
	default:
		return fmt.Errorf("fact %q carries a %T object; only string and int are defined", f.Predicate, f.Object)
	}
}

// WorldEntity is one materialized world entity on its way to graph-ingest.
//
// It is the ONLY way template content reaches ENTITY_STATES: it implements
// graph.Graphable, so the import travels the framework's ordinary fact lane
// and graph-ingest stays the sole writer. An importer that wrote the KV bucket
// itself would be faster and would silently skip the predicate contract, the
// hierarchy inference, and the merge semantics that make re-import converge.
type WorldEntity struct {
	// ID is the composed six-part entity ID.
	ID string `json:"id"`
	// Kind is the declared entity kind. It equals the type position of ID by
	// construction, and Validate enforces that so the two cannot drift.
	Kind vocabulary.EntityKind `json:"kind"`
	// Template records which template, version, and local id produced this.
	Template TemplateRef `json:"template"`
	// Facts are the authored starting facts. The kind triple is NOT among them
	// — it is projected from Kind, so one declaration produces both the ID
	// position and the triple.
	Facts []WorldFact `json:"facts"`
	// RecordedAt stamps every produced triple. Carried on the payload rather
	// than read from the clock inside Triples() because Graphable.Triples takes
	// no arguments, and a method that silently reads wall-clock time is a
	// method whose output cannot be asserted.
	RecordedAt time.Time `json:"recorded_at"`
}

// Schema implements message.Payload.
func (e *WorldEntity) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryWorldEntity, Version: SchemaVersion}
}

// IndexingProfile implements message.IndexingProfiler.
//
// World entities carry the author-written prose the context assembler
// retrieves over, so they are content, not control plane. The declaration
// matters now even though no embedding component runs in this slice: the
// profile is stamped at entity BIRTH and is immutable afterwards (ADR-054), so
// an entity born under the default control floor could never be embedded
// without being recreated.
func (e *WorldEntity) IndexingProfile() string { return ssvocab.IndexingProfileContent }

// EntityID implements graph.Graphable.
func (e *WorldEntity) EntityID() string { return e.ID }

// Validate implements message.Payload.
func (e *WorldEntity) Validate() error {
	if err := requireEntityID("id", e.ID); err != nil {
		return err
	}
	if _, err := vocabulary.ParseEntityKind(string(e.Kind)); err != nil {
		return err
	}
	if err := e.Template.Validate(); err != nil {
		return err
	}
	if err := e.validateIDAgreesWithDeclaration(); err != nil {
		return err
	}
	if e.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	if len(e.Facts) == 0 {
		return fmt.Errorf("entity %q declares no facts", e.ID)
	}
	return e.validateFacts()
}

// validateIDAgreesWithDeclaration pins the ID's meaning-bearing positions to
// the payload's own declarations.
//
// Without it the ID is just a string that happens to look structured, and an
// entity could be a character in its triples and an item in its address. The
// mapping rule is `{org}.semmachina.{world_ns}.{template_id}.{type}.{local_id}`,
// so four of the six positions are checkable against fields carried right here.
func (e *WorldEntity) validateIDAgreesWithDeclaration() error {
	parts := strings.Split(e.ID, ".")
	const (
		platformPosition = 1
		templatePosition = 3
		typePosition     = 4
		instancePosition = 5
		positionCount    = 6
	)
	if len(parts) != positionCount {
		// Unreachable: requireEntityID already rejected a non-six-part ID.
		return fmt.Errorf("id %q does not have %d positions", e.ID, positionCount)
	}
	if parts[platformPosition] != Domain {
		return fmt.Errorf("id %q has platform %q; every SemMachina entity is on the %q platform",
			e.ID, parts[platformPosition], Domain)
	}
	if parts[templatePosition] != e.Template.ID {
		return fmt.Errorf("id %q names template %q but the payload declares %q",
			e.ID, parts[templatePosition], e.Template.ID)
	}
	if parts[typePosition] != string(e.Kind) {
		return fmt.Errorf("id %q has type %q but the payload declares kind %q",
			e.ID, parts[typePosition], e.Kind)
	}
	if parts[instancePosition] != e.Template.LocalID {
		return fmt.Errorf("id %q has instance %q but the payload declares local id %q",
			e.ID, parts[instancePosition], e.Template.LocalID)
	}
	return nil
}

// validateFacts checks vocabulary membership, kind compatibility, and
// single-valued-ness.
//
// The repetition rule is load-bearing rather than fussy. graph-ingest merges an
// arriving Graphable by REPLACING every resident triple that shares a subject
// and predicate, so two `character.attribute.health` facts on one entity are
// not two values — they are one entity with an ambiguous value and no error.
// Relationships are the deliberate exception: carrying two items is the point.
func (e *WorldEntity) validateFacts() error {
	seen := make(map[vocabulary.Predicate]int, len(e.Facts))
	seenPairs := make(map[string]bool, len(e.Facts))

	for index := range e.Facts {
		fact := e.Facts[index]
		if fact.Predicate == vocabulary.WorldEntityKind {
			return fmt.Errorf("fact %d declares %q; the kind triple is projected from the kind field, not authored",
				index, vocabulary.WorldEntityKind)
		}
		if err := fact.Validate(); err != nil {
			return fmt.Errorf("fact %d: %w", index, err)
		}
		if !vocabulary.AllowsSubjectKind(fact.Predicate, e.Kind) {
			allowed, _ := vocabulary.SubjectKindsFor(fact.Predicate)
			return fmt.Errorf("fact %d: %q cannot be carried by a %q entity (allowed kinds: %v)",
				index, fact.Predicate, e.Kind, allowed)
		}

		pair := fmt.Sprintf("%s\x00%v", fact.Predicate, fact.Object)
		if seenPairs[pair] {
			return fmt.Errorf("fact %d repeats %q with the same object", index, fact.Predicate)
		}
		seenPairs[pair] = true

		seen[fact.Predicate]++
		if seen[fact.Predicate] > 1 {
			if !vocabulary.IsMultiValued(fact.Predicate) {
				return fmt.Errorf(
					"fact %d repeats single-valued predicate %q; the graph would replace one with the other "+
						"and neither value would be wrong", index, fact.Predicate)
			}
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (e *WorldEntity) MarshalJSON() ([]byte, error) {
	type Alias WorldEntity
	return json.Marshal((*Alias)(e))
}

// UnmarshalJSON implements json.Unmarshaler.
func (e *WorldEntity) UnmarshalJSON(data []byte) error {
	type Alias WorldEntity
	return json.Unmarshal(data, (*Alias)(e))
}

// WorldImportSource is the Source stamped on every triple an import produces.
// It is how a later reader tells authored starting state apart from anything
// play has written since.
const WorldImportSource = "world-import"

// Triples implements graph.Graphable.
//
// It returns the kind triple first and then one triple per authored fact.
// Invalid entities produce NO triples: Graphable has no error channel, so
// returning a partial set here would hand graph-ingest a half-entity with
// nothing to report. Validate runs at publish time through BaseMessage, so an
// invalid entity never reaches the wire in the first place; this is the
// belt for that suspenders.
func (e *WorldEntity) Triples() []message.Triple {
	if err := e.Validate(); err != nil {
		return nil
	}

	triples := make([]message.Triple, 0, len(e.Facts)+1)
	triples = append(triples, e.triple(vocabulary.WorldEntityKind, string(e.Kind), false))
	for index := range e.Facts {
		fact := e.Facts[index]
		triples = append(triples, e.triple(fact.Predicate, fact.Object, fact.Reference))
	}
	return triples
}

func (e *WorldEntity) triple(predicate vocabulary.Predicate, object any, reference bool) message.Triple {
	datatype := ""
	if reference {
		datatype = message.EntityReferenceDatatype
	}
	return message.Triple{
		Subject:   e.ID,
		Predicate: predicate.String(),
		Object:    object,
		Source:    WorldImportSource,
		Timestamp: e.RecordedAt,
		// Authored starting state is stated, not inferred.
		Confidence: 1.0,
		// Provenance a reader can act on: which template version wrote this.
		Context:  e.Template.ID + "@" + e.Template.Version,
		Datatype: datatype,
	}
}
