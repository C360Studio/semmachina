package persona_test

import (
	"testing"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/persona"
)

func TestRecordLoopFailedClassifiesOnlyTheStructuredUpstreamReason(t *testing.T) {
	for _, test := range []struct {
		reason string
		want   content.FailureClass
	}{
		{reason: "model_error", want: content.FailureClassProviderModel},
		{reason: "length_truncated", want: content.FailureClassModelOutputLimit},
		{reason: "handler_error", want: content.FailureClassAgentRuntime},
		{reason: "spawn_identity_birth_failed", want: content.FailureClassAgentRuntime},
		{reason: "MODEL_ERROR", want: content.FailureClassUnknown},
		{reason: "model_error: API key abc", want: content.FailureClassUnknown},
		{reason: "new_upstream_reason", want: content.FailureClassUnknown},
	} {
		t.Run(test.reason, func(t *testing.T) {
			artifacts := newFakeArtifacts(&journal{})
			failer := &fakeFailer{}
			_, err := persona.RecordLoopFailed(t.Context(), failer, artifacts, testIdentity(), persona.LoopFailure{
				Role: persona.RoleAdjudicator, Reason: test.reason,
				LastError: "model_error handler_error length_truncated API key must not affect class",
			})
			if err != nil {
				t.Fatalf("RecordLoopFailed: %v", err)
			}
			if got := artifacts.failures[failer.detail.Key].Class; got != test.want {
				t.Fatalf("class = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRecordCapExhaustedStoresAgentLimitClass(t *testing.T) {
	artifacts := newFakeArtifacts(&journal{})
	failer := &fakeFailer{}
	_, err := persona.RecordCapExhausted(t.Context(), failer, artifacts, testIdentity(), persona.CapExhaustion{
		Role: persona.RoleAdjudicator,
	})
	if err != nil {
		t.Fatalf("RecordCapExhausted: %v", err)
	}
	if got := artifacts.failures[failer.detail.Key].Class; got != content.FailureClassAgentLimit {
		t.Fatalf("class = %q, want %q", got, content.FailureClassAgentLimit)
	}
}
