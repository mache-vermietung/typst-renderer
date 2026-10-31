// Package main runs the isolated Typst renderer daemon.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/tobima/domio/internal/infrastructure/grpcserver"
	"github.com/tobima/domio/internal/infrastructure/telemetry"
	"github.com/tobima/domio/internal/renderer"
	rendererv1 "github.com/tobima/domio/internal/renderer/proto/renderer/v1"
)

func main() {
	logger := telemetry.InitLogger()

	if err := run(logger); err != nil {
		logger.Error("renderer daemon terminated with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	if err := godotenv.Load(); err != nil {
		slog.Debug("No .env file found or error loading it")
	}

	tp, err := telemetry.InitTracerForService(ctx, "domio-renderer")
	if err != nil {
		return err
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if shutdownErr := tp.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Warn("error shutting down tracer", "error", shutdownErr)
		}
	}()

	cfg := renderer.LoadConfigFromEnv()

	srv, err := renderer.NewServer(cfg)
	if err != nil {
		return err
	}

	grpcSrv := grpcserver.New(cfg.ListenAddr, renderer.GRPCServerOptions(cfg)...)
	rendererv1.RegisterRendererServiceServer(grpcSrv.GRPCServer(), srv)

	errCh := make(chan error, 1)

	go func() {
		logger.Info("starting renderer daemon",
			"addr", cfg.ListenAddr,
			"concurrency", cfg.MaxConcurrency,
			"max_request_bytes", cfg.MaxRequestBytes,
			"max_timeout_seconds", cfg.MaxTimeoutSeconds,
			"binary", cfg.TypstBinaryPath,
		)

		if err := grpcSrv.Start(); err != nil {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		logger.Info("shutting down renderer daemon", "signal", sig.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := grpcSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("error shutting down grpc server", "error", err)
		}

		logger.Info("renderer daemon stopped gracefully")

		return nil
	}
}
