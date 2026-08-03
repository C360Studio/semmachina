// Command bellweather-surface-stack starts the paid Bellweather engine and its
// real beta.159 read surface for a separate same-origin browser acceptance run.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/c360studio/semstreams/component"
	graphgateway "github.com/c360studio/semstreams/gateway/graph-gateway"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	graphquery "github.com/c360studio/semstreams/processor/graph-query"

	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

const (
	defaultConfigPath = "configs/instance.gemini35-flash-lite.bellweather.example.json"
	defaultWorldPath  = "fixtures/worlds/bellweather-maze"
	templateID        = "bellweather-maze"
)

type options struct {
	configPath, worldPath                 string
	playerAddr, graphAddr, diagnosticAddr string
}

type manifest struct {
	Status             string `json:"status"`
	PlayerWebSocketURL string `json:"player_websocket_url"`
	GraphQLURL         string `json:"graphql_url"`
	DiagnosticsURL     string `json:"diagnostics_url"`
	WorldPrefix        string `json:"world_prefix"`
	CampaignID         string `json:"campaign_id"`
}

func main() {
	var opts options
	flag.StringVar(&opts.configPath, "config", defaultConfigPath, "Bellweather Gemini instance configuration")
	flag.StringVar(&opts.worldPath, "world", defaultWorldPath, "Bellweather world package directory")
	flag.StringVar(&opts.playerAddr, "player-addr", "127.0.0.1:43101", "explicit loopback player address")
	flag.StringVar(&opts.graphAddr, "graphql-addr", "127.0.0.1:43102", "explicit loopback GraphQL address")
	flag.StringVar(&opts.diagnosticAddr, "diagnostic-addr", "127.0.0.1:43103", "explicit loopback diagnostic address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "bellweather surface stack: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options) (runErr error) {
	if err := requirePaidOptIn(os.Getenv); err != nil {
		return err
	}
	if err := validateOptions(opts); err != nil {
		return err
	}

	engineLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	freshNATS, err := natsclient.NewSharedTestClient(natsclient.WithJetStream(), natsclient.WithFileStorage())
	if err != nil {
		return fmt.Errorf("start fresh NATS broker: %w", err)
	}
	defer freshNATS.Terminate() //nolint:errcheck

	cfg, packageRoot, err := loadConfig(opts.configPath, opts.worldPath, engineLogger)
	if err != nil {
		return fmt.Errorf("load Bellweather binding: %w", err)
	}
	defer packageRoot.Close() //nolint:errcheck
	cfg.NATSURL = freshNATS.URL
	cfg.WorldNS = fmt.Sprintf("%s-%d", cfg.WorldNS, time.Now().UTC().UnixNano())
	cfg.Socket.Addr = opts.playerAddr

	engine, err := boot.New(cfg)
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}
	runtimeCtx, cancelRuntime := context.WithCancelCause(ctx)
	engineDone := make(chan struct{})
	go func() {
		err := engine.Run(runtimeCtx)
		if err != nil {
			cancelRuntime(fmt.Errorf("player engine stopped: %w", err))
		} else if ctx.Err() == nil && runtimeCtx.Err() == nil {
			cancelRuntime(errors.New("player engine stopped unexpectedly"))
		}
		close(engineDone)
	}()
	defer func() {
		if err := stopEngineSupervisor(cancelRuntime, engineDone, boot.DefaultStopTimeout); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	if err := awaitEngineReady(runtimeCtx, opts.playerAddr); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	queryClient, err := natsclient.NewClient(freshNATS.URL,
		natsclient.WithName("bellweather-surface-query"), natsclient.WithLogger(engineLogger))
	if err != nil {
		return fmt.Errorf("build query client: %w", err)
	}
	if err := queryClient.Connect(runtimeCtx); err != nil {
		return fmt.Errorf("connect query client: %w", err)
	}
	defer closeClient(queryClient)

	queryComponent, gatewayComponent, err := startReadSurface(runtimeCtx, queryClient, cfg.Registry, opts.graphAddr, engineLogger)
	if err != nil {
		return err
	}
	defer stopReadSurface(gatewayComponent, queryComponent)

	worldPrefix := strings.Join([]string{cfg.Org, vocabulary.PlatformSegment, cfg.WorldNS, templateID}, ".")
	locationPrefix := worldPrefix + "." + string(vocabulary.EntityKindLocation)
	locationID, err := vocabulary.ComposeEntityID(cfg.Org, cfg.WorldNS, templateID,
		string(vocabulary.EntityKindLocation), "fete-green-place")
	if err != nil {
		return fmt.Errorf("compose readiness location: %w", err)
	}
	graphqlURL := "http://" + opts.graphAddr + "/graphql"
	if err := awaitGraphQL(runtimeCtx, graphqlURL, locationPrefix, locationID); err != nil {
		return fmt.Errorf("beta.159 GraphQL readiness: %w", err)
	}

	observer, err := newProductionObserver(runtimeCtx, queryClient, cfg.ContentBucket)
	if err != nil {
		return fmt.Errorf("open authoritative observer: %w", err)
	}
	defer func() {
		if err := observer.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close authoritative observer: %w", err))
		}
	}()
	caseID, err := vocabulary.ComposeEntityID(cfg.Org, cfg.WorldNS, templateID,
		string(vocabulary.EntityKindCase), "bellweather-case")
	if err != nil {
		return fmt.Errorf("compose case identity: %w", err)
	}
	diagnosticListener, err := net.Listen("tcp", opts.diagnosticAddr)
	if err != nil {
		return fmt.Errorf("bind diagnostic endpoint: %w", err)
	}
	diagnosticServer := &http.Server{Handler: diagnosticHandler(observer, worldPrefix, caseID), ReadHeaderTimeout: 5 * time.Second}
	defer shutdownDiagnostic(diagnosticServer, 5*time.Second)
	diagnosticErrors := make(chan error, 1)
	go func() {
		if err := diagnosticServer.Serve(diagnosticListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			diagnosticErrors <- errors.New("diagnostic server stopped")
		}
	}()

	ready := manifest{
		Status: "ready", PlayerWebSocketURL: "ws://" + opts.playerAddr + cfg.Socket.Path,
		GraphQLURL: graphqlURL, DiagnosticsURL: "http://" + opts.diagnosticAddr,
		WorldPrefix: worldPrefix, CampaignID: engine.CampaignID(),
	}
	if err := json.NewEncoder(os.Stdout).Encode(ready); err != nil {
		return fmt.Errorf("write ready manifest: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-diagnosticErrors:
		return err
	case <-runtimeCtx.Done():
		if ctx.Err() != nil {
			return nil
		}
		return context.Cause(runtimeCtx)
	}
}

func validateOptions(opts options) error {
	for name, address := range map[string]string{"player": opts.playerAddr, "GraphQL": opts.graphAddr, "diagnostic": opts.diagnosticAddr} {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port == "" || port == "0" {
			return fmt.Errorf("%s address requires an explicit port", name)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("%s address must use a literal loopback IP", name)
		}
	}
	return nil
}

func requirePaidOptIn(getenv func(string) string) error {
	if getenv("SEMMACHINA_PAID_SMOKE") != "1" {
		return errors.New("paid smoke is disabled; set SEMMACHINA_PAID_SMOKE=1 explicitly")
	}
	if strings.TrimSpace(getenv("GEMINI_API_KEY")) == "" {
		return errors.New("GEMINI_API_KEY is empty")
	}
	return nil
}

func loadConfig(configPath, worldPath string, logger *slog.Logger) (boot.Config, *world.PackageDirectory, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return boot.Config{}, nil, fmt.Errorf("read instance file: %w", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		return boot.Config{}, nil, err
	}
	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		return boot.Config{}, nil, err
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		return boot.Config{}, nil, err
	}
	if err := vocabulary.RegisterPredicates(); err != nil {
		return boot.Config{}, nil, err
	}
	packageRoot, err := world.OpenPackageDirectory(worldPath)
	if err != nil {
		return boot.Config{}, nil, err
	}
	if _, err := fs.Stat(packageRoot, world.ManifestFile); err != nil {
		_ = packageRoot.Close()
		return boot.Config{}, nil, err
	}
	cfg.Registry, cfg.World, cfg.Logger = registry, packageRoot, logger
	return cfg, packageRoot, nil
}

func startReadSurface(ctx context.Context, client *natsclient.Client, registry *payloadregistry.Registry,
	graphAddr string, logger *slog.Logger) (component.LifecycleComponent, component.LifecycleComponent, error) {
	deps := component.Dependencies{NATSClient: client, PayloadRegistry: registry, Logger: logger}
	queryCfg := graphquery.DefaultConfig()
	queryCfg.StartupAttempts, queryCfg.StartupInterval = 1, 10*time.Millisecond
	queryRaw, _ := json.Marshal(queryCfg)
	createdQuery, err := graphquery.CreateGraphQuery(queryRaw, deps)
	if err != nil {
		return nil, nil, fmt.Errorf("create graph-query: %w", err)
	}
	query := createdQuery.(component.LifecycleComponent)
	if err := query.Initialize(); err != nil {
		_ = query.Stop(5 * time.Second)
		return nil, nil, fmt.Errorf("initialize graph-query: %w", err)
	}
	if err := query.Start(ctx); err != nil {
		_ = query.Stop(5 * time.Second)
		return nil, nil, fmt.Errorf("start graph-query: %w", err)
	}

	gatewayCfg := graphgateway.DefaultConfig()
	gatewayCfg.StandaloneServer, gatewayCfg.BindAddress = true, graphAddr
	gatewayRaw, _ := json.Marshal(gatewayCfg)
	createdGateway, err := graphgateway.CreateGraphGateway(gatewayRaw, deps)
	if err != nil {
		_ = query.Stop(5 * time.Second)
		return nil, nil, fmt.Errorf("create graph-gateway: %w", err)
	}
	gateway := createdGateway.(component.LifecycleComponent)
	if err := gateway.Initialize(); err != nil {
		stopReadSurface(gateway, query)
		return nil, nil, fmt.Errorf("initialize graph-gateway: %w", err)
	}
	if err := gateway.Start(ctx); err != nil {
		stopReadSurface(gateway, query)
		return nil, nil, fmt.Errorf("start graph-gateway: %w", err)
	}
	return query, gateway, nil
}

type componentStopper interface{ Stop(time.Duration) error }

func stopReadSurface(gateway, query componentStopper) {
	if gateway != nil {
		_ = gateway.Stop(5 * time.Second)
	}
	if query != nil {
		_ = query.Stop(5 * time.Second)
	}
}

type diagnosticServer interface {
	Shutdown(context.Context) error
	Close() error
}

func shutdownDiagnostic(server diagnosticServer, timeout time.Duration) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
	}
}

var playerDialContext = (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext

func awaitEngineReady(ctx context.Context, address string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := playerDialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

func stopEngineSupervisor(cancel context.CancelCauseFunc, done <-chan struct{}, timeout time.Duration) error {
	cancel(errors.New("surface stack stopping"))
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("player engine teardown timeout after %s", timeout)
	}
}

func closeClient(client *natsclient.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}
