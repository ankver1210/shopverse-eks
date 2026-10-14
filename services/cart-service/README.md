# cart-service

Shopping carts. Product names and prices are always fetched live from product-service, so carts never show stale prices. If product-service is down the cart is still returned, flagged `degraded`.

| | |
|---|---|
| Language | Node.js 24 (Express) |
| Owns | Redis hash `cart:<userId>` (7-day TTL) |
| Key files | `src/server.js`, `src/cart.js` |

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /cart` | cart with live prices and totals |
| `POST /cart/items` | `{product_id, quantity}` add (max 10 per item) |
| `PUT /cart/items/{productId}` | `{quantity}` set (0 removes) |
| `DELETE /cart/items/{productId}` | remove one item |
| `DELETE /cart` | clear (order-service calls this after checkout) |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `REDIS_HOST / REDIS_PORT` | Redis connection |
| `REDIS_PASSWORD` | from the `shopverse-secrets` Secret |
| `PRODUCT_SERVICE_URL` | product-service base URL |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/cart-service.yaml`](../../deploy/helm/values/cart-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `cart_operations_total{operation}`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t cart-service:test .

# or natively
node --test
```

Run the whole platform locally with `docker compose up --build` from the repository root.
