package boot_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/c360studio/semstreams/model"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/playersocket"
)

// socketConfig binds an ephemeral loopback port, so parallel boots in one test
// run do not fight over one.
func socketConfig() playersocket.Config {
	return playersocket.Config{Addr: "127.0.0.1:0"}
}

// The starter world's own names. Restated rather than derived so a fixture that
// silently renamed a character fails these tests rather than passing against
// whatever it now calls itself.
const (
	starterCharacter = "rook"
	starterScene     = "gatehouse"
	testCredential   = "boot-test-credential"
)

func testRegistry(t *testing.T) *payloadregistry.Registry {
	t.Helper()
	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		t.Fatalf("register framework payloads: %v", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		t.Fatalf("register semmachina payloads: %v", err)
	}
	return registry
}

// testModels declares both persona capabilities against one endpoint.
//
// It points at a URL nothing serves, deliberately: every test in this file is
// about BOOT, and a registry that resolves is all boot checks. The token-free
// E2E supplies a running stub through the same field, which is the whole
// "retarget, never rewrite" claim.
func testModels() *model.Registry {
	return &model.Registry{
		Endpoints: map[string]*model.EndpointConfig{
			"stub": {
				Provider:      "openai",
				URL:           "http://127.0.0.1:1/v1",
				Model:         "stub-model",
				MaxTokens:     128_000,
				SupportsTools: true,
			},
		},
		Capabilities: map[string]*model.CapabilityConfig{
			persona.CapabilityCasekeeping:  {Preferred: []string{"stub"}},
			persona.CapabilityCompanion:    {Preferred: []string{"stub"}},
			persona.CapabilityAdjudication: {Preferred: []string{"stub"}},
			persona.CapabilityNarration:    {Preferred: []string{"stub"}},
		},
		Defaults: model.DefaultsConfig{Model: "stub"},
	}
}

func testConfig(t *testing.T) boot.Config {
	t.Helper()
	world, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatalf("StarterWorld: %v", err)
	}
	return boot.Config{
		Org:     "c360",
		WorldNS: "boottest",
		Player: boot.PlayerConfig{
			LocalID:    "one",
			Name:       "The Player",
			Character:  "local:" + starterCharacter,
			Credential: testCredential,
		},
		Models:   testModels(),
		World:    world,
		Registry: testRegistry(t),
		Logger:   quietLogger(),
		Socket:   socketConfig(),
	}
}

func newTestEngine(t *testing.T, cfg boot.Config) *boot.Engine {
	t.Helper()
	engine, err := boot.New(cfg)
	if err != nil {
		t.Fatalf("boot.New: %v", err)
	}
	return engine
}

// New resolves the world without touching a broker, so the ids it exposes are
// assertable before anything starts.
func TestNew_ResolvesTheInstanceIdentityFromThePackage(t *testing.T) {
	engine := newTestEngine(t, testConfig(t))

	if want := "c360.semmachina.boottest.starter.scene." + starterScene; engine.SceneID() != want {
		t.Errorf("scene id = %q, want %q", engine.SceneID(), want)
	}
	if want := "c360.semmachina.boottest.starter.player.one"; engine.PlayerID() != want {
		t.Errorf("player id = %q, want %q", engine.PlayerID(), want)
	}
	if want := "c360.semmachina.boottest.starter.campaign.main"; engine.CampaignID() != want {
		t.Errorf("campaign id = %q, want %q", engine.CampaignID(), want)
	}
}

// The registry probe is a CHECK, not a formality: a binary that forgot
// RegisterPayloads produces an engine that consumes every action and accepts no
// turns, with no error anywhere.
func TestNew_RefusesARegistryThatCannotDecodeAPlayerAction(t *testing.T) {
	cfg := testConfig(t)
	bare := payloadregistry.New()
	if err := payloadbuiltins.Register(bare); err != nil {
		t.Fatalf("register framework payloads: %v", err)
	}
	cfg.Registry = bare

	_, err := boot.New(cfg)
	if err == nil {
		t.Fatal("boot.New accepted a registry with no SemMachina payloads; intake would consume every action, " +
			"decode none, and accept no turns")
	}
	if !strings.Contains(err.Error(), "RegisterPayloads") {
		t.Errorf("the refusal does not name the missing call: %v", err)
	}
}

// F20, at boot: an undeclared capability RESOLVES — to the registry's default
// model — so "it started" proves nothing about which model the adjudicator is on.
func TestNew_RefusesAModelRegistryMissingAPersonaCapability(t *testing.T) {
	for _, capability := range []string{
		persona.CapabilityCasekeeping, persona.CapabilityCompanion,
		persona.CapabilityAdjudication, persona.CapabilityNarration,
	} {
		t.Run(capability, func(t *testing.T) {
			cfg := testConfig(t)
			models := testModels()
			delete(models.Capabilities, capability)
			cfg.Models = models

			engine := newTestEngine(t, cfg)
			var check func(t *testing.T)
			for _, step := range engine.Steps() {
				if step.ID != boot.StepAgentic || step.Check == nil {
					continue
				}
				check = func(t *testing.T) {
					err := step.Check(t.Context())
					if err == nil {
						t.Fatalf("boot accepted a registry that does not declare %q; an undeclared capability "+
							"silently resolves to the DEFAULT model, so the schema-bearing persona would run on "+
							"whatever that is and nothing would report it", capability)
					}
					if !strings.Contains(err.Error(), capability) {
						t.Errorf("the refusal does not name the missing capability: %v", err)
					}
				}
			}
			if check == nil {
				t.Fatal("the agentic step declares no precondition, so the model registry is never checked")
			}
			check(t)
		})
	}
}

// A persona pointed at an endpoint that cannot call a tool can never terminate:
// every iteration returns prose, the budget burns, and the turn fails as a cap
// exhaustion — a code that describes the symptom and hides the cause.
func TestNew_RefusesAnEndpointThatCannotCallTools(t *testing.T) {
	cfg := testConfig(t)
	models := testModels()
	models.Endpoints["stub"].SupportsTools = false
	cfg.Models = models

	engine := newTestEngine(t, cfg)
	for _, step := range engine.Steps() {
		if step.ID != boot.StepAgentic || step.Check == nil {
			continue
		}
		if err := step.Check(t.Context()); err == nil {
			t.Fatal("boot accepted an endpoint with no tool calling; both personas exit through a tool, so every " +
				"turn would burn its whole budget and fail as a cap exhaustion")
		}
		return
	}
	t.Fatal("the agentic step declares no precondition")
}

func TestNew_RefusesAnInstanceItCannotIdentify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*boot.Config)
		want   string
	}{
		{name: "no org", mutate: func(c *boot.Config) { c.Org = "" }, want: "org"},
		{name: "no world namespace", mutate: func(c *boot.Config) { c.WorldNS = "" }, want: "world namespace"},
		{name: "no credential", mutate: func(c *boot.Config) { c.Player.Credential = "" }, want: "credential"},
		{name: "no world package", mutate: func(c *boot.Config) { c.World = nil }, want: "world package"},
		{name: "no model registry", mutate: func(c *boot.Config) { c.Models = nil }, want: "model registry"},
		{
			name:   "player plays nobody",
			mutate: func(c *boot.Config) { c.Player.Character = "local:nobody" },
			want:   "nobody",
		},
		{
			name:   "player plays an item",
			mutate: func(c *boot.Config) { c.Player.Character = "local:crowbar" },
			want:   "item",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			tc.mutate(&cfg)
			_, err := boot.New(cfg)
			if err == nil {
				t.Fatal("boot.New accepted an instance it cannot identify")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A package with several scenes must be told which one is played, because the
// alternative picks it by iteration order and the wrong answer is invisible.
func TestNew_RefusesAnAmbiguousSceneAndAcceptsANamedOne(t *testing.T) {
	pkg := twoScenePackage(t)

	cfg := testConfig(t)
	cfg.World = pkg
	_, err := boot.New(cfg)
	if err == nil {
		t.Fatal("boot.New picked a scene out of two without being told which")
	}
	if !strings.Contains(err.Error(), "iteration order") {
		t.Errorf("the refusal does not say why choosing would be wrong: %v", err)
	}

	cfg = testConfig(t)
	cfg.World = pkg
	cfg.SceneLocalID = "cellar"
	engine := newTestEngine(t, cfg)
	if want := "c360.semmachina.boottest.two.scene.cellar"; engine.SceneID() != want {
		t.Errorf("scene id = %q, want the named scene %q", engine.SceneID(), want)
	}

	cfg = testConfig(t)
	cfg.World = pkg
	cfg.SceneLocalID = "attic"
	if _, err := boot.New(cfg); err == nil {
		t.Fatal("boot.New accepted a scene the package does not declare")
	}
}

// A world nobody is standing in has no membership edge, so the reverse-edge
// readiness gate would have nothing to read — and a gate that passes because
// there is nothing to check is the one that reports green on the day the import
// produced nothing.
func TestNew_ProducesAWorldWhoseMembershipCanBeGatedOn(t *testing.T) {
	engine := newTestEngine(t, testConfig(t))
	// Membership is checked at boot against a live index; here the assertion is
	// that the PLAN declares any at all, which is the precondition for that gate
	// being non-vacuous.
	total := 0
	for _, sources := range engine.MembershipEdges() {
		total += len(sources)
	}
	if total == 0 {
		t.Fatal("the resolved plan declares no membership edge; the boot readiness gate would pass against an " +
			"empty index")
	}
}

// twoScenePackage is a minimal world declaring two scenes, so the ambiguity
// refusal has something real to refuse.
func twoScenePackage(t *testing.T) fstest.MapFS {
	t.Helper()
	manifest := "id: two\nname: Two Rooms\nversion: 0.1.0\n" +
		"engine_compat: \">=v1.0.0-beta.150 <v2.0.0\"\ndescription: two scenes\n"
	entities := strings.Join([]string{
		`{"local_id":"cellar","type":"scene","triples":[` +
			`{"predicate":"world.entity.name","object":"The Cellar"},` +
			`{"predicate":"scene.location.current","object":"local:cellar-place"}]}`,
		`{"local_id":"loft","type":"scene","triples":[` +
			`{"predicate":"world.entity.name","object":"The Loft"},` +
			`{"predicate":"scene.location.current","object":"local:loft-place"}]}`,
		`{"local_id":"cellar-place","type":"location","triples":[` +
			`{"predicate":"world.entity.name","object":"The Cellar Place"}]}`,
		`{"local_id":"loft-place","type":"location","triples":[` +
			`{"predicate":"world.entity.name","object":"The Loft Place"}]}`,
		`{"local_id":"` + starterCharacter + `","type":"character","triples":[` +
			`{"predicate":"world.entity.name","object":"Rook"},` +
			`{"predicate":"world.location.current","object":"local:cellar-place"}]}`,
	}, "\n") + "\n"

	return fstest.MapFS{
		"manifest.yaml":             {Data: []byte(manifest)},
		"entities.jsonl":            {Data: []byte(entities)},
		"personas/adjudicator.json": {Data: personaRecord("two/adjudicator", "adjudicator")},
		"personas/narrator.json":    {Data: personaRecord("two/narrator", "narrator")},
	}
}

func personaRecord(id, role string) []byte {
	return []byte(`{"id":"` + id + `","category":100,"priority":10,"roles":["` + role + `"],` +
		`"content":"A voice for ` + role + `."}`)
}
