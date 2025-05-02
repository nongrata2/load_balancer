package main

import (
	// "fmt"
	"cloudru/internal/config"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
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
}

func NewLoadBalancer(backends []string) *LoadBalancer {

	lb := &LoadBalancer{}
	for _, backendUrl := range backends {
		parsedUrl, err := url.Parse(backendUrl)
		if err != nil {
			log.Fatal("Failed to parse backend URL:", err)
		}
		proxy := httputil.NewSingleHostReverseProxy(parsedUrl)
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

	log.Printf("Forwarding request to %s", backend.URL)
	backend.ReverseProxy.ServeHTTP(w, r)
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "configuration file")
	flag.Parse()
	cfg := config.MustLoad(configPath)

	lb := NewLoadBalancer(cfg.Backends)

	log.Println("Load balancer started on port", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, lb); err != nil {
		log.Fatal("Failed to start load balancer:", err)
	}
}
