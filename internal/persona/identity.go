package persona

import (
	"fmt"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// requireEntityID rejects anything that is not a canonical six-part entity ID.
//
// The rule is upstream's; this wrapper adds the name of the field that carried
// the bad value, which is the whole difference between a diagnosable spawn
// failure and a parser complaint about a string.
func requireEntityID(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if err := types.ValidateEntityID(value); err != nil {
		return fmt.Errorf("%s %q is not a canonical six-part entity ID: %w", field, value, err)
	}
	return nil
}

// The metadata keys a spawner stamps on a persona task and a terminal tool
// reads back.
//
// This is the injection channel, and its whole value is that the model cannot
// reach it. TaskMessage.Metadata is domain context the SPAWNER attaches; the
// agentic loop merges it onto every tool call it dispatches; the model writes
// only the tool call's ARGUMENTS. So a value that arrives here was put there by
// the engine, and a schema that never mentions these names cannot invite a model
// to invent one.
//
// They are namespaced under the engine rather than dropped in bare because they
// share one map with the framework's own keys (agent.run_id, agent.role,
// agent.decide.action_allowlist) and with whatever a future rule attaches.
const (
	// MetadataKeyTurnID carries the turn identity — the instance segment of the
	// turn entity's six-part ID.
	MetadataKeyTurnID = "semmachina.turn.id"
	// MetadataKeyTurnEntityID carries the turn's six-part entity ID: the subject
	// every triple a terminal tool writes lands on.
	MetadataKeyTurnEntityID = "semmachina.turn.entity_id"
	// MetadataKeyActionID carries the originating action identity.
	MetadataKeyActionID = "semmachina.action.id"
	// MetadataKeySceneID carries the scene the turn is happening in.
	MetadataKeySceneID = "semmachina.scene.id"
	// MetadataKeyBand carries the outcome band a narration voices. It is the
	// dice's answer or the verdict's declined-the-dice `auto`, and it is engine
	// knowledge for exactly the same reason the identifiers are: a narrator asked
	// which band it was voicing could answer wrongly, and nothing would notice.
	MetadataKeyBand = "semmachina.outcome.band"
)

// Identity is who a persona task is about.
//
// Every field is engine knowledge, and none of it appears in any terminal tool's
// schema. A persona is told the world and the player's words; it is never asked
// to repeat an identifier back, because a repeated identifier is one a live
// model can get wrong and a scripted one cannot be written at all — the mock
// would have to know the turn id the harness happened to mint.
type Identity struct {
	// TurnID is the turn, and the instance segment of TurnEntityID.
	TurnID string
	// TurnEntityID is the turn's six-part entity ID.
	TurnEntityID string
	// ActionID is the originating player action.
	ActionID string
	// SceneID is the scene entity the turn is happening in.
	SceneID string
}

// Validate holds an identity to the same contracts the payloads do, so a
// malformed one is refused at the spawn rather than at the write.
func (i Identity) Validate() error {
	if err := payload.RequireTurnEntityID(i.TurnID, i.TurnEntityID); err != nil {
		return err
	}
	if want := payload.TurnIDForAction(i.ActionID); want != i.TurnID {
		return fmt.Errorf(
			"identity names action %q and turn %q, but the turn derived from that action is %q; turn and "+
				"action are 1:1 and the derivation is what makes a redelivery resolve to one turn",
			i.ActionID, i.TurnID, want)
	}
	if err := vocabulary.ValidateIDSegment(i.ActionID); err != nil {
		return fmt.Errorf("action_id: %w", err)
	}
	if err := requireEntityID("scene_id", i.SceneID); err != nil {
		return err
	}
	return nil
}

// metadata renders the identity as the map a task carries.
func (i Identity) metadata() map[string]any {
	return map[string]any{
		MetadataKeyTurnID:       i.TurnID,
		MetadataKeyTurnEntityID: i.TurnEntityID,
		MetadataKeyActionID:     i.ActionID,
		MetadataKeySceneID:      i.SceneID,
	}
}

// IdentityFrom reads the injected identity back off a tool call's metadata.
//
// A missing or malformed value is a WIRING failure, not a model failure, and the
// callers treat it as one: the model cannot fix it by trying again, so the tool
// returns an internal error rather than an invalid-arguments correction it would
// burn iterations on.
func IdentityFrom(metadata map[string]any) (Identity, error) {
	turnID, err := metadataString(metadata, MetadataKeyTurnID)
	if err != nil {
		return Identity{}, err
	}
	turnEntityID, err := metadataString(metadata, MetadataKeyTurnEntityID)
	if err != nil {
		return Identity{}, err
	}
	actionID, err := metadataString(metadata, MetadataKeyActionID)
	if err != nil {
		return Identity{}, err
	}
	sceneID, err := metadataString(metadata, MetadataKeySceneID)
	if err != nil {
		return Identity{}, err
	}
	identity := Identity{
		TurnID: turnID, TurnEntityID: turnEntityID, ActionID: actionID, SceneID: sceneID,
	}
	if err := identity.Validate(); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

// BandFrom reads the injected outcome band off a tool call's metadata.
func BandFrom(metadata map[string]any) (vocabulary.OutcomeBand, error) {
	value, err := metadataString(metadata, MetadataKeyBand)
	if err != nil {
		return "", err
	}
	return vocabulary.ParseOutcomeBand(value)
}

// metadataString reads one required string off the injected metadata.
//
// A JSON round trip through the task message turns every value into `any`, so
// the assertion is against string rather than against a typed field, and a
// non-string value is named by type: "the spawner put a float there" is a
// different bug from "the spawner forgot".
func metadataString(metadata map[string]any, key string) (string, error) {
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return "", fmt.Errorf(
			"tool call carries no %q metadata; the engine injects a persona's identity rather than asking the "+
				"model for it, so an absent key is a spawn that did not stamp it", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("tool call metadata %q is a %T, want a string", key, raw)
	}
	if value == "" {
		return "", fmt.Errorf("tool call metadata %q is empty", key)
	}
	return value, nil
}
