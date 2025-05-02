package main

import (
	"cloudru/internal/config"
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type BackendServer struct {
	URL          *url.URL
	ReverseProxy *httputil.ReverseProxy
	IsAlive      bool
}

type LoadBalancer struct {
	backends []*BackendServer
	current  uint64
	mu       sync.Mutex
	log      *slog.Logger
}

func NewLoadBalancer(backends []string, log *slog.Logger) *LoadBalancer {
	lb := &LoadBalancer{log: log}
	for _, backendUrl := range backends {
		parsedUrl, err := url.Parse(backendUrl)
		if err != nil {
			lb.log.Error("Failed to parse backend URL:", "error", err)
		}

		proxy := httputil.NewSingleHostReverseProxy(parsedUrl)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			lb.mu.Lock()
			for _, b := range lb.backends {
				if b.URL == parsedUrl {
					b.IsAlive = false
					break
				}
			}
			lb.mu.Unlock()
			lb.log.Info("Backend is not availiable", "url", parsedUrl)

			if nextBackend := lb.GetNextBackend(); nextBackend != nil {
				log.Info("Retrying request with next backend", "url", nextBackend.URL)
				nextBackend.ReverseProxy.ServeHTTP(w, r)
				return
			}

			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}

		lb.backends = append(lb.backends, &BackendServer{
			URL:          parsedUrl,
			ReverseProxy: proxy,
			IsAlive:      true,
		})
	}
	return lb
}

func (lb *LoadBalancer) GetNextBackend() *BackendServer {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	start := lb.current
	for {
		backend := lb.backends[lb.current%uint64(len(lb.backends))]
		lb.current++

		if backend.IsAlive {
			return backend
		}

		if lb.current == start {
			return nil
		}
	}
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend := lb.GetNextBackend()
	if backend == nil {
		http.Error(w, "No available backends", http.StatusServiceUnavailable)
		return
	}

	lb.log.Info("Forwarding request:", "url", backend.URL)
	backend.ReverseProxy.ServeHTTP(w, r)
}

func (lb *LoadBalancer) CheckBackendHealth(backend *BackendServer) bool {
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(backend.URL.String())
	if err != nil {
		lb.log.Debug("Health check failed",
			"url", backend.URL,
			"error", err,
		)
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode < http.StatusBadRequest
}

func (lb *LoadBalancer) RunHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lb.mu.Lock()
			for _, backend := range lb.backends {
				wasAlive := backend.IsAlive
				nowAlive := lb.CheckBackendHealth(backend)

				if wasAlive != nowAlive {
					backend.IsAlive = nowAlive
					status := "up"
					if !nowAlive {
						status = "down"
					}
					lb.log.Info("Backend status changed",
						"url", backend.URL,
						"status", status,
					)
				}
			}
			lb.mu.Unlock()
		}
	}
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "configuration file")
	flag.Parse()
	cfg := config.MustLoad(configPath)

	log := mustMakeLogger(cfg.LogLevel)

	lb := NewLoadBalancer(cfg.Backends, log)

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
