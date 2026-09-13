# JWKS Proxy

A Go service that proxies OIDC discovery documents and JWKS endpoints for arbitrary upstream OIDC providers, rewriting URLs using a simple string find-and-replace model.

For each configured route, it serves a rewritten copy of the upstream `.well-known/openid-configuration` document and proxies the upstream JWKS endpoint:

```
GET /<route>/.well-known/openid-configuration
GET /<route>/jwks                # and /<route>/jwks/
```

## Example

With a route `foobar` pointing at an Authentik application, requests to

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

## Configuration (JSON)

```json
{
  "listen": ":8080",
  "external_url": "https://jwks-proxy.com",
  "cache_ttl_seconds": 300,
  "routes": {
    "foobar": {
      "upstream": "https://authentik.central-dev.aldershaab-it.dk/application/o/foobar",
      "replacements": [
        { "find": "https://keys.internal.example.com/jwks.json",
          "replace": "https://jwks-proxy.com/foobar/jwks" }
      ]
    }
  }
}
```

Variable

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `listen` | no | `:8080` | HTTP listen address |
| `external_url` | yes | – | External base URL of the proxy (no trailing slash) |
| `cache_ttl_seconds` | no | `300` | Discovery document cache TTL; also emitted as `Cache-Control: max-age` |
| `routes` | yes | – | Map of route name to route definition |

Route fields:

| Field | Required | Description |
|-------|----------|-------------|
| `upstream` | yes | Upstream OIDC provider base URL for this application (no trailing slash) |
| `replacements` | no | Extra find/replace rules applied to the discovery document body, for upstreams that host JWKS on a different domain |

## Behavior

- The discovery document is fetched from `<upstream>/.well-known/openid-configuration`.
- The upstream `jwks_uri` is extracted from the original body (before rewriting) and used to proxy JWKS.
- Replacements are applied in order: first the implicit rule (`upstream` → `<external_url>/<route>`), then any custom `replacements`. Rules with an empty `find` are skipped.
- Discovery documents are cached in memory for `cache_ttl_seconds`; JWKS is fetched fresh on each request.
- Unknown routes return 404; upstream failures return 502 with the error message.
- Responses are capped at 4 MiB; upstream requests time out after 15s.

## Development

```
go build ./...
go test ./...
```

Run locally:

```
go run . -config config.json
```

or via `JWKS_PROXY_CONFIG`:

```
JWKS_PROXY_CONFIG=config.json go run .
```

## Kubernetes

The container image is built and pushed to GHCR by [`.github/workflows/build-push.yaml`](.github/workflows/build-push.yaml).

A Helm chart lives in [`deploy/jwks-proxy`](deploy/jwks-proxy):

```
helm install jwks-proxy ./deploy/jwks-proxy \
  --set config.externalUrl=https://jwks-proxy.com \
  --set config.routes.foobar.upstream=https://authentik.example.com/application/o/foobar
```

The chart renders `config.json` into a ConfigMap and mounts it; alternatively pass `existingConfigMap` pointing at a ConfigMap that already contains a `config.json` key.

Docker run equivalent:

```
docker run -v ./config.json:/config.json:ro -p 8080:8080 ghcr.io/lukaspj/jwks-proxy -config /config.json
```

CI (vet + tests, including race detector) runs in [`.github/workflows/ci.yaml`](.github/workflows/ci.yaml).
