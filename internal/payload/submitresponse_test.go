package payload_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
)

const responseActionID = "MFRGGZDFMZTWQ2LKNNWG23TPOBYXE43UOJUW4ZY"

var responseAt = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func acceptedResponse() *payload.SubmitResponse {
	return &payload.SubmitResponse{
		Protocol:       payload.PlayerProtocolV1,
		Status:         payload.StatusAccepted,
		IdempotencyKey: "key-1",
		ActionID:       responseActionID,
		TurnID:         payload.TurnIDForAction(responseActionID),
		ArrivedAt:      responseAt,
	}
}

func refusedResponse(refusal payload.SubmitRefusal) *payload.SubmitResponse {
	return &payload.SubmitResponse{
		Protocol:       payload.PlayerProtocolV1,
		Status:         payload.StatusRefused,
		IdempotencyKey: "key-1",
		Refusal:        &refusal,
	}
}

func TestSubmitResponse_AcceptsACompleteAnswerOfEitherShape(t *testing.T) {
	if err := acceptedResponse().Validate(); err != nil {
		t.Fatalf("a complete acceptance was refused: %v", err)
	}
	refused := refusedResponse(payload.SubmitRefusal{
		Code:         payload.RefusalTurnInProgress,
		Message:      "your turn turn-X is still adjudicating",
		ActiveTurnID: "turn-X",
	})
	if err := refused.Validate(); err != nil {
		t.Fatalf("a complete refusal was refused: %v", err)
	}
}

// A half-populated response is how one outcome quietly reads as the other, so
// both directions are checked.
func TestSubmitResponse_RefusesAnAnswerThatStatesTwoOutcomesOrNone(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*payload.SubmitResponse)
		wantSub string
	}{
		{
			name: "an acceptance carrying a refusal",
			mutate: func(r *payload.SubmitResponse) {
				r.Refusal = &payload.SubmitRefusal{Code: payload.RefusalUnavailable, Message: "x"}
			},
			wantSub: "accepted",
		},
		{
			name:    "an acceptance with no action id",
			mutate:  func(r *payload.SubmitResponse) { r.ActionID = "" },
			wantSub: "action_id",
		},
		{
			name:    "an acceptance whose turn id is not derived from its action id",
			mutate:  func(r *payload.SubmitResponse) { r.TurnID = "turn-something-else" },
			wantSub: "1:1",
		},
		{
			name:    "an acceptance with no arrival time",
			mutate:  func(r *payload.SubmitResponse) { r.ArrivedAt = time.Time{} },
			wantSub: "arrival",
		},
		{
			name:    "an acceptance that does not echo the key",
			mutate:  func(r *payload.SubmitResponse) { r.IdempotencyKey = "" },
			wantSub: "idempotency_key",
		},
		{
			name:    "an unknown status",
			mutate:  func(r *payload.SubmitResponse) { r.Status = "maybe" },
			wantSub: "maybe",
		},
		{
			name:    "a protocol version this engine does not speak",
			mutate:  func(r *payload.SubmitResponse) { r.Protocol = "player/v9" },
			wantSub: "protocol",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := acceptedResponse()
			test.mutate(response)
			err := response.Validate()
			if err == nil {
				t.Fatal("the response was accepted")
			}
			if !strings.Contains(err.Error(), test.wantSub) {
				t.Errorf("the refusal %q does not mention %q", err, test.wantSub)
			}
		})
	}
}

// A refused answer that still carried an identity would tell the client its move
// was taken. That is the one lie this contract exists to make impossible.
func TestSubmitResponse_ARefusalMayNotCarryAnIdentityOrAnArrival(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*payload.SubmitResponse)
	}{
		{"an action id", func(r *payload.SubmitResponse) { r.ActionID = responseActionID }},
		{"a turn id", func(r *payload.SubmitResponse) { r.TurnID = "turn-X" }},
		{"an arrival time", func(r *payload.SubmitResponse) { r.ArrivedAt = responseAt }},
		{"no refusal at all", func(r *payload.SubmitResponse) { r.Refusal = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := refusedResponse(payload.SubmitRefusal{
				Code: payload.RefusalInvalidField, Message: "text is required", Field: "text",
			})
			test.mutate(response)
			if err := response.Validate(); err == nil {
				t.Fatal("a refusal carrying an acceptance's fields was accepted")
			}
		})
	}
}

// turn_in_progress is the one refusal a client is expected to act on, so it is
// the one that must name what to wait for — and no other refusal may name a turn
// the client would then wait for pointlessly.
func TestSubmitRefusal_OnlyTurnInProgressNamesATurn(t *testing.T) {
	missing := payload.SubmitRefusal{
		Code: payload.RefusalTurnInProgress, Message: "you already hold a turn",
	}
	if err := missing.Validate(); err == nil {
		t.Fatal("a turn_in_progress refusal that names no turn was accepted; the client is told to wait " +
			"and not told for what")
	}

	for _, code := range payload.SubmitRefusalCodes() {
		if code == payload.RefusalTurnInProgress {
			continue
		}
		refusal := payload.SubmitRefusal{Code: code, Message: "x", ActiveTurnID: "turn-X"}
		if err := refusal.Validate(); err == nil {
			t.Fatalf("refusal %q was allowed to name an active turn", code)
		}
	}
}

func TestSubmitRefusal_RefusesACodeOutsideTheClosedSetAndAnEmptyMessage(t *testing.T) {
	if err := (payload.SubmitRefusal{Code: "computer_says_no", Message: "x"}).Validate(); err == nil {
		t.Fatal("a refusal code outside the closed set was accepted; a client branches on this")
	}
	if err := (payload.SubmitRefusal{Code: payload.RefusalUnavailable}).Validate(); err == nil {
		t.Fatal("a refusal with no message was accepted")
	}
}

// The result half of the protocol is ADDITIVE where the request half is strict,
// and the asymmetry is deliberate: ignoring an unrecognised result field costs a
// client nothing, while acting on a misunderstood request field costs a player a
// turn.
func TestSubmitResponse_IgnoresAFieldALaterEngineAdded(t *testing.T) {
	raw := []byte(`{"protocol":"player/v1","status":"accepted","idempotency_key":"key-1",` +
		`"action_id":"` + responseActionID + `","turn_id":"` + payload.TurnIDForAction(responseActionID) + `",` +
		`"arrived_at":"2026-07-29T12:00:00Z","resolution_card":{"band":"full"}}`)

	var response payload.SubmitResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("a response carrying a field this version does not define was refused: %v", err)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if response.ActionID != responseActionID {
		t.Fatalf("action_id = %q, want %q", response.ActionID, responseActionID)
	}
}

// A refused answer must not put a zero timestamp on the wire: a client reading
// "0001-01-01T00:00:00Z" has been handed a time that means nothing.
func TestSubmitResponse_ARefusalCarriesNoArrivalFieldOnTheWire(t *testing.T) {
	data, err := refusedResponse(payload.SubmitRefusal{
		Code: payload.RefusalUnauthenticated, Message: "authenticate first",
	}).MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if strings.Contains(string(data), "arrived_at") {
		t.Fatalf("a refusal serialized an arrival time: %s", data)
	}
	if strings.Contains(string(data), "action_id") {
		t.Fatalf("a refusal serialized an action id: %s", data)
	}
	// Anti-vacuity: an acceptance must carry both, or the assertions above would
	// pass against a marshaller that omitted them always.
	accepted, err := acceptedResponse().MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	for _, want := range []string{"arrived_at", "action_id", "turn_id"} {
		if !strings.Contains(string(accepted), want) {
			t.Fatalf("an acceptance does not serialize %q: %s", want, accepted)
		}
	}
}

// SubmitResponse never crosses NATS, so a registered factory would advertise a
// bus message nothing sends — and would invite a component to consume the
// gateway's client-facing answer as if it were engine state.
func TestSubmitResponse_HasNoRegisteredCategory(t *testing.T) {
	reg := testRegistry(t)
	for _, category := range []string{payload.CategorySubmitAction} {
		if created := reg.Create(payload.Domain, category, payload.SchemaVersion); created != nil {
			t.Fatalf("%s has a registered factory producing %T", category, created)
		}
	}
}
