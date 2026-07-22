// Package main is the entry point for the Argus service.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ismetkoralay/argus/internal/config"
	"github.com/ismetkoralay/argus/internal/githubapp"
	"github.com/ismetkoralay/argus/internal/health"
	"github.com/ismetkoralay/argus/internal/llm"
	"github.com/ismetkoralay/argus/internal/logging"
	"github.com/ismetkoralay/argus/internal/metrics"
	"github.com/ismetkoralay/argus/internal/review"
	"github.com/ismetkoralay/argus/internal/webhook"
)

func main() {
	// A default-level logger to report a config-load failure with (LOG_LEVEL
	// itself is one of the things config.Load() validates, so it can't
	// shape the logger used to report that it's invalid). Rebuilt below at
	// the operator's requested level once config.Load() succeeds.
	logger := logging.New("", os.Stdout)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	logger = logging.New(cfg.LogLevel, os.Stdout)

	ghClient, err := githubapp.New(cfg.GitHubAppID, cfg.GitHubPrivateKeyPEM)
	if err != nil {
		logger.Error("failed to build github app client", "err", err)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	recorder := metrics.NewRecorder(reg)

	provider := llm.NewOllamaProvider(cfg.OllamaBaseURL, cfg.OllamaModel, nil, logger)
	orchestrator := review.NewOrchestrator(provider, ghClient, logger, recorder)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Handler)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("POST /webhooks/github", webhook.NewHandler(cfg.GitHubWebhookSecret, orchestrator, logger))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
	logger.Info("server stopped")
}
