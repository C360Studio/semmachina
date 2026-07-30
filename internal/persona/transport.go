package persona

// The agentic loop's wire coordinates.
//
// They live in this package rather than beside the stage that publishes onto
// them because THREE components now have to agree on them exactly, and two of
// them are not the stage: the spawner publishes a task, the loop-failure watcher
// binds the stream the loop's lifecycle events arrive on, and the boot-time
// stranded-turn pass reads which tasks are still unacknowledged. A restatement in
// any one of them is a place for the three to disagree, and the disagreement is
// silent — a subject nobody publishes on and a filter nobody matches both look
// like an empty queue.
//
// The names and shapes are UPSTREAM's, not this engine's. The agentic-loop
// component declares every one of its ports with `stream_name: AGENT`, and
// semstreams' stream provisioning derives that stream's subjects from the name.
const (
	// TaskStream is the agentic loop's stream. It carries both directions of the
	// persona conversation: the tasks this engine publishes and the lifecycle
	// events it consumes.
	TaskStream = "AGENT"
	// TaskSubjectPrefix is the agentic loop's task subject space. The subject is
	// per ROLE rather than per turn: the loop's input port filters one token, so
	// a per-turn subject would match nothing.
	TaskSubjectPrefix = "agent.task."
	// TaskSubjectFilter is the subject filter upstream's task consumer declares,
	// and therefore the filter that identifies that consumer among the stream's
	// others. The stranded-turn pass finds the consumer by what it FILTERS rather
	// than by its name, because the name is built from an unexported sanitisation
	// plus an operator-configurable suffix (semstreams#733).
	TaskSubjectFilter = TaskSubjectPrefix + "*"
	// AgentSubjectFilter is the whole agentic subject space the AGENT stream
	// captures. It is DERIVED the way semstreams' own stream provisioning derives
	// it — the stream name, lower-cased, plus a full wildcard — so a stream this
	// engine creates and one semstreams creates capture the same subjects rather
	// than nearly the same ones.
	AgentSubjectFilter = "agent.>"

	// ToolExecuteSubjectFilter and ToolResultSubjectFilter are the tool-dispatch
	// lane, and they are the pair a composition is most likely to leave out.
	//
	// The agentic loop does NOT execute a tool in process. deps.ToolRegistry is
	// how it ADVERTISES tools to the model (ListAvailableTools → ListTools); when
	// the model calls one, the loop PUBLISHES the call onto `tool.execute.<name>`
	// and waits for the answer on `tool.result.>`. The component in between is
	// agentic-tools, which consumes `tool.execute.>` and publishes
	// `tool.result.*`. Both components declare both lanes with `stream_name:
	// AGENT`, so both ride this stream — and upstream's own subject derivation
	// (lower-case the stream name, append `.>`) produces neither.
	//
	// # Why this is a SILENT failure rather than a startup error
	//
	// Measured, not assumed: the NATS server ACCEPTS a consumer whose filter
	// subject lies outside its stream's subjects. So a deployment whose AGENT
	// stream carries only `agent.>` starts cleanly, binds every consumer, and
	// looks healthy — and then the first tool call is published into a subject
	// the stream does not capture, agentic-tools is never delivered it, and the
	// persona burns its whole iteration budget waiting for a result that cannot
	// arrive. The turn ends as a cap exhaustion, which describes the symptom and
	// hides the cause.
	//
	// Restated as literals for the reason internal/world restates the tool-result
	// root: nothing upstream exports them.
	// TestAgentStreamConfig_CapturesEveryPortTheAgenticComponentsDeclare reads
	// both components' own port declarations and fails when this set stops
	// covering them.
	ToolExecuteSubjectFilter = "tool.execute.>"
	// ToolResultSubjectFilter is the answering half of the lane above.
	ToolResultSubjectFilter = "tool.result.>"

	// ModelRequestSubjectFilter and ModelResponseSubjectFilter are the lane that
	// reaches the model, and it is the SECOND lane a composition is likely to
	// leave out for exactly the reason the first one is.
	//
	// The agentic loop does not call an endpoint any more than it executes a tool.
	// deps.ModelRegistry is how it RESOLVES which endpoint a capability means; when
	// it needs a completion it publishes an AgentRequest onto `agent.request.*` and
	// waits on `agent.response.>`. The component in between is agentic-model, which
	// owns the HTTP client, the retry curve and the token accounting.
	//
	// Both subjects sit under AgentSubjectFilter, so the stream captures them
	// whether or not anybody names them — which is precisely why the omission is
	// invisible at the stream. It shows only as a persona that publishes a request,
	// receives no response, and ends its turn on the loop's timeout: a stall
	// reported as a cap exhaustion, which names the symptom and hides the cause.
	// Found end to end, not by reading: a composition running the loop and the tool
	// executor and NOT the model bridge parked every turn in `adjudicating`.
	ModelRequestSubjectFilter = "agent.request.>"
	// ModelResponseSubjectFilter is the answering half of the lane above.
	ModelResponseSubjectFilter = "agent.response.>"
)

// AgentStreamSubjects returns every subject the AGENT stream must capture for
// this engine's personas to run.
//
// Stated once, here, because four things depend on the same answer and only one
// of them is obvious: the spawner publishes tasks onto it, the loop-failure
// watcher refuses to boot unless its own subject is captured, the agentic loop
// publishes every tool call onto it, and agentic-tools answers on it. A subject
// missing from this set is not a component that fails to start — the server
// accepts a consumer filtered outside its stream — it is a persona whose tool
// call is delivered to nobody and which then burns its whole budget waiting.
func AgentStreamSubjects() []string {
	return []string{AgentSubjectFilter, ToolExecuteSubjectFilter, ToolResultSubjectFilter}
}

// TaskSubjectFor returns the subject a role's tasks are published on.
func TaskSubjectFor(role Role) string { return TaskSubjectPrefix + string(role) }
