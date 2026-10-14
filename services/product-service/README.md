# product-service

The product catalog. Seeds 12 demo products on first start (`seed.go`, idempotent). Read-heavy and stateless apart from its database — an ideal autoscaling candidate. Single third-party dependency (the `lib/pq` Postgres driver); metrics are exposed by a small built-in Prometheus writer.

| | |
|---|---|
| Language | Go 1.26 (net/http, no framework) |
| Owns | PostgreSQL database `products` |
| Key files | `main.go`, `seed.go`, `internal/platform/` |

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /products?category=&search=&limit=` | list |
| `GET /products/batch?ids=1,2,3` | several at once (cart & order use it) |
| `GET /products/{id}` | one product |
| `GET /categories` | distinct categories |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `DB_HOST / DB_PORT / DB_NAME / DB_USER` | PostgreSQL connection |
| `DB_PASSWORD` | from the `shopverse-secrets` Secret |
| `DB_SSLMODE` | `disable` in-cluster |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/product-service.yaml`](../../deploy/helm/values/product-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `product_views_total{product_id}`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t product-service:test .

# or natively
go test ./...
```

Run the whole platform locally with `docker compose up --build` from the repository root.
