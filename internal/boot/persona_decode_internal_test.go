package boot

import (
	"testing"

	"github.com/c360studio/semmachina/internal/world"
)

func TestDecodePersona_UsesWorldPersonaValidationGate(t *testing.T) {
	data := []byte(`{"id":"stub/voice","category":100,"content":"Speak everywhere."}`)

	_, bootErr := decodePersona(data)
	_, worldErr := world.DecodePersonaRecord(data)
	if bootErr == nil || worldErr == nil {
		t.Fatalf("empty roles accepted: boot error=%v world error=%v", bootErr, worldErr)
	}
	if bootErr.Error() != worldErr.Error() {
		t.Fatalf("boot and world do not share the persona gate:\n boot: %q\nworld: %q", bootErr, worldErr)
	}
}
