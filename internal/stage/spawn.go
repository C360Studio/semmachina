package stage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The agentic loop's wire coordinates are persona's, not this package's: three
// components have to agree on them exactly and only one of them is a stage — the
// spawner publishes a task, the loop-failure watcher binds the stream, and the
// boot-time stranded-turn pass reads which tasks are still unacknowledged. See
// internal/persona/transport.go.
const (
	// TaskStream is the agentic loop's stream.
	TaskStream = persona.TaskStream
	// TaskSubjectPrefix is the agentic loop's task subject space.
	TaskSubjectPrefix = persona.TaskSubjectPrefix
	// AgentSubjectFilter is the whole agentic subject space the AGENT stream
	// captures.
	AgentSubjectFilter = persona.AgentSubjectFilter
	// TaskSource is the producer stamped on every task envelope this package
	// publishes.
	TaskSource = "semmachina-stage"
)

// TaskSubjectFor returns the subject a role's tasks are published on.
func TaskSubjectFor(role persona.Role) string { return persona.TaskSubjectFor(role) }

// ResumeGuard answers whether a stage's artifact is already on the turn.
type ResumeGuard interface {
	Check(ctx context.Context, spec persona.Spec, turnID, turnEntityID string) (persona.Resumption, error)
}

// ContextProjector builds the purpose-authorized value projection a persona is
// prompted with.
type ContextProjector interface {
	Project(ctx context.Context, audience epistemic.AuthenticatedAudience) (*epistemic.Projection, error)
}

// Prompter renders an authorized projection into one persona's spawn request.
type Prompter interface {
	Adjudicate(ctx context.Context, projection *epistemic.Projection) (persona.TaskRequest, error)
	Narrate(ctx context.Context, projection *epistemic.Projection) (persona.TaskRequest, error)
}

// TaskPublisher hands a composed task to the agentic loop.
type TaskPublisher interface {
	PublishToStreamWithMsgID(ctx context.Context, subject string, data []byte, msgID string) error
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ ResumeGuard      = (*persona.Guard)(nil)
	_ ContextProjector = (*epistemic.Projector)(nil)
	_ Prompter         = (*persona.Builder)(nil)
)

// spawnPhases pairs each persona with the phase its stage enters.
var spawnPhases = map[persona.Role]vocabulary.TurnPhase{
	persona.RoleAdjudicator: vocabulary.PhaseAdjudicating,
	persona.RoleNarrator:    vocabulary.PhaseNarrating,
}

// Spawner is the stage that runs a persona.
//
// It is one type for both personas because the two differ in exactly three
// values — which phase they enter, which prompt they are given, and which
// artifact says they finished — and every one of those already lives on the
// persona's Spec. A second type would be a second copy of the ordering below,
// which is the part that is easy to get wrong.
type Spawner struct {
	spec      persona.Spec
	phase     vocabulary.TurnPhase
	recorder  PhaseRecorder
	guard     ResumeGuard
	projector ContextProjector
	prompter  Prompter
	tasks     TaskPublisher
	logger    *slog.Logger
}

// SpawnerOption configures a Spawner.
type SpawnerOption func(*Spawner)

// WithSpawnerLogger sets the spawner's logger.
func WithSpawnerLogger(logger *slog.Logger) SpawnerOption {
	return func(s *Spawner) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// NewSpawner builds the stage that runs one persona.
func NewSpawner(
	spec persona.Spec,
	recorder PhaseRecorder,
	guard ResumeGuard,
	projector ContextProjector,
	prompter Prompter,
	tasks TaskPublisher,
	opts ...SpawnerOption,
) (*Spawner, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	phase, ok := spawnPhases[spec.Role]
	if !ok {
		return nil, fmt.Errorf("persona %q has no turn phase; a persona the turn loop cannot place has no stage",
			spec.Role)
	}
	switch {
	case recorder == nil:
		return nil, fmt.Errorf("the %s stage requires a phase recorder", spec.Role)
	case guard == nil:
		return nil, fmt.Errorf(
			"the %s stage requires a resume guard; without one every redelivered trigger buys a second billed "+
				"model call for a judgment the engine already has", spec.Role)
	case projector == nil:
		return nil, fmt.Errorf("the %s stage requires an epistemic context projector", spec.Role)
	case prompter == nil:
		return nil, fmt.Errorf("the %s stage requires a prompt builder", spec.Role)
	case tasks == nil:
		return nil, fmt.Errorf("the %s stage requires a task publisher", spec.Role)
	}

	spawner := &Spawner{
		spec: spec, phase: phase, recorder: recorder, guard: guard,
		projector: projector, prompter: prompter, tasks: tasks, logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(spawner)
	}
	return spawner, nil
}

// Phase implements Stage.
func (s *Spawner) Phase() vocabulary.TurnPhase { return s.phase }

// Run enters the stage, decides whether the persona still has work, and spawns
// it.
//
// # The recorder and the guard answer different questions, in that order
//
// The recorder answers "is this transition legal, and is this stage still the
// turn's business?" The guard answers "did the interrupted attempt finish?" Both
// are needed and the order between them is not a preference:
//
//   - The recorder goes FIRST, so that by the time the guard is consulted the
//     turn is IN this stage's own phase. That is what makes the guard's answer
//     safe to act on. Ask the guard first and it can be answered about a turn
//     still sitting in `accepted`, where "skip" would mean skipping a stage that
//     never ran — and a caller that then tried to advance past this stage would
//     be refused by the recorder as an illegal stage skip, which is the collision
//     this ordering removes by construction.
//   - A SKIP therefore never means "advance past this stage". It means "the
//     artifact this stage produces is already on the turn, so do not pay for it
//     again". The turn is left in this stage's phase, which is exactly where the
//     artifact says it belongs, and the sequencing rule that matches that
//     artifact is what carries the turn onward — the same rule that would have
//     carried it onward had the persona just run.
//
// So the two never disagree: the guard is only ever asked about the phase it
// governs, and it never asks the recorder for a transition the FSM forbids.
func (s *Spawner) Run(ctx context.Context, trigger Trigger) error {
	run, err := enter(ctx, s.recorder, trigger, s.phase)
	if err != nil {
		return err
	}
	if !run {
		s.logger.Debug("stage trigger declined; the turn has moved past it",
			"phase", s.phase, "turn", trigger.TurnEntityID)
		return nil
	}

	resumption, err := s.guard.Check(ctx, s.spec, trigger.TurnID, trigger.TurnEntityID)
	if err != nil {
		return fmt.Errorf("check whether the %s stage already finished for turn %s: %w",
			s.spec.Role, trigger.TurnEntityID, err)
	}
	if resumption.Decision == persona.DecisionSkip {
		s.logger.Info("persona stage already produced its artifact; not re-running it",
			"role", s.spec.Role, "turn", trigger.TurnEntityID, "artifact", resumption.Ref.String())
		return nil
	}

	audience, err := audienceFor(s.spec.Role, trigger)
	if err != nil {
		return err
	}
	projection, err := s.projector.Project(ctx, audience)
	if err != nil {
		return fmt.Errorf("project %s context for turn %s: %w", s.spec.Role, trigger.TurnEntityID, err)
	}
	request, err := s.request(ctx, projection)
	if err != nil {
		return err
	}
	task, err := s.spec.Task(request)
	if err != nil {
		return err
	}
	return s.publish(ctx, task)
}

// request renders the prompt this persona is spawned with.
func (s *Spawner) request(ctx context.Context, projection *epistemic.Projection) (persona.TaskRequest, error) {
	switch s.spec.Role {
	case persona.RoleAdjudicator:
		return s.prompter.Adjudicate(ctx, projection)
	case persona.RoleNarrator:
		return s.prompter.Narrate(ctx, projection)
	default:
		return persona.TaskRequest{}, fmt.Errorf("persona %q has no prompt", s.spec.Role)
	}
}

func audienceFor(role persona.Role, trigger Trigger) (epistemic.AuthenticatedAudience, error) {
	switch role {
	case persona.RoleAdjudicator:
		return epistemic.PublicAdjudicatorAudience(trigger.TurnID, trigger.TurnEntityID), nil
	case persona.RoleNarrator:
		return epistemic.NarratorAudience(trigger.TurnID, trigger.TurnEntityID), nil
	default:
		return epistemic.AuthenticatedAudience{}, fmt.Errorf("persona %q has no epistemic audience", role)
	}
}

// publish hands the task to the agentic loop.
//
// The task rides inside a BaseMessage envelope because that is what the loop's
// intake decodes; publishing the bare TaskMessage would land bytes the consumer
// reads as an unknown payload and drops.
func (s *Spawner) publish(ctx context.Context, task agentic.TaskMessage) error {
	envelope := message.NewBaseMessage(task.Schema(), &task, TaskSource)
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode the %s task: %w", s.spec.Role, err)
	}
	subject := TaskSubjectFor(s.spec.Role)
	if err := s.tasks.PublishToStreamWithMsgID(ctx, subject, data, task.TaskID); err != nil {
		return fmt.Errorf("publish the %s task to %s: %w", s.spec.Role, subject, err)
	}
	return nil
}
