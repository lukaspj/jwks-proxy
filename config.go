package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Listen       string          `json:"listen"`
	ExternalURL  string          `json:"external_url"`
	CacheTTLSecs int             `json:"cache_ttl_seconds"`
	Routes       map[string]Route `json:"routes"`
}

type Route struct {
	Upstream     string       `json:"upstream"`
	Replacements []ReplaceRule `json:"replacements,omitempty"`
}

type ReplaceRule struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.ExternalURL == "" {
		return fmt.Errorf("external_url is required")
	}
	c.ExternalURL = strings.TrimRight(c.ExternalURL, "/")
	if len(c.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}
	for name, r := range c.Routes {
		if strings.ContainsAny(name, "/?#") || name == "" {
			return fmt.Errorf("invalid route name %q", name)
		}
		if r.Upstream == "" {
			return fmt.Errorf("route %q: upstream is required", name)
		}
		r.Upstream = strings.TrimRight(r.Upstream, "/")
		c.Routes[name] = r
	}
	if c.CacheTTLSecs <= 0 {
		c.CacheTTLSecs = 300
	}
	return nil
}

func (c *Config) CacheTTL() time.Duration {
	return time.Duration(c.CacheTTLSecs) * time.Second
}
