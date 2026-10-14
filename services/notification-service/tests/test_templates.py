import pytest

from app.templates import render


def test_order_confirmed_message():
    subject, body = render("order.confirmed", {
        "order_id": "1234567890abcdef", "total_cents": 12345, "item_count": 3,
        "shipping_address": "1 Kubernetes Way",
    })
    assert subject == "Order #12345678 confirmed"
    assert "$123.45" in body and "3 item(s)" in body


def test_unknown_events_are_ignored():
    assert render("inventory.restocked", {}) is None


def test_malformed_event_raises_so_it_goes_to_the_dlq():
    with pytest.raises(KeyError):
        render("order.confirmed", {"order_id": "abc"})  # missing total_cents
