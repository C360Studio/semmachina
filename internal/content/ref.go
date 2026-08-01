package content

import (
	"fmt"
	"slices"
	"strings"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The reference grammar: obj://<storage instance>/<key>.
//
// Two fields rather than one because that is what upstream resolution needs.
// message.StorageReference splits the same way — StorageInstance names WHICH
// store holds the bytes and is resolved through a registry at read time, Key
// addresses the object inside it — and a bare key would be a reference that
// only resolves as long as there is exactly one store forever.
//
// It is a string on the graph because a triple object is a scalar, and the
// canonical spelling of that string lives in internal/payload rather than here.
// That is deliberate: the grammar has to be enforced where a reference reaches
// the GRAPH (the triple projection), not only where one is minted, or a decoded
// payload carrying a sentence in a ref field walks past it. This package writes
// the references and consumes the rule; it does not restate it.
const (
	// RefScheme prefixes every reference the engine writes.
	RefScheme = payload.RefScheme
	// refSeparator divides the storage instance from the key. The key contains
	// separators of its own, so the split is at the FIRST one and the instance
	// may not contain any — which is why it is the projection's separator and
	// not a second one that happens to look the same today.
	refSeparator = payload.RefSeparator
)

// Ref addresses one stored artifact.
//
// The zero value means "no reference", which is a legitimate value for an
// optional one (a turn that failed with no detail to store) and never a
// legitimate value for a required one.
type Ref struct {
	// Instance is the storage instance holding the object — the value
	// message.StorageReference calls StorageInstance.
	Instance string
	// Key addresses the object inside that instance.
	Key string
}

// IsZero reports the absence of a reference.
func (r Ref) IsZero() bool { return r.Instance == "" && r.Key == "" }

// String returns the canonical spelling that lands on a triple.
//
// A zero Ref stringifies to the empty string rather than a scheme with nothing
// behind it, so "no reference" and "a reference to nothing" are the same value
// at every call site that tests one.
func (r Ref) String() string {
	if r.IsZero() {
		return ""
	}
	return RefScheme + r.Instance + refSeparator + r.Key
}

// Validate rejects a reference that could not be resolved or could not be
// written.
func (r Ref) Validate() error {
	if r.Instance == "" {
		return fmt.Errorf("storage reference %q names no storage instance, so nothing can resolve it", r.String())
	}
	if strings.Contains(r.Instance, refSeparator) {
		return fmt.Errorf(
			"storage instance %q contains %q; the instance and the key are split at the first separator, so an "+
				"instance carrying one cannot be read back", r.Instance, refSeparator)
	}
	if r.Key == "" {
		return fmt.Errorf("storage reference on instance %q has no key", r.Instance)
	}
	// The bound is the TRIPLE's, because that is where this string is going.
	// Checking it here means an oversized reference is refused before the object
	// is written, rather than after — a reference the graph refuses would leave
	// the artifact stored and unreachable.
	if len(r.String()) > payload.MaxTripleObjectBytes {
		return fmt.Errorf(
			"storage reference is %d bytes, which exceeds the %d-byte triple-object budget it has to fit in",
			len(r.String()), payload.MaxTripleObjectBytes)
	}
	return nil
}

// StorageReference converts to the upstream reference type.
//
// It exists so a consumer that resolves through storeregistry — the canonical
// "instance name → live store" lookup (ADR-063) — is handed the type that
// lookup speaks, rather than re-deriving the split from our string.
func (r Ref) StorageReference() *message.StorageReference {
	if r.IsZero() {
		return nil
	}
	return &message.StorageReference{
		StorageInstance: r.Instance,
		Key:             r.Key,
		ContentType:     ContentType,
	}
}

// ParseRef reads a reference back from its canonical spelling.
//
// An empty string parses to the zero Ref with no error: that is how an absent
// optional reference round-trips through a triple object or a manifest field
// that legitimately carries none.
func ParseRef(s string) (Ref, error) {
	if s == "" {
		return Ref{}, nil
	}
	// The grammar check is the projection's, not a second copy of it: a writer
	// that minted references by one rule and a projection that accepted another
	// would disagree exactly where it costs the most.
	if err := payload.RequireStorageRef("storage reference", s); err != nil {
		return Ref{}, err
	}
	instance, key, _ := strings.Cut(strings.TrimPrefix(s, RefScheme), refSeparator)
	ref := Ref{Instance: instance, Key: key}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// SubjectKind names WHAT an artifact belongs to — the first segment of every
// key.
//
// It exists because not every stored artifact is a turn's. The chronicler's
// vignettes (roadmap stage 4) belong to a scene or an arc, and the shape a key
// derivation grows when a second subject arrives is a SECOND derivation beside
// this one — with a second store a step behind it. Naming the subject now costs
// one segment that the turn's keys already had, and the derived keys are
// byte-identical to the ones this store has been writing.
//
// Only the turn is declared. A subject kind arrives with the capability that
// stores under it, in the same discipline the entity-kind vocabulary follows:
// speculative members nothing reads are how a closed set stops meaning anything.
type SubjectKind string

// The subjects artifacts belong to.
const (
	// SubjectTurn is one turn's own artifacts: the action, the verdict, the
	// roll, the effect batch, the narration, the failure detail.
	SubjectTurn SubjectKind = "turn"
	// SubjectRevelation stores private testimony by deterministic testimony identity.
	SubjectRevelation SubjectKind = "revelation"
)

var subjectKinds = []SubjectKind{SubjectTurn, SubjectRevelation}

// SubjectKinds returns the closed subject-kind set.
func SubjectKinds() []SubjectKind { return slices.Clone(subjectKinds) }

// KeyFor derives the object key for one subject's artifact:
// <subject kind>/<subject id>/<slot>.
//
// The derivation is total and pure — the same subject and the same reference
// predicate always compose the same key — and that is the whole idempotency
// story for this store: a redelivered action re-puts the identical bytes at the
// identical key, so at-least-once delivery leaves one object rather than a pile
// of them, with no read-before-write and no conditional put (NATS ObjectStore
// offers neither).
//
// The subject id is an ID SEGMENT and NOT a six-part entity ID. Deliberately:
// the entity ID may be up to the full 256-byte budget on its own, so a key built
// from it could compose a reference the triple-object budget refuses — after the
// object had already been written. A segment is bounded, which makes "the
// reference always fits" structural rather than lucky
// (TestKeyFor_WorstCaseReferenceFitsTheTripleBudget). The cost is that keys are
// unique per subject id rather than per world namespace, which is exactly the
// instance-per-world assumption the MVP already resolves at the process
// boundary; a process that ever serves two namespaces changes this line, not its
// callers.
//
// It does NOT check that the subject matches a turn entity. That pairing is a
// property of the CALLER's arguments, not of the key, and it lives in the PutX
// wrappers where both halves of the pair are in hand — a key derivation that
// demanded a turn would have to be edited to store a scene's vignette, which is
// the edit this shape exists to avoid.
func KeyFor(refPredicate vocabulary.Predicate, kind SubjectKind, subjectID string) (string, error) {
	slot, err := vocabulary.ArtifactSlot(refPredicate)
	if err != nil {
		return "", err
	}
	if !slices.Contains(subjectKinds, kind) {
		return "", fmt.Errorf(
			"%q is not a registered artifact subject kind (registered: %v); an unregistered one would put "+
				"artifacts in a namespace no reader looks in", kind, subjectKinds)
	}
	if err := vocabulary.ValidateIDSegment(subjectID); err != nil {
		return "", fmt.Errorf("artifact key needs a %s id: %w", kind, err)
	}
	return strings.Join([]string{string(kind), subjectID, slot}, refSeparator), nil
}
