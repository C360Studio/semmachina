// Command bellweather-smoke is an operator-only, explicitly paid production
// smoke. It boots the Bellweather world and plays two bounded turns through the
// real WebSocket adapter: Rowan observes Harold's body, then explicitly requests
// Kit's help. Each turn runs its own production provider call chain.
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
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	"github.com/gorilla/websocket"

	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	bellweatherConfigPath = "configs/instance.gemini36-flash.bellweather.example.json"
	bellweatherWorldPath  = "fixtures/worlds/bellweather-maze"
	bellweatherTemplate   = "bellweather-maze"
	absoluteTimeout       = 3 * time.Minute
	pollInterval          = 30 * time.Second
	stallTimeout          = 60 * time.Second
	casePhasePoll         = 500 * time.Millisecond
	casePhaseTimeout      = 30 * time.Second
	observationTarget     = "victim-harold-wren"
	observationActionText = "I observe Harold Wren's body carefully, looking at the body itself " +
		"before investigating further."
	hintActionText = "I explicitly ask Kit Finch for a hint about what we observed."
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bellweather smoke: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := requirePaidOptIn(os.Getenv); err != nil {
		return err
	}
	configPath := flag.String("config", bellweatherConfigPath, "Bellweather Gemini instance configuration")
	worldPath := flag.String("world", bellweatherWorldPath, "Bellweather world package directory")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), absoluteTimeout)
	defer cancel()
	runID := time.Now().UTC().UnixNano()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// The production components never receive the smoke's diagnostic sink. This
	// keeps provider/config/request bodies out even if an upstream error starts
	// attaching them later; the operator surface below logs closed fields only.
	engineLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg, err := loadSmokeConfig(*configPath, *worldPath, engineLogger)
	if err != nil {
		return fmt.Errorf("configuration refused: %w", err)
	}
	// Every paid proof must begin at the authored cold_open. Reusing the base
	// namespace would silently inherit an earlier smoke's discovery phase and
	// turn the body-observation assertion into a no-op.
	cfg.WorldNS = smokeWorldNamespace(cfg.WorldNS, runID)
	engine, err := boot.New(cfg)
	if err != nil {
		return fmt.Errorf("production boot configuration refused: %w", err)
	}
	defer engine.Stop()
	if err := engine.Start(ctx); err != nil {
		return errors.New("production boot failed before smoke ingress")
	}
	logger.Info("production engine started",
		"campaign", engine.CampaignID(), "scene", engine.SceneID(), "player", engine.PlayerID())

	observerClient, err := natsclient.NewClient(cfg.NATSURL,
		natsclient.WithName("bellweather-smoke-observer"), natsclient.WithLogger(engineLogger))
	if err != nil {
		return fmt.Errorf("build state observer: %w", err)
	}
	if err := observerClient.Connect(ctx); err != nil {
		return fmt.Errorf("connect state observer: %w", err)
	}
	defer closeObserver(observerClient)
	observer, err := newProductionObserver(ctx, observerClient)
	if err != nil {
		return fmt.Errorf("open authoritative state observer: %w", err)
	}

	conn, err := dial(ctx, engine.Addr(), cfg)
	if err != nil {
		return errors.New("WebSocket authentication or connection failed")
	}
	defer conn.Close() //nolint:errcheck // best-effort operator smoke teardown
	frames, socketErrors := readFrames(conn)

	_, err = playTurn(ctx, conn, frames, socketErrors, observer, cfg, logger,
		fmt.Sprintf("bellweather-observe-%d", runID), observationActionText, validateTerminalDelivery)
	if err != nil {
		return fmt.Errorf("body-observation turn: %w", err)
	}
	caseEntityID, err := vocabulary.ComposeEntityID(
		cfg.Org, cfg.WorldNS, bellweatherTemplate, string(vocabulary.EntityKindCase), "bellweather-case")
	if err != nil {
		return fmt.Errorf("compose Bellweather case identity: %w", err)
	}
	if err := awaitCasePhase(ctx, observer, caseEntityID, vocabulary.CasePhaseDiscovery, casePhasePolicy{
		PollInterval: casePhasePoll, Timeout: casePhaseTimeout, Wait: waitContext,
	}); err != nil {
		return fmt.Errorf("body-observation lifecycle proof: %w", err)
	}
	logger.Info("body observation advanced the case", "case_phase", vocabulary.CasePhaseDiscovery)

	delivery, err := playTurn(ctx, conn, frames, socketErrors, observer, cfg, logger,
		fmt.Sprintf("bellweather-kit-hint-%d", runID), hintActionText, validateKitDelivery)
	if err != nil {
		return fmt.Errorf("Kit hint turn: %w", err)
	}
	hintTurnEntityID, err := (turn.Identity{
		Org: cfg.Org, WorldNS: cfg.WorldNS, Template: bellweatherTemplate,
	}).EntityID(delivery.Result.TurnID)
	if err != nil {
		return fmt.Errorf("compose authoritative Kit hint turn identity: %w", err)
	}
	if err := proveHintTrigger(ctx, observer, hintTurnEntityID); err != nil {
		return fmt.Errorf("Kit hint route proof: %w", err)
	}
	logger.Info("Bellweather Gemini smoke passed",
		"turn", delivery.Result.TurnID, "phase", delivery.Result.Phase,
		"companion_kind", delivery.Result.CompanionResolution.Kind, "provider_turns", 2)
	return nil
}

type deliveryValidator func(*payload.TurnDelivery, boot.Config) error

func playTurn(
	ctx context.Context,
	conn *websocket.Conn,
	frames <-chan *playersocket.Frame,
	socketErrors <-chan error,
	observer turnObserver,
	cfg boot.Config,
	logger *slog.Logger,
	key string,
	text string,
	validate deliveryValidator,
) (*payload.TurnDelivery, error) {
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	request, err := json.Marshal(map[string]any{
		"protocol": payload.PlayerProtocolV1, "idempotency_key": key, "text": text,
	})
	if err != nil {
		return nil, errors.New("encode the fixed smoke action")
	}
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		return nil, errors.New("send the fixed smoke action")
	}
	response, err := awaitSubmission(ctx, frames, socketErrors, key)
	if err != nil {
		return nil, err
	}
	if response.Status != payload.StatusAccepted {
		return nil, fmt.Errorf("production ingress refused the fixed smoke action (%s)", response.Refusal.Code)
	}
	logger.Info("action accepted", "action", response.ActionID, "turn", response.TurnID)

	turnEntityID, err := (turn.Identity{
		Org: cfg.Org, WorldNS: cfg.WorldNS, Template: bellweatherTemplate,
	}).EntityID(response.TurnID)
	if err != nil {
		return nil, fmt.Errorf("compose turn identity: %w", err)
	}
	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- monitorTurn(turnCtx, observer, turnEntityID, monitorPolicy{
			PollInterval: pollInterval,
			StallAfter:   stallTimeout,
			Wait:         waitContext,
			OnSnapshot: func(snapshot turnSnapshot) {
				logger.Info("authoritative turn poll", "phase", snapshot.Phase, "pending", snapshot.Pending)
			},
		})
	}()
	return awaitTerminalDelivery(
		turnCtx, frames, socketErrors, monitorDone, response.TurnID, validate, cfg, stallTimeout)
}

func awaitTerminalDelivery(
	ctx context.Context,
	frames <-chan *playersocket.Frame,
	socketErrors <-chan error,
	monitorDone <-chan error,
	turnID string,
	validate deliveryValidator,
	cfg boot.Config,
	deliveryAfterComplete time.Duration,
) (*payload.TurnDelivery, error) {
	var (
		deliveryTimer    *time.Timer
		deliveryDeadline <-chan time.Time
	)
	defer func() {
		if deliveryTimer != nil {
			deliveryTimer.Stop()
		}
	}()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return nil, errors.New("WebSocket closed before terminal delivery")
			}
			if frame.Type != playersocket.FrameTurnDelivery || frame.Delivery == nil ||
				frame.Delivery.Result.TurnID != turnID {
				continue
			}
			if validate == nil {
				return nil, errors.New("terminal delivery validator is absent")
			}
			if err := validate(frame.Delivery, cfg); err != nil {
				return nil, err
			}
			return frame.Delivery, nil
		case err := <-monitorDone:
			monitorDone = nil
			if err != nil {
				return nil, err
			}
			if deliveryAfterComplete <= 0 {
				return nil, errors.New("terminal delivery deadline must be positive")
			}
			deliveryTimer = time.NewTimer(deliveryAfterComplete)
			deliveryDeadline = deliveryTimer.C
		case <-deliveryDeadline:
			return nil, fmt.Errorf(
				"terminal WebSocket delivery did not arrive within %s after authoritative completion",
				deliveryAfterComplete)
		case <-socketErrors:
			return nil, errors.New("WebSocket read failed before terminal delivery")
		case <-ctx.Done():
			return nil, fmt.Errorf("absolute %s smoke timeout reached", absoluteTimeout)
		}
	}
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

func smokeWorldNamespace(base string, runID int64) string {
	return fmt.Sprintf("%s-%d", base, runID)
}

func loadSmokeConfig(configPath, worldPath string, logger *slog.Logger) (boot.Config, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return boot.Config{}, fmt.Errorf("read instance file: %w", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		return boot.Config{}, err
	}
	if err := validateBellweatherBinding(cfg); err != nil {
		return boot.Config{}, err
	}
	world := os.DirFS(worldPath)
	if _, err := fs.Stat(world, "manifest.yaml"); err != nil {
		return boot.Config{}, fmt.Errorf("open Bellweather world package: %w", err)
	}
	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		return boot.Config{}, fmt.Errorf("register framework payloads: %w", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		return boot.Config{}, fmt.Errorf("register SemMachina payloads: %w", err)
	}
	if err := vocabulary.RegisterPredicates(); err != nil {
		return boot.Config{}, fmt.Errorf("register predicates: %w", err)
	}
	cfg.Registry, cfg.World, cfg.Logger = registry, world, logger
	return cfg, nil
}

func validateBellweatherBinding(cfg boot.Config) error {
	if cfg.SceneLocalID != "fete-green" || cfg.Player.Name != "Rowan Vale" ||
		cfg.Player.Character != "local:rowan-vale" {
		return errors.New("instance must bind scene fete-green and player Rowan Vale/local:rowan-vale")
	}
	if cfg.Companion == nil || cfg.Companion.Character != "local:kit-finch" ||
		cfg.Companion.Policy != vocabulary.CompanionPolicyBoundedInitiative {
		return errors.New("instance must bind Kit Finch under bounded-initiative")
	}
	if cfg.Models == nil {
		return errors.New("instance must declare the Gemini model registry")
	}
	endpoint := cfg.Models.Endpoints["gemini-flash"]
	if endpoint == nil || endpoint.Provider != "gemini" || endpoint.Model != "gemini-3.6-flash" ||
		endpoint.APIKeyEnv != "GEMINI_API_KEY" || endpoint.WireBackend != "wire" ||
		!endpoint.SupportsTools || endpoint.ToolFormat != "openai" {
		return errors.New("instance must bind the gemini-3.6-flash endpoint through GEMINI_API_KEY")
	}
	for _, capability := range []string{"casekeeping", "companion_decision", "fiction_adjudication", "narration"} {
		declaration := cfg.Models.Capabilities[capability]
		if declaration == nil || len(declaration.Preferred) != 1 ||
			declaration.Preferred[0] != "gemini-flash" || !declaration.RequiresTools {
			return fmt.Errorf("instance capability %s must require only tool-capable gemini-flash", capability)
		}
	}
	return nil
}

func dial(ctx context.Context, addr string, cfg boot.Config) (*websocket.Conn, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Player.Credential)
	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, "ws://"+addr+playersocket.DefaultPath, header)
	return conn, err
}

func readFrames(conn *websocket.Conn) (<-chan *playersocket.Frame, <-chan error) {
	frames := make(chan *playersocket.Frame)
	errorsOut := make(chan error, 1)
	go func() {
		defer close(frames)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				errorsOut <- err
				return
			}
			var frame playersocket.Frame
			if err := json.Unmarshal(raw, &frame); err != nil || frame.Validate() != nil {
				errorsOut <- errors.New("invalid WebSocket response frame")
				return
			}
			frames <- &frame
		}
	}()
	return frames, errorsOut
}

func awaitSubmission(
	ctx context.Context,
	frames <-chan *playersocket.Frame,
	socketErrors <-chan error,
	key string,
) (*payload.SubmitResponse, error) {
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return nil, errors.New("WebSocket closed before the submit response")
			}
			if frame.Type == playersocket.FrameSubmitResponse && frame.Response != nil &&
				frame.Response.IdempotencyKey == key {
				return frame.Response, nil
			}
		case <-socketErrors:
			return nil, errors.New("WebSocket read failed before the submit response")
		case <-ctx.Done():
			return nil, fmt.Errorf("absolute %s smoke timeout reached", absoluteTimeout)
		}
	}
}

func validateTerminalDelivery(delivery *payload.TurnDelivery, _ boot.Config) error {
	if err := delivery.Validate(); err != nil {
		return errors.New("terminal WebSocket delivery violated the player protocol")
	}
	if delivery.Result.Phase == vocabulary.PhaseFailed {
		return fmt.Errorf("turn failed terminally (%s)", delivery.Result.FailureReason)
	}
	if delivery.Result.Phase != vocabulary.PhaseComplete {
		return fmt.Errorf("delivery phase %q is not terminal", delivery.Result.Phase)
	}
	return nil
}

func validateKitDelivery(delivery *payload.TurnDelivery, cfg boot.Config) error {
	if err := validateTerminalDelivery(delivery, cfg); err != nil {
		return err
	}
	companion := delivery.Result.CompanionResolution
	if companion == nil {
		return errors.New("terminal delivery carries no Kit exchange")
	}
	wantKit, err := vocabulary.ComposeEntityID(
		cfg.Org, cfg.WorldNS, bellweatherTemplate, string(vocabulary.EntityKindCharacter), "kit-finch")
	if err != nil {
		return fmt.Errorf("compose Kit identity: %w", err)
	}
	if companion.CompanionID != wantKit {
		return errors.New("terminal delivery carries a companion exchange from someone other than Kit")
	}
	if delivery.Narration == nil || !strings.Contains(strings.ToLower(delivery.Narration.Prose), "kit") {
		return errors.New("terminal delivery carries no narrated Kit exchange")
	}
	return nil
}

func closeObserver(client *natsclient.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}
