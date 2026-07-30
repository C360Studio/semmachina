package playersocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/c360studio/semmachina/internal/payload"
)

// FrameType discriminates the two documents this adapter sends.
type FrameType string

// The closed frame set.
const (
	// FrameSubmitResponse carries the answer to one submission.
	FrameSubmitResponse FrameType = "submit_response"
	// FrameTurnDelivery carries a resolved turn's result and its prose.
	FrameTurnDelivery FrameType = "turn_delivery"
)

var frameTypes = []FrameType{FrameSubmitResponse, FrameTurnDelivery}

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
// The REQUEST direction is deliberately unframed: it carries exactly one document
// (payload.SubmitAction), so a discriminator would discriminate nothing, and the
// gateway's strict decoding — unknown fields refused, server-owned fields refused
// by name — applies directly to the client's own bytes rather than to something
// this package unwrapped first. The day a second request document exists, it is
// the protocol version that grows a type field; a v1 engine refuses a v2 request
// on the version before it ever reaches the unknown field.
//
// The embedded documents are the canonical types, marshalled by their own
// marshallers, so a delivered result stays byte-identical to the published one.
type Frame struct {
	// Protocol is the public player protocol version this frame is written
	// against. It is on the envelope so a client can decide whether it can speak
	// the document at all before decoding it.
	Protocol payload.PlayerProtocolVersion `json:"protocol"`
	// Type names which of the two documents is populated.
	Type FrameType `json:"type"`
	// Response is populated exactly when Type is FrameSubmitResponse.
	Response *payload.SubmitResponse `json:"response,omitempty"`
	// Delivery is populated exactly when Type is FrameTurnDelivery.
	Delivery *payload.TurnDelivery `json:"delivery,omitempty"`
}

// responseFrame wraps a submission's answer.
func responseFrame(response *payload.SubmitResponse) *Frame {
	return &Frame{Protocol: payload.PlayerProtocolV1, Type: FrameSubmitResponse, Response: response}
}

// deliveryFrame wraps a resolved turn's result.
func deliveryFrame(delivery *payload.TurnDelivery) *Frame {
	return &Frame{Protocol: payload.PlayerProtocolV1, Type: FrameTurnDelivery, Delivery: delivery}
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
		return f.Response.Validate()
	case FrameTurnDelivery:
		if f.Delivery == nil {
			return errors.New("a turn_delivery frame carries no delivery")
		}
		if f.Response != nil {
			return errors.New("a turn_delivery frame also carries a submit response")
		}
		return f.Delivery.Validate()
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
