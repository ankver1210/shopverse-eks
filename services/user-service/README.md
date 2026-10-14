# user-service

Accounts and authentication. Passwords are hashed with bcrypt (in a thread pool so the event loop stays responsive); login returns a 24-hour HS256 JWT. Registration publishes `user.registered` through the transactional outbox. Unknown emails take the same time as wrong passwords (no user enumeration).

| | |
|---|---|
| Language | Python 3.13 (FastAPI) |
| Owns | PostgreSQL database `users` |
| Events | publishes `user.registered` |
| Key files | `app/main.py`, `app/outbox.py`, `app/db.py` |

## Endpoints

| Endpoint | Description |
|---|---|
| `POST /auth/register` | `{email, password, name}` → token + user |
| `POST /auth/login` | `{email, password}` → token + user |
| `GET /users/me` | profile of `x-user-id` |
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
| `JWT_SECRET` | signing key (Secret) |
| `JWT_TTL_HOURS` | token lifetime (24) |
| `BCRYPT_ROUNDS` | hash cost (10) |
| `SERVICE_NAME` | name used in logs/metrics |
| `APP_VERSION` | version shown in /healthz (set from the image tag) |
| `PORT` | listen port (default 8080) |
| `LOG_LEVEL` | debug | info | warn | error |
| `CHAOS_ERROR_RATE` | 0–1, fraction of requests that fail with 500 (labs) |
| `CHAOS_LATENCY_MS` | extra delay per request (labs) |

Kubernetes values: [`deploy/helm/values/user-service.yaml`](../../deploy/helm/values/user-service.yaml)

## Metrics

Standard: `http_requests_total`, `http_request_duration_seconds`, `app_build_info`. Service-specific: `user_registrations_total`, `user_logins_total{result}`, `events_published_total`, `outbox_pending_events`.

## Develop and test

```bash
# unit tests, exactly like CI
docker build --target test -t user-service:test .

# or natively
python -m pytest
```

Run the whole platform locally with `docker compose up --build` from the repository root.
