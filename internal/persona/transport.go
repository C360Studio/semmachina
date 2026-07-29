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
)

// TaskSubjectFor returns the subject a role's tasks are published on.
func TaskSubjectFor(role Role) string { return TaskSubjectPrefix + string(role) }
