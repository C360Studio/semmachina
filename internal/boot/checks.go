package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/payloadregistry"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The rule processor's own observability coordinates.
//
// Restated here because upstream exports neither, and the alternative to
// restating them is the thing this check exists to replace: a comment recording
// an obligation. TestRuleProcessorStatus_IsWrittenByTheRealProcessor pins both
// against a real processor, so a rename upstream fails a test rather than a boot.
const (
	// componentStatusBucket is where every semstreams component reports its
	// lifecycle stage (component.KVLifecycleReporter writes it, keyed by the
	// component's own name).
	componentStatusBucket = "COMPONENT_STATUS"
	// ruleProcessorComponent is the rule processor's metadata name, which is the
	// KEY it reports under.
	ruleProcessorComponent = "rule-processor"
	// ruleStatusWait bounds the readback below. The processor writes its status
	// inside Start, so a healthy deployment answers on the first read; the window
	// exists so a write that becomes visible a beat later is a pause rather than
	// a refused boot.
	ruleStatusWait = 10 * time.Second
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

// checkRuleProcessorStarted is the CHECKABLE half of the one ordering
// constraint the stranded-turn pass warns about hardest.
//
// # What the pass needs, and why the obvious probe is worthless
//
// resume.Reconcile reads the set of work the substrate is still holding and acts
// on what is missing from it. The rule processor's bootstrap replay publishes
// INTO that set, so a pass that ran before the processor was up would read an
// empty set for a turn about to receive a trigger — and would then either
// re-trigger it (a duplicate, and on a persona hop a second billed spawn) or END
// it, which is terminal and declines the replayed trigger when it lands.
//
// The obvious probe does not work, and this was MEASURED rather than assumed:
// the processor's Health() reports Healthy=true from CONSTRUCTION
// (processor/rule/processor.go initialises the cached HealthStatus with
// Healthy:true and only re-sets it inside run()), so a health check answers
// "healthy" for a processor that has never started. That is exactly the
// "indistinguishable from a quiet one" trap, and a boot that leaned on it would
// have a check that always passes.
//
// # What IS observable, and why it is EXISTENCE rather than freshness
//
// Start's last act is a lifecycle report: the processor writes its status into
// the COMPONENT_STATUS KV bucket under its own component name
// (component.KVLifecycleReporter.ReportStage → Put). The key is attributable.
// The trouble is that it SURVIVES A RESTART, so its mere presence is satisfied on
// every boot after the first by the previous boot's write.
//
// The first version of this check compared timestamps: the reported
// StageStartedAt had to be at or after the instant this boot called Start. That
// reads correctly and rests on the one input a process does not control. The
// stamp round-trips through JSON, which strips the monotonic reading, so the
// comparison is WALL CLOCK on both sides — and a backwards clock step (an NTP
// correction, a VM snapshot restore, a container host resync) lets a previous
// boot's status satisfy it. On its own that is harmless, because the sequence has
// already returned from Start by then. It bites in exactly one combination:
// broken lifecycle reporting AND a backwards step, which is precisely where this
// check is supposed to refuse.
//
// So the key is DELETED before the processor starts and the check waits for it to
// REAPPEAR. Existence-after-deletion is a per-boot fact no clock can forge, and
// it collapses the comparison into a mechanism — the same move this codebase
// makes for connection ids, where reuse is unrepresentable rather than avoided.
// The only clock left is the local poll deadline, which bounds a wait rather than
// deciding a fact.
//
// One assumption it does rest on, stated because it is the deployment's rather
// than the code's: instance-per-world. Two processes serving one world could have
// the other's processor rewrite the key after this one deleted it. That is the
// same assumption internal/resume's whole pass rests on, and it fails the same
// way — the day it stops holding, this check needs a per-process identity in the
// key before it needs anything else.
//
// It does NOT prove the replay has DRAINED. Nothing upstream exposes that, and
// the pass covers it itself by waiting for the work queues to stop moving.
// This closes the half that could otherwise only be a comment.
//
// # Why the reported STAGE is required to be non-empty and no more
//
// Requiring the exact value `idle` looks free and is not. The processor writes
// exactly four stages and has NO path back to idle — there is no
// ReportCycleComplete call anywhere in processor/rule, so `evaluating` is sticky
// once written. Its entity watcher is already delivering before Start returns
// (that is what rp.ready signals) and the reporter's throttle only suppresses for
// a second, so a busy broker can leave the key reading `evaluating` by the time
// this check looks. Pinning the value would hang the boot in that window, which
// trades a hypothetical (`stopping`, a stage this component never writes) for a
// stall. Non-empty rejects the shape that can actually occur — a zero-valued or
// malformed status decoding to no stage at all — and costs nothing.
//
// # It fails CLOSED, deliberately
//
// Upstream's lifecycle reporting is best-effort: a COMPONENT_STATUS bucket it
// cannot create downgrades to a no-op reporter with a warning. On such a
// deployment the key never reappears and this check refuses the boot rather than
// running the pass blind. Running the pass against a processor nobody can confirm
// started is the failure that ends live turns; refusing to boot is the failure an
// operator reads about.
func (e *Engine) checkRuleProcessorStarted(ctx context.Context) error {
	if !e.ruleStatusCleared {
		return errors.New(
			"this boot never cleared the rule processor's lifecycle status, so it cannot tell a status the " +
				"processor wrote from one an earlier process left behind; the processor has not been started by " +
				"this boot at all")
	}
	bucket, err := e.client.GetKeyValueBucket(ctx, componentStatusBucket)
	if err != nil {
		return fmt.Errorf(
			"the %s bucket is unreadable, so this boot cannot confirm the rule processor started: %w. The "+
				"stranded-turn pass must not run against an unstarted processor — it would read an empty work "+
				"set for turns about to receive a trigger and END them, and `failed` is terminal",
			componentStatusBucket, err)
	}

	return awaitReportedStatus(ctx,
		func(ctx context.Context) ([]byte, error) {
			entry, getErr := bucket.Get(ctx, ruleProcessorComponent)
			if getErr != nil {
				return nil, getErr
			}
			return entry.Value(), nil
		},
		readinessWindow{timeout: ruleStatusWait, poll: e.cfg.ReadyPoll, sleep: e.sleep})
}

// clearRuleProcessorStatus deletes the rule processor's lifecycle key so that its
// REAPPEARANCE is a fact about this boot.
//
// Called immediately before the processor is started, and its success is what
// checkRuleProcessorStarted requires: a boot that could not clear the key has no
// way to tell the processor's own write from an earlier process's leftover, so it
// refuses rather than reading one as the other.
//
// An ABSENT bucket is a cleared key, not a failure. The bucket is created by
// whichever component reports first — the processor itself, inside Start — so
// finding none means there is no leftover to forge with, which is the same
// guarantee the delete provides.
func (e *Engine) clearRuleProcessorStatus(ctx context.Context) error {
	bucket, err := e.client.GetKeyValueBucket(ctx, componentStatusBucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			e.ruleStatusCleared = true
			return nil
		}
		return fmt.Errorf(
			"read the %s bucket to clear the rule processor's stale lifecycle status: %w", componentStatusBucket, err)
	}
	if err := bucket.Delete(ctx, ruleProcessorComponent); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return fmt.Errorf(
			"delete the rule processor's stale lifecycle status from %s: %w; without the delete this boot cannot "+
				"tell a status the processor wrote from one an earlier process left, and the stranded-turn pass "+
				"would run against a processor nobody confirmed started",
			componentStatusBucket, err)
	}
	e.ruleStatusCleared = true
	return nil
}

// awaitReportedStatus waits for a component's lifecycle status to EXIST and to
// carry a stage.
//
// Existence is the whole predicate, and it is only meaningful because the caller
// deleted the key first: a check that merely found a status would be satisfied by
// every earlier boot's leftover. See checkRuleProcessorStarted for why that is
// preferred to comparing timestamps across a restart boundary.
//
// The stage must be non-empty. That rejects a zero-valued or malformed status —
// the shape that can actually occur — without pinning a value the component may
// legitimately have moved past by the time this reads it.
func awaitReportedStatus(
	ctx context.Context,
	read func(context.Context) ([]byte, error),
	w readinessWindow,
) error {
	deadline := time.Now().Add(w.timeout)
	var last string
	for {
		value, readErr := read(ctx)
		switch {
		case readErr != nil:
			last = readErr.Error()
		default:
			var status component.Status
			if err := json.Unmarshal(value, &status); err != nil {
				last = "status is undecodable: " + err.Error()
				break
			}
			if status.Stage != "" {
				return nil
			}
			last = "a status reappeared carrying no stage at all, which is not a report this component writes"
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"the rule processor's lifecycle status did not reappear within %s after this boot deleted it (%s). "+
					"It writes one at the end of Start, keyed %q in %s, so this is either a processor that did "+
					"not start or a deployment whose lifecycle reporting is disabled — and this boot refuses to "+
					"run the stranded-turn pass against a processor it cannot confirm, because an unstarted "+
					"processor is indistinguishable from a quiet one and the pass would end live turns",
				w.timeout, last, ruleProcessorComponent, componentStatusBucket)
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
