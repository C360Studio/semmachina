package playersocket_test

import (
	"encoding/json"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
)

// The envelope's whole job is that a client never has to guess which document
// arrived, so what it must refuse is a frame whose type and contents disagree.

func TestFrame_RefusesAnEnvelopeThatDisagreesWithItsContents(t *testing.T) {
	delivery := terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1"))
	response := &payload.SubmitResponse{
		Protocol: payload.PlayerProtocolV1,
		Status:   payload.StatusRefused,
		Refusal: &payload.SubmitRefusal{
			Code: payload.RefusalMalformedRequest, Message: "not a submission",
		},
	}

	tests := map[string]*playersocket.Frame{
		"an unknown protocol": {
			Protocol: "player/v9", Type: playersocket.FrameSubmitResponse, Response: response,
		},
		"an unknown type": {
			Protocol: payload.PlayerProtocolV1, Type: "resolution_card", Response: response,
		},
		"no type at all": {
			Protocol: payload.PlayerProtocolV1, Response: response,
		},
		"a submit_response with no response": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameSubmitResponse,
		},
		"a turn_delivery with no delivery": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameTurnDelivery,
		},
		"a submit_response carrying a delivery": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameSubmitResponse,
			Response: response, Delivery: delivery,
		},
		"a turn_delivery carrying a response": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameTurnDelivery,
			Delivery: delivery, Response: response,
		},
		"a turn_delivery whose delivery is incoherent": {
			Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameTurnDelivery,
			Delivery: &payload.TurnDelivery{Protocol: payload.PlayerProtocolV1},
		},
	}
	for name, frame := range tests {
		t.Run(name, func(t *testing.T) {
			if err := frame.Validate(); err == nil {
				t.Fatalf("the frame was accepted: %+v", frame)
			}
		})
	}
}

func TestFrame_AcceptsEachDocumentUnderItsOwnType(t *testing.T) {
	delivery := terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1"))
	frame := &playersocket.Frame{
		Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameTurnDelivery, Delivery: delivery,
	}
	if err := frame.Validate(); err != nil {
		t.Fatalf("a well-formed delivery frame was refused: %v", err)
	}
}

// The requirement the envelope exists to protect: wrapping a result must not
// change it. A delivered result that differed from the published one would be
// two documents wearing one name.
func TestFrame_TheWrappedResultIsByteIdenticalToTheCanonicalOne(t *testing.T) {
	delivery := terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1"))
	canonical, err := json.Marshal(delivery.Result)
	if err != nil {
		t.Fatalf("marshal the canonical result: %v", err)
	}

	framed, err := json.Marshal(&playersocket.Frame{
		Protocol: payload.PlayerProtocolV1, Type: playersocket.FrameTurnDelivery, Delivery: delivery,
	})
	if err != nil {
		t.Fatalf("marshal the frame: %v", err)
	}

	var decoded struct {
		Delivery struct {
			Result json.RawMessage `json:"result"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(framed, &decoded); err != nil {
		t.Fatalf("decode the frame: %v", err)
	}
	if string(decoded.Delivery.Result) != string(canonical) {
		t.Fatalf("the framed result encodes as\n%s\nand the canonical one as\n%s",
			decoded.Delivery.Result, canonical)
	}
}
