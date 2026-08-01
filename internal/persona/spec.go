package persona

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/model"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Role names one persona. The set is closed: the engine has exactly two places
// where a language model is allowed to speak, and a third would be a new
// capability with its own spec, not a string somebody passed in.
type Role string

// The closed persona-role set.
const (
	// RoleAdjudicator judges fiction and exits through the banded verdict.
	RoleAdjudicator Role = "adjudicator"
	// RoleNarrator voices a committed outcome and exits through prose.
	RoleNarrator Role = "narrator"
	// RoleCasekeeper privately interprets mystery actions into CaseDecision.
	RoleCasekeeper Role = "casekeeper"
)

// ModelSlot is the size class a persona expects to run on.
//
// It is DOCUMENTATION with teeth rather than a routing key: the actual endpoint
// comes from the model registry via Capability, which is deployment
// configuration. The slot records what the engine believes the role needs, so a
// deployment that wires the adjudicator to a 4B model is making a visible choice
// rather than an invisible one.
type ModelSlot string

// The closed slot set.
const (
	// SlotSmall is a classification-shaped model: fast, cheap, no prose quality
	// and no schema burden. Nothing in this slice runs here; NPC decisions
	// (roadmap stage 9) are what it exists for.
	SlotSmall ModelSlot = "small"
	// SlotMid is a model that can hold a nested schema. ADR-026 records upstream
	// living the alternative: "requiring every agent to submit via a
	// schema-enforced terminal tool breaks on small models. Schema adherence
	// fails; retries eat iteration budget; flows stall."
	SlotMid ModelSlot = "mid"
	// SlotLarge is a model chosen for prose. It carries no schema burden at all,
	// which is exactly the trade ADR-026 recommends: concentrate schema
	// discipline in one role and let the other be good at writing.
	SlotLarge ModelSlot = "large"
)

// The model-registry capability names the personas resolve through.
//
// A capability rather than an endpoint name, because that is the seam that makes
// "a live model is a model_registry retarget, never a code change" true: the
// registry maps the capability to whichever endpoint holds the right slot, and
// nothing in this package names a provider, a URL, or a model.
const (
	// CapabilityAdjudication is the fiction adjudicator's capability.
	CapabilityAdjudication = "fiction_adjudication"
	// CapabilityNarration is the narrator's capability.
	CapabilityNarration = "narration"
	// CapabilityCasekeeping resolves the private case interpreter.
	CapabilityCasekeeping = "casekeeping"
)

// Iteration budgets and per-call timeouts.
//
// The budgets are small on purpose, and the asymmetry is the design. Neither
// persona has a tool that fetches anything: the whole world it needs is
// assembled by the context assembler and handed over in the prompt, so a
// well-behaved persona exits on its FIRST iteration. Every iteration after that
// is a correction round after the tool boundary refused an exit.
//
// So the adjudicator gets three — one exit and two corrections, which is what a
// mid-tier model needs when it declares the wrong band set — and the narrator
// gets two, because a single-property schema has almost nothing to get wrong. A
// bigger budget does not buy better output; it buys a longer, more expensive way
// to reach the same failure (F5: "retries eat iteration budget; flows stall").
const (
	// AdjudicatorMaxIterations is the adjudicator's per-spawn iteration budget.
	AdjudicatorMaxIterations = 3
	// NarratorMaxIterations is the narrator's per-spawn iteration budget.
	NarratorMaxIterations = 2
	// CasekeeperMaxIterations permits one exit and two correction rounds.
	CasekeeperMaxIterations = 3

	// AdjudicatorTimeout bounds one adjudication's model calls.
	AdjudicatorTimeout = 90 * time.Second
	// NarratorTimeout bounds one narration's model calls. Longer because the
	// large slot generates prose, and prose is more tokens than a verdict.
	NarratorTimeout = 120 * time.Second
	// CasekeeperTimeout bounds private structured interpretation.
	CasekeeperTimeout = 90 * time.Second
)

// Spec is one persona's whole configuration: which model class it expects, how
// much cognition it is allowed, what it may call, and what its completion leaves
// on the turn.
//
// It is a value rather than a config file because none of it is world content.
// The starter world's persona records carry tone, register, and judging stance —
// which are properties of that world — and deliberately carry no model slot, no
// iteration cap, and no tool schema, because a downloaded package must not be
// able to pick its own spend or widen its own exit vocabulary.
type Spec struct {
	// Role is the persona, and the value the loop filters prompt fragments by:
	// it must equal the `roles` entry in the world package's persona record, or
	// the assembled system prompt loses the world's voice without erroring.
	Role Role
	// Slot is the model class this role expects.
	Slot ModelSlot
	// Capability is the model-registry capability the spawn resolves through.
	Capability string
	// MaxIterations is the per-spawn iteration budget. The loop narrows to
	// min(this, the component's ceiling), so a spec may tighten a budget and
	// never widen one.
	MaxIterations int
	// Timeout bounds each model call this persona makes.
	Timeout time.Duration
	// Artifact is the reference predicate this persona's completion lands on the
	// turn entity. It is what the resume check reads: the phase is written on
	// stage ENTRY and cannot tell "entered" from "finished", and this can.
	Artifact vocabulary.Predicate
	// Tool is the persona's single terminal tool — the only thing it may call.
	Tool agentic.ToolDefinition
	// NeedsBand declares that this persona's task carries the committed outcome
	// band. Only the narrator does: it is voicing a band, and the adjudicator has
	// not produced one yet.
	NeedsBand bool
	// NeedsCase requires casekeeper-only case and acting-actor metadata.
	NeedsCase bool
}

// Adjudicator returns the fiction adjudicator's spec.
func Adjudicator() Spec {
	return Spec{
		Role:          RoleAdjudicator,
		Slot:          SlotMid,
		Capability:    CapabilityAdjudication,
		MaxIterations: AdjudicatorMaxIterations,
		Timeout:       AdjudicatorTimeout,
		Artifact:      vocabulary.TurnVerdictRef,
		Tool:          verdictToolDefinition(),
	}
}

// Narrator returns the narrator's spec.
func Narrator() Spec {
	return Spec{
		Role:          RoleNarrator,
		Slot:          SlotLarge,
		Capability:    CapabilityNarration,
		MaxIterations: NarratorMaxIterations,
		Timeout:       NarratorTimeout,
		Artifact:      vocabulary.TurnNarrationRef,
		Tool:          narrationToolDefinition(),
		NeedsBand:     true,
	}
}

// Casekeeper returns the private mystery interpreter's spec.
func Casekeeper() Spec {
	return Spec{
		Role: RoleCasekeeper, Slot: SlotMid, Capability: CapabilityCasekeeping,
		MaxIterations: CasekeeperMaxIterations, Timeout: CasekeeperTimeout,
		Artifact: vocabulary.TurnCaseDecisionRef, Tool: caseDecisionToolDefinition(), NeedsCase: true,
	}
}

// specs is the authoritative list. A persona missing from it is invisible to
// boot-time registry checks and to tool registration.
var specs = []Spec{Casekeeper(), Adjudicator(), Narrator()}

// Specs returns every persona this engine runs.
func Specs() []Spec { return slices.Clone(specs) }

// SpecFor returns one persona's spec by role.
func SpecFor(role Role) (Spec, error) {
	for _, spec := range specs {
		if spec.Role == role {
			return spec, nil
		}
	}
	return Spec{}, fmt.Errorf("%q is not a persona role (this engine runs %v)", role, roleNames())
}

func roleNames() []Role {
	out := make([]Role, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Role)
	}
	return out
}

// Validate checks a spec is internally coherent.
func (s Spec) Validate() error {
	if s.Role == "" {
		return errors.New("persona spec has no role")
	}
	if s.Capability == "" {
		return fmt.Errorf("persona %q names no model-registry capability", s.Role)
	}
	if s.MaxIterations < 1 {
		return fmt.Errorf(
			"persona %q has an iteration budget of %d; bounded cognition means a positive cap, and an "+
				"unbounded persona is a turn that can stall forever", s.Role, s.MaxIterations)
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("persona %q has no timeout", s.Role)
	}
	if !vocabulary.IsStorageRef(s.Artifact) {
		return fmt.Errorf(
			"persona %q lands artifact predicate %q, which is not a storage reference; the resume check reads "+
				"a POINTER at what the persona produced, and a predicate that carries a value instead cannot "+
				"say whether the interrupted attempt finished", s.Role, s.Artifact)
	}
	if err := s.Tool.Validate(); err != nil {
		return fmt.Errorf("persona %q terminal tool: %w", s.Role, err)
	}
	return nil
}

// CheckRegistry proves the deployment can actually run this persona.
//
// Three things are checked, and the first two are ones that fail SILENTLY at
// runtime — which is why this exists at all rather than being left to the first
// turn.
//
//   - The capability must be DECLARED, not merely resolvable. Upstream's
//     Registry.Resolve falls back to Defaults.Model for a capability it does not
//     know, so ResolveEndpoint succeeds for a name nobody configured and hands
//     back whatever the deployment's default model is. A deployment that forgot
//     `fiction_adjudication` would therefore run the adjudicator on the default
//     endpoint and report nothing — which is exactly the "on the small slot by
//     accident" configuration ADR-026 and F5 warn about, arrived at through a
//     silent fallback rather than a decision.
//   - The endpoint must support tool calling. A persona whose only exit is a
//     tool call, pointed at an endpoint that cannot make one, can NEVER
//     terminate: every iteration returns prose, the loop burns its whole budget,
//     and the turn fails as a cap exhaustion — a code that describes the symptom
//     and hides the cause.
//   - The spec itself must be coherent, which CheckRegistryAll checks alongside.
func (s Spec) CheckRegistry(registry model.RegistryReader) error {
	if registry == nil {
		return fmt.Errorf("persona %q cannot be checked against a nil model registry", s.Role)
	}
	if registry.GetCapability(s.Capability) == nil {
		return fmt.Errorf(
			"persona %q resolves through model-registry capability %q, which this deployment does not declare; "+
				"an undeclared capability silently resolves to the registry's DEFAULT model, so this persona "+
				"would run on whatever that is and nothing would report it", s.Role, s.Capability)
	}
	resolved, endpoint, err := model.ResolveEndpointWithConfig(registry, s.Capability)
	if err != nil {
		return fmt.Errorf(
			"persona %q could not resolve model-registry capability %q: %w", s.Role, s.Capability, err)
	}
	if endpoint == nil {
		return fmt.Errorf("persona %q resolved capability %q to no endpoint configuration", s.Role, s.Capability)
	}
	if !endpoint.SupportsTools {
		return fmt.Errorf(
			"persona %q resolves to endpoint model %q, which does not support tool calling; this persona's "+
				"only exit is the %q tool, so every turn would burn its whole %d-iteration budget and fail as "+
				"a cap exhaustion rather than as the configuration error it is",
			s.Role, resolved.Model, s.Tool.Name, s.MaxIterations)
	}
	return nil
}

// CheckRegistryAll checks every persona against one deployment's registry.
func CheckRegistryAll(registry model.RegistryReader) error {
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			return err
		}
		if err := spec.CheckRegistry(registry); err != nil {
			return err
		}
	}
	return nil
}

// TaskRequest is one spawn's inputs.
type TaskRequest struct {
	// Identity is who the task is about. Injected onto every tool call the loop
	// dispatches; never asked of the model.
	Identity Identity
	// ResumeAttempt is the persisted turn-resume attempt for this execution. Zero
	// is the original execution. It is part of TaskID so a publish retry inside
	// one execution deduplicates while work explicitly re-triggered by recovery
	// is a new task the AGENT stream must accept.
	ResumeAttempt int
	// Band is the committed outcome the narrator is voicing. Required for a
	// persona whose spec declares NeedsBand, refused for one that does not.
	Band vocabulary.OutcomeBand
	// Prompt is the assembled user prompt — the world and the turn, rendered.
	// The SYSTEM prompt is not here: the loop assembles it from the role's
	// registered fragments, which is where the world package's voice lives.
	Prompt string
}

// Task renders a spawn as the framework's own task message.
//
// Four things on it are load-bearing and none of them is the prompt:
//
//   - Tools is an explicit per-task allowlist of exactly one tool. Non-nil is
//     an override, so this is not "the tools we happen to have registered" — it
//     is "these and nothing else", which is how the narration spec's "no
//     mutation-capable tool" becomes structural rather than a review note.
//   - ToolChoice is `required`, which with a one-tool allowlist says "exit
//     through this". It is a HINT: honoured where the provider honours it,
//     silently ignored elsewhere, and the executor plus the cap are what actually
//     hold.
//   - MaxIterations is the per-spawn budget. The loop takes the minimum of it
//     and the component ceiling, so this can only tighten.
//   - Metadata is the identity channel. The loop merges it onto every tool call
//     it dispatches, and the model cannot write to it.
//
// TaskID is derived from role + turn + persisted execution generation rather
// than minted. Generation zero preserves the legacy role-turn ID. A resumed
// execution uses slash-delimited fields because slash is impossible in a
// validated turn ID; appending a suffix would collide with a different valid
// turn whose ID already ended in that suffix. A redelivery inside one attempt
// carries the same ID and deduplicates.
func (s Spec) Task(request TaskRequest) (agentic.TaskMessage, error) {
	if err := s.Validate(); err != nil {
		return agentic.TaskMessage{}, err
	}
	if err := request.Identity.Validate(); err != nil {
		return agentic.TaskMessage{}, err
	}
	if request.ResumeAttempt < 0 {
		return agentic.TaskMessage{}, fmt.Errorf(
			"persona %q was spawned with negative resume attempt %d", s.Role, request.ResumeAttempt)
	}
	if request.Prompt == "" {
		return agentic.TaskMessage{}, fmt.Errorf(
			"persona %q was spawned with an empty prompt; the assembled world and the player's action are the "+
				"entire input, and a persona handed neither would answer from the system prompt alone", s.Role)
	}

	metadata := request.Identity.metadata()
	if s.NeedsCase {
		if request.Identity.CaseID == "" || request.Identity.ActorID == "" {
			return agentic.TaskMessage{}, fmt.Errorf("persona %q requires injected case_id and actor_id", s.Role)
		}
	} else if request.Identity.CaseID != "" || request.Identity.ActorID != "" {
		return agentic.TaskMessage{}, fmt.Errorf("persona %q must not receive private case identity", s.Role)
	}
	switch {
	case s.NeedsBand:
		band, err := vocabulary.ParseOutcomeBand(string(request.Band))
		if err != nil {
			return agentic.TaskMessage{}, fmt.Errorf(
				"persona %q voices a committed outcome and must be told which one: %w", s.Role, err)
		}
		metadata[MetadataKeyBand] = string(band)
	case request.Band != "":
		return agentic.TaskMessage{}, fmt.Errorf(
			"persona %q was spawned carrying outcome band %q; it runs before any band exists, and a band in "+
				"its context would be a number the engine had not chosen yet", s.Role, request.Band)
	}

	budget := s.MaxIterations
	taskID := fmt.Sprintf("%s-%s", s.Role, request.Identity.TurnID)
	if request.ResumeAttempt > 0 {
		taskID = fmt.Sprintf("%s/%s/resume/%d", s.Role, request.Identity.TurnID, request.ResumeAttempt)
	}
	task := agentic.TaskMessage{
		TaskID:        taskID,
		Role:          string(s.Role),
		Model:         s.Capability,
		Prompt:        request.Prompt,
		Tools:         []agentic.ToolDefinition{s.Tool},
		ToolChoice:    &agentic.ToolChoice{Mode: "required"},
		MaxIterations: &budget,
		Timeout:       s.Timeout.String(),
		Metadata:      metadata,
	}
	if err := task.Validate(); err != nil {
		return agentic.TaskMessage{}, fmt.Errorf("persona %q task: %w", s.Role, err)
	}
	return task, nil
}
