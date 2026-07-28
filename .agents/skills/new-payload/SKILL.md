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

## Step 1: Define the Type

Create your message struct with JSON tags and domain constants:

```go
// verdict/types.go
const (
    Domain          = "semmachina"
    CategoryVerdict = "verdict"
    Version         = "v1"
)

type Verdict struct {
    ActionID         string `json:"action_id"`
    Plausibility     string `json:"plausibility"`      // closed vocabulary
    Risk             string `json:"risk"`              // closed vocabulary
    ConsequenceClass string `json:"consequence_class"` // closed vocabulary; rules match this
}
```

## Step 2: Implement MarshalJSON

**MUST wrap in BaseMessage. MUST use type alias to avoid infinite recursion.**

```go
func (m *Verdict) MarshalJSON() ([]byte, error) {
    type Alias Verdict
    return json.Marshal(&message.BaseMessage{
        Type: message.MessageType{
            Domain:   Domain,
            Category: CategoryVerdict,
            Version:  Version,
        },
        Payload: (*Alias)(m),
    })
}
```

## Step 3: Register in init()

Create a `payload_registry.go` file in your package:

```go
package verdict

import "github.com/c360studio/semstreams/payloadregistry"

func init() {
    err := payloadregistry.Register(&payloadregistry.Registration{
        Domain:      Domain,
        Category:    CategoryVerdict,
        Version:     Version,
        Description: "Fiction adjudicator verdict",
        Factory:     func() any { return &Verdict{} },
    })
    if err != nil {
        panic("failed to register Verdict: " + err.Error())
    }
}
```

## Step 4: Import the Package in EVERY Binary

Ensure the package is imported so `init()` runs — in the production binary AND the mock-LLM/e2e binary:

```go
import _ "github.com/c360studio/semmachina/verdict"
```

The half-wired-binary class (registered in one `cmd/`, missing in another) shipped silently broken flows
upstream for months. `grep -rn "verdict" cmd/` and confirm every relevant binary.

## Step 5: Write Round-Trip Test

```go
func TestVerdict_RoundTrip(t *testing.T) {
    original := &Verdict{ActionID: "act-1", ConsequenceClass: "roll_required"}

    data, err := json.Marshal(original)
    require.NoError(t, err)

    require.Contains(t, string(data), `"domain":"semmachina"`)

    var base message.BaseMessage
    err = json.Unmarshal(data, &base)
    require.NoError(t, err)

    result, ok := base.Payload.(*Verdict)
    require.True(t, ok, "expected *Verdict, got %T", base.Payload)
    assert.Equal(t, original.ActionID, result.ActionID)
}
```

Round-trip through the production decoder, not an anonymous shape cast.

## Verification Checklist

- [ ] Domain/Category/Version constants match between registration and MarshalJSON
- [ ] Rule-matched fields use closed-vocabulary constants, not free strings (M2)
- [ ] MarshalJSON uses type alias (`type Alias YourMessage`) to prevent recursion
- [ ] `payload_registry.go` exists with `init()` function
- [ ] Package is imported (blank import if needed) in EVERY binary that handles it
- [ ] Round-trip test passes through the production decoder
- [ ] Any generated schemas are regenerated with no uncommitted drift

## Common Mistakes

| Symptom | Cause | Fix |
|---------|-------|-----|
| JSON missing `"type"` wrapper | Missing MarshalJSON | Implement MarshalJSON wrapping in BaseMessage |
| Deserializes as `*message.GenericPayload` | Domain/Category/Version mismatch | Match constants between registration and MarshalJSON |
| Payload never appears in registry | Package not imported | Add blank import in the entry point(s) |
| Stack overflow on Marshal | No type alias in MarshalJSON | Add `type Alias YourMessage` before marshal call |
| Works in prod binary, dead in e2e (or vice versa) | Import missing in one binary | Blank import in every `cmd/` that handles it |

Read `~/Code/c360/semstreams/docs/concepts/15-payload-registry.md` for full documentation.
