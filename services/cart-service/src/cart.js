'use strict';
/** Pure cart logic (no I/O) so it is easy to unit test. */
const MAX_QTY_PER_ITEM = 10;

function clampQuantity(quantity) {
  return Math.max(0, Math.min(MAX_QTY_PER_ITEM, Math.trunc(quantity)));
}

function cartTotals(items) {
  const itemCount = items.reduce((sum, i) => sum + i.quantity, 0);
  const subtotal = items.reduce((sum, i) => sum + (i.price_cents ?? 0) * i.quantity, 0);
  return {
    items: items.map((i) => ({ ...i, line_total_cents: (i.price_cents ?? 0) * i.quantity })),
    item_count: itemCount,
    subtotal_cents: subtotal,
  };
}

module.exports = { MAX_QTY_PER_ITEM, clampQuantity, cartTotals };
