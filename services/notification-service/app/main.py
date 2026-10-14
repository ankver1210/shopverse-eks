"""ShopVerse Notification Service (Python / RabbitMQ consumer / Redis)

Event-driven: it never gets called during checkout. It listens to the `shopverse.events`
exchange and reacts to `user.*` and `order.*` events by "sending" an email (logged — no
real email provider needed) and storing an in-app notification in Redis.

Production patterns shown here:
  * Manual acks + prefetch  -> no message lost if the pod dies mid-processing
  * Dead-letter queue (DLQ) -> poison messages are parked, not retried forever
  * Idempotent consumer     -> duplicate deliveries (at-least-once) are ignored
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from urllib.parse import quote

import aio_pika
import redis.asyncio as aioredis
import uvicorn
from fastapi import FastAPI, Header
from fastapi.responses import JSONResponse
from prometheus_client import Counter, Gauge

from .observability import (
    ObservabilityMiddleware, health_body, install_error_handlers, log, metrics_response, setup_logging,
)
from .templates import render

EXCHANGE = "shopverse.events"
DEAD_LETTER_EXCHANGE = "shopverse.dlx"
QUEUE = "notification-service.events"
DEAD_LETTER_QUEUE = "notification-service.dlq"
MAX_PER_USER = 50

SENT = Counter("notifications_sent_total", "Notifications delivered", ["event_type"])
DUPLICATES = Counter("notifications_duplicates_total", "Duplicate events ignored")
FAILED = Counter("notifications_failed_total", "Events sent to the dead-letter queue")
CONSUMER_UP = Gauge("notification_consumer_connected", "1 if consuming from RabbitMQ")

redis = aioredis.Redis(
    host=os.getenv("REDIS_HOST", "localhost"),
    port=int(os.getenv("REDIS_PORT", "6379")),
    password=os.getenv("REDIS_PASSWORD") or None,
    decode_responses=True,
    socket_timeout=3,
    socket_connect_timeout=3,
)
state = {"consumer_connected": False}


def rabbitmq_url() -> str:
    user = quote(os.getenv("RABBITMQ_USER", "guest"), safe="")
    password = quote(os.getenv("RABBITMQ_PASSWORD", "guest"), safe="")
    return f"amqp://{user}:{password}@{os.getenv('RABBITMQ_HOST', 'localhost')}:{os.getenv('RABBITMQ_PORT', '5672')}/"


async def handle(message: aio_pika.abc.AbstractIncomingMessage) -> None:
    try:
        event = json.loads(message.body)
        event_id, event_type, data = event["event_id"], event["type"], event["data"]
        processed_key = f"notifications:processed:{event_id}"
        if await redis.exists(processed_key):
            DUPLICATES.inc()
            await message.ack()
            return
        rendered = render(event_type, data)
        if rendered is None:  # an event we don't care about
            await message.ack()
            return
        subject, body = rendered
        notification = {
            "id": event_id, "type": event_type, "subject": subject, "body": body,
            "created_at": datetime.now(timezone.utc).isoformat(),
        }
        user_key = f"notifications:{data['user_id']}"
        await (redis.pipeline()
               .lpush(user_key, json.dumps(notification))
               .ltrim(user_key, 0, MAX_PER_USER - 1)
               .set(processed_key, "1", ex=86_400)
               .execute())
        # A real system would call SES / SendGrid here.
        log(logging.INFO, "email sent (simulated)", to=data.get("email"), subject=subject, event_type=event_type)
        SENT.labels(event_type).inc()
        await message.ack()
    except Exception as exc:  # noqa: BLE001
        FAILED.inc()
        log(logging.ERROR, "could not process event, moving it to the DLQ", error=str(exc),
            body=message.body[:500].decode(errors="replace"))
        await message.reject(requeue=False)  # -> dead-letter exchange -> DLQ


async def consume_forever() -> None:
    while True:
        try:
            connection = await aio_pika.connect_robust(rabbitmq_url(), timeout=5)
            async with connection:
                channel = await connection.channel()
                await channel.set_qos(prefetch_count=20)
                events = await channel.declare_exchange(EXCHANGE, aio_pika.ExchangeType.TOPIC, durable=True)
                dlx = await channel.declare_exchange(DEAD_LETTER_EXCHANGE, aio_pika.ExchangeType.FANOUT, durable=True)
                dlq = await channel.declare_queue(DEAD_LETTER_QUEUE, durable=True)
                await dlq.bind(dlx)
                queue = await channel.declare_queue(
                    QUEUE, durable=True, arguments={"x-dead-letter-exchange": DEAD_LETTER_EXCHANGE}
                )
                for routing_key in ("user.*", "order.*"):
                    await queue.bind(events, routing_key)
                state["consumer_connected"] = True
                CONSUMER_UP.set(1)
                log(logging.INFO, "consuming events", queue=QUEUE)
                async with queue.iterator() as messages:
                    async for message in messages:
                        await handle(message)
        except asyncio.CancelledError:
            raise
        except Exception as exc:  # noqa: BLE001
            log(logging.WARNING, "rabbitmq unavailable, retrying in 3s", error=str(exc))
        state["consumer_connected"] = False
        CONSUMER_UP.set(0)
        await asyncio.sleep(3)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    setup_logging()
    consumer = asyncio.create_task(consume_forever(), name="event-consumer")
    yield
    consumer.cancel()
    try:
        await consumer
    except asyncio.CancelledError:
        pass
    await redis.aclose()


app = FastAPI(title="notification-service", version=os.getenv("APP_VERSION", "dev"), lifespan=lifespan)
app.add_middleware(ObservabilityMiddleware)
install_error_handlers(app)


@app.get("/healthz")
async def healthz():
    return health_body("ok")


@app.get("/readyz")
async def readyz():
    checks = {"redis": "ok", "rabbitmq": "connected" if state["consumer_connected"] else "disconnected"}
    try:
        await redis.ping()
    except Exception as exc:  # noqa: BLE001
        checks["redis"] = str(exc)
        return JSONResponse(health_body("not_ready", checks=checks), status_code=503)
    return health_body("ready", checks=checks)


@app.get("/metrics")
async def metrics():
    return metrics_response()


@app.get("/notifications")
async def list_notifications(x_user_id: str = Header(...)):
    raw = await redis.lrange(f"notifications:{x_user_id}", 0, MAX_PER_USER - 1)
    return {"notifications": [json.loads(item) for item in raw]}


if __name__ == "__main__":
    uvicorn.run(
        app,
        host="0.0.0.0",  # noqa: S104
        port=int(os.getenv("PORT", "8080")),
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=20,
    )
