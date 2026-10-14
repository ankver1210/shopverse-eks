# syntax=docker/dockerfile:1
# ---------------------------------------------------------------------------
# Multi-stage build:
#   deps    -> production dependencies only
#   test    -> runs unit tests (CI builds this with: docker build --target test .)
#   runtime -> small final image, runs as a non-root user
# ---------------------------------------------------------------------------
ARG NODE_IMAGE=node:24-alpine

FROM ${NODE_IMAGE} AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund

FROM ${NODE_IMAGE} AS test
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm test

FROM ${NODE_IMAGE} AS runtime
ARG APP_VERSION=dev
ENV NODE_ENV=production \
    APP_VERSION=${APP_VERSION} \
    PORT=8080
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY package.json ./
COPY src ./src
COPY public ./public
# Numeric UID so Kubernetes can verify runAsNonRoot (the "node" user is 1000).
USER 1000:1000
EXPOSE 8080
CMD ["node", "src/server.js"]
