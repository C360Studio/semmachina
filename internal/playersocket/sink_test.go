package playersocket_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// recordingWriter is a gateway.Writer that keeps what it was handed.
type recordingWriter struct {
	mu        sync.Mutex
	documents [][]byte
	err       error
}

func (w *recordingWriter) Write(_ context.Context, document []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.documents = append(w.documents, append([]byte(nil), document...))
	return nil
}

func (w *recordingWriter) written() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.documents
}

func websocketSession(writer gateway.Writer) gateway.Session {
	return gateway.Session{
		PlayerID: testPlayerID,
		Connection: gateway.Connection{
			ID: "ws-test-1", Adapter: vocabulary.AdapterWebSocket, ReplyTo: "ws-test-1", Writer: writer,
		},
	}
}

func TestSink_WritesOneFramedDeliveryToTheSessionsOwnWriter(t *testing.T) {
	writer := &recordingWriter{}
	turnID := turnIDFor(t, testPlayerID, "key-1")

	if err := playersocket.NewSink().Deliver(t.Context(), websocketSession(writer),
		terminalDelivery(t, testPlayerID, turnID)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	written := writer.written()
	if len(written) != 1 {
		t.Fatalf("the sink wrote %d documents", len(written))
	}
	var frame playersocket.Frame
	if err := json.Unmarshal(written[0], &frame); err != nil {
		t.Fatalf("the sink wrote %q, which is not a frame: %v", written[0], err)
	}
	if err := frame.Validate(); err != nil {
		t.Fatalf("the sink wrote a frame that fails its own contract: %v", err)
	}
	if frame.Type != playersocket.FrameTurnDelivery || frame.Delivery.Result.TurnID != turnID {
		t.Fatalf("the sink wrote %+v", frame)
	}
}

// Every refusal is an ERROR, because the router counts acceptances: a sink that
// returned nil for something it did not send would report a delivery the player
// never received.
func TestSink_RefusesWhatItCannotSendRatherThanReportingSuccess(t *testing.T) {
	sink := playersocket.NewSink()
	delivery := terminalDelivery(t, testPlayerID, turnIDFor(t, testPlayerID, "key-1"))

	t.Run("no document", func(t *testing.T) {
		if err := sink.Deliver(t.Context(), websocketSession(&recordingWriter{}), nil); err == nil {
			t.Fatal("the sink accepted a nil delivery")
		}
	})

	t.Run("another adapter's session", func(t *testing.T) {
		session := websocketSession(&recordingWriter{})
		session.Connection.Adapter = vocabulary.ChannelAdapter("email")
		if err := sink.Deliver(t.Context(), session, delivery); err == nil {
			t.Fatal("the WebSocket sink delivered another adapter's session; each adapter delivers its own, " +
				"and an email session written down a socket is a delivery to somebody else's transport")
		}
	})

	t.Run("no writer", func(t *testing.T) {
		if err := sink.Deliver(t.Context(), websocketSession(nil), delivery); err == nil {
			t.Fatal("the sink accepted a session with no live writer")
		}
	})

	t.Run("a writer that failed", func(t *testing.T) {
		writer := &recordingWriter{err: errors.New("write: broken pipe")}
		err := sink.Deliver(t.Context(), websocketSession(writer), delivery)
		if err == nil {
			t.Fatal("a failed write was reported as a delivery")
		}
		if !strings.Contains(err.Error(), "broken pipe") {
			t.Fatalf("the failure %q does not carry the transport's own cause", err)
		}
	})
}

// The prose travels WITH the result, resolved by the server, because no client
// can dereference an obj:// and no adapter may assume a second round trip.
func TestSink_CarriesTheProseTheResultOnlyReferences(t *testing.T) {
	writer := &recordingWriter{}
	turnID := turnIDFor(t, testPlayerID, "key-1")
	delivery := proseDelivery(t, testPlayerID, turnID)

	if err := playersocket.NewSink().Deliver(t.Context(), websocketSession(writer), delivery); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	var frame playersocket.Frame
	if err := json.Unmarshal(writer.written()[0], &frame); err != nil {
		t.Fatalf("decode the frame: %v", err)
	}
	if frame.Delivery.Narration == nil || frame.Delivery.Narration.Prose != delivery.Narration.Prose {
		t.Fatal("the delivered document does not carry the prose its result references")
	}
	if frame.Delivery.Result.NarrationRef != delivery.Result.NarrationRef {
		t.Fatalf("the delivered result's reference is %q, want the published %q",
			frame.Delivery.Result.NarrationRef, delivery.Result.NarrationRef)
	}
}

// A compile-time statement that the sink cannot reach anybody it was not handed:
// it holds no directory, no socket map, and no way to name a connection.
func TestSink_HoldsNoRegistryOfItsOwn(t *testing.T) {
	var sink any = playersocket.NewSink()
	if _, enumerable := sink.(interface{ Connections() []string }); enumerable {
		t.Fatal("the sink can enumerate connections; targeting must stay structural")
	}
	if _, addressable := sink.(interface {
		Deliver(context.Context, string, *payload.TurnDelivery) error
	}); addressable {
		t.Fatal("the sink can deliver to a connection id; a second registry keyed on connection is exactly " +
			"the drift the gateway's one table exists to prevent")
	}
}
