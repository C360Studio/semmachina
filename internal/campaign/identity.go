package campaign

import (
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TypeSegment is the campaign entity's position in the six-part ID.
//
// It is deliberately NOT a vocabulary.EntityKind. Entity kinds are what a WORLD
// PACKAGE may declare — characters, items, scenes — and a campaign is not a
// thing in the fiction: it is engine bookkeeping, created per INSTANCE, that no
// template may author or even name. The same is true of the turn entity. ID
// composition is one rule for both worlds and engine entities
// (vocabulary.ComposeEntityID takes a plain type segment for exactly this
// reason); the vocabulary is not.
const TypeSegment = "campaign"

// InstanceSegment is the campaign entity's instance position.
//
// It is a constant, not a parameter, and that is what makes the campaign entity
// a usable SENTINEL. The gate's whole mechanism is that one atomic create
// answers "has this world been instantiated?" — if the instance position were
// caller-chosen, two boots could claim two different campaign IDs in the same
// namespace, both succeed, and each conclude it had a fresh world. One world
// instance, one campaign, one key.
const InstanceSegment = "main"

// Identity locates a campaign entity inside one world instance.
//
// The three fields are the instance's positions, not the template's content: a
// template never names a campaign, because a template that did could be
// instantiated exactly once.
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
		return fmt.Errorf("campaign org: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(i.WorldNS); err != nil {
		return fmt.Errorf("campaign world namespace: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(i.Template); err != nil {
		return fmt.Errorf("campaign template: %w", err)
	}
	return nil
}

// EntityID composes the campaign entity's canonical six-part ID.
//
// Same composer as every world entity, so the campaign sits in the same
// namespace as the world it governs and a prefix query over the instance finds
// it alongside everything else.
func (i Identity) EntityID() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	return vocabulary.ComposeEntityID(i.Org, i.WorldNS, i.Template, TypeSegment, InstanceSegment)
}
