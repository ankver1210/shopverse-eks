# syntax=docker/dockerfile:1
# ---------------------------------------------------------------------------
# Multi-stage build:
#   builder -> installs dependencies into a virtualenv
#   test    -> runs pytest (CI builds this with: docker build --target test .)
#   runtime -> slim image with only the venv + app code, non-root user
# ---------------------------------------------------------------------------
ARG PYTHON_IMAGE=python:3.13-slim

FROM ${PYTHON_IMAGE} AS base
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /app

FROM base AS builder
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:${PATH}"
COPY requirements.txt .
RUN pip install -r requirements.txt

FROM builder AS test
COPY requirements-dev.txt .
RUN pip install -r requirements-dev.txt
COPY . .
RUN python -m pytest -q

FROM base AS runtime
ARG APP_VERSION=dev
ENV PATH="/opt/venv/bin:${PATH}" \
    APP_VERSION=${APP_VERSION} \
    PORT=8080
RUN groupadd --gid 10001 app && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin app
COPY --from=builder /opt/venv /opt/venv
COPY app ./app
USER 10001:10001
EXPOSE 8080
CMD ["python", "-m", "app.main"]
