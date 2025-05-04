package main

import (
	"cloudru/internal/balancer"
	"cloudru/internal/config"
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "configuration file")
	flag.Parse()
	cfg := config.MustLoad(configPath)

	log := mustMakeLogger(cfg.LogLevel)

	lb := balancer.NewLoadBalancer(cfg.Backends, log, cfg.Algorithm)

	log.Info("Load balancer started on address", "address", cfg.Address)

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	)
	defer stop()

	go lb.RunHealthChecks(ctx, 10*time.Second)

	server := http.Server{
		Addr:        cfg.Address,
		Handler:     lb,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		log.Debug("shutting down server")
		if err := server.Shutdown(context.Background()); err != nil {
			log.Error("erroneous shutdown", "error", err)
		}
	}()

	log.Info("Running HTTP server", "address", cfg.Address)
	if err := server.ListenAndServe(); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server closed unexpectedly", "error", err)
			return
		}
	}
}

func mustMakeLogger(logLevel string) *slog.Logger {
	var level slog.Level
	switch logLevel {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "ERROR":
		level = slog.LevelError
	default:
		panic("unknown log level: " + logLevel)
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level, AddSource: true})
	return slog.New(handler)
}
