"""Turn domain events into human-friendly notifications (pure functions, unit tested)."""
from __future__ import annotations


def _money(cents: int) -> str:
    return f"${cents / 100:,.2f}"


def render(event_type: str, data: dict) -> tuple[str, str] | None:
    """Return (subject, body) for an event, or None if we don't notify on it."""
    if event_type == "user.registered":
        return (
            f"Welcome to ShopVerse, {data.get('name', 'friend')}!",
            "Your account is ready. Every page you open is served by microservices on Amazon EKS.",
        )
    if event_type == "order.confirmed":
        short_id = str(data["order_id"])[:8]
        items = data.get("item_count", 0)
        return (
            f"Order #{short_id} confirmed",
            f"We received your payment of {_money(data['total_cents'])} for {items} item(s). "
            f"It will ship to: {data.get('shipping_address', 'your address')}.",
        )
    if event_type == "order.failed":
        short_id = str(data["order_id"])[:8]
        return (
            f"Order #{short_id} could not be completed",
            f"Reason: {data.get('reason', 'unknown')}. You have not been charged.",
        )
    return None
