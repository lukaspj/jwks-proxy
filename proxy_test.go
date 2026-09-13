package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func fakeResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    nil,
	}
}

func notFoundResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("404 not found")),
		Header:     make(http.Header),
	}
}

func fakeUpstream(h func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripperFunc(h)}
}

func callProxy(p *Proxy, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, req)
	return rec
}

func testConfig(external, authHost string) *Config {
	return &Config{
		Listen:       ":0",
		ExternalURL:  external,
		CacheTTLSecs: 1,
		Routes: map[string]Route{
			"foobar": {
				Upstream: authHost + "/application/o/foobar",
			},
		},
	}
}

func TestDiscoveryRewritesIssuerAndJwksURI(t *testing.T) {
	upstream := "http://auth.example.com"
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/application/o/foobar/.well-known/openid-configuration":
			return fakeResponse(fmt.Sprintf(`{"issuer": "%s/application/o/foobar", "jwks_uri": "%s/application/o/foobar/jwks/", "token_endpoint": "%s/application/o/foobar/token/"}`, upstream, upstream, upstream)), nil
		case "/application/o/foobar/jwks/":
			return fakeResponse(`{"keys": [{"kty": "RSA", "kid": "test-key"}]}`), nil
		default:
			return notFoundResponse(), nil
		}
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", upstream), client)
	rec := callProxy(p, "/foobar/.well-known/openid-configuration")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if got, want := doc["issuer"], "https://jwks-proxy.com/foobar"; got != want {
		t.Errorf("issuer = %q, want %q", got, want)
	}
	if got, want := doc["jwks_uri"], "https://jwks-proxy.com/foobar/jwks/"; got != want {
		t.Errorf("jwks_uri = %q, want %q", got, want)
	}
	if got, want := doc["token_endpoint"], "https://jwks-proxy.com/foobar/token/"; got != want {
		t.Errorf("token_endpoint = %q, want %q", got, want)
	}
}

func TestJWKSProxied(t *testing.T) {
	upstream := "http://auth.example.com"
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/application/o/foobar/.well-known/openid-configuration":
			return fakeResponse(fmt.Sprintf(`{"issuer": "%s/application/o/foobar", "jwks_uri": "%s/application/o/foobar/jwks/"}`, upstream, upstream)), nil
		case "/application/o/foobar/jwks/":
			return fakeResponse(`{"keys": [{"kty": "RSA", "kid": "test-key"}]}`), nil
		default:
			return notFoundResponse(), nil
		}
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", upstream), client)
	rec := callProxy(p, "/foobar/jwks")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "test-key") {
		t.Errorf("jwks body missing key: %q", rec.Body.String())
	}
}

func TestUnknownRoute404(t *testing.T) {
	p := NewProxy(testConfig("https://jwks-proxy.com", "http://127.0.0.1:8123"))
	rec := callProxy(p, "/nosuch/.well-known/openid-configuration")
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCustomReplacements(t *testing.T) {
	upstream := "http://auth.example.com"
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oidc/.well-known/openid-configuration" {
			return fakeResponse(`{"jwks_uri": "https://keys.internal.example.com/jwks.json", "issuer": "https://auth.example.com/oidc"}`), nil
		}
		return notFoundResponse(), nil
	})

	cfg := testConfig("https://jwks-proxy.com", upstream)
	route := cfg.Routes["foobar"]
	route.Upstream = upstream + "/oidc"
	route.Replacements = []ReplaceRule{
		{Find: "https://keys.internal.example.com/jwks.json", Replace: "https://jwks-proxy.com/foobar/jwks"},
	}
	cfg.Routes["foobar"] = route

	rec := callProxy(NewProxyWithClient(cfg, client), "/foobar/.well-known/openid-configuration")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	s := rec.Body.String()
	if strings.Contains(s, "keys.internal.example.com") {
		t.Errorf("internal host leaked: %s", s)
	}
	if !strings.Contains(s, "https://jwks-proxy.com/foobar/jwks") {
		t.Errorf("replacement missing: %s", s)
	}
}
