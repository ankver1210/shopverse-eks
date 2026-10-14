# inventory-service

Stock levels and checkout reservations. Reservations lock rows with `SELECT … FOR UPDATE` in a consistent order (no deadlocks, no overselling) and are idempotent per order. A CronJob tops stock back up every 6 hours.

| | |
|---|---|
| Language | Go 1.26 (net/http) |
| Owns | PostgreSQL database `inventory` |
| Key files | `main.go`, `internal/platform/` |

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /inventory?ids=1,2` | stock for several products (public) |
| `GET /inventory/{productId}` | stock for one product (public) |
| `POST /inventory/reserve` | `{order_id, items[]}` → 201, or 409 when short |
| `POST /inventory/release` | `{order_id}` undo (saga compensation) |
| `POST /inventory/commit` | `{order_id}` finalise |
| `POST /inventory/restock` | top up (restock CronJob) |
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

Kubernetes values: [`deploy/helm/values/inventory-service.yaml`](../../deploy/helm/values/inventory-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `inventory_reservations_total{result}`, `inventory_available_units`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t inventory-service:test .

# or natively
go test ./...
```

Run the whole platform locally with `docker compose up --build` from the repository root.
