'use strict';
/**
 * ShopVerse API Gateway
 * ---------------------
 * The single entry point for all /api/* traffic coming from the ALB.
 *
 *  - Routes requests to the right backend microservice
 *  - Verifies JWTs and forwards the trusted user identity (x-user-id) downstream
 *  - Strips spoofed identity headers coming from the internet
 *  - Rate limits per client IP
 *  - Circuit breaker per upstream (fail fast when a service is down)
 *  - /api/status aggregates the health of every service (used by the UI)
 */
const express = require('express');
const jwt = require('jsonwebtoken');
const obs = require('./lib/observability');
const { forward } = require('./lib/proxy');
const { CircuitBreaker, RateLimiter } = require('./lib/resilience');

const { logger, client, register } = obs;
const PORT = parseInt(process.env.PORT || '8080', 10);
const JWT_SECRET = process.env.JWT_SECRET;
const UPSTREAM_TIMEOUT_MS = parseInt(process.env.UPSTREAM_TIMEOUT_MS || '10000', 10);

if (!JWT_SECRET) {
  logger.fatal('JWT_SECRET environment variable is required');
  process.exit(1);
}

const upstreams = {
  user: process.env.USER_SERVICE_URL || 'http://user-service',
  product: process.env.PRODUCT_SERVICE_URL || 'http://product-service',
  cart: process.env.CART_SERVICE_URL || 'http://cart-service',
  order: process.env.ORDER_SERVICE_URL || 'http://order-service',
  inventory: process.env.INVENTORY_SERVICE_URL || 'http://inventory-service',
  payment: process.env.PAYMENT_SERVICE_URL || 'http://payment-service',
  recommendation: process.env.RECOMMENDATION_SERVICE_URL || 'http://recommendation-service',
  notification: process.env.NOTIFICATION_SERVICE_URL || 'http://notification-service',
};

/**
 * Public routing table. Note payment-service is intentionally NOT exposed:
 * only order-service may call it (also enforced by a NetworkPolicy).
 */
const ROUTES = [
  { prefix: '/api/auth', upstream: 'user', auth: false },
  { prefix: '/api/users', upstream: 'user', auth: true },
  { prefix: '/api/products', upstream: 'product', auth: false },
  { prefix: '/api/categories', upstream: 'product', auth: false },
  { prefix: '/api/inventory', upstream: 'inventory', auth: false, methods: ['GET'] },
  { prefix: '/api/recommendations', upstream: 'recommendation', auth: false },
  { prefix: '/api/cart', upstream: 'cart', auth: true },
  { prefix: '/api/orders', upstream: 'order', auth: true },
  { prefix: '/api/notifications', upstream: 'notification', auth: true },
];

// ---------------------------------------------------------------- metrics
const upstreamRequests = new client.Counter({
  name: 'gateway_upstream_requests_total',
  help: 'Requests proxied to upstream services',
  labelNames: ['upstream', 'status_code'],
  registers: [register],
});
const circuitState = new client.Gauge({
  name: 'gateway_circuit_breaker_state',
  help: 'Circuit breaker state per upstream (0=closed, 1=half-open, 2=open)',
  labelNames: ['upstream'],
  registers: [register],
});
const rateLimited = new client.Counter({
  name: 'gateway_rate_limited_total',
  help: 'Requests rejected by the rate limiter',
  registers: [register],
});

const STATE_VALUE = { CLOSED: 0, HALF_OPEN: 1, OPEN: 2 };
const breakers = Object.fromEntries(Object.keys(upstreams).map((name) => {
  circuitState.set({ upstream: name }, 0);
  return [name, new CircuitBreaker(name, {
    failureThreshold: parseInt(process.env.CIRCUIT_FAILURE_THRESHOLD || '5', 10),
    resetTimeoutMs: parseInt(process.env.CIRCUIT_RESET_TIMEOUT_MS || '10000', 10),
    onStateChange: (upstream, from, to) => {
      circuitState.set({ upstream }, STATE_VALUE[to]);
      logger.warn({ upstream, from, to }, 'circuit breaker state changed');
    },
  })];
}));

const limiter = new RateLimiter({
  limit: parseInt(process.env.RATE_LIMIT_PER_MINUTE || '600', 10),
  windowMs: 60_000,
});

// ---------------------------------------------------------------- app
const app = express();
const state = { shuttingDown: false };
app.disable('x-powered-by');
app.set('trust proxy', 1); // trust exactly one proxy hop (the AWS ALB) for the client IP

app.use(obs.requestObservability());

app.get('/healthz', (_req, res) => res.json(obs.healthBody('ok')));
app.get('/readyz', (_req, res) => {
  if (state.shuttingDown) return res.status(503).json(obs.healthBody('shutting_down'));
  return res.json(obs.healthBody('ready'));
});
app.get('/metrics', obs.metricsHandler);

app.use((_req, res, next) => {
  res.setHeader('x-content-type-options', 'nosniff');
  res.setHeader('x-frame-options', 'DENY');
  res.setHeader('referrer-policy', 'strict-origin-when-cross-origin');
  next();
});
app.use(obs.chaos());

// Rate limiting for all API traffic
app.use('/api', (req, res, next) => {
  const result = limiter.take(req.ip);
  res.setHeader('x-ratelimit-remaining', String(result.remaining));
  if (!result.allowed) {
    rateLimited.inc();
    res.setHeader('retry-after', String(Math.ceil(result.resetMs / 1000)));
    req.metricRoute = '/api (rate-limited)';
    return res.status(429).json({ error: 'Too many requests, slow down.' });
  }
  return next();
});

// Aggregated platform status (used by the "Platform" page in the UI)
app.get('/api/status', async (req, res) => {
  const checks = await Promise.all(Object.entries(upstreams).map(async ([name, base]) => {
    const started = Date.now();
    try {
      const r = await fetch(`${base}/readyz`, {
        signal: AbortSignal.timeout(2000),
        headers: { 'x-request-id': req.id },
      });
      const body = await r.json().catch(() => ({}));
      return {
        name: `${name}-service`,
        status: r.ok ? 'up' : 'degraded',
        http_status: r.status,
        version: body.version || 'unknown',
        pod: body.pod || 'unknown',
        checks: body.checks || {},
        latency_ms: Date.now() - started,
        circuit: breakers[name].state,
      };
    } catch (err) {
      return {
        name: `${name}-service`, status: 'down', error: err.message,
        latency_ms: Date.now() - started, circuit: breakers[name].state,
      };
    }
  }));
  res.json({ gateway: obs.healthBody('up'), services: checks, checked_at: new Date().toISOString() });
});

function authenticate(req) {
  const header = req.get('authorization') || '';
  const [scheme, token] = header.split(' ');
  if (scheme !== 'Bearer' || !token) return null;
  try {
    return jwt.verify(token, JWT_SECRET, { algorithms: ['HS256'], issuer: 'shopverse-user-service' });
  } catch {
    return null;
  }
}

for (const route of ROUTES) {
  app.use(route.prefix, async (req, res) => {
    req.metricRoute = route.prefix;
    if (route.methods && !route.methods.includes(req.method)) {
      return res.status(405).json({ error: 'Method not allowed' });
    }

    // Never trust identity headers from the internet.
    const identity = {};
    delete req.headers['x-user-id'];
    delete req.headers['x-user-email'];
    delete req.headers['x-user-name'];

    if (route.auth) {
      const claims = authenticate(req);
      if (!claims) return res.status(401).json({ error: 'Authentication required' });
      identity['x-user-id'] = String(claims.sub);
      identity['x-user-email'] = claims.email || '';
      identity['x-user-name'] = encodeURIComponent(claims.name || '');
    }

    const breaker = breakers[route.upstream];
    if (!breaker.canRequest()) {
      upstreamRequests.inc({ upstream: route.upstream, status_code: 'circuit_open' });
      return res.status(503).json({ error: `${route.upstream}-service is temporarily unavailable (circuit open)` });
    }

    try {
      const status = await forward(req, res, {
        target: upstreams[route.upstream],
        path: req.originalUrl.replace(/^\/api/, ''),
        headers: { ...identity, 'x-request-id': req.id },
        timeoutMs: UPSTREAM_TIMEOUT_MS,
      });
      upstreamRequests.inc({ upstream: route.upstream, status_code: String(status) });
      if (status >= 500) breaker.onFailure(); else breaker.onSuccess();
    } catch (err) {
      breaker.onFailure();
      upstreamRequests.inc({ upstream: route.upstream, status_code: 'error' });
      req.log.error({ err: err.message, upstream: route.upstream }, 'upstream request failed');
      if (!res.headersSent) {
        const timedOut = err.code === 'ETIMEDOUT';
        res.status(timedOut ? 504 : 502).json({
          error: timedOut ? `${route.upstream}-service timed out` : `${route.upstream}-service is unreachable`,
        });
      } else {
        res.destroy();
      }
    }
    return undefined;
  });
}

app.use('/api', (req, res) => {
  req.metricRoute = '/api (not found)';
  res.status(404).json({ error: 'Route not found' });
});

const server = app.listen(PORT, () => {
  logger.info({ port: PORT, upstreams }, 'api-gateway listening');
});
// Keep idle connections alive longer than the ALB idle timeout (60s) to avoid 502s.
server.keepAliveTimeout = 65_000;
server.headersTimeout = 66_000;

obs.gracefulShutdown(server, { state });
