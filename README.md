# JWKS Proxy

A Go service that proxies OIDC discovery documents and JWKS endpoints for arbitrary upstream OIDC providers, rewriting URLs using a simple string find-and-replace model.

Routes are dynamic: any first path segment maps to an upstream via a single `upstream_template` with a `{route}` placeholder. For a given route, it serves a rewritten copy of the upstream `.well-known/openid-configuration` document and proxies the upstream JWKS endpoint:

```
GET /<route>/.well-known/openid-configuration
GET /<route>/jwks                # and /<route>/jwks/
```

## Example

With `upstream_template: https://authentik.central-dev.aldershaab-it.dk/application/o/{route}`, requests to

```
https://jwks-proxy.com/foobar/.well-known/openid-configuration
```

return a copy of

```
https://authentik.central-dev.aldershaab-it.dk/application/o/foobar/.well-known/openid-configuration
```

with every occurrence of the upstream base URL (`https://authentik.central-dev.aldershaab-it.dk/application/o/foobar`) replaced by `https://jwks-proxy.com/foobar`. The issuer, token endpoints and `jwks_uri` all end up pointing at the proxy, and `jwks_uri` resolves to

```
https://jwks-proxy.com/foobar/jwks/
```

which is proxied from the upstream JWKS endpoint.

## Configuration (YAML)

Loaded with [go-fang](https://github.com/lukaspj/go-fang) (YAML file + env overrides + defaults):

```yaml
listen: ":8080"
external_url: https://jwks-proxy.com
upstream_template: https://authentik.central-dev.aldershaab-it.dk/application/o/{route}
cache_ttl_seconds: 300
```

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `listen` | no | `:8080` | HTTP listen address |
| `external_url` | yes | – | External base URL of the proxy (no trailing slash) |
| `upstream_template` | yes | – | Upstream URL template; `{route}` is replaced with the first path segment of the incoming request (must appear exactly once, http/https, no trailing slash) |
| `cache_ttl_seconds` | no | `300` | Discovery document cache TTL; also emitted as `Cache-Control: max-age` |

Scalar settings can be overridden with `JWKS_PROXY_`-prefixed environment variables (`JWKS_PROXY_LISTEN`, `JWKS_PROXY_EXTERNAL_URL`, `JWKS_PROXY_UPSTREAM_TEMPLATE`, `JWKS_PROXY_CACHE_TTL_SECONDS`).

## Behavior

- The route name is the first path segment of the incoming request (e.g. `/foobar/jwks` → `foobar`).
- The discovery document is fetched from `<upstream_template with {route} replaced>/.well-known/openid-configuration`.
- The upstream `jwks_uri` is extracted from the original body (before rewriting) and used to proxy JWKS.
- Rewriting replaces every occurrence of the upstream base URL with `<external_url>/<route>`.
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
