package playersocket

import (
	"context"
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Sink is the WebSocket half of targeted delivery: one document, one session,
// one socket.
//
// It is STATELESS, and that is the design rather than a simplification. It holds
// no map from player to socket and no map from connection id to socket, because
// the socket it writes to arrives ON the session the router hands it — the
// gateway's table is the only registry, so there is no second one to drift from
// it. A session that was disconnected is not reachable from here at all: the
// router could not have resolved it, and this sink has no other way to name a
// connection.
//
// It decides nothing about WHO. That is the router's, resolved from the player
// entity id at delivery time; this sink's whole contract is what "sent" means on
// this transport, which is "one JSON text frame accepted by the socket's write
// buffer within the write deadline".
type Sink struct{}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ egress.Sink = (*Sink)(nil)

// NewSink builds the WebSocket delivery sink.
func NewSink() *Sink { return &Sink{} }

// Deliver writes one turn's result to one connection.
//
// Every refusal here is an ERROR rather than a silent no-op, because the caller
// counts acceptances: a sink that returned nil for a session it could not write
// to would report a delivery that never happened, and the player would be shown
// nothing while the operator's log said "delivered".
func (s *Sink) Deliver(
	ctx context.Context,
	session gateway.Session,
	delivery *payload.TurnDelivery,
) error {
	if delivery == nil {
		return errors.New("the player socket was asked to deliver nothing")
	}
	if session.Connection.Adapter != vocabulary.AdapterWebSocket {
		// One sink per adapter, and a session belongs to exactly one. Writing an
		// email session's result down a WebSocket is not a degraded delivery; it
		// is a delivery to somebody else's transport.
		return fmt.Errorf(
			"the player socket was handed a %q session (connection %s); each adapter delivers its own",
			session.Connection.Adapter, session.Connection.ID)
	}
	writer := session.Connection.Writer
	if writer == nil {
		return fmt.Errorf(
			"the session on connection %s has no live writer; a WebSocket session is bound WITH its socket, "+
				"so one without is a session this process did not create", session.Connection.ID)
	}

	document, err := deliveryFrame(delivery).encode()
	if err != nil {
		return err
	}
	return writer.Write(ctx, document)
}
