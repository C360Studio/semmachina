package content

import (
	"fmt"
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
// It is a string on the graph because a triple object is a scalar. This is the
// canonical spelling of that string, stated once, so a writer and a reader
// cannot each invent one.
const (
	// RefScheme prefixes every reference the engine writes. It exists to make a
	// reference recognizable on sight — in a triple, in a ledger manifest, in a
	// log line — and to make anything else obviously not one.
	RefScheme = "obj://"
	// refSeparator divides the storage instance from the key. The key contains
	// separators of its own, so the split is at the FIRST one and the instance
	// may not contain any.
	refSeparator = "/"
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
	rest, ok := strings.CutPrefix(s, RefScheme)
	if !ok {
		return Ref{}, fmt.Errorf(
			"%q is not a storage reference: it does not begin with %q. A reference predicate whose object is a "+
				"sentence passes every shape gate on the way to the graph, so the scheme is the check that it is "+
				"a pointer at all", s, RefScheme)
	}
	instance, key, found := strings.Cut(rest, refSeparator)
	if !found {
		return Ref{}, fmt.Errorf("storage reference %q carries no key", s)
	}
	ref := Ref{Instance: instance, Key: key}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// keyPrefix is the first segment of every turn-artifact key. Keys are a
// namespace shared with whatever else this store ever holds, so a turn's
// artifacts live under one prefix rather than at the root.
const keyPrefix = "turn"

// KeyFor derives the object key for one turn's artifact.
//
// The derivation is total and pure — the same turn and the same reference
// predicate always compose the same key — and that is the whole idempotency
// story for this store: a redelivered action re-puts the identical bytes at the
// identical key, so at-least-once delivery leaves one object rather than a pile
// of them, with no read-before-write and no conditional put (NATS ObjectStore
// offers neither).
//
// It is derived from turn_id and NOT from the turn's six-part entity ID.
// Deliberately: the entity ID may be up to the full 256-byte budget on its own,
// so a key built from it could compose a reference the triple-object budget
// refuses — after the object had already been written. turn_id is bounded at
// one segment, which makes "the reference always fits" structural rather than
// lucky (TestKeyFor_WorstCaseReferenceFitsTheTripleBudget). The cost is that
// keys are unique per turn id rather than per world namespace, which is exactly
// the instance-per-world assumption the MVP already resolves at the process
// boundary; a process that ever serves two namespaces changes this line, not
// its callers.
func KeyFor(refPredicate vocabulary.Predicate, turnID string) (string, error) {
	slot, err := vocabulary.ArtifactSlot(refPredicate)
	if err != nil {
		return "", err
	}
	if err := vocabulary.ValidateIDSegment(turnID); err != nil {
		return "", fmt.Errorf("artifact key needs a turn id: %w", err)
	}
	return strings.Join([]string{keyPrefix, turnID, slot}, refSeparator), nil
}
