"""PostgreSQL connection pool with startup retry and simple schema migration."""
from __future__ import annotations

import asyncio
import logging
import os

from psycopg.conninfo import make_conninfo
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

from .observability import log


def _conninfo() -> str:
    parts = {
        "host": os.getenv("DB_HOST", "localhost"),
        "port": os.getenv("DB_PORT", "5432"),
        "dbname": os.getenv("DB_NAME", "app"),
        "user": os.getenv("DB_USER", "app"),
        "password": os.getenv("DB_PASSWORD", ""),
        "sslmode": os.getenv("DB_SSLMODE", "prefer"),
        "connect_timeout": "5",
        "application_name": os.getenv("SERVICE_NAME", "python-service"),
    }
    return make_conninfo(**parts)  # handles quoting of special characters in passwords


class Database:
    def __init__(self, schema: str):
        self.schema = schema
        self.pool: AsyncConnectionPool | None = None
        self.ready = False

    async def connect(self, attempts: int = 30) -> None:
        """Open the pool, wait for PostgreSQL to accept connections, then migrate.

        Retrying here (instead of crashing) means pods don't CrashLoop while the
        database is still starting; the readiness probe keeps traffic away meanwhile.
        """
        self.pool = AsyncConnectionPool(
            _conninfo(),
            min_size=1,
            max_size=int(os.getenv("DB_POOL_MAX", "10")),
            open=False,
            kwargs={"autocommit": True, "row_factory": dict_row},
        )
        await self.pool.open(wait=False)
        for attempt in range(1, attempts + 1):
            try:
                await self.ping()
                break
            except Exception as exc:  # noqa: BLE001
                log(logging.WARNING, "database not ready yet", attempt=attempt, error=str(exc))
                if attempt == attempts:
                    raise
                await asyncio.sleep(min(2 * attempt, 5))
        async with self.pool.connection() as conn:
            # Simple, idempotent migrations. Bigger teams use Alembic / Flyway / Atlas.
            await conn.execute(self.schema)
        self.ready = True
        log(logging.INFO, "database connected and migrated")

    async def ping(self) -> None:
        assert self.pool is not None
        async with self.pool.connection(timeout=3) as conn:
            await conn.execute("SELECT 1")

    async def close(self) -> None:
        if self.pool is not None:
            await self.pool.close()
