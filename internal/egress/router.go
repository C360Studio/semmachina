package egress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
)

// Directory resolves a player's live delivery targets.
//
// ONE method, taking a player entity id. That is the whole targeting design: this
// package is handed no way to enumerate every session, so it cannot broadcast even
// by accident. Upstream's output/websocket iterates its entire client map, which
// is the shape this interface exists to make unavailable — and the failure it
// produces is one player reading another's fiction, which no amount of care at the
// call site would catch in review.
type Directory interface {
	// SessionsFor returns the sessions currently bound to a player, or none.
	SessionsFor(playerID string) []gateway.Session
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Directory = (*gateway.Gateway)(nil)

// Sink is the transport half of delivery: one adapter writing one document to
// one connection.
//
// It takes the TYPED document rather than bytes because rendering is per-adapter
// and the shape is not: a WebSocket adapter marshals the delivery as JSON, an
// email adapter renders its prose into a body, and neither may decide WHICH
// player the document belongs to. What this package guarantees is the pairing —
// this document, that session — and what an adapter decides is what "sent" means
// on its transport.
//
// A sink reaches its transport through the SESSION it is handed and never
// through a table of its own: a live connection carries its writer
// (gateway.Connection.Writer), and an adapter with a durable address instead uses
// ChannelBinding.ReplyTo. A sink that kept its own map from connection id to
// socket would be a second registry that can drift from the session table, and a
// connection present in one and absent from the other is either a socket that
// receives a player's fiction without being able to submit as them, or a
// delivery reported as sent to nowhere.
type Sink interface {
	Deliver(ctx context.Context, session gateway.Session, delivery *payload.TurnDelivery) error
}

// Router delivers one turn's result to the player it belongs to.
type Router struct {
	directory Directory
	sink      Sink
	logger    *slog.Logger
}

// RouterOption configures a Router.
type RouterOption func(*Router)

// WithRouterLogger sets the router's logger.
func WithRouterLogger(logger *slog.Logger) RouterOption {
	return func(r *Router) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// NewRouter builds the targeted delivery path.
func NewRouter(directory Directory, sink Sink, opts ...RouterOption) (*Router, error) {
	switch {
	case directory == nil:
		return nil, errors.New(
			"targeted delivery requires a session directory; without one there is no way to resolve WHICH " +
				"player a result belongs to, and the only remaining option is to send it to everybody")
	case sink == nil:
		return nil, errors.New("targeted delivery requires a sink")
	}
	router := &Router{directory: directory, sink: sink, logger: slog.Default()}
	for _, opt := range opts {
		opt(router)
	}
	return router, nil
}

// Outcome is what one delivery attempt did.
//
// Named apart from payload.TurnDelivery deliberately: that is the DOCUMENT and
// this is what happened to it.
type Outcome struct {
	// PlayerID is the player the result was addressed to.
	PlayerID string
	// Recipients is how many sinks ACCEPTED the document.
	//
	// Accepted, deliberately, and not "reached". What a sink returning nil means
	// is the adapter's own contract: on a WebSocket it is a write to a buffer on a
	// socket the transport may already have abandoned, and this package has no way
	// to know that and no business asserting it. Counting acceptances is a fact
	// about this call; counting arrivals would be a claim about somebody else's
	// transport.
	//
	// Zero is a normal outcome and not a failure. It is reported rather than
	// dropped so an operator can see that a turn resolved for a player who was
	// away — which at email cadence is most of them.
	Recipients int
}

// Deliver sends one result to exactly the player it belongs to.
//
// # The lookup is at DELIVERY time
//
// The player id comes off the result and the connections come from the directory
// NOW, not from a channel binding captured when the action was submitted. That is
// why TurnResult carries no binding at all: an address recorded at submission is
// stale the moment the socket drops, and dialling it would deliver a player's
// turn into a closed connection while they sit reconnected watching nothing.
//
// # Nobody connected is not an error
//
// It acknowledges. The durable answer is retrieval — Results reads the same turn
// from the graph whenever the player comes back — and holding the delivery to
// re-push later would be adapter memory wearing a queue's clothes, which is
// exactly what "retrieval from durable state, never adapter memory" forbids.
// Nothing here measures how long the player has been away, because nothing here
// may.
//
// # One connection's failure does not silence the others
//
// Every session is attempted and the failures are joined. A player on two sockets
// with one broken must still be told on the other; abandoning the fan-out at the
// first error would make one wedged connection into a delivery outage for that
// player.
func (r *Router) Deliver(ctx context.Context, delivery *payload.TurnDelivery) (Outcome, error) {
	if delivery == nil {
		return Outcome{}, errors.New("delivery requires a document")
	}
	// Validated before anything is sent, not after. This is the last point at
	// which an incoherent document — prose from another turn, a result that is
	// not terminal — can be stopped, and everything past it is a player reading
	// whatever arrived.
	if err := delivery.Validate(); err != nil {
		return Outcome{}, fmt.Errorf("refusing to deliver an incoherent result: %w", err)
	}

	playerID := delivery.Result.PlayerID
	sessions := r.directory.SessionsFor(playerID)
	if len(sessions) == 0 {
		r.logger.Info("a turn resolved for a player with no live connection; the result stays retrievable",
			"player", playerID, "turn", delivery.Result.TurnID)
		return Outcome{PlayerID: playerID}, nil
	}

	sent := 0
	var errs []error
	for _, session := range sessions {
		if err := r.sink.Deliver(ctx, session, delivery); err != nil {
			errs = append(errs, fmt.Errorf(
				"deliver turn %s to player %s on connection %s: %w",
				delivery.Result.TurnID, playerID, session.Connection.ID, err))
			continue
		}
		sent++
	}
	return Outcome{PlayerID: playerID, Recipients: sent}, errors.Join(errs...)
}
