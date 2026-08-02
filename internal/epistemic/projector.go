package epistemic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// DefaultMaxSupplementalEntities bounds the cumulative unique entities a
	// purpose may add to the independently bounded scene projection.
	DefaultMaxSupplementalEntities = 32
	// DefaultMaxProjectionEntities bounds the complete serialized projection.
	DefaultMaxProjectionEntities = scene.DefaultMaxEntities + DefaultMaxSupplementalEntities
	// DefaultMaxProjectionTriples bounds facts even when many fit on one entity.
	DefaultMaxProjectionTriples = 4096
	// DefaultMaxProjectionBytes bounds the deterministic JSON value snapshot.
	DefaultMaxProjectionBytes = 1 << 20
)

// SceneAssembler is the existing bounded public-scene retrieval.
type SceneAssembler interface {
	Assemble(context.Context, string, string) (*scene.View, error)
}

// Graph is the narrow NATS-direct read surface for purpose-specific records.
type Graph interface {
	GetEntities(context.Context, []string) (graphio.BatchResult, error)
	EntitiesByPredicateValue(context.Context, string, string, int) ([]string, error)
}

var _ Graph = (*graphio.Store)(nil)

// DenouementAuthorizer verifies that a correct accusation authorized solution
// disclosure. Lifecycle phase is checked separately by Projector.
type DenouementAuthorizer interface {
	Authorized(context.Context, string, string, string) (bool, error)
}

// CompanionBondValidator is the single relationship authority consumed by a
// companion projection. It is injected to keep epistemic independent of the
// concrete graph-backed companion package.
type CompanionBondValidator interface {
	ValidateCompanionBond(context.Context, string, string, string) error
}

// Option configures Projector.
type Option func(*Projector)

// WithMaxSupplementalEntities overrides the non-scene entity cap.
func WithMaxSupplementalEntities(limit int) Option {
	return func(p *Projector) { p.maxSupplemental = limit }
}

// WithMaxProjectionEntities overrides the whole-projection entity cap.
func WithMaxProjectionEntities(limit int) Option {
	return func(p *Projector) { p.maxProjectionEntities = limit }
}

// WithMaxProjectionTriples overrides the whole-projection fact cap.
func WithMaxProjectionTriples(limit int) Option {
	return func(p *Projector) { p.maxProjectionTriples = limit }
}

// WithMaxProjectionBytes overrides the deterministic serialized-byte cap.
func WithMaxProjectionBytes(limit int) Option {
	return func(p *Projector) { p.maxProjectionBytes = limit }
}

// WithDenouementAuthorizer installs the deterministic accusation-result gate.
func WithDenouementAuthorizer(authorizer DenouementAuthorizer) Option {
	return func(p *Projector) { p.denouement = authorizer }
}

// WithCompanionBondValidator installs the authoritative durable-bond validator.
func WithCompanionBondValidator(validator CompanionBondValidator) Option {
	return func(p *Projector) { p.companionBonds = validator }
}

// Projector is the centralized authorization and omission boundary.
type Projector struct {
	scenes                SceneAssembler
	graph                 Graph
	scope                 Scope
	maxSupplemental       int
	maxProjectionEntities int
	maxProjectionTriples  int
	maxProjectionBytes    int
	denouement            DenouementAuthorizer
	companionBonds        CompanionBondValidator
}

// NewProjector builds one projector over bounded NATS-direct readers.
func NewProjector(
	scenes SceneAssembler, graphReader Graph, scope Scope, opts ...Option,
) (*Projector, error) {
	if scenes == nil {
		return nil, errors.New("epistemic projector requires the bounded scene assembler")
	}
	if graphReader == nil {
		return nil, errors.New("epistemic projector requires a graph reader")
	}
	projector := &Projector{
		scenes: scenes, graph: graphReader, scope: scope,
		maxSupplemental:       DefaultMaxSupplementalEntities,
		maxProjectionEntities: DefaultMaxProjectionEntities,
		maxProjectionTriples:  DefaultMaxProjectionTriples,
		maxProjectionBytes:    DefaultMaxProjectionBytes,
	}
	for _, option := range opts {
		option(projector)
	}
	if projector.maxSupplemental <= 0 {
		return nil, errors.New("epistemic projector requires a positive supplemental entity cap")
	}
	if projector.maxProjectionEntities <= 0 ||
		projector.maxProjectionTriples <= 0 || projector.maxProjectionBytes <= 0 {
		return nil, errors.New("epistemic projector requires positive output entity, triple, and byte caps")
	}
	return projector, nil
}

// Project returns only values authorized for audience's closed purpose.
func (p *Projector) Project(
	ctx context.Context, audience AuthenticatedAudience,
) (*Projection, error) {
	if !slices.Contains(purposes, audience.purpose) {
		return nil, fmt.Errorf("project context: undeclared purpose %q", audience.purpose)
	}
	if audience.purpose == PurposeOperator {
		return nil, errors.New("operator projection is not a persona surface; use the authenticated operator graph API")
	}
	if err := p.validateAudience(audience); err != nil {
		return nil, err
	}
	if audience.purpose == PurposeVerifier {
		projection, err := p.solutionOnly(ctx, audience.purpose, "", "")
		if err != nil {
			return nil, err
		}
		if err := p.enforceProjectionBounds(projection, nil); err != nil {
			return nil, err
		}
		return projection, nil
	}

	view, err := p.scenes.Assemble(ctx, audience.turnID, audience.turnEntityID)
	if err != nil {
		return nil, fmt.Errorf("assemble bounded public scene: %w", err)
	}
	if view == nil {
		return nil, errors.New("assemble bounded public scene: assembler returned nil")
	}
	if view.TurnID != audience.turnID || view.TurnEntityID != audience.turnEntityID {
		return nil, errors.New("assembled turn identity does not match the authenticated audience scope")
	}
	if !view.Actor.Verified() {
		return nil, fmt.Errorf("pin acting actor from graph: %s", view.Actor.Doubt)
	}
	projection := publicProjection(view, audience.purpose)
	baseEntityIDs := projectionEntityIDs(projection)
	actorID, err := p.authorizedActor(audience, view)
	if err != nil {
		return nil, err
	}

	switch audience.purpose {
	case PurposePublicAdjudicator:
		if err := p.addKnowledge(ctx, projection, actorID); err != nil {
			return nil, err
		}
	case PurposePlayer:
		if err := p.addKnowledge(ctx, projection, actorID); err != nil {
			return nil, err
		}
		if err := p.addRevelations(ctx, projection, actorID, audience.turnID); err != nil {
			return nil, err
		}
	case PurposeCompanion:
		if p.companionBonds == nil {
			return nil, errors.New("companion projection requires the authoritative bond validator")
		}
		if err := p.companionBonds.ValidateCompanionBond(
			ctx, audience.bondID, view.Actor.PlayerID, actorID,
		); err != nil {
			return nil, err
		}
		projection.ContextRef = audience.contextRef
		projection.CompanionID = audience.companionID
		projection.BondID = audience.bondID
		if err := p.addCompanionBond(ctx, projection, audience.bondID); err != nil {
			return nil, err
		}
		if err := p.addKnowledge(ctx, projection, actorID); err != nil {
			return nil, err
		}
	case PurposeNarrator:
		if err := p.addKnowledge(ctx, projection, actorID); err != nil {
			return nil, err
		}
		if err := p.addRevelations(ctx, projection, actorID, audience.turnID); err != nil {
			return nil, err
		}
	case PurposeCasekeeper:
		if err := p.addCasekeeperState(ctx, projection, audience.targetActorIDs); err != nil {
			return nil, err
		}
	case PurposeDenouement:
		if err := p.addKnowledge(ctx, projection, actorID); err != nil {
			return nil, err
		}
		if err := p.addRevelations(ctx, projection, actorID, audience.turnID); err != nil {
			return nil, err
		}
		if err := p.authorizeDenouement(ctx, projection, audience.authorizerRef); err != nil {
			return nil, err
		}
	}

	finalize(projection)
	if err := p.enforceProjectionBounds(projection, baseEntityIDs); err != nil {
		return nil, err
	}
	return projection, nil
}

func (p *Projector) enforceProjectionBounds(
	projection *Projection, baseEntityIDs map[string]bool,
) error {
	entities := projection.Entities()
	if len(entities) > p.maxProjectionEntities {
		return fmt.Errorf("epistemic projection has %d entities; limit is %d",
			len(entities), p.maxProjectionEntities)
	}
	supplemental := 0
	triples := 0
	for _, entity := range entities {
		if !baseEntityIDs[entity.ID] {
			supplemental++
		}
		triples += len(entity.Facts)
	}
	if supplemental > p.maxSupplemental {
		return fmt.Errorf("epistemic projection has %d cumulative supplemental entities; limit is %d",
			supplemental, p.maxSupplemental)
	}
	if triples > p.maxProjectionTriples {
		return fmt.Errorf("epistemic projection has %d triples; limit is %d",
			triples, p.maxProjectionTriples)
	}
	bytes, err := projection.Bytes()
	if err != nil {
		return fmt.Errorf("serialize epistemic projection for byte bound: %w", err)
	}
	if len(bytes) > p.maxProjectionBytes {
		return fmt.Errorf("epistemic projection has %d serialized bytes; limit is %d",
			len(bytes), p.maxProjectionBytes)
	}
	return nil
}

func projectionEntityIDs(projection *Projection) map[string]bool {
	ids := make(map[string]bool)
	for _, entity := range projection.Entities() {
		ids[entity.ID] = true
	}
	return ids
}

func (p *Projector) validateAudience(audience AuthenticatedAudience) error {
	caseBound := audience.purpose == PurposeCasekeeper || audience.purpose == PurposeCompanion ||
		audience.purpose == PurposeVerifier || audience.purpose == PurposeDenouement
	caseBound = caseBound && audience.purpose != PurposeCompanion
	if caseBound && (strings.TrimSpace(audience.caseID) == "" || audience.caseID != p.scope.caseID) {
		return errors.New("epistemic audience case does not match the projector's resolved world scope")
	}
	turnBound := audience.purpose != PurposeVerifier && audience.purpose != PurposeOperator
	if turnBound && (strings.TrimSpace(audience.turnID) == "" ||
		strings.TrimSpace(audience.turnEntityID) == "") {
		return errors.New("epistemic audience requires exact turn and turn-entity IDs")
	}
	if len(audience.targetActorIDs) > MaxCasekeeperTargetActors {
		return fmt.Errorf("casekeeper audience has %d target actors; limit is %d",
			len(audience.targetActorIDs), MaxCasekeeperTargetActors)
	}
	seenTargets := make(map[string]bool, len(audience.targetActorIDs))
	for _, targetActorID := range audience.targetActorIDs {
		if strings.TrimSpace(targetActorID) == "" {
			return errors.New("casekeeper audience contains an empty target actor ID")
		}
		if seenTargets[targetActorID] {
			return fmt.Errorf("casekeeper audience repeats target actor %s", targetActorID)
		}
		seenTargets[targetActorID] = true
	}
	if audience.purpose == PurposeCompanion && (strings.TrimSpace(audience.companionID) == "" ||
		strings.TrimSpace(audience.bondID) == "" || strings.TrimSpace(audience.contextRef) == "") {
		return errors.New("companion audience requires exact generic context, companion, and bond IDs")
	}
	if audience.purpose == PurposeDenouement && strings.TrimSpace(audience.authorizerRef) == "" {
		return errors.New("denouement audience requires an exact authorization reference")
	}
	return nil
}

func (p *Projector) authorizedActor(audience AuthenticatedAudience, view *scene.View) (string, error) {
	graphActor := view.Actor.CharacterID
	switch audience.purpose {
	case PurposeCompanion:
		return audience.companionID, nil
	default:
		return graphActor, nil
	}
}

func (p *Projector) queryIDs(
	ctx context.Context, predicate vocabulary.Predicate, value string,
) ([]string, error) {
	ids, err := p.graph.EntitiesByPredicateValue(
		ctx, predicate.String(), value, p.maxSupplemental+1)
	if err != nil {
		return nil, fmt.Errorf("query %s = %q: %w", predicate, value, err)
	}
	if len(ids) > p.maxSupplemental {
		return nil, fmt.Errorf("query %s = %q returned at least %d entities; limit is %d",
			predicate, value, len(ids), p.maxSupplemental)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (p *Projector) hydrate(ctx context.Context, ids []string) ([]graph.EntityState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > p.maxSupplemental {
		return nil, fmt.Errorf("supplemental hydration requests %d entities; limit is %d",
			len(ids), p.maxSupplemental)
	}
	result, err := p.graph.GetEntities(ctx, ids)
	if err != nil {
		return nil, err
	}
	states := make([]graph.EntityState, 0, len(result.Entities))
	requested := make(map[string]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	seen := make(map[string]bool, len(result.Entities))
	for _, state := range result.Entities {
		if !requested[state.ID] {
			return nil, fmt.Errorf("supplemental hydration returned unrequested entity %s", state.ID)
		}
		if seen[state.ID] {
			return nil, fmt.Errorf("supplemental hydration returned entity %s more than once", state.ID)
		}
		seen[state.ID] = true
		if state.ID == "" || state.IsStub() {
			continue
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	return states, nil
}

func publicProjection(view *scene.View, purpose Purpose) *Projection {
	turnPredicates := publicTurnPredicates
	if purpose == PurposeNarrator || purpose == PurposeDenouement {
		turnPredicates = narrationTurnPredicates
	}
	projection := &Projection{
		Purpose: purpose, TurnID: view.TurnID, TurnEntityID: view.TurnEntityID, SceneID: view.SceneID,
		Actor: Actor{PlayerID: view.Actor.PlayerID, CharacterID: view.Actor.CharacterID},
		Turn:  projectSceneEntity(view.Turn, turnPredicates),
		Scene: projectSceneEntity(view.Scene, publicWorldPredicates),
	}
	for _, member := range view.Members {
		if publicEntityKind(member) {
			projection.Members = append(projection.Members, projectSceneEntity(member, publicWorldPredicates))
		}
	}
	for _, neighbour := range view.Neighbours {
		if publicEntityKind(neighbour) {
			projection.Neighbours = append(projection.Neighbours, projectSceneEntity(neighbour, publicWorldPredicates))
		}
	}
	return projection
}

func projectSceneEntity(entity scene.Entity, allowed map[vocabulary.Predicate]bool) Entity {
	return projectTriples(entity.ID, entity.Triples, allowed)
}

func projectState(state graph.EntityState, allowed map[vocabulary.Predicate]bool) Entity {
	return projectTriples(state.ID, state.Triples, allowed)
}

func projectTriples(id string, triples []message.Triple, allowed map[vocabulary.Predicate]bool) Entity {
	entity := Entity{ID: id}
	for _, triple := range triples {
		predicate := vocabulary.Predicate(triple.Predicate)
		if allowed[predicate] {
			entity.Facts = append(entity.Facts, Fact{Predicate: predicate, Object: triple.Object})
		}
	}
	sort.Slice(entity.Facts, func(i, j int) bool {
		left, right := entity.Facts[i], entity.Facts[j]
		if left.Predicate != right.Predicate {
			return left.Predicate < right.Predicate
		}
		return canonicalObjectKey(left.Object) < canonicalObjectKey(right.Object)
	})
	return entity
}

func canonicalObjectKey(object any) string {
	encoded, err := json.Marshal(object)
	if err != nil {
		// The whole-projection byte check returns the serialization error before
		// this value can leave Projector. The type-prefixed fallback keeps sort
		// behavior deterministic on that refused path too.
		return fmt.Sprintf("%T:<unserializable>", object)
	}
	return fmt.Sprintf("%T:%s", object, encoded)
}

func publicEntityKind(entity scene.Entity) bool {
	objects := entity.Objects(vocabulary.WorldEntityKind)
	if len(objects) != 1 {
		return false
	}
	kind, ok := objects[0].(string)
	if !ok {
		return false
	}
	switch vocabulary.EntityKind(kind) {
	case vocabulary.EntityKindCharacter, vocabulary.EntityKindItem,
		vocabulary.EntityKindScene, vocabulary.EntityKindPlayer:
		return true
	default:
		return false
	}
}

func finalize(projection *Projection) {
	projection.Members = dedupeSorted(projection.Members, projection.Turn.ID, projection.Scene.ID)
	memberIDs := make(map[string]bool, len(projection.Members)+2)
	memberIDs[projection.Turn.ID] = projection.Turn.ID != ""
	memberIDs[projection.Scene.ID] = projection.Scene.ID != ""
	for _, entity := range projection.Members {
		memberIDs[entity.ID] = true
	}
	projection.Neighbours = dedupeSorted(projection.Neighbours, mapsKeys(memberIDs)...)
	allowedIDs := make(map[string]bool)
	for _, entity := range projection.Entities() {
		allowedIDs[entity.ID] = true
	}
	projection.Turn = pruneDangling(projection.Turn, allowedIDs)
	projection.Scene = pruneDangling(projection.Scene, allowedIDs)
	for index := range projection.Members {
		projection.Members[index] = pruneDangling(projection.Members[index], allowedIDs)
	}
	for index := range projection.Neighbours {
		projection.Neighbours[index] = pruneDangling(projection.Neighbours[index], allowedIDs)
	}
}

func dedupeSorted(entities []Entity, excluded ...string) []Entity {
	exclude := make(map[string]bool, len(excluded))
	for _, id := range excluded {
		exclude[id] = id != ""
	}
	byID := make(map[string]Entity, len(entities))
	for _, entity := range entities {
		if entity.ID != "" && !exclude[entity.ID] {
			byID[entity.ID] = entity
		}
	}
	ids := mapsKeys(byID)
	slices.Sort(ids)
	out := make([]Entity, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

func mapsKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func pruneDangling(entity Entity, allowedIDs map[string]bool) Entity {
	facts := entity.Facts[:0]
	for _, fact := range entity.Facts {
		if vocabulary.IsEntityReference(fact.Predicate) {
			id, ok := fact.Object.(string)
			if !ok || !allowedIDs[id] {
				continue
			}
		}
		facts = append(facts, fact)
	}
	entity.Facts = facts
	return entity
}

var publicWorldPredicates = predicateSet(
	vocabulary.WorldEntityName,
	vocabulary.WorldEntityKind,
	vocabulary.WorldEntityDescription,
	vocabulary.PlayerCharacterCurrent,
	vocabulary.CharacterStatusCurrent,
	vocabulary.WorldLocationCurrent,
	vocabulary.CharacterAttributeHealth,
	vocabulary.CharacterAttributeStamina,
	vocabulary.CharacterAttributeResolve,
	vocabulary.ItemAttributeQuantity,
	vocabulary.SceneAttributeTension,
	vocabulary.WorldRelationAlliedWith,
	vocabulary.WorldRelationHostileTo,
	vocabulary.WorldRelationKnows,
	vocabulary.WorldRelationCarries,
	vocabulary.WorldRelationOwesDebt,
)

var publicTurnPredicates = predicateSet(
	vocabulary.TurnPhaseCurrent,
	vocabulary.TurnActionRef,
	vocabulary.TurnActionPlayer,
	vocabulary.TurnActionScene,
	vocabulary.TurnVerdictPlausibility,
	vocabulary.TurnVerdictRisk,
	vocabulary.TurnVerdictConsequence,
	vocabulary.TurnVerdictRequiresRoll,
	vocabulary.TurnVerdictRef,
	vocabulary.TurnRollBand,
	vocabulary.TurnRollTotal,
	vocabulary.TurnRollRef,
	vocabulary.TurnEffectsBatch,
	vocabulary.TurnEffectsRef,
	vocabulary.TurnNarrationRef,
	vocabulary.TurnResumeAttempts,
	vocabulary.TurnFailureReason,
	vocabulary.TurnFailureRef,
)

var narrationTurnPredicates = func() map[vocabulary.Predicate]bool {
	allowed := make(map[vocabulary.Predicate]bool, len(publicTurnPredicates)+3)
	for predicate := range publicTurnPredicates {
		allowed[predicate] = true
	}
	allowed[vocabulary.TurnKnowledgeRef] = true
	allowed[vocabulary.TurnCompanionStageRef] = true
	allowed[vocabulary.TurnCompanionDecisionRef] = true
	return allowed
}()

var evidencePredicates = predicateSet(
	vocabulary.WorldEntityName,
	vocabulary.WorldEntityKind,
	vocabulary.WorldEntityDescription,
)

var casekeeperPredicates = func() map[vocabulary.Predicate]bool {
	allowed := predicateSet(
		vocabulary.WorldEntityName,
		vocabulary.WorldEntityKind,
		vocabulary.WorldEntityDescription,
		vocabulary.CaseSolutionCulprit,
		vocabulary.CaseSolutionMethod,
		vocabulary.CaseSolutionMotive,
		vocabulary.CaseLifecyclePhase,
		vocabulary.CaseRequirementSuspects,
		vocabulary.CaseRequirementEvidence,
		vocabulary.CaseMemberSuspect,
		vocabulary.CaseMemberEvidence,
		vocabulary.CaseMemberTimeline,
		vocabulary.CaseMemberVictim,
		vocabulary.CaseTimelineOrder,
		vocabulary.EvidenceTruthStatusCurrent,
		vocabulary.EvidenceRevealPhase,
		vocabulary.EvidenceRevealKindPredicate,
		vocabulary.EvidenceRevealTarget,
		vocabulary.BeliefActorHolder,
		vocabulary.BeliefEvidenceRef,
		vocabulary.BeliefStanceCurrent,
	)
	return allowed
}()

func predicateSet(predicates ...vocabulary.Predicate) map[vocabulary.Predicate]bool {
	set := make(map[vocabulary.Predicate]bool, len(predicates))
	for _, predicate := range predicates {
		set[predicate] = true
	}
	return set
}
