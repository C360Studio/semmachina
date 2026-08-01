package persona

import (
	"errors"
	"fmt"

	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"

	"github.com/c360studio/semmachina/internal/content"
)

// ArtifactStore is the durable home of everything both personas produce.
type ArtifactStore interface {
	CaseDecisionStore
	VerdictStore
	NarrationStore
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ ArtifactStore = (*content.Store)(nil)

// RegisterTools wires both terminal tools into a deployment's tool registry.
//
// It is the one entry point, called once at boot, and it is deliberately
// all-or-nothing: a deployment that registered the narrator's exit and not the
// adjudicator's would boot healthy, accept a turn, and wedge on the first
// adjudication with a tool-not-found the model cannot act on. Registering both
// or neither makes that a boot failure instead.
//
// RegisterExecutor rather than RegisterTool, following upstream's own guidance:
// it maps every name the executor advertises, so ListTools() (what the model is
// shown) and Execute() dispatch (what the model reaches) cannot drift apart.
// A name already taken is an error rather than a silent overwrite — a second
// executor answering to submit_verdict is a second opinion about what a verdict
// is.
func RegisterTools(
	registry *agentictools.ExecutorRegistry,
	store ArtifactStore,
	writer TurnWriter,
	opts ...ExecutorOption,
) error {
	if registry == nil {
		return errors.New("registering the persona tools requires a tool registry")
	}
	verdict, err := NewVerdictExecutor(store, writer, opts...)
	if err != nil {
		return err
	}
	casekeeper, err := NewCaseDecisionExecutor(store, writer, opts...)
	if err != nil {
		return err
	}
	narration, err := NewNarrationExecutor(store, writer, opts...)
	if err != nil {
		return err
	}
	if err := registry.RegisterExecutor(casekeeper); err != nil {
		return fmt.Errorf("register the %s tool: %w", CaseDecisionToolName, err)
	}
	if err := registry.RegisterExecutor(verdict); err != nil {
		return fmt.Errorf("register the %s tool: %w", VerdictToolName, err)
	}
	if err := registry.RegisterExecutor(narration); err != nil {
		return fmt.Errorf("register the %s tool: %w", NarrationToolName, err)
	}
	return nil
}
