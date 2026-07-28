package turn

import (
	"fmt"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TypeSegment is the turn entity's position in the six-part ID.
//
// Like the campaign's, it is deliberately NOT a vocabulary.EntityKind. Entity
// kinds are what a WORLD PACKAGE may declare — characters, items, scenes — and a
// turn is not a thing in the fiction: it is engine bookkeeping created per
// action, which no template may author or name. ID composition is one rule for
// world entities and engine entities alike; the vocabulary is not.
const TypeSegment = "turn"

// Identity locates turn entities inside one world instance.
//
// The three fields are the instance's positions, exactly as the campaign gate's
// are, so a turn lands in the same namespace as the world it happened in and a
// prefix query over the instance finds it alongside everything else.
type Identity struct {
	// Org is the federation organization position.
	Org string
	// WorldNS is the world namespace — the position that makes two campaigns
	// from one template disjoint.
	WorldNS string
	// Template is the template the world instance was materialized from.
	Template string
}

// Validate checks each contributed position against the entity-ID segment rule.
func (i Identity) Validate() error {
	if err := vocabulary.ValidateIDSegment(i.Org); err != nil {
		return fmt.Errorf("turn org: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(i.WorldNS); err != nil {
		return fmt.Errorf("turn world namespace: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(i.Template); err != nil {
		return fmt.Errorf("turn template: %w", err)
	}
	return nil
}

// EntityID composes the six-part ID for one turn.
//
// turnID becomes the INSTANCE segment, which is the binding
// payload.RequireTurnEntityID asserts everywhere the two are paired. Composition
// is total and pure: the same action always resolves to the same turn entity,
// which is what makes the duplicate-delivery guard work at all.
func (i Identity) EntityID(turnID string) (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	if err := vocabulary.ValidateIDSegment(turnID); err != nil {
		return "", fmt.Errorf("turn id: %w", err)
	}
	return vocabulary.ComposeEntityID(i.Org, i.WorldNS, i.Template, TypeSegment, turnID)
}

// EntityIDForAction composes the turn entity ID an action resolves to.
//
// It is the whole identity chain in one call — action_id → turn_id → entity ID —
// because that chain is exactly where a wiring bug would put two names on one
// turn. A caller that derived the turn id itself and composed separately could
// pair them wrong; here it cannot.
func (i Identity) EntityIDForAction(actionID string) (turnID, entityID string, err error) {
	// The RAW action id is checked as well as the derived turn id, because the
	// prefix launders some illegal values into legal ones: `-leading` cannot
	// start an ID segment, and `turn--leading` can. An action id that only
	// becomes legal by being prefixed is one an ingress adapter should never
	// have minted, and accepting it here would put a value in the graph that no
	// other gate in the slice would have passed.
	if err := vocabulary.ValidateIDSegment(actionID); err != nil {
		return "", "", fmt.Errorf("action id: %w", err)
	}
	turnID = payload.TurnIDForAction(actionID)
	entityID, err = i.EntityID(turnID)
	if err != nil {
		return "", "", err
	}
	return turnID, entityID, nil
}
