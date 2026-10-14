from app.pricing import SHIPPING_FEE_CENTS, compute_totals


def test_small_order_pays_shipping():
    totals = compute_totals([{"price_cents": 1999, "quantity": 2}])
    assert totals["subtotal_cents"] == 3998
    assert totals["shipping_cents"] == SHIPPING_FEE_CENTS
    assert totals["total_cents"] == 3998 + SHIPPING_FEE_CENTS
    assert totals["item_count"] == 2


def test_free_shipping_from_100_dollars():
    totals = compute_totals([{"price_cents": 5000, "quantity": 2}])
    assert totals["shipping_cents"] == 0
    assert totals["total_cents"] == 10_000
