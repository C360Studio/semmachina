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

func TestQwen35ExampleInstanceConfig_TargetsOneLocalLiveSmokeModel(t *testing.T) {
	raw, err := os.ReadFile("../../configs/instance.qwen35-9b.example.json")
	if err != nil {
		t.Fatalf("read the qwen3.5 live-smoke configuration: %v", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		t.Fatalf("the qwen3.5 live-smoke configuration does not parse: %v", err)
	}
	if len(cfg.Models.Endpoints) != 1 {
		t.Fatalf("qwen3.5 live-smoke endpoints = %d, want one", len(cfg.Models.Endpoints))
	}
	endpoint := cfg.Models.Endpoints["local-qwen"]
	if endpoint == nil || endpoint.Model != "qwen3.5:9b" || endpoint.URL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("qwen3.5 live-smoke endpoint = %+v, want local Ollama qwen3.5:9b", endpoint)
	}
	for capability, declaration := range cfg.Models.Capabilities {
		if len(declaration.Preferred) != 1 || declaration.Preferred[0] != "local-qwen" {
			t.Errorf("capability %s prefers %v, want only local-qwen", capability, declaration.Preferred)
		}
	}
}

func TestGemini36FlashExampleInstanceConfig_TargetsOneToolCapableWireEndpoint(t *testing.T) {
	raw, err := os.ReadFile("../../configs/instance.gemini36-flash.example.json")
	if err != nil {
		t.Fatalf("read the Gemini 3.6 Flash live configuration: %v", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		t.Fatalf("the Gemini 3.6 Flash live configuration does not parse: %v", err)
	}
	if len(cfg.Models.Endpoints) != 1 {
		t.Fatalf("Gemini live endpoints = %d, want one", len(cfg.Models.Endpoints))
	}
	endpoint := cfg.Models.Endpoints["gemini-flash"]
	if endpoint == nil {
		t.Fatal("Gemini live configuration has no gemini-flash endpoint")
	}
	if endpoint.Provider != "gemini" ||
		endpoint.URL != "https://generativelanguage.googleapis.com/v1beta/openai" ||
		endpoint.Model != "gemini-3.6-flash" ||
		endpoint.APIKeyEnv != "GEMINI_API_KEY" ||
		endpoint.WireBackend != "wire" ||
		endpoint.MaxTokens != 1_048_576 ||
		!endpoint.SupportsTools || endpoint.ToolFormat != "openai" {
		t.Fatalf("Gemini live endpoint = %+v, want the explicit Gemini wire configuration", endpoint)
	}
	for _, capability := range []string{"fiction_adjudication", "narration"} {
		declaration := cfg.Models.Capabilities[capability]
		if declaration == nil {
			t.Errorf("Gemini live configuration has no %s capability", capability)
			continue
		}
		if len(declaration.Preferred) != 1 || declaration.Preferred[0] != "gemini-flash" ||
			!declaration.RequiresTools {
			t.Errorf("capability %s = %+v, want tool-capable gemini-flash only", capability, declaration)
		}
	}
}
