package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
)

// NarrationStore is the durable home of a turn's prose.
type NarrationStore interface {
	// PutNarration stores the prose and returns the reference the turn entity
	// will carry. Called BEFORE the reference reaches the graph.
	PutNarration(ctx context.Context, turnEntityID string, narration *content.Narration) (content.Ref, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ NarrationStore = (*content.Store)(nil)

// NarrationExecutor is the narrator's terminal tool.
//
// It is the smallest surface in the engine that a language model can reach, and
// that is its entire design. The narrator runs after the outcome is committed:
// the classes were judged by the adjudicator, the band was chosen by seeded
// dice, and the world changes were validated and written by the applier. So this
// tool takes prose and nothing else, writes prose and one pointer at it, and has
// no way to touch anything a rule can branch on beyond the pointer's arrival.
//
// "No mutation-capable tool" (the narration spec) is therefore three separate
// structural facts rather than a promise: the spawned task advertises exactly
// one tool, that tool's schema has exactly one property, and this executor's
// only registered predicate is a reference. None of the three is a review note.
type NarrationExecutor struct {
	store  NarrationStore
	graph  TurnWriter
	logger *slog.Logger
	now    func() time.Time
}

// NewNarrationExecutor builds the narrator's terminal tool.
func NewNarrationExecutor(
	store NarrationStore,
	writer TurnWriter,
	opts ...ExecutorOption,
) (*NarrationExecutor, error) {
	if store == nil {
		return nil, errors.New("the narration tool requires a content store; prose is fiction and exceeds the " +
			"triple-object budget, so it can only reach the graph as a reference to something stored")
	}
	if writer == nil {
		return nil, errors.New("the narration tool requires a graph write surface")
	}
	deps := resolveDeps(opts)
	return &NarrationExecutor{store: store, graph: writer, logger: deps.logger, now: deps.now}, nil
}

// ListTools implements the framework's tool-executor contract.
func (e *NarrationExecutor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{narrationToolDefinition()}
}

// Execute implements the framework's tool-executor contract.
//
// The write order is the whole crash-safety argument of this stage and is not a
// preference: prose first, reference last. A reference to missing prose is a
// completed turn whose result cannot be delivered and cannot be re-derived — the
// player is told the turn finished and there is nothing to show them — while an
// object no turn points at is garbage that the next attempt overwrites at the
// identical derived key. The two failures are not symmetric, so the order is
// fixed rather than convenient.
func (e *NarrationExecutor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if call.Name != NarrationToolName {
		return notFound(call, NarrationToolName)
	}

	identity, err := IdentityFrom(call.Metadata)
	if err != nil {
		return internalFailure(call, "resolve the turn this narration belongs to", err)
	}
	// The band is engine knowledge for the same reason the identifiers are. A
	// narrator asked which outcome it was voicing could answer wrongly, and a
	// narration filed under a band it does not voice is exactly the drift this
	// engine exists to make detectable.
	band, err := BandFrom(call.Metadata)
	if err != nil {
		return internalFailure(call, "resolve the outcome band this narration voices", err)
	}

	args, err := parseNarrationArgs(call.Arguments)
	if err != nil {
		return correctable(call, err), nil
	}

	narration := &content.Narration{TurnID: identity.TurnID, Band: band, Prose: args.Prose}
	if err := narration.Validate(); err != nil {
		return correctable(call, err), nil
	}

	ref, err := e.store.PutNarration(ctx, identity.TurnEntityID, narration)
	if err != nil {
		return transientFailure(call, "store the narration prose", err)
	}

	triples, err := narrationTriples(identity, ref, e.source(), e.now().UTC())
	if err != nil {
		return internalFailure(call, "project the narration reference onto the turn", err)
	}
	if _, err := e.graph.MergeTriples(ctx, identity.TurnEntityID, triples); err != nil {
		// The prose is stored and unreferenced. That is the survivable half of
		// the ordering: a resumed narration re-puts identical bytes at the same
		// derived key and commits the reference, and nothing in between ever
		// pointed at an object that was not there.
		return transientFailure(call, "record the narration reference on the turn", err)
	}

	body, err := json.Marshal(narration)
	if err != nil {
		return internalFailure(call, "encode the narration for the loop result", err)
	}

	return agentic.ToolResult{
		CallID:   call.ID,
		Name:     call.Name,
		Content:  string(body),
		StopLoop: true,
		Metadata: map[string]any{
			"turn_id":       identity.TurnID,
			"narration_ref": ref.String(),
			"band":          string(band),
			"prose_bytes":   len(narration.Prose),
		},
	}, nil
}

// source names this tool as the producer of the triple it writes.
func (e *NarrationExecutor) source() string { return sourceFor(RoleNarrator) }

// narrationArgs is the tool's argument shape: prose, and deliberately nothing
// else.
//
// The narration spec bounds the exit to "the prose reference and rule-opaque
// metadata", and this engine's narrator emits no metadata at all. That is a
// choice, not an omission: nothing in the slice reads narrator metadata, and a
// field with no consumer in a closed exit vocabulary is how a closed vocabulary
// stops meaning anything. When the chronicler (roadmap stage 4) needs salience
// marks, they arrive with the consumer that reads them.
type narrationArgs struct {
	Prose string `json:"prose"`
}

// parseNarrationArgs decodes the narrator's arguments strictly.
//
// Unknown fields are refused, and here that is not merely hygiene: this is the
// tool an over-helpful narrator would try to propose an effect through. A model
// that emits `{"prose": "...", "effects": [...]}` must be told no — not have its
// effects silently dropped while the call reports success, which is the shape
// where the fiction and the world quietly disagree.
func parseNarrationArgs(arguments map[string]any) (narrationArgs, error) {
	var args narrationArgs
	if err := decodeStrict(arguments, &args); err != nil {
		return narrationArgs{}, fmt.Errorf(
			"%w. This tool takes `prose` and nothing else: the outcome is already decided and already "+
				"committed, so there is nothing here to propose", err)
	}
	return args, nil
}

// narrationTriples projects the narration's single mark on the graph.
//
// It goes through the shared projection like every other payload's triples, and
// the reason is the reference: the projection is where the storage-reference
// grammar is enforced and where the turn-id/turn-entity pairing is checked, so a
// hand-built triple here would be the one write in the engine that skipped both
// gates — on the one predicate whose object is guaranteed to be LLM-adjacent.
// That the narration lands NOTHING but the pointer is a property of the
// projection rather than of this function: TurnNarration declares itself
// scalar-less, the projection refuses a scalar-less shape that also registers
// predicates, and TestTurnNarration_ProjectsExactlyOneReferenceTriple holds the
// count. There is no assertion to make here that those do not already make
// better.
func narrationTriples(identity Identity, ref content.Ref, source string, at time.Time) ([]message.Triple, error) {
	state := &payload.TurnNarration{TurnID: identity.TurnID, NarrationRef: ref.String()}
	return state.Triples(identity.TurnEntityID, source, at)
}
