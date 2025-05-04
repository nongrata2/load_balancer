package main

import (
	"cloudru/internal/config"
	"context"
	"errors"
	"flag"
	"log/slog"
	"math/rand"
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
	mu           sync.Mutex
	activeConns  int
}

func (b *BackendServer) IncrementConn() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeConns++
}

func (b *BackendServer) DecrementConn() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeConns--
}

func (b *BackendServer) GetActiveConns() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeConns
}

type BalancingAlgorithm int

const (
	RoundRobin BalancingAlgorithm = iota
	LeastConnections
	Random
)

func (a BalancingAlgorithm) String() string {
	switch a {
	case RoundRobin:
		return "RoundRobin"
	case LeastConnections:
		return "LeastConnections"
	case Random:
		return "Random"
	default:
		return "Unknown"
	}
}

type LoadBalancer struct {
	backends  []*BackendServer
	current   uint64
	mu        sync.Mutex
	log       *slog.Logger
	algorithm BalancingAlgorithm
}

func NewLoadBalancer(backends []string, log *slog.Logger, algorithmstr string) *LoadBalancer {
	var algorithm BalancingAlgorithm
	if algorithmstr == "random" {
		algorithm = Random
	} else if algorithmstr == "leastconnections" {
		algorithm = LeastConnections
	} else {
		algorithm = RoundRobin
	}
	lb := &LoadBalancer{log: log, algorithm: algorithm}
	lb.log.Info("Using load balancer with", "algorithm", algorithmstr)
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

	switch lb.algorithm {
	case LeastConnections:
		return lb.getLeastBusyBackend()
	case Random:
		return lb.getRandomBackend()
	default:
		return lb.getRoundRobinBackend()
	}
}

// least busy algorithm
func (lb *LoadBalancer) getLeastBusyBackend() *BackendServer {
	var leastBusy *BackendServer
	minConns := int(^uint(0) >> 1)

	for _, backend := range lb.backends {
		if !backend.IsAlive {
			continue
		}

		conns := backend.GetActiveConns()
		if conns < minConns {
			leastBusy = backend
			minConns = conns
		}
	}

	if leastBusy == nil {
		lb.log.Error("No healthy backends available")
		return nil
	}

	return leastBusy
}

// random algorithm
func (lb *LoadBalancer) getRandomBackend() *BackendServer {
	var available []*BackendServer
	for _, backend := range lb.backends {
		if backend.IsAlive {
			available = append(available, backend)
		}
	}

	if len(available) == 0 {
		lb.log.Error("No healthy backends available")
		return nil
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return available[rng.Intn(len(available))]
}

// round robin algorithm
func (lb *LoadBalancer) getRoundRobinBackend() *BackendServer {
	start := lb.current
	for {
		backend := lb.backends[lb.current%uint64(len(lb.backends))]
		lb.current++

		if backend.IsAlive {
			return backend
		}

		if lb.current == start {
			break
		}
	}

	lb.log.Error("No healthy backends available")
	return nil
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend := lb.GetNextBackend()
	if backend == nil {
		http.Error(w, "No available backends", http.StatusServiceUnavailable)
		return
	}

	backend.IncrementConn()
	defer backend.DecrementConn()

	lb.log.Info("Forwarding request",
		"url", backend.URL,
		"algorithm", lb.algorithm.String(),
		"active_conns", backend.GetActiveConns(),
	)

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

	lb := NewLoadBalancer(cfg.Backends, log, cfg.Algorithm)

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
