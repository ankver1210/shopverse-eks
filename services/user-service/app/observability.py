"""Shared "golden path" observability for every Python service in ShopVerse.

* Structured JSON logs (one JSON object per line on stdout)
* Prometheus RED metrics with the SAME names as the Node.js and Go services
* X-Request-ID propagation (read from the incoming request, sent on outgoing calls)
* Chaos injection (random errors / latency) used by the hands-on labs
"""
from __future__ import annotations

import asyncio
import contextvars
import json
import logging
import os
import random
import socket
import sys
import time
import uuid
from datetime import datetime, timezone

from fastapi import Request
from fastapi.responses import JSONResponse, Response
from prometheus_client import CONTENT_TYPE_LATEST, Counter, Gauge, Histogram, generate_latest
from starlette.middleware.base import BaseHTTPMiddleware

SERVICE_NAME = os.getenv("SERVICE_NAME", "unknown-service")
APP_VERSION = os.getenv("APP_VERSION", "dev")
POD_NAME = os.getenv("POD_NAME", socket.gethostname())
SKIP_PATHS = {"/healthz", "/readyz", "/metrics"}

request_id_var: contextvars.ContextVar[str] = contextvars.ContextVar("request_id", default="-")


# --------------------------------------------------------------------- logging
class JsonFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload = {
            "timestamp": datetime.fromtimestamp(record.created, tz=timezone.utc).isoformat(),
            "level": {"warning": "warn", "critical": "fatal"}.get(record.levelname.lower(), record.levelname.lower()),
            "service": SERVICE_NAME,
            "version": APP_VERSION,
            "pod": POD_NAME,
            "logger": record.name,
            "msg": record.getMessage(),
        }
        request_id = request_id_var.get()
        if request_id != "-":
            payload["request_id"] = request_id
        payload.update(getattr(record, "fields", {}) or {})
        if record.exc_info:
            payload["exception"] = self.formatException(record.exc_info)
        return json.dumps(payload, default=str)


def setup_logging() -> None:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JsonFormatter())
    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(os.getenv("LOG_LEVEL", "INFO").upper())
    logging.getLogger("uvicorn.access").disabled = True  # we write our own access log
    for noisy in ("aio_pika", "aiormq", "httpx", "httpcore", "psycopg.pool"):
        logging.getLogger(noisy).setLevel(logging.WARNING)


logger = logging.getLogger(SERVICE_NAME)


def log(level: int, msg: str, **fields) -> None:
    """Log with structured key/value fields: log(logging.INFO, "order placed", order_id=...)."""
    logger.log(level, msg, extra={"fields": fields})


# --------------------------------------------------------------------- metrics
HTTP_REQUESTS = Counter(
    "http_requests_total", "Total HTTP requests handled", ["method", "route", "status_code"]
)
HTTP_DURATION = Histogram(
    "http_request_duration_seconds",
    "HTTP request latency in seconds",
    ["method", "route", "status_code"],
    buckets=(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
)
BUILD_INFO = Gauge("app_build_info", "Build information (always 1)", ["version"])
BUILD_INFO.labels(APP_VERSION).set(1)

CHAOS_ERROR_RATE = float(os.getenv("CHAOS_ERROR_RATE", "0") or 0)
CHAOS_LATENCY_MS = int(os.getenv("CHAOS_LATENCY_MS", "0") or 0)


class ObservabilityMiddleware(BaseHTTPMiddleware):
    """Request id + chaos injection + access log + RED metrics, in one place."""

    async def dispatch(self, request: Request, call_next):
        request_id = request.headers.get("x-request-id") or str(uuid.uuid4())
        token = request_id_var.set(request_id)
        start = time.perf_counter()
        path = request.url.path
        route_label = None
        try:
            if path not in SKIP_PATHS and (CHAOS_ERROR_RATE > 0 or CHAOS_LATENCY_MS > 0):
                if CHAOS_LATENCY_MS > 0:
                    await asyncio.sleep(CHAOS_LATENCY_MS / 1000)
                if CHAOS_ERROR_RATE > 0 and random.random() < CHAOS_ERROR_RATE:
                    route_label = "chaos"
                    response = JSONResponse(
                        {"error": "chaos: injected failure", "service": SERVICE_NAME}, status_code=500
                    )
                else:
                    response = await call_next(request)
            else:
                response = await call_next(request)
        except Exception:  # unhandled error inside an endpoint
            logger.exception("unhandled error", extra={"fields": {"path": path}})
            response = JSONResponse({"error": "internal error"}, status_code=500)

        elapsed = time.perf_counter() - start
        if route_label is None:
            route = request.scope.get("route")
            route_label = getattr(route, "path", "unmatched")
        labels = (request.method, route_label, str(response.status_code))
        HTTP_REQUESTS.labels(*labels).inc()
        HTTP_DURATION.labels(*labels).observe(elapsed)
        response.headers["x-request-id"] = request_id
        response.headers["x-service-version"] = f"{SERVICE_NAME}@{APP_VERSION}"
        if path not in SKIP_PATHS:
            level = logging.ERROR if response.status_code >= 500 else (
                logging.WARNING if response.status_code >= 400 else logging.INFO)
            log(level, "request completed", method=request.method, path=path,
                status=response.status_code, duration_ms=round(elapsed * 1000))
        request_id_var.reset(token)
        return response


def metrics_response() -> Response:
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)


def health_body(status: str, **extra) -> dict:
    return {"status": status, "service": SERVICE_NAME, "version": APP_VERSION, "pod": POD_NAME, **extra}


def outgoing_headers(**extra: str) -> dict:
    """Headers for service-to-service calls (propagates the request id)."""
    return {"x-request-id": request_id_var.get(), **{k.replace("_", "-"): v for k, v in extra.items()}}


if CHAOS_ERROR_RATE > 0 or CHAOS_LATENCY_MS > 0:
    print(json.dumps({"level": "warning", "service": SERVICE_NAME, "msg": "CHAOS MODE ENABLED",
                      "error_rate": CHAOS_ERROR_RATE, "latency_ms": CHAOS_LATENCY_MS}), flush=True)


def install_error_handlers(app) -> None:
    """Return every error as {"error": "..."} — the same shape as the Node and Go services."""
    from fastapi.exceptions import RequestValidationError
    from starlette.exceptions import HTTPException as StarletteHTTPException

    @app.exception_handler(StarletteHTTPException)
    async def _http_error(_request, exc):
        return JSONResponse({"error": exc.detail}, status_code=exc.status_code)

    @app.exception_handler(RequestValidationError)
    async def _validation_error(_request, exc):
        first = exc.errors()[0] if exc.errors() else {}
        field = ".".join(str(p) for p in first.get("loc", []) if p not in ("body", "query", "path"))
        message = first.get("msg", "invalid request").removeprefix("Value error, ")
        return JSONResponse({"error": f"{field}: {message}" if field else message}, status_code=422)
