package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/testinfra"
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

type fakeProjector struct {
	journal    *journal
	calls      int
	projection *epistemic.Projection
	audiences  []epistemic.Purpose
	err        error
}

func (f *fakeProjector) Project(
	_ context.Context,
	audience epistemic.AuthenticatedAudience,
) (*epistemic.Projection, error) {
	f.journal.note("project:" + string(audience.Purpose()))
	f.calls++
	f.audiences = append(f.audiences, audience.Purpose())
	if f.err != nil {
		return nil, f.err
	}
	if f.projection != nil {
		return f.projection, nil
	}
	turnID, turnEntityID := audience.TurnIdentity()
	return &epistemic.Projection{
		Purpose: audience.Purpose(), TurnID: turnID, TurnEntityID: turnEntityID, SceneID: testSceneID,
	}, nil
}

type fakePrompter struct {
	journal  *journal
	actionID string
}

func (f *fakePrompter) identity(view *epistemic.Projection) persona.Identity {
	identity := identityFor(view)
	if f.actionID != "" {
		identity.ActionID = f.actionID
	}
	return identity
}

func (f *fakePrompter) Adjudicate(
	_ context.Context, view *epistemic.Projection,
) (persona.TaskRequest, error) {
	f.journal.note("prompt:adjudicate")
	attempt, err := payload.ResumeAttemptsFromTriples(projectedTriples(view.Turn))
	return persona.TaskRequest{
		Identity: f.identity(view), ResumeAttempt: attempt, Prompt: "judge this",
	}, err
}

func (f *fakePrompter) Narrate(
	_ context.Context, view *epistemic.Projection,
) (persona.TaskRequest, error) {
	f.journal.note("prompt:narrate")
	attempt, err := payload.ResumeAttemptsFromTriples(projectedTriples(view.Turn))
	return persona.TaskRequest{
		Identity: f.identity(view), ResumeAttempt: attempt,
		Band: vocabulary.BandPartial, Prompt: "voice this",
	}, err
}

func (f *fakePrompter) Interpret(
	_ context.Context, view *epistemic.Projection,
) (persona.TaskRequest, error) {
	f.journal.note("prompt:interpret")
	identity := f.identity(view)
	identity.CaseID = "acme.semmachina.keep.starter.case.bellweather"
	identity.ActorID = "acme.semmachina.keep.starter.character.rook"
	return persona.TaskRequest{Identity: identity, Prompt: "interpret this"}, nil
}

type fakeDecisionStore struct {
	journal *journal
	records []*payload.CaseDecisionRecord
}

func (s *fakeDecisionStore) PutCaseDecisionRecord(
	_ context.Context, _ string, record *payload.CaseDecisionRecord,
) (content.Ref, error) {
	s.journal.note("put:case-decision")
	s.records = append(s.records, record)
	return content.Ref{Instance: "TEST_CONTENT", Key: "turn/" + record.TurnID + "/case-decision"}, nil
}

type fakeDecisionWriter struct {
	journal *journal
	triples []message.Triple
}

func (w *fakeDecisionWriter) MergeTriples(
	_ context.Context, _ string, triples []message.Triple, _ ...graphio.MergeOption,
) (*graph.EntityState, error) {
	w.journal.note("merge:case-decision")
	w.triples = append([]message.Triple(nil), triples...)
	return &graph.EntityState{}, nil
}

func identityFor(view *epistemic.Projection) persona.Identity {
	return persona.Identity{
		TurnID:       view.TurnID,
		TurnEntityID: view.TurnEntityID,
		ActionID:     testActionID,
		SceneID:      view.SceneID,
	}
}

func projectedTriples(entity epistemic.Entity) []message.Triple {
	triples := make([]message.Triple, 0, len(entity.Facts))
	for _, fact := range entity.Facts {
		triples = append(triples, message.Triple{
			Subject: entity.ID, Predicate: fact.Predicate.String(), Object: fact.Object,
		})
	}
	return triples
}

type fakePublisher struct {
	journal  *journal
	subjects []string
	payloads [][]byte
	msgIDs   []string
}

type acknowledgedThenFailedPublisher struct {
	client interface {
		PublishToStreamWithMsgID(context.Context, string, []byte, string) error
	}
	calls int
}

func (p *acknowledgedThenFailedPublisher) PublishToStreamWithMsgID(
	ctx context.Context, subject string, data []byte, msgID string,
) error {
	p.calls++
	if err := p.client.PublishToStreamWithMsgID(ctx, subject, data, msgID); err != nil {
		return err
	}
	if p.calls == 1 {
		return errors.New("the server stored the task, but the caller lost the PubAck")
	}
	return nil
}

func (f *fakePublisher) PublishToStreamWithMsgID(
	_ context.Context, subject string, data []byte, msgID string,
) error {
	f.journal.note("publish:" + subject)
	f.subjects = append(f.subjects, subject)
	f.payloads = append(f.payloads, data)
	f.msgIDs = append(f.msgIDs, msgID)
	return nil
}

type spawnFixture struct {
	journal   *journal
	recorder  *fakeRecorder
	guard     *fakeGuard
	projector *fakeProjector
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
		projector: &fakeProjector{journal: j},
		publisher: &fakePublisher{journal: j},
	}
	spawner, err := stage.NewSpawner(
		spec, fixture.recorder, fixture.guard, fixture.projector, &fakePrompter{journal: j}, fixture.publisher)
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
	want := []string{"advance:adjudicating", "guard:adjudicator", "project:public-adjudicator", "prompt:adjudicate",
		"publish:agent.task.adjudicator"}
	if strings.Join(fixture.journal.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", fixture.journal.calls, want)
	}
}

func TestSpawner_MapsEachRoleToItsClosedEpistemicPurpose(t *testing.T) {
	for name, testCase := range map[string]struct {
		spec    persona.Spec
		purpose epistemic.Purpose
	}{
		"adjudicator": {spec: persona.Adjudicator(), purpose: epistemic.PurposePublicAdjudicator},
		"narrator":    {spec: persona.Narrator(), purpose: epistemic.PurposeNarrator},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newSpawnFixture(t, testCase.spec)
			if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !slices.Equal(fixture.projector.audiences, []epistemic.Purpose{testCase.purpose}) {
				t.Fatalf("projection purposes = %v, want %s", fixture.projector.audiences, testCase.purpose)
			}
		})
	}
}

func TestSpawner_NonMysteryInterpretationCommitsNoOpWithoutModelPath(t *testing.T) {
	j := &journal{}
	recorder := &fakeRecorder{journal: j, transition: turn.Transition{Outcome: turn.OutcomeAdvanced}}
	guard := &fakeGuard{journal: j, resumption: persona.Resumption{Decision: persona.DecisionRun}}
	projector := &fakeProjector{journal: j}
	publisher := &fakePublisher{journal: j}
	store := &fakeDecisionStore{journal: j}
	writer := &fakeDecisionWriter{journal: j}
	scope, err := epistemic.NewScope("", nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	spawner, err := stage.NewSpawner(
		persona.Casekeeper(), recorder, guard, projector, &fakePrompter{journal: j}, publisher,
		stage.WithCaseInterpretation(scope, store, writer),
	)
	if err != nil {
		t.Fatalf("NewSpawner: %v", err)
	}
	if err := spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"advance:interpreting", "guard:casekeeper", "put:case-decision", "merge:case-decision"}
	if !slices.Equal(j.calls, want) {
		t.Fatalf("calls = %v, want %v", j.calls, want)
	}
	if projector.calls != 0 || len(publisher.payloads) != 0 {
		t.Fatalf("non-mystery used model path: projections=%d tasks=%d", projector.calls, len(publisher.payloads))
	}
	if len(store.records) != 1 || store.records[0].Status != payload.CaseDecisionStatusNotApplicable {
		t.Fatalf("stored records = %#v", store.records)
	}
	if len(writer.triples) != 1 || writer.triples[0].Predicate != vocabulary.TurnCaseDecisionRef.String() {
		t.Fatalf("no-op triples = %#v", writer.triples)
	}
}

func TestSpawner_MysteryInterpretationUsesPrivateProjectionAndPublishesCasekeeperTask(t *testing.T) {
	j := &journal{}
	recorder := &fakeRecorder{journal: j, transition: turn.Transition{Outcome: turn.OutcomeAdvanced}}
	guard := &fakeGuard{journal: j, resumption: persona.Resumption{Decision: persona.DecisionRun}}
	projector := &fakeProjector{journal: j}
	publisher := &fakePublisher{journal: j}
	store := &fakeDecisionStore{journal: j}
	writer := &fakeDecisionWriter{journal: j}
	scope, err := epistemic.NewScope("acme.semmachina.keep.starter.case.bellweather", map[string][]string{
		"acme.semmachina.keep.starter.character.rook": {"belief-rook"},
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	spawner, err := stage.NewSpawner(
		persona.Casekeeper(), recorder, guard, projector, &fakePrompter{journal: j}, publisher,
		stage.WithCaseInterpretation(scope, store, writer),
	)
	if err != nil {
		t.Fatalf("NewSpawner: %v", err)
	}
	if err := spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"advance:interpreting", "guard:casekeeper", "project:casekeeper", "prompt:interpret",
		"publish:agent.task.casekeeper",
	}
	if !slices.Equal(j.calls, want) {
		t.Fatalf("calls = %v, want %v", j.calls, want)
	}
	if len(publisher.payloads) != 1 || len(store.records) != 0 || len(writer.triples) != 0 {
		t.Fatalf("real path tasks=%d no-op-records=%d no-op-triples=%d",
			len(publisher.payloads), len(store.records), len(writer.triples))
	}
}

func TestSpawner_RestartedInterpretationWithArtifactSkipsWithoutSecondDecision(t *testing.T) {
	j := &journal{}
	recorder := &fakeRecorder{journal: j, transition: turn.Transition{
		Previous: vocabulary.PhaseInterpreting, Phase: vocabulary.PhaseInterpreting,
		Outcome: turn.OutcomeResumed,
	}}
	guard := &fakeGuard{journal: j, resumption: persona.Resumption{
		Role: persona.RoleCasekeeper, Decision: persona.DecisionSkip,
		Ref: content.Ref{Instance: "TEST_CONTENT", Key: "turn/" + testTurnID + "/case-decision"},
	}}
	projector := &fakeProjector{journal: j}
	publisher := &fakePublisher{journal: j}
	store := &fakeDecisionStore{journal: j}
	writer := &fakeDecisionWriter{journal: j}
	scope, err := epistemic.NewScope("acme.semmachina.keep.starter.case.bellweather", map[string][]string{
		"acme.semmachina.keep.starter.character.rook": {"belief-rook"},
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	spawner, err := stage.NewSpawner(
		persona.Casekeeper(), recorder, guard, projector, &fakePrompter{journal: j}, publisher,
		stage.WithCaseInterpretation(scope, store, writer),
	)
	if err != nil {
		t.Fatalf("NewSpawner: %v", err)
	}
	if err := spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"advance:interpreting", "guard:casekeeper"}
	if !slices.Equal(j.calls, want) {
		t.Fatalf("restart calls = %v, want %v", j.calls, want)
	}
	if len(store.records) != 0 || len(writer.triples) != 0 || projector.calls != 0 || len(publisher.payloads) != 0 {
		t.Fatalf("restart duplicated work: records=%d triples=%d projections=%d tasks=%d",
			len(store.records), len(writer.triples), projector.calls, len(publisher.payloads))
	}
}

func TestSpawner_ProjectionFailureDoesNotBuildOrPublishATask(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Adjudicator())
	fixture.projector.err = errors.New("secret boundary unavailable")

	err := fixture.spawner.Run(t.Context(), testTrigger())
	if err == nil || !strings.Contains(err.Error(), "secret boundary unavailable") {
		t.Fatalf("Run error = %v, want projector failure", err)
	}
	if len(fixture.publisher.payloads) != 0 {
		t.Fatalf("projector failure published %d task(s)", len(fixture.publisher.payloads))
	}
	if slices.Contains(fixture.journal.calls, "prompt:adjudicate") {
		t.Fatalf("projector failure reached prompt builder: %v", fixture.journal.calls)
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
	if fixture.projector.calls != 0 {
		t.Errorf("a skipped stage projected context %d time(s)", fixture.projector.calls)
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

func TestSpawner_UsesThePersistedResumeAttemptAsTaskGeneration(t *testing.T) {
	fixture := newSpawnFixture(t, persona.Adjudicator())
	fixture.projector.projection = &epistemic.Projection{
		TurnID: testTurnID, TurnEntityID: testTurnEntityID, SceneID: testSceneID,
		Turn: epistemic.Entity{ID: testTurnEntityID, Facts: []epistemic.Fact{{
			Predicate: vocabulary.TurnResumeAttempts, Object: float64(1),
		}}},
	}

	if err := fixture.spawner.Run(t.Context(), testTrigger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var envelope struct {
		Payload agentic.TaskMessage `json:"payload"`
	}
	if err := json.Unmarshal(fixture.publisher.payloads[0], &envelope); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if want := string(persona.RoleAdjudicator) + "/" + testTurnID + "/resume/1"; envelope.Payload.TaskID != want {
		t.Fatalf("resumed task id = %q, want %q", envelope.Payload.TaskID, want)
	}
	if fixture.publisher.msgIDs[0] != envelope.Payload.TaskID {
		t.Fatalf("resumed MsgID = %q, want task id %q", fixture.publisher.msgIDs[0], envelope.Payload.TaskID)
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
	if got := fixture.publisher.msgIDs[0]; got != task.TaskID {
		t.Errorf("Nats-Msg-Id = %q, want deterministic task id %q", got, task.TaskID)
	}
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

// This is the exact publish crash window: JetStream accepts the first task and
// returns a PubAck, but the process loses that success and the stage delivery is
// retried. The deterministic TaskID is also the Nats-Msg-Id, so the second
// publish is acknowledged as a duplicate and only one task is stored/delivered.
func TestSpawner_AckLostAfterStoreAndRedeliveryProducesOneTask(t *testing.T) {
	harness := testinfra.Require(t)
	stream, err := harness.Client.EnsureStream(t.Context(), stage.AgentStreamConfig())
	if err != nil {
		t.Fatalf("ensure AGENT: %v", err)
	}
	before, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read AGENT before publish: %v", err)
	}

	journal := &journal{}
	publisher := &acknowledgedThenFailedPublisher{client: harness.Client}
	uniqueActionID := "crash-window-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	uniqueTurnID := "turn-" + uniqueActionID
	spawner, err := stage.NewSpawner(
		persona.Adjudicator(),
		&fakeRecorder{journal: journal, transition: turn.Transition{Outcome: turn.OutcomeAdvanced}},
		&fakeGuard{journal: journal, resumption: persona.Resumption{Decision: persona.DecisionRun}},
		&fakeProjector{journal: journal},
		&fakePrompter{journal: journal, actionID: uniqueActionID},
		publisher,
	)
	if err != nil {
		t.Fatalf("NewSpawner: %v", err)
	}
	trigger := stage.Trigger{
		TurnID:       uniqueTurnID,
		TurnEntityID: strings.TrimSuffix(testTurnEntityID, testTurnID) + uniqueTurnID,
		Subject:      "semmachina.turn.adjudicating",
	}
	if err := spawner.Run(t.Context(), trigger); err == nil {
		t.Fatal("first delivery saw success even though its PubAck was deliberately lost")
	}
	if err := spawner.Run(t.Context(), trigger); err != nil {
		t.Fatalf("redelivered stage: %v", err)
	}

	after, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("read AGENT after publish: %v", err)
	}
	wantTaskID := string(persona.RoleAdjudicator) + "-" + uniqueTurnID
	stored := 0
	for seq := before.State.LastSeq + 1; seq <= after.State.LastSeq; seq++ {
		raw, getErr := stream.GetMsg(t.Context(), seq)
		if getErr != nil {
			if errors.Is(getErr, jetstream.ErrMsgNotFound) {
				continue
			}
			t.Fatalf("read AGENT sequence %d: %v", seq, getErr)
		}
		var envelope struct {
			Payload agentic.TaskMessage `json:"payload"`
		}
		if json.Unmarshal(raw.Data, &envelope) == nil && envelope.Payload.TaskID == wantTaskID {
			stored++
		}
	}
	if stored != 1 {
		t.Fatalf("stored tasks with TaskID %q = %d, want exactly 1", wantTaskID, stored)
	}

	consumerName := "crash-window-" + strings.TrimPrefix(uniqueActionID, "crash-window-")
	consumer, err := stream.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   before.State.LastSeq + 1,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: stage.TaskSubjectFor(persona.RoleAdjudicator),
	})
	if err != nil {
		t.Fatalf("create proof consumer: %v", err)
	}
	t.Cleanup(func() { _ = stream.DeleteConsumer(context.Background(), consumerName) })
	batch, err := consumer.Fetch(256, jetstream.FetchMaxWait(250*time.Millisecond))
	if err != nil {
		t.Fatalf("fetch stored tasks: %v", err)
	}
	delivered := 0
	for msg := range batch.Messages() {
		var envelope struct {
			Payload agentic.TaskMessage `json:"payload"`
		}
		if json.Unmarshal(msg.Data(), &envelope) == nil && envelope.Payload.TaskID == wantTaskID {
			delivered++
		}
		if err := msg.Ack(); err != nil {
			t.Fatalf("ack proof delivery: %v", err)
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered tasks with TaskID %q = %d, want exactly 1", wantTaskID, delivered)
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
		&fakeProjector{journal: j},
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
		&fakeProjector{journal: j},
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
