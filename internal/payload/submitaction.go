package payload

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/c360studio/semstreams/message"
)

// MaxIdempotencyKeyBytes bounds the client's replay token.
//
// The key is a NAME, not content: a UUIDv4 in canonical form is 36 bytes and a
// ULID is 26, so 128 admits every realistic token shape while refusing a key
// being used as a second, unbounded text field. MaxActionTextBytes bounds the
// one field that is allowed to carry the player's words; without a bound here
// the request has a second one that nothing budgets.
const MaxIdempotencyKeyBytes = 128

// SubmitAction is the PUBLIC submission contract: everything an untrusted
// client is allowed to say, and nothing else.
//
// It exists because PlayerAction is the ENGINE's input contract and must never
// be the wire format. Every identity field on a PlayerAction is server-owned,
// and action_id is the sharpest of them: it is derived DETERMINISTICALLY so a
// redelivered channel message produces one turn rather than two (see
// TurnIDForAction — turn and action are 1:1 and the derivation is the only thing
// that pairs them). That determinism is what makes duplicate delivery safe, and
// it is also what makes a client-chosen action_id an attack: a client that could
// name it could PRE-CLAIM another player's action id, and the victim's genuine
// action would then be absorbed as a duplicate of a turn they never took —
// silently, with an ack, because absorbing duplicates is exactly what the engine
// is supposed to do. There is no downstream check that could tell the two apart,
// which is why the refusal has to be here.
//
// So the gateway injects player_id (from the authenticated session), campaign_id,
// scene_id, the arrival timestamp, the derived action_id, and the channel
// binding. The client supplies the fiction and a replay token.
type SubmitAction struct {
	// Protocol is the version of the public player protocol this request is
	// written against. Required; see ParsePlayerProtocolVersion for why it is
	// never defaulted.
	Protocol PlayerProtocolVersion `json:"protocol"`

	// Text is the player's free-text action — the one field of the canonical
	// PlayerAction a client legitimately supplies. Bounded by the same
	// MaxActionTextBytes the engine's contract states, so a client learns the
	// budget at the boundary that owns it rather than discovering it as a
	// transport failure later.
	Text string `json:"text"`

	// IdempotencyKey is the client's replay token: resubmitting the same action
	// with the same key must resolve to the same turn rather than taking a
	// second one.
	//
	// It is NOT the action id, and a derivation that passed it through unchanged
	// would hand the client the action id under a different name — reintroducing
	// the pre-claim above in full. The gateway (task 9.1) derives action_id from
	// this key SCOPED TO THE AUTHENTICATED player_id, so two players choosing the
	// same token address two different turns and neither can address the other's.
	IdempotencyKey string `json:"idempotency_key"`
}

// clientSuppliedActionField is the ONE json field of the canonical PlayerAction
// that a client supplies. Naming it here — rather than listing the six fields a
// client may not supply — is what makes the refusal set unable to fall behind
// PlayerAction: a field added there is server-owned by default, and making it
// client-owned takes a deliberate edit at this line.
const clientSuppliedActionField = "text"

var (
	// clientOwnedSubmitFields is every json name SubmitAction declares.
	clientOwnedSubmitFields = jsonFieldNames(reflect.TypeFor[SubmitAction]())

	// serverOwnedActionFields is every json name the canonical PlayerAction
	// declares except the one above, INCLUDING the leaves of its nested channel
	// binding — so a client naming reply_to gets told it is the gateway's to
	// stamp rather than the weaker "I do not know that field".
	//
	// Derived rather than listed on purpose. A hand-written list is a second
	// statement of PlayerAction's field set, and the day the two disagree is the
	// day a newly added identity field becomes silently client-settable.
	// TestSubmitAction_ServerOwnedFieldsAreExactlyTheCanonicalActionsIdentity
	// pins the derived set, so a field added to either struct forces a decision
	// instead of inheriting one.
	serverOwnedActionFields = serverOwnedFields()
)

func serverOwnedFields() map[string]struct{} {
	owned := jsonFieldNames(reflect.TypeFor[PlayerAction]())
	delete(owned, clientSuppliedActionField)
	return owned
}

// jsonFieldNames collects the json names of every exported field of t,
// recursing into nested structs so a nested leaf is refused by its own name.
func jsonFieldNames(t reflect.Type) map[string]struct{} {
	names := make(map[string]struct{})
	collectJSONFieldNames(t, names)
	return names
}

func collectJSONFieldNames(t reflect.Type, into map[string]struct{}) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == reflect.TypeFor[time.Time]() {
		return
	}
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		into[name] = struct{}{}

		nested := field.Type
		for nested.Kind() == reflect.Pointer || nested.Kind() == reflect.Slice {
			nested = nested.Elem()
		}
		collectJSONFieldNames(nested, into)
	}
}

// Schema implements message.Payload.
func (s *SubmitAction) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategorySubmitAction, Version: SchemaVersion}
}

// Validate implements message.Payload.
//
// The protocol version is checked FIRST because it is the only failure whose
// remedy is not "fix this field": a client speaking a version this engine does
// not know may have every other field wrong for reasons that follow from that,
// and reporting one of those instead would send the client chasing a symptom.
func (s *SubmitAction) Validate() error {
	if _, err := ParsePlayerProtocolVersion(string(s.Protocol)); err != nil {
		return err
	}
	if err := requireNonEmpty("text", s.Text); err != nil {
		return err
	}
	// A blank action is refused at the boundary rather than adjudicated. This
	// is the untrusted edge, and an action of nothing but spaces is a billed
	// persona call whose prompt input is empty — cost is a policy, and the
	// cheapest place to decline a turn that says nothing is before it starts.
	if strings.TrimSpace(s.Text) == "" {
		return fmt.Errorf("text is %d bytes of whitespace and declares no action", len(s.Text))
	}
	if len(s.Text) > MaxActionTextBytes {
		return fmt.Errorf("text is %d bytes, which exceeds the %d-byte action-text budget",
			len(s.Text), MaxActionTextBytes)
	}
	return validateIdempotencyKey(s.IdempotencyKey)
}

// validateIdempotencyKey holds the replay token to being a token.
//
// The character rule is about ECHO, not about derivation: the key comes back to
// the client in a duplicate or refusal answer and appears in the gateway's
// operator diagnostics, so a key carrying control bytes turns a diagnostic into
// an injection. The derivation itself needs no such rule — it hashes the key
// with the session's player_id and encodes the result, so the entity-ID alphabet
// is the gateway's problem and never the client's.
func validateIdempotencyKey(key string) error {
	if err := requireNonEmpty("idempotency_key", key); err != nil {
		return err
	}
	if len(key) > MaxIdempotencyKeyBytes {
		return fmt.Errorf("idempotency_key is %d bytes, which exceeds the %d-byte key budget",
			len(key), MaxIdempotencyKeyBytes)
	}
	if !utf8.ValidString(key) {
		return fmt.Errorf("idempotency_key is not valid UTF-8, so it is not a token this engine can echo back")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf(
				"idempotency_key contains the control character U+%04X; the key is echoed back in refusals and "+
					"in operator diagnostics, so it must be a name rather than a payload", r)
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (s *SubmitAction) MarshalJSON() ([]byte, error) {
	type Alias SubmitAction
	return json.Marshal((*Alias)(s))
}

// UnmarshalJSON implements json.Unmarshaler, and it is where "a server-owned
// field is REFUSED, not ignored" is enforced.
//
// # Why the gate lives here
//
// Because this is the only conversion from client bytes to a SubmitAction that
// exists. A gate in a separate DecodeSubmitAction helper is a gate the gateway
// author can bypass with a plain json.Unmarshal and never notice — the struct
// would populate perfectly and the extra field would vanish. Put on the
// unmarshaler, there is no bypass short of hand-writing the struct.
//
// # How presence is detected, and why not the obvious way
//
// On the JSON object's KEY SET, before any value is mapped into a Go field, and
// SubmitAction has no field for a server-owned value at all. Both halves are the
// check:
//
//   - Without the key-set read there is nothing to check. encoding/json IGNORES
//     a field the target struct does not declare, so a client sending player_id
//     would be silently corrected — the precise outcome the contract forbids.
//   - Adding fields to hold those values and checking them for emptiness would
//     re-open the hole from the other side. Go's zero value erases the
//     difference between "absent" and "explicitly zero", and for a refuse-if-
//     present check that difference IS the check: "arrived_at":
//     "0001-01-01T00:00:00Z" is a client asserting a timestamp, and .IsZero()
//     reads it as silence. A field that exists is also a field a later author
//     can be tempted to honour "if it is set".
//
// A key present with a null value is refused for the same reason: the client
// wrote the field. Whether they wrote anything in it is not the question.
//
// Names are checked in sorted order so a request carrying two bad fields always
// reports the same one; ranging the map would make the refusal depend on Go's
// randomized iteration order, and a client fixing one field at a time would be
// chasing a different answer each attempt.
func (s *SubmitAction) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("a submission must be a JSON object: %w", err)
	}
	if fields == nil {
		return fmt.Errorf("a submission must be a JSON object, got null")
	}

	names := slices.Sorted(maps.Keys(fields))
	for _, name := range names {
		if _, owned := serverOwnedActionFields[name]; owned {
			return fmt.Errorf(
				"%w: %q is set by the gateway from the authenticated session, not by the client, and a request "+
					"that names one is refused rather than corrected so a confused client learns and a hostile "+
					"one is stopped", ErrServerOwnedField, name)
		}
	}
	for _, name := range names {
		if _, known := clientOwnedSubmitFields[name]; !known {
			return fmt.Errorf("%w: %q is not a field of %s", ErrUnknownField, name, PlayerProtocolV1)
		}
	}

	type Alias SubmitAction
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*s = SubmitAction(alias)
	return nil
}
