package boot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/model"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// Defaults for the parts of a deployment that have a sensible one.
const (
	// DefaultNATSURL is the local broker an instance-per-world box runs.
	DefaultNATSURL = "nats://127.0.0.1:4222"
	// DefaultReadyTimeout bounds every boot-time readback: the imported world
	// becoming queryable, and the reverse-edge index catching up with it.
	DefaultReadyTimeout = 90 * time.Second
	// DefaultReadyPoll is how often those readbacks re-ask.
	DefaultReadyPoll = 250 * time.Millisecond
	// DefaultStopTimeout bounds each component's shutdown.
	DefaultStopTimeout = 10 * time.Second
)

// PlayerConfig is the instance's one player: who they are, what they play, and
// the credential that authenticates them.
//
// One player, because that is this slice's scope (the change's non-goals: a
// single local player). The roster underneath takes a map, so more players is a
// config shape change and not an engine change.
type PlayerConfig struct {
	// LocalID becomes the instance position of the player entity's id.
	LocalID string `json:"local_id"`
	// Name is the player's display name.
	Name string `json:"name"`
	// Character is a `local:` reference into the template naming the character
	// this player controls.
	Character string `json:"character"`
	// Credential is the opaque bearer token that authenticates this player.
	//
	// Instance configuration with no expiry, rotation or revocation short of a
	// restart — every one of those absences is a consequence of the local-only
	// posture, and internal/playersocket enumerates them by name the moment an
	// instance is bound beyond loopback.
	Credential string `json:"credential"`
}

// Config is one world instance's whole deployment configuration.
//
// The JSON-tagged half is what an operator writes. The untagged half is what a
// process supplies — an embedded world package, a payload registry the binary
// populated at its own bootstrap, a logger — and is deliberately not
// configurable, because each of those is a decision the binary has already made
// by the time it has a Config to fill in.
type Config struct {
	// NATSURL is the broker to connect to.
	NATSURL string `json:"nats_url,omitempty"`

	// Org is the federation organization position of every entity id.
	Org string `json:"org"`
	// WorldNS is the world namespace: the position that makes two campaigns
	// from one template disjoint.
	WorldNS string `json:"world_ns"`

	// Player is the instance's player binding and credential.
	Player PlayerConfig `json:"player"`

	// SceneLocalID names the scene actions are taken in, as a template-local id.
	//
	// Optional: a package declaring exactly one scene needs no answer here, and
	// this engine derives it. A package with several REQUIRES one, because
	// picking for the operator would mean picking the scene the campaign is
	// played in by iteration order.
	SceneLocalID string `json:"scene_local_id,omitempty"`

	// ContentBucket is the ObjectStore bucket every artifact reference names.
	ContentBucket string `json:"content_bucket,omitempty"`

	// Models is the model registry the personas resolve through.
	//
	// A registry rather than an endpoint, because that is the seam that makes
	// "a live model is a config retarget, never a code change" true — and it is
	// the same seam the token-free E2E uses to point both personas at a local
	// stub. Boot REFUSES a registry that does not declare every persona's
	// capability: an undeclared one silently resolves to the registry's default
	// model, which is F20's failure and the exact configuration ADR-026 warns
	// about, arrived at through a fallback rather than a decision.
	Models *model.Registry `json:"model_registry"`

	// Socket is the player transport's configuration.
	Socket playersocket.Config `json:"socket"`

	// ReadyTimeout bounds the boot-time readbacks; ReadyPoll is their interval.
	ReadyTimeout time.Duration `json:"ready_timeout,omitempty"`
	ReadyPoll    time.Duration `json:"ready_poll,omitempty"`
	// MarkerWait bounds how long a boot waits for ANOTHER claimant's import to
	// finish before failing loudly. See campaign.AwaitImportCompletion.
	MarkerWait time.Duration `json:"marker_wait,omitempty"`
	// StopTimeout bounds each component's shutdown.
	StopTimeout time.Duration `json:"stop_timeout,omitempty"`

	// World is the world package to instantiate from.
	World fs.FS `json:"-"`
	// CampaignSeed is the seed a FRESH campaign is created with. The zero value
	// means mint one from crypto/rand, which is what production does.
	//
	// # Why this is not operator configuration
	//
	// It carries no JSON tag on purpose. A campaign seed is not a secret — an
	// operator holding ENTITY_STATES can predict the dice, and instance-per-world
	// makes the operator and the player the same person — but it IS the root of
	// every roll the campaign will ever make, and a config file is where one seed
	// gets copied into a second deployment by accident. A process that wants a
	// reproducible campaign says so in code, where the reason can sit next to it.
	//
	// # Why it exists at all
	//
	// The token-free end-to-end suite has to play a turn that lands in a NAMED
	// outcome band, and a band is not scriptable at the model boundary: a verdict
	// declares intents for all three and the seeded dice select one (design F19).
	// The only way to choose is to supply the (campaign_seed, turn_id) pair whose
	// derived roll lands there — which makes the pair a pinned fixture constant and
	// the assertion a small proof of seeded replay in its own right.
	//
	// It is honoured only on the boot that CREATES the campaign. A campaign that
	// already exists keeps the seed it was created with, because that is the whole
	// contract of campaign.Gate: a seed is generated at most once in a campaign's
	// life no matter how many times it is claimed.
	CampaignSeed campaign.Seed `json:"-"`
	// Registry is the payload registry, populated by the BINARY's bootstrap.
	//
	// Passed in rather than built here so there is exactly one place per binary
	// that registers payloads, and so a binary that forgot is a boot failure
	// rather than a component that decodes nothing. Boot checks it rather than
	// trusting it — see checkPayloadRegistry.
	Registry *payloadregistry.Registry `json:"-"`
	// Logger is the operator log. Defaults to slog.Default().
	Logger *slog.Logger `json:"-"`
}

// LoadConfig reads an operator's JSON configuration.
//
// Unknown fields are REFUSED rather than ignored, for the reason the world
// manifest refuses them: a misspelled key that is silently dropped is a setting
// the operator believes they made. On a file that carries the model registry and
// a player's credential, "silently not what you wrote" is the worst available
// failure.
func LoadConfig(data []byte) (Config, error) {
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse the instance configuration: %w", err)
	}
	return cfg, nil
}

// withDefaults fills in every value that has a defensible default and leaves
// every value that does not.
func (c Config) withDefaults() Config {
	if c.NATSURL == "" {
		c.NATSURL = DefaultNATSURL
	}
	if c.ContentBucket == "" {
		c.ContentBucket = content.DefaultBucket
	}
	if c.ReadyTimeout <= 0 {
		c.ReadyTimeout = DefaultReadyTimeout
	}
	if c.ReadyPoll <= 0 {
		c.ReadyPoll = DefaultReadyPoll
	}
	if c.MarkerWait <= 0 {
		c.MarkerWait = campaign.DefaultMarkerWait
	}
	if c.StopTimeout <= 0 {
		c.StopTimeout = DefaultStopTimeout
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Validate checks everything that can be checked without touching a broker.
//
// It runs at construction rather than at Start so a misconfigured deployment
// fails before it has created a campaign entity, opened a bucket, or bound a
// consumer — the states a half-booted engine leaves behind are exactly the ones
// the instantiation marker exists to reason about, and not entering them is
// cheaper than recovering from them.
func (c Config) Validate() error {
	if err := vocabulary.ValidateIDSegment(c.Org); err != nil {
		return fmt.Errorf("instance org: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(c.WorldNS); err != nil {
		return fmt.Errorf("instance world namespace: %w", err)
	}
	if c.Player.Credential == "" {
		return errors.New(
			"the instance player has no credential; an instance nobody can authenticate to is an instance " +
				"nobody can play, and a blank credential would authenticate a silent client")
	}
	if c.World == nil {
		return errors.New("the instance has no world package to instantiate from")
	}
	if c.Registry == nil {
		return errors.New(
			"the instance has no payload registry; the binary's bootstrap builds it, and an engine without one " +
				"would consume player actions, decode nothing, and accept no turns")
	}
	if c.Models == nil {
		return errors.New(
			"the instance declares no model registry; both personas resolve their endpoint through it, and an " +
				"absent one is not a degraded mode — it is a turn loop with no adjudicator")
	}
	if err := c.Models.Validate(); err != nil {
		return fmt.Errorf("model registry: %w", err)
	}
	// The binding the world package needs, checked here so the failure names the
	// instance configuration rather than surfacing from inside the resolver.
	binding := world.InstanceConfig{Org: c.Org, WorldNS: c.WorldNS, Player: c.playerBinding()}
	if err := binding.Validate(); err != nil {
		return err
	}
	if c.SceneLocalID != "" {
		if err := vocabulary.ValidateIDSegment(c.SceneLocalID); err != nil {
			return fmt.Errorf("instance scene id: %w", err)
		}
	}
	return nil
}

func (c Config) playerBinding() world.PlayerBinding {
	return world.PlayerBinding{
		LocalID:   c.Player.LocalID,
		Name:      c.Player.Name,
		Character: c.Player.Character,
	}
}
