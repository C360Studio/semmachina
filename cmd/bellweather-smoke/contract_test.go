package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestPaidSmokeRequiresBothExplicitOptIns(t *testing.T) {
	tests := map[string]struct {
		env     map[string]string
		wantErr string
	}{
		"neither":  {env: map[string]string{}, wantErr: "SEMMACHINA_PAID_SMOKE=1"},
		"key only": {env: map[string]string{"GEMINI_API_KEY": "present"}, wantErr: "SEMMACHINA_PAID_SMOKE=1"},
		"flag only": {
			env: map[string]string{"SEMMACHINA_PAID_SMOKE": "1"}, wantErr: "GEMINI_API_KEY",
		},
		"blank key": {
			env:     map[string]string{"SEMMACHINA_PAID_SMOKE": "1", "GEMINI_API_KEY": "  "},
			wantErr: "GEMINI_API_KEY",
		},
		"both": {env: map[string]string{"SEMMACHINA_PAID_SMOKE": "1", "GEMINI_API_KEY": "present"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := requirePaidOptIn(func(key string) string { return tc.env[key] })
			if tc.wantErr == "" && err != nil {
				t.Fatalf("requirePaidOptIn: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("requirePaidOptIn error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestFixedActionsPinBodyObservationThenClosedKitHintRequest(t *testing.T) {
	observation := strings.ToLower(observationActionText)
	for _, required := range []string{"observe", "harold wren", "body"} {
		if !strings.Contains(observation, required) {
			t.Errorf("body-observation action omits %q", required)
		}
	}
	if observationTarget != "victim-harold-wren" {
		t.Fatalf("body-observation target = %q, want victim-harold-wren", observationTarget)
	}

	hint := strings.ToLower(hintActionText)
	for _, required := range []string{"explicitly ask", "kit finch", "hint"} {
		if !strings.Contains(hint, required) {
			t.Errorf("Kit request_hint action omits %q", required)
		}
	}
	if observationActionText == hintActionText {
		t.Fatal("body observation and Kit request_hint collapsed into one ambiguous action")
	}
}

func TestEachPaidRunUsesAFreshColdOpenNamespace(t *testing.T) {
	if got := smokeWorldNamespace("bellweather-gemini35-flash-lite-smoke", 12345); got !=
		"bellweather-gemini35-flash-lite-smoke-12345" {
		t.Fatalf("fresh smoke namespace = %q", got)
	}
}

func TestBellweatherGeminiConfigPinsTheOperatorScenario(t *testing.T) {
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, bellweatherConfigPath))
	if err != nil {
		t.Fatalf("read Bellweather config: %v", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		t.Fatalf("load Bellweather config: %v", err)
	}
	if err := validateBellweatherBinding(cfg); err != nil {
		t.Fatalf("Bellweather binding: %v", err)
	}
	if cfg.SceneLocalID != "fete-green" || cfg.Player.Name != "Rowan Vale" ||
		cfg.Player.Character != "local:rowan-vale" {
		t.Fatalf("wrong scene/player binding: scene=%q player=%q character=%q",
			cfg.SceneLocalID, cfg.Player.Name, cfg.Player.Character)
	}
	if cfg.Companion == nil || cfg.Companion.Character != "local:kit-finch" ||
		cfg.Companion.Policy != vocabulary.CompanionPolicyBoundedInitiative {
		t.Fatalf("wrong Kit binding: %+v", cfg.Companion)
	}
	if cfg.ContentBucket != "BELLWEATHER_GEMINI_SMOKE" || len(cfg.ContentBucket) > 32 {
		t.Fatalf("Bellweather content bucket = %q (%d bytes), want the exact reference-safe instance",
			cfg.ContentBucket, len(cfg.ContentBucket))
	}
	if bellweatherConfigPath != "configs/instance.gemini35-flash-lite.bellweather.example.json" {
		t.Fatalf("paid smoke config = %q, want the Flash-Lite instance", bellweatherConfigPath)
	}
	endpoint := cfg.Models.Endpoints["gemini-flash-lite"]
	if endpoint == nil || endpoint.Provider != "gemini" ||
		endpoint.URL != "https:/"+"/generativelanguage.googleapis.com/v1beta/openai" ||
		endpoint.Model != "gemini-3.5-flash-lite" || endpoint.APIKeyEnv != "GEMINI_API_KEY" ||
		endpoint.WireBackend != "wire" || !endpoint.SupportsTools || endpoint.ToolFormat != "openai" ||
		endpoint.MaxTokens != 1048576 {
		t.Fatalf("Bellweather Flash-Lite endpoint = %+v, want the exact paid target", endpoint)
	}
	if len(cfg.Models.Endpoints) != 1 || cfg.Models.Defaults.Model != "gemini-flash-lite" {
		t.Fatalf("Bellweather model routes = endpoints %v default %q, want only gemini-flash-lite",
			cfg.Models.Endpoints, cfg.Models.Defaults.Model)
	}
	if len(cfg.Models.Capabilities) != 4 {
		t.Fatalf("Bellweather capabilities = %v, want exactly the four smoke capabilities", cfg.Models.Capabilities)
	}
	for _, capability := range []string{"casekeeping", "companion_decision", "fiction_adjudication", "narration"} {
		declaration := cfg.Models.Capabilities[capability]
		if declaration == nil || len(declaration.Preferred) != 1 ||
			declaration.Preferred[0] != "gemini-flash-lite" || !declaration.RequiresTools {
			t.Fatalf("capability %s = %+v, want exclusive tool-capable Flash-Lite route", capability, declaration)
		}
	}
}

func TestValidateBellweatherBindingRejectsNonExclusiveFlashLiteRoutes(t *testing.T) {
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, bellweatherConfigPath))
	if err != nil {
		t.Fatalf("read Bellweather config: %v", err)
	}
	tests := map[string]func(*boot.Config){
		"extra endpoint": func(cfg *boot.Config) {
			cfg.Models.Endpoints["fallback"] = cfg.Models.Endpoints["gemini-flash-lite"]
		},
		"wrong default": func(cfg *boot.Config) {
			cfg.Models.Defaults.Model = "fallback"
		},
		"wrong provider URL": func(cfg *boot.Config) {
			cfg.Models.Endpoints["gemini-flash-lite"].URL = "https://example.com/v1"
		},
		"wrong token limit": func(cfg *boot.Config) {
			cfg.Models.Endpoints["gemini-flash-lite"].MaxTokens--
		},
		"extra capability": func(cfg *boot.Config) {
			cfg.Models.Capabilities["fallback"] = cfg.Models.Capabilities["narration"]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, loadErr := boot.LoadConfig(raw)
			if loadErr != nil {
				t.Fatalf("load Bellweather config: %v", loadErr)
			}
			mutate(&cfg)
			if err := validateBellweatherBinding(cfg); err == nil {
				t.Fatal("validateBellweatherBinding() error = nil")
			}
		})
	}
}

func TestPaidSmokeTaskIsExplicitAndExcludedFromDefaultAndCI(t *testing.T) {
	root := repositoryRoot(t)
	taskfile, err := os.ReadFile(filepath.Join(root, "Taskfile.yml"))
	if err != nil {
		t.Fatalf("read Taskfile: %v", err)
	}
	text := string(taskfile)
	if !strings.Contains(text, "smoke:gemini:bellweather:") ||
		!strings.Contains(text, `test "$SEMMACHINA_PAID_SMOKE" = "1"`) ||
		!strings.Contains(text, `test -n "$GEMINI_API_KEY"`) ||
		!strings.Contains(text, "-config configs/instance.gemini35-flash-lite.bellweather.example.json") {
		t.Fatal("paid smoke task does not carry both explicit preconditions")
	}
	defaultBlock := text[strings.Index(text, "  default:"):strings.Index(text, "\n  lint:")]
	if strings.Contains(defaultBlock, "smoke:gemini:bellweather") {
		t.Fatal("paid smoke is reachable from the default task")
	}

	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatalf("find CI workflows: %v", err)
	}
	for _, path := range workflows {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if strings.Contains(string(raw), "smoke:gemini:bellweather") {
			t.Fatalf("paid smoke is invoked by CI workflow %s", path)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}
