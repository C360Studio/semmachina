package boot_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/boot"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recorder builds steps that append their own id to a shared trace, so a test
// can assert WHAT RAN as well as what was refused.
type recorder struct {
	trace []boot.StepID
	fail  map[boot.StepID]error
	check map[boot.StepID]error
}

func (r *recorder) step(id boot.StepID, needs ...boot.StepID) boot.Step {
	step := boot.Step{
		ID:    id,
		Needs: needs,
		Run: func(context.Context) error {
			if err := r.fail[id]; err != nil {
				return err
			}
			r.trace = append(r.trace, id)
			return nil
		},
	}
	if err, ok := r.check[id]; ok {
		step.Check = func(context.Context) error { return err }
	}
	return step
}

func newRecorder() *recorder {
	return &recorder{fail: map[boot.StepID]error{}, check: map[boot.StepID]error{}}
}

func TestSequence_RunsEveryStepInOrder(t *testing.T) {
	rec := newRecorder()
	seq, err := boot.NewSequence(quietLogger(),
		rec.step("first"),
		rec.step("second", "first"),
		rec.step("third", "second", "first"),
	)
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}
	if err := seq.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []boot.StepID{"first", "second", "third"}
	if !slices.Equal(rec.trace, want) {
		t.Errorf("ran %v, want %v", rec.trace, want)
	}
	if !slices.Equal(seq.Completed(), want) {
		t.Errorf("completed %v, want %v", seq.Completed(), want)
	}
}

// The whole point of declaring prerequisites separately from the order: a
// sequence whose ORDER contradicts its own dependencies is refused, and refused
// BEFORE the out-of-order step does any of its work.
//
// This is what turns "the ordering is the composition's to get right" from a
// comment into a mechanism. The realistic edit is somebody moving a line in a
// list — the same edit that, in the real sequence, runs the stranded-turn pass
// before the rule processor and ends live turns.
func TestSequence_RefusesAStepThatRunsBeforeItsPrerequisite(t *testing.T) {
	rec := newRecorder()
	seq, err := boot.NewSequence(quietLogger(),
		rec.step("first"),
		// Declared third-but-listed-second: the dependency says it needs
		// "second", and the order puts it before it.
		rec.step("third", "second", "first"),
		rec.step("second", "first"),
	)
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}

	err = seq.Run(t.Context())
	if err == nil {
		t.Fatal("a sequence whose order violates its own dependencies ran to completion")
	}
	for _, want := range []string{`"third"`, `"second"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if slices.Contains(rec.trace, boot.StepID("third")) {
		t.Error("the out-of-order step ran before it was refused; a half-done step is the state this check exists " +
			"to avoid entering")
	}
	if !slices.Equal(rec.trace, []boot.StepID{"first"}) {
		t.Errorf("ran %v, want only the step whose prerequisites were met", rec.trace)
	}
}

// A step's external precondition runs BEFORE its work and stops the sequence.
func TestSequence_StopsOnAFailedPrecondition(t *testing.T) {
	rec := newRecorder()
	rec.check["second"] = errors.New("the stream does not exist")
	seq, err := boot.NewSequence(quietLogger(),
		rec.step("first"),
		rec.step("second", "first"),
		rec.step("third", "second"),
	)
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}

	err = seq.Run(t.Context())
	if err == nil {
		t.Fatal("a step whose precondition failed still ran")
	}
	if !strings.Contains(err.Error(), "precondition") || !strings.Contains(err.Error(), "the stream does not exist") {
		t.Errorf("the refusal does not carry the precondition's own reason: %v", err)
	}
	if !slices.Equal(rec.trace, []boot.StepID{"first"}) {
		t.Errorf("ran %v; nothing after the failed precondition may run", rec.trace)
	}
}

// A precondition that passes must not be mistaken for the step itself running.
// Without this, a Check that returned nil and a Run that never fired would look
// identical from outside.
func TestSequence_APassingPreconditionStillRunsTheStep(t *testing.T) {
	rec := newRecorder()
	rec.check["second"] = nil
	seq, err := boot.NewSequence(quietLogger(), rec.step("first"), rec.step("second", "first"))
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}
	if err := seq.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Contains(rec.trace, boot.StepID("second")) {
		t.Error("a step with a passing precondition did not run")
	}
}

func TestSequence_StopsOnAFailedStep(t *testing.T) {
	rec := newRecorder()
	rec.fail["second"] = errors.New("the world did not import")
	seq, err := boot.NewSequence(quietLogger(),
		rec.step("first"), rec.step("second", "first"), rec.step("third", "second"))
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}

	err = seq.Run(t.Context())
	if err == nil {
		t.Fatal("a sequence continued past a failed step")
	}
	if !strings.Contains(err.Error(), "the world did not import") {
		t.Errorf("the failure does not carry the step's own reason: %v", err)
	}
	if slices.Contains(rec.trace, boot.StepID("third")) {
		t.Error("a step ran after its prerequisite failed")
	}
}

func TestNewSequence_RefusesShapesThatCannotBeChecked(t *testing.T) {
	rec := newRecorder()
	run := func(context.Context) error { return nil }

	for _, tc := range []struct {
		name  string
		steps []boot.Step
		want  string
	}{
		{name: "no steps", steps: nil, want: "no steps"},
		{
			name:  "duplicate id",
			steps: []boot.Step{rec.step("one"), rec.step("one")},
			want:  "declared twice",
		},
		{
			name:  "unknown prerequisite",
			steps: []boot.Step{rec.step("one", "nowhere")},
			want:  "does not declare",
		},
		{
			name:  "self prerequisite",
			steps: []boot.Step{rec.step("one", "one")},
			want:  "requires itself",
		},
		{
			name:  "no id",
			steps: []boot.Step{{Run: run}},
			want:  "no id",
		},
		{
			name:  "nothing to run",
			steps: []boot.Step{{ID: "one"}},
			want:  "nothing to run",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := boot.NewSequence(quietLogger(), tc.steps...)
			if err == nil {
				t.Fatal("NewSequence accepted a sequence it cannot check")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not say %q", err, tc.want)
			}
		})
	}
}

// wantDependencies is the boot sequence's dependency graph, stated a SECOND time.
//
// # Why a whole table and not a handful of asserted pairs
//
// The first version of this check asserted eleven pairs it had thought about,
// against roughly thirty declared dependencies. That is the shape of coverage
// that feels adequate and is not: a reviewer's probe happened to land on a pinned
// pair, which told both of us the sequence was protected when two thirds of it
// was not. `Rules -> World` and `Egress -> Resume` were among the unpinned ones,
// and both are load-bearing — a rule pack matching turn entities against a world
// that may still be importing, and a push consumer whose DeliverPolicy is "new",
// so a turn resolving before it binds is never delivered to the player at all.
//
// So the table is TOTAL. Every step appears exactly once with its complete
// prerequisite set, and the test below compares the whole graph: a deleted Needs
// entry, an added one, a renamed step or a reordering all fail. That is what
// retires the residual the Step doc used to state — that somebody deleting a
// Needs entry deletes the check with it. They now have to delete it twice, in two
// files, which is a deliberate act rather than a line moved while chasing
// something else.
//
// The ones worth naming, because a reader should not have to infer which of these
// are merely true and which are the reason the engine works:
//
//   - Resume -> Rules: the pass reads the work set the processor's bootstrap
//     replay publishes INTO. Run it first and it fails a turn that was about to
//     receive a trigger, terminally.
//   - Rules -> StageStream: a rule firing before the stream exists publishes into
//     nothing, and a stage never triggered looks exactly like one that ran and did
//     nothing.
//   - Rules -> World: the pack matches turn entities and its stages read the
//     world those turns happen in.
//   - Egress -> Resume, and Intake -> Egress: the notifier's durable starts at
//     "new", so anything resolving before it binds is retrievable but never
//     pushed. Binding before intake is what keeps that window empty.
//   - Agentic -> AgentStream: the loop binds a consumer per declared port.
//   - Intake/Stages/Ingress -> World: nothing may consume an action or run a
//     persona against a world whose import is unproven.
var wantDependencies = map[boot.StepID][]boot.StepID{
	boot.StepConnect:      nil,
	boot.StepStreams:      {boot.StepConnect},
	boot.StepEntityStream: {boot.StepStreams},
	boot.StepGraph:        {boot.StepConnect, boot.StepEntityStream},
	boot.StepAgentStream:  {boot.StepStreams},
	boot.StepPersonas:     {boot.StepConnect},
	boot.StepAgentic:      {boot.StepAgentStream, boot.StepPersonas},
	boot.StepWorld:        {boot.StepGraph},
	boot.StepLifecycle:    {boot.StepWorld, boot.StepGraph},
	boot.StepStageStream:  {boot.StepStreams},
	boot.StepRules:        {boot.StepStageStream, boot.StepGraph, boot.StepWorld, boot.StepLifecycle},
	boot.StepResume: {
		boot.StepRules, boot.StepStageStream, boot.StepAgentStream, boot.StepAgentic, boot.StepWorld,
	},
	boot.StepLoopFailures: {boot.StepResume, boot.StepAgentStream, boot.StepWorld},
	boot.StepStages:       {boot.StepLoopFailures, boot.StepAgentic, boot.StepWorld},
	boot.StepEgress:       {boot.StepStageStream, boot.StepResume},
	boot.StepLedger:       {boot.StepWorld, boot.StepResume},
	boot.StepActionStream: {boot.StepStreams},
	boot.StepIntake: {
		boot.StepActionStream, boot.StepStages, boot.StepEgress, boot.StepLedger, boot.StepWorld,
	},
	boot.StepIngress: {boot.StepIntake},
}

// The engine's real sequence, checked as DATA rather than by booting it.
//
// Three properties, and each catches a different edit:
//
//   - the step SET matches, so an added or removed step fails here rather than
//     being silently unprotected;
//   - each step's prerequisite SET matches exactly, so a deleted Needs entry
//     fails even though the sequence would still run happily without it;
//   - every prerequisite precedes its dependant in the slice, so a reordering
//     fails even when the declarations are untouched.
func TestEngineSequence_MatchesItsDeclaredDependencyGraph(t *testing.T) {
	steps := newTestEngine(t, testConfig(t)).Steps()

	position := make(map[boot.StepID]int, len(steps))
	got := make(map[boot.StepID][]boot.StepID, len(steps))
	for idx, step := range steps {
		position[step.ID] = idx
		got[step.ID] = step.Needs
	}

	for id := range got {
		if _, declared := wantDependencies[id]; !declared {
			t.Errorf("the sequence declares step %q, which this table does not; a step nobody stated the "+
				"dependencies of is a step no reordering check protects", id)
		}
	}
	for id := range wantDependencies {
		if _, present := got[id]; !present {
			t.Errorf("this table declares step %q, which the sequence does not", id)
		}
	}

	for id, want := range wantDependencies {
		needs, present := got[id]
		if !present {
			continue
		}
		gotSorted := slices.Clone(needs)
		wantSorted := slices.Clone(want)
		slices.Sort(gotSorted)
		slices.Sort(wantSorted)
		if !slices.Equal(gotSorted, wantSorted) {
			t.Errorf("step %q requires %v; this table says %v. One of the two is wrong, and the sequence would "+
				"run either way — which is exactly why both are written down", id, gotSorted, wantSorted)
		}
		for _, need := range want {
			if _, ok := position[need]; !ok {
				continue
			}
			if position[id] <= position[need] {
				t.Errorf("step %q is listed at %d, before %q at %d, which it requires",
					id, position[id], need, position[need])
			}
		}
	}
}

// The two steps that let play begin re-read the import marker before they run.
// A remembered boolean would satisfy a reviewer and not a partial world.
func TestEngineSequence_GatesBothHalvesOfIngressOnAnExternalCheck(t *testing.T) {
	engine := newTestEngine(t, testConfig(t))
	for _, want := range []boot.StepID{boot.StepStages, boot.StepIntake} {
		found := false
		for _, step := range engine.Steps() {
			if step.ID != want {
				continue
			}
			found = true
			if step.Check == nil {
				t.Errorf("step %q has no external precondition; the import marker gates INGRESS, not just the "+
					"importer, and a step that only trusts an earlier step's memory is not a gate", want)
			}
		}
		if !found {
			t.Errorf("the sequence declares no %q step", want)
		}
	}
}
