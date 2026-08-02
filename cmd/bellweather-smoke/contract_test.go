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
	if got := smokeWorldNamespace("bellweather-gemini36-smoke", 12345); got !=
		"bellweather-gemini36-smoke-12345" {
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
	endpoint := cfg.Models.Endpoints["gemini-flash"]
	if endpoint == nil || endpoint.URL != "https:/"+"/generativelanguage.googleapis.com/v1beta/openai" {
		t.Fatalf("Bellweather Gemini endpoint = %+v, want the exact live endpoint", endpoint)
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
		!strings.Contains(text, `test -n "$GEMINI_API_KEY"`) {
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
