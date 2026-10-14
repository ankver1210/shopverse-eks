# api-gateway

Single entry point for all API traffic. Verifies JWTs, strips spoofed identity headers and forwards the trusted identity (`x-user-id`, `x-user-email`) downstream, rate-limits per client IP, and protects every upstream with a circuit breaker. `payment-service` is deliberately not routable from the internet.

| | |
|---|---|
| Language | Node.js 24 (Express) |
| Owns | — |
| Key files | `src/server.js`, `src/lib/proxy.js`, `src/lib/resilience.js` |

## Endpoints

| Endpoint | Description |
|---|---|
| `/api/auth/*` | → user-service (public) |
| `/api/users/*` | → user-service (JWT) |
| `/api/products/*, /api/categories` | → product-service (public) |
| `GET /api/inventory/*` | → inventory-service (public, GET only) |
| `/api/recommendations/*` | → recommendation-service (public) |
| `/api/cart/*` | → cart-service (JWT) |
| `/api/orders/*` | → order-service (JWT) |
| `/api/notifications` | → notification-service (JWT) |
| `GET /api/status` | readiness of every service (Platform page) |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `*_SERVICE_URL` | upstream base URLs |
| `JWT_SECRET` | HS256 key shared with user-service (Secret) |
| `RATE_LIMIT_PER_MINUTE` | requests per client IP per pod |
| `UPSTREAM_TIMEOUT_MS` | per-request upstream timeout |
| `CIRCUIT_FAILURE_THRESHOLD / CIRCUIT_RESET_TIMEOUT_MS` | breaker tuning |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/api-gateway.yaml`](../../deploy/helm/values/api-gateway.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `gateway_upstream_requests_total`, `gateway_circuit_breaker_state`, `gateway_rate_limited_total`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t api-gateway:test .

# or natively
node --test
```

Run the whole platform locally with `docker compose up --build` from the repository root.
