package payload_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func completedDelivery(t *testing.T) *payload.TurnDelivery {
	t.Helper()
	result := validTurnResult()
	return &payload.TurnDelivery{
		Protocol: payload.PlayerProtocolV1,
		Result:   result,
		Narration: &payload.DeliveredNarration{
			TurnID: result.TurnID,
			Band:   result.Resolution.Band,
			Prose:  "The gate groans open.",
		},
	}
}

// deadBeforeTheNarrator is the failed turn that carries NO prose: one that ended
// before the narrator ever ran. Its sibling — a turn abandoned after its prose
// landed — is validFailedTurnResult, and both have to be deliverable.
func deadBeforeTheNarrator() *payload.TurnResult {
	result := validFailedTurnResult()
	result.NarrationRef = ""
	return result
}

func TestTurnDelivery_Valid(t *testing.T) {
	if err := completedDelivery(t).Validate(); err != nil {
		t.Fatalf("a completed turn's delivery does not validate: %v", err)
	}
}

// The whole point of an envelope rather than a fatter TurnResult: the result a
// client is DELIVERED is byte-for-byte the result the engine composed and the
// archive can reconstruct. A delivered shape that differed from the published one
// would be two documents wearing one name, which is the failure TurnResult was
// made a single discriminated type to avoid.
func TestTurnDelivery_EmbedsThePublishedResultByteForByte(t *testing.T) {
	delivery := completedDelivery(t)

	published, err := json.Marshal(delivery.Result)
	if err != nil {
		t.Fatalf("marshal the result on its own: %v", err)
	}
	encoded, err := json.Marshal(delivery)
	if err != nil {
		t.Fatalf("marshal the delivery: %v", err)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode the delivery envelope: %v", err)
	}
	if !bytes.Equal(published, envelope.Result) {
		t.Fatalf("the delivered result is\n  %s\nand the published one is\n  %s\n"+
			"delivery must carry the canonical document unmodified, not a second shape with the same name",
			envelope.Result, published)
	}
}

// Prose is what a client cannot resolve for itself, so its presence is decided by
// the reference and never guessed. Both directions are failures worth naming: a
// reference with no prose is a delivery the player cannot read, and prose with no
// reference is prose from nowhere.
func TestTurnDelivery_NarrationPresenceFollowsTheReference(t *testing.T) {
	t.Run("a result with a reference and no prose is refused", func(t *testing.T) {
		delivery := completedDelivery(t)
		delivery.Narration = nil

		err := delivery.Validate()
		if err == nil {
			t.Fatal("a delivery carrying a narration reference and no prose was accepted; no client can " +
				"resolve an obj:// reference, so that is a result the player cannot read")
		}
		if !strings.Contains(err.Error(), "narration") {
			t.Errorf("the refusal %q does not name the narration", err)
		}
	})

	t.Run("prose with no reference is refused", func(t *testing.T) {
		delivery := completedDelivery(t)
		delivery.Result = deadBeforeTheNarrator()
		delivery.Narration.TurnID = delivery.Result.TurnID

		if err := delivery.Validate(); err == nil {
			t.Fatal("a delivery carried prose for a turn whose result references none; the prose came from " +
				"somewhere other than this turn's record")
		}
	})

	t.Run("a failed turn that never reached the narrator delivers no prose", func(t *testing.T) {
		delivery := &payload.TurnDelivery{
			Protocol: payload.PlayerProtocolV1,
			Result:   deadBeforeTheNarrator(),
		}
		if err := delivery.Validate(); err != nil {
			t.Fatalf("a failed turn with no narration must still be deliverable: %v", err)
		}
	})
}

// The disclosure check. A narration reference resolving to another turn's prose
// would put somebody else's fiction in front of this player — the same defect a
// broadcast adapter produces, arriving through the dereference instead of the
// socket.
func TestTurnDelivery_RefusesProseFromAnotherTurn(t *testing.T) {
	delivery := completedDelivery(t)
	delivery.Narration.TurnID = "turn-SOMEONEELSE"

	err := delivery.Validate()
	if err == nil {
		t.Fatal("a delivery carried another turn's prose; that is one player reading another's fiction")
	}
	if !strings.Contains(err.Error(), "turn-SOMEONEELSE") || !strings.Contains(err.Error(), delivery.Result.TurnID) {
		t.Errorf("the refusal %q names neither turn, so an operator cannot tell which prose went where", err)
	}
}

// A narration voicing a band the turn did not land on is a story about a turn
// that did not happen. The archive detects it on replay; this is the surface a
// human would actually read it on.
func TestTurnDelivery_RefusesProseVoicingAnotherBand(t *testing.T) {
	delivery := completedDelivery(t)
	if delivery.Result.Resolution.Band == vocabulary.BandMiss {
		t.Fatalf("fixture drift: the completed result already bands %q", delivery.Result.Resolution.Band)
	}
	delivery.Narration.Band = vocabulary.BandMiss

	err := delivery.Validate()
	if err == nil {
		t.Fatal("a delivery carried prose voicing a band the turn did not land on")
	}
	if !strings.Contains(err.Error(), string(vocabulary.BandMiss)) {
		t.Errorf("the refusal %q does not name the band the prose voiced", err)
	}
}

func TestTurnDelivery_RefusesProseOutsideTheBudget(t *testing.T) {
	delivery := completedDelivery(t)
	delivery.Narration.Prose = strings.Repeat("a", payload.MaxProseBytes+1)

	if err := delivery.Validate(); err == nil {
		t.Fatal("prose past the budget was accepted; the same bound governs the stored artifact, so a " +
			"deliverable that exceeded it would be prose that stored and could never be sent")
	}
}

func TestTurnDelivery_RefusesAnEmptyDelivery(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*payload.TurnDelivery)
		wantSub string
	}{
		{
			name:    "no protocol version",
			mutate:  func(d *payload.TurnDelivery) { d.Protocol = "" },
			wantSub: "protocol",
		},
		{
			name:    "no result",
			mutate:  func(d *payload.TurnDelivery) { d.Result = nil },
			wantSub: "result",
		},
		{
			name: "the result does not satisfy its own contract",
			mutate: func(d *payload.TurnDelivery) {
				d.Result.Phase = vocabulary.PhaseNarrating
			},
			wantSub: "terminal",
		},
		{
			// Not a version-AGREEMENT check — that one is unreachable while a
			// single version exists, and TurnDelivery.Validate records why it is
			// deliberately absent. This is the embedded result being held to its
			// own contract, which is what actually refuses the value.
			name: "the embedded result declares a version this engine does not speak",
			mutate: func(d *payload.TurnDelivery) {
				d.Result.Protocol = "player/v99"
			},
			wantSub: "player/v99",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := completedDelivery(t)
			test.mutate(delivery)

			err := delivery.Validate()
			if err == nil {
				t.Fatal("Validate accepted it")
			}
			if !strings.Contains(err.Error(), test.wantSub) {
				t.Errorf("Validate said %q, which does not name %q", err, test.wantSub)
			}
		})
	}
}

func TestTurnDelivery_SurvivesARoundTrip(t *testing.T) {
	delivery := completedDelivery(t)
	data, err := delivery.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var back payload.TurnDelivery
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("a round-tripped delivery no longer validates: %v", err)
	}
	if back.Result.TurnID != delivery.Result.TurnID || back.Narration.Prose != delivery.Narration.Prose {
		t.Errorf("round trip produced %+v, want the delivered turn and its prose", back)
	}
}
