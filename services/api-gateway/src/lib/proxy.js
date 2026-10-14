'use strict';
/**
 * Tiny, dependency-free reverse proxy built on node:http.
 * Streams request/response bodies (no buffering), supports timeouts and
 * keep-alive connection pooling to upstream services.
 */
const http = require('node:http');

const HOP_BY_HOP = new Set([
  'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
  'te', 'trailer', 'transfer-encoding', 'upgrade', 'host',
]);

const agent = new http.Agent({ keepAlive: true, maxSockets: 200, keepAliveMsecs: 10_000 });

/**
 * Forward `req` to `target` + `path`.
 * @returns {Promise<number>} resolves with the upstream status code, rejects on network error/timeout
 */
function forward(req, res, { target, path, headers = {}, timeoutMs = 10_000 }) {
  return new Promise((resolve, reject) => {
    const url = new URL(path, target);
    const outHeaders = {};
    for (const [key, value] of Object.entries(req.headers)) {
      if (!HOP_BY_HOP.has(key)) outHeaders[key] = value;
    }
    const clientIp = req.ip || req.socket.remoteAddress;
    outHeaders['x-forwarded-for'] = req.headers['x-forwarded-for']
      ? `${req.headers['x-forwarded-for']}, ${clientIp}` : clientIp;
    Object.assign(outHeaders, headers);

    const upstreamReq = http.request({
      protocol: url.protocol,
      hostname: url.hostname,
      port: url.port || 80,
      path: `${url.pathname}${url.search}`,
      method: req.method,
      headers: outHeaders,
      agent,
      timeout: timeoutMs,
    }, (upstreamRes) => {
      res.status(upstreamRes.statusCode);
      for (const [key, value] of Object.entries(upstreamRes.headers)) {
        if (!HOP_BY_HOP.has(key) && value !== undefined) res.setHeader(key, value);
      }
      upstreamRes.pipe(res);
      upstreamRes.on('end', () => resolve(upstreamRes.statusCode));
      upstreamRes.on('error', reject);
    });

    upstreamReq.on('timeout', () => upstreamReq.destroy(Object.assign(new Error('upstream timeout'), { code: 'ETIMEDOUT' })));
    upstreamReq.on('error', reject);
    // If the browser disconnects, stop work upstream too.
    res.on('close', () => { if (!res.writableFinished) upstreamReq.destroy(); });
    req.pipe(upstreamReq);
  });
}

module.exports = { forward };
