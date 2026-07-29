package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	testActionID     = "act-0001"
	testTurnID       = "turn-" + testActionID
	testTurnEntityID = "acme.semmachina.keep.starter.turn." + testTurnID
	testSceneID      = "acme.semmachina.keep.starter.scene.gatehouse"
)

func testTrigger() stage.Trigger {
	return stage.Trigger{TurnEntityID: testTurnEntityID, TurnID: testTurnID, Subject: "semmachina.turn.adjudicating"}
}

// journal records the ORDER of the calls a stage makes, because the ordering
// between the recorder and the guard is the property under test and no
// end-state assertion can see it.
type journal struct{ calls []string }

func (j *journal) note(call string) { j.calls = append(j.calls, call) }

type fakeRecorder struct {
	journal    *journal
	transition turn.Transition
	err        error
	target     vocabulary.TurnPhase
	calls      int
}

func (f *fakeRecorder) Advance(
	_ context.Context, _, _ string, to vocabulary.TurnPhase,
) (turn.Transition, error) {
	f.journal.note("advance:" + string(to))
	f.target = to
	f.calls++
	return f.transition, f.err
}

type fakeGuard struct {
	journal    *journal
	resumption persona.Resumption
	err        error
	calls      int
}

func (f *fakeGuard) Check(
	_ context.Context, spec persona.Spec, _, _ string,
) (persona.Resumption, error) {
	f.journal.note("guard:" + string(spec.Role))
	f.calls++
	return f.resumption, f.err
}

type fakeAssembler struct {
	journal *journal
	calls   int
}

func (f *fakeAssembler) Assemble(_ context.Context, turnID, turnEntityID string) (*scene.View, error) {
	f.journal.note("assemble")
	f.calls++
	return &scene.View{TurnID: turnID, TurnEntityID: turnEntityID, SceneID: testSceneID}, nil
}

type fakePrompter struct{ journal *journal }

func (f *fakePrompter) Adjudicate(_ context.Context, view *scene.View) (persona.TaskRequest, error) {
	f.journal.note("prompt:adjudicate")
	return persona.TaskRequest{Identity: identityFor(view), Prompt: "judge this"}, nil
}

func (f *fakePrompter) Narrate(_ context.Context, view *scene.View) (persona.TaskRequest, error) {
	f.journal.note("prompt:narrate")
	return persona.TaskRequest{
		Identity: identityFor(view), Band: vocabulary.BandPartial, Prompt: "voice this",
	}, nil
}

func identityFor(view *scene.View) persona.Identity {
	return persona.Identity{
		TurnID:       view.TurnID,
		TurnEntityID: view.TurnEntityID,
		ActionID:     testActionID,
		SceneID:      view.SceneID,
	}
}

type fakePublisher struct {
	journal  *journal
	subjects []string
	payloads [][]byte
}

func (f *fakePublisher) PublishToStream(_ context.Context, subject string, data []byte) error {
	f.journal.note("publish:" + subject)
	f.subjects = append(f.subjects, subject)
	f.payloads = append(f.payloads, data)
	return nil
}

type spawnFixture struct {
	journal   *journal
	recorder  *fakeRecorder
	guard     *fakeGuard
	assembler *fakeAssembler
	publisher *fakePublisher
	spawner   *stage.Spawner
}

func newSpawnFixture(t *testing.T, spec persona.Spec) *spawnFixture {
	t.Helper()
	j := &journal{}
	fixture := &spawnFixture{
		journal:   j,
		recorder:  &fakeRecorder{journal: j, transition: turn.Transition{Outcome: turn.OutcomeAdvanced}},
		guard:     &fakeGuard{journal: j, resumption: persona.Resumption{Decision: persona.DecisionRun}},
		assembler: &fakeAssembler{journal: j},
		publisher: &fakePublisher{journal: j},
	}
	spawner, err := stage.NewSpawner(
		spec, fixture.recorder, fixture.guard, fixture.assembler, &fakePrompter{journal: j}, fixture.publisher)
	if err != nil {
		t.Fatalf("NewSpawner: %v", err)
	}
	fixture.spawner = spawner
	return fixture
}

// The reconciliation the task asked for, stated as a test: the phase is claimed
// BEFORE the artifact guard is consulted, so the guard is only ever asked about
// the stage it governs.
func TestSpawner_ClaimsThePhaseBeforeConsultingTheResumeGuard(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Adjudicator())

	if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"advance:adjudicating", "guard:adjudicator", "assemble", "prompt:adjudicate",
		"publish:agent.task.adjudicator"}
	if strings.Join(fixture.journal.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", fixture.journal.calls, want)
	}
}

// A skip does NOT advance past the stage. It leaves the turn in the phase the
// artifact belongs to, and the sequencing rule that matches that artifact
// carries the turn onward — the same rule that would have carried it onward had
// the persona actually run.
func TestSpawner_SkipDoesNotSpendAndDoesNotAdvancePastTheStage(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Adjudicator())
	fixture.guard.resumption = persona.Resumption{
		Role:     persona.RoleAdjudicator,
		Decision: persona.DecisionSkip,
		Ref:      content.Ref{Instance: "SEMMACHINA_CONTENT", Key: "turn/" + testTurnID + "/verdict"},
	}

	if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fixture.publisher.subjects) != 0 {
		t.Errorf("a stage whose artifact already exists published %v; 7.2's saving evaporates",
			fixture.publisher.subjects)
	}
	if fixture.assembler.calls != 0 {
		t.Errorf("a skipped stage assembled a scene %d time(s)", fixture.assembler.calls)
	}
	if fixture.recorder.calls != 1 {
		t.Errorf("the recorder was called %d times; a skip must not attempt a second transition, which the "+
			"FSM would refuse as a skipped stage", fixture.recorder.calls)
	}
	if fixture.recorder.target != vocabulary.PhaseAdjudicating {
		t.Errorf("the only transition attempted was into %s, want the stage's own phase", fixture.recorder.target)
	}
}

// A resumed stage — the turn already in this phase because an earlier attempt
// was interrupted — still runs, because the phase is written on ENTRY and never
// said the stage finished. Only the artifact guard can say that.
func TestSpawner_ResumedStageStillRunsWhenNoArtifactExists(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Adjudicator())
	fixture.recorder.transition = turn.Transition{
		Previous: vocabulary.PhaseAdjudicating,
		Phase:    vocabulary.PhaseAdjudicating,
		Outcome:  turn.OutcomeResumed,
	}

	if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fixture.publisher.subjects) != 1 {
		t.Fatalf("a resumed stage with no artifact published %d tasks, want 1", len(fixture.publisher.subjects))
	}
}

func TestSpawner_DeclinedTriggerSpendsNothingAndNeverAsksTheGuard(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Narrator())
	fixture.recorder.transition = turn.Transition{
		Previous: vocabulary.PhaseComplete, Phase: vocabulary.PhaseComplete, Outcome: turn.OutcomeDeclined,
	}

	if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fixture.guard.calls != 0 {
		t.Errorf("a declined trigger consulted the guard %d time(s); the turn already moved past this stage",
			fixture.guard.calls)
	}
	if len(fixture.publisher.subjects) != 0 {
		t.Errorf("a declined trigger published %v", fixture.publisher.subjects)
	}
}

func TestSpawner_IllegalTransitionStopsTheStage(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Adjudicator())
	fixture.recorder.err = &turn.IllegalTransitionError{
		TurnEntityID: testTurnEntityID,
		From:         vocabulary.PhaseAccepted,
		To:           vocabulary.PhaseNarrating,
		Fault:        turn.FaultSkippedStage,
	}

	err := fixture.spawner.Run(t.Context(), testTrigger())
	var illegal *turn.IllegalTransitionError
	if !errors.As(err, &illegal) {
		t.Fatalf("Run error = %v, want an IllegalTransitionError so the runner can nak it loudly", err)
	}
	if fixture.guard.calls != 0 || len(fixture.publisher.subjects) != 0 {
		t.Error("an illegal transition still consulted the guard or published a task")
	}
}

// The engine tells the persona who it is about; the model is never asked.
func TestSpawner_PublishesATaskCarryingTheInjectedIdentity(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Narrator())

	if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fixture.publisher.subjects[0]; got != stage.TaskSubjectFor(persona.RoleNarrator) {
		t.Fatalf("published to %q, want %q", got, stage.TaskSubjectFor(persona.RoleNarrator))
	}

	var envelope struct {
		Payload agentic.TaskMessage `json:"payload"`
	}
	if err := json.Unmarshal(fixture.publisher.payloads[0], &envelope); err != nil {
		t.Fatalf("the published task is not a BaseMessage envelope the agentic loop can decode: %v", err)
	}
	task := envelope.Payload
	if task.Role != string(persona.RoleNarrator) {
		t.Errorf("task role = %q, want %q", task.Role, persona.RoleNarrator)
	}
	if task.MaxIterations == nil || *task.MaxIterations != persona.NarratorMaxIterations {
		t.Errorf("task carries max_iterations %v, want the narrator's budget %d",
			task.MaxIterations, persona.NarratorMaxIterations)
	}
	if len(task.Tools) != 1 {
		t.Errorf("task advertises %d tools; a persona exits through exactly one", len(task.Tools))
	}
	for key, want := range map[string]string{
		persona.MetadataKeyTurnID:       testTurnID,
		persona.MetadataKeyTurnEntityID: testTurnEntityID,
		persona.MetadataKeyActionID:     testActionID,
		persona.MetadataKeySceneID:      testSceneID,
		persona.MetadataKeyBand:         string(vocabulary.BandPartial),
	} {
		if got, _ := task.Metadata[key].(string); got != want {
			t.Errorf("task metadata %q = %q, want %q", key, got, want)
		}
	}
}

func TestNewSpawner_RefusesAPersonaWithNoStage(t *testing.T) {
	spec := persona.Adjudicator()
	spec.Role = persona.Role("chronicler")
	j := &journal{}
	_, err := stage.NewSpawner(
		spec,
		&fakeRecorder{journal: j},
		&fakeGuard{journal: j},
		&fakeAssembler{journal: j},
		&fakePrompter{journal: j},
		&fakePublisher{journal: j},
	)
	if err == nil {
		t.Fatal("a persona with no turn phase built a stage")
	}
}

func TestNewSpawner_RefusesAMissingResumeGuard(t *testing.T) {
	j := &journal{}
	_, err := stage.NewSpawner(
		persona.Adjudicator(),
		&fakeRecorder{journal: j},
		nil,
		&fakeAssembler{journal: j},
		&fakePrompter{journal: j},
		&fakePublisher{journal: j},
	)
	if err == nil {
		t.Fatal("a persona stage built without a resume guard; every redelivery would re-bill the model")
	}
}

// stubSubscriber satisfies the cap watcher's subscription surface for tests that
// drive Handle directly rather than through the broker.
type stubSubscriber struct{}

func (stubSubscriber) Subscribe(
	_ context.Context, _ string, _ func(context.Context, *nats.Msg),
) (*natsclient.Subscription, error) {
	return nil, errors.New("this stub never binds a subscription")
}
