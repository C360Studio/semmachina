package playersocket_test

import (
	"encoding/json"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
)

func TestRetrieveRequest_ValidatesEachLookupWithoutAmbiguityWithSubmit(t *testing.T) {
	tests := map[string]playersocket.RetrieveRequest{
		"by turn": {
			Protocol: payload.PlayerProtocolV1,
			Type:     playersocket.RequestRetrieve,
			By:       playersocket.RetrieveByTurn,
			ID:       "turn-act-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		"by action": {
			Protocol: payload.PlayerProtocolV1,
			Type:     playersocket.RequestRetrieve,
			By:       playersocket.RetrieveByAction,
			ID:       "act-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		"latest": {
			Protocol: payload.PlayerProtocolV1,
			Type:     playersocket.RequestRetrieve,
			By:       playersocket.RetrieveLatest,
		},
	}
	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err != nil {
				t.Fatalf("valid retrieval refused: %v", err)
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("marshal retrieval: %v", err)
			}
			var shape map[string]any
			if err := json.Unmarshal(raw, &shape); err != nil {
				t.Fatalf("decode retrieval shape: %v", err)
			}
			if shape["type"] != string(playersocket.RequestRetrieve) {
				t.Fatalf("retrieval has no explicit discriminator: %s", raw)
			}
			if _, ambiguous := shape["text"]; ambiguous {
				t.Fatalf("retrieval can be mistaken for SubmitAction: %s", raw)
			}
		})
	}
}

func TestRetrieveResponse_BindsTheLookupToTheReturnedDelivery(t *testing.T) {
	turnID := turnIDFor(t, testPlayerID, "binding")
	delivery := terminalDelivery(t, testPlayerID, turnID)
	tests := map[string]*playersocket.RetrieveResponse{
		"malformed turn id": {
			Protocol: payload.PlayerProtocolV1, Status: playersocket.RetrieveRefused,
			By: playersocket.RetrieveByTurn, ID: "not-a-turn",
			Refusal: &playersocket.RetrieveRefusal{Code: playersocket.RetrieveNotFound, Message: "not found"},
		},
		"malformed action id": {
			Protocol: payload.PlayerProtocolV1, Status: playersocket.RetrieveRefused,
			By: playersocket.RetrieveByAction, ID: "not.an.action",
			Refusal: &playersocket.RetrieveRefusal{Code: playersocket.RetrieveNotFound, Message: "not found"},
		},
		"different returned turn": {
			Protocol: payload.PlayerProtocolV1, Status: playersocket.RetrieveFound,
			By: playersocket.RetrieveByTurn, ID: "turn-act-something-else", Delivery: delivery,
		},
		"different returned action": {
			Protocol: payload.PlayerProtocolV1, Status: playersocket.RetrieveFound,
			By: playersocket.RetrieveByAction, ID: "act-something-else", Delivery: delivery,
		},
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if err := response.Validate(); err == nil {
				t.Fatalf("response was accepted: %+v", response)
			}
		})
	}
}

func TestRetrieveRequest_RejectsAnIDForLatestAndRequiresOneForNamedLookups(t *testing.T) {
	tests := map[string]playersocket.RetrieveRequest{
		"latest with id": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: playersocket.RetrieveLatest, ID: "turn-not-allowed",
		},
		"turn without id": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: playersocket.RetrieveByTurn,
		},
		"action without id": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.RequestRetrieve,
			By: playersocket.RetrieveByAction,
		},
	}
	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err == nil {
				t.Fatal("invalid retrieval was accepted")
			}
		})
	}
}
