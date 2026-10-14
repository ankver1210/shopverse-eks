'use strict';
/**
 * ShopVerse Frontend
 * ------------------
 * Serves the single-page web app (public/). In EKS the ALB sends /api/* straight
 * to the api-gateway; the /api proxy below is only used when you run the frontend
 * on its own (docker compose, kubectl port-forward), so the UI works everywhere.
 */
const path = require('node:path');
const express = require('express');
const obs = require('./lib/observability');
const { forward } = require('./lib/proxy');

const { logger } = obs;
const PORT = parseInt(process.env.PORT || '8080', 10);
const API_GATEWAY_URL = process.env.API_GATEWAY_URL || 'http://api-gateway';
const PUBLIC_DIR = path.join(__dirname, '..', 'public');

const app = express();
const state = { shuttingDown: false };
app.disable('x-powered-by');
app.set('trust proxy', 1); // one hop: the AWS ALB
app.use(obs.requestObservability());

app.get('/healthz', (_req, res) => res.json(obs.healthBody('ok')));
app.get('/readyz', (_req, res) => (state.shuttingDown
  ? res.status(503).json(obs.healthBody('shutting_down'))
  : res.json(obs.healthBody('ready'))));
app.get('/metrics', obs.metricsHandler);

app.use((_req, res, next) => {
  res.setHeader('content-security-policy',
    "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'");
  res.setHeader('x-content-type-options', 'nosniff');
  res.setHeader('referrer-policy', 'strict-origin-when-cross-origin');
  next();
});
app.use(obs.chaos());

app.get('/config.json', (_req, res) => {
  res.json({
    version: obs.APP_VERSION,
    pod: obs.POD_NAME,
    environment: process.env.ENVIRONMENT || 'dev',
  });
});

app.use('/api', async (req, res) => {
  req.metricRoute = '/api (proxy)';
  try {
    await forward(req, res, { target: API_GATEWAY_URL, path: req.originalUrl, headers: { 'x-request-id': req.id } });
  } catch (err) {
    req.log.error({ err: err.message }, 'api-gateway unreachable');
    if (!res.headersSent) res.status(502).json({ error: 'api-gateway unreachable' });
  }
});

app.use(express.static(PUBLIC_DIR, { maxAge: '5m', index: 'index.html' }));

// Single-page-app fallback: every other GET returns index.html
app.use((req, res) => {
  if (req.method !== 'GET') return res.status(404).json({ error: 'not found' });
  req.metricRoute = '/*spa';
  return res.sendFile(path.join(PUBLIC_DIR, 'index.html'));
});

const server = app.listen(PORT, () => logger.info({ port: PORT, API_GATEWAY_URL }, 'frontend listening'));
server.keepAliveTimeout = 65_000;
server.headersTimeout = 66_000;
obs.gracefulShutdown(server, { state });
