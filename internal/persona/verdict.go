package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnWriter is the graph surface a terminal tool needs.
//
// One method, and it is the MERGE lane. There is no seam here for triple.add or
// .add_batch: every predicate a persona lands is single-valued, and a duplicate
// exit committed through an appending lane would leave a turn holding two
// plausibilities or two narration references, with a success response and no
// error anywhere (F14). Upstream's own decide tool writes through the appending
// lane, which is correct for the loop entity it accumulates onto and wrong for
// ours.
//
// It is also write-ONLY. A terminal tool that could read the world is a terminal
// tool that could be asked to check its own work, and the executor's job is to
// record a judgment, not to re-open it.
type TurnWriter interface {
	MergeTriples(
		ctx context.Context,
		entityID string,
		triples []message.Triple,
		opts ...graphio.MergeOption,
	) (*graph.EntityState, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ TurnWriter = (*graphio.Store)(nil)

// VerdictStore is the durable home of the verdict's bulky half.
type VerdictStore interface {
	// PutVerdict stores the verdict and returns the reference the turn entity
	// will carry. Called BEFORE the reference reaches the graph.
	PutVerdict(ctx context.Context, turnEntityID string, verdict *payload.Verdict) (content.Ref, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ VerdictStore = (*content.Store)(nil)

// VerdictExecutor is the adjudicator's terminal tool: the single point in the
// engine where judgment about fiction becomes state.
//
// Everything upstream of it is prose and everything downstream of it is
// deterministic, so this is the boundary the whole design rests on. It does four
// things in a fixed order, and the order is the contract:
//
//  1. read the injected identity — never the model's word for it;
//  2. validate the exit against the closed vocabulary, refusing with a
//     correction the model can act on;
//  3. store the verdict;
//  4. commit the rule-matchable scalars and one reference to the turn.
//
// Steps 3 and 4 are in that order because a reference to a verdict nobody stored
// is a turn the dice and the applier cannot resolve, while a stored verdict no
// turn points at is garbage the next attempt overwrites at the same derived key.
type VerdictExecutor struct {
	store  VerdictStore
	graph  TurnWriter
	logger *slog.Logger
	now    func() time.Time
}

// ExecutorOption configures a terminal-tool executor.
type ExecutorOption func(*executorDeps)

type executorDeps struct {
	logger *slog.Logger
	now    func() time.Time
}

// WithLogger sets the executor's logger.
func WithLogger(logger *slog.Logger) ExecutorOption {
	return func(d *executorDeps) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// WithClock overrides the executor's clock, so a committed triple's timestamp is
// an assertable value rather than whatever time it happened to be.
func WithClock(now func() time.Time) ExecutorOption {
	return func(d *executorDeps) {
		if now != nil {
			d.now = now
		}
	}
}

func resolveDeps(opts []ExecutorOption) executorDeps {
	deps := executorDeps{logger: slog.Default(), now: time.Now}
	for _, opt := range opts {
		opt(&deps)
	}
	return deps
}

// NewVerdictExecutor builds the adjudicator's terminal tool.
func NewVerdictExecutor(store VerdictStore, writer TurnWriter, opts ...ExecutorOption) (*VerdictExecutor, error) {
	if store == nil {
		return nil, errors.New("the verdict tool requires a content store; a verdict that cannot be stored " +
			"cannot be referenced, and its banded intents have nowhere to live but the graph")
	}
	if writer == nil {
		return nil, errors.New("the verdict tool requires a graph write surface")
	}
	deps := resolveDeps(opts)
	return &VerdictExecutor{store: store, graph: writer, logger: deps.logger, now: deps.now}, nil
}

// ListTools implements the framework's tool-executor contract.
func (e *VerdictExecutor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{verdictToolDefinition()}
}

// Execute implements the framework's tool-executor contract.
func (e *VerdictExecutor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if call.Name != VerdictToolName {
		return notFound(call, VerdictToolName)
	}

	identity, err := IdentityFrom(call.Metadata)
	if err != nil {
		// Deliberately NOT invalid-args. The model cannot fix a spawn that did
		// not stamp its identity, so handing it back as a correction would spend
		// the whole iteration budget re-emitting a perfectly good verdict.
		return internalFailure(call, "resolve the turn this verdict belongs to", err)
	}

	args, err := parseVerdictArgs(call.Arguments)
	if err != nil {
		return correctable(call, err), nil
	}

	verdict := args.verdict(identity)
	if err := verdict.Validate(); err != nil {
		return correctable(call, err), nil
	}

	ref, err := e.store.PutVerdict(ctx, identity.TurnEntityID, verdict)
	if err != nil {
		return transientFailure(call, "store the verdict", err)
	}

	triples, err := verdict.Triples(identity.TurnEntityID, ref.String(), e.source(), e.now().UTC())
	if err != nil {
		// The projection refuses anything that is not registered rule-matching
		// surface. Reaching here means the verdict passed its own contract and
		// still could not be projected, which is an engine bug rather than
		// something the model said.
		return internalFailure(call, "project the verdict onto the turn", err)
	}
	if _, err := e.graph.MergeTriples(ctx, identity.TurnEntityID, triples); err != nil {
		return transientFailure(call, "record the verdict on the turn", err)
	}

	gate, err := verdict.Scalars.RollGate()
	if err != nil {
		return internalFailure(call, "compare the reported roll gate with the mapping", err)
	}
	e.reportGate(call, identity, gate)

	body, err := json.Marshal(verdict)
	if err != nil {
		return internalFailure(call, "encode the verdict for the loop result", err)
	}

	return agentic.ToolResult{
		CallID:   call.ID,
		Name:     call.Name,
		Content:  string(body),
		StopLoop: true,
		Metadata: map[string]any{
			"turn_id":           identity.TurnID,
			"verdict_ref":       ref.String(),
			"requires_roll":     gate.Reported,
			"gate_advised":      gate.Advised,
			"gate_agrees":       gate.Agrees(),
			"declared_bands":    bandNames(vocabulary.BandsForVerdict(gate.Reported)),
			"intent_count":      countIntents(verdict.Bands),
			"modifier_total":    payload.SumModifiers(verdict.Modifiers),
			"rationale_present": verdict.Rationale != "",
		},
	}, nil
}

// source names this tool as the producer of every triple it writes.
func (e *VerdictExecutor) source() string { return sourceFor(RoleAdjudicator) }

// reportGate makes a roll-gate disagreement visible without acting on it.
//
// D12 gives the adjudicator the gate: a verdict whose reported requires_roll
// disagrees with the (plausibility, risk) mapping is VALID and proceeds, because
// narrative positioning dictates mechanics and a two-axis lookup deciding when
// the dice come out is the rules-first thing this engine exists not to do. What
// the design asks instead is that the disagreement be recorded as data, and the
// divergence rate is the measurement that says whether the persona's judgment
// tracks the mapping — a quality signal and a cost signal both, since a persona
// that calls for the dice far more often than the mapping expects is buying
// rolls with tokens.
//
// It is derivable rather than stored: the recorded verdict carries the classes
// and the reported gate, so any reader — the ledger writer, a metrics pass —
// recomputes the comparison exactly. What is added here is IMMEDIACY: a warn on
// divergence and the comparison on the tool result, following upstream's own
// signalling shape for decide's SAP coercions.
func (e *VerdictExecutor) reportGate(call agentic.ToolCall, identity Identity, gate payload.RollGateExpectation) {
	if gate.Agrees() {
		return
	}
	e.logger.Warn("adjudicator's roll gate disagrees with the advisory mapping",
		"loop_id", call.LoopID,
		"turn_id", identity.TurnID,
		"plausibility", string(gate.Plausibility),
		"risk", string(gate.Risk),
		"reported", gate.Reported,
		"advised", gate.Advised,
		"hint", "this is allowed and the reported gate is what runs (D12); a sustained rate is data about "+
			"the persona or the vocabulary, not a reason to take the gate back")
}

// verdictArgs is the tool's argument shape — deliberately NOT the payload.
//
// The payload carries turn, action and scene; the arguments must not, because
// those are engine knowledge and a schema that asked for them would be asking a
// live model to hallucinate an identifier and a scripted one to know a turn id
// the harness mints at runtime. Keeping the two types apart is what makes that
// structural: there is no field here to fill in.
type verdictArgs struct {
	Scalars   *verdictScalarArgs  `json:"scalars"`
	Modifiers []payload.Modifier  `json:"modifiers"`
	Bands     payload.EffectBands `json:"bands"`
	Rationale string              `json:"rationale"`
}

// verdictScalarArgs mirrors the payload's scalars with ONE difference, and the
// difference is the reason this type exists rather than an embedded reuse.
//
// RequiresRoll is a pointer. On the payload it is a plain bool, which is correct
// there — every stored verdict has been through this gate. Here, absent and
// false would decode to the same value, so "the model forgot the roll gate" and
// "the model declined the dice" would be indistinguishable — and they lead to
// opposite band sets, opposite costs, and opposite turns.
type verdictScalarArgs struct {
	Plausibility vocabulary.Plausibility     `json:"plausibility"`
	Risk         vocabulary.Risk             `json:"risk"`
	Consequence  vocabulary.ConsequenceClass `json:"consequence"`
	RequiresRoll *bool                       `json:"requires_roll"`
}

// verdict builds the canonical payload, injecting the identity the model was
// never asked for.
func (a verdictArgs) verdict(identity Identity) *payload.Verdict {
	return &payload.Verdict{
		TurnID:   identity.TurnID,
		ActionID: identity.ActionID,
		SceneID:  identity.SceneID,
		Scalars: payload.VerdictScalars{
			Plausibility: a.Scalars.Plausibility,
			Risk:         a.Scalars.Risk,
			Consequence:  a.Scalars.Consequence,
			RequiresRoll: *a.Scalars.RequiresRoll,
		},
		Modifiers: a.Modifiers,
		Bands:     a.Bands,
		Rationale: a.Rationale,
	}
}

// parseVerdictArgs decodes the model's arguments strictly.
//
// UNKNOWN FIELDS ARE REJECTED, and that is load-bearing rather than tidy. A
// silently-ignored field is an exit the model believes it made and the engine
// never received: a `turn_id` it invented would look accepted while the executor
// used its own, and an effect smuggled under a name nobody reads would be a
// world change the fiction promised and the world never got. Rejecting names the
// field, so the correction is actionable.
//
// The framework substitutes an EMPTY argument map for arguments that are not
// valid JSON — documented client behaviour, and the reason the missing-field
// path below has to produce a message that makes sense on its own: what reaches
// here from a truncated or malformed tool call is not broken text, it is nothing
// at all.
func parseVerdictArgs(arguments map[string]any) (verdictArgs, error) {
	var args verdictArgs
	if err := decodeStrict(arguments, &args); err != nil {
		return verdictArgs{}, err
	}
	if args.Scalars == nil {
		return verdictArgs{}, fmt.Errorf(
			"the verdict carries no `scalars` object; it must report plausibility (%v), risk (%v), "+
				"consequence (%v) and requires_roll",
			vocabulary.Plausibilities(), vocabulary.Risks(), vocabulary.Consequences())
	}
	if args.Scalars.RequiresRoll == nil {
		return verdictArgs{}, errors.New(
			"the verdict does not report `requires_roll`; whether the dice are consulted is your judgment and " +
				"the engine will not assume it, because omitting it and declining the dice are different verdicts")
	}
	if args.Bands == nil {
		return verdictArgs{}, fmt.Errorf(
			"the verdict declares no `bands`; a verdict that requires a roll declares exactly %v and one that "+
				"does not declares exactly [%s], and either may be empty",
			vocabulary.RollBands(), vocabulary.BandAuto)
	}
	return args, nil
}

// decodeStrict re-encodes the framework's untyped argument map and decodes it
// into a typed shape with unknown fields refused.
//
// A round trip rather than a hand-written walk over map[string]any: the typed
// shape is then the ONE statement of what an exit may contain, the closed
// vocabulary's own string types do the membership work, and a numeric field that
// arrived as 4.5 is a decode error naming the field rather than a silent
// truncation to 4.
func decodeStrict(arguments map[string]any, into any) error {
	if len(arguments) == 0 {
		return errors.New(
			"the tool call carried no arguments at all. If your reply was cut off mid-call, emit the whole " +
				"call again; the engine receives an empty argument set for a call it could not parse")
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("the tool call's arguments could not be read: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("the tool call's arguments do not match the tool's schema: %w", err)
	}
	if decoder.More() {
		return errors.New("the tool call's arguments carry trailing content after the object")
	}
	return nil
}

// bandNames renders a band list for tool-result metadata.
func bandNames(bands []vocabulary.OutcomeBand) []string {
	out := make([]string, 0, len(bands))
	for _, band := range bands {
		out = append(out, string(band))
	}
	return out
}

// countIntents totals the proposed intents across every declared band. It walks
// the vocabulary's band order rather than the map so the number does not depend
// on Go's randomized iteration.
func countIntents(bands payload.EffectBands) int {
	total := 0
	for _, band := range vocabulary.OutcomeBands() {
		total += len(bands[band])
	}
	return total
}

// sourceFor names a persona as the producer of the triples its exit writes.
//
// Derived from the role rather than declared per executor, so a new persona
// cannot ship with another one's provenance stamped on its facts.
func sourceFor(role Role) string { return "persona-" + string(role) }

// notFound is the routing-bug answer: this executor was handed a tool that is
// not its own.
func notFound(call agentic.ToolCall, want string) (agentic.ToolResult, error) {
	err := fmt.Errorf("tool %q routed to the %q executor", call.Name, want)
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Error:     err.Error(),
		ErrorKind: agentic.ToolErrorNotFound,
	}, errs.WrapInvalid(err, "persona", "Execute", "route tool")
}

// correctable is a refusal the MODEL can act on: the exit was wrong, the
// vocabulary is closed, and the message names what to fix.
//
// The Go error is deliberately nil. ToolErrorInvalidArgs is fed back into the
// conversation as a tool result, so the loop iterates and the model re-emits
// within its budget; returning an error instead would surface it as an executor
// failure and end the turn on the first thing a mid-tier model got wrong.
func correctable(call agentic.ToolCall, cause error) agentic.ToolResult {
	return agentic.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Error: fmt.Sprintf("%v. The vocabulary for this tool is closed: correct the call and emit it again.",
			cause),
		ErrorKind: agentic.ToolErrorInvalidArgs,
	}
}

// transientFailure is a refusal NOTHING the model does can fix: infrastructure.
func transientFailure(call agentic.ToolCall, what string, cause error) (agentic.ToolResult, error) {
	wrapped := fmt.Errorf("%s: %w", what, cause)
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Error:     wrapped.Error(),
		ErrorKind: agentic.ToolErrorNetwork,
	}, errs.WrapTransient(wrapped, "persona", "Execute", what)
}

// internalFailure is a refusal that is the ENGINE's fault: a spawn that did not
// stamp its identity, a projection that refused a validated payload.
func internalFailure(call agentic.ToolCall, what string, cause error) (agentic.ToolResult, error) {
	wrapped := fmt.Errorf("%s: %w", what, cause)
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Error:     wrapped.Error(),
		ErrorKind: agentic.ToolErrorInternal,
	}, errs.WrapInvalid(wrapped, "persona", "Execute", what)
}
