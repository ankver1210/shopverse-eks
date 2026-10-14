# order-service

Checkout orchestrator. Runs the **saga**: read cart → fetch authoritative prices → save PENDING order → reserve stock → charge payment → commit stock → CONFIRM, with compensating actions (release stock, refund) when a step fails. The final status change and its event are written in one transaction (transactional outbox).

| | |
|---|---|
| Language | Python 3.13 (FastAPI) |
| Owns | PostgreSQL database `orders` |
| Events | publishes `order.confirmed`, `order.failed` |
| Key files | `app/main.py` (the saga), `app/pricing.py`, `app/outbox.py` |

## Endpoints

| Endpoint | Description |
|---|---|
| `POST /orders` | `{payment_method, shipping_address}` → order (201) or `{error, order}` (402/409/503) |
| `GET /orders` | the caller's last 50 orders |
| `GET /orders/{id}` | one order (owned by the caller) |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `DB_HOST / DB_PORT / DB_NAME / DB_USER` | PostgreSQL connection |
| `DB_PASSWORD` | from the `shopverse-secrets` Secret |
| `DB_SSLMODE` | `disable` in-cluster |
| `RABBITMQ_HOST / RABBITMQ_USER` | broker connection |
| `RABBITMQ_PASSWORD` | from the `shopverse-secrets` Secret |
| `CART_ / PRODUCT_ / INVENTORY_ / PAYMENT_SERVICE_URL` | dependencies |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/order-service.yaml`](../../deploy/helm/values/order-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `orders_total{status}`, `orders_revenue_cents_total`, `order_saga_compensations_total{action}`, `outbox_pending_events`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t order-service:test .

# or natively
python -m pytest
```

Run the whole platform locally with `docker compose up --build` from the repository root.
