package boot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// StepID names one boot step.
type StepID string

// Step is one boot step with its prerequisites stated as data.
//
// # Why the prerequisites are declared rather than implied by the order
//
// Because the ORDER is the thing that gets edited. A boot sequence is a slice
// somebody adds to, moves an entry in, or reorders while chasing an unrelated
// failure — and in this engine three of those edits end live turns rather than
// merely misbehaving (see the doc on Needs below). A slice alone records the
// intended order and nothing about WHY, so a reordering is silent: it compiles,
// it boots, and the damage is a player's turn failed by a pass that read a
// half-built world.
//
// Declaring the constraint separately from the order makes the two independent,
// which is what gives the check something to catch: Run executes in slice order
// and REFUSES a step whose prerequisites are not yet done, naming the missing
// one. A reordering that violates a dependency is a boot that stops with a
// sentence instead of a campaign that quietly loses turns.
//
// The declarations are as editable as the order, so on their own they would carry
// their own hole: somebody deleting a Needs entry deletes the check with it. That
// is why the graph is written down a SECOND time, as a total golden table beside
// the sequence (see TestEngineSequence_MatchesItsDeclaredDependencyGraph). A
// deleted entry, an added one, a new step and a reordering all fail there, so
// removing a constraint takes a deliberate edit in two files rather than one line
// moved while chasing something else.
type Step struct {
	// ID names the step. Unique within a sequence.
	ID StepID
	// Needs are the steps that MUST have completed before this one runs.
	//
	// A dependency, not a preference. Each one is stated where the step is
	// declared, next to the sentence explaining what breaks without it.
	Needs []StepID
	// Check is an EXTERNAL precondition, read from the running system rather
	// than from this sequence's own bookkeeping.
	//
	// It is separate from Run so the answer to "which preconditions can actually
	// be verified?" is readable off the sequence rather than buried in a step
	// body. A nil Check is a step whose precondition nothing outside this
	// process can answer, and those are the ones the ordering alone protects.
	Check func(ctx context.Context) error
	// Run performs the step.
	Run func(ctx context.Context) error
}

// Sequence is an ordered set of boot steps with declared prerequisites.
type Sequence struct {
	steps  []Step
	logger *slog.Logger
	done   map[StepID]bool
	// completed is the steps that ran, in the order they ran, so a failure can
	// report how far the boot got.
	completed []StepID
}

// NewSequence validates a boot sequence's shape without running any of it.
//
// Three refusals, all of them wiring bugs rather than runtime conditions: a
// duplicate step id (which would make "is it done?" ambiguous), a prerequisite
// naming a step the sequence does not declare (a check that can never pass), and
// a step with no work to do.
//
// Ordering is NOT validated here, deliberately. A sequence whose order violates
// its own dependencies is refused at RUN, when the violated prerequisite is
// named against a step that was actually reached — which is the diagnosis an
// operator can act on. Refusing at construction would produce the same
// information one layer further from the failure, and would also make it
// impossible to test the run-time refusal without a second construction path.
func NewSequence(logger *slog.Logger, steps ...Step) (*Sequence, error) {
	if len(steps) == 0 {
		return nil, errors.New("a boot sequence with no steps starts nothing")
	}
	if logger == nil {
		logger = slog.Default()
	}

	declared := make(map[StepID]bool, len(steps))
	for _, step := range steps {
		if step.ID == "" {
			return nil, errors.New("a boot step has no id; a step nothing can name cannot be a prerequisite")
		}
		if declared[step.ID] {
			return nil, fmt.Errorf(
				"boot step %q is declared twice; a duplicate id makes \"has this run?\" a question with two "+
					"answers, and every prerequisite naming it would be satisfied by whichever ran first", step.ID)
		}
		if step.Run == nil {
			return nil, fmt.Errorf("boot step %q has nothing to run", step.ID)
		}
		declared[step.ID] = true
	}
	for _, step := range steps {
		for _, need := range step.Needs {
			if need == step.ID {
				return nil, fmt.Errorf("boot step %q requires itself", step.ID)
			}
			if !declared[need] {
				return nil, fmt.Errorf(
					"boot step %q requires %q, which this sequence does not declare; a prerequisite naming a "+
						"step that does not exist can never be satisfied and would stop every boot",
					step.ID, need)
			}
		}
	}
	return &Sequence{steps: steps, logger: logger, done: make(map[StepID]bool, len(steps))}, nil
}

// Steps returns the sequence's steps in order.
func (s *Sequence) Steps() []Step { return slices.Clone(s.steps) }

// Completed returns the steps that finished, in the order they ran.
func (s *Sequence) Completed() []StepID { return slices.Clone(s.completed) }

// Run executes every step in order, refusing one whose prerequisites are not met.
//
// A prerequisite failure is reported BEFORE the step's own external check and
// before any of its work, so a misordered sequence stops without having done
// half of the thing that was out of order.
func (s *Sequence) Run(ctx context.Context) error { return s.RunThrough(ctx, "") }

// RunThrough executes steps up to and INCLUDING last, or all of them when last
// is empty. It resumes from wherever a previous call stopped.
//
// It exists for the one thing a boot test cannot otherwise reach: the state the
// ordering exists to PREVENT. Proving that the stranded-turn pass refuses an
// unstarted rule processor means standing in that state — everything up to the
// stage stream running, the processor not — and a test that mocked its way there
// would be asserting against a fake of the very condition in question.
//
// It is not a partial-boot facility for production. Nothing in cmd calls it, and
// a sequence stopped part-way is an engine with components running and no
// ingress, which is exactly the state Start tears down on failure.
func (s *Sequence) RunThrough(ctx context.Context, last StepID) error {
	if last != "" && !s.declares(last) {
		return fmt.Errorf("this sequence declares no step %q to run through", last)
	}
	for _, step := range s.steps {
		if s.done[step.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("boot cancelled before step %q: %w", step.ID, err)
		}
		if missing := s.missing(step); len(missing) > 0 {
			return fmt.Errorf(
				"boot step %q runs before %s, which it requires. This sequence's order and its declared "+
					"dependencies disagree, and the order is what was edited. Completed so far: %v",
				step.ID, quote(missing), s.completed)
		}
		if step.Check != nil {
			if err := step.Check(ctx); err != nil {
				return fmt.Errorf("boot step %q: precondition not met: %w", step.ID, err)
			}
		}
		started := time.Now()
		if err := step.Run(ctx); err != nil {
			return fmt.Errorf("boot step %q: %w", step.ID, err)
		}
		s.done[step.ID] = true
		s.completed = append(s.completed, step.ID)
		s.logger.Info("boot step complete", "step", step.ID, "took", time.Since(started))
		if step.ID == last {
			return nil
		}
	}
	return nil
}

func (s *Sequence) declares(id StepID) bool {
	for _, step := range s.steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

// missing returns the prerequisites a step declares that have not completed.
func (s *Sequence) missing(step Step) []StepID {
	var out []StepID
	for _, need := range step.Needs {
		if !s.done[need] {
			out = append(out, need)
		}
	}
	return out
}

func quote(ids []StepID) string {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, fmt.Sprintf("%q", id))
	}
	return strings.Join(quoted, ", ")
}
