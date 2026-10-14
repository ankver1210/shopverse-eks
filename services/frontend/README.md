# frontend

The web storefront: a dependency-free single-page app (`public/`) served by Express with a strict Content-Security-Policy. In EKS the ALB sends `/api/*` straight to the api-gateway; the built-in `/api` proxy only matters when running the frontend alone (docker compose, port-forward).

| | |
|---|---|
| Language | Node.js 24 (Express) |
| Owns | — |
| Key files | `src/server.js`, `public/app.js`, `public/styles.css` |

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /` | the single-page app |
| `GET /config.json` | version, pod and environment shown in the footer |
| `ANY /api/*` | proxied to `API_GATEWAY_URL` (local development) |
| `GET /healthz` | liveness: the process is alive |
| `GET /readyz` | readiness: owned dependencies are reachable |
| `GET /metrics` | Prometheus metrics |

## Configuration (environment variables)

| Variable | Purpose |
|---|---|
| `API_GATEWAY_URL` | where to proxy `/api` (default `http://api-gateway`) |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/frontend.yaml`](../../deploy/helm/values/frontend.yaml)

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t frontend:test .

# or natively
node --test
```

Run the whole platform locally with `docker compose up --build` from the repository root.
