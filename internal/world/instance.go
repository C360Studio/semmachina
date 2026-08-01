package world

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// PlayerBinding is the instance-configured link between a human and the
// character they play.
//
// It lives in INSTANCE configuration and never in the package. That is the
// whole difference between a template and a campaign: a package that named its
// own player would be instantiable exactly once, and "worlds are data" would be
// false at the second campaign.
type PlayerBinding struct {
	// LocalID becomes the instance position of the player entity's ID.
	LocalID string
	// Name is the player's display name.
	Name string
	// Character is a `local:` reference into the template naming the character
	// this player controls.
	Character string
}

// InstanceConfig is everything a campaign supplies that the template does not.
type InstanceConfig struct {
	// Org is the federation organization position.
	Org string
	// WorldNS is the world namespace: the position that makes two campaigns
	// from one template disjoint.
	WorldNS string
	// Player is the campaign's player binding.
	Player PlayerBinding
}

// Validate checks the positions this config contributes to every composed ID.
func (c InstanceConfig) Validate() error {
	if err := vocabulary.ValidateIDSegment(c.Org); err != nil {
		return fmt.Errorf("instance org: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(c.WorldNS); err != nil {
		return fmt.Errorf("instance world namespace: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(c.Player.LocalID); err != nil {
		return fmt.Errorf("instance player id: %w", err)
	}
	if strings.TrimSpace(c.Player.Name) == "" {
		return errors.New("instance player name is required")
	}
	if len(c.Player.Name) > payload.MaxFactTextBytes {
		return fmt.Errorf("instance player name is %d bytes, which exceeds the %d-byte fact-text budget",
			len(c.Player.Name), payload.MaxFactTextBytes)
	}
	if _, found := strings.CutPrefix(c.Player.Character, LocalRefPrefix); !found {
		return fmt.Errorf("instance player character must be a %q reference into the template, got %q",
			LocalRefPrefix, c.Player.Character)
	}
	return nil
}

// PlannedEntity is one entity resolved to instance identity, ready to
// materialize.
//
// It carries no timestamp on purpose: a plan is a pure function of the package
// plus the instance config, so two resolutions of the same inputs are equal
// values, and "the mapping is deterministic" is something a test can assert by
// comparison rather than by re-deriving IDs.
type PlannedEntity struct {
	// ID is the composed six-part entity ID.
	ID string
	// Kind is the declared entity kind.
	Kind vocabulary.EntityKind
	// Template is the provenance of this entity.
	Template payload.TemplateRef
	// Facts are the resolved facts, with every reference rewritten to a
	// six-part ID.
	Facts []payload.WorldFact
}

// Payload converts the planned entity into the Graphable the importer
// publishes.
func (e PlannedEntity) Payload(recordedAt time.Time) *payload.WorldEntity {
	facts := make([]payload.WorldFact, len(e.Facts))
	copy(facts, e.Facts)
	return &payload.WorldEntity{
		ID:         e.ID,
		Kind:       e.Kind,
		Template:   e.Template,
		Facts:      facts,
		RecordedAt: recordedAt,
	}
}

// Plan is a package resolved into one world instance.
type Plan struct {
	// Org and WorldNS are the instance positions every ID carries.
	Org     string
	WorldNS string
	// Template identifies the package the plan was resolved from.
	TemplateID      string
	TemplateVersion string
	// Entities are the entities to materialize, in template file order with
	// the instance-configured player entity last.
	Entities []PlannedEntity
	// seal binds the import plan to the exact result Package.Resolve validated.
	// It is deliberately unexported: callers may inspect a plan, but cannot
	// construct or alter one and still present it as validated seed data.
	seal [32]byte
}

// IDs returns every entity ID the plan materializes, in plan order.
func (p *Plan) IDs() []string {
	out := make([]string, 0, len(p.Entities))
	for _, entity := range p.Entities {
		out = append(out, entity.ID)
	}
	return out
}

// Resolve maps a package into one world instance: template-local ids become
// six-part entity IDs, `local:` references become those same IDs, and the
// instance's player entity is appended.
//
// It is deterministic — same package, same org, same namespace, same IDs and
// same order — which is what makes re-import a no-op rather than a second
// world. Nothing here reads a clock, a random source, or the environment.
func (p *Package) Resolve(inst InstanceConfig) (*Plan, error) {
	if _, err := validateMysteryEntities(p.Entities); err != nil {
		return nil, fmt.Errorf("validate mystery package: %w", err)
	}
	if err := inst.Validate(); err != nil {
		return nil, err
	}

	templateRef := func(localID string) payload.TemplateRef {
		return payload.TemplateRef{ID: p.Manifest.ID, Version: p.Manifest.Version, LocalID: localID}
	}

	// Pass one: every local id gets its mapped ID before any reference is
	// rewritten, so a reference to a later line resolves exactly like a
	// reference to an earlier one. Declaration order is not reference order.
	mapped := make(map[string]string, len(p.Entities))
	kinds := make(map[string]vocabulary.EntityKind, len(p.Entities))
	for _, entity := range p.Entities {
		id, err := vocabulary.ComposeEntityID(
			inst.Org, inst.WorldNS, p.Manifest.ID, string(entity.Kind), entity.LocalID)
		if err != nil {
			return nil, &LineError{File: EntitiesFile, Line: entity.Line, Err: err}
		}
		mapped[entity.LocalID] = id
		kinds[entity.LocalID] = entity.Kind
	}

	planned := make([]PlannedEntity, 0, len(p.Entities)+1)
	for _, entity := range p.Entities {
		facts := make([]payload.WorldFact, 0, len(entity.Facts))
		for _, fact := range entity.Facts {
			resolved, err := resolveFact(entity, fact, mapped, kinds)
			if err != nil {
				return nil, &LineError{File: EntitiesFile, Line: entity.Line, Err: err}
			}
			facts = append(facts, resolved)
		}
		planned = append(planned, PlannedEntity{
			ID:       mapped[entity.LocalID],
			Kind:     entity.Kind,
			Template: templateRef(entity.LocalID),
			Facts:    facts,
		})
	}

	player, err := planPlayerEntity(p, inst, mapped, kinds, templateRef)
	if err != nil {
		return nil, err
	}
	planned = append(planned, player)

	plan := &Plan{
		Org:             inst.Org,
		WorldNS:         inst.WorldNS,
		TemplateID:      p.Manifest.ID,
		TemplateVersion: p.Manifest.Version,
		Entities:        planned,
	}
	if err := plan.sealResolved(); err != nil {
		return nil, err
	}
	return plan, nil
}

// resolveFact rewrites one authored fact into instance identity.
//
// The object-KIND check is the reason this needs the kind map and not just the
// id map. A reference that resolves can still point at the wrong sort of thing —
// a character standing inside a crowbar, a character carried like an item — and
// resolution alone would import both. The rule has exactly one other
// enforcement path, the effect applier, and letting a bad object in here does
// not merely delay the rejection: the applier seeds a multi-valued write from
// the entity's CURRENT set, so the next `add_relationship` puts the imported
// sibling back on the wire as part of the complete set the merge lane demands.
// The runtime gate would re-assert, in a write of its own that succeeds, the
// exact fact it would have refused as an intent. Stating the rule once
// (vocabulary.AllowsObjectKind) is the whole point of registering it there.
//
// It runs at RESOLVE rather than at parse because kinds are a whole-package
// fact: a reference may name a later line, so the object's kind is unknown
// while parsing the line that points at it.
func resolveFact(
	entity TemplateEntity,
	fact TemplateFact,
	mapped map[string]string,
	kinds map[string]vocabulary.EntityKind,
) (payload.WorldFact, error) {
	if !fact.IsReference() {
		return payload.WorldFact{Predicate: fact.Predicate, Object: fact.Literal}, nil
	}

	target, declared := mapped[fact.LocalRef]
	if !declared {
		return payload.WorldFact{}, fmt.Errorf(
			"entity %q references %q via %q, which no entity in this package declares",
			entity.LocalID, LocalRefPrefix+fact.LocalRef, fact.Predicate)
	}
	if fact.LocalRef == entity.LocalID {
		return payload.WorldFact{}, fmt.Errorf(
			"entity %q references itself via %q", entity.LocalID, fact.Predicate)
	}
	if kind := kinds[fact.LocalRef]; !vocabulary.AllowsObjectKind(fact.Predicate, kind) {
		allowed, _ := vocabulary.ObjectKindsFor(fact.Predicate)
		return payload.WorldFact{}, fmt.Errorf(
			"entity %q points %q at %q, which is a %q (allowed kinds: %v)",
			entity.LocalID, fact.Predicate, LocalRefPrefix+fact.LocalRef, kind, allowed)
	}
	return payload.WorldFact{Predicate: fact.Predicate, Object: target, Reference: true}, nil
}

// planPlayerEntity materializes the instance's player.
//
// The player entity's ID still carries the template position, because it is
// scoped to a world instance that was made from this template — it is not a
// free-floating account. What the TEMPLATE does not get to decide is that this
// player exists, what they are called, or which character they hold.
func planPlayerEntity(
	pkg *Package,
	inst InstanceConfig,
	mapped map[string]string,
	kinds map[string]vocabulary.EntityKind,
	templateRef func(string) payload.TemplateRef,
) (PlannedEntity, error) {
	local, _ := strings.CutPrefix(inst.Player.Character, LocalRefPrefix)
	if err := vocabulary.ValidateIDSegment(local); err != nil {
		return PlannedEntity{}, fmt.Errorf("instance player character: %w", err)
	}

	character, declared := mapped[local]
	if !declared {
		return PlannedEntity{}, fmt.Errorf(
			"instance player character %q names no entity in template %q",
			inst.Player.Character, pkg.Manifest.ID)
	}
	// Same rule as every other entity-valued reference, asked the same way. A
	// hand-rolled `!= EntityKindCharacter` here read identically and was a
	// SECOND statement of the object-kind rule — the exact divergence
	// referenceObjectKinds exists to prevent.
	if kind := kinds[local]; !vocabulary.AllowsObjectKind(vocabulary.PlayerCharacterCurrent, kind) {
		allowed, _ := vocabulary.ObjectKindsFor(vocabulary.PlayerCharacterCurrent)
		return PlannedEntity{}, fmt.Errorf(
			"instance player character %q is a %q; %q points at %v",
			inst.Player.Character, kind, vocabulary.PlayerCharacterCurrent, allowed)
	}
	if _, taken := mapped[inst.Player.LocalID]; taken {
		return PlannedEntity{}, fmt.Errorf(
			"instance player id %q collides with a template entity of the same local id",
			inst.Player.LocalID)
	}

	id, err := vocabulary.ComposeEntityID(
		inst.Org, inst.WorldNS, pkg.Manifest.ID,
		string(vocabulary.EntityKindPlayer), inst.Player.LocalID)
	if err != nil {
		return PlannedEntity{}, err
	}

	return PlannedEntity{
		ID:       id,
		Kind:     vocabulary.EntityKindPlayer,
		Template: templateRef(inst.Player.LocalID),
		Facts: []payload.WorldFact{
			{Predicate: vocabulary.WorldEntityName, Object: inst.Player.Name},
			{Predicate: vocabulary.PlayerCharacterCurrent, Object: character, Reference: true},
		},
	}, nil
}
