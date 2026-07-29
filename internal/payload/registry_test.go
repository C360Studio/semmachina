package payload_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/internal/payload"
)

// roundTripCase pairs a fully-populated fixture with the concrete type the
// decoder must reconstruct.
type roundTripCase struct {
	name    string
	payload message.Payload
	// exempt names fixture fields that are legitimately zero for this case.
	exempt []string
}

func roundTripCases() []roundTripCase {
	return []roundTripCase{
		{name: "player_action", payload: validPlayerAction()},
		{name: "verdict", payload: validVerdict()},
		{name: "roll_result", payload: validRollResult()},
		{name: "effect_batch", payload: validEffectBatch()},
		// A complete turn must not carry a failure reason; the failed-turn
		// manifest is covered separately in turnmanifest_test.go.
		{name: "turn_manifest", payload: validTurnManifest(), exempt: []string{"FailureReason"}},
		{name: "world_entity", payload: validWorldEntity()},
		// A complete turn carries no failure reason; the failed shape has its own
		// round-trip in turnresult_test.go, where its rules are.
		{name: "turn_result", payload: validTurnResult(), exempt: []string{"FailureReason"}},
	}
}

// The load-bearing test for registration: publish exactly as production does
// (BaseMessage envelope, validated), decode through message.Decoder bound to
// a registry populated by RegisterPayloads, and require every field back.
func TestPayloads_RoundTripThroughTheProductionDecoder(t *testing.T) {
	for _, tc := range roundTripCases() {
		t.Run(tc.name, func(t *testing.T) {
			assertFixtureFullyPopulated(t, tc.payload, tc.exempt...)

			data := publish(t, tc.payload)
			decoded := decode(t, data)

			if reflect.TypeOf(decoded) != reflect.TypeOf(tc.payload) {
				t.Fatalf("decoder produced %T, want %T", decoded, tc.payload)
			}
			if !reflect.DeepEqual(decoded, tc.payload) {
				t.Fatalf("round-trip lost or altered fields:\n got %#v\nwant %#v", decoded, tc.payload)
			}
			if err := decoded.Validate(); err != nil {
				t.Fatalf("decoded payload failed validation: %v", err)
			}
		})
	}
}

// The wire must carry the type discriminator, or the decoder has nothing to
// resolve against.
func TestPayloads_WireCarriesTheRegisteredTypeDiscriminator(t *testing.T) {
	for _, tc := range roundTripCases() {
		t.Run(tc.name, func(t *testing.T) {
			var envelope struct {
				Type message.Type `json:"type"`
			}
			if err := json.Unmarshal(publish(t, tc.payload), &envelope); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if envelope.Type != tc.payload.Schema() {
				t.Fatalf("wire type %v does not match Schema() %v", envelope.Type, tc.payload.Schema())
			}
			if envelope.Type.Domain != payload.Domain {
				t.Fatalf("wire domain %q, want %q", envelope.Type.Domain, payload.Domain)
			}
		})
	}
}

// Guards the round-trip test from being vacuous. If an unregistered payload
// decoded successfully anyway, the round-trip proof above would say nothing
// about registration.
func TestDecoder_WithoutOurRegistrationsRefusesOurPayloads(t *testing.T) {
	bare := message.NewDecoder(payloadregistry.New())
	for _, tc := range roundTripCases() {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bare.Decode(publish(t, tc.payload)); err == nil {
				t.Fatal("an empty registry decoded our payload; the registration proof is vacuous")
			}
		})
	}
}

func TestRegisterPayloads_RegistersEveryTypeUnderItsSchemaCoordinates(t *testing.T) {
	reg := testRegistry(t)

	for _, tc := range roundTripCases() {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.payload.Schema()
			created := reg.Create(schema.Domain, schema.Category, schema.Version)
			if created == nil {
				t.Fatalf("registry has no factory for %v", schema)
			}
			if reflect.TypeOf(created) != reflect.TypeOf(tc.payload) {
				t.Fatalf("factory for %v produced %T, want %T", schema, created, tc.payload)
			}

			fresh, ok := created.(message.Payload)
			if !ok {
				t.Fatalf("factory product %T does not implement message.Payload", created)
			}
			if fresh.Schema() != schema {
				t.Fatalf("factory product reports schema %v, registered under %v", fresh.Schema(), schema)
			}
		})
	}

	registered := reg.List()
	if len(registered) != len(roundTripCases()) {
		t.Fatalf("registry holds %d payloads, expected %d", len(registered), len(roundTripCases()))
	}
}

// The real bootstrap layers our payloads on top of the framework builtins.
// A domain/category collision with an upstream payload would only surface at
// boot, so it is proven here on the same composition the binary performs.
func TestRegisterPayloads_ComposesWithTheFrameworkBuiltins(t *testing.T) {
	reg := payloadregistry.New()
	if err := payloadbuiltins.Register(reg); err != nil {
		t.Fatalf("payloadbuiltins.Register: %v", err)
	}
	if err := payload.RegisterPayloads(reg); err != nil {
		t.Fatalf("our payloads collide with the framework builtins: %v", err)
	}

	decoder := message.NewDecoder(reg)
	for _, tc := range roundTripCases() {
		msg, err := decoder.Decode(publish(t, tc.payload))
		if err != nil {
			t.Fatalf("%s: decode against the composed registry: %v", tc.name, err)
		}
		if reflect.TypeOf(msg.Payload()) != reflect.TypeOf(tc.payload) {
			t.Fatalf("%s: composed registry produced %T, want %T", tc.name, msg.Payload(), tc.payload)
		}
	}
}

// The campaign entity's category is a provenance envelope, never a wire
// payload — the campaign entity is created through the atomic mutation lane, so
// nothing ever publishes or decodes one. Its absence from RegisterPayloads is
// therefore a decision, and this is what keeps it one: registering a payload
// under those coordinates would put a decodable type and an entity's provenance
// on the same (domain, category, version), and the collision has no other
// reporter.
func TestEntityOnlyCategories_AreDeliberatelyUnregistered(t *testing.T) {
	reg := testRegistry(t)

	// Every category here names entity state that reaches the graph through the
	// mutation lane, never through the fact lane. None is a wire type, and a
	// registered factory would advertise a message shape nothing sends.
	for _, category := range []string{
		payload.CategoryCampaignEntity,
		payload.CategoryTurnState,
		payload.CategoryTurnNarration,
		payload.CategoryTurnResume,
	} {
		if created := reg.Create(payload.Domain, category, payload.SchemaVersion); created != nil {
			t.Fatalf("%s/%s/%s has a registered factory producing %T; that category names entity state "+
				"written through the mutation lane and must not also name a decodable wire payload",
				payload.Domain, category, payload.SchemaVersion, created)
		}
	}

	// Anti-vacuity: the same lookup against a category that IS registered must
	// return something, or the assertion above would pass for any typo.
	if reg.Create(payload.Domain, payload.CategoryVerdict, payload.SchemaVersion) == nil {
		t.Fatal("the registry has no factory for a registered category; the unregistered check proves nothing")
	}
}

func TestRegisterPayloads_IsNotIdempotentAndSaysSo(t *testing.T) {
	reg := testRegistry(t)
	if err := payload.RegisterPayloads(reg); err == nil {
		t.Fatal("registering twice into one registry must report the collision, not silently overwrite")
	}
}

// BaseMessage validates before serializing, so an invalid payload can never
// reach the wire. This is the last line of defense behind the adjudicator
// tool boundary.
func TestInvalidPayloadCannotBePublished(t *testing.T) {
	verdict := validVerdict()
	verdict.Scalars.Plausibility = "likely"

	msg := message.NewBaseMessage(verdict.Schema(), verdict, "payload-test")
	if _, err := json.Marshal(msg); err == nil {
		t.Fatal("BaseMessage serialized a payload carrying an out-of-vocabulary plausibility")
	}
}

// Decoding deliberately does not validate. Every consumer must call Validate
// after decode, and these are the wire bytes that prove it matters.
func TestDecodingDoesNotValidate_ValidateIsTheGate(t *testing.T) {
	verdict := validVerdict()
	verdict.Scalars.Plausibility = "likely"

	decoded := decode(t, publishUnvalidated(t, verdict))
	if err := decoded.Validate(); err == nil {
		t.Fatal("Validate accepted an out-of-vocabulary plausibility that arrived over the wire")
	}
}
