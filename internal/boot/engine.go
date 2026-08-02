package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	sspersona "github.com/c360studio/semstreams/persona"
	"github.com/c360studio/semstreams/pkg/lifecycle"
	agenticloop "github.com/c360studio/semstreams/processor/agentic-loop"
	agenticmodel "github.com/c360studio/semstreams/processor/agentic-model"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	graphindex "github.com/c360studio/semstreams/processor/graph-index"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/c360studio/semstreams/processor/rule"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/accusation"
	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/companion"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/egress"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/gateway"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/knowledge"
	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/resume"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// The boot sequence's step ids, declared as constants so a prerequisite is a
// compile-time name rather than a string somebody spells twice.
const (
	// StepConnect opens the NATS connection.
	StepConnect StepID = "connect"
	// StepStreams declaratively provisions every ordinary stream this composition
	// owns before a component can bind or publish.
	StepStreams StepID = "streams"
	// StepEntityStream creates the fact lane graph-ingest consumes and the world
	// importer publishes onto.
	StepEntityStream StepID = "entity-stream"
	// StepGraph starts graph-ingest (the sole ENTITY_STATES writer) and
	// graph-index (the reverse-edge projection).
	StepGraph StepID = "graph"
	// StepAgentStream creates the stream the agentic loop's ports bind.
	StepAgentStream StepID = "agent-stream"
	// StepPersonas seeds the world's voice into the PERSONAS bucket.
	StepPersonas StepID = "personas"
	// StepAgentic registers the terminal tools and starts the agentic loop.
	StepAgentic StepID = "agentic"
	// StepWorld claims the campaign and instantiates the world.
	StepWorld StepID = "world"
	// StepLifecycle registers and attaches case lifecycle after world import.
	StepLifecycle StepID = "lifecycle"
	// StepStageStream creates the stage-trigger stream.
	StepStageStream StepID = "stage-stream"
	// StepKnowledge binds the deterministic grant consumer before rules can publish to it.
	StepKnowledge StepID = "knowledge"
	// StepAccusation binds the deterministic verifier consumer before rules can publish to it.
	StepAccusation StepID = "accusation"
	// StepCaseProgress binds deterministic lifecycle receipt handling before rules publish to it.
	StepCaseProgress StepID = "case-progress"
	// StepRules starts the rule processor that IS the turn's state machine.
	StepRules StepID = "rules"
	// StepResume runs the boot-time stranded-turn pass.
	StepResume StepID = "resume"
	// StepLoopFailures binds the durable loop-failure watcher and drains failures
	// that accumulated before this boot.
	StepLoopFailures StepID = "loop-failures"
	// StepStages binds the executable stage runners.
	StepStages StepID = "stages"
	// StepEgress binds the resolved-turn notifier.
	StepEgress StepID = "egress"
	// StepLedger creates the archive, reconciles it, and binds its consumer.
	StepLedger StepID = "ledger"
	// StepActionStream creates the player-action stream.
	StepActionStream StepID = "action-stream"
	// StepIntake binds the player-action consumer.
	StepIntake StepID = "intake"
	// StepIngress opens the player socket.
	StepIngress StepID = "ingress"
)

// streamReader is the narrow read surface the boot checks need.
type streamReader interface {
	GetStream(ctx context.Context, name string) (jetstream.Stream, error)
}

// Engine is one world instance: every component, in one process, started in an
// order whose violations are refusals rather than lost turns.
type Engine struct {
	cfg Config

	client    *natsclient.Client
	graph     *graphio.Store
	content   *content.Store
	backend   interface{ Close() error }
	recorder  *turn.Recorder
	gate      *campaign.Gate
	importer  *world.Importer
	results   *egress.Results
	lifecycle *lifecycle.Manager

	pkg       *world.Package
	plan      *world.Plan
	personas  []sspersona.Persona
	mechanics []selectedMechanicsDefinition
	sceneID   string
	playerID  string

	instantiation campaign.Instantiation

	tools            *agentictools.ExecutorRegistry
	companionExhaust *companion.Executor
	gatewaySv        *gateway.Gateway
	listener         net.Listener

	// components are stopped in reverse start order.
	components []namedComponent
	// ruleStatusCleared records that this boot DELETED the rule processor's
	// lifecycle status before starting it. That deletion is what makes the
	// status's reappearance a fact about this boot rather than one satisfied by
	// the previous boot's leftover key — and unlike a timestamp comparison, it
	// cannot be forged by a clock that stepped backwards.
	ruleStatusCleared bool

	seq    *Sequence
	served chan error
}

type namedComponent struct {
	name      string
	component component.LifecycleComponent
}

// New builds an engine from a validated configuration. It touches nothing.
func New(cfg Config) (*Engine, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := checkPayloadRegistry(cfg.Registry); err != nil {
		return nil, err
	}
	engine := &Engine{cfg: cfg, served: make(chan error, 1)}
	if err := engine.loadWorld(); err != nil {
		return nil, err
	}
	seq, err := NewSequence(cfg.Logger, engine.steps()...)
	if err != nil {
		return nil, err
	}
	engine.seq = seq
	return engine, nil
}

// SceneID returns the scene this instance plays in.
func (e *Engine) SceneID() string { return e.sceneID }

// PlayerID returns the instance player's entity id.
func (e *Engine) PlayerID() string { return e.playerID }

// CampaignID returns the campaign entity this instance serves.
func (e *Engine) CampaignID() string {
	if e.gate != nil {
		return e.gate.CampaignID()
	}
	id, _ := e.campaignIdentity().EntityID()
	return id
}

// Addr returns the address the player socket is listening on, or "" before the
// ingress step has run.
func (e *Engine) Addr() string {
	if e.listener == nil {
		return ""
	}
	return e.listener.Addr().String()
}

// Steps returns the boot sequence, for a caller that wants to inspect or
// document it without running it.
func (e *Engine) Steps() []Step { return e.seq.Steps() }

// Start runs the boot sequence.
//
// # The order, and what each position is protecting
//
// It is stated once, here, because the order IS the correctness argument and a
// reader should not have to reassemble it from the individual step declarations:
//
//  1. connect, provision every stream, then graph-ingest and graph-index — nothing else can read or
//     write authoritative state until the sole writer is running.
//  2. the AGENT stream, before anything can start the agentic loop.
//  3. the world provenance gate: claim with the sealed experience, import (or
//     skip, or refuse), read the world back, and mark it. A mismatch or
//     pre-provenance campaign stops here, before persona prompts or rules start.
//  4. the world's persona fragments, then the agentic loop. The loop reads the
//     persona bucket at Start and binds a consumer per declared input port; it
//     must be RUNNING before any persona stage can be triggered, or every stage
//     publishes a task nothing consumes and nak-loops.
//  5. attach the case lifecycle after the imported case is queryable. This
//     registers the shared manager before any lifecycle-transition rule loads,
//     while attach-on-existing preserves an earlier boot's phase.
//  6. the stage-trigger stream, BEFORE the rule processor — a rule that fires
//     while the stream does not exist publishes into nothing, and a stage that
//     was never triggered looks exactly like a stage that ran and did nothing.
//  7. the rule processor, gated on the proven world as well as its stage
//     dependencies.
//  8. the stranded-turn pass. This is the position that ends live turns when it
//     is wrong: the pass reads which turns the substrate still holds work for
//     and acts on what is MISSING from that set, and the processor's bootstrap
//     replay publishes into that very set. Run before the processor, the pass
//     reads an empty set for a turn about to receive a trigger and FAILS it —
//     terminally, after which the replayed trigger is declined.
//  9. the stage runners and the egress notifier. The notifier's durable is
//     DeliverPolicy "new", so a turn that resolves before it binds stays
//     retrievable and is not pushed; binding it here rather than after intake is
//     what keeps that window empty.
//  10. the ledger, then intake, then ingress. Ingress opens last because it is
//     the only step that lets a stranger add work.
//
// Every one of those is a Needs declaration on the step, so a reordering is
// refused rather than merely wrong.
func (e *Engine) Start(ctx context.Context) error {
	if err := e.seq.Run(ctx); err != nil {
		// A boot that got half-way leaves components running and a connection
		// open; unwinding is the difference between a failed start and a process
		// holding consumers nobody is reading. Stop is idempotent, so a caller
		// with its own deferred Stop is not penalised for this.
		e.Stop()
		return err
	}
	return nil
}

// StartThrough runs the boot sequence up to and including one step, resuming
// from wherever a previous call stopped.
//
// It is how a test stands in the state the ordering exists to prevent — the rule
// processor not started, everything before it running — so the refusal that
// protects live turns is proven against the real condition rather than a fake of
// it. Nothing in cmd calls it. A half-run sequence still needs Stop.
func (e *Engine) StartThrough(ctx context.Context, last StepID) error {
	if err := e.seq.RunThrough(ctx, last); err != nil {
		e.stopComponents()
		return err
	}
	return nil
}

// Run starts the engine and blocks until ctx is done or the socket stops.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	defer e.Stop()

	select {
	case <-ctx.Done():
		return nil
	case err := <-e.served:
		return err
	}
}

// Stop shuts the engine down, in reverse start order.
// Stop shuts the engine down, in reverse start order, and is safe to call twice.
//
// The order is the mirror of Start's and matters for the same reason: the socket
// stops taking work first, then the components stop — over the connection they
// still need, which is why the consumers this engine bound directly are stopped
// AFTER them and the client is closed last. Closing the connection first would
// turn every component's orderly shutdown into a transport error.
func (e *Engine) Stop() {
	if e.listener != nil {
		_ = e.listener.Close()
		e.listener = nil
	}
	e.stopComponents()
	if e.client != nil {
		// The consumers this engine bound itself — intake, the stages, the
		// loop-failure watcher, the egress notifier, the ledger — which no
		// component's Stop knows about.
		e.client.StopAllConsumers()
	}
	if e.backend != nil {
		_ = e.backend.Close()
		e.backend = nil
	}
	if e.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), e.cfg.StopTimeout)
		defer cancel()
		_ = e.client.Close(ctx)
		e.client = nil
	}
}

func (e *Engine) stopComponents() {
	for idx := len(e.components) - 1; idx >= 0; idx-- {
		entry := e.components[idx]
		if err := entry.component.Stop(e.cfg.StopTimeout); err != nil {
			e.log().Warn("a component did not stop cleanly", "component", entry.name, "error", err)
		}
	}
	e.components = nil
}

// steps declares the boot sequence: what runs, in what order, and what each step
// requires to have happened first.
func (e *Engine) steps() []Step {
	return []Step{
		{ID: StepConnect, Run: e.connect},
		{ID: StepStreams, Needs: []StepID{StepConnect}, Run: e.ensureStreams},
		{ID: StepEntityStream, Needs: []StepID{StepStreams}, Run: e.ensureEntityStream},
		{
			ID: StepGraph,
			// The fact lane first: graph-ingest binds its input consumer with
			// AutoCreate false, so a missing ENTITY stream is a sole-writer that
			// does not start.
			Needs: []StepID{StepConnect, StepEntityStream},
			Run:   e.startGraph,
		},
		{ID: StepAgentStream, Needs: []StepID{StepStreams}, Run: e.ensureAgentStream},
		{
			ID: StepWorld,
			// graph-ingest must be running to serve the claim and the import, and
			// graph-index to answer the membership readback the world step gates on.
			Needs: []StepID{StepGraph},
			Run:   e.instantiate,
		},
		{ID: StepPersonas, Needs: []StepID{StepConnect, StepWorld}, Run: e.seedPersonas},
		{
			ID: StepAgentic,
			// The stream first, because the loop binds a consumer per declared
			// input port and JetStream refuses a consumer whose filter its stream
			// does not capture — so a missing stream is a component that does not
			// start. The personas first, because the loop seeds its prompt
			// registry from the bucket at Start.
			Needs: []StepID{StepAgentStream, StepPersonas},
			Check: e.checkPersonaModels,
			Run:   e.startAgenticLoop,
		},
		{
			ID:    StepLifecycle,
			Needs: []StepID{StepWorld, StepGraph},
			Run:   e.attachCaseLifecycle,
		},
		{ID: StepStageStream, Needs: []StepID{StepStreams}, Run: e.ensureStageStream},
		{ID: StepKnowledge, Needs: []StepID{StepStageStream, StepGraph, StepWorld}, Run: e.startKnowledge},
		{
			ID:    StepAccusation,
			Needs: []StepID{StepStageStream, StepGraph, StepWorld, StepLifecycle},
			Run:   e.startAccusation,
		},
		{
			ID:    StepCaseProgress,
			Needs: []StepID{StepStageStream, StepGraph, StepWorld, StepLifecycle},
			Run:   e.startCaseProgress,
		},
		{
			ID: StepRules,
			// The stage stream must exist before a rule can fire: the pack's
			// actions publish stage triggers, and a JetStream publish into a
			// subject no stream captures reaches no durable consumer while
			// reporting success. The world must be instantiated because the pack
			// matches turn entities against a world the stages will read.
			Needs: []StepID{StepStageStream, StepGraph, StepWorld, StepLifecycle, StepKnowledge, StepAccusation,
				StepCaseProgress},
			Run: e.startRules,
		},
		{
			ID: StepResume,
			// The pass measures both queues that can deliver work for a turn, so
			// both streams must exist and the agentic loop's consumer must be
			// bound — an absent consumer on agent.task.* makes the pass refuse
			// rather than read "nothing in flight" and re-trigger every turn whose
			// persona is mid-flight. And the rule processor must have STARTED:
			// its bootstrap replay publishes into the set the pass reads.
			Needs: []StepID{StepRules, StepStageStream, StepAgentStream, StepAgentic, StepWorld, StepKnowledge,
				StepAccusation, StepCaseProgress},
			Check: e.checkResumePreconditions,
			Run:   e.reconcileStrandedTurns,
		},
		{
			ID: StepLoopFailures,
			// A queued failure is terminal evidence from work that already ran.
			// Drain it before an old stage trigger can spawn that work again.
			Needs: []StepID{StepResume, StepAgentStream, StepWorld},
			Run:   e.startLoopFailures,
		},
		{
			ID: StepStages,
			// After the pass, so no stage consumer drains a trigger the pass is
			// still measuring. This is the step after which a persona can run, so
			// it is where both gates that protect a persona live: the marker is
			// re-read, and the two lanes the agentic loop waits on are proven to
			// have somebody consuming them.
			Needs: []StepID{StepLoopFailures, StepAgentic, StepWorld},
			Check: e.checkStagePreconditions,
			Run:   e.startStages,
		},
		{
			ID: StepEgress,
			// Before intake, because the notifier's durable is DeliverPolicy
			// "new": a turn that resolved before it bound is retrievable and is
			// not pushed, and the way to keep that window empty is to bind before
			// anything can start a turn.
			Needs: []StepID{StepStageStream, StepResume},
			Run:   e.startEgress,
		},
		{ID: StepLedger, Needs: []StepID{StepWorld, StepResume}, Run: e.startLedger},
		{ID: StepActionStream, Needs: []StepID{StepStreams}, Run: e.ensureActionStream},
		{
			ID: StepIntake,
			// The last step before a stranger can add work, and the one the
			// marker gate exists for: a partial-world boot that consumed an action
			// would create a turn against a world it is about to re-import.
			Needs: []StepID{StepActionStream, StepStages, StepEgress, StepLedger, StepWorld},
			Check: e.checkImportMarked,
			Run:   e.startIntake,
		},
		{ID: StepIngress, Needs: []StepID{StepIntake}, Run: e.startIngress},
	}
}

func (e *Engine) log() *slog.Logger { return e.cfg.Logger }

// sleep waits, or returns the context's error.
func (e *Engine) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// seedSource is the campaign gate's seed generator: the configured one when this
// deployment pinned a seed, and crypto/rand otherwise.
//
// A pinned seed is honoured only where the gate honours ANY minted seed — on the
// create that wins. The gate discards a minted seed the moment it learns the
// campaign already exists, so a boot that pins a seed into a living campaign
// changes nothing, which is the behaviour a replay guarantee requires.
func (e *Engine) seedSource() []campaign.GateOption {
	if e.cfg.CampaignSeed.IsZero() {
		return nil
	}
	pinned := e.cfg.CampaignSeed
	return []campaign.GateOption{campaign.WithSeedSource(func() (campaign.Seed, error) { return pinned, nil })}
}

func (e *Engine) campaignIdentity() campaign.Identity {
	return campaign.Identity{Org: e.cfg.Org, WorldNS: e.cfg.WorldNS, Template: e.plan.TemplateID}
}

func (e *Engine) turnIdentity() turn.Identity {
	return turn.Identity{Org: e.cfg.Org, WorldNS: e.cfg.WorldNS, Template: e.plan.TemplateID}
}

// connect opens the broker connection and the stores every later step is built
// on.
func (e *Engine) connect(ctx context.Context) error {
	client, err := natsclient.NewClient(e.cfg.NATSURL, natsclient.WithLogger(e.log()))
	if err != nil {
		return fmt.Errorf("build the NATS client: %w", err)
	}
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect to %s: %w", e.cfg.NATSURL, err)
	}
	e.client = client

	store, err := graphio.NewStore(client)
	if err != nil {
		return err
	}
	e.graph = store

	backend, err := content.NewObjectStore(ctx, client, content.WithBucket(e.cfg.ContentBucket))
	if err != nil {
		return err
	}
	e.backend = backend
	artifacts, err := content.NewStore(backend)
	if err != nil {
		return err
	}
	e.content = artifacts

	recorder, err := turn.NewRecorder(store, artifacts, e.turnIdentity())
	if err != nil {
		return err
	}
	e.recorder = recorder

	gate, err := campaign.NewGate(store, e.campaignIdentity(), e.seedSource()...)
	if err != nil {
		return err
	}
	e.gate = gate

	importer, err := world.NewImporter(client)
	if err != nil {
		return err
	}
	e.importer = importer
	return nil
}

// startGraph runs graph-ingest and graph-index in-process.
//
// graph-ingest is the SOLE ENTITY_STATES writer, and running it here rather than
// writing the bucket directly is what makes the predicate contract, the
// indexing-profile stamp, referential stubs and the newer-wins merge apply to
// this engine's writes at all. graph-index is the reverse-edge projection the
// context assembler's "who is in this scene" question is answered from.
func (e *Engine) startGraph(ctx context.Context) error {
	if err := e.startComponent(ctx, "graph-ingest", graphingest.CreateGraphIngest); err != nil {
		return err
	}
	return e.startComponent(ctx, "graph-index", graphindex.CreateGraphIndex)
}

type factory func(json.RawMessage, component.Dependencies) (component.Discoverable, error)

// startComponent creates, initialises and starts one framework component through
// its production factory and lifecycle.
func (e *Engine) startComponent(ctx context.Context, name string, create factory, cfg ...json.RawMessage) error {
	var raw json.RawMessage
	if len(cfg) == 1 {
		raw = cfg[0]
	}
	created, err := create(raw, e.deps())
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	lifecycle, ok := created.(component.LifecycleComponent)
	if !ok {
		return fmt.Errorf("%s is a %T, not a LifecycleComponent", name, created)
	}
	if err := lifecycle.Initialize(); err != nil {
		return fmt.Errorf("initialize %s: %w", name, err)
	}
	if err := lifecycle.Start(ctx); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	e.components = append(e.components, namedComponent{name: name, component: lifecycle})
	return nil
}

func (e *Engine) deps() component.Dependencies {
	return component.Dependencies{
		NATSClient:       e.client,
		PayloadRegistry:  e.cfg.Registry,
		Logger:           e.log(),
		ModelRegistry:    e.cfg.Models,
		ToolRegistry:     e.tools,
		LifecycleManager: e.lifecycle,
	}
}

// attachCaseLifecycle registers the workflow and adds its initial phase to an
// imported case. Manager.Create is attach-on-existing and returns AlreadyExists
// on restart, so an existing phase is preserved rather than reset.
func (e *Engine) attachCaseLifecycle(ctx context.Context) error {
	e.lifecycle = lifecycle.NewManager(e.client, e.log())
	if err := e.lifecycle.Register(caseflow.Workflow()); err != nil {
		return fmt.Errorf("register case lifecycle: %w", err)
	}
	if e.pkg.Mystery == nil {
		return nil
	}
	var caseID string
	for _, entity := range e.plan.Entities {
		if entity.Kind == vocabulary.EntityKindCase && entity.Template.LocalID == e.pkg.Mystery.ID {
			caseID = entity.ID
			break
		}
	}
	if caseID == "" {
		return fmt.Errorf("attach case lifecycle: resolved plan has no case %q", e.pkg.Mystery.ID)
	}
	initial := &caseflow.CaseState{ID: caseID, CurrentPhase: vocabulary.CasePhaseColdOpen}
	if err := e.lifecycle.Create(ctx, initial); err != nil && !errors.Is(err, lifecycle.ErrAlreadyExists) {
		return fmt.Errorf("attach case lifecycle to %s: %w", caseID, err)
	}
	return nil
}

// ensureAgentStream creates the stream the agentic loop's ports bind.
//
// This engine creates it because this engine runs the loop. Upstream's own
// milestone subscriber declines to create it — "they create the AGENT stream" —
// and that is the right posture for a subscriber; here there is no other "they".
// The configuration is stage.AgentStreamConfig, which states all three eviction
// limits rather than inheriting unlimited, and captures every subject the loop
// binds a consumer on (including tool.result.>, which upstream's subject
// derivation does not produce and without which the loop does not start).
func (e *Engine) ensureAgentStream(ctx context.Context) error {
	stream, err := e.client.EnsureStream(ctx, stage.AgentStreamConfig())
	if err != nil {
		return fmt.Errorf("ensure the %s stream: %w", stage.TaskStream, err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("read the %s stream after declarative reconciliation: %w", stage.TaskStream, err)
	}
	if info.Config.MaxAge != stage.AgentStreamMaxAge || info.Config.Duplicates != info.Config.MaxAge {
		return fmt.Errorf(
			"the %s stream retained tasks for %s but duplicate evidence for %s; both must equal %s",
			stage.TaskStream, info.Config.MaxAge, info.Config.Duplicates, stage.AgentStreamMaxAge)
	}
	return checkStreamCaptures(ctx, e.client, stage.TaskStream, agentStreamSubjects...)
}

// ensureEntityStream creates the fact lane graph-ingest consumes and the
// importer publishes onto.
func (e *Engine) ensureEntityStream(ctx context.Context) error {
	if _, err := e.client.EnsureStream(ctx, world.EntityStreamConfig()); err != nil {
		return fmt.Errorf("ensure the %s stream: %w", world.EntityStream, err)
	}
	return checkStreamCaptures(ctx, e.client, world.EntityStream, world.DefaultImportSubject)
}

// ensureStageStream creates the stage-trigger stream and proves it captures the
// subjects the pack publishes on.
func (e *Engine) ensureStageStream(ctx context.Context) error {
	if _, err := e.client.EnsureStream(ctx, stage.StreamConfig()); err != nil {
		return fmt.Errorf("ensure the %s stream: %w", rulepack.StageStream, err)
	}
	return checkStreamCaptures(ctx, e.client, rulepack.StageStream, stageStreamSubjects...)
}

// ensureActionStream creates the player-action stream with the limits this
// engine requires, before anything binds a consumer on it.
func (e *Engine) ensureActionStream(ctx context.Context) error {
	return turn.EnsureActionStream(ctx, e.client)
}

// startAgenticLoop registers both terminal tools and starts the three components
// that make a persona run at all.
//
// # There are THREE components here, and two of them are easy to miss
//
// The agentic loop neither calls a model nor executes a tool. It is an
// orchestrator that publishes and waits, and each of the two things it waits for
// has its own component on the other end of a subject:
//
//   - deps.ModelRegistry is how the loop RESOLVES which endpoint a capability
//     means; when it needs a completion it publishes an AgentRequest onto
//     `agent.request.*` and waits on `agent.response.>`. The component in between
//     is agentic-model, which owns the HTTP client and the retry curve.
//   - deps.ToolRegistry is how the loop ADVERTISES tools to the model; when the
//     model calls one, the loop publishes the call onto `tool.execute.<name>` and
//     waits for `tool.result.*`. The component in between is agentic-tools, handed
//     the same registry.
//
// Leave out either one and the deployment boots clean, resolves its endpoints,
// advertises its tools, and then parks every turn: the persona waits for an answer
// that cannot arrive and the loop ends on its own timeout. The turn fails as a cap
// exhaustion — a code that describes the symptom and hides the cause — and the
// AGENT stream looks perfectly healthy, because both subjects are captured by
// `agent.>` whether or not anything is consuming them.
//
// Both bridges start BEFORE the loop, so their consumers are bound before
// anything can publish for them.
//
// # The tools are registered before either, and both or neither
//
// A deployment that registered the narrator's exit and not the adjudicator's
// would boot healthy, accept a turn, and wedge on the first adjudication with a
// tool-not-found the model cannot act on. persona.RegisterTools is all-or-nothing
// for that reason.
//
// # Governance stays disabled, and that is a decision rather than a default
//
// Upstream's approval re-dispatch rebuilds a bare tool call and propagates only a
// closed framework metadata list, so a persona tool routed through it loses the
// engine-injected identity — and identity is exactly what this engine refuses to
// ask the model for. Every exit would fail as an internal error. Governance is
// off by default and nothing here turns it on; the mode is stated explicitly so
// enabling it is an edit somebody makes on purpose, next to the reason not to.
func (e *Engine) startAgenticLoop(ctx context.Context) error {
	registry := agentictools.NewExecutorRegistry()
	if err := persona.RegisterTools(registry, e.content, e.graph); err != nil {
		return err
	}
	authority, err := companion.NewAuthority(e.graph)
	if err != nil {
		return err
	}
	scope, err := e.epistemicScope()
	if err != nil {
		return err
	}
	assembler, err := scene.NewAssembler(e.graph)
	if err != nil {
		return err
	}
	projector, err := epistemic.NewProjector(
		assembler, e.graph, scope, epistemic.WithCompanionBondValidator(authority))
	if err != nil {
		return err
	}
	companionExecutor, err := companion.NewExecutor(e.content, e.graph, authority, projector)
	if err != nil {
		return err
	}
	if err := registry.RegisterExecutor(companionExecutor); err != nil {
		return fmt.Errorf("register the %s tool: %w", persona.CompanionDecisionToolName, err)
	}
	e.companionExhaust = companionExecutor
	e.tools = registry

	// The tool executor, bound before anything can publish a call for it.
	//
	// From upstream's defaults, with two positions left EMPTY on purpose and
	// stated so they read as decisions rather than omissions. ApprovalRequired is
	// empty because a persona tool routed through the approval path loses the
	// engine-injected identity (see below), and AllowedTools is empty — which
	// upstream reads as "allow all" — because the per-TASK allowlist is where this
	// engine closes the exit vocabulary: each spawn carries exactly one tool, and
	// a deployment-wide list would be a second, weaker statement of the same rule.
	tools := agentictools.DefaultConfig()
	tools.StreamName = stage.TaskStream
	tools.ApprovalRequired = nil
	tools.AllowedTools = nil
	toolsCfg, err := json.Marshal(tools)
	if err != nil {
		return fmt.Errorf("encode the agentic-tools configuration: %w", err)
	}
	if err := e.startComponent(ctx, "agentic-tools", agentictools.NewComponent, toolsCfg); err != nil {
		return err
	}

	// The model bridge, bound before anything can publish a request for it.
	//
	// Its defaults are upstream's, with only the stream named: the retry curve, the
	// per-request timeout and the rate-limit backoff are provider-facing policy this
	// engine has no better opinion about than the component that owns the client.
	// The endpoint itself is never named here — it is resolved per request from the
	// model registry by the capability the persona spec declares, which is what
	// makes "a live model is a config retarget, never a code change" true.
	models := agenticmodel.DefaultConfig()
	models.StreamName = stage.TaskStream
	modelsCfg, err := json.Marshal(models)
	if err != nil {
		return fmt.Errorf("encode the agentic-model configuration: %w", err)
	}
	if err := e.startComponent(ctx, "agentic-model", agenticmodel.NewComponent, modelsCfg); err != nil {
		return err
	}

	// Built FROM upstream's defaults and overridden field by field, rather than
	// composed from a zero value. A zero Config marshals its nested consumer and
	// context blocks as zeroes, and "the defaults are repaired afterwards" is a
	// property of the current NewComponent rather than a contract — inheriting
	// them makes the overrides below the complete list of decisions this engine
	// is making about the loop.
	loop := agenticloop.DefaultConfig()
	// The component-wide ceiling. Each persona's spec narrows to
	// min(spec, ceiling), so this can only be a bound and never a widening — and
	// it is DERIVED from the specs rather than picked, so a persona asking for
	// more than the engine allows is impossible rather than merely unlikely.
	loop.MaxIterations = maxPersonaIterations()
	loop.Timeout = maxPersonaTimeout().String()
	loop.StreamName = stage.TaskStream
	// Disabled, and stated rather than defaulted. Upstream's approval
	// re-dispatch rebuilds a bare tool call and propagates only a closed
	// framework metadata list, so a persona tool routed through it loses the
	// engine-injected identity — and identity is exactly what this engine refuses
	// to ask the model for. Every exit would fail as an internal error. Writing
	// the mode here puts the reason next to the switch.
	loop.ToolCallGovernance = agenticloop.ToolCallGovernanceConfig{
		Mode: agenticloop.ToolCallGovernanceModeDisabled,
	}
	// Left false deliberately. The synthesised terminal is a `decide` call, and
	// this engine does not register that tool: a synthesised exit would dispatch
	// a tool nothing answers to. The engine's answer to a persona that never
	// exits is the iteration cap plus the loop-failure watcher, which ends the
	// turn with a closed reason instead of inventing one.
	loop.SynthesizeTerminalOnCompletion = false

	cfg, err := json.Marshal(loop)
	if err != nil {
		return fmt.Errorf("encode the agentic-loop configuration: %w", err)
	}
	return e.startComponent(ctx, "agentic-loop", agenticloop.NewComponent, cfg)
}

// maxPersonaIterations is the widest budget any persona declares.
func maxPersonaIterations() int {
	widest := 1
	for _, spec := range persona.Specs() {
		if spec.MaxIterations > widest {
			widest = spec.MaxIterations
		}
	}
	return widest
}

// maxPersonaTimeout is the longest per-call timeout any persona declares, plus
// room for the loop's own bookkeeping around it.
func maxPersonaTimeout() time.Duration {
	longest := time.Minute
	for _, spec := range persona.Specs() {
		if spec.Timeout > longest {
			longest = spec.Timeout
		}
	}
	return longest * time.Duration(maxPersonaIterations()+1)
}

// startRules loads the fixed engine turn-sequencing pack and the selected world
// mechanics into one real rule processor.
//
// The pack is INLINE rather than a file path: a deployment must not be able to
// boot with half a turn loop because somebody forgot to copy a directory, and a
// missing rules file would present as a game that accepts actions and never
// adjudicates them.
func (e *Engine) startRules(ctx context.Context) error {
	raw, err := e.ruleProcessorConfig()
	if err != nil {
		return fmt.Errorf("build the rule processor configuration: %w", err)
	}
	// Cleared BEFORE Start, so the readback that follows asks "did the processor
	// write a status after this boot deleted the old one?" — a question whose
	// answer no earlier process and no clock can supply. Comparing timestamps
	// instead reads correctly and rests on wall clocks either side of a restart;
	// see checkRuleProcessorStarted.
	if err := e.clearRuleProcessorStatus(ctx); err != nil {
		return err
	}
	return e.startComponent(ctx, "rule-processor", rule.CreateRuleProcessor, raw)
}

func (e *Engine) startKnowledge(ctx context.Context) error {
	scope, err := e.epistemicScope()
	if err != nil {
		return err
	}
	loader, err := knowledge.NewLoader(e.graph, e.content, scope.CaseID())
	if err != nil {
		return err
	}
	authority, err := companion.NewAuthority(e.graph)
	if err != nil {
		return err
	}
	granter, err := knowledge.NewGranter(e.graph, e.content)
	if err != nil {
		return err
	}
	consumer, err := knowledge.NewConsumer(e.client, loader, granter, e.recorder, authority, authority)
	if err != nil {
		return err
	}
	return consumer.Start(ctx)
}

func (e *Engine) startAccusation(ctx context.Context) error {
	scope, err := e.epistemicScope()
	if err != nil {
		return err
	}
	assembler, err := scene.NewAssembler(e.graph)
	if err != nil {
		return err
	}
	projector, err := epistemic.NewProjector(assembler, e.graph, scope)
	if err != nil {
		return err
	}
	loader, err := accusation.NewLoader(e.graph, e.content, projector, scope.CaseID())
	if err != nil {
		return err
	}
	committer, err := accusation.NewCommitter(e.graph, e.content)
	if err != nil {
		return err
	}
	lifecycleRecorder, err := caseflow.NewRecorder(e.graph)
	if err != nil {
		return err
	}
	consumer, err := accusation.NewConsumer(e.client, loader, committer, lifecycleRecorder, e.recorder)
	if err != nil {
		return err
	}
	return consumer.Start(ctx)
}

func (e *Engine) startCaseProgress(ctx context.Context) error {
	lifecycleRecorder, err := caseflow.NewRecorder(e.graph)
	if err != nil {
		return err
	}
	progressor, err := caseflow.NewProgressor(e.graph, e.content, lifecycleRecorder)
	if err != nil {
		return err
	}
	consumer, err := caseflow.NewProgressConsumer(e.client, progressor, e.recorder, e.turnIdentity())
	if err != nil {
		return err
	}
	return consumer.Start(ctx)
}

// checkResumePreconditions is every external fact the stranded-turn pass needs
// that this boot can actually read.
func (e *Engine) checkResumePreconditions(ctx context.Context) error {
	if err := checkStreamCaptures(ctx, e.client, rulepack.StageStream, stageStreamSubjects...); err != nil {
		return err
	}
	if err := checkStreamCaptures(ctx, e.client, stage.TaskStream, agentStreamSubjects...); err != nil {
		return err
	}
	return e.checkRuleProcessorStarted(ctx)
}

// reconcileStrandedTurns runs the boot-time pass that puts parked turns back in
// motion.
//
// A per-turn failure is reported and does not stop the boot: the pass collects
// them precisely so one corrupt turn record does not leave every turn behind it
// waiting. A failure to READ the queues is different and does stop the boot,
// which is why the error is returned rather than logged.
func (e *Engine) reconcileStrandedTurns(ctx context.Context) error {
	stages, err := e.client.GetStream(ctx, rulepack.StageStream)
	if err != nil {
		return fmt.Errorf("read the %s stream: %w", rulepack.StageStream, err)
	}
	agent, err := e.client.GetStream(ctx, persona.TaskStream)
	if err != nil {
		return fmt.Errorf("read the %s stream: %w", persona.TaskStream, err)
	}
	queues, err := resume.NewWorkQueues(stages, agent)
	if err != nil {
		return err
	}
	reconciler, err := resume.NewReconciler(
		e.graph, e.client, e.recorder, e.content, queues, e.gate.CampaignID(),
		resume.WithLogger(e.log()))
	if err != nil {
		return err
	}

	report, err := reconciler.Reconcile(ctx)
	e.log().Info("stranded-turn pass complete",
		"scanned", report.Scanned, "unborn", report.Unborn, "resolved", report.Resolved,
		"queued", report.Queued, "retriggered", report.StageRetriggered,
		"unadvanceable", report.Unadvanceable, "abandoned", report.Abandoned)
	if err != nil {
		// Per-turn failures, already recorded on the turns themselves. Loud, and
		// not fatal: refusing to boot would leave every OTHER parked turn waiting
		// for the same reason this pass exists to end.
		e.log().Error("the stranded-turn pass could not resolve every turn it found", "error", err)
	}
	return nil
}

// checkStagePreconditions is everything that must hold before a stage can spawn a
// persona: the world is whole, and the two lanes the agentic loop waits on have
// somebody on the other end.
func (e *Engine) checkStagePreconditions(ctx context.Context) error {
	if err := e.checkImportMarked(ctx); err != nil {
		return err
	}
	return e.checkAgenticBridges(ctx)
}

// startStages binds every executable stage of the turn loop.
func (e *Engine) startStages(ctx context.Context) error {
	assembler, err := scene.NewAssembler(e.graph)
	if err != nil {
		return err
	}
	scope, err := e.epistemicScope()
	if err != nil {
		return err
	}
	denouement, err := accusation.NewDenouementAuthorizer(e.graph, e.content)
	if err != nil {
		return err
	}
	authority, err := companion.NewAuthority(e.graph)
	if err != nil {
		return err
	}
	narrationEvidence, err := companion.NewNarrationResolver(e.graph, e.content, authority)
	if err != nil {
		return err
	}
	projector, err := epistemic.NewProjector(assembler, e.graph, scope,
		epistemic.WithDenouementAuthorizer(denouement),
		epistemic.WithCompanionBondValidator(authority),
		epistemic.WithNarrationEvidenceResolver(narrationEvidence))
	if err != nil {
		return err
	}
	var stageProjector stage.ContextProjector = projector
	if scope.CaseID() != "" {
		router, routerErr := accusation.NewNarrationRouter(e.graph, e.content, projector, scope.CaseID())
		if routerErr != nil {
			return routerErr
		}
		stageProjector = router
	}
	guard, err := persona.NewGuard(e.graph)
	if err != nil {
		return err
	}
	builder, err := persona.NewBuilder(e.content)
	if err != nil {
		return err
	}

	adjudicator, err := stage.NewSpawner(
		persona.Adjudicator(), e.recorder, guard, stageProjector, builder, e.client,
		stage.WithSpawnerLogger(e.log()))
	if err != nil {
		return err
	}
	casekeeper, err := stage.NewSpawner(
		persona.Casekeeper(), e.recorder, guard, stageProjector, builder, e.client,
		stage.WithSpawnerLogger(e.log()),
		stage.WithCaseInterpretation(scope, e.content, e.graph))
	if err != nil {
		return err
	}
	narrator, err := stage.NewSpawner(
		persona.Narrator(), e.recorder, guard, stageProjector, builder, e.client,
		stage.WithSpawnerLogger(e.log()))
	if err != nil {
		return err
	}
	companionStage, err := stage.NewCompanionStage(
		e.recorder, e.graph, e.content, authority, projector, builder, e.client)
	if err != nil {
		return err
	}

	roller, err := dice.NewRoller(vocabulary.Mechanic2d6PbtaV1)
	if err != nil {
		return err
	}
	resolver, err := dice.NewResolver(roller, e.graph, e.instantiation)
	if err != nil {
		return err
	}
	diceStage, err := stage.NewResolver(
		e.recorder, e.graph, e.content, e.content, e.graph, resolver,
		stage.WithResolverLogger(e.log()))
	if err != nil {
		return err
	}

	applier, err := effect.NewApplier(e.graph)
	if err != nil {
		return err
	}
	effector, err := stage.NewEffector(
		e.recorder, e.recorder, e.graph, e.content, e.content, e.content, applier,
		stage.WithEffectorLogger(e.log()))
	if err != nil {
		return err
	}
	completer, err := stage.NewCompleter(e.recorder)
	if err != nil {
		return err
	}

	runner, err := stage.NewRunner(e.client, e.client,
		[]stage.Stage{casekeeper, adjudicator, diceStage, effector, companionStage, narrator, completer},
		stage.WithLogger(e.log()))
	if err != nil {
		return err
	}
	return runner.Start(ctx)
}

func (e *Engine) epistemicScope() (epistemic.Scope, error) {
	if e.plan == nil || e.pkg == nil {
		return epistemic.Scope{}, errors.New("build epistemic scope: resolved world plan is unavailable")
	}
	if e.pkg.Mystery == nil {
		return epistemic.NewScope("", nil)
	}

	byLocalID := make(map[string]world.PlannedEntity, len(e.plan.Entities))
	for _, entity := range e.plan.Entities {
		byLocalID[entity.Template.LocalID] = entity
	}
	caseEntity, ok := byLocalID[e.pkg.Mystery.ID]
	if !ok || caseEntity.Kind != vocabulary.EntityKindCase {
		return epistemic.Scope{}, fmt.Errorf("build epistemic scope: resolved plan has no case %q",
			e.pkg.Mystery.ID)
	}

	beliefsByActorID := make(map[string][]string)
	for _, belief := range e.pkg.Mystery.Beliefs {
		record, recordOK := byLocalID[belief.Record]
		actor, actorOK := byLocalID[belief.Actor]
		if !recordOK || record.Kind != vocabulary.EntityKindBelief {
			return epistemic.Scope{}, fmt.Errorf("build epistemic scope: resolved plan has no belief %q",
				belief.Record)
		}
		if !actorOK || actor.Kind != vocabulary.EntityKindCharacter {
			return epistemic.Scope{}, fmt.Errorf("build epistemic scope: resolved plan has no belief actor %q",
				belief.Actor)
		}
		beliefsByActorID[actor.ID] = append(beliefsByActorID[actor.ID], record.ID)
	}
	return epistemic.NewScope(caseEntity.ID, beliefsByActorID)
}

// startLoopFailures binds the durable watcher and waits until every failure
// queued before this boot is acknowledged. Keeping this as its own sequence
// step makes the ordering relative to stage consumers explicit and testable.
func (e *Engine) startLoopFailures(ctx context.Context) error {
	watcher, err := stage.NewLoopFailureWatcher(
		e.client, e.client, message.NewDecoder(e.cfg.Registry), e.recorder, e.content,
		stage.WithLoopFailureLogger(e.log()), stage.WithCompanionExhauster(e.companionExhaust))
	if err != nil {
		return err
	}
	if err := watcher.Start(ctx); err != nil {
		return err
	}
	return watcher.CatchUp(ctx)
}

// startEgress binds the push half of player delivery.
func (e *Engine) startEgress(ctx context.Context) error {
	results, err := egress.NewResults(e.graph, e.content, e.turnIdentity(), e.gate.CampaignID())
	if err != nil {
		return err
	}
	e.results = results
	if err := e.buildGateway(); err != nil {
		return err
	}
	router, err := egress.NewRouter(e.gatewaySv, playersocket.NewSink(), egress.WithRouterLogger(e.log()))
	if err != nil {
		return err
	}
	notifier, err := egress.NewNotifier(results, router, e.client, egress.WithNotifierLogger(e.log()))
	if err != nil {
		return err
	}
	return notifier.Start(ctx)
}

// buildGateway constructs the session gateway once, whichever step needs it
// first.
//
// Egress needs it as a session DIRECTORY and ingress needs it as a submission
// surface, and they must be the same object: the session table is the only
// registry, and two of them would let a connection be present as a delivery
// target and absent as an identity — a socket that receives a player's fiction
// without being able to submit as them.
func (e *Engine) buildGateway() error {
	if e.gatewaySv != nil {
		return nil
	}
	roster, err := gateway.NewRoster(map[string]string{e.cfg.Player.Credential: e.playerID})
	if err != nil {
		return err
	}
	gw, err := gateway.New(roster, e.graph, e.client, gateway.Config{
		CampaignID: e.gate.CampaignID(),
		SceneID:    e.sceneID,
	}, gateway.WithLogger(e.log()))
	if err != nil {
		return err
	}
	e.gatewaySv = gw
	return nil
}

// PlayerConnectionCount reports how many live WebSocket sessions the configured
// player currently owns. It is operational state, not durable player identity;
// callers use it to synchronize shutdown and reconnect boundaries.
func (e *Engine) PlayerConnectionCount() int {
	if e.gatewaySv == nil || e.playerID == "" {
		return 0
	}
	return len(e.gatewaySv.SessionsFor(e.playerID))
}

// startLedger creates the archive, reconciles it against ENTITY_STATES, and
// binds the resolved-turn consumer.
func (e *Engine) startLedger(ctx context.Context) error {
	writer, err := ledger.NewWriter(
		e.graph, e.client, e.client, e.client, message.NewDecoder(e.cfg.Registry),
		e.gate.CampaignID(), ledger.WithLogger(e.log()))
	if err != nil {
		return err
	}
	return writer.Start(ctx)
}

// startIntake binds the player-action consumer: the moment a submitted action
// becomes a turn.
func (e *Engine) startIntake(ctx context.Context) error {
	intake, err := turn.NewIntake(e.client, e.recorder, message.NewDecoder(e.cfg.Registry))
	if err != nil {
		return err
	}
	return intake.Start(ctx)
}

// startIngress opens the player socket — the last step, because it is the only
// one that lets a stranger add work.
func (e *Engine) startIngress(ctx context.Context) error {
	if err := e.buildGateway(); err != nil {
		return err
	}
	if err := e.gatewaySv.Start(ctx, e.client); err != nil {
		return err
	}
	server, err := playersocket.NewServer(e.gatewaySv, e.cfg.Socket,
		playersocket.WithLogger(e.log()), playersocket.WithResultRetriever(e.results))
	if err != nil {
		return err
	}
	listener, err := server.Listen()
	if err != nil {
		return err
	}
	e.listener = listener

	go func() {
		err := server.Serve(ctx, listener)
		select {
		case e.served <- err:
		default:
		}
	}()
	e.log().Info("the player socket is open", "address", listener.Addr().String(), "path", e.cfg.Socket.Path)
	return nil
}

// decodePersona reads one world persona record as the upstream type the agentic
// loop will load it as.
//
// Unknown fields are refused, matching the world loader's own gate: an author who
// writes `model:` or `max_iterations:` is told immediately that those are
// deployment configuration and not world content, rather than having them
// silently dropped.
func decodePersona(data []byte) (*sspersona.Persona, error) {
	return world.DecodePersonaRecord(data)
}
