"""ShopVerse User Service (Python / FastAPI / PostgreSQL)

Owns user accounts and authentication.
  POST /auth/register   create an account, returns a JWT
  POST /auth/login      exchange email + password for a JWT
  GET  /users/me        profile of the caller (identity comes from the api-gateway)

Publishes `user.registered` events through the transactional outbox.
"""
from __future__ import annotations

import logging
import os
from contextlib import asynccontextmanager
from datetime import datetime, timedelta, timezone

import bcrypt
import jwt
import psycopg
import uvicorn
from fastapi import FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse
from prometheus_client import Counter
from pydantic import BaseModel, EmailStr, Field, field_validator
from starlette.concurrency import run_in_threadpool

from .db import Database
from .observability import (
    ObservabilityMiddleware, health_body, install_error_handlers, log, metrics_response, setup_logging,
)
from .outbox import OUTBOX_SCHEMA, OutboxPublisher, enqueue_event

JWT_SECRET = os.getenv("JWT_SECRET", "")
JWT_ISSUER = "shopverse-user-service"
JWT_TTL_HOURS = int(os.getenv("JWT_TTL_HOURS", "24"))
BCRYPT_ROUNDS = int(os.getenv("BCRYPT_ROUNDS", "10"))

SCHEMA = """
CREATE TABLE IF NOT EXISTS users (
    id             BIGSERIAL PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    password_hash  TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
""" + OUTBOX_SCHEMA

REGISTRATIONS = Counter("user_registrations_total", "Successful user registrations")
LOGINS = Counter("user_logins_total", "Login attempts", ["result"])

db = Database(SCHEMA)
publisher = OutboxPublisher(db)
# A real bcrypt hash used to keep login timing identical for unknown emails
# (prevents attackers from discovering which emails are registered).
_DUMMY_HASH = bcrypt.hashpw(b"timing-safe-dummy", bcrypt.gensalt(rounds=BCRYPT_ROUNDS))


@asynccontextmanager
async def lifespan(_app: FastAPI):
    setup_logging()
    if not JWT_SECRET:
        raise RuntimeError("JWT_SECRET environment variable is required")
    await db.connect()
    publisher.start()
    yield
    await publisher.stop()
    await db.close()


app = FastAPI(title="user-service", version=os.getenv("APP_VERSION", "dev"), lifespan=lifespan)
app.add_middleware(ObservabilityMiddleware)
install_error_handlers(app)


class RegisterRequest(BaseModel):
    email: EmailStr
    password: str = Field(min_length=8, max_length=72)
    name: str = Field(min_length=1, max_length=80)

    @field_validator("password")
    @classmethod
    def bcrypt_limit(cls, value: str) -> str:
        if len(value.encode()) > 72:
            raise ValueError("password is too long (max 72 bytes)")
        return value


class LoginRequest(BaseModel):
    email: EmailStr
    password: str = Field(max_length=72)


def public_user(row: dict) -> dict:
    return {"id": row["id"], "email": row["email"], "name": row["name"], "created_at": row["created_at"]}


def issue_token(user: dict) -> str:
    now = datetime.now(timezone.utc)
    claims = {
        "sub": str(user["id"]),
        "email": user["email"],
        "name": user["name"],
        "iss": JWT_ISSUER,
        "iat": now,
        "exp": now + timedelta(hours=JWT_TTL_HOURS),
    }
    return jwt.encode(claims, JWT_SECRET, algorithm="HS256")


def token_response(user: dict, status_code: int = 200) -> JSONResponse:
    from fastapi.encoders import jsonable_encoder

    return JSONResponse(
        status_code=status_code,
        content=jsonable_encoder({
            "access_token": issue_token(user),
            "token_type": "bearer",
            "expires_in": JWT_TTL_HOURS * 3600,
            "user": public_user(user),
        }),
    )


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


@app.post("/auth/register", status_code=201)
async def register(body: RegisterRequest):
    email = body.email.lower()
    password_hash = await run_in_threadpool(
        lambda: bcrypt.hashpw(body.password.encode(), bcrypt.gensalt(rounds=BCRYPT_ROUNDS)).decode()
    )
    try:
        async with db.pool.connection() as conn:
            async with conn.transaction():
                cur = await conn.execute(
                    "INSERT INTO users (email, name, password_hash) VALUES (%s, %s, %s) "
                    "RETURNING id, email, name, created_at",
                    (email, body.name.strip(), password_hash),
                )
                user = await cur.fetchone()
                # Same transaction => the event exists if and only if the user exists.
                await enqueue_event(conn, "user.registered", {
                    "user_id": str(user["id"]), "email": user["email"], "name": user["name"],
                })
    except psycopg.errors.UniqueViolation:
        raise HTTPException(status_code=409, detail="An account with this email already exists")
    REGISTRATIONS.inc()
    log(logging.INFO, "user registered", user_id=user["id"])
    return token_response(user, status_code=201)


@app.post("/auth/login")
async def login(body: LoginRequest):
    async with db.pool.connection() as conn:
        cur = await conn.execute(
            "SELECT id, email, name, password_hash, created_at FROM users WHERE email = %s",
            (body.email.lower(),),
        )
        user = await cur.fetchone()
    stored = user["password_hash"].encode() if user else _DUMMY_HASH
    valid = await run_in_threadpool(bcrypt.checkpw, body.password.encode(), stored)
    if not user or not valid:
        LOGINS.labels("failure").inc()
        raise HTTPException(status_code=401, detail="Invalid email or password")
    LOGINS.labels("success").inc()
    return token_response(user)


@app.get("/users/me")
async def me(x_user_id: str = Header(...)):
    async with db.pool.connection() as conn:
        cur = await conn.execute(
            "SELECT id, email, name, created_at FROM users WHERE id = %s", (int(x_user_id),)
        )
        user = await cur.fetchone()
    if not user:
        raise HTTPException(status_code=404, detail="User not found")
    return public_user(user)


if __name__ == "__main__":
    uvicorn.run(
        app,
        host="0.0.0.0",  # noqa: S104 - must listen on all interfaces inside the container
        port=int(os.getenv("PORT", "8080")),
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=20,
        proxy_headers=True,
    )
