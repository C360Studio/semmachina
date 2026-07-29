package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type recordedFailure struct {
	turnID       string
	turnEntityID string
	reason       vocabulary.FailureReason
	detail       content.Ref
}

type fakeFailer struct {
	failures   []recordedFailure
	transition turn.Transition
	err        error
}

func (f *fakeFailer) Fail(
	_ context.Context,
	turnID, turnEntityID string,
	reason vocabulary.FailureReason,
	detail content.Ref,
) (turn.Transition, error) {
	f.failures = append(f.failures, recordedFailure{turnID, turnEntityID, reason, detail})
	return f.transition, f.err
}

type fakeDetails struct {
	stored []*content.FailureDetail
	ref    content.Ref
	err    error
}

func (f *fakeDetails) PutFailureDetail(
	_ context.Context, _ string, detail *content.FailureDetail,
) (content.Ref, error) {
	f.stored = append(f.stored, detail)
	return f.ref, f.err
}

func capDecoder(t *testing.T) *message.Decoder {
	t.Helper()
	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		t.Fatalf("register framework payloads: %v", err)
	}
	return message.NewDecoder(registry)
}

func loopFailure(t *testing.T, reason string, metadata map[string]any) []byte {
	t.Helper()
	event := &agentic.LoopFailedEvent{
		LoopID:     "loop-1",
		TaskID:     "adjudicator-" + testTurnID,
		Outcome:    agentic.OutcomeFailed,
		Reason:     reason,
		Error:      "max iterations (3) reached",
		Role:       string(persona.RoleAdjudicator),
		Iterations: persona.AdjudicatorMaxIterations,
		Metadata:   metadata,
	}
	data, err := json.Marshal(message.NewBaseMessage(event.Schema(), event, "agentic-loop"))
	if err != nil {
		t.Fatalf("encode loop failure: %v", err)
	}
	return data
}

func engineMetadata() map[string]any {
	return map[string]any{
		persona.MetadataKeyTurnID:       testTurnID,
		persona.MetadataKeyTurnEntityID: testTurnEntityID,
		persona.MetadataKeyActionID:     testActionID,
		persona.MetadataKeySceneID:      testSceneID,
	}
}

func newCapWatcher(t *testing.T, failer *fakeFailer, details *fakeDetails) *stage.CapWatcher {
	t.Helper()
	watcher, err := stage.NewCapWatcher(stubSubscriber{}, capDecoder(t), failer, details)
	if err != nil {
		t.Fatalf("NewCapWatcher: %v", err)
	}
	return watcher
}

func TestCapWatcher_EndsTheTurnWhenAPersonaExhaustsItsBudget(t *testing.T) {
	failer := &fakeFailer{transition: turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}}
	details := &fakeDetails{ref: content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/failure"}}
	watcher := newCapWatcher(t, failer, details)

	if err := watcher.Handle(t.Context(), loopFailure(t, stage.ReasonMaxIterations, engineMetadata())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(failer.failures) != 1 {
		t.Fatalf("recorded %d failures, want 1; a cap-exhausted loop must not end in silence", len(failer.failures))
	}
	failure := failer.failures[0]
	if failure.reason != vocabulary.FailurePersonaCapExhausted {
		t.Errorf("recorded reason %q, want %q", failure.reason, vocabulary.FailurePersonaCapExhausted)
	}
	if failure.turnEntityID != testTurnEntityID {
		t.Errorf("failed turn %q, want %q", failure.turnEntityID, testTurnEntityID)
	}
	if failure.detail.IsZero() {
		t.Error("the failure carries no detail reference; the explanation is the most useful sentence in the turn")
	}
	if len(details.stored) != 1 {
		t.Fatalf("stored %d explanations, want 1", len(details.stored))
	}
}

// The typed loss error says the turn IS resolved. A caller reading err != nil as
// "still needs running" would re-bill a persona that already ran out of budget.
func TestCapWatcher_ADetailStoreRefusalStillLeavesTheTurnEnded(t *testing.T) {
	failer := &fakeFailer{transition: turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}}
	details := &fakeDetails{err: errors.New("object store unreachable")}
	watcher := newCapWatcher(t, failer, details)

	if err := watcher.Handle(t.Context(), loopFailure(t, stage.ReasonMaxIterations, engineMetadata())); err != nil {
		t.Fatalf("Handle reported %v; a lost explanation is not an unresolved turn", err)
	}
	if len(failer.failures) != 1 {
		t.Fatalf("recorded %d failures, want 1", len(failer.failures))
	}
	if !failer.failures[0].detail.IsZero() {
		t.Error("the turn claims a detail reference the store never wrote")
	}
}

func TestCapWatcher_IgnoresALoopThatIsNotThisEngines(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newCapWatcher(t, failer, &fakeDetails{})

	if err := watcher.Handle(t.Context(), loopFailure(t, stage.ReasonMaxIterations, nil)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(failer.failures) != 0 {
		t.Fatalf("a loop carrying no turn identity failed %d turns", len(failer.failures))
	}
}

// Only cap exhaustion has a closed reason code. Another failure cause must not
// be laundered into one — that would put a wrong outcome on the player's record.
func TestCapWatcher_DoesNotRecordAFailureReasonItHasNoCodeFor(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newCapWatcher(t, failer, &fakeDetails{})

	if err := watcher.Handle(t.Context(), loopFailure(t, "handler_error", engineMetadata())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(failer.failures) != 0 {
		t.Fatalf("a non-cap loop failure recorded %d turn failures with a cap-exhaustion code",
			len(failer.failures))
	}
}

func TestCapWatcher_RefusesALoopClaimingARoleThisEngineDoesNotRun(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newCapWatcher(t, failer, &fakeDetails{})

	event := &agentic.LoopFailedEvent{
		LoopID: "loop-1", TaskID: "chronicler-" + testTurnID, Outcome: agentic.OutcomeFailed,
		Reason: stage.ReasonMaxIterations, Role: "chronicler", Iterations: 3, Metadata: engineMetadata(),
	}
	data, err := json.Marshal(message.NewBaseMessage(event.Schema(), event, "agentic-loop"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := watcher.Handle(t.Context(), data); err == nil {
		t.Fatal("a loop carrying this engine's identity but an unknown role was accepted")
	}
	if len(failer.failures) != 0 {
		t.Error("an unknown role still failed a turn")
	}
}

func TestCapWatcher_IgnoresAMessageThatIsNotALoopFailure(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newCapWatcher(t, failer, &fakeDetails{})

	created := &agentic.LoopCreatedEvent{
		LoopID: "loop-1", TaskID: "adjudicator-" + testTurnID, Role: string(persona.RoleAdjudicator),
	}
	data, err := json.Marshal(message.NewBaseMessage(created.Schema(), created, "agentic-loop"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := watcher.Handle(t.Context(), data); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(failer.failures) != 0 {
		t.Error("a loop-created event failed a turn")
	}
}
