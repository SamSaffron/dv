package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type route struct {
	Host   string `json:"host"`
	Target string `json:"target"`
}

type proxyTable struct {
	mu      sync.RWMutex
	routes  map[string]*url.URL
	proxies map[string]*httputil.ReverseProxy
}

func newProxyTable() *proxyTable {
	return &proxyTable{
		routes:  map[string]*url.URL{},
		proxies: map[string]*httputil.ReverseProxy{},
	}
}

func (p *proxyTable) set(host string, target *url.URL) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[host] = target
	p.proxies[host] = buildReverseProxy(host, target)
}

func (p *proxyTable) delete(host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.routes[host]; !ok {
		return false
	}
	delete(p.routes, host)
	delete(p.proxies, host)
	return true
}

func (p *proxyTable) list() []route {
	p.mu.RLock()
	defer p.mu.RUnlock()
	hosts := make([]string, 0, len(p.routes))
	for h := range p.routes {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	out := make([]route, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, route{
			Host:   h,
			Target: p.routes[h].String(),
		})
	}
	return out
}

func (p *proxyTable) lookup(host string) (*url.URL, *httputil.ReverseProxy) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routes[host], p.proxies[host]
}

func (p *proxyTable) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := normalizeHost(r.Host)
	if host == "" {
		http.Error(w, "missing host", http.StatusBadGateway)
		return
	}
	target, proxy := p.lookup(host)
	if target == nil || proxy == nil {
		http.Error(w, "no route for host", http.StatusBadGateway)
		return
	}
	proxy.ServeHTTP(w, r)
}

func main() {
	httpAddr := envOrDefault("PROXY_HTTP_ADDR", ":80")
	apiAddr := envOrDefault("PROXY_API_ADDR", ":2080")
	table := newProxyTable()

	go func() {
		log.Printf("local-proxy admin listening on %s", apiAddr)
		admin := &http.Server{
			Addr:              apiAddr,
			Handler:           apiRouter(table),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		if err := admin.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("admin server error: %v", err)
		}
	}()

	log.Printf("local-proxy listening on %s", httpAddr)
	server := &http.Server{
		Addr:              httpAddr,
		Handler:           table,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("proxy server error: %v", err)
	}
}

func apiRouter(table *proxyTable) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/api/routes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(table.list()); err != nil {
				http.Error(w, "failed to encode routes", http.StatusInternalServerError)
			}
		case http.MethodPost:
			var payload route
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			host := normalizeHost(payload.Host)
			if host == "" {
				http.Error(w, "host must end with .localhost", http.StatusBadRequest)
				return
			}
			target, err := parseTarget(payload.Target)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			table.set(host, target)
			log.Printf("registered route %s -> %s", host, target)
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/routes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		host := normalizeHost(strings.TrimPrefix(r.URL.Path, "/api/routes/"))
		if host == "" {
			http.Error(w, "host must end with .localhost", http.StatusBadRequest)
			return
		}
		if !table.delete(host) {
			http.NotFound(w, r)
			return
		}
		log.Printf("removed route %s", host)
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func envOrDefault(key string, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return ""
	}
	if strings.ContainsAny(h, "/\\@") {
		return ""
	}
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		parsed, err := url.Parse(h)
		if err != nil {
			return ""
		}
		h = parsed.Host
	}
	if strings.Contains(h, ":") {
		hostOnly, _, err := net.SplitHostPort(h)
		if err == nil {
			h = hostOnly
		}
	}
	if !strings.HasSuffix(h, ".localhost") {
		return ""
	}
	return h
}

func parseTarget(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("target required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid target: %v", err)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("only http targets are supported right now")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("target host is required")
	}
	return u, nil
}

func buildReverseProxy(host string, target *url.URL) *httputil.ReverseProxy {
	targetQuery := target.RawQuery
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}
		req.Host = host
		req.Header.Set("X-Forwarded-Host", host)
		req.Header.Set("X-Forwarded-Proto", "http")
		req.Header.Set("X-Forwarded-Port", "80")
		if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil && ip != "" {
			appendForwardedFor(req, ip)
		}
	}
	proxy := &httputil.ReverseProxy{
		Director:      director,
		FlushInterval: 50 * time.Millisecond,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	return proxy
}

func appendForwardedFor(req *http.Request, ip string) {
	const header = "X-Forwarded-For"
	if prior := req.Header.Get(header); prior != "" {
		req.Header.Set(header, prior+", "+ip)
	} else {
		req.Header.Set(header, ip)
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
