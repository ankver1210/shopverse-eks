'use strict';
/**
 * ShopVerse Cart Service (Node.js + Redis)
 * ----------------------------------------
 * Stores each user's shopping cart as a Redis hash:  cart:<userId> -> { productId: quantity }
 * Product names/prices are always looked up live from product-service, so the cart
 * never shows stale prices.
 *
 * Identity comes from the x-user-id header set by the API gateway after JWT verification.
 */
const express = require('express');
const { Redis } = require('ioredis');
const obs = require('./lib/observability');
const { cartTotals, clampQuantity, MAX_QTY_PER_ITEM } = require('./cart');

const { logger, client, register } = obs;
const PORT = parseInt(process.env.PORT || '8080', 10);
const PRODUCT_SERVICE_URL = process.env.PRODUCT_SERVICE_URL || 'http://product-service';
const CART_TTL_SECONDS = 7 * 24 * 3600;

const redis = new Redis({
  host: process.env.REDIS_HOST || 'localhost',
  port: parseInt(process.env.REDIS_PORT || '6379', 10),
  password: process.env.REDIS_PASSWORD || undefined,
  maxRetriesPerRequest: 2,
  enableOfflineQueue: false, // fail fast instead of queueing when Redis is down
  retryStrategy: (times) => Math.min(times * 200, 3000),
});
redis.on('error', (err) => logger.error({ err: err.message }, 'redis error'));
redis.on('ready', () => logger.info('connected to redis'));

const cartOps = new client.Counter({
  name: 'cart_operations_total',
  help: 'Cart operations by type',
  labelNames: ['operation'],
  registers: [register],
});

const key = (userId) => `cart:${userId}`;

async function fetchProducts(ids, requestId) {
  const url = `${PRODUCT_SERVICE_URL}/products/batch?ids=${ids.join(',')}`;
  const res = await fetch(url, { signal: AbortSignal.timeout(3000), headers: { 'x-request-id': requestId } });
  if (!res.ok) throw new Error(`product-service responded ${res.status}`);
  const body = await res.json();
  return body.products || [];
}

async function buildCart(userId, requestId) {
  const raw = await redis.hgetall(key(userId));
  const ids = Object.keys(raw);
  if (ids.length === 0) return { user_id: userId, items: [], item_count: 0, subtotal_cents: 0 };
  let products = [];
  let degraded = false;
  try {
    products = await fetchProducts(ids, requestId);
  } catch (err) {
    degraded = true; // still return the cart, just without live product details
    logger.warn({ err: err.message, request_id: requestId }, 'product-service unavailable, returning degraded cart');
  }
  const byId = Object.fromEntries(products.map((p) => [String(p.id), p]));
  const items = ids.map((id) => {
    const p = byId[id];
    return {
      product_id: Number(id),
      quantity: Number(raw[id]),
      name: p ? p.name : `Product #${id}`,
      emoji: p ? p.emoji : '📦',
      price_cents: p ? p.price_cents : null,
    };
  });
  return { user_id: userId, ...cartTotals(items), degraded };
}

const app = express();
const state = { shuttingDown: false };
app.disable('x-powered-by');
app.use(obs.requestObservability());
app.use(express.json({ limit: '32kb' }));

app.get('/healthz', (_req, res) => res.json(obs.healthBody('ok')));
app.get('/readyz', async (_req, res) => {
  if (state.shuttingDown) return res.status(503).json(obs.healthBody('shutting_down'));
  try {
    await redis.ping();
    return res.json(obs.healthBody('ready', { checks: { redis: 'ok' } }));
  } catch (err) {
    return res.status(503).json(obs.healthBody('not_ready', { checks: { redis: err.message } }));
  }
});
app.get('/metrics', obs.metricsHandler);
app.use(obs.chaos());

// Every cart route needs the identity forwarded by the gateway.
app.use('/cart', (req, res, next) => {
  const userId = req.get('x-user-id');
  if (!userId) return res.status(401).json({ error: 'missing user identity' });
  req.userId = userId;
  return next();
});

app.get('/cart', async (req, res) => {
  cartOps.inc({ operation: 'view' });
  res.json(await buildCart(req.userId, req.id));
});

app.post('/cart/items', async (req, res) => {
  const productId = Number(req.body?.product_id);
  const quantity = Number(req.body?.quantity ?? 1);
  if (!Number.isInteger(productId) || productId <= 0 || !Number.isInteger(quantity) || quantity <= 0) {
    return res.status(400).json({ error: 'product_id and quantity must be positive integers' });
  }
  // Validate the product exists before adding it.
  try {
    const products = await fetchProducts([productId], req.id);
    if (products.length === 0) return res.status(400).json({ error: `product ${productId} does not exist` });
  } catch (err) {
    return res.status(503).json({ error: 'product-service unavailable, try again shortly' });
  }
  const current = Number(await redis.hget(key(req.userId), String(productId))) || 0;
  const next = clampQuantity(current + quantity);
  await redis.multi()
    .hset(key(req.userId), String(productId), next)
    .expire(key(req.userId), CART_TTL_SECONDS)
    .exec();
  cartOps.inc({ operation: 'add' });
  return res.status(201).json(await buildCart(req.userId, req.id));
});

app.put('/cart/items/:productId', async (req, res) => {
  const quantity = Number(req.body?.quantity);
  if (!Number.isInteger(quantity) || quantity < 0) {
    return res.status(400).json({ error: `quantity must be an integer between 0 and ${MAX_QTY_PER_ITEM}` });
  }
  if (quantity === 0) await redis.hdel(key(req.userId), req.params.productId);
  else await redis.hset(key(req.userId), req.params.productId, clampQuantity(quantity));
  cartOps.inc({ operation: 'update' });
  res.json(await buildCart(req.userId, req.id));
});

app.delete('/cart/items/:productId', async (req, res) => {
  await redis.hdel(key(req.userId), req.params.productId);
  cartOps.inc({ operation: 'remove' });
  res.json(await buildCart(req.userId, req.id));
});

app.delete('/cart', async (req, res) => {
  await redis.del(key(req.userId));
  cartOps.inc({ operation: 'clear' });
  res.status(204).end();
});

// Express 5 forwards async errors here automatically.
app.use((err, req, res, _next) => {
  req.log?.error({ err: err.message }, 'unhandled error');
  if (res.headersSent) return res.destroy();
  const status = /Stream isn't writeable|Connection is closed|ECONNREFUSED/.test(err.message) ? 503 : 500;
  return res.status(status).json({ error: status === 503 ? 'cart storage unavailable' : 'internal error' });
});

const server = app.listen(PORT, () => logger.info({ port: PORT }, 'cart-service listening'));
server.keepAliveTimeout = 65_000;
obs.gracefulShutdown(server, { state, onShutdown: () => redis.quit() });
