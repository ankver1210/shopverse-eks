'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { cartTotals, clampQuantity, MAX_QTY_PER_ITEM } = require('../src/cart');

test('cart totals sum line items', () => {
  const totals = cartTotals([
    { product_id: 1, quantity: 2, price_cents: 1500 },
    { product_id: 2, quantity: 1, price_cents: 999 },
  ]);
  assert.strictEqual(totals.item_count, 3);
  assert.strictEqual(totals.subtotal_cents, 3999);
  assert.strictEqual(totals.items[0].line_total_cents, 3000);
});

test('quantity is clamped to the per-item maximum', () => {
  assert.strictEqual(clampQuantity(50), MAX_QTY_PER_ITEM);
  assert.strictEqual(clampQuantity(-3), 0);
  assert.strictEqual(clampQuantity(4), 4);
});
