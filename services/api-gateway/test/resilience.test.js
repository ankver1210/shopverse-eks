'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { CircuitBreaker, RateLimiter } = require('../src/lib/resilience');

test('circuit breaker opens after threshold and recovers after timeout', async () => {
  const transitions = [];
  const cb = new CircuitBreaker('demo', {
    failureThreshold: 3, resetTimeoutMs: 50,
    onStateChange: (_n, from, to) => transitions.push(`${from}->${to}`),
  });
  for (let i = 0; i < 3; i += 1) { assert.ok(cb.canRequest()); cb.onFailure(); }
  assert.strictEqual(cb.state, 'OPEN');
  assert.strictEqual(cb.canRequest(), false, 'open circuit must fail fast');
  await new Promise((r) => setTimeout(r, 60));
  assert.ok(cb.canRequest(), 'one trial request allowed when half-open');
  assert.strictEqual(cb.canRequest(), false, 'only one trial at a time');
  cb.onSuccess();
  assert.strictEqual(cb.state, 'CLOSED');
  assert.deepStrictEqual(transitions, ['CLOSED->OPEN', 'OPEN->HALF_OPEN', 'HALF_OPEN->CLOSED']);
});

test('rate limiter blocks after limit', () => {
  const rl = new RateLimiter({ limit: 2, windowMs: 1000 });
  assert.ok(rl.take('1.2.3.4').allowed);
  assert.ok(rl.take('1.2.3.4').allowed);
  assert.strictEqual(rl.take('1.2.3.4').allowed, false);
  assert.ok(rl.take('5.6.7.8').allowed, 'limits are per client');
});
