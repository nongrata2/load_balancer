package balancer

import (
	"cloudru/internal/backend"
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

type LoadBalancer struct {
	backends  []*backend.BackendServer
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

			lb.log.Error("Backend request failed",
				"url", parsedUrl,
				"error", err,
			)

			if nextBackend := lb.GetNextBackend(); nextBackend != nil {
				lb.log.Info("Retrying request with next backend", "url", nextBackend.URL)
				nextBackend.ReverseProxy.ServeHTTP(w, r)
				return
			}

			lb.log.Error("All backends unavailable")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("All backends are down\n"))
		}

		lb.backends = append(lb.backends, &backend.BackendServer{
			URL:          parsedUrl,
			ReverseProxy: proxy,
			IsAlive:      true,
		})
	}
	return lb
}

func (lb *LoadBalancer) GetNextBackend() *backend.BackendServer {
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
func (lb *LoadBalancer) getLeastBusyBackend() *backend.BackendServer {
	var leastBusy *backend.BackendServer
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
func (lb *LoadBalancer) getRandomBackend() *backend.BackendServer {
	var available []*backend.BackendServer
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
func (lb *LoadBalancer) getRoundRobinBackend() *backend.BackendServer {
	if len(lb.backends) == 0 {
		lb.log.Error("No backends configured")
		return nil
	}

	start := lb.current
	for {
		idx := lb.current % uint64(len(lb.backends))
		backend := lb.backends[idx]

		if backend.IsAlive {
			lb.current++
			return backend
		}

		lb.current++
		if lb.current-start >= uint64(len(lb.backends)) {
			break
		}
	}

	lb.log.Error("No healthy backends available")
	return nil
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend := lb.GetNextBackend()
	if backend == nil {
		lb.log.Error("No available backends")
		http.Error(w, "All backends are down", http.StatusServiceUnavailable)
		return
	}

	backend.IncrementConn()
	defer backend.DecrementConn()

	lb.log.Info("Forwarding request",
		"url", backend.URL,
		"algorithm", lb.algorithm.String(),
		"active_conns", backend.GetActiveConns(),
	)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	backend.ReverseProxy.ServeHTTP(w, r.WithContext(ctx))
}

func (lb *LoadBalancer) CheckBackendHealth(backend *backend.BackendServer) bool {
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
