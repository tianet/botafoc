package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Route struct {
	Subdomain string
	Target    int
	Healthy   bool
}

type LogEntry struct {
	Time      time.Time
	Subdomain string
	Method    string
	Path      string
	Status    int
	Duration  time.Duration
}

type Proxy struct {
	baseDomain string
	listenPort int

	mu     sync.RWMutex
	routes map[string]*Route

	logMu sync.RWMutex
	logs  []LogEntry

	server *http.Server
}

func New(baseDomain string, listenPort int) *Proxy {
	p := &Proxy{
		baseDomain: baseDomain,
		listenPort: listenPort,
		routes:     make(map[string]*Route),
	}
	p.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", listenPort),
		Handler: p,
	}
	return p
}

func (p *Proxy) AddRoute(subdomain string, target int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[subdomain] = &Route{
		Subdomain: subdomain,
		Target:    target,
		Healthy:   false,
	}
}

func (p *Proxy) RemoveRoute(subdomain string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, subdomain)
}

func (p *Proxy) Routes() []Route {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]Route, 0, len(p.routes))
	for _, r := range p.routes {
		result = append(result, *r)
	}
	return result
}

// matchRoute finds the best matching route by walking up the subdomain hierarchy.
// For "x.y.app", it tries "x.y.app", then "y.app", then "app".
// Caller must hold p.mu (at least RLock).
func (p *Proxy) matchRoute(subdomain string) (*Route, bool) {
	for sub := subdomain; sub != ""; {
		if route, ok := p.routes[sub]; ok {
			return route, true
		}
		dot := strings.Index(sub, ".")
		if dot == -1 {
			break
		}
		sub = sub[dot+1:]
	}
	return nil, false
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	if !strings.HasSuffix(host, p.baseDomain) {
		http.Error(w, "unknown host", http.StatusBadGateway)
		return
	}

	subdomain := strings.TrimSuffix(host, p.baseDomain)
	if subdomain == "" {
		http.Error(w, "no subdomain", http.StatusBadGateway)
		return
	}

	p.mu.RLock()
	route, ok := p.matchRoute(subdomain)
	p.mu.RUnlock()

	if !ok {
		http.Error(w, fmt.Sprintf("no route for %q", subdomain), http.StatusBadGateway)
		return
	}

	target, _ := url.Parse(fmt.Sprintf("http://localhost:%d", route.Target))
	rp := httputil.NewSingleHostReverseProxy(target)
	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	rp.ServeHTTP(sw, r)

	p.logMu.Lock()
	p.logs = append(p.logs, LogEntry{
		Time:      start,
		Subdomain: subdomain,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    sw.status,
		Duration:  time.Since(start),
	})
	// Cap at 1000 entries
	if len(p.logs) > 1000 {
		p.logs = p.logs[len(p.logs)-1000:]
	}
	p.logMu.Unlock()
}

func (p *Proxy) Logs(subdomain string) []LogEntry {
	p.logMu.RLock()
	defer p.logMu.RUnlock()
	if subdomain == "" {
		result := make([]LogEntry, len(p.logs))
		copy(result, p.logs)
		return result
	}
	var result []LogEntry
	for _, l := range p.logs {
		if l.Subdomain == subdomain {
			result = append(result, l)
		}
	}
	return result
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (p *Proxy) Start() error {
	go p.healthCheckLoop()
	if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (p *Proxy) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.server.Shutdown(ctx)
}

func (p *Proxy) healthCheckLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		p.checkHealth()
	}
}

func (p *Proxy) checkHealth() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.routes {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", r.Target), 500*time.Millisecond)
		if err != nil {
			r.Healthy = false
			continue
		}
		conn.Close()
		r.Healthy = true
	}
}
