// k6 load test: simulates real shoppers (browse -> recommendations -> cart -> checkout).
// Run it inside the cluster with:  ./scripts/07-run-load-test.sh
// Then watch Grafana and `kubectl -n shopverse get hpa -w` to see autoscaling happen.
import http from 'k6/http';
import { check, group, sleep } from 'k6';

const BASE = __ENV.BASE_URL || 'http://api-gateway';
const VUS = parseInt(__ENV.VUS || '20', 10);
const DURATION = __ENV.DURATION || '5m';
// Products with plenty of stock (scarce ones are left alone so the UI demo still works).
const PRODUCTS = [1, 3, 4, 5, 6, 7, 8, 10, 11];

export const options = {
  scenarios: {
    shoppers: {
      executor: 'ramping-vus',
      startVUs: 1,
      stages: [
        { duration: '1m', target: VUS },   // ramp up
        { duration: DURATION, target: VUS }, // steady load
        { duration: '30s', target: 0 },    // ramp down
      ],
      gracefulRampDown: '10s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],      // less than 5% errors
    http_req_duration: ['p(95)<1500'],   // 95% of requests under 1.5s
    checks: ['rate>0.95'],
  },
};

const JSON_HEADERS = { 'Content-Type': 'application/json' };
let token = null; // each virtual user keeps its own login

function login() {
  const email = `k6-vu${__VU}-${Date.now()}@example.com`;
  const res = http.post(`${BASE}/api/auth/register`,
    JSON.stringify({ email, password: 'load-test-password', name: `Load Tester ${__VU}` }),
    { headers: JSON_HEADERS, tags: { name: 'register' } });
  check(res, { 'registered (201)': (r) => r.status === 201 });
  return res.status === 201 ? res.json('access_token') : null;
}

export default function () {
  if (!token) token = login();
  const auth = { headers: { ...JSON_HEADERS, Authorization: `Bearer ${token}` } };
  const productId = PRODUCTS[Math.floor(Math.random() * PRODUCTS.length)];

  group('browse', () => {
    const list = http.get(`${BASE}/api/products`, { tags: { name: 'products' } });
    check(list, { 'products listed': (r) => r.status === 200 });
    http.get(`${BASE}/api/inventory?ids=${PRODUCTS.join(',')}`, { tags: { name: 'inventory' } });
    const recs = http.get(`${BASE}/api/recommendations/${productId}`, { tags: { name: 'recommendations' } });
    check(recs, { 'recommendations ok': (r) => r.status === 200 });
  });
  sleep(Math.random() * 2);

  if (!token) return;
  group('cart', () => {
    const add = http.post(`${BASE}/api/cart/items`, JSON.stringify({ product_id: productId, quantity: 1 }),
      { ...auth, tags: { name: 'add-to-cart' } });
    check(add, { 'added to cart': (r) => r.status === 201 });
  });
  sleep(Math.random());

  // About 1 in 3 iterations ends with a purchase (10% of those with a declined card).
  if (Math.random() < 0.33) {
    group('checkout', () => {
      const method = Math.random() < 0.1 ? 'tok_declined' : 'tok_visa';
      const order = http.post(`${BASE}/api/orders`,
        JSON.stringify({ payment_method: method, shipping_address: '1 Load Test Lane, Seattle' }),
        { ...auth, tags: { name: 'checkout' }, responseCallback: http.expectedStatuses(201, 402) });
      check(order, { 'checkout completed': (r) => r.status === 201 || r.status === 402 });
    });
  }
  sleep(1 + Math.random() * 2);
}
