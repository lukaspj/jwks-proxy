package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
external_url: https://jwks-proxy.com/
upstream_template: https://auth.example.com/application/o/{route}/
`), 0o644)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("listen default = %q", cfg.Listen)
	}
	if cfg.CacheTTLSecs != 300 {
		t.Errorf("cache ttl default = %d", cfg.CacheTTLSecs)
	}
	if cfg.ExternalURL != "https://jwks-proxy.com" {
		t.Errorf("external_url = %q", cfg.ExternalURL)
	}
	if cfg.UpstreamTemplate != "https://auth.example.com/application/o/{route}" {
		t.Errorf("upstream_template = %q", cfg.UpstreamTemplate)
	}
}

func TestLoadConfigEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
external_url: https://jwks-proxy.com
upstream_template: https://auth.example.com/application/o/{route}
`), 0o644)

	t.Setenv("JWKS_PROXY_LISTEN", ":9999")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9999" {
		t.Errorf("listen = %q, want :9999 from env override", cfg.Listen)
	}
}

func TestValidateUpstreamTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantErr  bool
	}{
		{"missing placeholder", "https://auth.example.com/application/o", true},
		{"relative url", "auth.example.com/application/o/{route}", true},
		{"ftp scheme", "ftp://auth.example.com/{route}", true},
		{"valid", "https://auth.example.com/application/o/{route}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{
				ExternalURL:      "https://jwks-proxy.com",
				UpstreamTemplate: tt.template,
			}
			err := c.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
