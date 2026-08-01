// Package epistemic is the single authorization and omission boundary for
// persona-visible graph context.
package epistemic

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Purpose is the closed reason an authenticated caller requests a projection.
type Purpose string

// The closed projection purposes.
const (
	PurposeCasekeeper        Purpose = "casekeeper"
	PurposePlayer            Purpose = "player"
	PurposeCompanion         Purpose = "companion"
	PurposePublicAdjudicator Purpose = "public-adjudicator"
	PurposeNarrator          Purpose = "narrator"
	PurposeDenouement        Purpose = "denouement"
	PurposeVerifier          Purpose = "verifier"
	PurposeOperator          Purpose = "operator"
)

var purposes = []Purpose{
	PurposeCasekeeper,
	PurposePlayer,
	PurposeCompanion,
	PurposePublicAdjudicator,
	PurposeNarrator,
	PurposeDenouement,
	PurposeVerifier,
	PurposeOperator,
}

// Purposes returns every authorized projection purpose.
func Purposes() []Purpose { return slices.Clone(purposes) }

// ParsePurpose accepts only the closed purpose set.
func ParsePurpose(value string) (Purpose, error) {
	purpose := Purpose(value)
	if slices.Contains(purposes, purpose) {
		return purpose, nil
	}
	return "", fmt.Errorf("epistemic purpose %q is not declared (allowed: %v)", value, purposes)
}

// AuthenticatedAudience is minted only by purpose-specific constructors. Its
// authorization details are private so callers cannot assemble a more
// privileged identity with a struct literal.
type AuthenticatedAudience struct {
	purpose        Purpose
	turnID         string
	turnEntityID   string
	caseID         string
	contextRef     string
	targetActorIDs []string
	companionID    string
	bondID         string
	authorizerRef  string
}

// Purpose returns the audience's closed authorization purpose.
func (a AuthenticatedAudience) Purpose() Purpose { return a.purpose }

// TurnIdentity returns the immutable turn coordinates bound by the audience
// constructor. Empty values identify purposes, such as verifier, that do not
// read turn context.
func (a AuthenticatedAudience) TurnIdentity() (string, string) {
	return a.turnID, a.turnEntityID
}

// CasekeeperAudience authorizes the engine's private case interpreter for the
// supplied target actors' scoped belief records.
func CasekeeperAudience(
	caseID, turnID, turnEntityID string, targetActorIDs ...string,
) AuthenticatedAudience {
	return AuthenticatedAudience{
		purpose: PurposeCasekeeper, caseID: caseID, turnID: turnID, turnEntityID: turnEntityID,
		targetActorIDs: slices.Clone(targetActorIDs),
	}
}

// PlayerAudience authorizes the graph-pinned actor for one exact turn.
func PlayerAudience(turnID, turnEntityID string) AuthenticatedAudience {
	return AuthenticatedAudience{purpose: PurposePlayer, turnID: turnID, turnEntityID: turnEntityID}
}

// CompanionAudience authorizes one exact companion through one exact scoped
// bond while retaining the graph-pinned player turn.
func CompanionAudience(
	turnID, turnEntityID, contextRef, companionID, bondID string,
) AuthenticatedAudience {
	return AuthenticatedAudience{
		purpose: PurposeCompanion, turnID: turnID, turnEntityID: turnEntityID,
		contextRef: contextRef, companionID: companionID, bondID: bondID,
	}
}

// CompanionIdentity returns the generic context and verified relationship coordinates.
func (a AuthenticatedAudience) CompanionIdentity() (contextRef, companionID, bondID string) {
	return a.contextRef, a.companionID, a.bondID
}

// PublicAdjudicatorAudience derives the acting actor from graph state.
func PublicAdjudicatorAudience(turnID, turnEntityID string) AuthenticatedAudience {
	return AuthenticatedAudience{
		purpose: PurposePublicAdjudicator, turnID: turnID, turnEntityID: turnEntityID,
	}
}

// NarratorAudience derives the acting actor from graph state.
func NarratorAudience(turnID, turnEntityID string) AuthenticatedAudience {
	return AuthenticatedAudience{purpose: PurposeNarrator, turnID: turnID, turnEntityID: turnEntityID}
}

// DenouementAudience requests the gated terminal narration projection.
func DenouementAudience(
	turnID, turnEntityID, caseID, authorizerRef string,
) AuthenticatedAudience {
	return AuthenticatedAudience{
		purpose: PurposeDenouement, turnID: turnID, turnEntityID: turnEntityID,
		caseID: caseID, authorizerRef: authorizerRef,
	}
}

// VerifierAudience requests only the canonical solution identity tuple.
func VerifierAudience(caseID string) AuthenticatedAudience {
	return AuthenticatedAudience{purpose: PurposeVerifier, caseID: caseID}
}

// OperatorAudience is intentionally rejected by Projector; operators use the
// separate graph/operator surface rather than persona projection.
func OperatorAudience() AuthenticatedAudience {
	return AuthenticatedAudience{purpose: PurposeOperator}
}

// Fact is one authorized predicate/object value. It carries no source,
// timestamp, graph client, or lazy resolver.
type Fact struct {
	Predicate vocabulary.Predicate `json:"predicate"`
	Object    any                  `json:"object"`
}

// Entity is one authorized value entity.
type Entity struct {
	ID    string `json:"id"`
	Facts []Fact `json:"facts"`
}

// Objects returns every authorized object for predicate.
func (e Entity) Objects(predicate vocabulary.Predicate) []any {
	var values []any
	for _, fact := range e.Facts {
		if fact.Predicate == predicate {
			values = append(values, fact.Object)
		}
	}
	return values
}

// Actor is the graph-pinned player and current character for this turn.
type Actor struct {
	PlayerID    string `json:"player_id"`
	CharacterID string `json:"character_id"`
}

// Solution is the exact canonical identity tuple. It is absent unless
// HasSolution is true and never carries descriptive target entities implicitly.
type Solution struct {
	Culprit string `json:"culprit"`
	Method  string `json:"method"`
	Motive  string `json:"motive"`
}

// Projection is an immutable-by-convention value snapshot. It contains no
// exclusion identifiers, graph clients, readers, callbacks, or lazy handles.
type Projection struct {
	Purpose      Purpose  `json:"purpose"`
	TurnID       string   `json:"turn_id,omitempty"`
	TurnEntityID string   `json:"turn_entity_id,omitempty"`
	SceneID      string   `json:"scene_id,omitempty"`
	ContextRef   string   `json:"context_ref,omitempty"`
	CompanionID  string   `json:"companion_id,omitempty"`
	BondID       string   `json:"bond_id,omitempty"`
	Actor        Actor    `json:"actor"`
	Turn         Entity   `json:"turn"`
	Scene        Entity   `json:"scene"`
	Members      []Entity `json:"members"`
	Neighbours   []Entity `json:"neighbours"`
	HasSolution  bool     `json:"has_solution"`
	Solution     Solution `json:"solution"`
}

// Entities returns every authorized entity in deterministic category order.
func (p Projection) Entities() []Entity {
	entities := make([]Entity, 0, 2+len(p.Members)+len(p.Neighbours))
	if p.Turn.ID != "" {
		entities = append(entities, p.Turn)
	}
	if p.Scene.ID != "" {
		entities = append(entities, p.Scene)
	}
	entities = append(entities, p.Members...)
	return append(entities, p.Neighbours...)
}

// Entity returns an authorized entity by ID.
func (p Projection) Entity(id string) (Entity, bool) {
	for _, entity := range p.Entities() {
		if entity.ID == id {
			return entity, true
		}
	}
	return Entity{}, false
}

// Bytes serializes the already-sorted value projection deterministically.
func (p Projection) Bytes() ([]byte, error) { return json.Marshal(p) }
