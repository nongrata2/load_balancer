package balancer

import (
	"cloudru/internal/backend"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

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

type Algorithm interface {
	GetNextBackend(backends []*backend.BackendServer, log *slog.Logger) *backend.BackendServer
}

type RoundRobinAlgo struct {
	current uint64
	mu      sync.Mutex
}

func (a *RoundRobinAlgo) GetNextBackend(backends []*backend.BackendServer, log *slog.Logger) *backend.BackendServer {
	a.mu.Lock()
	defer a.mu.Unlock()

	start := a.current
	for {
		backend := backends[a.current%uint64(len(backends))]
		a.current++

		if backend.IsAlive {
			return backend
		}

		if a.current == start {
			break
		}
	}

	log.Error("No healthy backends available")
	return nil
}

type LeastConnectionsAlgo struct{}

func (a *LeastConnectionsAlgo) GetNextBackend(backends []*backend.BackendServer, log *slog.Logger) *backend.BackendServer {
	var leastBusy *backend.BackendServer
	minConns := int(^uint(0) >> 1)

	for _, backend := range backends {
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
		log.Error("No healthy backends available")
		return nil
	}

	return leastBusy
}

type RandomAlgo struct {
	rng *rand.Rand
	mu  sync.Mutex
}

func NewRandomAlgo() *RandomAlgo {
	return &RandomAlgo{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (a *RandomAlgo) GetNextBackend(backends []*backend.BackendServer, log *slog.Logger) *backend.BackendServer {
	var available []*backend.BackendServer
	for _, backend := range backends {
		if backend.IsAlive {
			available = append(available, backend)
		}
	}

	if len(available) == 0 {
		log.Error("No healthy backends available")
		return nil
	}

	a.mu.Lock()
	selected := available[a.rng.Intn(len(available))]
	a.mu.Unlock()

	return selected
}
