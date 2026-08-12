package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/c360studio/semstreams/component"
	graphindex "github.com/c360studio/semstreams/processor/graph-index"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/c360studio/semstreams/storage"
)

func TestGraphComponentConfigsCarryValidCanonicalDefaultsAcrossJSON(t *testing.T) {
	configs, err := graphComponentConfigs()
	if err != nil {
		t.Fatalf("graphComponentConfigs: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("graph component configs = %d, want graph-ingest and graph-index", len(configs))
	}

	ingestWire := configs["graph-ingest"].Config
	if len(ingestWire) == 0 || string(ingestWire) == "null" {
		t.Fatalf("graph-ingest config = %q; null loses its required port declarations", ingestWire)
	}
	var ingest graphingest.Config
	if err := json.Unmarshal(ingestWire, &ingest); err != nil {
		t.Fatalf("decode graph-ingest defaults: %v", err)
	}
	if err := ingest.Validate(); err != nil {
		t.Fatalf("graph-ingest defaults are not admissible: %v", err)
	}
	if ingest.Ports == nil || len(ingest.Ports.Inputs) != 2 || len(ingest.Ports.Outputs) != 1 {
		t.Fatalf("graph-ingest ports = %#v, want entity stream + canonical mutation input and entity-state output",
			ingest.Ports)
	}

	indexWire := configs["graph-index"].Config
	if len(indexWire) == 0 || string(indexWire) == "null" {
		t.Fatalf("graph-index config = %q; defaults must be explicit at the composition boundary", indexWire)
	}
	var index graphindex.Config
	if err := json.Unmarshal(indexWire, &index); err != nil {
		t.Fatalf("decode graph-index defaults: %v", err)
	}
	if err := index.Validate(); err != nil {
		t.Fatalf("graph-index defaults are not admissible: %v", err)
	}
	if index.Ports == nil || len(index.Ports.Inputs) != 1 || len(index.Ports.Outputs) != 4 {
		t.Fatalf("graph-index ports = %#v, want entity-state input and four canonical index outputs", index.Ports)
	}
}

type manifestComponent struct {
	inputs  []component.Port
	outputs []component.Port
}

func (*manifestComponent) Meta() component.Metadata             { return component.Metadata{} }
func (c *manifestComponent) InputPorts() []component.Port       { return c.inputs }
func (c *manifestComponent) OutputPorts() []component.Port      { return c.outputs }
func (*manifestComponent) ConfigSchema() component.ConfigSchema { return component.ConfigSchema{} }
func (*manifestComponent) Health() component.HealthStatus       { return component.HealthStatus{} }
func (*manifestComponent) DataFlow() component.FlowMetrics      { return component.FlowMetrics{} }

type manifestStorage struct{ manifestComponent }

func (*manifestStorage) ProvidedStores() map[string]storage.StreamableStore { return nil }

type journalManager struct {
	name     string
	journal  *[]string
	startErr error
	stopErr  error
}

type journalShutdownClient struct {
	journal *[]string
}

func (c *journalShutdownClient) StopAllConsumers() {
	*c.journal = append(*c.journal, "stop-all-consumers")
}

func (c *journalShutdownClient) Close(context.Context) error {
	*c.journal = append(*c.journal, "close-client")
	return nil
}

func (m *journalManager) Start(context.Context) error {
	*m.journal = append(*m.journal, "start:"+m.name)
	return m.startErr
}

func (m *journalManager) Stop(time.Duration) error {
	*m.journal = append(*m.journal, "stop:"+m.name)
	return m.stopErr
}

func manifestMutationPort(t *testing.T, direction component.Direction, required bool) component.Port {
	t.Helper()
	port, err := (component.PortDefinition{
		Name: "graph_mutations", Required: required,
		Config: component.NATSRequestPort{Subject: "graph.mutation.>", Interface: &component.InterfaceContract{
			Type: "semstreams.graph.mutation", Version: "v1",
		}},
	}).Resolve(direction)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestValidateUnionComponentsAcceptsOneCanonicalMutationAndStorageProvider(t *testing.T) {
	err := validateUnionComponents(
		map[string]component.Discoverable{
			"graph-ingest": &manifestComponent{inputs: []component.Port{
				manifestMutationPort(t, component.DirectionInput, true),
			}},
		},
		map[string]component.Discoverable{
			"rule": &manifestComponent{outputs: []component.Port{
				manifestMutationPort(t, component.DirectionOutput, true),
			}},
			"objectstore": &manifestStorage{},
		},
	)
	if err != nil {
		t.Fatalf("validateUnionComponents: %v", err)
	}
}

func TestValidateUnionComponentsFailsBeforeActivationOnInvalidTopology(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups []map[string]component.Discoverable
	}{
		{name: "no mutation provider", groups: []map[string]component.Discoverable{{
			"rule":        &manifestComponent{outputs: []component.Port{manifestMutationPort(t, component.DirectionOutput, true)}},
			"objectstore": &manifestStorage{},
		}}},
		{name: "two mutation providers", groups: []map[string]component.Discoverable{{
			"graph-a":     &manifestComponent{inputs: []component.Port{manifestMutationPort(t, component.DirectionInput, true)}},
			"objectstore": &manifestStorage{},
		}, {
			"graph-b": &manifestComponent{inputs: []component.Port{manifestMutationPort(t, component.DirectionInput, true)}},
		}}},
		{name: "no storage provider", groups: []map[string]component.Discoverable{{
			"graph": &manifestComponent{inputs: []component.Port{manifestMutationPort(t, component.DirectionInput, true)}},
		}}},
		{name: "duplicate instance", groups: []map[string]component.Discoverable{{
			"graph":       &manifestComponent{inputs: []component.Port{manifestMutationPort(t, component.DirectionInput, true)}},
			"objectstore": &manifestStorage{},
		}, {"graph": &manifestComponent{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateUnionComponents(tc.groups...); err == nil {
				t.Fatal("invalid union manifest was accepted")
			}
		})
	}
}

func TestComponentManagersStopInStrictReverseActivationOrder(t *testing.T) {
	journal := []string{}
	engine := &Engine{cfg: Config{StopTimeout: time.Second}}
	for _, name := range []string{"graph", "agentic", "rule"} {
		manager := &journalManager{name: name, journal: &journal}
		if err := engine.activateManager(t.Context(), name, manager, nil); err != nil {
			t.Fatalf("activate %s manager: %v", name, err)
		}
	}
	engine.stopComponents()
	want := []string{
		"start:graph", "start:agentic", "start:rule",
		"stop:rule", "stop:agentic", "stop:graph",
	}
	if !slices.Equal(journal, want) {
		t.Fatalf("lifecycle journal = %v, want %v", journal, want)
	}
	engine.stopComponents()
	if !slices.Equal(journal, want) {
		t.Fatalf("idempotent stop changed journal: %v", journal)
	}
}

func TestEngineStopLetsManagersReleaseOwnedConsumersBeforeGlobalClientShutdown(t *testing.T) {
	journal := []string{}
	engine := &Engine{
		cfg:            Config{StopTimeout: time.Second},
		shutdownClient: &journalShutdownClient{journal: &journal},
	}
	for _, name := range []string{"graph", "agentic", "rule"} {
		manager := &journalManager{name: name, journal: &journal}
		if err := engine.activateManager(t.Context(), name, manager, nil); err != nil {
			t.Fatalf("activate %s manager: %v", name, err)
		}
	}

	engine.Stop()

	want := []string{
		"start:graph", "start:agentic", "start:rule",
		"stop:rule", "stop:agentic", "stop:graph",
		"stop-all-consumers", "close-client",
	}
	if !slices.Equal(journal, want) {
		t.Fatalf("shutdown journal = %v, want %v", journal, want)
	}
	engine.Stop()
	if !slices.Equal(journal, want) {
		t.Fatalf("idempotent stop changed journal: %v", journal)
	}
}

func TestComponentManagerStartFailureUnwindsPartiallyStartedBarrierFirst(t *testing.T) {
	journal := []string{}
	engine := &Engine{cfg: Config{StopTimeout: time.Second}}
	graphManager := &journalManager{name: "graph", journal: &journal}
	agenticManager := &journalManager{name: "agentic", journal: &journal}
	ruleManager := &journalManager{name: "rule", journal: &journal, startErr: errors.New("injected start failure")}
	for _, entry := range []struct {
		name    string
		manager *journalManager
	}{{"graph", graphManager}, {"agentic", agenticManager}} {
		if err := engine.activateManager(t.Context(), entry.name, entry.manager, nil); err != nil {
			t.Fatalf("activate %s manager: %v", entry.name, err)
		}
	}
	if err := engine.activateManager(t.Context(), "rule", ruleManager, nil); err == nil {
		t.Fatal("rule manager start failure was accepted")
	}
	engine.stopComponents()
	want := []string{"start:graph", "start:agentic", "start:rule", "stop:rule", "stop:agentic", "stop:graph"}
	if !slices.Equal(journal, want) {
		t.Fatalf("partial-start lifecycle journal = %v, want %v", journal, want)
	}
}

func TestPostStartVerificationFailureStillTransfersCleanupOwnership(t *testing.T) {
	journal := []string{}
	engine := &Engine{cfg: Config{StopTimeout: time.Second}}
	manager := &journalManager{name: "graph", journal: &journal}
	err := engine.activateManager(t.Context(), "graph", manager, func() error {
		return fmt.Errorf("injected verification failure")
	})
	if err == nil {
		t.Fatal("post-start verification failure was accepted")
	}
	engine.stopComponents()
	want := []string{"start:graph", "stop:graph"}
	if !slices.Equal(journal, want) {
		t.Fatalf("verification-failure lifecycle journal = %v, want %v", journal, want)
	}
}
