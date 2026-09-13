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

func testConfig(external, template string) *Config {
	return &Config{
		Listen:           ":0",
		ExternalURL:      external,
		UpstreamTemplate: template,
		CacheTTLSecs:     1,
	}
}

const testTemplate = "http://auth.example.com/application/o/{route}"

func TestDiscoveryRewritesIssuerAndJwksURI(t *testing.T) {
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/application/o/foobar/.well-known/openid-configuration":
			return fakeResponse(fmt.Sprintf(`{"issuer": "http://auth.example.com/application/o/foobar", "jwks_uri": "http://auth.example.com/application/o/foobar/jwks/", "token_endpoint": "http://auth.example.com/application/o/foobar/token/"}`)), nil
		case "/application/o/foobar/jwks/":
			return fakeResponse(`{"keys": [{"kty": "RSA", "kid": "test-key"}]}`), nil
		default:
			return notFoundResponse(), nil
		}
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", testTemplate), client)
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
	if got, want := doc["jwks_uri"], "https://jwks-proxy.com/foobar/jwks"; got != want {
		t.Errorf("jwks_uri = %q, want %q", got, want)
	}
	if got, want := doc["token_endpoint"], "http://auth.example.com/application/o/foobar/token/"; got != want {
		t.Errorf("token_endpoint = %q, want %q (should be untouched)", got, want)
	}
}

func TestJWKSProxied(t *testing.T) {
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/application/o/foobar/.well-known/openid-configuration":
			return fakeResponse(fmt.Sprintf(`{"issuer": "http://auth.example.com/application/o/foobar", "jwks_uri": "http://auth.example.com/application/o/foobar/jwks/"}`)), nil
		case "/application/o/foobar/jwks/":
			return fakeResponse(`{"keys": [{"kty": "RSA", "kid": "test-key"}]}`), nil
		default:
			return notFoundResponse(), nil
		}
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", testTemplate), client)
	rec := callProxy(p, "/foobar/jwks")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "test-key") {
		t.Errorf("jwks body missing key: %q", rec.Body.String())
	}
}

func TestDynamicRouteNoConfig(t *testing.T) {
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/application/o/bazapp/.well-known/openid-configuration":
			return fakeResponse(fmt.Sprintf(`{"issuer": "http://auth.example.com/application/o/bazapp", "jwks_uri": "http://auth.example.com/application/o/bazapp/jwks"}`)), nil
		case "/application/o/bazapp/jwks":
			return fakeResponse(`{"keys": [{"kty": "EC", "kid": "baz"}]}`), nil
		default:
			return notFoundResponse(), nil
		}
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", testTemplate), client)
	rec := callProxy(p, "/bazapp/jwks")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "baz") {
		t.Errorf("jwks body missing key: %q", rec.Body.String())
	}
}

func TestUpstream404Propagates(t *testing.T) {
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		return notFoundResponse(), nil
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", testTemplate), client)
	rec := callProxy(p, "/nosuchapp/.well-known/openid-configuration")
	if rec.Code != 502 {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestFieldAwareRewrite(t *testing.T) {
	client := fakeUpstream(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/application/o/foobar/.well-known/openid-configuration" {
			return fakeResponse(fmt.Sprintf(`{"issuer": "http://auth.example.com/application/o/foobar", "jwks_uri": "http://auth.example.com/application/o/foobar/jwks", "authorization_endpoint": "http://auth.example.com/application/o/foobar/auth", "end_session_endpoint": "http://auth.example.com/application/o/foobar/end-session"}`)), nil
		}
		return notFoundResponse(), nil
	})

	p := NewProxyWithClient(testConfig("https://jwks-proxy.com", testTemplate), client)
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
	if got, want := doc["jwks_uri"], "https://jwks-proxy.com/foobar/jwks"; got != want {
		t.Errorf("jwks_uri = %q, want %q", got, want)
	}
	if got, want := doc["authorization_endpoint"], "http://auth.example.com/application/o/foobar/auth"; got != want {
		t.Errorf("authorization_endpoint = %q, want %q (should be untouched)", got, want)
	}
	if got, want := doc["end_session_endpoint"], "http://auth.example.com/application/o/foobar/end-session"; got != want {
		t.Errorf("end_session_endpoint = %q, want %q (should be untouched)", got, want)
	}
}
