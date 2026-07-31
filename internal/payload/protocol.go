package payload

import (
	"errors"
	"fmt"
	"slices"
)

// PlayerProtocolVersion is the version of the PUBLIC player protocol: the
// documents an untrusted client exchanges with the player-session gateway.
// Player/v1 includes bare SubmitAction, the explicitly typed WebSocket
// RetrieveRequest, and every Frame response document (submission answer, pushed
// delivery, retrieval answer, and typed unsupported-operation refusal).
//
// It is a SEPARATE version from SchemaVersion, and conflating the two is the
// mistake this type exists to prevent. SchemaVersion is the payload registry's
// decoder coordinate — it identifies a Go struct travelling between SemMachina
// components on NATS, and it may have to move for a reason no client can
// observe (a field only the ledger reads, a category rename inside the engine).
// This version is a PROMISE TO A THIRD PARTY. Tying them together would either
// freeze the engine's internal evolution behind client compatibility or break
// every client for a change they cannot see. They start equal at v1 and are free
// to diverge.
//
// # What a version covers
//
// One version fixes: the field set and meaning of SubmitAction, RetrieveRequest,
// every response Frame and its embedded documents, and TurnResult,
// the closed vocabularies their values are drawn from (plausibility, risk,
// consequence, outcome band, modifier source, failure reason, turn phase), the
// bounds on client-supplied text and keys, and the shape of Modifier, which both
// protocols share with the engine's own payloads. A change to Modifier is
// therefore a PROTOCOL change even though the type lives beside the internal
// payloads — which is exactly what this version is for saying out loud.
//
// # What a client may assume across one version
//
// Every field named in that version is present with the stated meaning, and no
// field is removed or re-meaned. A later engine MAY ADD fields to a TurnResult,
// so a client must ignore result fields it does not recognise; removing one, or
// changing what one means, is a NEW version and never an edit of this one.
//
// # The asymmetry is deliberate
//
// A REQUEST is strict — SubmitAction and RetrieveRequest refuse fields they do
// not recognise, and SubmitAction refuses a server-owned one by name — while a
// RESULT is additive. They point
// opposite ways because the costs do. Acting on a misunderstood request is a
// correctness failure a player pays for (see SubmitAction: the sharpest case is
// a client that could name its own action_id), and a silently-ignored request
// field teaches a confused client nothing. Ignoring an unrecognised result field
// costs a client nothing at all.
type PlayerProtocolVersion string

// PlayerProtocolV1 is the only version this engine speaks.
//
// The value names WHAT is versioned rather than being a bare "v1", because a
// bare "v1" on the wire beside SchemaVersion's "v1" is two different promises
// spelled identically — and the first time they diverge, every log line and
// every client bug report becomes ambiguous.
const PlayerProtocolV1 PlayerProtocolVersion = "player/v1"

var playerProtocolVersions = []PlayerProtocolVersion{PlayerProtocolV1}

// PlayerProtocolVersions returns every version this engine speaks.
func PlayerProtocolVersions() []PlayerProtocolVersion { return slices.Clone(playerProtocolVersions) }

// Protocol-level refusals a gateway maps to a stable client-facing code. Each
// one names a DIFFERENT remedy, which is the only reason there are three:
// upgrade the client, stop sending that field, correct the field name.
var (
	// ErrUnsupportedProtocol reports a version this engine does not speak. The
	// client's remedy is to upgrade (or downgrade) — nothing about the rest of
	// the request can help, which is why it is checked before anything else.
	ErrUnsupportedProtocol = errors.New("unsupported player protocol version")
	// ErrServerOwnedField reports a request that carried a field the gateway
	// owns. It is REFUSED rather than ignored: sanitizing teaches a confused
	// client nothing and hides a hostile one.
	ErrServerOwnedField = errors.New("server-owned field")
	// ErrUnknownField reports a request field this protocol version does not
	// define — a typo, or a client from a version this engine does not speak
	// that failed to say so.
	ErrUnknownField = errors.New("unknown field")
)

// FieldError names the request field a refusal is about, alongside the sentinel
// that classifies it.
//
// It exists because a gateway has to put the field NAME in a structured answer,
// and the alternative is scraping it out of a message this package is otherwise
// free to reword. That is the shape a client would end up parsing, and a reworded
// diagnosis would silently stop naming a field the client was told to fix.
//
// It wraps the sentinel rather than replacing it, so errors.Is keeps working for
// callers that only want the class.
type FieldError struct {
	// Name is the json field the client sent, or the one whose contract failed.
	Name string
	// Err is the classifying sentinel: ErrServerOwnedField, ErrUnknownField, or
	// nil for a field that simply failed its own contract.
	Err error
	// Detail is the human-readable diagnosis. It is composed from the request
	// and from this package's own contract, never from anything internal, so it
	// is safe to hand back to the client that sent the field.
	Detail string
}

func (e *FieldError) Error() string {
	if e.Err == nil {
		return e.Detail
	}
	return fmt.Sprintf("%s: %s", e.Err, e.Detail)
}

// Unwrap exposes the classifying sentinel to errors.Is.
func (e *FieldError) Unwrap() error { return e.Err }

// fieldErrorf builds a FieldError with a formatted diagnosis.
func fieldErrorf(name string, sentinel error, format string, args ...any) *FieldError {
	return &FieldError{Name: name, Err: sentinel, Detail: fmt.Sprintf(format, args...)}
}

// ParsePlayerProtocolVersion accepts only versions this engine speaks.
//
// An ABSENT version is refused rather than defaulted to v1. A default would mean
// a v2 client that forgot the field gets silently served v1 semantics — the
// failure the version exists to make loud, arriving instead as a mystery about
// field meanings.
func ParsePlayerProtocolVersion(s string) (PlayerProtocolVersion, error) {
	version := PlayerProtocolVersion(s)
	if slices.Contains(playerProtocolVersions, version) {
		return version, nil
	}
	if s == "" {
		return "", fmt.Errorf(
			"%w: no protocol version was declared; it is required rather than defaulted, because defaulting "+
				"would serve a newer client older semantics without either side noticing (this engine speaks %v)",
			ErrUnsupportedProtocol, playerProtocolVersions)
	}
	return "", fmt.Errorf("%w: %q; this engine speaks %v", ErrUnsupportedProtocol, s, playerProtocolVersions)
}
