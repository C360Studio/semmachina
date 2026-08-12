package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/graph/readiness"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/payloadregistry"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	ruleProcessorComponent = "rule-processor"
	ruleStatusWait         = 20 * time.Second
)

// checkPayloadRegistry proves the binary's bootstrap actually registered this
// engine's payloads.
//
// It is a CHECK rather than a trust because the failure it catches is silent in
// the worst possible place: a decoder built over a registry missing
// payload.PlayerAction consumes every player action, decodes none of them, and
// accepts no turns — with no error anywhere, because "this is not a player
// action" is an ordinary answer for a decoder. The same registry is what the
// ledger's duplicate guard reads a manifest back through, so a binary that
// forgot would also archive every redelivered turn twice.
//
// It round-trips one payload rather than asking the registry what it holds,
// because that is the operation every consumer actually performs.
func checkPayloadRegistry(registry *payloadregistry.Registry) error {
	if registry == nil {
		return errors.New("no payload registry")
	}
	probe := &payload.PlayerAction{
		ActionID:   "boot-probe",
		PlayerID:   "c360.semmachina.boot.probe.player.one",
		CampaignID: "c360.semmachina.boot.probe.campaign.main",
		SceneID:    "c360.semmachina.boot.probe.scene.one",
		Text:       "a registry probe",
		ArrivedAt:  time.Unix(0, 0).UTC(),
		Channel:    payload.ChannelBinding{Adapter: "websocket", ReplyTo: "boot-probe"},
	}
	wire, err := json.Marshal(message.NewBaseMessage(probe.Schema(), probe, "boot"))
	if err != nil {
		return fmt.Errorf("encode the registry probe: %w", err)
	}
	decoded, err := message.NewDecoder(registry).Decode(wire)
	if err != nil {
		return fmt.Errorf(
			"the payload registry cannot decode this engine's own player action: %w; call "+
				"payload.RegisterPayloads at the binary's bootstrap, or intake will consume every action and "+
				"accept no turns", err)
	}
	if _, ok := decoded.Payload().(*payload.PlayerAction); !ok {
		return fmt.Errorf(
			"the payload registry decoded this engine's player action as a %T; something else is registered "+
				"under that schema", decoded.Payload())
	}
	return nil
}

// checkStreamCaptures proves a stream exists AND captures the subject the thing
// about to bind it will use.
//
// Both halves, because a JetStream publish is a core publish underneath: a
// missing stream, or one whose subjects are narrower than they should be, can
// look like it worked while the message reaches no durable consumer at all. The
// version of this failure that costs turns is the quiet one — a stage trigger
// published into nothing looks exactly like a stage that ran and did nothing.
func checkStreamCaptures(ctx context.Context, streams streamReader, name string, subjects ...string) error {
	stream, err := streams.GetStream(ctx, name)
	if err != nil {
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			return fmt.Errorf(
				"the %s stream does not exist; a JetStream publish into a subject no stream captures is a CORE "+
					"publish that reaches no durable consumer and is reported as a success: %w", name, err)
		}
		return fmt.Errorf("read the %s stream: %w", name, err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("read the %s stream's configuration: %w", name, err)
	}
	for _, subject := range subjects {
		if !subjectCaptured(subject, info.Config.Subjects) {
			return fmt.Errorf(
				"the %s stream exists but its subjects %v do not capture %q; both halves of that are SILENT — a "+
					"publish onto an uncaptured subject reaches no consumer, and a consumer filtered on one is "+
					"ACCEPTED by the server (measured) and simply never delivered anything",
				name, info.Config.Subjects, subject)
		}
	}
	return nil
}

// checkRuleProcessorStarted closes the ordering edge between rule bootstrap and
// stranded-turn recovery. ComponentManager state proves this process started the
// admitted generation; the fresh GRAPH_STATUS/rule envelope proves its current
// entity replay completed. Missing, stale, building, degraded, and reset-required
// states all fail closed. The readiness key is never deleted: it is framework
// operational state with heartbeat-based freshness semantics.
func (e *Engine) checkRuleProcessorStarted(ctx context.Context) error {
	if e.ruleManager == nil || !e.ruleManager.IsStarted() {
		return errors.New("rule processor component manager has not started")
	}
	managed := e.ruleManager.GetManagedComponents()[ruleProcessorComponent]
	if managed == nil || managed.State != component.StateStarted {
		return fmt.Errorf("rule component manager reports %q is not started", ruleProcessorComponent)
	}

	waitCtx, cancel := context.WithTimeout(ctx, ruleStatusWait)
	defer cancel()
	return awaitRuleReadiness(waitCtx, e.ruleStatusBaseline, func() ruleReadinessGeneration {
		return e.readRuleReadinessGeneration(waitCtx)
	},
		readinessWindow{timeout: ruleStatusWait, poll: e.cfg.ReadyPoll, sleep: e.sleep})
}

type ruleReadinessGeneration struct {
	revision uint64
	reading  readiness.Reading
}

func (e *Engine) captureRuleStatusBaseline(ctx context.Context) error {
	e.ruleStatusBaseline = 0
	bucket, err := e.client.GetKeyValueBucket(ctx, readiness.BucketGraphStatus)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("capture %s/%s generation: %w", readiness.BucketGraphStatus, readiness.KeyRule, err)
	}
	entry, err := bucket.Get(ctx, readiness.KeyRule)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("capture %s/%s generation: %w", readiness.BucketGraphStatus, readiness.KeyRule, err)
	}
	e.ruleStatusBaseline = entry.Revision()
	return nil
}

func (e *Engine) readRuleReadinessGeneration(ctx context.Context) ruleReadinessGeneration {
	bucket, err := e.client.GetKeyValueBucket(ctx, readiness.BucketGraphStatus)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return ruleReadinessGeneration{}
	}
	if err != nil {
		return ruleReadinessGeneration{reading: readiness.Reading{Err: err}}
	}
	entry, err := bucket.Get(ctx, readiness.KeyRule)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return ruleReadinessGeneration{}
	}
	if err != nil {
		return ruleReadinessGeneration{reading: readiness.Reading{Err: err}}
	}
	var status graph.IndexStatusResponse
	if err := json.Unmarshal(entry.Value(), &status); err != nil {
		return ruleReadinessGeneration{revision: entry.Revision(), reading: readiness.Reading{
			Known: true, Err: fmt.Errorf("decode %s/%s: %w", readiness.BucketGraphStatus, readiness.KeyRule, err),
		}}
	}
	age := time.Since(entry.Created())
	return ruleReadinessGeneration{revision: entry.Revision(), reading: readiness.Reading{
		Known: true, Fresh: age <= readiness.FreshnessWindow(readiness.DefaultHeartbeat), Age: age, Status: status,
	}}
}

func awaitRuleReadiness(
	ctx context.Context,
	baseline uint64,
	read func() ruleReadinessGeneration,
	w readinessWindow,
) error {
	deadline := time.Now().Add(w.timeout)
	var last string
	for {
		generation := read()
		reading := generation.reading
		switch {
		case generation.revision <= baseline:
			last = fmt.Sprintf("status generation %d has not advanced past pre-activation generation %d",
				generation.revision, baseline)
		case !reading.Known:
			last = "status unknown"
		case reading.Err != nil:
			last = reading.Err.Error()
		case !reading.Fresh:
			last = fmt.Sprintf("status stale (age %s)", reading.Age)
		case reading.Status.State != graph.IndexStateReady:
			last = fmt.Sprintf("state %q", reading.Status.State)
		case !reading.Status.Ready:
			last = "ready is false"
		case !reading.Status.BootstrapComplete:
			last = "bootstrap is incomplete"
		default:
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rule readiness did not become fresh, ready, and bootstrap-complete within %s (%s)",
				w.timeout, last)
		}
		if err := w.sleep(ctx, w.poll); err != nil {
			return err
		}
	}
}

// checkImportMarked re-reads the completion marker from the graph immediately
// before a step that would let play begin.
//
// It gates INGRESS, not just the importer, and the two halves are separate steps
// on purpose: no PLAYER_ACTIONS message may be consumed and no persona may run
// until the marker is observed. A partial-world boot that accepted an action
// would create a turn against a world it is about to re-import, and the
// re-import would clobber the state that play created.
//
// A re-read rather than a remembered boolean, because the value of a gate is
// what it reads. The instantiation step already knows the answer; this is the
// step that is about to act on it, and it asks the graph.
func (e *Engine) checkImportMarked(ctx context.Context) error {
	at, marked, err := e.gate.ImportCompletion(ctx)
	if err != nil {
		return fmt.Errorf("re-read the import-completion marker: %w", err)
	}
	if !marked {
		return fmt.Errorf(
			"campaign %s carries no %s; this boot will not open a path into a world whose import is unproven",
			e.gate.CampaignID(), vocabulary.CampaignImportCompleted)
	}
	e.log().Debug("import marker observed", "campaign", e.gate.CampaignID(), "imported_at", at)
	return nil
}

// checkPersonaModels proves the deployment can actually run both personas.
//
// F20's failure is the one it exists for, and it is silent: upstream's
// Registry.Resolve falls back to the DEFAULT model for a capability it does not
// know, so a deployment that forgot `fiction_adjudication` runs the adjudicator
// on whatever the default happens to be and reports nothing. That is exactly the
// schema-bearing-persona-on-the-small-slot configuration ADR-026 warns about,
// arrived at through a fallback rather than a decision — which is worse, because
// a decision leaves a trace.
func (e *Engine) checkPersonaModels(context.Context) error {
	return persona.CheckRegistryAll(e.cfg.Models)
}

// subjectCaptured reports whether a stream's subject patterns capture everything
// a subject can match.
func subjectCaptured(want string, subjects []string) bool {
	wantTokens := strings.Split(want, ".")
	for _, subject := range subjects {
		if patternCovers(strings.Split(subject, "."), wantTokens) {
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

// The subjects each stream must capture, named where the check reads them so a
// reader can see what the gate is actually asserting.
var (
	stageStreamSubjects = []string{rulepack.StageSubjectFilter}
	// The task lane and the failure lane are named EXPLICITLY on top of the
	// stream's own declared subjects, so this gate asserts what the engine has to
	// be able to reach rather than restating AgentStreamConfig back to itself. If
	// AgentStreamSubjects ever narrows, these two still have to hold.
	agentStreamSubjects = append(persona.AgentStreamSubjects(),
		stage.LoopFailedSubject, persona.TaskSubjectFilter,
		// The two lanes the loop WAITS on. Both are inside `agent.>` and
		// `tool.execute.>`, so naming them here changes nothing about the stream —
		// it states what the engine has to be able to reach, so a narrowing
		// upstream is a refusal rather than a turn that parks.
		persona.ModelRequestSubjectFilter, persona.ModelResponseSubjectFilter)

	// The bridges the agentic loop cannot run without, named by the subject each
	// one consumes.
	//
	// The loop publishes and waits; these are the two components on the other end.
	// A composition missing either boots clean, resolves its endpoints, advertises
	// its tools, and parks every turn in the phase where the persona ran — because
	// a subject nobody consumes and a subject nobody publishes on look identical
	// from the stream, and the loop's own timeout reports the stall as a cap
	// exhaustion.
	agenticBridges = []struct {
		filter string
		what   string
	}{
		{
			filter: persona.ModelRequestSubjectFilter,
			what: "agentic-model, which owns the HTTP client: without it every persona publishes a model " +
				"request nobody answers and the turn parks in the phase that spawned it",
		},
		{
			filter: persona.ToolExecuteSubjectFilter,
			what: "agentic-tools, which executes the terminal tools: without it every persona's exit is " +
				"published into silence and the loop burns its whole iteration budget",
		},
	}
)

// checkAgenticBridges proves something is actually consuming the two lanes the
// agentic loop waits on.
//
// It is a CONSUMER check rather than a stream check, and that distinction is the
// whole value of it. `agent.request.*` and `tool.execute.>` are captured by the
// AGENT stream whether or not anything reads them, so every stream-level assertion
// this engine makes passes on a composition that starts the loop alone. The lanes
// are only reachable if somebody bound a consumer on them, and that is what this
// asks.
//
// Found end to end rather than by reading: a composition running the loop and the
// tool executor but NOT the model bridge parked every turn in `adjudicating` with
// no error anywhere, until the loop's timeout ended it as a cap exhaustion.
func (e *Engine) checkAgenticBridges(ctx context.Context) error {
	stream, err := e.client.GetStream(ctx, persona.TaskStream)
	if err != nil {
		return fmt.Errorf("read the %s stream: %w", persona.TaskStream, err)
	}
	bound := map[string]bool{}
	lister := stream.ListConsumers(ctx)
	for info := range lister.Info() {
		if info == nil {
			continue
		}
		for _, subject := range append([]string{info.Config.FilterSubject}, info.Config.FilterSubjects...) {
			if subject != "" {
				bound[subject] = true
			}
		}
	}
	if err := lister.Err(); err != nil {
		return fmt.Errorf("list the consumers on %s: %w", persona.TaskStream, err)
	}

	for _, bridge := range agenticBridges {
		if !bound[bridge.filter] {
			return fmt.Errorf(
				"nothing consumes %q on the %s stream. That lane is %s. The subject IS captured by the stream, "+
					"so no stream-level check can see this: a persona would publish onto it, wait, and end the "+
					"turn on the loop's timeout as a cap exhaustion",
				bridge.filter, persona.TaskStream, bridge.what)
		}
	}
	return nil
}
