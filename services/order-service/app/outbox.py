"""Transactional Outbox pattern.

Problem: "save to the database AND publish an event" are two systems. If the service
crashes between them, the event is lost (or published for data that was never saved).

Solution: write the event into an `outbox` table in the SAME database transaction as the
business data. A background loop then publishes unpublished rows to RabbitMQ and marks
them as published. Delivery is "at least once", so consumers de-duplicate by event_id.

`FOR UPDATE SKIP LOCKED` lets several replicas run the publisher safely at the same time.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
import uuid
from datetime import datetime, timezone
from urllib.parse import quote

import aio_pika
from prometheus_client import Counter, Gauge

from .observability import SERVICE_NAME, log

EXCHANGE = "shopverse.events"

OUTBOX_SCHEMA = """
CREATE TABLE IF NOT EXISTS outbox (
    id            UUID PRIMARY KEY,
    event_type    TEXT NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
"""

EVENTS_PUBLISHED = Counter("events_published_total", "Events published to RabbitMQ", ["event_type"])
OUTBOX_PENDING = Gauge("outbox_pending_events", "Events waiting in the outbox to be published")


def rabbitmq_url() -> str:
    user = quote(os.getenv("RABBITMQ_USER", "guest"), safe="")
    password = quote(os.getenv("RABBITMQ_PASSWORD", "guest"), safe="")
    host = os.getenv("RABBITMQ_HOST", "localhost")
    port = os.getenv("RABBITMQ_PORT", "5672")
    return f"amqp://{user}:{password}@{host}:{port}/"


async def enqueue_event(conn, event_type: str, data: dict) -> dict:
    """Insert an event into the outbox using the caller's open transaction."""
    event = {
        "event_id": str(uuid.uuid4()),
        "type": event_type,
        "source": SERVICE_NAME,
        "occurred_at": datetime.now(timezone.utc).isoformat(),
        "data": data,
    }
    await conn.execute(
        "INSERT INTO outbox (id, event_type, payload) VALUES (%s, %s, %s)",
        (event["event_id"], event_type, json.dumps(event, default=str)),
    )
    return event


class OutboxPublisher:
    def __init__(self, db, interval_seconds: float = 1.0, batch_size: int = 50):
        self.db = db
        self.interval = interval_seconds
        self.batch_size = batch_size
        self.connection: aio_pika.abc.AbstractRobustConnection | None = None
        self.exchange: aio_pika.abc.AbstractExchange | None = None
        self._task: asyncio.Task | None = None
        self._stopping = False

    @property
    def status(self) -> str:
        if self.connection is not None and not self.connection.is_closed:
            return "connected"
        return "disconnected"

    def start(self) -> None:
        self._task = asyncio.create_task(self._run(), name="outbox-publisher")

    async def stop(self) -> None:
        self._stopping = True
        if self._task:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
        if self.connection is not None:
            await self.connection.close()

    async def _ensure_connection(self) -> None:
        if self.connection is not None and not self.connection.is_closed:
            return
        self.connection = await aio_pika.connect_robust(rabbitmq_url(), timeout=5)
        channel = await self.connection.channel(publisher_confirms=True)
        self.exchange = await channel.declare_exchange(
            EXCHANGE, aio_pika.ExchangeType.TOPIC, durable=True
        )
        log(logging.INFO, "connected to rabbitmq")

    async def _publish_batch(self) -> int:
        assert self.exchange is not None
        async with self.db.pool.connection() as conn:
            async with conn.transaction():
                cur = await conn.execute(
                    "SELECT id, event_type, payload FROM outbox WHERE published_at IS NULL "
                    "ORDER BY created_at LIMIT %s FOR UPDATE SKIP LOCKED",
                    (self.batch_size,),
                )
                rows = await cur.fetchall()
                for row in rows:
                    await self.exchange.publish(
                        aio_pika.Message(
                            body=json.dumps(row["payload"]).encode(),
                            content_type="application/json",
                            delivery_mode=aio_pika.DeliveryMode.PERSISTENT,
                            message_id=str(row["id"]),
                            type=row["event_type"],
                        ),
                        routing_key=row["event_type"],
                    )
                    await conn.execute("UPDATE outbox SET published_at = now() WHERE id = %s", (row["id"],))
                    EVENTS_PUBLISHED.labels(row["event_type"]).inc()
        return len(rows)

    async def _update_pending_gauge(self) -> None:
        async with self.db.pool.connection() as conn:
            cur = await conn.execute("SELECT count(*) AS n FROM outbox WHERE published_at IS NULL")
            OUTBOX_PENDING.set((await cur.fetchone())["n"])

    async def _run(self) -> None:
        backoff = self.interval
        while not self._stopping:
            published = 0
            try:
                await self._update_pending_gauge()
                await self._ensure_connection()
                published = await self._publish_batch()
                backoff = self.interval
            except asyncio.CancelledError:
                raise
            except Exception as exc:  # noqa: BLE001 - keep the loop alive no matter what
                log(logging.WARNING, "outbox publish failed, will retry", error=str(exc))
                backoff = min(backoff * 2, 15)
            # If we just published a full batch there may be more: loop immediately.
            await asyncio.sleep(0 if published >= self.batch_size else backoff)
