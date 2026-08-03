package content_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestFailureClassIsClosedAndLegacyDetailMayOmitIt(t *testing.T) {
	want := []content.FailureClass{
		content.FailureClassProviderModel,
		content.FailureClassModelOutputLimit,
		content.FailureClassAgentRuntime,
		content.FailureClassAgentLimit,
		content.FailureClassDeterministic,
		content.FailureClassUnknown,
	}
	if got := content.FailureClasses(); !slices.Equal(got, want) {
		t.Fatalf("FailureClasses() = %v, want %v", got, want)
	}
	for _, class := range want {
		parsed, err := content.ParseFailureClass(string(class))
		if err != nil || parsed != class {
			t.Fatalf("ParseFailureClass(%q) = %q, %v", class, parsed, err)
		}
	}
	if _, err := content.ParseFailureClass("provider said the credential was invalid"); err == nil {
		t.Fatal("ParseFailureClass accepted open diagnostic text")
	}

	legacy := &content.FailureDetail{
		TurnID: "turn-legacy", Reason: vocabulary.FailurePersonaLoopFailed, Message: "legacy detail",
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy detail without class no longer validates: %v", err)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), `"class":""`) {
		t.Fatalf("legacy class was serialized as an open empty value: %s", encoded)
	}
}

func TestFailureDetailRefusesClassOutsideClosedSet(t *testing.T) {
	detail := &content.FailureDetail{
		TurnID: "turn-bad-class", Reason: vocabulary.FailurePersonaLoopFailed,
		Class: "provider-credential-invalid", Message: "failure",
	}
	if err := detail.Validate(); err == nil {
		t.Fatal("FailureDetail.Validate accepted a class outside the closed set")
	}
}

func TestFailureDetailClassRoundTripsThroughContentStore(t *testing.T) {
	store, _ := newTestStore(t)
	detail := &content.FailureDetail{
		TurnID: "turn-class-roundtrip", Reason: vocabulary.FailureEffectInvalid,
		Class: content.FailureClassDeterministic, Message: "deterministic refusal",
	}
	ref, err := store.PutFailureDetail(t.Context(), compose(t, "turn", detail.TurnID), detail)
	if err != nil {
		t.Fatalf("PutFailureDetail: %v", err)
	}
	got, err := store.GetFailureDetail(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetFailureDetail: %v", err)
	}
	if got.Class != detail.Class {
		t.Fatalf("class = %q, want %q", got.Class, detail.Class)
	}
}

func TestFailureDetailAuthorizationReasonIsClosedAndKnowledgeOnly(t *testing.T) {
	valid := &content.FailureDetail{
		TurnID: "turn-auth", Reason: vocabulary.FailureKnowledgeUnauthorized,
		Class: content.FailureClassDeterministic, AuthorizationReason: vocabulary.AuthorizationWrongActor,
		Message: "knowledge authorization was refused",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid authorization detail: %v", err)
	}
	for _, detail := range []*content.FailureDetail{
		{TurnID: "turn-auth", Reason: vocabulary.FailureKnowledgeUnauthorized,
			Class: content.FailureClassDeterministic, Message: "fixed"},
		{TurnID: "turn-auth", Reason: vocabulary.FailureKnowledgeUnauthorized,
			Class: content.FailureClassDeterministic, AuthorizationReason: "credential-invalid", Message: "fixed"},
		{TurnID: "turn-auth", Reason: vocabulary.FailureEffectInvalid,
			Class: content.FailureClassDeterministic, AuthorizationReason: vocabulary.AuthorizationWrongActor, Message: "fixed"},
		{TurnID: "turn-auth", Reason: vocabulary.FailureKnowledgeUnauthorized,
			AuthorizationReason: vocabulary.AuthorizationWrongActor, Message: "fixed"},
		{TurnID: "turn-auth", Reason: vocabulary.FailureKnowledgeUnauthorized,
			Class: content.FailureClassUnknown, AuthorizationReason: vocabulary.AuthorizationWrongActor, Message: "fixed"},
	} {
		if err := detail.Validate(); err == nil {
			t.Fatalf("invalid authorization detail validated: %+v", detail)
		}
	}
	legacy := &content.FailureDetail{
		TurnID: "turn-auth", Reason: vocabulary.FailureKnowledgeUnauthorized, Message: "legacy fixed message",
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("classless legacy knowledge detail no longer validates: %v", err)
	}

	store, _ := newTestStore(t)
	ref, err := store.PutFailureDetail(t.Context(), compose(t, "turn", valid.TurnID), valid)
	if err != nil {
		t.Fatalf("PutFailureDetail: %v", err)
	}
	got, err := store.GetFailureDetail(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetFailureDetail: %v", err)
	}
	if got.AuthorizationReason != valid.AuthorizationReason {
		t.Fatalf("authorization reason = %q, want %q", got.AuthorizationReason, valid.AuthorizationReason)
	}
}
