package payload_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
)

// submissionWith builds the wire bytes of a well-formed submission plus the
// extra raw fields a test wants to smuggle in. It composes JSON by hand rather
// than marshalling a struct, because the whole subject of these tests is fields
// SubmitAction has no struct member for.
func submissionWith(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	valid := validSubmitAction()
	parts := []string{
		fmt.Sprintf("%q:%q", "protocol", valid.Protocol),
		fmt.Sprintf("%q:%q", "text", valid.Text),
		fmt.Sprintf("%q:%q", "idempotency_key", valid.IdempotencyKey),
	}
	for _, name := range slices.Sorted(maps.Keys(extra)) {
		parts = append(parts, fmt.Sprintf("%q:%s", name, extra[name]))
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

// serverOwnedFieldValues is a plausible client-supplied value for every field
// the gateway owns. Plausible matters: a refusal that only fires on garbage
// would not stop the attack, which is a client sending a WELL-FORMED action id.
func serverOwnedFieldValues() map[string]string {
	return map[string]string{
		"action_id":   `"act-9"`,
		"player_id":   `"c360.semmachina.world1.starter.player.p2"`,
		"campaign_id": `"c360.semmachina.world1.starter.campaign.main"`,
		"scene_id":    `"c360.semmachina.world1.starter.scene.gatehouse"`,
		"arrived_at":  `"2026-07-29T09:15:30Z"`,
		"channel":     `{"adapter":"websocket","reply_to":"ws-conn-1"}`,
		"adapter":     `"websocket"`,
		"reply_to":    `"ws-conn-1"`,
	}
}

func TestSubmitAction_ValidFixtureIsAccepted(t *testing.T) {
	if err := validSubmitAction().Validate(); err != nil {
		t.Fatalf("the valid fixture was rejected: %v", err)
	}

	var decoded payload.SubmitAction
	if err := json.Unmarshal(submissionWith(t, nil), &decoded); err != nil {
		t.Fatalf("a well-formed submission was refused: %v", err)
	}
	if !reflect.DeepEqual(&decoded, validSubmitAction()) {
		t.Fatalf("decoding a well-formed submission produced %#v", decoded)
	}
}

// The submission's own marshal output must survive its own strict unmarshal.
// Both sides are derived from the same struct tags, so this is what proves the
// derivation is self-consistent — a field added to SubmitAction that the allowed
// set somehow missed would fail here rather than in a caller.
func TestSubmitAction_MarshalOutputSurvivesItsOwnStrictUnmarshal(t *testing.T) {
	wire, err := json.Marshal(validSubmitAction())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded payload.SubmitAction
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("SubmitAction refused its own marshal output: %v\nwire: %s", err, wire)
	}
}

// The type carries no field for a server-owned value at all, which is the half
// of the check that a validation rule cannot express. A struct member would let
// a later author honour the field "if it is set", and Go's zero value would make
// "absent" and "explicitly zero" the same observation.
func TestSubmitAction_DeclaresNoFieldForAServerOwnedValue(t *testing.T) {
	declared := map[string]bool{}
	typ := reflect.TypeFor[payload.SubmitAction]()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		declared[name] = true
	}

	for name := range serverOwnedFieldValues() {
		if declared[name] {
			t.Errorf("SubmitAction declares a %q field; a server-owned value must have nowhere to land", name)
		}
	}
	if len(declared) != 3 {
		t.Fatalf("SubmitAction declares %d fields (%v); the client owns exactly the text, the replay key, and "+
			"the protocol version", len(declared), declared)
	}
}

// Pins the derived refusal set. It is DERIVED from PlayerAction's own tags so it
// cannot fall behind that struct; this is what makes a field added to either
// side force a decision rather than inherit one.
func TestSubmitAction_ServerOwnedFieldsAreExactlyTheCanonicalActionsIdentity(t *testing.T) {
	want := slices.Sorted(maps.Keys(serverOwnedFieldValues()))

	var got []string
	for _, name := range append(want, "text", "protocol", "idempotency_key", "not_a_field") {
		if refusedAsServerOwned(t, name) {
			got = append(got, name)
		}
	}
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("the server-owned refusal set is %v, want %v; PlayerAction's field set and this list have "+
			"drifted, and the drift is what makes a new identity field silently client-settable", got, want)
	}
}

func refusedAsServerOwned(t *testing.T, name string) bool {
	t.Helper()
	var target payload.SubmitAction
	err := json.Unmarshal([]byte(fmt.Sprintf(`{%q:null}`, name)), &target)
	return errors.Is(err, payload.ErrServerOwnedField)
}

// The spec scenario: a client-supplied identity field is refused, naming the
// field. Every server-owned field, with a plausible value.
func TestSubmitAction_RefusesEveryServerOwnedFieldByName(t *testing.T) {
	for name, value := range serverOwnedFieldValues() {
		t.Run(name, func(t *testing.T) {
			wire := submissionWith(t, map[string]string{name: value})

			var target payload.SubmitAction
			err := json.Unmarshal(wire, &target)
			if err == nil {
				t.Fatalf("a submission carrying %s=%s was accepted", name, value)
			}
			if !errors.Is(err, payload.ErrServerOwnedField) {
				t.Fatalf("the refusal is %v, which a gateway cannot classify as a server-owned field", err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("the refusal is %q and does not name the offending field", err)
			}

			// Anti-vacuity, and the reason "refused rather than ignored" is a
			// requirement at all: without the key-set gate, encoding/json accepts
			// exactly these bytes and silently drops the field.
			var bare struct {
				Protocol       string `json:"protocol"`
				Text           string `json:"text"`
				IdempotencyKey string `json:"idempotency_key"`
			}
			if err := json.Unmarshal(wire, &bare); err != nil {
				t.Fatalf("the control decode failed, so the refusal above proves nothing: %v", err)
			}
			if bare.Text != validSubmitAction().Text {
				t.Fatalf("the control decode did not produce the submission; %s was not merely ignored", name)
			}
		})
	}
}

// The zero-value hazard the key-set check exists for. Every one of these is a
// client WRITING a server-owned field, and every one of them would read as
// silence to a check that looked at the decoded value instead of the wire.
func TestSubmitAction_RefusesAServerOwnedFieldEvenWhenItsValueSaysNothing(t *testing.T) {
	for name, value := range map[string]string{
		"an empty player id":  `"player_id":""`,
		"a null action id":    `"action_id":null`,
		"the zero timestamp":  `"arrived_at":"0001-01-01T00:00:00Z"`,
		"an empty channel":    `"channel":{}`,
		"a null channel":      `"channel":null`,
		"an empty reply-to":   `"reply_to":""`,
		"a zero-ish scene id": `"scene_id":""`,
	} {
		t.Run(name, func(t *testing.T) {
			field, raw, _ := strings.Cut(value, ":")
			wire := submissionWith(t, map[string]string{strings.Trim(field, `"`): raw})

			var target payload.SubmitAction
			err := json.Unmarshal(wire, &target)
			if !errors.Is(err, payload.ErrServerOwnedField) {
				t.Fatalf("%s was accepted (err=%v); presence is the check, not the value", value, err)
			}
		})
	}
}

func TestSubmitAction_RefusesAFieldThisProtocolDoesNotDefine(t *testing.T) {
	wire := submissionWith(t, map[string]string{"tone": `"grim"`})

	var target payload.SubmitAction
	err := json.Unmarshal(wire, &target)
	if !errors.Is(err, payload.ErrUnknownField) {
		t.Fatalf("an undefined field was not refused as unknown: %v", err)
	}
	if !strings.Contains(err.Error(), "tone") {
		t.Fatalf("the refusal is %q and does not name the field", err)
	}
	if errors.Is(err, payload.ErrServerOwnedField) {
		t.Fatal("an undefined field was blamed on the gateway; the two refusals have different remedies")
	}
}

// A request with several bad fields must always report the same one. Ranging the
// key map would make the answer depend on Go's randomized iteration order, and a
// client fixing one field at a time would chase a different answer each attempt.
func TestSubmitAction_ReportsTheSameFieldEveryTime(t *testing.T) {
	wire := submissionWith(t, map[string]string{
		"tone":      `"grim"`,
		"player_id": `"c360.semmachina.world1.starter.player.p2"`,
		"scene_id":  `"c360.semmachina.world1.starter.scene.gatehouse"`,
		"zebra":     `1`,
	})

	first := ""
	for i := range 50 {
		var target payload.SubmitAction
		err := json.Unmarshal(wire, &target)
		if err == nil {
			t.Fatal("a submission carrying four illegal fields was accepted")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("attempt %d reported %q; attempt 0 reported %q", i, err, first)
		}
	}

	// A server-owned field outranks an unknown one: "stop sending that, the
	// gateway owns it" is a different remedy from "I do not know that name", and
	// the CLASSIFICATION is what carries it. Asserting only that the message
	// names player_id proves nothing — checking unknown-first would refuse
	// player_id too, as an unknown field, and say so with the same word in it.
	var target payload.SubmitAction
	err := json.Unmarshal(wire, &target)
	if !errors.Is(err, payload.ErrServerOwnedField) {
		t.Fatalf("the refusal is %v; a server-owned field must be classified as one, ahead of any unknown "+
			"field that happens to sort earlier", err)
	}
	if !strings.Contains(first, "player_id") {
		t.Fatalf("the refusal is %q and does not name the server-owned field it is about", first)
	}
}

func TestSubmitAction_RefusesAnythingThatIsNotAJSONObject(t *testing.T) {
	for name, wire := range map[string]string{
		"null":     `null`,
		"an array": `[{"protocol":"player/v1"}]`,
		"a string": `"I open the gate"`,
		"a number": `7`,
	} {
		t.Run(name, func(t *testing.T) {
			var target payload.SubmitAction
			if err := json.Unmarshal([]byte(wire), &target); err == nil {
				t.Fatalf("%s decoded as a submission", name)
			}
		})
	}
}

func TestSubmitAction_RefusesAProtocolVersionThisEngineDoesNotSpeak(t *testing.T) {
	for name, version := range map[string]payload.PlayerProtocolVersion{
		"absent":            "",
		"a future version":  "player/v2",
		"the schema's own":  "v1",
		"a plausible typo":  "player/1",
		"someone else's v1": "semdragon/v1",
	} {
		t.Run(name, func(t *testing.T) {
			submission := validSubmitAction()
			submission.Protocol = version

			err := submission.Validate()
			if !errors.Is(err, payload.ErrUnsupportedProtocol) {
				t.Fatalf("protocol %q was accepted or misclassified: %v", version, err)
			}
		})
	}
}

// The version is checked before anything else, because a client speaking an
// unknown version may have every other field wrong AS A CONSEQUENCE, and
// reporting one of those sends them chasing a symptom.
func TestSubmitAction_ReportsTheProtocolVersionAheadOfAnyFieldFault(t *testing.T) {
	submission := &payload.SubmitAction{Protocol: "player/v2", Text: "", IdempotencyKey: ""}

	err := submission.Validate()
	if !errors.Is(err, payload.ErrUnsupportedProtocol) {
		t.Fatalf("a v2 request with two empty fields reported %v, not the version", err)
	}
}

func TestSubmitAction_RefusesTextThatDeclaresNoAction(t *testing.T) {
	for name, text := range map[string]string{
		"absent":            "",
		"spaces":            "    ",
		"a newline":         "\n\n",
		"a tab and a space": "\t ",
		"unicode spaces":    "\u00a0\u2003",
	} {
		t.Run(name, func(t *testing.T) {
			submission := validSubmitAction()
			submission.Text = text
			if err := submission.Validate(); err == nil {
				t.Fatalf("a %s action was accepted and would have bought a persona call", name)
			}
		})
	}

	t.Run("over the budget", func(t *testing.T) {
		submission := validSubmitAction()
		submission.Text = strings.Repeat("a", payload.MaxActionTextBytes+1)
		if err := submission.Validate(); err == nil {
			t.Fatal("an action over the action-text budget was accepted")
		}
	})

	t.Run("exactly the budget", func(t *testing.T) {
		submission := validSubmitAction()
		submission.Text = strings.Repeat("a", payload.MaxActionTextBytes)
		if err := submission.Validate(); err != nil {
			t.Fatalf("an action exactly at the budget was rejected: %v", err)
		}
	})
}

func TestSubmitAction_RefusesAReplayKeyThatIsNotAToken(t *testing.T) {
	for name, key := range map[string]string{
		"absent":              "",
		"over the budget":     strings.Repeat("k", payload.MaxIdempotencyKeyBytes+1),
		"a newline":           "act-1\nact-2",
		"a null byte":         "act-1\x00",
		"a delete character":  "act-1\x7f",
		"invalid UTF-8 bytes": string([]byte{0xff, 0xfe, 0x41}),
	} {
		t.Run(name, func(t *testing.T) {
			submission := validSubmitAction()
			submission.IdempotencyKey = key
			if err := submission.Validate(); err == nil {
				t.Fatalf("a replay key that is %s was accepted", name)
			}
		})
	}

	t.Run("exactly the budget", func(t *testing.T) {
		submission := validSubmitAction()
		submission.IdempotencyKey = strings.Repeat("k", payload.MaxIdempotencyKeyBytes)
		if err := submission.Validate(); err != nil {
			t.Fatalf("a replay key exactly at the budget was rejected: %v", err)
		}
	})
}

// SubmitAction never crosses NATS: the gateway decodes it from client bytes and
// publishes a canonical PlayerAction. A registered factory would advertise a
// decodable shape nothing sends, and would invite a component to consume the
// UNTRUSTED shape where it should only ever see the gateway-stamped one.
func TestSubmitAction_IsDeliberatelyUnregistered(t *testing.T) {
	reg := testRegistry(t)

	schema := validSubmitAction().Schema()
	if schema.Domain != payload.Domain ||
		schema.Category != payload.CategorySubmitAction ||
		schema.Version != payload.SchemaVersion {
		t.Fatalf("the schema is %v, want the submit-action coordinates", schema)
	}
	if created := reg.Create(schema.Domain, schema.Category, schema.Version); created != nil {
		t.Fatalf("%v has a registered factory producing %T; the client's wire format is not a bus message",
			schema, created)
	}

	// Anti-vacuity: the same lookup against the registered public payload must
	// return something, or the assertion above would pass for any typo.
	if reg.Create(payload.Domain, payload.CategoryTurnResult, payload.SchemaVersion) == nil {
		t.Fatal("the registry has no factory for turn_result; the unregistered check proves nothing")
	}
}
