package payload_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
)

// The field set of PlayerProtocolV1, written out. This is not a shape test for
// its own sake: the field set IS the version, and pinning it is what turns
// "adding, removing, or renaming a field is a new version" from a paragraph in a
// doc comment into something a build refuses to let slide.
//
// It covers the SHARED types too — VerdictScalars and Modifier live beside the
// engine's own payloads and are reached through this protocol — because that
// sharing is exactly where a protocol change can happen by accident: an author
// tuning a Modifier for the ledger's benefit has, without touching either public
// document, changed what every client parses.
//
// The failure detail deliberately has no field anywhere in here. A player is
// told WHICH ending happened, from the closed reason set; the engine's prose
// account of why lives behind turn.failure.ref and is operator data, not fiction.
func protocolV1Surface() map[string][]string {
	return map[string][]string{
		"DeliveredNarration": {"band", "prose", "turn_id"},
		"Frame":              {"delivery", "operation", "protocol", "response", "retrieval", "type"},
		"OperationRefusal":   {"code", "message", "operation"},
		"OperationResponse":  {"protocol", "refusal", "status"},
		"RetrieveRefusal":    {"code", "message"},
		"RetrieveRequest":    {"by", "id", "protocol", "type"},
		"RetrieveResponse":   {"by", "delivery", "id", "protocol", "refusal", "status"},
		"SubmitAction":       {"idempotency_key", "protocol", "text"},
		"SubmitRefusal":      {"active_turn_id", "code", "field", "message"},
		"SubmitResponse":     {"action_id", "arrived_at", "idempotency_key", "protocol", "refusal", "status", "turn_id"},
		"TurnDelivery":       {"narration", "protocol", "result"},
		"TurnResult": {
			"action_id", "companion_resolution", "failure_reason", "narration_ref", "phase", "player_id",
			"protocol", "resolution", "resolved_at", "turn_id",
		},
		"CompanionResolution": {"companion_id", "hint_level", "kind"},
		"TurnResolution":      {"band", "roll", "verdict"},
		"TurnRoll":            {"dice", "mechanic", "modifier_total", "modifiers", "total"},
		"VerdictScalars":      {"consequence", "plausibility", "requires_roll", "risk"},
		"Modifier":            {"note", "source", "value"},
	}
}

func TestPlayerProtocol_V1FieldSetIsPinned(t *testing.T) {
	types := map[string]reflect.Type{
		"DeliveredNarration":  reflect.TypeFor[payload.DeliveredNarration](),
		"Frame":               reflect.TypeFor[playersocket.Frame](),
		"OperationRefusal":    reflect.TypeFor[playersocket.OperationRefusal](),
		"OperationResponse":   reflect.TypeFor[playersocket.OperationResponse](),
		"RetrieveRefusal":     reflect.TypeFor[playersocket.RetrieveRefusal](),
		"RetrieveRequest":     reflect.TypeFor[playersocket.RetrieveRequest](),
		"RetrieveResponse":    reflect.TypeFor[playersocket.RetrieveResponse](),
		"SubmitAction":        reflect.TypeFor[payload.SubmitAction](),
		"SubmitRefusal":       reflect.TypeFor[payload.SubmitRefusal](),
		"SubmitResponse":      reflect.TypeFor[payload.SubmitResponse](),
		"TurnDelivery":        reflect.TypeFor[payload.TurnDelivery](),
		"TurnResult":          reflect.TypeFor[payload.TurnResult](),
		"CompanionResolution": reflect.TypeFor[payload.CompanionResolution](),
		"TurnResolution":      reflect.TypeFor[payload.TurnResolution](),
		"TurnRoll":            reflect.TypeFor[payload.TurnRoll](),
		"VerdictScalars":      reflect.TypeFor[payload.VerdictScalars](),
		"Modifier":            reflect.TypeFor[payload.Modifier](),
	}

	surface := protocolV1Surface()
	if len(types) != len(surface) {
		t.Fatalf("%d types are pinned but %d are checked", len(surface), len(types))
	}

	for name, typ := range types {
		t.Run(name, func(t *testing.T) {
			var got []string
			for i := range typ.NumField() {
				field := typ.Field(i)
				if !field.IsExported() {
					t.Fatalf("%s.%s is unexported and cannot cross a wire", name, field.Name)
				}
				jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if jsonName == "" || jsonName == "-" {
					t.Fatalf("%s.%s has no json name, so its wire spelling is the Go field name and would "+
						"change with a rename", name, field.Name)
				}
				got = append(got, jsonName)
			}
			slices.Sort(got)

			if !slices.Equal(got, surface[name]) {
				t.Fatalf("%s now speaks %v, and %s pins %v.\nThe field set IS the protocol version: adding, "+
					"removing, or renaming one is a NEW PlayerProtocolVersion, never an edit of this one.",
					name, got, "PlayerProtocolV1", surface[name])
			}
		})
	}
}

func TestPlayerProtocolV1_SocketClosedSetsArePinned(t *testing.T) {
	want := map[string]string{
		"request":           strings.Join([]string{"retrieve_result"}, ","),
		"by":                strings.Join([]string{"action", "latest", "turn"}, ","),
		"status":            strings.Join([]string{"found", "refused"}, ","),
		"refusal":           strings.Join([]string{"malformed_request", "not_found", "not_ready", "unavailable"}, ","),
		"operation status":  strings.Join([]string{"refused"}, ","),
		"operation refusal": strings.Join([]string{"malformed_operation", "unsupported_operation"}, ","),
		"frame types":       strings.Join([]string{"operation_response", "retrieve_response", "submit_response", "turn_delivery"}, ","),
	}
	got := map[string]string{
		"request":           strings.Join(playersocket.RequestTypeStrings(), ","),
		"by":                strings.Join(playersocket.RetrieveByStrings(), ","),
		"status":            strings.Join(playersocket.RetrieveStatusStrings(), ","),
		"refusal":           strings.Join(playersocket.RetrieveRefusalCodeStrings(), ","),
		"operation status":  strings.Join(playersocket.OperationStatusStrings(), ","),
		"operation refusal": strings.Join(playersocket.OperationRefusalCodeStrings(), ","),
		"frame types":       strings.Join(playersocket.FrameTypeStrings(), ","),
	}
	for set, expected := range want {
		if got[set] != expected {
			t.Errorf("%s = %q, want pinned player/v1 set %q", set, got[set], expected)
		}
	}
}

// The two versions are separate promises and must not be spelled the same. A
// bare "v1" on the public wire beside SchemaVersion's "v1" is two different
// contracts sharing a string, and the first time they diverge every log line and
// bug report about "v1" is ambiguous.
func TestPlayerProtocolVersion_IsNotTheRegistrySchemaVersion(t *testing.T) {
	if string(payload.PlayerProtocolV1) == payload.SchemaVersion {
		t.Fatal("the public protocol version and the registry schema version are the same string")
	}
	if _, err := payload.ParsePlayerProtocolVersion(payload.SchemaVersion); err == nil {
		t.Fatalf("%q was accepted as a public protocol version", payload.SchemaVersion)
	}
}

// Anti-vacuity for every version refusal in the suite: the advertised list and
// the parser must be the same set, or a refusal test could be passing because
// the parser accepts nothing at all.
func TestPlayerProtocolVersions_AreExactlyWhatTheParserAccepts(t *testing.T) {
	versions := payload.PlayerProtocolVersions()
	if len(versions) == 0 {
		t.Fatal("this engine advertises no protocol version")
	}
	for _, version := range versions {
		if _, err := payload.ParsePlayerProtocolVersion(string(version)); err != nil {
			t.Fatalf("advertised version %q is refused by the parser: %v", version, err)
		}
	}

	// The refusal names what IS spoken, so a client can act on it rather than
	// guessing. Absent and unknown are different messages for the same reason.
	for name, value := range map[string]string{"absent": "", "unknown": "player/v99"} {
		t.Run(name, func(t *testing.T) {
			_, err := payload.ParsePlayerProtocolVersion(value)
			if !errors.Is(err, payload.ErrUnsupportedProtocol) {
				t.Fatalf("%s version reported %v", name, err)
			}
			for _, version := range versions {
				if !strings.Contains(err.Error(), string(version)) {
					t.Fatalf("the refusal is %q and does not tell the client that %q is spoken", err, version)
				}
			}
		})
	}
}

// PlayerProtocolVersions hands out a copy. A caller that could sort or truncate
// the returned slice would be editing what this engine says it speaks.
func TestPlayerProtocolVersions_CannotBeEditedByACaller(t *testing.T) {
	first := payload.PlayerProtocolVersions()
	clear(first)

	if !slices.Contains(payload.PlayerProtocolVersions(), payload.PlayerProtocolV1) {
		t.Fatal("clearing the returned slice removed a version this engine speaks")
	}
}

// The three protocol refusals exist because they have three different remedies:
// upgrade the client, stop sending that field, correct the field name. Collapsing
// any two would hand a gateway one code where the client needs three.
func TestProtocolRefusals_AreDistinguishable(t *testing.T) {
	sentinels := map[string]error{
		"unsupported protocol": payload.ErrUnsupportedProtocol,
		"server-owned field":   payload.ErrServerOwnedField,
		"unknown field":        payload.ErrUnknownField,
	}
	for name, sentinel := range sentinels {
		for otherName, other := range sentinels {
			if name == otherName {
				continue
			}
			if errors.Is(sentinel, other) {
				t.Fatalf("%s and %s are the same error, so a gateway cannot answer them differently",
					name, otherName)
			}
		}
	}
	if len(slices.Collect(maps.Keys(sentinels))) != 3 {
		t.Fatal("the sentinel set changed without this test")
	}
}
