package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type cachedDoc struct {
	upstreamJwksURI string
	body            []byte
	expires         time.Time
}

type Proxy struct {
	cfg    *Config
	client *http.Client

	mu    sync.Mutex
	cache map[string]cachedDoc
}

func NewProxy(cfg *Config) *Proxy {
	return NewProxyWithClient(cfg, &http.Client{Timeout: 15 * time.Second})
}

func NewProxyWithClient(cfg *Config, client *http.Client) *Proxy {
	return &Proxy{
		cfg:    cfg,
		client: client,
		cache:  make(map[string]cachedDoc),
	}
}

func validRouteName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/?#")
}

func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{name}/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/{name}/jwks", p.handleJWKS)
	mux.HandleFunc("/{name}/jwks/", p.handleJWKS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return mux
}

func (p *Proxy) rewrite(name string, body []byte) []byte {
	return []byte(strings.ReplaceAll(string(body), p.cfg.UpstreamBase(name), p.cfg.ExternalURL+"/"+name))
}

func (p *Proxy) fetchUpstream(url string) ([]byte, int, error) {
	resp, err := p.client.Get(url)
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, http.StatusBadGateway, fmt.Errorf("fetch %s: upstream returned %d", url, resp.StatusCode)
	}
	return body, http.StatusOK, nil
}

func (p *Proxy) getDiscoveryDoc(name string) (cachedDoc, int, error) {
	p.mu.Lock()
	cached, ok := p.cache[name]
	p.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached, http.StatusOK, nil
	}

	docURL := p.cfg.UpstreamBase(name) + "/.well-known/openid-configuration"
	raw, status, err := p.fetchUpstream(docURL)
	if err != nil {
		return cachedDoc{}, status, err
	}

	var discovery struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(raw, &discovery); err != nil {
		return cachedDoc{}, http.StatusBadGateway, fmt.Errorf("parse discovery doc from %s: %w", docURL, err)
	}

	doc := cachedDoc{
		upstreamJwksURI: discovery.JWKSURI,
		body:            p.rewrite(name, raw),
		expires:         time.Now().Add(p.cfg.CacheTTL()),
	}

	p.mu.Lock()
	p.cache[name] = doc
	p.mu.Unlock()
	return doc, http.StatusOK, nil
}

func (p *Proxy) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validRouteName(name) {
		http.NotFound(w, r)
		return
	}
	doc, status, err := p.getDiscoveryDoc(name)
	if err != nil {
		log.Printf("discovery %s: %v", name, err)
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", p.cfg.CacheTTLSecs))
	w.WriteHeader(http.StatusOK)
	w.Write(doc.body)
}

func (p *Proxy) handleJWKS(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validRouteName(name) {
		http.NotFound(w, r)
		return
	}
	doc, status, err := p.getDiscoveryDoc(name)
	if err != nil {
		log.Printf("jwks %s: %v", name, err)
		http.Error(w, err.Error(), status)
		return
	}
	if doc.upstreamJwksURI == "" {
		http.Error(w, "upstream discovery doc has no jwks_uri", http.StatusBadGateway)
		return
	}
	body, status, err := p.fetchUpstream(doc.upstreamJwksURI)
	if err != nil {
		log.Printf("jwks %s: %v", name, err)
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", p.cfg.CacheTTLSecs))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}
