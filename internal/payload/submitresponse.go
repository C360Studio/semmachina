package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// SubmitStatus is the two-valued answer to a submission.
//
// Two rather than three: there is no "pending". An accepted submission is one
// the engine has DURABLY taken responsibility for — the action is on the stream
// with a broker acknowledgment — and anything short of that is a refusal the
// client may retry. A third status would let a gateway answer "maybe", and a
// player who cannot tell whether their move was taken will take it again.
type SubmitStatus string

// The two answers.
const (
	// StatusAccepted means the action is durably queued under the reported
	// action id. The turn it produces is the reported turn id.
	StatusAccepted SubmitStatus = "accepted"
	// StatusRefused means no action was published. The refusal names why.
	StatusRefused SubmitStatus = "refused"
)

// SubmitRefusalCode is the closed set of reasons a submission is refused.
//
// It is CLOSED and it is part of the player protocol version, because a client
// branches on it: a `turn_in_progress` is worth waiting out, an
// `unsupported_protocol` needs a new client, and a `server_owned_field` needs a
// code change. A free-text reason would make every one of those decisions a
// string match against a message the engine is otherwise free to reword.
//
// Each code names a DIFFERENT remedy. Two codes with the same remedy would be
// one code with two spellings, and the extra spelling only ever fails to be
// handled.
type SubmitRefusalCode string

// The closed refusal set.
const (
	// RefusalUnauthenticated means the connection carries no session. It is
	// distinct from every field-level refusal because none of those can be
	// reported at all: the gateway does not read an unauthenticated client's
	// request body, so it has nothing to name.
	RefusalUnauthenticated SubmitRefusalCode = "unauthenticated"
	// RefusalUnsupportedProtocol means the request declared a protocol version
	// this engine does not speak. Reported before any other field is judged —
	// every other verdict would be reached under the wrong version's rules.
	RefusalUnsupportedProtocol SubmitRefusalCode = "unsupported_protocol"
	// RefusalServerOwnedField means the request named a field the gateway
	// stamps from the authenticated session. Refused rather than sanitized: a
	// client that could set action_id could pre-claim another player's turn.
	RefusalServerOwnedField SubmitRefusalCode = "server_owned_field"
	// RefusalUnknownField means the request named a field this protocol version
	// does not define.
	RefusalUnknownField SubmitRefusalCode = "unknown_field"
	// RefusalMalformedRequest means the bytes are not a submission at all.
	RefusalMalformedRequest SubmitRefusalCode = "malformed_request"
	// RefusalInvalidField means a client-owned field failed its contract — an
	// empty action, a key past the budget.
	RefusalInvalidField SubmitRefusalCode = "invalid_field"
	// RefusalTurnInProgress means this player already holds a non-terminal turn.
	// The refusal names it in ActiveTurnID; queueing is a later change, and
	// refusing while the player is still connected to be told is the MVP.
	RefusalTurnInProgress SubmitRefusalCode = "turn_in_progress"
	// RefusalUnavailable means the engine could not take responsibility for the
	// action: the graph or the action stream was unreachable. It is the ONLY
	// code that carries no diagnosis, deliberately — see SubmitRefusal.Message.
	RefusalUnavailable SubmitRefusalCode = "unavailable"
)

var submitRefusalCodes = []SubmitRefusalCode{
	RefusalUnauthenticated,
	RefusalUnsupportedProtocol,
	RefusalServerOwnedField,
	RefusalUnknownField,
	RefusalMalformedRequest,
	RefusalInvalidField,
	RefusalTurnInProgress,
	RefusalUnavailable,
}

// SubmitRefusalCodes returns the closed refusal set.
func SubmitRefusalCodes() []SubmitRefusalCode { return slices.Clone(submitRefusalCodes) }

// Valid reports membership in the closed set.
func (c SubmitRefusalCode) Valid() bool { return slices.Contains(submitRefusalCodes, c) }

// SubmitRefusal explains one refusal.
type SubmitRefusal struct {
	// Code is the closed reason. A client branches on this and on nothing else.
	Code SubmitRefusalCode `json:"code"`

	// Message is a human-readable diagnosis for the person reading the client,
	// and it is composed by the gateway from what it already told the client in
	// Code — never from an internal error. That restriction is the point: an
	// engine error string names buckets, subjects, entity ids and stack context,
	// and a submission surface is exactly where a curious client would go
	// looking for them. The operator's copy of the real cause goes to the log.
	Message string `json:"message"`

	// Field names the offending request field for the three refusals that have
	// one. Empty otherwise: a field name on a turn_in_progress would be a field
	// the client would try to fix.
	Field string `json:"field,omitempty"`

	// ActiveTurnID names the non-terminal turn this player already holds, and is
	// set only on RefusalTurnInProgress. It is the turn id rather than the turn
	// ENTITY id because the entity id carries the deployment's org, namespace and
	// template positions, and a refusal is not a place to hand an untrusted
	// client the shape of the instance it is talking to.
	ActiveTurnID string `json:"active_turn_id,omitempty"`
}

// Validate checks a refusal states a closed reason and the fields that reason
// requires.
func (r SubmitRefusal) Validate() error {
	if !r.Code.Valid() {
		return fmt.Errorf("submission refusal code %q is not one of %v", r.Code, submitRefusalCodes)
	}
	if r.Message == "" {
		return errors.New("a submission refusal needs a message; a bare code tells a human nothing")
	}
	if r.Code == RefusalTurnInProgress {
		if err := requireIDSegment("active_turn_id", r.ActiveTurnID); err != nil {
			return fmt.Errorf(
				"a %s refusal must name the turn the player is waiting on: %w", RefusalTurnInProgress, err)
		}
	} else if r.ActiveTurnID != "" {
		return fmt.Errorf(
			"refusal %q names active turn %q; only %s has one, and a turn id on any other refusal would have "+
				"the client wait for a turn that is not running", r.Code, r.ActiveTurnID, RefusalTurnInProgress)
	}
	return nil
}

// SubmitResponse is the gateway's answer to one submission: the other half of
// the public player protocol's request document.
//
// One document for both outcomes rather than two, because the transport carries
// frames and a client has to know what it is holding before it can branch. The
// discriminator is Status, and exactly one of the two field groups is populated
// under it.
//
// Like SubmitAction it never crosses NATS and is deliberately absent from the
// payload registry: it is composed from client bytes and written back to the
// same client, and a registered factory would advertise a bus message nothing
// sends.
type SubmitResponse struct {
	// Protocol is the version this answer is written against. Stamped on every
	// answer including the refusal of an unsupported version, so a client that
	// guessed wrong learns what this engine speaks from the refusal itself.
	Protocol PlayerProtocolVersion `json:"protocol"`

	// Status discriminates the two field groups below.
	Status SubmitStatus `json:"status"`

	// IdempotencyKey echoes the key the client sent, when the request parsed far
	// enough to have one. It is what lets a client match an answer to a
	// submission without depending on transport ordering — and matching by
	// action id would not do, because a refused submission has no action id.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// ActionID and TurnID are the accepted submission's identities. Both are
	// derived, not chosen: see SubmitAction.IdempotencyKey for why a client that
	// could name either could pre-claim another player's turn.
	ActionID string `json:"action_id,omitempty"`
	TurnID   string `json:"turn_id,omitempty"`

	// ArrivedAt is the timestamp the engine stamped on the action, and it is
	// returned because it is the one the ENGINE will judge deadlines by. A
	// client that assumed its own send time would disagree with the world.
	ArrivedAt time.Time `json:"arrived_at,omitzero"`

	// Refusal is populated exactly when Status is refused.
	Refusal *SubmitRefusal `json:"refusal,omitempty"`
}

// Validate checks the answer states one outcome completely.
//
// Both directions are checked — an accepted answer carrying a refusal, and a
// refused answer carrying an action id — because a half-populated response is
// how one outcome quietly reads as the other. A refused answer that still
// carried an action id would tell a client its move was taken.
func (r *SubmitResponse) Validate() error {
	if _, err := ParsePlayerProtocolVersion(string(r.Protocol)); err != nil {
		return err
	}

	switch r.Status {
	case StatusAccepted:
		if r.Refusal != nil {
			return fmt.Errorf("an accepted submission carries refusal %q", r.Refusal.Code)
		}
		if err := requireActionID("action_id", r.ActionID); err != nil {
			return err
		}
		if err := requireIDSegment("turn_id", r.TurnID); err != nil {
			return err
		}
		if want := TurnIDForAction(r.ActionID); r.TurnID != want {
			return fmt.Errorf(
				"accepted action %q reports turn %q, but the turn derived from that action is %q; turn and "+
					"action are 1:1 and the derivation is the only thing that pairs them",
				r.ActionID, r.TurnID, want)
		}
		if r.ArrivedAt.IsZero() {
			return errors.New("an accepted submission must report the arrival time the engine will judge it by")
		}
		if err := validateIdempotencyKey(r.IdempotencyKey); err != nil {
			return err
		}
		return nil

	case StatusRefused:
		if r.Refusal == nil {
			return errors.New("a refused submission carries no refusal; a client cannot branch on silence")
		}
		if r.ActionID != "" || r.TurnID != "" {
			return fmt.Errorf(
				"a refused submission reports action %q / turn %q; nothing was published, so an identity here "+
					"tells the client its move was taken", r.ActionID, r.TurnID)
		}
		if !r.ArrivedAt.IsZero() {
			return fmt.Errorf(
				"a refused submission reports an arrival time (%s); nothing arrived", r.ArrivedAt)
		}
		return r.Refusal.Validate()

	default:
		return fmt.Errorf("submission status %q is neither %q nor %q", r.Status, StatusAccepted, StatusRefused)
	}
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion.
func (r *SubmitResponse) MarshalJSON() ([]byte, error) {
	type Alias SubmitResponse
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON implements json.Unmarshaler.
//
// Deliberately permissive where SubmitAction is strict, and the asymmetry is the
// protocol's: a REQUEST refuses a field it does not recognise because acting on
// a misunderstood one is a correctness failure a player pays for, while a RESULT
// is additive so a client may ignore fields a later engine adds.
func (r *SubmitResponse) UnmarshalJSON(data []byte) error {
	type Alias SubmitResponse
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*r = SubmitResponse(alias)
	return nil
}
