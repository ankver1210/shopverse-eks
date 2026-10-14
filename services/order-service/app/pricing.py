"""Pure pricing rules (no I/O) — easy to unit test."""
FREE_SHIPPING_THRESHOLD_CENTS = 10_000  # free shipping on orders of $100 or more
SHIPPING_FEE_CENTS = 799


def compute_totals(lines: list[dict]) -> dict:
    subtotal = sum(line["price_cents"] * line["quantity"] for line in lines)
    shipping = 0 if subtotal >= FREE_SHIPPING_THRESHOLD_CENTS else SHIPPING_FEE_CENTS
    return {
        "subtotal_cents": subtotal,
        "shipping_cents": shipping,
        "total_cents": subtotal + shipping,
        "item_count": sum(line["quantity"] for line in lines),
    }
