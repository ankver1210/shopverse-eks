'use strict';
/**
 * Resilience patterns used by the API gateway:
 *  - CircuitBreaker: stop hammering a failing service, fail fast, retry later
 *  - RateLimiter:    protect the platform from a single noisy client
 */

class CircuitBreaker {
  /**
   * CLOSED    -> requests flow normally
   * OPEN      -> requests fail fast with 503 (upstream is considered down)
   * HALF_OPEN -> one trial request is allowed; success closes, failure re-opens
   */
  constructor(name, { failureThreshold = 5, resetTimeoutMs = 10_000, onStateChange = () => {} } = {}) {
    this.name = name;
    this.failureThreshold = failureThreshold;
    this.resetTimeoutMs = resetTimeoutMs;
    this.onStateChange = onStateChange;
    this.state = 'CLOSED';
    this.failures = 0;
    this.openedAt = 0;
    this.trialInFlight = false;
  }

  canRequest() {
    if (this.state === 'CLOSED') return true;
    if (this.state === 'OPEN' && Date.now() - this.openedAt >= this.resetTimeoutMs) {
      this.#transition('HALF_OPEN');
    }
    if (this.state === 'HALF_OPEN' && !this.trialInFlight) {
      this.trialInFlight = true;
      return true;
    }
    return false;
  }

  onSuccess() {
    this.failures = 0;
    this.trialInFlight = false;
    if (this.state !== 'CLOSED') this.#transition('CLOSED');
  }

  onFailure() {
    this.failures += 1;
    this.trialInFlight = false;
    if (this.state === 'HALF_OPEN' || this.failures >= this.failureThreshold) {
      this.openedAt = Date.now();
      if (this.state !== 'OPEN') this.#transition('OPEN');
    }
  }

  #transition(next) {
    const previous = this.state;
    this.state = next;
    this.onStateChange(this.name, previous, next);
  }
}

class RateLimiter {
  /** Fixed-window limiter: `limit` requests per `windowMs` per key (client IP). */
  constructor({ limit = 300, windowMs = 60_000 } = {}) {
    this.limit = limit;
    this.windowMs = windowMs;
    this.hits = new Map();
    this.sweeper = setInterval(() => this.#sweep(), windowMs);
    this.sweeper.unref();
  }

  /** @returns {{allowed: boolean, remaining: number, resetMs: number}} */
  take(key) {
    const now = Date.now();
    let entry = this.hits.get(key);
    if (!entry || now >= entry.resetAt) {
      entry = { count: 0, resetAt: now + this.windowMs };
      this.hits.set(key, entry);
    }
    entry.count += 1;
    return {
      allowed: entry.count <= this.limit,
      remaining: Math.max(0, this.limit - entry.count),
      resetMs: entry.resetAt - now,
    };
  }

  #sweep() {
    const now = Date.now();
    for (const [key, entry] of this.hits) if (now >= entry.resetAt) this.hits.delete(key);
  }
}

module.exports = { CircuitBreaker, RateLimiter };
