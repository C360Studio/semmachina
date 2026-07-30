package playersocket

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/c360studio/semmachina/internal/gateway"
)

// ErrConnectionClosed reports a write to a socket that is no longer open.
//
// It is a normal outcome rather than a fault: a player's socket can drop between
// the moment the delivery path resolved them as a target and the moment it
// writes. The delivery path reports it, the durable retrieval surface still
// answers, and nothing is queued in adapter memory.
var ErrConnectionClosed = errors.New("the connection is closed")

// conn is one live socket, and the only thing in this package that a delivery
// can reach.
//
// It implements gateway.Writer, and it is handed to the gateway as part of the
// Connection at bind time. That is what keeps this package from holding a second
// registry: there is no map here from connection id to socket, because the socket
// travels WITH the session that names it and dies with the same Disconnect.
type conn struct {
	id string
	ws *websocket.Conn
	// writeTimeout bounds one write to this socket. It bounds the ENGINE's
	// patience with a TCP buffer, never a player's pace — see the package doc.
	writeTimeout time.Duration

	// mu serialises writes. Gorilla permits one concurrent writer, and this
	// socket has two: the connection goroutine answering submissions and the
	// egress consumer delivering results.
	mu     sync.Mutex
	closed bool
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ gateway.Writer = (*conn)(nil)

func newConn(id string, ws *websocket.Conn, writeTimeout time.Duration) *conn {
	return &conn{id: id, ws: ws, writeTimeout: writeTimeout}
}

// Write sends one document, bounded by the write deadline.
//
// A write that FAILS closes the socket, and that is deliberate rather than
// tidy-up. A gorilla connection whose write deadline expired is documented as
// corrupt — every later write on it fails — so leaving it open would leave a
// session in the delivery index that can never receive anything again, silently
// absorbing one write attempt per turn forever. Closing it ends the read loop,
// which unwinds the deferred Disconnect, which takes the socket out of the
// delivery index: a player on a wedged connection stops being a target instead of
// becoming a permanently failing one.
func (c *conn) Write(ctx context.Context, document []byte) error {
	// Checked before the lock: a delivery whose context is already done is one
	// nobody is waiting for, and taking a write deadline's worth of a wedged
	// socket for it would delay the next player's.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write to connection %s: %w", c.id, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("write to connection %s: %w", c.id, ErrConnectionClosed)
	}
	if err := c.ws.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
		return fmt.Errorf("set the write deadline on connection %s: %w", c.id, err)
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, document); err != nil {
		c.closeLocked()
		return fmt.Errorf(
			"write to connection %s (closed; a socket that cannot absorb one document in %s is not a "+
				"delivery target): %w", c.id, c.writeTimeout, err)
	}
	return nil
}

// send encodes a frame and writes it.
func (c *conn) send(ctx context.Context, frame *Frame) error {
	document, err := frame.encode()
	if err != nil {
		return err
	}
	return c.Write(ctx, document)
}

// close ends the socket. Idempotent, because several paths reach it: the
// connection goroutine's defer, the shutdown watcher, and a failed write.
func (c *conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *conn) closeLocked() {
	if c.closed {
		return
	}
	c.closed = true
	c.ws.Close() //nolint:errcheck // the socket is going away either way
}

// closeWith tells the peer WHY before closing.
//
// Best effort, and bounded: a peer that is not reading gets one write deadline's
// worth of patience and then the socket goes anyway. It exists because the two
// server-initiated closes a client can do something about — the per-player
// connection cap and a shutdown — are indistinguishable from a network fault
// without it, and a client that cannot tell them apart will reconnect in a loop
// against a cap it has already hit.
func (c *conn) closeWith(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	//nolint:errcheck // the socket closes below whether or not the peer hears this
	c.ws.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(c.writeTimeout))
	c.closeLocked()
}
