package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaspj/go-fang"
)

const routePlaceholder = "{route}"

type Config struct {
	Listen           string `yaml:"listen" fang:"listen"`
	ExternalURL      string `yaml:"external_url" fang:"external_url"`
	UpstreamTemplate string `yaml:"upstream_template" fang:"upstream_template"`
	CacheTTLSecs     int    `yaml:"cache_ttl_seconds" fang:"cache_ttl_seconds"`
}

func LoadConfig(path string) (*Config, error) {
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	cfg, err := fang.New[Config]().
		WithDefault(Config{
			Listen:       ":8080",
			CacheTTLSecs: 300,
		}).
		WithAutomaticEnv("JWKS_PROXY").
		WithConfigFile(fang.ConfigFileOptions{
			Type:  fang.ConfigFileTypeYaml,
			Paths: []string{dir},
			Names: []string{base},
		}).
		Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
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
	if c.UpstreamTemplate == "" {
		return fmt.Errorf("upstream_template is required")
	}
	c.UpstreamTemplate = strings.TrimRight(c.UpstreamTemplate, "/")
	if !strings.Contains(c.UpstreamTemplate, routePlaceholder) {
		return fmt.Errorf("upstream_template must contain %s", routePlaceholder)
	}
	if strings.Count(c.UpstreamTemplate, routePlaceholder) > 1 {
		return fmt.Errorf("upstream_template must contain %s at most once", routePlaceholder)
	}
	u, err := url.Parse(strings.ReplaceAll(c.UpstreamTemplate, routePlaceholder, "route"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstream_template %q is not a valid absolute URL", c.UpstreamTemplate)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream_template must use http or https")
	}
	if c.CacheTTLSecs <= 0 {
		c.CacheTTLSecs = 300
	}
	return nil
}

// UpstreamBase returns the upstream URL prefix for the given route name, e.g.
// https://auth.example.com/application/o/foobar for template
// https://auth.example.com/application/o/{route} and route "foobar".
func (c *Config) UpstreamBase(route string) string {
	return strings.ReplaceAll(c.UpstreamTemplate, routePlaceholder, route)
}

func (c *Config) CacheTTL() time.Duration {
	return time.Duration(c.CacheTTLSecs) * time.Second
}
