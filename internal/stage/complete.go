package stage

import (
	"context"
	"errors"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Completer is the stage that closes a narrated turn.
//
// It writes one fact and does no work, which is exactly right and worth stating
// because it looks like a stage that could be deleted. It cannot: the turn's
// phase belongs to the recorder, so SOMETHING has to enter `complete`, and a
// rule writing the phase itself would make the rule pack a second owner of a
// single-valued fact — through an appending lane, at that (F14). The phase write
// is also the event the terminal rule matches, which is how a completed turn
// announces itself to the ledger and to the player's egress adapter.
type Completer struct {
	recorder PhaseRecorder
}

// NewCompleter builds the turn-completion stage.
func NewCompleter(recorder PhaseRecorder) (*Completer, error) {
	if recorder == nil {
		return nil, errors.New("the completion stage requires a phase recorder")
	}
	return &Completer{recorder: recorder}, nil
}

// Phase implements Stage.
func (c *Completer) Phase() vocabulary.TurnPhase { return vocabulary.PhaseComplete }

// Run closes the turn.
func (c *Completer) Run(ctx context.Context, trigger Trigger) error {
	_, err := enter(ctx, c.recorder, trigger, c.Phase())
	return err
}
