"""ShopVerse Order Service (Python / FastAPI / PostgreSQL)

Checkout is a distributed transaction across several services. We implement it as an
orchestrated SAGA: each step has a compensating action that undoes it if a later step fails.

    1. cart-service       read the user's cart
    2. product-service    fetch authoritative prices (never trust prices from the client)
    3. order-service      save order as PENDING
    4. inventory-service  reserve stock              (compensation: release)
    5. payment-service    charge the customer        (compensation: refund)
    6. inventory-service  commit the reservation
    7. order-service      mark CONFIRMED + emit `order.confirmed` (outbox, same transaction)
    8. cart-service       clear the cart (best effort)
"""
from __future__ import annotations

import logging
import os
import uuid
from contextlib import asynccontextmanager
from urllib.parse import unquote

import httpx
import uvicorn
from fastapi import FastAPI, Header, HTTPException
from fastapi.encoders import jsonable_encoder
from fastapi.responses import JSONResponse
from prometheus_client import Counter
from pydantic import BaseModel, Field

from .db import Database
from .observability import (
    ObservabilityMiddleware, health_body, install_error_handlers, log, metrics_response,
    outgoing_headers, setup_logging,
)
from .outbox import OUTBOX_SCHEMA, OutboxPublisher, enqueue_event
from .pricing import compute_totals

CART_URL = os.getenv("CART_SERVICE_URL", "http://cart-service")
PRODUCT_URL = os.getenv("PRODUCT_SERVICE_URL", "http://product-service")
INVENTORY_URL = os.getenv("INVENTORY_SERVICE_URL", "http://inventory-service")
PAYMENT_URL = os.getenv("PAYMENT_SERVICE_URL", "http://payment-service")

SCHEMA = """
CREATE TABLE IF NOT EXISTS orders (
    id                UUID PRIMARY KEY,
    user_id           TEXT NOT NULL,
    status            TEXT NOT NULL,
    subtotal_cents    INTEGER NOT NULL,
    shipping_cents    INTEGER NOT NULL,
    total_cents       INTEGER NOT NULL,
    shipping_address  TEXT NOT NULL,
    payment_method    TEXT NOT NULL,
    failure_reason    TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS orders_user_idx ON orders (user_id, created_at DESC);
CREATE TABLE IF NOT EXISTS order_items (
    order_id     UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id   INTEGER NOT NULL,
    name         TEXT NOT NULL,
    emoji        TEXT NOT NULL DEFAULT '',
    price_cents  INTEGER NOT NULL,
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (order_id, product_id)
);
""" + OUTBOX_SCHEMA

ORDERS = Counter("orders_total", "Orders by final status", ["status"])
REVENUE = Counter("orders_revenue_cents_total", "Revenue from confirmed orders, in cents")
SAGA_COMPENSATIONS = Counter("order_saga_compensations_total", "Compensating actions executed", ["action"])

db = Database(SCHEMA)
publisher = OutboxPublisher(db)
http: httpx.AsyncClient | None = None


class DependencyError(Exception):
    def __init__(self, service: str, detail: str):
        super().__init__(f"{service}: {detail}")
        self.service = service


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global http
    setup_logging()
    http = httpx.AsyncClient(timeout=httpx.Timeout(5.0, connect=2.0),
                             limits=httpx.Limits(max_connections=100, max_keepalive_connections=20))
    await db.connect()
    publisher.start()
    yield
    await publisher.stop()
    await http.aclose()
    await db.close()


app = FastAPI(title="order-service", version=os.getenv("APP_VERSION", "dev"), lifespan=lifespan)
app.add_middleware(ObservabilityMiddleware)
install_error_handlers(app)


class CreateOrder(BaseModel):
    payment_method: str = Field(default="tok_visa", pattern=r"^tok_[a-z_]{1,30}$")
    shipping_address: str = Field(min_length=5, max_length=300)


async def call(service: str, method: str, url: str, headers: dict, **kwargs) -> httpx.Response:
    """Call another service; network failures become DependencyError (-> HTTP 503)."""
    try:
        return await http.request(method, url, headers=headers, **kwargs)
    except httpx.HTTPError as exc:
        raise DependencyError(service, f"unreachable ({exc.__class__.__name__})") from exc


async def load_order(order_id: str, user_id: str | None = None) -> dict | None:
    async with db.pool.connection() as conn:
        query = "SELECT * FROM orders WHERE id = %s" + (" AND user_id = %s" if user_id else "")
        params = (order_id, user_id) if user_id else (order_id,)
        order = await (await conn.execute(query, params)).fetchone()
        if not order:
            return None
        items = await (await conn.execute(
            "SELECT product_id, name, emoji, price_cents, quantity FROM order_items WHERE order_id = %s "
            "ORDER BY product_id", (order_id,))).fetchall()
    order["items"] = items
    return order


async def fail_order(order_id: str, user_id: str, email: str, reason: str) -> dict:
    async with db.pool.connection() as conn:
        async with conn.transaction():
            await conn.execute(
                "UPDATE orders SET status = 'FAILED', failure_reason = %s, updated_at = now() WHERE id = %s",
                (reason, order_id),
            )
            await enqueue_event(conn, "order.failed", {
                "order_id": order_id, "user_id": user_id, "email": email, "reason": reason,
            })
    ORDERS.labels("FAILED").inc()
    log(logging.WARNING, "order failed", order_id=order_id, reason=reason)
    return await load_order(order_id)


def error_response(status: int, reason: str, order: dict | None = None) -> JSONResponse:
    return JSONResponse(status_code=status, content=jsonable_encoder({"error": reason, "order": order}))


@app.get("/healthz")
async def healthz():
    return health_body("ok")


@app.get("/readyz")
async def readyz():
    checks = {"postgres": "ok", "rabbitmq": publisher.status}
    try:
        await db.ping()
    except Exception as exc:  # noqa: BLE001
        checks["postgres"] = str(exc)
        return JSONResponse(health_body("not_ready", checks=checks), status_code=503)
    return health_body("ready", checks=checks)


@app.get("/metrics")
async def metrics():
    return metrics_response()


@app.post("/orders", status_code=201)
async def create_order(body: CreateOrder, x_user_id: str = Header(...), x_user_email: str = Header(default="")):
    user_headers = outgoing_headers(x_user_id=x_user_id)
    email = unquote(x_user_email)
    try:
        # 1. cart --------------------------------------------------------------
        r = await call("cart-service", "GET", f"{CART_URL}/cart", user_headers)
        if r.status_code != 200:
            raise DependencyError("cart-service", f"HTTP {r.status_code}")
        cart_items = r.json()["items"]
        if not cart_items:
            raise HTTPException(status_code=400, detail="Your cart is empty")

        # 2. authoritative prices ---------------------------------------------------
        ids = ",".join(str(i["product_id"]) for i in cart_items)
        r = await call("product-service", "GET", f"{PRODUCT_URL}/products/batch?ids={ids}", outgoing_headers())
        if r.status_code != 200:
            raise DependencyError("product-service", f"HTTP {r.status_code}")
        products = {p["id"]: p for p in r.json()["products"]}
        missing = [i["product_id"] for i in cart_items if i["product_id"] not in products]
        if missing:
            raise HTTPException(status_code=409, detail=f"Products no longer available: {missing}")
        lines = [{
            "product_id": i["product_id"],
            "name": products[i["product_id"]]["name"],
            "emoji": products[i["product_id"]].get("emoji", ""),
            "price_cents": products[i["product_id"]]["price_cents"],
            "quantity": i["quantity"],
        } for i in cart_items]
        totals = compute_totals(lines)
    except DependencyError as exc:
        return error_response(503, f"Checkout is temporarily unavailable ({exc})")

    # 3. persist PENDING order ------------------------------------------------------
    order_id = str(uuid.uuid4())
    async with db.pool.connection() as conn:
        async with conn.transaction():
            await conn.execute(
                "INSERT INTO orders (id, user_id, status, subtotal_cents, shipping_cents, total_cents, "
                "shipping_address, payment_method) VALUES (%s, %s, 'PENDING', %s, %s, %s, %s, %s)",
                (order_id, x_user_id, totals["subtotal_cents"], totals["shipping_cents"],
                 totals["total_cents"], body.shipping_address, body.payment_method),
            )
            for line in lines:
                await conn.execute(
                    "INSERT INTO order_items (order_id, product_id, name, emoji, price_cents, quantity) "
                    "VALUES (%s, %s, %s, %s, %s, %s)",
                    (order_id, line["product_id"], line["name"], line["emoji"], line["price_cents"], line["quantity"]),
                )
    log(logging.INFO, "order created", order_id=order_id, total_cents=totals["total_cents"])

    # 4. reserve stock -------------------------------------------------------------
    reservation = {"order_id": order_id,
                   "items": [{"product_id": l["product_id"], "quantity": l["quantity"]} for l in lines]}
    try:
        r = await call("inventory-service", "POST", f"{INVENTORY_URL}/inventory/reserve",
                       outgoing_headers(), json=reservation)
    except DependencyError as exc:
        order = await fail_order(order_id, x_user_id, email, "inventory service unavailable")
        return error_response(503, str(exc), order)
    if r.status_code == 409:
        reason = r.json().get("error", "insufficient stock")
        order = await fail_order(order_id, x_user_id, email, reason)
        return error_response(409, reason, order)
    if r.status_code >= 300:
        order = await fail_order(order_id, x_user_id, email, f"inventory error HTTP {r.status_code}")
        return error_response(503, "inventory service error", order)

    async def release_stock():
        SAGA_COMPENSATIONS.labels("release_stock").inc()
        try:
            await call("inventory-service", "POST", f"{INVENTORY_URL}/inventory/release",
                       outgoing_headers(), json={"order_id": order_id})
        except DependencyError:
            log(logging.ERROR, "COMPENSATION FAILED: could not release stock", order_id=order_id)

    # 5. charge payment ------------------------------------------------------------
    charge = {"order_id": order_id, "user_id": x_user_id, "amount_cents": totals["total_cents"],
              "payment_method": body.payment_method}
    try:
        r = await call("payment-service", "POST", f"{PAYMENT_URL}/payments/charge",
                       outgoing_headers(), json=charge)
    except DependencyError as exc:
        # Unknown outcome (maybe charged, maybe not): refund defensively (idempotent), then release stock.
        SAGA_COMPENSATIONS.labels("refund").inc()
        try:
            await call("payment-service", "POST", f"{PAYMENT_URL}/payments/refund",
                       outgoing_headers(), json={"order_id": order_id})
        except DependencyError:
            log(logging.ERROR, "COMPENSATION FAILED: refund not confirmed", order_id=order_id)
        await release_stock()
        order = await fail_order(order_id, x_user_id, email, "payment service unavailable")
        return error_response(503, str(exc), order)
    if r.status_code == 402:
        await release_stock()
        order = await fail_order(order_id, x_user_id, email, "payment declined by the bank")
        return error_response(402, "Payment was declined. Try the 'always approved' test card.", order)
    if r.status_code >= 300:
        await release_stock()
        order = await fail_order(order_id, x_user_id, email, f"payment error HTTP {r.status_code}")
        return error_response(503, "payment service error", order)

    # 6. commit reservation (if this fails the stock simply stays reserved; safe) ----------
    try:
        await call("inventory-service", "POST", f"{INVENTORY_URL}/inventory/commit",
                   outgoing_headers(), json={"order_id": order_id})
    except DependencyError:
        log(logging.WARNING, "could not commit reservation, it stays reserved", order_id=order_id)

    # 7. confirm + event, atomically ------------------------------------------------
    async with db.pool.connection() as conn:
        async with conn.transaction():
            await conn.execute("UPDATE orders SET status = 'CONFIRMED', updated_at = now() WHERE id = %s",
                               (order_id,))
            await enqueue_event(conn, "order.confirmed", {
                "order_id": order_id, "user_id": x_user_id, "email": email,
                "total_cents": totals["total_cents"], "item_count": totals["item_count"],
                "shipping_address": body.shipping_address,
            })
    ORDERS.labels("CONFIRMED").inc()
    REVENUE.inc(totals["total_cents"])

    # 8. clear cart (best effort) ----------------------------------------------------
    try:
        await call("cart-service", "DELETE", f"{CART_URL}/cart", user_headers)
    except DependencyError:
        log(logging.WARNING, "could not clear cart", order_id=order_id)

    log(logging.INFO, "order confirmed", order_id=order_id)
    return JSONResponse(status_code=201, content=jsonable_encoder(await load_order(order_id)))


@app.get("/orders")
async def list_orders(x_user_id: str = Header(...)):
    async with db.pool.connection() as conn:
        orders = await (await conn.execute(
            "SELECT * FROM orders WHERE user_id = %s ORDER BY created_at DESC LIMIT 50", (x_user_id,)
        )).fetchall()
        if orders:
            items = await (await conn.execute(
                "SELECT order_id, product_id, name, emoji, price_cents, quantity FROM order_items "
                "WHERE order_id = ANY(%s)", ([o["id"] for o in orders],)
            )).fetchall()
        else:
            items = []
    by_order: dict = {}
    for item in items:
        by_order.setdefault(item.pop("order_id"), []).append(item)
    for order in orders:
        order["items"] = by_order.get(order["id"], [])
    return {"orders": orders}


@app.get("/orders/{order_id}")
async def get_order(order_id: uuid.UUID, x_user_id: str = Header(...)):
    order = await load_order(str(order_id), x_user_id)
    if not order:
        raise HTTPException(status_code=404, detail="Order not found")
    return order


if __name__ == "__main__":
    uvicorn.run(
        app,
        host="0.0.0.0",  # noqa: S104
        port=int(os.getenv("PORT", "8080")),
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=20,
    )
