package stage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	agenticloop "github.com/c360studio/semstreams/processor/agentic-loop"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/nats-io/nats.go/jetstream"

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

// stubConsumer and stubStreams satisfy the watcher's binding surface for tests
// that drive Handle directly rather than through the broker.
type stubConsumer struct{}

func (stubConsumer) ConsumeDurable(
	_ context.Context, _ natsclient.StreamConsumerConfig, _ time.Duration, _ func(context.Context, []byte) error,
) error {
	return errors.New("this stub never binds a consumer")
}

type stubStreams struct{ err error }

func (s stubStreams) GetStream(_ context.Context, _ string) (jetstream.Stream, error) {
	if s.err != nil {
		return nil, s.err
	}
	return nil, errors.New("this stub never reads a stream")
}

func newLoopFailureWatcher(t *testing.T, failer *fakeFailer, details *fakeDetails) *stage.LoopFailureWatcher {
	t.Helper()
	watcher, err := stage.NewLoopFailureWatcher(stubConsumer{}, stubStreams{}, capDecoder(t), failer, details)
	if err != nil {
		t.Fatalf("NewLoopFailureWatcher: %v", err)
	}
	return watcher
}

func TestLoopFailureWatcher_EndsTheTurnWhenAPersonaExhaustsItsBudget(t *testing.T) {
	failer := &fakeFailer{transition: turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}}
	details := &fakeDetails{ref: content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/failure"}}
	watcher := newLoopFailureWatcher(t, failer, details)

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
	if got := details.stored[0].Class; got != content.FailureClassAgentLimit {
		t.Fatalf("stored class %q, want %q", got, content.FailureClassAgentLimit)
	}
}

// The commoner half of the same event. A model error leaves the turn in exactly
// the place cap exhaustion does, and until the vocabulary grew a code for it this
// branch logged and returned — describing a recovery replay that does not happen
// while the player waited.
func TestLoopFailureWatcher_EndsTheTurnWhenALoopFailsForAnyOtherReason(t *testing.T) {
	failer := &fakeFailer{transition: turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}}
	details := &fakeDetails{ref: content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/failure"}}
	watcher := newLoopFailureWatcher(t, failer, details)

	if err := watcher.Handle(t.Context(), loopFailure(t, "handler_error", engineMetadata())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(failer.failures) != 1 {
		t.Fatalf("recorded %d failures, want 1; a loop that fails for a non-cap reason strands the turn just as "+
			"completely as one that runs out of budget", len(failer.failures))
	}
	if got := failer.failures[0].reason; got != vocabulary.FailurePersonaLoopFailed {
		t.Errorf("recorded reason %q, want %q", got, vocabulary.FailurePersonaLoopFailed)
	}
	if len(details.stored) != 1 {
		t.Fatalf("stored %d explanations, want 1", len(details.stored))
	}
	// The loop's own reason code is UPSTREAM's vocabulary, so it belongs in the
	// stored explanation and nowhere near the graph's rule-matching surface.
	if stored := details.stored[0]; stored.Reason != vocabulary.FailurePersonaLoopFailed {
		t.Errorf("stored detail carries reason %q, want the closed loop-failed code", stored.Reason)
	}
	if got := details.stored[0].Class; got != content.FailureClassAgentRuntime {
		t.Errorf("stored class %q, want %q", got, content.FailureClassAgentRuntime)
	}
	if got := details.stored[0].Message; !strings.Contains(got, "handler_error") {
		t.Errorf("the stored explanation does not quote the loop's own reason: %q", got)
	}
}

func TestLoopFailureWatcher_DoesNotLogOpenFailureFields(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	failer := &fakeFailer{transition: turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}}
	details := &fakeDetails{ref: content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/failure"}}
	watcher := newLoopFailureWatcher(t, failer, details)
	data := loopFailure(t, "handler_error-secret-reason", engineMetadata())
	if err := watcher.Handle(t.Context(), data); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	for _, forbidden := range []string{"loop-1", "handler_error-secret-reason", "max iterations (3) reached", `"iterations"`} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("logs leaked %q: %s", forbidden, logs.String())
		}
	}
}

// The two endings must not be laundered into one another: a cap exhaustion and a
// model error are different facts about the turn, and the player's record says
// which happened.
func TestLoopFailureWatcher_KeepsTheTwoEndingsDistinct(t *testing.T) {
	failer := &fakeFailer{transition: turn.Transition{
		Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed, Outcome: turn.OutcomeAdvanced,
	}}
	details := &fakeDetails{ref: content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/failure"}}
	watcher := newLoopFailureWatcher(t, failer, details)

	for _, reason := range []string{stage.ReasonMaxIterations, "handler_error"} {
		if err := watcher.Handle(t.Context(), loopFailure(t, reason, engineMetadata())); err != nil {
			t.Fatalf("Handle(%s): %v", reason, err)
		}
	}
	if len(failer.failures) != 2 {
		t.Fatalf("recorded %d failures, want 2", len(failer.failures))
	}
	if failer.failures[0].reason == failer.failures[1].reason {
		t.Fatalf("both endings recorded %q; the turn record cannot say which one happened",
			failer.failures[0].reason)
	}
}

// The typed loss error says the turn IS resolved. A caller reading err != nil as
// "still needs running" would re-bill a persona that already ran out of budget.
func TestLoopFailureWatcher_ADetailStoreRefusalStillLeavesTheTurnEnded(t *testing.T) {
	for _, reason := range []string{stage.ReasonMaxIterations, "handler_error"} {
		t.Run(reason, func(t *testing.T) {
			failer := &fakeFailer{transition: turn.Transition{
				Previous: vocabulary.PhaseAdjudicating, Phase: vocabulary.PhaseFailed,
				Outcome: turn.OutcomeAdvanced,
			}}
			details := &fakeDetails{err: errors.New("object store unreachable")}
			watcher := newLoopFailureWatcher(t, failer, details)

			if err := watcher.Handle(t.Context(), loopFailure(t, reason, engineMetadata())); err != nil {
				t.Fatalf("Handle reported %v; a lost explanation is not an unresolved turn", err)
			}
			if len(failer.failures) != 1 {
				t.Fatalf("recorded %d failures, want 1", len(failer.failures))
			}
			if !failer.failures[0].detail.IsZero() {
				t.Error("the turn claims a detail reference the store never wrote")
			}
		})
	}
}

func TestLoopFailureWatcher_IgnoresALoopThatIsNotThisEngines(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newLoopFailureWatcher(t, failer, &fakeDetails{})

	if err := watcher.Handle(t.Context(), loopFailure(t, stage.ReasonMaxIterations, nil)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(failer.failures) != 0 {
		t.Fatalf("a loop carrying no turn identity failed %d turns", len(failer.failures))
	}
}

func TestLoopFailureWatcher_RefusesALoopClaimingARoleThisEngineDoesNotRun(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newLoopFailureWatcher(t, failer, &fakeDetails{})

	event := &agentic.LoopFailedEvent{
		LoopID: "loop-1", TaskID: "chronicler-" + testTurnID, Outcome: agentic.OutcomeFailed,
		Reason: stage.ReasonMaxIterations, Role: "chronicler", Iterations: 3, Metadata: engineMetadata(),
	}
	data, err := json.Marshal(message.NewBaseMessage(event.Schema(), event, "agentic-loop"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	err = watcher.Handle(t.Context(), data)
	if err == nil {
		t.Fatal("a loop carrying this engine's identity but an unknown role was accepted")
	}
	// The turn is real and still waiting, so the delivery must be RETRIED rather
	// than thrown away: a terminate here would drop the only message that ends
	// the turn to spare an operator a log line.
	var permanent *natsclient.PermanentDeliveryError
	if errors.As(err, &permanent) {
		t.Fatal("an unknown role terminated the delivery; the turn it names is real and still waiting")
	}
	if len(failer.failures) != 0 {
		t.Error("an unknown role still failed a turn")
	}
}

func TestLoopFailureWatcher_IgnoresAMessageThatIsNotALoopFailure(t *testing.T) {
	failer := &fakeFailer{}
	watcher := newLoopFailureWatcher(t, failer, &fakeDetails{})

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

// Bytes that are not a decodable payload can never end a turn, so redelivering
// them reproduces the same refusal forever.
func TestLoopFailureWatcher_TerminatesBytesItCanNeverDecode(t *testing.T) {
	watcher := newLoopFailureWatcher(t, &fakeFailer{}, &fakeDetails{})

	err := watcher.Handle(t.Context(), []byte("{not a message"))
	if err == nil {
		t.Fatal("undecodable bytes were acknowledged as handled")
	}
	var permanent *natsclient.PermanentDeliveryError
	if !errors.As(err, &permanent) {
		t.Fatalf("undecodable bytes were naked rather than terminated: %v; they redeliver forever", err)
	}
}

// The consumer must bind to the stream the agentic loop actually publishes on,
// which is upstream's AGENT stream and not one of ours. A separate stream over
// agent.failed.> is refused by NATS as an overlapping subject wherever an
// agentic loop has ever run.
func TestLoopFailureWatcher_BindsTheAgenticLoopsOwnStream(t *testing.T) {
	watcher := newLoopFailureWatcher(t, &fakeFailer{}, &fakeDetails{})
	cfg := watcher.ConsumerConfig()

	if cfg.StreamName != stage.TaskStream {
		t.Errorf("the loop-failure consumer binds stream %q, want the agentic loop's own %q",
			cfg.StreamName, stage.TaskStream)
	}
	if cfg.FilterSubject != stage.LoopFailedSubject {
		t.Errorf("filter subject %q, want %q", cfg.FilterSubject, stage.LoopFailedSubject)
	}
	if cfg.DeliverPolicy != "all" {
		t.Errorf("deliver policy %q; a loop that failed while the engine was down must still end its turn",
			cfg.DeliverPolicy)
	}
	if cfg.AckPolicy != "explicit" {
		t.Errorf("ack policy %q; the whole point of moving off core NATS is that this message is acknowledged",
			cfg.AckPolicy)
	}
	if cfg.MaxDeliver != 0 {
		t.Errorf("max deliver %d; a transport failure must not silently drop the message that ends a turn",
			cfg.MaxDeliver)
	}
}

// The AGENT stream belongs to the agentic loop, so this engine READS it and does
// not create it — upstream's own subscriber over the same subjects makes the same
// choice. Creating it here would be worse than unnecessary: EnsureStream is
// get-or-create with no reconcile, and upstream's creation path leaves MaxBytes
// at 0 (unlimited), so whether this deployment has a size limit would depend on
// which component booted first.
//
// Absence is an ERROR rather than upstream's no-op, and that divergence is the
// point of this test. Upstream's milestone subscriber is optional garnish on
// deployments that legitimately run no agentic component; here the personas ARE
// the loop and the spawner publishes onto this same stream, so a missing AGENT
// stream is a turn loop that cannot run. Booting quietly into that trades one
// loud error for a player who waits.
func TestLoopFailureWatcher_RefusesToBootWithoutTheStreamItDoesNotOwn(t *testing.T) {
	watcher, err := stage.NewLoopFailureWatcher(
		stubConsumer{}, stubStreams{err: jetstream.ErrStreamNotFound},
		capDecoder(t), &fakeFailer{}, &fakeDetails{})
	if err != nil {
		t.Fatalf("NewLoopFailureWatcher: %v", err)
	}

	err = watcher.Start(t.Context())
	if err == nil {
		t.Fatal("the watcher booted against a broker with no AGENT stream. Nothing would ever end a turn whose " +
			"persona loop failed, and the spawner's own tasks would have nowhere to go either")
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("the absent stream was reported as %v; the sentinel has to survive so a caller can tell a "+
			"missing stream from a broken one", err)
	}
}

// The stream is time-shaped and MUST evict, which is the opposite of the ledger.
// All three limits are stated rather than inherited.
func TestAgentStreamConfig_StatesEveryEvictionLimit(t *testing.T) {
	cfg := stage.AgentStreamConfig()

	if cfg.Name != stage.TaskStream {
		t.Errorf("stream name %q, want %q", cfg.Name, stage.TaskStream)
	}
	if !slices.Equal(cfg.Subjects, persona.AgentStreamSubjects()) {
		t.Errorf("stream subjects %v, want every subject the loop binds %v",
			cfg.Subjects, persona.AgentStreamSubjects())
	}
	if cfg.MaxAge <= 0 {
		t.Errorf("MaxAge is %v; an unbounded age on a work stream is a disk that fills instead of a horizon",
			cfg.MaxAge)
	}
	if cfg.MaxBytes <= 0 {
		t.Errorf("MaxBytes is %d; -1 and 0 both mean unlimited, which is not a stated limit", cfg.MaxBytes)
	}
	if cfg.Duplicates != stage.AgentStreamMaxAge {
		t.Errorf("Duplicates is %v, want the complete retained-task horizon %v",
			cfg.Duplicates, stage.AgentStreamMaxAge)
	}
	if cfg.Discard != jetstream.DiscardOld {
		t.Errorf("discard policy %v; refusing NEW publishes would stop the engine spawning personas at all",
			cfg.Discard)
	}
}

// The AGENT stream this engine creates must capture every subject the agentic
// components declare on it, and that is a wider set than upstream's own stream
// derivation produces.
//
// The failure it guards is SILENT rather than loud, which is what makes the
// check worth having. The NATS server ACCEPTS a consumer whose filter subject
// lies outside its stream's subjects (measured), so a narrow AGENT stream boots
// cleanly and binds everything — and then the agentic loop publishes a tool call
// onto `tool.execute.*`, which the stream does not capture, agentic-tools never
// receives it, and the persona burns its whole iteration budget waiting for a
// result that cannot arrive.
//
// It reads BOTH components' own port declarations, in BOTH directions, so
// upstream adding a lane on this stream fails here rather than at somebody's
// first live turn.
func TestAgentStreamConfig_CapturesEveryPortTheAgenticComponentsDeclare(t *testing.T) {
	declarations := map[string]*component.PortConfig{
		"agentic-loop":  agenticloop.DefaultConfig().Ports,
		"agentic-tools": agentictools.DefaultConfig().Ports,
	}
	subjects := stage.AgentStreamConfig().Subjects
	checked := 0

	for name, ports := range declarations {
		if ports == nil {
			t.Fatalf("%s declares no ports; this test can no longer see what it checks", name)
		}
		for _, group := range []struct {
			direction string
			ports     []component.PortDefinition
		}{
			{"input", ports.Inputs},
			{"output", ports.Outputs},
		} {
			for _, port := range group.ports {
				if port.StreamName != stage.TaskStream {
					continue
				}
				checked++
				if !streamCaptures(port.Subject, subjects) {
					t.Errorf("the %s stream's subjects %v do not capture %s's %s port %q (%s); a publish onto an "+
						"uncaptured subject reaches no consumer and a consumer filtered on one is accepted by the "+
						"server and never delivered anything — both silent",
						stage.TaskStream, subjects, name, group.direction, port.Name, port.Subject)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no agentic port declares stream %q; this test checked nothing", stage.TaskStream)
	}
}

// streamCaptures reports whether a stream's subject patterns capture everything
// a consumer filter can match. It is the same wildcard rule the watcher's own
// capture check uses, restated in the test binary because that one is unexported.
func streamCaptures(filter string, subjects []string) bool {
	want := strings.Split(filter, ".")
	for _, subject := range subjects {
		pattern := strings.Split(subject, ".")
		if patternCovers(pattern, want) {
			return true
		}
	}
	return false
}

func patternCovers(pattern, want []string) bool {
	for idx, token := range pattern {
		if token == ">" {
			return true
		}
		if idx >= len(want) {
			return false
		}
		if token != "*" && token != want[idx] {
			return false
		}
	}
	return len(pattern) == len(want)
}
