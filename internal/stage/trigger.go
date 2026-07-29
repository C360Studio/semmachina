package stage

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/payload"
)

// TurnTypeSegment is the turn entity's position in the six-part ID.
const TurnTypeSegment = "turn"

// Trigger is one decoded stage trigger.
//
// It carries REFERENCES and nothing else — a turn entity ID and the turn id that
// is its instance segment — which is the whole shape of the rules-carry-
// references discipline at this seam. Everything a stage needs to know about the
// turn it reads from the graph at execution time, so a trigger that sat in the
// stream for an hour drives the stage against the world as it is now rather than
// against a snapshot of the world when the rule fired.
type Trigger struct {
	// TurnEntityID is the turn's six-part entity ID.
	TurnEntityID string
	// TurnID is the turn identity — the instance segment of TurnEntityID.
	TurnID string
	// Subject is the stage subject the trigger arrived on.
	Subject string
}

// rulePublication is the rule engine's own `publish` action payload.
//
// It is upstream's wire shape rather than one of ours, which is why it is
// decoded structurally here instead of through the payload registry: the rule
// engine composes this map itself and stamps no BaseMessage envelope on it. The
// only field the turn loop reads is the entity the rule fired for.
type rulePublication struct {
	EntityID   string         `json:"entity_id"`
	Subject    string         `json:"subject"`
	Source     string         `json:"source"`
	Properties map[string]any `json:"properties"`
}

// ParseTrigger decodes a stage trigger and holds it to the turn's identity
// contract.
//
// Everything it rejects is permanent for that message: bytes that are not the
// rule engine's publication shape, an entity ID that is not canonical, and an
// entity that is not a TURN. The last one is the interesting refusal — a rule
// whose pattern was widened to characters or scenes would otherwise hand a stage
// a perfectly valid entity ID and the stage would advance a phase on something
// that has no phase.
func ParseTrigger(data []byte) (Trigger, error) {
	var published rulePublication
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&published); err != nil {
		return Trigger{}, fmt.Errorf("decode stage trigger: %w", err)
	}
	if published.EntityID == "" {
		return Trigger{}, fmt.Errorf(
			"stage trigger on %q names no entity; a rule payload carries the reference and nothing else, so "+
				"without it there is no turn to run", published.Subject)
	}
	if err := types.ValidateEntityID(published.EntityID); err != nil {
		return Trigger{}, fmt.Errorf(
			"stage trigger names %q, which is not a canonical six-part entity ID: %w", published.EntityID, err)
	}

	parts := strings.Split(published.EntityID, ".")
	if segment := parts[len(parts)-2]; segment != TurnTypeSegment {
		return Trigger{}, fmt.Errorf(
			"stage trigger names %q, whose type segment is %q rather than %q; a stage advances a TURN's phase, "+
				"and nothing else in the world has one", published.EntityID, segment, TurnTypeSegment)
	}
	turnID := parts[len(parts)-1]
	if err := payload.RequireTurnEntityID(turnID, published.EntityID); err != nil {
		return Trigger{}, err
	}

	return Trigger{
		TurnEntityID: published.EntityID,
		TurnID:       turnID,
		Subject:      published.Subject,
	}, nil
}
