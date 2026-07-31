package playersocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/c360studio/semmachina/internal/payload"
)

// FrameType discriminates the documents this adapter sends.
type FrameType string

// The closed frame set.
const (
	// FrameSubmitResponse carries the answer to one submission.
	FrameSubmitResponse FrameType = "submit_response"
	// FrameTurnDelivery carries a resolved turn's result and its prose.
	FrameTurnDelivery FrameType = "turn_delivery"
	// FrameRetrieveResponse carries the answer to an explicit result lookup.
	FrameRetrieveResponse FrameType = "retrieve_response"
	// FrameOperationResponse carries a refusal for an undispatchable typed request.
	FrameOperationResponse FrameType = "operation_response"
)

var frameTypes = []FrameType{
	FrameSubmitResponse, FrameTurnDelivery, FrameRetrieveResponse, FrameOperationResponse,
}

// FrameTypeStrings returns the player/v1 server frame types in lexical order.
func FrameTypeStrings() []string { return sortedStrings(frameTypes) }

// Frame is the envelope every server-to-client message travels in.
//
// # Why the envelope is the ADAPTER's and not the protocol's
//
// Because it exists to solve a problem only a duplex socket has. This connection
// carries two unrelated documents — the answer to a submission and the result of
// a turn that resolved minutes later — and a client reading a frame has to know
// which it is holding before it can decode it. Discriminating by shape (does it
// have `status`? does it have `result`?) is exactly the guess the player protocol
// refuses to make a client make; it would be wrong first on the path a client
// exercises least, which is the refusal path.
//
// An email or SMS adapter delivers one document per message and needs none of
// this, which is what says the multiplexing is transport-specific. Putting it in
// internal/payload would make every adapter carry a discriminator for a problem
// only this one has.
//
// The original REQUEST direction remains unframed for compatibility: a bare
// payload.SubmitAction still reaches the gateway's strict decoder unchanged.
// Retrieval is the second operation and therefore carries an explicit request
// type. This asymmetric extension keeps old player/v1 clients working while a
// new client never has to infer whether a document submits or retrieves.
//
// The embedded documents are the canonical types, marshalled by their own
// marshallers, so a delivered result stays byte-identical to the published one.
type Frame struct {
	// Protocol is the public player protocol version this frame is written
	// against. It is on the envelope so a client can decide whether it can speak
	// the document at all before decoding it.
	Protocol payload.PlayerProtocolVersion `json:"protocol"`
	// Type names which document is populated.
	Type FrameType `json:"type"`
	// Response is populated exactly when Type is FrameSubmitResponse.
	Response *payload.SubmitResponse `json:"response,omitempty"`
	// Delivery is populated exactly when Type is FrameTurnDelivery.
	Delivery *payload.TurnDelivery `json:"delivery,omitempty"`
	// Retrieval is populated exactly when Type is FrameRetrieveResponse.
	Retrieval *RetrieveResponse `json:"retrieval,omitempty"`
	// Operation is populated exactly when Type is FrameOperationResponse.
	Operation *OperationResponse `json:"operation,omitempty"`
}

// responseFrame wraps a submission's answer.
func responseFrame(response *payload.SubmitResponse) *Frame {
	return &Frame{Protocol: payload.PlayerProtocolV1, Type: FrameSubmitResponse, Response: response}
}

// deliveryFrame wraps a resolved turn's result.
func deliveryFrame(delivery *payload.TurnDelivery) *Frame {
	return &Frame{Protocol: payload.PlayerProtocolV1, Type: FrameTurnDelivery, Delivery: delivery}
}

func retrievalFrame(response *RetrieveResponse) *Frame {
	return &Frame{Protocol: payload.PlayerProtocolV1, Type: FrameRetrieveResponse, Retrieval: response}
}

func operationFrame(response *OperationResponse) *Frame {
	return &Frame{Protocol: payload.PlayerProtocolV1, Type: FrameOperationResponse, Operation: response}
}

// Validate checks the frame declares exactly the document it carries.
//
// Both directions, because a half-populated envelope is how one document quietly
// reads as the other: a frame typed as a delivery carrying a submission answer
// would have a client decode a refusal as a turn result and show a player a turn
// that never ran.
func (f *Frame) Validate() error {
	if _, err := payload.ParsePlayerProtocolVersion(string(f.Protocol)); err != nil {
		return err
	}
	if !slices.Contains(frameTypes, f.Type) {
		return fmt.Errorf("frame type %q is not one of %v", f.Type, frameTypes)
	}

	switch f.Type {
	case FrameSubmitResponse:
		if f.Response == nil {
			return errors.New("a submit_response frame carries no response")
		}
		if f.Delivery != nil {
			return errors.New("a submit_response frame also carries a turn delivery")
		}
		if f.Retrieval != nil {
			return errors.New("a submit_response frame also carries a retrieval response")
		}
		if f.Operation != nil {
			return errors.New("a submit_response frame also carries an operation response")
		}
		return f.Response.Validate()
	case FrameTurnDelivery:
		if f.Delivery == nil {
			return errors.New("a turn_delivery frame carries no delivery")
		}
		if f.Response != nil {
			return errors.New("a turn_delivery frame also carries a submit response")
		}
		if f.Retrieval != nil {
			return errors.New("a turn_delivery frame also carries a retrieval response")
		}
		if f.Operation != nil {
			return errors.New("a turn_delivery frame also carries an operation response")
		}
		return f.Delivery.Validate()
	case FrameRetrieveResponse:
		if f.Retrieval == nil {
			return errors.New("a retrieve_response frame carries no retrieval response")
		}
		if f.Response != nil || f.Delivery != nil || f.Operation != nil {
			return errors.New("a retrieve_response frame also carries an unrelated document")
		}
		return f.Retrieval.Validate()
	case FrameOperationResponse:
		if f.Operation == nil {
			return errors.New("an operation_response frame carries no operation response")
		}
		if f.Response != nil || f.Delivery != nil || f.Retrieval != nil {
			return errors.New("an operation_response frame also carries an unrelated document")
		}
		return f.Operation.Validate()
	default:
		return fmt.Errorf("frame type %q is not one of %v", f.Type, frameTypes)
	}
}

// encode marshals a frame after checking it states one outcome completely.
//
// Validated before it is written, never after: past the write it is whatever
// arrived in front of a player, and a client cannot report a document it could
// not decode.
func (f *Frame) encode() ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("refusing to send an incoherent frame: %w", err)
	}
	data, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("encode the %s frame: %w", f.Type, err)
	}
	return data, nil
}
