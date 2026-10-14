# recommendation-service

"You might also like": same-category products ranked by price similarity, then top items from other categories. Caches the catalog and keeps serving the stale copy if product-service fails. Its readiness probe intentionally does not depend on product-service (avoids cascading failures).

| | |
|---|---|
| Language | Python 3.13 (FastAPI) |
| Owns | in-memory catalog cache (60 s) |
| Key files | `app/main.py`, `app/engine.py` |

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /recommendations/{product_id}?limit=4` | up to 10 recommendations |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `PRODUCT_SERVICE_URL` | catalog source |
| `CACHE_TTL_SECONDS` | cache freshness (60) |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/recommendation-service.yaml`](../../deploy/helm/values/recommendation-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `recommendation_cache_total{result=hit|miss|stale}`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t recommendation-service:test .

# or natively
python -m pytest
```

Run the whole platform locally with `docker compose up --build` from the repository root.
