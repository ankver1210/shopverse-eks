'use strict';
/**
 * Shared "golden path" observability for every Node.js service in ShopVerse.
 *
 *  - Structured JSON logs (pino) that CloudWatch / Loki / any log tool can parse
 *  - Prometheus metrics with the SAME names used by the Go and Python services,
 *    so one Grafana dashboard works for all 10 services
 *  - X-Request-ID propagation so you can follow one request across services
 *  - Chaos injection (random errors / latency) for the hands-on labs
 */
const crypto = require('node:crypto');
const os = require('node:os');
const pino = require('pino');
const client = require('prom-client');

const SERVICE_NAME = process.env.SERVICE_NAME || 'unknown-service';
const APP_VERSION = process.env.APP_VERSION || 'dev';
const POD_NAME = process.env.POD_NAME || os.hostname();

const logger = pino({
  level: process.env.LOG_LEVEL || 'info',
  base: { service: SERVICE_NAME, version: APP_VERSION, pod: POD_NAME },
  timestamp: () => `,"timestamp":"${new Date().toISOString()}"`,
  messageKey: 'msg',
  formatters: { level: (label) => ({ level: label }) },
});

// ---------------------------------------------------------------- metrics
const register = new client.Registry();
client.collectDefaultMetrics({ register });

const httpRequests = new client.Counter({
  name: 'http_requests_total',
  help: 'Total HTTP requests handled',
  labelNames: ['method', 'route', 'status_code'],
  registers: [register],
});

const httpDuration = new client.Histogram({
  name: 'http_request_duration_seconds',
  help: 'HTTP request latency in seconds',
  labelNames: ['method', 'route', 'status_code'],
  buckets: [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10],
  registers: [register],
});

const buildInfo = new client.Gauge({
  name: 'app_build_info',
  help: 'Build information (always 1)',
  labelNames: ['version'],
  registers: [register],
});
buildInfo.set({ version: APP_VERSION }, 1);

const SKIP_PATHS = new Set(['/healthz', '/readyz', '/metrics']);

/** Express middleware: request id + access log + RED metrics. */
function requestObservability() {
  return (req, res, next) => {
    const start = process.hrtime.bigint();
    const requestId = req.get('x-request-id') || crypto.randomUUID();
    req.id = requestId;
    req.log = logger.child({ request_id: requestId });
    res.setHeader('x-request-id', requestId);
    res.setHeader('x-service-version', `${SERVICE_NAME}@${APP_VERSION}`);

    res.on('finish', () => {
      const seconds = Number(process.hrtime.bigint() - start) / 1e9;
      // Use the matched route template (e.g. /cart/items/:productId) to keep
      // metric cardinality low. Unmatched requests are grouped as "unmatched".
      const route = req.metricRoute || (req.route ? `${req.baseUrl || ''}${req.route.path}` : 'unmatched');
      const labels = { method: req.method, route, status_code: String(res.statusCode) };
      httpRequests.inc(labels);
      httpDuration.observe(labels, seconds);
      if (!SKIP_PATHS.has(req.path)) {
        const level = res.statusCode >= 500 ? 'error' : res.statusCode >= 400 ? 'warn' : 'info';
        req.log[level]({
          method: req.method,
          path: req.originalUrl,
          status: res.statusCode,
          duration_ms: Math.round(seconds * 1000),
        }, 'request completed');
      }
    });
    next();
  };
}

/**
 * Chaos engineering middleware (used in the labs).
 *   CHAOS_ERROR_RATE=0.2   -> 20% of requests fail with HTTP 500
 *   CHAOS_LATENCY_MS=800   -> add 800ms delay to every request
 * Health and metrics endpoints are never affected.
 */
function chaos() {
  const errorRate = parseFloat(process.env.CHAOS_ERROR_RATE || '0');
  const latencyMs = parseInt(process.env.CHAOS_LATENCY_MS || '0', 10);
  if (errorRate <= 0 && latencyMs <= 0) return (_req, _res, next) => next();
  logger.warn({ errorRate, latencyMs }, 'CHAOS MODE ENABLED');
  return (req, res, next) => {
    if (SKIP_PATHS.has(req.path)) return next();
    const proceed = () => {
      if (errorRate > 0 && Math.random() < errorRate) {
        req.metricRoute = 'chaos';
        return res.status(500).json({ error: 'chaos: injected failure', service: SERVICE_NAME });
      }
      return next();
    };
    if (latencyMs > 0) setTimeout(proceed, latencyMs);
    else proceed();
  };
}

async function metricsHandler(_req, res) {
  res.set('Content-Type', register.contentType);
  res.end(await register.metrics());
}

/** Body sent by /healthz and /readyz on every service. */
function healthBody(status, extra = {}) {
  return { status, service: SERVICE_NAME, version: APP_VERSION, pod: POD_NAME, ...extra };
}

/**
 * Graceful shutdown: on SIGTERM (sent by Kubernetes during rolling updates,
 * scale-down and node drains) stop accepting new connections, let in-flight
 * requests finish, close dependencies, then exit.
 */
function gracefulShutdown(server, { onShutdown = async () => {}, state } = {}) {
  let shuttingDown = false;
  const shutdown = async (signal) => {
    if (shuttingDown) return;
    shuttingDown = true;
    if (state) state.shuttingDown = true; // readiness probe starts failing
    logger.info({ signal }, 'shutdown signal received, draining connections');
    const force = setTimeout(() => {
      logger.error('forced shutdown after timeout');
      process.exit(1);
    }, 25_000);
    force.unref();
    server.close(async () => {
      try {
        await onShutdown();
      } catch (err) {
        logger.error({ err }, 'error during shutdown');
      }
      logger.info('shutdown complete');
      process.exit(0);
    });
    server.closeIdleConnections?.();
  };
  process.on('SIGTERM', () => shutdown('SIGTERM'));
  process.on('SIGINT', () => shutdown('SIGINT'));
}

module.exports = {
  SERVICE_NAME,
  APP_VERSION,
  POD_NAME,
  logger,
  register,
  client,
  requestObservability,
  chaos,
  metricsHandler,
  healthBody,
  gracefulShutdown,
};
