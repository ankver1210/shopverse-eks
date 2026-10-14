"""ShopVerse Recommendation Service (Python / FastAPI, stateless)

GET /recommendations/{product_id}?limit=4

Reads the catalog from product-service and caches it in memory for CACHE_TTL_SECONDS.
If product-service goes down it keeps serving the last known catalog ("stale-while-error"),
so the store degrades gracefully instead of failing.

Note: readiness does NOT depend on product-service. Making a pod "unready" because a
*different* service is down causes cascading failures — a classic Kubernetes anti-pattern.
"""
from __future__ import annotations

import logging
import os
import time
from contextlib import asynccontextmanager

import httpx
import uvicorn
from fastapi import FastAPI, HTTPException, Query
from prometheus_client import Counter

from .engine import ProductNotFound, recommend
from .observability import (
    ObservabilityMiddleware, health_body, install_error_handlers, log, metrics_response,
    outgoing_headers, setup_logging,
)

PRODUCT_URL = os.getenv("PRODUCT_SERVICE_URL", "http://product-service")
CACHE_TTL_SECONDS = int(os.getenv("CACHE_TTL_SECONDS", "60"))

CACHE = Counter("recommendation_cache_total", "Catalog cache lookups", ["result"])
cache: dict = {"products": [], "fetched_at": 0.0}
http: httpx.AsyncClient | None = None


async def get_catalog() -> list[dict]:
    age = time.monotonic() - cache["fetched_at"]
    if cache["products"] and age < CACHE_TTL_SECONDS:
        CACHE.labels("hit").inc()
        return cache["products"]
    try:
        r = await http.get(f"{PRODUCT_URL}/products?limit=500", headers=outgoing_headers())
        r.raise_for_status()
        cache["products"] = r.json()["products"]
        cache["fetched_at"] = time.monotonic()
        CACHE.labels("miss").inc()
    except httpx.HTTPError as exc:
        if not cache["products"]:
            raise HTTPException(status_code=503, detail="product catalog unavailable") from exc
        CACHE.labels("stale").inc()
        log(logging.WARNING, "product-service unavailable, serving stale catalog", error=str(exc))
    return cache["products"]


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global http
    setup_logging()
    http = httpx.AsyncClient(timeout=httpx.Timeout(3.0, connect=1.0))
    yield
    await http.aclose()


app = FastAPI(title="recommendation-service", version=os.getenv("APP_VERSION", "dev"), lifespan=lifespan)
app.add_middleware(ObservabilityMiddleware)
install_error_handlers(app)


@app.get("/healthz")
async def healthz():
    return health_body("ok")


@app.get("/readyz")
async def readyz():
    return health_body("ready", checks={"catalog_cache": f"{len(cache['products'])} products"})


@app.get("/metrics")
async def metrics():
    return metrics_response()


@app.get("/recommendations/{product_id}")
async def recommendations(product_id: int, limit: int = Query(default=4, ge=1, le=10)):
    catalog = await get_catalog()
    try:
        picks = recommend(catalog, product_id, limit)
    except ProductNotFound:
        raise HTTPException(status_code=404, detail=f"product {product_id} not found")
    return {"product_id": product_id, "strategy": "same-category+price-similarity", "recommendations": picks}


if __name__ == "__main__":
    uvicorn.run(
        app,
        host="0.0.0.0",  # noqa: S104
        port=int(os.getenv("PORT", "8080")),
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=20,
    )
