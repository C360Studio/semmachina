---
name: new-payload
description: Step-by-step checklist for adding a new payload type to the registry. Use when creating new message types (verdicts, roll results, NPC decisions, chronicle refs) or any polymorphic message flow.
argument-hint: [PayloadTypeName]
---

# New Payload Type Checklist (SemMachina)

## What payload type are you adding?

$ARGUMENTS

Game payloads carry the closed exit vocabulary (M2): verdict classes, consequence classes, reaction
classes, salience levels are typed constants here, not free strings.

> **Pinned to semstreams v1.0.0-beta.158.** The package-level payload-registry singleton and the
> `init()` registration style were **retired upstream in beta.18** — see
> `~/Code/c360/semstreams/docs/operations/migration-beta18.md`. Code written against the old pattern
> does not compile. `internal/payload/registry.go` is this repo's reference implementation.

## Step 1: Define the Type

Create your message struct with JSON tags and domain constants:

```go
// internal/payload/verdict.go
const (
    Domain          = "semmachina"
    CategoryVerdict = "verdict"
    SchemaVersion   = "v1"
)

type Verdict struct {
    ActionID     string                  `json:"action_id"`
    Plausibility vocabulary.Plausibility `json:"plausibility"` // closed vocabulary
    Risk         vocabulary.Risk         `json:"risk"`         // closed vocabulary
    Consequence  vocabulary.Consequence  `json:"consequence"`  // closed vocabulary; rules match this
}
```

Snake_case is fine in JSON field names. It is **illegal in predicates** — a triple predicate is
exactly three lower-kebab segments (`turn.verdict.requires-roll`, never `requires_roll`). See design
finding F2.

## Step 2: Implement MarshalJSON

**MUST use a type alias to avoid infinite recursion. MUST NOT wrap in `BaseMessage`.**

The envelope is the publisher's job, not the payload's. `BaseMessage.MarshalJSON` calls your
payload's `MarshalJSON` and wraps the result in the wire format itself, so a payload that also wraps
produces a double envelope that the decoder cannot read.

```go
func (v *Verdict) MarshalJSON() ([]byte, error) {
    type Alias Verdict
    return json.Marshal((*Alias)(v))
}

func (v *Verdict) UnmarshalJSON(data []byte) error {
    type Alias Verdict
    return json.Unmarshal(data, (*Alias)(v))
}

func (v *Verdict) Schema() message.Type {
    return message.Type{Domain: Domain, Category: CategoryVerdict, Version: SchemaVersion}
}
```

Implement `Validate() error` too — `BaseMessage.MarshalJSON` validates before serializing, so an
invalid payload cannot be published.

## Step 3: Register explicitly

Add the type to the package's `RegisterPayloads`, which takes the registry as a parameter:

```go
// RegisterPayloads registers every payload type in this package.
// Call at bootstrap, typically alongside payloadbuiltins.Register.
func RegisterPayloads(reg *payloadregistry.Registry) error {
    return reg.Register(&payloadregistry.Registration{
        Domain:      Domain,
        Category:    CategoryVerdict,
        Version:     SchemaVersion,
        Description: "Fiction adjudicator verdict",
        Factory:     func() any { return &Verdict{} },
    })
}
```

There is no `init()` and no package-level `payloadregistry.Register`. Registration is explicit and
ordered, which is what makes the composition in step 4 verifiable.

## Step 4: Call RegisterPayloads at Every Bootstrap

Every binary that handles the payload must call it — production **and** the mock-LLM/e2e binary:

```go
reg := payloadregistry.NewRegistry()
if err := payloadbuiltins.Register(reg); err != nil { return err }
if err := payload.RegisterPayloads(reg); err != nil { return err }
```

The half-wired-binary class (registered in one `cmd/`, missing in another) shipped silently broken
flows upstream for months. A blank import no longer saves you — with explicit registration, a missed
call is a missed payload. `grep -rn "RegisterPayloads" cmd/` and confirm every binary.

## Step 5: Write a Round-Trip Test Through the Production Decoder

`BaseMessage.UnmarshalJSON` fail-fasts unless the message was built by `message.NewDecoder(reg)`, so
the decoder is the only honest round-trip:

```go
func TestVerdict_RoundTrip(t *testing.T) {
    reg := payloadregistry.NewRegistry()
    require.NoError(t, payload.RegisterPayloads(reg))

    original := &Verdict{ActionID: "act-1", Plausibility: vocabulary.PlausibilityPlausible}
    wire, err := json.Marshal(message.NewBaseMessage(original.Schema(), original, "test"))
    require.NoError(t, err)

    decoded, err := message.NewDecoder(reg).Decode(wire)
    require.NoError(t, err)

    result, ok := decoded.Payload().(*Verdict)
    require.True(t, ok, "expected *Verdict, got %T", decoded.Payload())
    assert.Equal(t, original.ActionID, result.ActionID)
}
```

Populate **every** field in the fixture and assert the whole struct. A round-trip test over a
half-empty fixture is comparing zeros to zeros and proves nothing.

## Verification Checklist

- [ ] Domain/Category/Version constants match between registration and `Schema()`
- [ ] Rule-matched fields use closed-vocabulary constants, not free strings (M2)
- [ ] `MarshalJSON` uses a type alias and does **not** wrap in `BaseMessage`
- [ ] `Validate()` rejects out-of-vocabulary values and malformed identity fields
- [ ] The type is registered in `RegisterPayloads(reg *payloadregistry.Registry) error`
- [ ] `RegisterPayloads` is called at EVERY binary's bootstrap, after `payloadbuiltins.Register`
- [ ] Round-trip test goes through `message.NewDecoder(reg)`, over a fully-populated fixture
- [ ] Any predicate the payload can produce is accepted by `vocabulary.ParsePredicate` (F2)
- [ ] Any generated schemas are regenerated with no uncommitted drift

## Common Mistakes

| Symptom | Cause | Fix |
|---------|-------|-----|
| Double-wrapped JSON; decoder can't read it | `MarshalJSON` wraps in `BaseMessage` | Alias only — the publisher owns the envelope |
| `payloadregistry.Register` undefined | Pre-beta.18 package-level singleton | Use `(*Registry).Register` via `RegisterPayloads(reg)` |
| Deserializes as `*message.GenericPayload` | Domain/Category/Version mismatch | Match constants between registration and `Schema()` |
| `UnmarshalJSON` fail-fasts | Decoding without the registry | Decode via `message.NewDecoder(reg)` |
| Payload never appears in registry | `RegisterPayloads` not called at bootstrap | Add the call in every `cmd/` that handles it |
| Stack overflow on Marshal | No type alias in `MarshalJSON` | Add `type Alias YourMessage` before the marshal call |
| Predicate rejected at the write gate | Two segments, or an underscore | Three lower-kebab segments (F2) |

Read `~/Code/c360/semstreams/docs/concepts/15-payload-registry.md` and
`~/Code/c360/semstreams/docs/operations/migration-beta18.md` for full documentation.
