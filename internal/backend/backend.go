package backend

import (
	"net/http/httputil"
	"net/url"
	"sync"
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
