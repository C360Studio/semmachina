package main

import (
	"os"
	"testing"

	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/payload"
)

// The shipped example configuration must actually boot this binary's engine.
//
// An example that no longer parses is worse than no example: it is the first
// thing an operator copies, and every field in it is one they will not question.
// Config decoding REFUSES unknown fields, so a renamed key in the engine turns
// this file into a boot failure — which is exactly the failure this test moves
// from an operator's terminal to CI.
//
// It stops short of connecting to anything. What it proves is the shape: every
// required field present, both persona capabilities declared against endpoints
// that exist and support tool calling, and the player binding resolvable against
// the world package this binary embeds.
func TestExampleInstanceConfig_BuildsAnEngine(t *testing.T) {
	raw, err := os.ReadFile("../../configs/instance.example.json")
	if err != nil {
		t.Fatalf("read the example instance configuration: %v", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		t.Fatalf("the shipped example configuration does not parse: %v", err)
	}

	world, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("StarterWorld: %v", err)
	}
	cfg.World = world

	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		t.Fatalf("register framework payloads: %v", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		t.Fatalf("register semmachina payloads: %v", err)
	}
	cfg.Registry = registry

	engine, err := boot.New(cfg)
	if err != nil {
		t.Fatalf("the shipped example configuration does not build an engine: %v", err)
	}

	// The engine resolved the example's player binding against the embedded
	// starter world, which is the half of the example most likely to rot: a
	// character the world stops declaring makes every session authenticate to an
	// entity nothing imports.
	if engine.PlayerID() == "" || engine.SceneID() == "" || engine.CampaignID() == "" {
		t.Errorf("the example resolved to player %q, scene %q, campaign %q",
			engine.PlayerID(), engine.SceneID(), engine.CampaignID())
	}
}
