# JWKS Proxy

A Go service that proxies OIDC discovery documents and JWKS endpoints for arbitrary upstream OIDC providers, rewriting URLs field-aware: only `issuer` and `jwks_uri` in the discovery document are rewritten; everything else passes through untouched.

Routes are dynamic: any first path segment maps to an upstream via a single `upstream_template` with a `{route}` placeholder. For a given route, it serves a rewritten copy of the upstream `.well-known/openid-configuration` document and proxies the upstream JWKS endpoint:

```
GET /<route>/.well-known/openid-configuration
GET /<route>/jwks                # and /<route>/jwks/
```

## Example

With `upstream_template: https://idp.example.com/application/o/{route}`, requests to

```
https://jwks-proxy.com/foobar/.well-known/openid-configuration
```

return a copy of

```
https://idp.example.com/application/o/foobar/.well-known/openid-configuration
```

with every occurrence of the upstream base URL (`https://idp.example.com/application/o/foobar`) replaced by `https://jwks-proxy.com/foobar`. The issuer, token endpoints and `jwks_uri` all end up pointing at the proxy, and `jwks_uri` resolves to

```
https://jwks-proxy.com/foobar/jwks/
```

which is proxied from the upstream JWKS endpoint.

## Configuration (YAML)

Loaded with [go-fang](https://github.com/lukaspj/go-fang) (YAML file + env overrides + defaults):

```yaml
listen: ":8080"
external_url: https://jwks-proxy.com
upstream_template: https://idp.example.com/application/o/{route}
cache_ttl_seconds: 300
```

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `listen` | no | `:8080` | HTTP listen address |
| `external_url` | yes | – | External base URL of the proxy (no trailing slash) |
| `upstream_template` | yes | – | Upstream URL template; `{route}` is replaced with the first path segment of the incoming request (must appear exactly once, http/https, no trailing slash) |
| `cache_ttl_seconds` | no | `300` | Discovery document cache TTL; also emitted as `Cache-Control: max-age` |

Scalar settings can be overridden with `JWKS_PROXY_`-prefixed environment variables (`JWKS_PROXY_LISTEN`, `JWKS_PROXY_EXTERNAL_URL`, `JWKS_PROXY_UPSTREAM_TEMPLATE`, `JWKS_PROXY_CACHE_TTL_SECONDS`).

## What is rewritten (and why)

Rewriting is field-aware: the discovery document is parsed as JSON and exactly two fields are rewritten — `issuer` and `jwks_uri`. Every other field (`authorization_endpoint`, `token_endpoint`, `end_session_endpoint`, …) is passed through untouched, whatever host it points at.

Given this upstream document:

```json
{
  "issuer": "https://jwks-proxy.com/runtime-identity/",
  "authorization_endpoint": "https://idp.example.com/application/o/authorize/",
  "token_endpoint": "https://idp.example.com/application/o/token/",
  "end_session_endpoint": "https://jwks-proxy.com/runtime-identity/end-session/",
  "jwks_uri": "https://jwks-proxy.com/runtime-identity/jwks/"
}
```

with `external_url: https://jwks-proxy.com`, the served copy becomes:

```json
{
  "issuer": "https://jwks-proxy.com/runtime-identity",
  "authorization_endpoint": "https://idp.example.com/application/o/authorize/",
  "token_endpoint": "https://idp.example.com/application/o/token/",
  "end_session_endpoint": "https://jwks-proxy.com/runtime-identity/end-session/",
  "jwks_uri": "https://jwks-proxy.com/runtime-identity/jwks"
}
```

Why this split:

- **`issuer`** is rewritten because OIDC clients validate that the document was served from the issuer URL; the issuer must therefore match the proxy's external URL for the proxy to be transparent.
- **`jwks_uri`** is rewritten (and its target proxied) so clients fetch signing keys from the proxy instead of the internal issuer host — this is the core purpose of the service. The proxy extracts the upstream `jwks_uri` before rewriting and serves it at `/<route>/jwks`.
- **`end_session_endpoint`** is *not* rewritten: it is assumed to be directly reachable by clients. If your clients can only reach the proxy host (and not the issuer host directly), RP-initiated logout will break — in that case the issuer host should be made reachable, or you should front it separately.
- **`authorization_endpoint`, `token_endpoint`, etc.** are likewise untouched: they are assumed to be directly reachable by clients. The proxy is not a general reverse proxy for them.

## Behavior

- The route name is the first path segment of the incoming request (e.g. `/foobar/jwks` → `foobar`).
- The discovery document is fetched from `<upstream_template with {route} replaced>/.well-known/openid-configuration`.
- The upstream `jwks_uri` is extracted from the original body (before rewriting) and used to proxy JWKS.
- Rewriting is field-aware: only `issuer` and `jwks_uri` are rewritten; all other fields pass through untouched.
- Discovery documents are cached in memory per route for `cache_ttl_seconds`; JWKS is fetched fresh on each request.
- Unknown upstreams (404 from the provider) result in 502; upstream failures return 502 with the error message.
- Responses are capped at 4 MiB; upstream requests time out after 15s.
- Since any route segment is accepted, the proxy will relay requests for any application hosted on the upstream template's provider — do not expose it to parties who should not reach that provider.

## Development

```
go build ./...
go test ./...
```

Run locally:

```
cp config.example.yaml config.yaml
go run . -config config.yaml
```

or via `JWKS_PROXY_CONFIG`:

```
JWKS_PROXY_CONFIG=config.yaml go run .
```

## Kubernetes

The container image is built and pushed to GHCR by [`.github/workflows/build-push.yaml`](.github/workflows/build-push.yaml).

A Helm chart lives in [`deploy/jwks-proxy`](deploy/jwks-proxy):

```
helm install jwks-proxy ./deploy/jwks-proxy \
  --set config.externalUrl=https://jwks-proxy.com \
  --set config.upstreamTemplate=https://authentik.example.com/application/o/{route}
```

The chart renders `config.yaml` into a ConfigMap and mounts it; alternatively pass `existingConfigMap` pointing at a ConfigMap that already contains a `config.yaml` key.

Docker run equivalent:

```
docker run -v ./config.yaml:/config/config.yaml:ro -p 8080:8080 ghcr.io/lukaspj/jwks-proxy -config /config/config.yaml
```

CI (vet + tests, including race detector) runs in [`.github/workflows/ci.yaml`](.github/workflows/ci.yaml).
