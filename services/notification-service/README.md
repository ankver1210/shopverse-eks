# notification-service

Event-driven: consumes `user.*` and `order.*` events from the `shopverse.events` topic exchange, "sends" an email (logged — no provider needed) and stores an in-app notification. Manual acks with prefetch, a dead-letter queue for poison messages, and idempotency via processed `event_id`s in Redis.

| | |
|---|---|
| Language | Python 3.13 (FastAPI + aio-pika) |
| Owns | Redis list `notifications:<userId>` (last 50) |
| Events | consumes `user.registered`, `order.confirmed`, `order.failed` (queue `notification-service.events`, DLQ `notification-service.dlq`) |
| Key files | `app/main.py`, `app/templates.py` |

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /notifications` | the caller's inbox |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `REDIS_HOST / REDIS_PORT` | Redis connection |
| `REDIS_PASSWORD` | from the `shopverse-secrets` Secret |
| `RABBITMQ_HOST / RABBITMQ_USER` | broker connection |
| `RABBITMQ_PASSWORD` | from the `shopverse-secrets` Secret |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/notification-service.yaml`](../../deploy/helm/values/notification-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `notifications_sent_total{event_type}`, `notifications_duplicates_total`, `notifications_failed_total`, `notification_consumer_connected`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t notification-service:test .

# or natively
python -m pytest
```

Run the whole platform locally with `docker compose up --build` from the repository root.
