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

func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	for name := range p.cfg.Routes {
		base := "/" + name
		mux.HandleFunc(base+"/.well-known/openid-configuration", p.handleDiscovery(name))
		mux.HandleFunc(base+"/jwks", p.handleJWKS(name))
		mux.HandleFunc(base+"/jwks/", p.handleJWKS(name))
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return mux
}

func (p *Proxy) replacements(name string, route Route) []ReplaceRule {
	rules := make([]ReplaceRule, 0, len(route.Replacements)+1)
	rules = append(rules, ReplaceRule{
		Find:    route.Upstream,
		Replace: p.cfg.ExternalURL + "/" + name,
	})
	rules = append(rules, route.Replacements...)
	return rules
}

func applyReplacements(body []byte, rules []ReplaceRule) []byte {
	s := string(body)
	for _, r := range rules {
		if r.Find == "" {
			continue
		}
		s = strings.ReplaceAll(s, r.Find, r.Replace)
	}
	return []byte(s)
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

	route, ok := p.cfg.Routes[name]
	if !ok {
		return cachedDoc{}, http.StatusNotFound, fmt.Errorf("unknown route %q", name)
	}

	docURL := route.Upstream + "/.well-known/openid-configuration"
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
		body:            applyReplacements(raw, p.replacements(name, route)),
		expires:         time.Now().Add(p.cfg.CacheTTL()),
	}

	p.mu.Lock()
	p.cache[name] = doc
	p.mu.Unlock()
	return doc, http.StatusOK, nil
}

func (p *Proxy) handleDiscovery(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
}

func (p *Proxy) handleJWKS(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
}
