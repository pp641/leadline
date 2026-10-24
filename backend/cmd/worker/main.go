// Command worker runs background jobs and the campaign scheduler.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // campaign timezones must resolve even in minimal images

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"leadline/config"
	"leadline/internal/app"
	"leadline/internal/workers"
	"leadline/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}
	log := logger.New(cfg.LogLevel).With("service", "worker")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer a.Close()

	// Metrics and liveness for the worker on a separate port.
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) })
	metricsSrv := &http.Server{Addr: getenv("WORKER_METRICS_ADDR", ":9090"), Handler: mux}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "error", err)
		}
	}()

	workers.Run(ctx, a) // returns after in-flight jobs finish
	_ = metricsSrv.Shutdown(context.Background())
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
