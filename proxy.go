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

func (p *Proxy) rewrite(name string, raw []byte) ([]byte, string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("parse discovery doc: %w", err)
	}

	var upstreamJwksURI string
	if fields["jwks_uri"] != nil {
		if err := json.Unmarshal(fields["jwks_uri"], &upstreamJwksURI); err != nil {
			return nil, "", fmt.Errorf("parse jwks_uri: %w", err)
		}
	}

	external := p.cfg.ExternalURL + "/" + name
	issuer, _ := json.Marshal(external)
	jwksURI, _ := json.Marshal(external + "/jwks")
	fields["issuer"] = issuer
	fields["jwks_uri"] = jwksURI

	body, err := json.Marshal(fields)
	if err != nil {
		return nil, "", fmt.Errorf("serialize discovery doc: %w", err)
	}
	return body, upstreamJwksURI, nil
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

	body, upstreamJwksURI, err := p.rewrite(name, raw)
	if err != nil {
		return cachedDoc{}, http.StatusBadGateway, err
	}

	doc := cachedDoc{
		upstreamJwksURI: upstreamJwksURI,
		body:            body,
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
