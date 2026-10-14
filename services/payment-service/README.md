# payment-service

A **simulated** payment processor — no real card data, ever. Test tokens: `tok_visa` is approved, `tok_declined` is declined (HTTP 402); charges above $5,000 trip a fraud rule. Idempotent per order. Only order-service may call it (NetworkPolicy, and the gateway has no route to it).

| | |
|---|---|
| Language | Go 1.26 (net/http) |
| Owns | PostgreSQL database `payments` |
| Key files | `main.go`, `internal/platform/` |

## Endpoints

| Endpoint | Description |
|---|---|
| `POST /payments/charge` | `{order_id, user_id, amount_cents, payment_method}` |
| `POST /payments/refund` | `{order_id}` |
| `GET /payments/{orderId}` | payment record |
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

Kubernetes values: [`deploy/helm/values/payment-service.yaml`](../../deploy/helm/values/payment-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `payments_total{status}`, `payments_amount_cents_total{status}`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t payment-service:test .

# or natively
go test ./...
```

Run the whole platform locally with `docker compose up --build` from the repository root.
