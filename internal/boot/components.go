package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/c360studio/semstreams/component"
	ssconfig "github.com/c360studio/semstreams/config"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/lifecycle"
	"github.com/c360studio/semstreams/pkg/projection"
	agenticloop "github.com/c360studio/semstreams/processor/agentic-loop"
	agenticmodel "github.com/c360studio/semstreams/processor/agentic-model"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	graphindex "github.com/c360studio/semstreams/processor/graph-index"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"
	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/service"
	"github.com/c360studio/semstreams/storage/objectstore"
	"github.com/c360studio/semstreams/types"

	"github.com/c360studio/semmachina/internal/companion"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/stage"
)

const componentConfigVersion = "1.1.0"

func componentConfig(name string, kind types.ComponentType, raw json.RawMessage) types.ComponentConfig {
	return types.ComponentConfig{Type: kind, Name: name, Enabled: true, Config: raw}
}

func marshalComponentConfig(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// prepareComponentBarriers admits every framework component before the first
// activation. The Sequence owns ordering between these immutable managers;
// ComponentManager owns lifecycle inside each barrier.
func (e *Engine) prepareComponentBarriers(ctx context.Context) error {
	e.lifecycle = lifecycle.NewManager(e.client, e.log())

	graphRegistry := component.NewRegistry(component.WithLogger(e.log()))
	if err := graphingest.Register(graphRegistry); err != nil {
		return fmt.Errorf("register graph-ingest: %w", err)
	}
	if err := graphindex.Register(graphRegistry); err != nil {
		return fmt.Errorf("register graph-index: %w", err)
	}
	graphConfigs, err := graphComponentConfigs()
	if err != nil {
		return err
	}
	graphManager, err := e.newComponentManager(ctx, "graph", graphRegistry, graphConfigs, nil)
	if err != nil {
		return err
	}
	e.graphManager = graphManager

	e.tools = agentictools.NewExecutorRegistry()
	agenticRegistry := component.NewRegistry(component.WithLogger(e.log()))
	for name, register := range map[string]func(*component.Registry) error{
		"objectstore":   objectstore.Register,
		"agentic-tools": func(registry *component.Registry) error { return agentictools.Register(registry) },
		"agentic-model": func(registry *component.Registry) error { return agenticmodel.Register(registry) },
		"agentic-loop":  agenticloop.Register,
	} {
		if err := register(agenticRegistry); err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
	}
	agenticConfigs, err := e.agenticComponentConfigs()
	if err != nil {
		return err
	}
	agenticManager, err := e.newComponentManager(ctx, "agentic", agenticRegistry, agenticConfigs, e.tools)
	if err != nil {
		return err
	}
	e.agenticManager = agenticManager

	managedBackend, err := content.NewManagedBackend(e.client, agenticManager, e.cfg.ContentBucket)
	if err != nil {
		return err
	}
	artifacts, err := content.NewStore(managedBackend)
	if err != nil {
		return err
	}
	e.content = artifacts
	if err := e.registerAgenticTools(); err != nil {
		return err
	}

	ruleRaw, err := e.ruleProcessorConfig()
	if err != nil {
		return fmt.Errorf("build the rule processor configuration: %w", err)
	}
	ruleRegistry := component.NewRegistry(component.WithLogger(e.log()))
	if err := rule.Register(ruleRegistry); err != nil {
		return fmt.Errorf("register rule-processor: %w", err)
	}
	ruleManager, err := e.newComponentManager(ctx, "rule", ruleRegistry, ssconfig.ComponentConfigs{
		"rule-processor": componentConfig("rule-processor", types.ComponentTypeProcessor, ruleRaw),
	}, e.tools)
	if err != nil {
		return err
	}
	e.ruleManager = ruleManager
	if err := bindRulePackProjection(e.client, ruleManager); err != nil {
		return err
	}

	return validateUnionManifest(graphManager, agenticManager, ruleManager)
}

// graphComponentConfigs serializes explicit defaults because ComponentConfig
// crosses ConfigManager's JSON boundary. A nil RawMessage becomes JSON null;
// graph-ingest decodes null as a zero Config and deliberately does not invent
// missing port declarations, so generic best-effort admission would drop the
// sole graph writer. Explicit defaults make the admitted topology visible and
// stable at the composition root.
func graphComponentConfigs() (ssconfig.ComponentConfigs, error) {
	ingestRaw, err := marshalComponentConfig(graphingest.DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("encode graph-ingest configuration: %w", err)
	}
	indexRaw, err := marshalComponentConfig(graphindex.DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("encode graph-index configuration: %w", err)
	}
	return ssconfig.ComponentConfigs{
		"graph-ingest": componentConfig("graph-ingest", types.ComponentTypeProcessor, ingestRaw),
		"graph-index":  componentConfig("graph-index", types.ComponentTypeProcessor, indexRaw),
	}, nil
}

func bindRulePackProjection(client *natsclient.Client, manager *service.ComponentManager) error {
	if client == nil || manager == nil {
		return errors.New("bind rule-pack projection: NATS client and component manager are required")
	}
	managed := manager.GetManagedComponents()[ruleProcessorComponent]
	if managed == nil {
		return fmt.Errorf("bind rule-pack projection: component %q is not admitted", ruleProcessorComponent)
	}
	binder, ok := managed.Component.(service.ProjectionBinder)
	if !ok {
		return fmt.Errorf("bind rule-pack projection: component %q is a %T, not a projection binder",
			ruleProcessorComponent, managed.Component)
	}
	if err := binder.PreflightProjectionMutations(); err != nil {
		return fmt.Errorf("preflight selected mechanics projection: %w", err)
	}
	packID, contracts := binder.ProjectionBindings()
	if len(contracts) == 0 {
		return nil
	}
	reconciler, err := projection.NewMutationClient(projection.MutationClientConfig{
		NATS: client, Contracts: contracts,
	})
	if err != nil {
		return fmt.Errorf("build predicate reconciler for rule pack %q: %w", packID, err)
	}
	if err := binder.SetPredicateReconciler(reconciler); err != nil {
		return fmt.Errorf("inject predicate reconciler for rule pack %q: %w", packID, err)
	}
	return nil
}

func (e *Engine) newComponentManager(
	_ context.Context,
	barrier string,
	registry *component.Registry,
	components ssconfig.ComponentConfigs,
	tools component.ToolRegistryReader,
) (*service.ComponentManager, error) {
	cfg := &ssconfig.Config{
		Version: componentConfigVersion,
		Platform: ssconfig.PlatformConfig{
			Org: e.cfg.Org, ID: "semmachina-" + e.cfg.WorldNS, Type: "application", Environment: "runtime",
		},
		Components:    components,
		ModelRegistry: e.cfg.Models,
	}
	managerConfig, err := ssconfig.NewConfigManager(cfg, e.client, e.log())
	if err != nil {
		return nil, fmt.Errorf("build %s component config manager: %w", barrier, err)
	}
	constructed, err := service.NewComponentManager(json.RawMessage(`{"watch_config":false}`), &service.Dependencies{
		NATSClient: e.client, Logger: e.log(),
		Platform: types.PlatformMeta{Org: e.cfg.Org, Platform: "semmachina-" + e.cfg.WorldNS},
		Manager:  managerConfig, ComponentRegistry: registry, ToolRegistry: tools,
		PayloadRegistry: e.cfg.Registry, LifecycleManager: e.lifecycle,
	})
	if err != nil {
		return nil, fmt.Errorf("build %s component manager: %w", barrier, err)
	}
	manager, ok := constructed.(*service.ComponentManager)
	if !ok {
		return nil, fmt.Errorf("build %s component manager: constructor returned %T", barrier, constructed)
	}
	managed := manager.GetManagedComponents()
	if len(managed) != len(components) {
		return nil, fmt.Errorf("build %s component manager: admitted %d of %d configured components",
			barrier, len(managed), len(components))
	}
	for name := range components {
		entry := managed[name]
		if entry == nil || entry.State != component.StateInitialized {
			return nil, fmt.Errorf("build %s component manager: component %q was not initialized", barrier, name)
		}
	}
	return manager, nil
}

func (e *Engine) startManager(ctx context.Context, name string, manager *service.ComponentManager) error {
	if manager == nil {
		return fmt.Errorf("%s component manager was not prepared", name)
	}
	return e.activateManager(ctx, name, manager, func() error {
		for componentName, managed := range manager.GetManagedComponents() {
			if managed.State != component.StateStarted {
				return fmt.Errorf("component %q is %s", componentName, managed.State)
			}
		}
		return nil
	})
}

func (e *Engine) activateManager(
	ctx context.Context,
	name string,
	manager componentManagerLifecycle,
	verify func() error,
) error {
	if manager == nil {
		return fmt.Errorf("%s component manager was not prepared", name)
	}
	// ComponentManager can return from Start after activating an earlier member
	// of its barrier. Transfer cleanup ownership before the attempt so Engine's
	// reverse-order unwind also stops a partially started barrier.
	e.managers = append(e.managers, namedManager{name: name, manager: manager})
	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("start %s component manager: %w", name, err)
	}
	if verify != nil {
		if err := verify(); err != nil {
			return fmt.Errorf("start %s component manager: %w", name, err)
		}
	}
	return nil
}

func (e *Engine) agenticComponentConfigs() (ssconfig.ComponentConfigs, error) {
	storageConfig := objectstore.DefaultConfig()
	storageConfig.BucketName = e.cfg.ContentBucket
	storageConfig.DataCache.Enabled = false
	storageRaw, err := marshalComponentConfig(storageConfig)
	if err != nil {
		return nil, fmt.Errorf("encode content objectstore configuration: %w", err)
	}

	tools := agentictools.DefaultConfig()
	tools.StreamName = stage.TaskStream
	tools.ApprovalRequired = nil
	tools.AllowedTools = nil
	toolsRaw, err := marshalComponentConfig(tools)
	if err != nil {
		return nil, fmt.Errorf("encode agentic-tools configuration: %w", err)
	}

	models := agenticmodel.DefaultConfig()
	modelsRaw, err := marshalComponentConfig(models)
	if err != nil {
		return nil, fmt.Errorf("encode agentic-model configuration: %w", err)
	}

	loop := agenticloop.DefaultConfig()
	loop.MaxIterations = maxPersonaIterations()
	loop.Timeout = maxPersonaTimeout().String()
	loop.StreamName = stage.TaskStream
	loop.TrajectoryEvidenceStorageInstance = content.ManagedStorageInstance
	loop.ToolCallGovernance = agenticloop.ToolCallGovernanceConfig{Mode: agenticloop.ToolCallGovernanceModeDisabled}
	loop.SynthesizeTerminalOnCompletion = false
	loopRaw, err := marshalComponentConfig(loop)
	if err != nil {
		return nil, fmt.Errorf("encode agentic-loop configuration: %w", err)
	}

	return ssconfig.ComponentConfigs{
		content.ManagedStorageInstance: componentConfig("objectstore", types.ComponentTypeStorage, storageRaw),
		"agentic-tools":                componentConfig("agentic-tools", types.ComponentTypeProcessor, toolsRaw),
		"agentic-model":                componentConfig("agentic-model", types.ComponentTypeProcessor, modelsRaw),
		"agentic-loop":                 componentConfig("agentic-loop", types.ComponentTypeProcessor, loopRaw),
	}, nil
}

func (e *Engine) registerAgenticTools() error {
	if err := persona.RegisterTools(e.tools, e.content, e.graph); err != nil {
		return err
	}
	authority, err := companion.NewAuthority(e.graph)
	if err != nil {
		return err
	}
	scope, err := e.epistemicScope()
	if err != nil {
		return err
	}
	assembler, err := scene.NewAssembler(e.graph)
	if err != nil {
		return err
	}
	projector, err := epistemic.NewProjector(assembler, e.graph, scope,
		epistemic.WithCompanionBondValidator(authority))
	if err != nil {
		return err
	}
	executor, err := companion.NewExecutor(e.content, e.graph, authority, projector)
	if err != nil {
		return err
	}
	if err := e.tools.RegisterExecutor(executor); err != nil {
		return fmt.Errorf("register the %s tool: %w", persona.CompanionDecisionToolName, err)
	}
	e.companionExhaust = executor
	return nil
}

// validateUnionManifest validates cross-barrier invariants before the first
// manager starts. ComponentManager's public flow graph is manager-local, so the
// composition root folds only declaration facts; it does not reproduce any
// lifecycle machinery.
func validateUnionManifest(managers ...*service.ComponentManager) error {
	groups := make([]map[string]component.Discoverable, len(managers))
	for index, manager := range managers {
		if manager == nil {
			return fmt.Errorf("component manifest manager %d is nil", index)
		}
		groups[index] = manager.ListComponents()
	}
	return validateUnionComponents(groups...)
}

func validateUnionComponents(groups ...map[string]component.Discoverable) error {
	const (
		mutationSubject = "graph.mutation.>"
		mutationType    = "semstreams.graph.mutation"
		mutationVersion = "v1"
	)
	providers := 0
	storageProviders := 0
	seenNames := make(map[string]string)
	for index, group := range groups {
		for name, discoverable := range group {
			if previous, duplicate := seenNames[name]; duplicate {
				return fmt.Errorf("component %q appears in both %s and manager-%d", name, previous, index)
			}
			seenNames[name] = fmt.Sprintf("manager-%d", index)
			if _, ok := discoverable.(component.StoreProvider); ok {
				storageProviders++
			}
			for _, port := range append(append([]component.Port(nil), discoverable.InputPorts()...), discoverable.OutputPorts()...) {
				facts, err := port.Facts()
				if err != nil {
					return fmt.Errorf("component %s port %s: %w", name, port.Name, err)
				}
				contract, hasContract := facts.Interface()
				connections := facts.ConnectionIDs()
				isMutation := hasContract && contract.Type == mutationType
				for _, connection := range connections {
					isMutation = isMutation || strings.HasPrefix(connection, "graph.mutation.")
				}
				if !isMutation {
					continue
				}
				if facts.Kind() != component.PortKindNATSRequest || len(connections) != 1 ||
					connections[0] != mutationSubject || !hasContract || contract.Type != mutationType ||
					contract.Version != mutationVersion || !port.Required {
					return fmt.Errorf("component %s port %s is not the canonical required graph mutation port", name, port.Name)
				}
				if port.Direction == component.DirectionInput {
					providers++
				}
			}
		}
	}
	if providers != 1 {
		return fmt.Errorf("component manifest requires exactly one graph mutation provider, found %d", providers)
	}
	if storageProviders != 1 {
		return fmt.Errorf("component manifest requires exactly one storage provider, found %d", storageProviders)
	}
	return nil
}
