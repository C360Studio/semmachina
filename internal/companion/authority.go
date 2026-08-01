// Package companion owns durable player-companion bond authority and the
// structural companion decision commit boundary.
package companion

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// ErrBondIntegrity marks permanent malformed or ambiguous bond state.
var ErrBondIntegrity = errors.New("companion bond integrity failure")

// Graph is the authoritative NATS-direct bond read surface.
type Graph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	EntitiesByPredicateValue(context.Context, string, string, int) ([]string, error)
}

var _ Graph = (*graphio.Store)(nil)

// Bond is the verified structural relationship used by every authorization path.
type Bond struct {
	ID, PlayerID, CharacterID string
	Policy                    vocabulary.CompanionPolicy
	HintLevel                 vocabulary.HintLevel
}

// Authority is the one graph-backed interpreter of companion bonds.
type Authority struct{ graph Graph }

// NewAuthority builds the bond authority.
func NewAuthority(reader Graph) (*Authority, error) {
	if reader == nil {
		return nil, errors.New("companion authority requires a graph reader")
	}
	return &Authority{graph: reader}, nil
}

// ValidateCompanionBond implements epistemic.CompanionBondValidator using the
// same complete authority used by sharing, witnessing, and terminal execution.
func (a *Authority) ValidateCompanionBond(
	ctx context.Context, bondID, playerID, companionID string,
) error {
	_, err := a.ValidateBond(ctx, bondID, playerID, companionID)
	return err
}

// ActiveBondForPlayer returns nil for no companion and fails on ambiguity.
func (a *Authority) ActiveBondForPlayer(ctx context.Context, playerID string) (*Bond, error) {
	if err := requireKindID("player_id", playerID, vocabulary.EntityKindPlayer); err != nil {
		return nil, err
	}
	ids, err := a.query(ctx, vocabulary.CompanionBondPlayer, playerID, 2)
	if err != nil {
		return nil, err
	}
	switch len(ids) {
	case 0:
		return nil, nil
	case 1:
		return a.ValidateBond(ctx, ids[0], playerID, "")
	default:
		return nil, integrity("player %s has %d active companion bonds", playerID, len(ids))
	}
}

// ValidateBond re-reads and verifies the complete bond and its referents.
func (a *Authority) ValidateBond(
	ctx context.Context, bondID, expectedPlayerID, expectedCompanionID string,
) (*Bond, error) {
	if err := requireKindID("bond_id", bondID, vocabulary.EntityKindCompanionBond); err != nil {
		return nil, integrity("%v", err)
	}
	state, err := a.graph.GetEntity(ctx, bondID)
	if err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return nil, integrity("bond %s is missing", bondID)
		}
		return nil, fmt.Errorf("read companion bond %s: %w", bondID, err)
	}
	if state == nil || state.IsStub() {
		return nil, integrity("bond %s is missing or a referential stub", bondID)
	}
	if err := requireStateKind(state, vocabulary.EntityKindCompanionBond); err != nil {
		return nil, err
	}
	playerID, err := exactImportedString(state, vocabulary.CompanionBondPlayer)
	if err != nil {
		return nil, err
	}
	companionID, err := exactImportedString(state, vocabulary.CompanionBondCharacter)
	if err != nil {
		return nil, err
	}
	policyText, err := exactImportedString(state, vocabulary.CompanionBondPolicy)
	if err != nil {
		return nil, err
	}
	policy, err := vocabulary.ParseCompanionPolicy(policyText)
	if err != nil {
		return nil, integrity("bond %s policy: %v", bondID, err)
	}
	hintText, err := exactString(state, vocabulary.CompanionBondHintLevel)
	if err != nil {
		return nil, err
	}
	hint, err := vocabulary.ParseHintLevel(hintText)
	if err != nil {
		return nil, integrity("bond %s hint level: %v", bondID, err)
	}
	if expectedPlayerID != "" && playerID != expectedPlayerID {
		return nil, integrity("bond %s names player %s, want %s", bondID, playerID, expectedPlayerID)
	}
	if expectedCompanionID != "" && companionID != expectedCompanionID {
		return nil, integrity("bond %s names companion %s, want %s", bondID, companionID, expectedCompanionID)
	}

	player, err := a.graph.GetEntity(ctx, playerID)
	if err != nil {
		return nil, a.referentError("player", playerID, err)
	}
	if err := requireStateKind(player, vocabulary.EntityKindPlayer); err != nil {
		return nil, err
	}
	controlled, err := exactImportedString(player, vocabulary.PlayerCharacterCurrent)
	if err != nil {
		return nil, err
	}
	if controlled == companionID {
		return nil, integrity("companion %s is also player %s's controlled character", companionID, playerID)
	}
	companionState, err := a.graph.GetEntity(ctx, companionID)
	if err != nil {
		return nil, a.referentError("companion", companionID, err)
	}
	if err := requireStateKind(companionState, vocabulary.EntityKindCharacter); err != nil {
		return nil, err
	}
	candidateText, err := exactImportedString(companionState, vocabulary.CompanionCandidatePolicy)
	if err != nil {
		return nil, fmt.Errorf("%w: companion candidate: %v", ErrBondIntegrity, err)
	}
	candidate, err := vocabulary.ParseCompanionPolicy(candidateText)
	if err != nil {
		return nil, integrity("companion %s candidate policy: %v", companionID, err)
	}
	if candidate == vocabulary.CompanionPolicyReactive && policy == vocabulary.CompanionPolicyBoundedInitiative {
		return nil, integrity("bond %s policy %s is wider than candidate policy %s", bondID, policy, candidate)
	}

	playerBonds, err := a.query(ctx, vocabulary.CompanionBondPlayer, playerID, 2)
	if err != nil {
		return nil, err
	}
	if len(playerBonds) != 1 || playerBonds[0] != bondID {
		return nil, integrity("player %s has %d active companion bonds", playerID, len(playerBonds))
	}
	companionBonds, err := a.query(ctx, vocabulary.CompanionBondCharacter, companionID, 2)
	if err != nil {
		return nil, err
	}
	if len(companionBonds) != 1 || companionBonds[0] != bondID {
		return nil, integrity("companion %s is bonded to %d players", companionID, len(companionBonds))
	}
	parsedPlayer, _ := types.ParseEntityID(playerID)
	expectedID, err := world.CompanionBondID(
		parsedPlayer.Org, parsedPlayer.Domain, parsedPlayer.System, playerID, companionID)
	if err != nil {
		return nil, integrity("derive bond identity: %v", err)
	}
	if bondID != expectedID {
		return nil, integrity("bond id %s does not match deterministic identity %s", bondID, expectedID)
	}
	return &Bond{ID: bondID, PlayerID: playerID, CharacterID: companionID, Policy: policy, HintLevel: hint}, nil
}

// Authorized implements knowledge.ShareAuthorizer. The evidence parameter is
// intentionally not used for bond admission; knowledge preflight has already
// proved the source knows each cited evidence item before this method runs.
func (a *Authority) Authorized(
	ctx context.Context, sourceActorID, recipientID, evidenceID string,
) (bool, error) {
	if err := requireKindID("source actor", sourceActorID, vocabulary.EntityKindCharacter); err != nil {
		return false, err
	}
	if err := requireKindID("recipient", recipientID, vocabulary.EntityKindCharacter); err != nil {
		return false, err
	}
	if err := requireKindID("evidence", evidenceID, vocabulary.EntityKindEvidence); err != nil {
		return false, err
	}
	players, err := a.query(ctx, vocabulary.PlayerCharacterCurrent, sourceActorID, 2)
	if err != nil {
		return false, err
	}
	if len(players) == 0 {
		return false, nil
	}
	if len(players) > 1 {
		return false, integrity("character %s is controlled by %d players", sourceActorID, len(players))
	}
	bond, err := a.ActiveBondForPlayer(ctx, players[0])
	if err != nil {
		return false, err
	}
	return bond != nil && bond.CharacterID == recipientID, nil
}

// Witness implements knowledge.WitnessAuthorizer. It returns the single bonded
// companion only when actor and companion share the action's structural location.
func (a *Authority) Witness(
	ctx context.Context, sourceActorID string, targetRefs []string,
) (string, bool, error) {
	players, err := a.query(ctx, vocabulary.PlayerCharacterCurrent, sourceActorID, 2)
	if err != nil {
		return "", false, err
	}
	if len(players) == 0 {
		return "", false, nil
	}
	if len(players) > 1 {
		return "", false, integrity("character %s is controlled by %d players", sourceActorID, len(players))
	}
	bond, err := a.ActiveBondForPlayer(ctx, players[0])
	if err != nil || bond == nil {
		return "", false, err
	}
	source, err := a.graph.GetEntity(ctx, sourceActorID)
	if err != nil {
		return "", false, fmt.Errorf("read witnessing actor %s: %w", sourceActorID, err)
	}
	companionState, err := a.graph.GetEntity(ctx, bond.CharacterID)
	if err != nil {
		return "", false, fmt.Errorf("read witnessing companion %s: %w", bond.CharacterID, err)
	}
	sourceLocation, err := exactString(source, vocabulary.WorldLocationCurrent)
	if err != nil {
		return "", false, nil
	}
	companionLocation, err := exactString(companionState, vocabulary.WorldLocationCurrent)
	if err != nil {
		return "", false, nil
	}
	if sourceLocation != companionLocation || !slices.Contains(targetRefs, sourceLocation) {
		return "", false, nil
	}
	return bond.CharacterID, true, nil
}

func (a *Authority) query(
	ctx context.Context, predicate vocabulary.Predicate, value string, limit int,
) ([]string, error) {
	ids, err := a.graph.EntitiesByPredicateValue(ctx, predicate.String(), value, limit)
	if err != nil {
		return nil, fmt.Errorf("query %s = %s: %w", predicate, value, err)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (a *Authority) referentError(kind, id string, err error) error {
	if errors.Is(err, graphio.ErrEntityNotFound) {
		return integrity("bond %s referent %s is missing", kind, id)
	}
	return fmt.Errorf("read bond %s %s: %w", kind, id, err)
}

func requireStateKind(state *graph.EntityState, want vocabulary.EntityKind) error {
	if state == nil || state.IsStub() {
		return integrity("%s referent is missing or a stub", want)
	}
	wantMessage := (&payload.WorldEntity{}).Schema()
	if state.MessageType != wantMessage {
		return integrity("entity %s message type is %s, want imported world type %s",
			state.ID, state.MessageType, wantMessage)
	}
	value, err := exactImportedString(state, vocabulary.WorldEntityKind)
	if err != nil {
		return err
	}
	if value != string(want) {
		return integrity("entity %s has kind %s, want %s", state.ID, value, want)
	}
	if err := requireKindID("entity", state.ID, want); err != nil {
		return integrity("%v", err)
	}
	return nil
}

func exactImportedString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var matches []message.Triple
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			matches = append(matches, triple)
		}
	}
	if len(matches) != 1 {
		return "", integrity("entity %s carries %d values for %s, want exactly one",
			state.ID, len(matches), predicate)
	}
	if matches[0].Source != payload.WorldImportSource || strings.TrimSpace(matches[0].Context) == "" {
		return "", integrity("entity %s predicate %s lacks world-import template provenance",
			state.ID, predicate)
	}
	parsed, err := types.ParseEntityID(state.ID)
	if err != nil {
		return "", integrity("entity %s provenance cannot be matched to its identity: %v", state.ID, err)
	}
	provenance := strings.SplitN(matches[0].Context, "@", 2)
	if len(provenance) != 2 || provenance[0] != parsed.System || strings.TrimSpace(provenance[1]) == "" {
		return "", integrity("entity %s predicate %s has inconsistent template provenance %q",
			state.ID, predicate, matches[0].Context)
	}
	value, ok := matches[0].Object.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", integrity("entity %s predicate %s is not one non-empty string", state.ID, predicate)
	}
	return value, nil
}

func exactString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var values []string
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return "", integrity("entity %s has non-string or empty %s", state.ID, predicate)
		}
		values = append(values, value)
	}
	if len(values) != 1 {
		return "", integrity("entity %s carries %d values for %s, want exactly one", state.ID, len(values), predicate)
	}
	return values[0], nil
}

func requireKindID(field, value string, want vocabulary.EntityKind) error {
	parsed, err := types.ParseEntityID(value)
	if err != nil {
		return fmt.Errorf("%s %q is not canonical: %w", field, value, err)
	}
	if parsed.Type != string(want) {
		return fmt.Errorf("%s %q has type %s, want %s", field, value, parsed.Type, want)
	}
	return nil
}

func integrity(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBondIntegrity, fmt.Sprintf(format, args...))
}
