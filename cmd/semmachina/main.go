// Command semmachina runs one world instance: the whole turn loop, in one
// process, against one NATS broker.
//
// Instance-per-world is the MVP's deployment shape — one world, one stack — and
// this binary is that stack. It composes the framework components (graph-ingest,
// graph-index, the rule processor, the agentic loop) with SemMachina's own
// (world importer, turn intake, the five stages, the effect applier, the
// campaign ledger, the player gateway and socket) and starts them in the one
// order that is correct.
//
// The order lives in internal/boot, not here, and it is enforced there rather
// than described: each step declares what must already have happened, and a
// sequence whose order contradicts its own dependencies stops with a sentence
// instead of losing a player's turn.
//
// Usage:
//
//	semmachina -config instance.json [-world DIR] [-nats URL] [-addr HOST:PORT]
//
// The world package defaults to the starter world EMBEDDED in this binary, so a
// deployment cannot boot into an empty world because somebody forgot to copy a
// directory.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
	worldpkg "github.com/c360studio/semmachina/internal/world"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "semmachina: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", "", "path to the instance configuration JSON (required)")
		worldPath  = flag.String("world", "", "path to a world package directory (default: the embedded starter world)")
		natsURL    = flag.String("nats", "", "NATS URL, overriding the configuration")
		addr       = flag.String("addr", "", "player socket address, overriding the configuration")
		logLevel   = flag.String("log-level", "info", "log level: debug, info, warn, error")
	)
	flag.Parse()

	logger, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	if *configPath == "" {
		return errors.New(
			"no -config given; an instance's org, world namespace, player and model registry are deployment " +
				"facts this binary will not guess at")
	}
	raw, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read the instance configuration: %w", err)
	}
	cfg, err := boot.LoadConfig(raw)
	if err != nil {
		return err
	}
	if *natsURL != "" {
		cfg.NATSURL = *natsURL
	}
	if *addr != "" {
		cfg.Socket.Addr = *addr
	}
	cfg.Logger = logger

	// The payload registry is built HERE, at the binary's bootstrap, and handed
	// to the engine. Every binary does this: a decoder over a registry that does
	// not know payload.PlayerAction consumes every player action, decodes none of
	// them, accepts no turns, and reports nothing — "this is not a player action"
	// is an ordinary answer for a decoder. internal/boot checks the registry
	// rather than trusting it, and cmd/semmachina's own test greps every main
	// package for this call, because the one binary that forgets is the one that
	// never gets a test.
	registry := payloadregistry.New()
	if err := payloadbuiltins.Register(registry); err != nil {
		return fmt.Errorf("register the framework payloads: %w", err)
	}
	if err := payload.RegisterPayloads(registry); err != nil {
		return fmt.Errorf("register the SemMachina payloads: %w", err)
	}
	cfg.Registry = registry

	// The predicate registry is process-global and upstream's. Rule-config
	// validation rejects a canonical-but-undeclared predicate, so the
	// turn-sequencing pack does not load until this has run — silently, with the
	// engine simply never adjudicating anything.
	if err := vocabulary.RegisterPredicates(); err != nil {
		return fmt.Errorf("register the SemMachina predicates: %w", err)
	}

	world, closeWorld, err := worldPackage(*worldPath)
	if err != nil {
		return err
	}
	defer func() { _ = closeWorld() }()
	cfg.World = world

	engine, err := boot.New(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting semmachina",
		"campaign", engine.CampaignID(), "scene", engine.SceneID(), "player", engine.PlayerID())
	if err := engine.Run(ctx); err != nil {
		return err
	}
	logger.Info("semmachina stopped")
	return nil
}

// worldPackage returns the world package to instantiate from.
//
// The embedded starter world is the default, deliberately: a world package
// travels inside the binary, so a broken package is a build failure rather than
// a deployment that boots into an empty world. A directory is the escape hatch
// for authoring, and is the same fs.FS shape, so the loader cannot tell them
// apart and neither path is the privileged one.
func worldPackage(path string) (fs.FS, func() error, error) {
	if path == "" {
		world, err := fixtures.StarterWorld()
		return world, func() error { return nil }, err
	}
	root, err := worldpkg.OpenPackageDirectory(path)
	if err != nil {
		return nil, nil, err
	}
	return root, root.Close, nil
}

func newLogger(level string) (*slog.Logger, error) {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", level, err)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed})), nil
}
